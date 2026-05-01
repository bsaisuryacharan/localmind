"""Worker agent runtime.

A `Worker` is one named participant in the group chat. The orchestrator
constructs it with a `Skill` (slot 2) describing the system prompt,
recommended model, and allowed tools. The worker's `run()` coroutine:

1. Announces what it's about to do (`@worker info`).
2. Streams an Ollama chat completion against ``skill.system_prompt``.
3. Watches the stream for ``TOOL_CALL: {...}`` lines — when one appears,
   dispatches via `MCPTools.call(...)`, posts `tool-call` then
   `tool-result`, and feeds the result back into the conversation.
4. Repeats until the model emits ``FINAL: <answer>`` or hits
   ``MAX_TOOL_CALLS`` (5).
5. Posts the final message (`kind=final`) and returns the result body.

The text protocol is intentionally simple. It's not as robust as a tool-
use API on the model side, but it works against any Ollama model and lets
the chat transcript stay legible to humans watching the SSE stream.
"""
from __future__ import annotations

import json
import logging
import re
import time
from typing import TYPE_CHECKING, Any

from .chat import ChatBus, ChatMessage

if TYPE_CHECKING:
    from .ollama import OllamaClient

logger = logging.getLogger(__name__)

MAX_TOOL_CALLS = 5
DEFAULT_MAX_TOKENS = 1024

# The model emits one of these markers per turn.
_TOOL_CALL_RE = re.compile(r"^\s*TOOL_CALL\s*:\s*(\{.*\})\s*$", re.MULTILINE)
_FINAL_RE = re.compile(r"^\s*FINAL\s*:\s*(.+)$", re.MULTILINE | re.DOTALL)


def _parse_tool_call(buf: str) -> tuple[str, dict[str, Any]] | None:
    """Return (tool_name, args) if the buffer contains a TOOL_CALL line.

    The model is instructed to emit::

        TOOL_CALL: {"name": "search", "args": {"q": "kubernetes"}}

    We pick the first match; everything after the matched line is treated
    as a continuation that we'll discard once the tool result comes back.
    """
    m = _TOOL_CALL_RE.search(buf)
    if not m:
        return None
    raw = m.group(1).strip()
    try:
        obj = json.loads(raw)
    except json.JSONDecodeError:
        logger.warning("worker: malformed TOOL_CALL JSON: %r", raw[:120])
        return None
    name = obj.get("name") or obj.get("tool")
    args = obj.get("args") or obj.get("arguments") or {}
    if not isinstance(name, str) or not isinstance(args, dict):
        return None
    return name, args


def _parse_final(buf: str) -> str | None:
    m = _FINAL_RE.search(buf)
    if not m:
        return None
    return m.group(1).strip()


