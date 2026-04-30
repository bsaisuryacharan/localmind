"""SQLite-backed graph + message persistence.

Used by ChatBus so subscribers can replay history after a process restart and
so `localmind agent show <id>` keeps working when the run is over.

Single-writer-per-graph (the orchestrator's coordinator task) is enforced
by an asyncio.Lock keyed by graph_id; many readers are fine.
"""
from __future__ import annotations

import asyncio
import json
import logging
import os
import time
from dataclasses import dataclass, field
from typing import TYPE_CHECKING

import aiosqlite

if TYPE_CHECKING:
    from .chat import ChatMessage

logger = logging.getLogger(__name__)

DEFAULT_DB_PATH = os.environ.get(
    "LOCALMIND_ORCHESTRATOR_DB", "/var/lib/localmind/orchestrator.db"
)

SCHEMA = """
CREATE TABLE IF NOT EXISTS graphs (
    id TEXT PRIMARY KEY,
    user_query TEXT NOT NULL,
    mode TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at REAL NOT NULL,
    updated_at REAL NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
    graph_id TEXT NOT NULL REFERENCES graphs(id),
    seq INTEGER NOT NULL,
    ts_unix REAL NOT NULL,
    speaker TEXT NOT NULL,
    body TEXT NOT NULL,
    kind TEXT NOT NULL,
    refs TEXT NOT NULL,
    meta TEXT NOT NULL,
    PRIMARY KEY (graph_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_messages_graph ON messages(graph_id, seq);
"""


@dataclass
class Graph:
    id: str
    user_query: str
    mode: str  # 'direct' | 'light' | 'full'
    status: str  # 'pending-confirm' | 'running' | 'done' | 'cancelled' | 'error'
    created_at: float = field(default_factory=time.time)
    updated_at: float = field(default_factory=time.time)


class GraphStore:
    """Owns the SQLite connection. One instance per process, shared via FastAPI deps."""

    def __init__(self, path: str | None = None) -> None:
        self.path = path or DEFAULT_DB_PATH
        self._conn: aiosqlite.Connection | None = None
        self._write_lock = asyncio.Lock()

    async def init_db(self) -> None:
        """Open the connection, apply schema, enable WAL."""
        # Make sure the parent directory exists; the volume mount may create
        # only the leaf directory, and tests use a tmp path.
        parent = os.path.dirname(self.path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        self._conn = await aiosqlite.connect(self.path)
        await self._conn.execute("PRAGMA journal_mode=WAL")
        await self._conn.execute("PRAGMA synchronous=NORMAL")
        await self._conn.executescript(SCHEMA)
        await self._conn.commit()
        logger.info("orchestrator db ready at %s", self.path)

    async def close(self) -> None:
        if self._conn is not None:
            await self._conn.close()
            self._conn = None

    def _require_conn(self) -> aiosqlite.Connection:
        if self._conn is None:
            raise RuntimeError("GraphStore.init_db() was not awaited")
        return self._conn

    async def record_graph(self, g: Graph) -> None:
        conn = self._require_conn()
        async with self._write_lock:
            await conn.execute(
                "INSERT OR REPLACE INTO graphs(id, user_query, mode, status, created_at, updated_at)"
                " VALUES (?, ?, ?, ?, ?, ?)",
                (g.id, g.user_query, g.mode, g.status, g.created_at, g.updated_at),
            )
            await conn.commit()

    async def update_status(self, graph_id: str, status: str) -> None:
        conn = self._require_conn()
        async with self._write_lock:
            await conn.execute(
                "UPDATE graphs SET status=?, updated_at=? WHERE id=?",
                (status, time.time(), graph_id),
            )
            await conn.commit()

    async def record_message(self, m: "ChatMessage") -> None:
        conn = self._require_conn()
        async with self._write_lock:
            await conn.execute(
                "INSERT OR REPLACE INTO messages(graph_id, seq, ts_unix, speaker, body, kind, refs, meta)"
                " VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                (
                    m.graph_id,
                    m.seq,
                    m.ts_unix,
                    m.speaker,
                    m.body,
                    m.kind,
                    json.dumps(m.refs),
                    json.dumps(m.meta),
                ),
            )
            await conn.commit()

    async def load_history(self, graph_id: str) -> list["ChatMessage"]:
        from .chat import ChatMessage  # local import to avoid cycle

        conn = self._require_conn()
        cur = await conn.execute(
            "SELECT graph_id, seq, ts_unix, speaker, body, kind, refs, meta"
            " FROM messages WHERE graph_id=? ORDER BY seq ASC",
            (graph_id,),
        )
        rows = await cur.fetchall()
        await cur.close()
        out: list[ChatMessage] = []
        for r in rows:
            out.append(
                ChatMessage(
                    graph_id=r[0],
                    seq=r[1],
                    ts_unix=r[2],
                    speaker=r[3],
                    body=r[4],
                    kind=r[5],
                    refs=json.loads(r[6]),
                    meta=json.loads(r[7]),
                )
            )
        return out

    async def get_graph(self, graph_id: str) -> Graph | None:
        conn = self._require_conn()
        cur = await conn.execute(
            "SELECT id, user_query, mode, status, created_at, updated_at FROM graphs WHERE id=?",
            (graph_id,),
        )
        row = await cur.fetchone()
        await cur.close()
        if not row:
            return None
        return Graph(
            id=row[0],
            user_query=row[1],
            mode=row[2],
            status=row[3],
            created_at=row[4],
            updated_at=row[5],
        )


# Convenience module-level shims so callers can write `await state.init_db(path)`
# without importing GraphStore explicitly when a singleton is enough.
async def init_db(path: str) -> GraphStore:
    s = GraphStore(path)
    await s.init_db()
    return s
