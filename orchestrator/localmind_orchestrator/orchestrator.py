"""Main orchestrator state machine.

A graph run flows through one of two paths depending on classification:

::

    direct:  classify -> answer-direct -> end
    light:   classify -> spawn-one-worker -> end
    full:    classify -> plan -> await-confirm -> spawn-workers -> synthesize -> end

We model this with LangGraph's StateGraph so the structure is explicit,
but the heavy lifting (LLM calls, tool calls, chat publishing) all
happens inside node functions that are otherwise plain async code. That
keeps things testable without standing up a full graph executor.

For v0.3.0 every full run uses parallel workers with no inter-worker
dependencies. `PlannedWorker.depends_on` is reserved for v0.3.x.
"""
from __future__ import annotations

import asyncio
import logging
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Awaitable, Callable, Literal

from pydantic import BaseModel, Field

from .chat import ChatBus, ChatMessage
from .classifier import classify
from .ollama import OllamaClient
from .state import Graph, GraphStore
from .worker import Worker

logger = logging.getLogger(__name__)

# Slot 2 owns the actual implementations; we accept any object that
# satisfies the structural protocol used here. Importing the names
# (without using them at module scope) makes the wiring obvious.
try:  # pragma: no cover — import is opportunistic
    from .skills import load_skill  # type: ignore  # noqa: F401
    from .tools import MCPTools  # type: ignore  # noqa: F401
except Exception:
    pass

Mode = Literal["direct", "light", "full"]
Status = Literal["pending-confirm", "running", "done", "cancelled", "error"]

DIRECT_SYSTEM = (
    "You are @orchestrator in a group chat. Answer the user concisely "
    "(under 5 sentences). Do not call tools. Do not roleplay. "
    "Reply in plain prose, no preamble."
)

PLANNER_SYSTEM = (
    "You are @orchestrator. Decompose the user's request into 3-5 specialist "
    "subtasks that can run in parallel. For each subtask pick exactly one role "
    "from this list: researcher, reviewer, writer, coder, summarizer. "
    "Reply ONLY with a JSON array of objects, each with fields "
    '{"role": "...", "instruction": "..."}. No prose, no fences.'
)


class PlannedWorker(BaseModel):
    role: str
    name: str
    instruction: str
    depends_on: list[str] = Field(default_factory=list)


@dataclass
class GraphRun:
    graph_id: str
    user_query: str
    mode: Mode
    plan: list[PlannedWorker] | None = None
    status: Status = "running"
    final: str | None = None
    # Used by confirm_run / cancel_run to wake the coordinator task.
    confirm_event: asyncio.Event = field(default_factory=asyncio.Event)
    confirm_accepted: bool = False
    confirm_edits: list[PlannedWorker] | None = None
    cancelled: bool = False
    coordinator_task: asyncio.Task | None = None