class Worker:
    """One specialist agent. Lives for the duration of a single subtask."""

    def __init__(
        self,
        *,
        name: str,
        role: str,
        instruction: str,
        skill: Any,  # slot 2's Skill — duck-typed to avoid an import cycle
        bus: ChatBus,
        ollama: "OllamaClient",
        tools: Any,  # slot 2's MCPTools
        graph_id: str,
        prior_context: list[ChatMessage] | None = None,
    ) -> None:
        self.name = name
        self.role = role
        self.instruction = instruction
        self.skill = skill
        self.bus = bus
        self.ollama = ollama
        self.tools = tools
        self.graph_id = graph_id
        self.prior_context = prior_context or []

    def _system_prompt(self) -> str:
        """Compose the system prompt. Adds the protocol instructions on top of skill."""
        skill_prompt = getattr(self.skill, "system_prompt", "") or ""
        protocol = (
            "You are participating in a group chat as " + self.name + ".\n"
            "When you need to call a tool, output a single line:\n"
            '  TOOL_CALL: {"name": "<tool>", "args": {...}}\n'
            "and STOP. The orchestrator will run the tool and reply with TOOL_RESULT.\n"
            "When you are finished, output a single line starting with FINAL:\n"
            "  FINAL: <your concise answer, 1-3 sentences>\n"
            "Keep chat updates terse — at most two short sentences per turn."
        )
        return f"{skill_prompt}\n\n{protocol}".strip()

    def _initial_messages(self) -> list[dict]:
        msgs: list[dict] = []
        # Inject any prior chat context the orchestrator wanted us to see.
        if self.prior_context:
            transcript = "\n".join(f"{m.speaker}: {m.body}" for m in self.prior_context)
            msgs.append(
                {
                    "role": "user",
                    "content": f"Group-chat context so far:\n{transcript}\n",
                }
            )
        msgs.append({"role": "user", "content": self.instruction})
        return msgs

    async def _stream_until_marker(
        self,
        model: str,
        messages: list[dict],
        system: str,
    ) -> str:
        """Drain a chat stream into a single buffer. Returns the full text."""
        buf_parts: list[str] = []
        async for chunk in self.ollama.chat(
            model=model,
            messages=messages,
            system=system,
            max_tokens=DEFAULT_MAX_TOKENS,
        ):
            buf_parts.append(chunk)
        return "".join(buf_parts)

    async def run(self) -> str:
        """Execute the subtask. Returns the final result body."""
        await self.bus.publish(
            self.graph_id,
            speaker=self.name,
            body=f"Starting: {self.instruction}",
            kind="info",
        )

        model = getattr(self.skill, "recommended_model", None) or "qwen2.5:3b"
        system = self._system_prompt()
        messages = self._initial_messages()

        for iteration in range(MAX_TOOL_CALLS + 1):
            t0 = time.monotonic()
            try:
                text = await self._stream_until_marker(model, messages, system)
            except Exception as exc:
                err = f"LLM call failed: {exc}"
                await self.bus.publish(
                    self.graph_id,
                    speaker=self.name,
                    body=err,
                    kind="error",
                    meta={"model": model},
                )
                return err
            latency_ms = int((time.monotonic() - t0) * 1000)

            final = _parse_final(text)
            if final is not None:
                await self.bus.publish(
                    self.graph_id,
                    speaker=self.name,
                    body=final,
                    kind="final",
                    meta={"model": model, "latency_ms": latency_ms, "iterations": iteration},
                )
                return final

            tool = _parse_tool_call(text)
            if tool is None:
                # Model didn't follow the protocol — treat the whole reply as the final answer.
                body = text.strip() or "(no answer)"
                await self.bus.publish(
                    self.graph_id,
                    speaker=self.name,
                    body=body,
                    kind="final",
                    meta={"model": model, "latency_ms": latency_ms, "protocol": "implicit-final"},
                )
                return body

            tool_name, tool_args = tool
            await self.bus.publish(
                self.graph_id,
                speaker=self.name,
                body=f"Calling tool `{tool_name}`",
                kind="tool-call",
                meta={"tool_name": tool_name, "args": tool_args},
            )

            try:
                result = await self.tools.call(tool_name, tool_args)
            except Exception as exc:
                result = f"ERROR: {exc}"
            result_str = result if isinstance(result, str) else json.dumps(result, default=str)

            await self.bus.publish(
                self.graph_id,
                speaker=self.name,
                body=f"Tool `{tool_name}` returned ({len(result_str)} chars).",
                kind="tool-result",
                meta={"tool_name": tool_name},
            )

            # Feed the assistant's TOOL_CALL turn + a synthetic user TOOL_RESULT
            # back into the conversation, so the next stream continues from here.
            messages.append({"role": "assistant", "content": text})
            messages.append(
                {
                    "role": "user",
                    "content": f"TOOL_RESULT for {tool_name}: {result_str}\n"
                    "Continue. Either call another tool or emit FINAL.",
                }
            )

        # Hit the cap without seeing FINAL.
        msg = "Hit max tool-call iterations without a final answer."
        await self.bus.publish(
            self.graph_id,
            speaker=self.name,
            body=msg,
            kind="error",
            meta={"max_iterations": MAX_TOOL_CALLS},
        )
        return msg
