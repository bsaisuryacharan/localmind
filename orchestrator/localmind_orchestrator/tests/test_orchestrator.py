"""Smoke tests for the orchestrator state machine.

These tests stub out the LLM by patching `OllamaClient.generate` /
`OllamaClient.chat`. They assert the *shape* of the chat transcript for
each of the three modes — not the content of any particular LLM reply.
"""
from __future__ import annotations

import asyncio
import os
import tempfile
from typing import AsyncIterator

import pytest

from localmind_orchestrator.chat import ChatBus
from localmind_orchestrator.ollama import OllamaClient
from localmind_orchestrator.orchestrator import Orchestrator
from localmind_orchestrator.state import GraphStore


class _FakeOllama:
    """Test double for OllamaClient. `generate` returns canned text by call order."""

    def __init__(self, replies: list[str]) -> None:
        self._replies = list(replies)
        self.calls: list[dict] = []

    async def generate(self, *, model: str, prompt: str, system: str | None = None,
                       max_tokens: int = 512) -> str:
        self.calls.append({"model": model, "prompt": prompt, "system": system})
        if not self._replies:
            return ""
        return self._replies.pop(0)

    async def chat(self, *, model: str, messages: list[dict],
                   max_tokens: int = 1024, system: str | None = None) -> AsyncIterator[str]:
        # Default streaming reply — short FINAL. This must be an async
        # generator function (no `return`) to mirror OllamaClient.chat.
        yield "FINAL: did the work"

    async def aclose(self) -> None:
        return None


async def _make_store() -> tuple[GraphStore, str]:
    fd, path = tempfile.mkstemp(suffix=".db", prefix="lmtest-orch-")
    os.close(fd)
    s = GraphStore(path)
    await s.init_db()
    return s, path


async def _drain_until_done(orch: Orchestrator, graph_id: str, *, timeout: float = 3.0) -> None:
    run = orch.get_run(graph_id)
    assert run is not None
    if run.coordinator_task is not None:
        try:
            await asyncio.wait_for(run.coordinator_task, timeout=timeout)
        except asyncio.TimeoutError:
            pass


@pytest.mark.asyncio
async def test_direct_mode_emits_one_orchestrator_final() -> None:
    store, path = await _make_store()
    try:
        bus = ChatBus(store=store)
        # First reply = classifier label "direct"; second = direct answer.
        ollama = _FakeOllama(["direct", "2 + 2 = 4."])
        orch = Orchestrator(bus=bus, store=store, ollama=ollama)  # type: ignore[arg-type]

        run = await orch.start_run("what's 2+2")
        assert run.mode == "direct"
        await _drain_until_done(orch, run.graph_id)

        history = await bus.history(run.graph_id)
        # Expect: @user echo, @orchestrator final.
        speakers = [m.speaker for m in history]
        kinds = [m.kind for m in history]
        assert speakers[0] == "@user"
        assert "@orchestrator" in speakers
        # No worker should have been spawned.
        assert not any(s.startswith("@researcher") for s in speakers)
        assert "final" in kinds
    finally:
        await store.close()
        try:
            os.unlink(path)
        except OSError:
            pass


@pytest.mark.asyncio
async def test_light_mode_spawns_one_worker() -> None:
    store, path = await _make_store()
    try:
        bus = ChatBus(store=store)
        ollama = _FakeOllama(["light"])  # classifier
        orch = Orchestrator(bus=bus, store=store, ollama=ollama)  # type: ignore[arg-type]

        run = await orch.start_run("search my notes for kubernetes")
        assert run.mode == "light"
        await _drain_until_done(orch, run.graph_id)

        history = await bus.history(run.graph_id)
        speakers = [m.speaker for m in history]
        kinds = [m.kind for m in history]
        # We saw the user echo, an orchestrator handoff, and the worker's final.
        assert "@user" in speakers
        assert "@orchestrator" in speakers
        assert any(s.startswith("@researcher") for s in speakers)
        assert "handoff" in kinds
        assert "final" in kinds
    finally:
        await store.close()
        try:
            os.unlink(path)
        except OSError:
            pass


@pytest.mark.asyncio
async def test_full_mode_publishes_plan_and_confirm_request() -> None:
    store, path = await _make_store()
    try:
        bus = ChatBus(store=store)
        plan_json = (
            '[{"role":"researcher","instruction":"do A"},'
            '{"role":"researcher","instruction":"do B"},'
            '{"role":"reviewer","instruction":"review"}]'
        )
        # classifier -> "full", planner -> JSON plan.
        ollama = _FakeOllama(["full", plan_json])
        orch = Orchestrator(bus=bus, store=store, ollama=ollama)  # type: ignore[arg-type]

        run = await orch.start_run("review my repo and propose fixes for every module")
        assert run.mode == "full"

        # Wait for the orchestrator to publish plan + confirm-request.
        # It blocks on confirm_event so coordinator_task won't complete yet.
        for _ in range(50):
            history = await bus.history(run.graph_id)
            kinds = [m.kind for m in history]
            if "plan" in kinds and "confirm-request" in kinds:
                break
            await asyncio.sleep(0.02)
        history = await bus.history(run.graph_id)
        kinds = [m.kind for m in history]
        assert "plan" in kinds, f"expected plan kind, got {kinds}"
        assert "confirm-request" in kinds, f"expected confirm-request, got {kinds}"

        # Reject the plan so the coordinator exits cleanly without needing
        # the worker stream path (which the FakeOllama.chat handles anyway).
        await orch.confirm_run(run.graph_id, accepted=False)
        await _drain_until_done(orch, run.graph_id)
        assert run.status in {"cancelled", "done"}
    finally:
        await store.close()
        try:
            os.unlink(path)
        except OSError:
            pass