class Orchestrator:
    """Owns all in-flight runs. One instance per FastAPI app."""

    def __init__(
        self,
        bus: ChatBus,
        store: GraphStore,
        ollama: OllamaClient,
        *,
        skills_loader: Callable[[str], Any] | None = None,
        mcp_tools: Any | None = None,
        classifier_model: str = "qwen2.5:3b",
        planner_model: str = "qwen2.5:7b",
        direct_model: str = "qwen2.5:7b",
        synthesizer_model: str = "qwen2.5:7b",
    ) -> None:
        self.bus = bus
        self.store = store
        self.ollama = ollama
        self.skills_loader = skills_loader
        self.mcp_tools = mcp_tools
        self.classifier_model = classifier_model
        self.planner_model = planner_model
        self.direct_model = direct_model
        self.synthesizer_model = synthesizer_model
        self._runs: dict[str, GraphRun] = {}

    # ------------------------------------------------------------------ public

    async def start_run(self, query: str) -> GraphRun:
        """Kick off a new graph. Returns immediately with mode + graph_id;
        the rest of the work runs in a background coordinator task.
        """
        graph_id = uuid.uuid4().hex[:12]
        # Classify first — we need the mode in the synchronous return.
        mode = await classify(query, self.ollama, model=self.classifier_model)
        run = GraphRun(graph_id=graph_id, user_query=query, mode=mode)
        run.status = "pending-confirm" if mode == "full" else "running"
        self._runs[graph_id] = run

        await self.store.record_graph(
            Graph(
                id=graph_id,
                user_query=query,
                mode=mode,
                status=run.status,
            )
        )
        # Echo the user's prompt as the first chat message so subscribers
        # see a complete transcript starting from turn 0.
        await self.bus.publish(
            graph_id, speaker="@user", body=query, kind="info"
        )

        run.coordinator_task = asyncio.create_task(
            self._coordinate(run), name=f"orchestrator:{graph_id}"
        )
        return run

    async def confirm_run(
        self,
        graph_id: str,
        accepted: bool,
        edits: list[PlannedWorker] | None = None,
    ) -> None:
        run = self._runs.get(graph_id)
        if run is None:
            raise KeyError(graph_id)
        run.confirm_accepted = accepted
        run.confirm_edits = edits
        run.confirm_event.set()

    async def cancel_run(self, graph_id: str) -> None:
        run = self._runs.get(graph_id)
        if run is None:
            return
        run.cancelled = True
        run.confirm_event.set()  # unblock any awaiter
        if run.coordinator_task is not None and not run.coordinator_task.done():
            run.coordinator_task.cancel()
        run.status = "cancelled"
        await self.store.update_status(graph_id, "cancelled")
        await self.bus.publish(
            graph_id,
            speaker="@orchestrator",
            body="Run cancelled.",
            kind="info",
        )
        await self.bus.cancel(graph_id)

    def get_run(self, graph_id: str) -> GraphRun | None:
        return self._runs.get(graph_id)

    # ------------------------------------------------------------- coordinator

    async def _coordinate(self, run: GraphRun) -> None:
        """Drive the run end-to-end. Catches exceptions so the task never dies silently."""
        try:
            if run.mode == "direct":
                await self._run_direct(run)
            elif run.mode == "light":
                await self._run_light(run)
            else:
                await self._run_full(run)
            # Only mark "done" if we actually completed normally — a rejected
            # confirm or a mid-flight cancel will already have set the status.
            if not run.cancelled and run.status not in {"cancelled", "error"}:
                run.status = "done"
                await self.store.update_status(run.graph_id, "done")
        except asyncio.CancelledError:
            logger.info("graph %s coordinator cancelled", run.graph_id)
            raise
        except Exception as exc:
            logger.exception("graph %s failed", run.graph_id)
            run.status = "error"
            await self.store.update_status(run.graph_id, "error")
            await self.bus.publish(
                run.graph_id,
                speaker="@orchestrator",
                body=f"Run errored: {exc}",
                kind="error",
            )
        finally:
            # Close subscribers when the run is fully done. cancel_run() may
            # have already done this; ChatBus.cancel() is idempotent.
            await self.bus.cancel(run.graph_id)

    # ---------------------------------------------------------------- direct

    async def _run_direct(self, run: GraphRun) -> None:
        t0 = time.monotonic()
        try:
            text = await self.ollama.generate(
                model=self.direct_model,
                prompt=run.user_query,
                system=DIRECT_SYSTEM,
                max_tokens=500,
            )
        except Exception as exc:
            text = f"(local model unavailable: {exc})"
        run.final = text.strip() or "(empty response)"
        await self.bus.publish(
            run.graph_id,
            speaker="@orchestrator",
            body=run.final,
            kind="final",
            meta={"latency_ms": int((time.monotonic() - t0) * 1000)},
        )

    # ----------------------------------------------------------------- light

    async def _run_light(self, run: GraphRun) -> None:
        # Single specialist worker. We default to "researcher" because most
        # light queries are search/read; the skill can override.
        role = "researcher"
        worker_name = f"@{role}-1"
        await self.bus.publish(
            run.graph_id,
            speaker="@orchestrator",
            body=f"Handing this off to {worker_name}.",
            kind="handoff",
            refs=[worker_name],
        )
        skill = self._load_skill(role)
        worker = Worker(
            name=worker_name,
            role=role,
            instruction=run.user_query,
            skill=skill,
            bus=self.bus,
            ollama=self.ollama,
            tools=self.mcp_tools,
            graph_id=run.graph_id,
        )
        result = await worker.run()
        run.final = result

    # ------------------------------------------------------------------ full

    async def _run_full(self, run: GraphRun) -> None:
        plan = await self._plan(run)
        run.plan = plan

        await self.bus.publish(
            run.graph_id,
            speaker="@orchestrator",
            body=self._format_plan(plan),
            kind="plan",
            meta={"plan": [p.model_dump() for p in plan]},
        )
        await self.bus.publish(
            run.graph_id,
            speaker="@orchestrator",
            body="Approve this plan? Reply y / n / edit.",
            kind="confirm-request",
            refs=["@user"],
        )

        # Wait for confirmation. The HTTP layer calls confirm_run() which
        # sets the event. cancel_run() also sets it so we can exit cleanly.
        await run.confirm_event.wait()
        if run.cancelled or not run.confirm_accepted:
            run.status = "cancelled"
            await self.store.update_status(run.graph_id, "cancelled")
            await self.bus.publish(
                run.graph_id,
                speaker="@orchestrator",
                body="Plan rejected; stopping.",
                kind="info",
            )
            return

        if run.confirm_edits:
            plan = run.confirm_edits
            run.plan = plan

        run.status = "running"
        await self.store.update_status(run.graph_id, "running")

        # Spawn workers in parallel. v0.3.0 ignores depends_on.
        workers = [self._build_worker(run.graph_id, p) for p in plan]
        results = await asyncio.gather(
            *(w.run() for w in workers), return_exceptions=True
        )
        # Stringify any exceptions so the synthesizer sees them as content.
        clean_results: list[tuple[str, str]] = []
        for w, r in zip(workers, results):
            if isinstance(r, Exception):
                clean_results.append((w.name, f"(worker errored: {r})"))
            else:
                clean_results.append((w.name, r))

        # Synthesizer turn — orchestrator itself reads the results and writes
        # the final answer. This is a regular Ollama generate call rather than
        # a Worker so the speaker is unambiguously @orchestrator.
        synthesis = await self._synthesize(run, clean_results)
        run.final = synthesis
        await self.bus.publish(
            run.graph_id,
            speaker="@orchestrator",
            body=synthesis,
            kind="final",
            refs=[name for name, _ in clean_results],
        )

    # ---------------------------------------------------------------- helpers

    def _load_skill(self, role: str) -> Any:
        """Defer to slot 2's loader. If unavailable, return a stub Skill."""
        if self.skills_loader is not None:
            try:
                return self.skills_loader(role)
            except Exception:
                logger.exception("skills_loader(%s) failed; using stub", role)
        return _StubSkill(role=role)

    def _build_worker(self, graph_id: str, planned: PlannedWorker) -> Worker:
        skill = self._load_skill(planned.role)
        return Worker(
            name=planned.name,
            role=planned.role,
            instruction=planned.instruction,
            skill=skill,
            bus=self.bus,
            ollama=self.ollama,
            tools=self.mcp_tools,
            graph_id=graph_id,
        )

    async def _plan(self, run: GraphRun) -> list[PlannedWorker]:
        """Ask the planner LLM for a JSON array of subtasks. Robust to noise."""
        try:
            raw = await self.ollama.generate(
                model=self.planner_model,
                prompt=f"User request: {run.user_query}",
                system=PLANNER_SYSTEM,
                max_tokens=512,
            )
        except Exception as exc:
            logger.warning("planner LLM unavailable, using stub plan: %s", exc)
            raw = ""
        plan = _parse_plan(raw)
        if not plan:
            # Fallback: a 3-worker generic plan that's always safe.
            plan = [
                PlannedWorker(role="researcher", name="@researcher-1",
                              instruction=f"Research the request: {run.user_query}"),
                PlannedWorker(role="researcher", name="@researcher-2",
                              instruction=f"Find a second perspective on: {run.user_query}"),
                PlannedWorker(role="reviewer", name="@reviewer-1",
                              instruction="Review the other workers' findings and surface gaps."),
            ]
        return plan

    def _format_plan(self, plan: list[PlannedWorker]) -> str:
        lines = ["Plan:"]
        for p in plan:
            lines.append(f"  - {p.name} ({p.role}): {p.instruction}")
        return "\n".join(lines)

    async def _synthesize(
        self,
        run: GraphRun,
        results: list[tuple[str, str]],
    ) -> str:
        joined = "\n\n".join(f"{name}:\n{body}" for name, body in results)
        prompt = (
            f"Original request: {run.user_query}\n\n"
            f"Worker outputs:\n{joined}\n\n"
            "Write the final answer for the user. Be concise and concrete."
        )
        try:
            return (
                await self.ollama.generate(
                    model=self.synthesizer_model,
                    prompt=prompt,
                    system="You are @orchestrator synthesising worker outputs into a final answer.",
                    max_tokens=800,
                )
            ).strip() or "(no synthesis produced)"
        except Exception as exc:
            return f"(synthesizer unavailable: {exc})\n\nRaw outputs:\n{joined}"


