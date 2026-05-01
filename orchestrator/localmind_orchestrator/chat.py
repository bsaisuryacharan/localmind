"""Group-chat message bus.

Every observable orchestrator action becomes a typed `ChatMessage`. The
`ChatBus` is an in-memory pub/sub keyed by graph_id, with optional
SQLite persistence so subscribers that arrive late (or after a process
restart) still see the full history.

Design notes
------------
* Single writer per graph (the orchestrator's coordinator task) — every
  publish is serialised through a per-graph asyncio.Lock so seq numbers
  stay strictly monotonic.
* Many readers — each `subscribe()` call gets its own asyncio.Queue.
  Readers always see history first, then live updates.
* `cancel()` closes the per-graph fanout: live subscribers' iterators
  terminate cleanly via a sentinel.
"""
from __future__ import annotations

import asyncio
import logging
import time
from typing import Any, AsyncIterator, Literal, TYPE_CHECKING

from pydantic import BaseModel, Field

if TYPE_CHECKING:
    from .state import GraphStore

logger = logging.getLogger(__name__)

ChatKind = Literal[
    "info",
    "plan",
    "confirm-request",
    "tool-call",
    "tool-result",
    "handoff",
    "final",
    "error",
]


class ChatMessage(BaseModel):
    """One observable event in a graph run.

    Speakers are conventionally @-prefixed (`@user`, `@orchestrator`,
    `@researcher-1`). `refs` lists other speakers this message
    @-mentions — the UI uses it to draw arrows in the group-chat view.
    """

    graph_id: str
    seq: int
    ts_unix: float
    speaker: str
    body: str
    kind: ChatKind
    refs: list[str] = Field(default_factory=list)
    meta: dict[str, Any] = Field(default_factory=dict)


# Sentinel used to wake subscriber iterators when the graph is cancelled.
_CLOSE = object()


class _GraphChannel:
    """Per-graph state inside the bus."""

    __slots__ = ("seq", "subscribers", "lock", "closed")

    def __init__(self) -> None:
        self.seq: int = 0
        self.subscribers: list[asyncio.Queue[Any]] = []
        self.lock: asyncio.Lock = asyncio.Lock()
        self.closed: bool = False


class ChatBus:
    """In-memory pub/sub. Pass a `GraphStore` to mirror messages to SQLite."""

    def __init__(self, store: "GraphStore | None" = None) -> None:
        self._channels: dict[str, _GraphChannel] = {}
        self._store = store
        self._channels_lock = asyncio.Lock()

    def _get_channel(self, graph_id: str) -> _GraphChannel:
        ch = self._channels.get(graph_id)
        if ch is None:
            ch = _GraphChannel()
            self._channels[graph_id] = ch
        return ch

    async def publish(
        self,
        msg_or_graph_id: "ChatMessage | str",
        *,
        speaker: str | None = None,
        body: str | None = None,
        kind: ChatKind | None = None,
        refs: list[str] | None = None,
        meta: dict[str, Any] | None = None,
    ) -> ChatMessage:
        """Publish a message. Two call styles are supported:

        ``await bus.publish(msg)`` — caller pre-built a ChatMessage; the
        bus assigns `seq` if it's missing (== 0) and `ts_unix` if 0.

        ``await bus.publish(graph_id, speaker=..., body=..., kind=...)`` —
        convenience for the orchestrator's hot paths.
        """
        if isinstance(msg_or_graph_id, ChatMessage):
            graph_id = msg_or_graph_id.graph_id
        else:
            graph_id = msg_or_graph_id

        ch = self._get_channel(graph_id)
        async with ch.lock:
            ch.seq += 1
            seq = ch.seq
            if isinstance(msg_or_graph_id, ChatMessage):
                msg = msg_or_graph_id.model_copy(
                    update={
                        "seq": seq,
                        "ts_unix": msg_or_graph_id.ts_unix or time.time(),
                    }
                )
            else:
                if speaker is None or body is None or kind is None:
                    raise ValueError("publish() needs speaker, body, kind when graph_id is a string")
                msg = ChatMessage(
                    graph_id=graph_id,
                    seq=seq,
                    ts_unix=time.time(),
                    speaker=speaker,
                    body=body,
                    kind=kind,
                    refs=refs or [],
                    meta=meta or {},
                )

            if self._store is not None:
                try:
                    await self._store.record_message(msg)
                except Exception:  # pragma: no cover — persistence shouldn't break delivery
                    logger.exception("failed to persist chat message")

            # Fan out to live subscribers. Use put_nowait so a slow consumer can't
            # block the publisher; queues are unbounded by design (graphs are short).
            for q in list(ch.subscribers):
                try:
                    q.put_nowait(msg)
                except asyncio.QueueFull:  # pragma: no cover — unbounded queues
                    logger.warning("subscriber queue full; dropping")
            return msg

    async def subscribe(self, graph_id: str) -> AsyncIterator[ChatMessage]:
        """Yield history first, then live updates until cancel() or graph end.

        Usage::

            async for msg in bus.subscribe(graph_id):
                ...
        """
        ch = self._get_channel(graph_id)
        # Register the subscriber FIRST under the channel lock, then read
        # history. Publish() also takes ch.lock before fanning out, so any
        # message we miss in history will arrive on `q` instead — never both,
        # never neither. We dedupe with `already_seen` just in case.
        q: asyncio.Queue[Any] = asyncio.Queue()
        async with ch.lock:
            ch.subscribers.append(q)
        if self._store is not None:
            history = await self._store.load_history(graph_id)
        else:
            history = []
        already_seen = {m.seq for m in history}

        async def _gen() -> AsyncIterator[ChatMessage]:
            try:
                for m in history:
                    yield m
                while True:
                    item = await q.get()
                    if item is _CLOSE:
                        return
                    msg: ChatMessage = item
                    if msg.seq in already_seen:
                        continue
                    yield msg
            finally:
                async with ch.lock:
                    if q in ch.subscribers:
                        ch.subscribers.remove(q)

        return _gen()

    async def history(self, graph_id: str) -> list[ChatMessage]:
        if self._store is not None:
            return await self._store.load_history(graph_id)
        # Fallback — no store; we don't keep an in-memory log because the
        # bus is meant to be paired with a store in production.
        return []

    async def cancel(self, graph_id: str) -> None:
        """Close all subscribers for `graph_id`. Idempotent."""
        ch = self._channels.get(graph_id)
        if ch is None:
            return
        async with ch.lock:
            ch.closed = True
            for q in ch.subscribers:
                q.put_nowait(_CLOSE)
            ch.subscribers.clear()
