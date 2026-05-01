"""Smoke tests for ChatBus.

Covers:
* monotonically-increasing seq numbers
* history replay for late subscribers
* subscribe-after-publish: a subscriber that joins late still sees prior messages
* cancel() closes subscriber iterators cleanly
"""
from __future__ import annotations

import asyncio
import os
import tempfile

import pytest

from localmind_orchestrator.chat import ChatBus, ChatMessage
from localmind_orchestrator.state import GraphStore


async def _make_store() -> tuple[GraphStore, str]:
    fd, path = tempfile.mkstemp(suffix=".db", prefix="lmtest-")
    os.close(fd)
    s = GraphStore(path)
    await s.init_db()
    return s, path


async def _drop_store(s: GraphStore, path: str) -> None:
    await s.close()
    try:
        os.unlink(path)
    except OSError:
        pass


@pytest.mark.asyncio
async def test_seq_monotonic_and_persisted() -> None:
    store, path = await _make_store()
    try:
        bus = ChatBus(store=store)
        g = "g1"
        a = await bus.publish(g, speaker="@user", body="hi", kind="info")
        b = await bus.publish(g, speaker="@orchestrator", body="hello", kind="final")
        assert a.seq == 1
        assert b.seq == 2
        history = await bus.history(g)
        assert [m.seq for m in history] == [1, 2]
        assert history[0].body == "hi"
    finally:
        await _drop_store(store, path)


@pytest.mark.asyncio
async def test_subscribe_replays_history() -> None:
    store, path = await _make_store()
    try:
        bus = ChatBus(store=store)
        g = "g2"
        await bus.publish(g, speaker="@user", body="first", kind="info")
        await bus.publish(g, speaker="@orchestrator", body="second", kind="info")

        seen: list[ChatMessage] = []
        iterator = await bus.subscribe(g)

        async def reader() -> None:
            async for m in iterator:
                seen.append(m)
                if len(seen) >= 3:
                    return

        task = asyncio.create_task(reader())
        # Give the reader a chance to drain history.
        await asyncio.sleep(0.05)
        await bus.publish(g, speaker="@worker-1", body="third", kind="final")
        await asyncio.wait_for(task, timeout=2.0)
        bodies = [m.body for m in seen]
        assert bodies == ["first", "second", "third"]
    finally:
        await _drop_store(store, path)


@pytest.mark.asyncio
async def test_cancel_closes_subscribers() -> None:
    store, path = await _make_store()
    try:
        bus = ChatBus(store=store)
        g = "g3"
        await bus.publish(g, speaker="@user", body="hi", kind="info")
        iterator = await bus.subscribe(g)

        async def reader() -> int:
            n = 0
            async for _ in iterator:
                n += 1
            return n

        task = asyncio.create_task(reader())
        await asyncio.sleep(0.05)  # let reader drain history
        await bus.cancel(g)
        n = await asyncio.wait_for(task, timeout=2.0)
        # Reader saw the one history message then exited cleanly on close.
        assert n == 1
    finally:
        await _drop_store(store, path)


@pytest.mark.asyncio
async def test_publish_with_chatmessage_assigns_seq() -> None:
    store, path = await _make_store()
    try:
        bus = ChatBus(store=store)
        g = "g4"
        msg = ChatMessage(
            graph_id=g, seq=0, ts_unix=0, speaker="@user", body="hello", kind="info"
        )
        out = await bus.publish(msg)
        assert out.seq == 1
        assert out.ts_unix > 0
    finally:
        await _drop_store(store, path)