# ---------------------------------------------------------------------- helpers


@dataclass
class _StubSkill:
    """Used when slot 2's skills loader hasn't merged yet. Lets us run end-to-end."""
    role: str
    system_prompt: str = ""
    recommended_model: str = "qwen2.5:3b"

    def __post_init__(self) -> None:
        self.system_prompt = (
            f"You are a {self.role} agent. Do your job concisely and reply in chat form."
        )


def _parse_plan(text: str) -> list[PlannedWorker]:
    """Extract a list of PlannedWorker from the planner's raw reply."""
    import json
    import re

    if not text:
        return []
    # Strip code fences if the model added any.
    text = re.sub(r"^```(?:json)?", "", text.strip(), flags=re.IGNORECASE)
    text = re.sub(r"```\s*$", "", text.strip())
    # Find the first JSON array in the reply.
    m = re.search(r"\[.*\]", text, re.DOTALL)
    if not m:
        return []
    try:
        items = json.loads(m.group(0))
    except json.JSONDecodeError:
        return []
    if not isinstance(items, list):
        return []
    out: list[PlannedWorker] = []
    role_counts: dict[str, int] = {}
    for it in items:
        if not isinstance(it, dict):
            continue
        role = str(it.get("role", "researcher")).strip().lower() or "researcher"
        instr = str(it.get("instruction", "")).strip()
        if not instr:
            continue
        role_counts[role] = role_counts.get(role, 0) + 1
        name = f"@{role}-{role_counts[role]}"
        out.append(PlannedWorker(role=role, name=name, instruction=instr))
    return out
