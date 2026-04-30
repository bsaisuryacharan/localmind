"""FastAPI HTTP server for the orchestrator sidecar.

Wires up:
* a single `GraphStore` (SQLite, WAL),
* a single `ChatBus` mirroring to that store,
* a single `OllamaClient`,
* a single `Orchestrator`.

All four are constructed in the FastAPI lifespan and exposed through
dependency callables. Tests can override the dependencies with
`app.dependency_overrides[get_orchestrator] = lambda: ...`.
"""
from __future__ import annotations

import asyncio
import json
import logging
import os
from contextlib import asynccontextmanager
from typing import Any, AsyncIterator

from fastapi import Depends, FastAPI, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel

from .chat import ChatBus
from .ollama import OllamaClient
from .orchestrator import Orchestrator, PlannedWorker
from .state import DEFAULT_DB_PATH, GraphStore

logger = logging.getLogger(__name__)


# ----------------------------------------------------------------- app state


class _AppState:
    """Holds the long-lived singletons. Attached to `app.state.localmind`."""

    store: GraphStore
    bus: ChatBus
    ollama: OllamaClient
    orchestrator: Orchestrator


# ----------------------------------------------------------------- lifecycle


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    db_path = os.environ.get("LOCALMIND_ORCHESTRATOR_DB", DEFAULT_DB_PATH)
    store = GraphStore(db_path)
    await store.init_db()
    bus = ChatBus(store=store)
    ollama = OllamaClient()

    # Slot 2 wires these up; if its imports succeed we use them, otherwise the
    # orchestrator falls back to its built-in stub skill.
    skills_loader: Any = None
    mcp_tools: Any = None
    try:  # pragma: no cover — depends on slot 2 being merged
        from .skills import load_skill  # type: ignore
        from .tools import MCPTools  # type: ignore

        skills_loader = load_skill
        mcp_tools = MCPTools()
    except Exception as exc:
        logger.info("skills/tools not available yet (%s); using stubs", exc)

    orch = Orchestrator(
        bus=bus,
        store=store,
        ollama=ollama,
        skills_loader=skills_loader,
        mcp_tools=mcp_tools,
    )

    state = _AppState()
    state.store = store
    state.bus = bus
    state.ollama = ollama
    state.orchestrator = orch
    app.state.localmind = state

    try:
        yield
    finally:
        await ollama.aclose()
        await store.close()


# ------------------------------------------------------------------ FastAPI

app = FastAPI(
    title="localmind orchestrator",
    version="0.3.0",
    lifespan=lifespan,
)

_origins = ["http://localhost:7900", "http://localhost:3000"]
if os.environ.get("LOCALMIND_ORCHESTRATOR_CORS_DEV"):
    _origins.append("*")
app.add_middleware(
    CORSMiddleware,
    allow_origins=_origins,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


# --------------------------------------------------------------- dependencies


def get_state(request: Request) -> _AppState:
    state: _AppState | None = getattr(request.app.state, "localmind", None)
    if state is None:
        raise HTTPException(status_code=503, detail="orchestrator not initialised")
    return state


def get_bus(state: _AppState = Depends(get_state)) -> ChatBus:
    return state.bus


def get_orchestrator(state: _AppState = Depends(get_state)) -> Orchestrator:
    return state.orchestrator


# -------------------------------------------------------------- request bodies


class RunBody(BaseModel):
    query: str


class ConfirmBody(BaseModel):
    accepted: bool
    edits: list[PlannedWorker] | None = None


class InjectBody(BaseModel):
    body: str


# ------------------------------------------------------------------- endpoints


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/run")
async def run(body: RunBody, orch: Orchestrator = Depends(get_orchestrator)) -> dict[str, str]:
    if not body.query.strip():
        raise HTTPException(status_code=400, detail="query must not be empty")
    run = await orch.start_run(body.query)
    return {"graph_id": run.graph_id, "mode": run.mode}


@app.get("/history/{graph_id}")
async def history(graph_id: str, bus: ChatBus = Depends(get_bus)) -> JSONResponse:
    msgs = await bus.history(graph_id)
    return JSONResponse([m.model_dump() for m in msgs])


@app.post("/confirm/{graph_id}")
async def confirm(
    graph_id: str,
    body: ConfirmBody,
    orch: Orchestrator = Depends(get_orchestrator),
) -> dict[str, str]:
    try:
        await orch.confirm_run(graph_id, body.accepted, body.edits)
    except KeyError:
        raise HTTPException(status_code=404, detail="unknown graph_id")
    return {"status": "ok"}


@app.post("/inject/{graph_id}")
async def inject(
    graph_id: str,
    body: InjectBody,
    bus: ChatBus = Depends(get_bus),
) -> dict[str, str]:
    if not body.body.strip():
        raise HTTPException(status_code=400, detail="body must not be empty")
    await bus.publish(graph_id, speaker="@user", body=body.body, kind="info")
    return {"status": "ok"}


@app.post("/cancel/{graph_id}")
async def cancel(
    graph_id: str,
    orch: Orchestrator = Depends(get_orchestrator),
) -> dict[str, str]:
    await orch.cancel_run(graph_id)
    return {"status": "ok"}


@app.get("/stream/{graph_id}")
async def stream(graph_id: str, bus: ChatBus = Depends(get_bus)) -> StreamingResponse:
    """SSE: replays history then streams live updates until the run ends or is cancelled."""

    async def gen() -> AsyncIterator[bytes]:
        iterator = await bus.subscribe(graph_id)
        try:
            async for msg in iterator:
                payload = msg.model_dump_json()
                yield f"event: chat\ndata: {payload}\n\n".encode()
        except asyncio.CancelledError:
            return
        finally:
            # SSE clients usually disconnect by closing the connection; we
            # don't need to do anything special here.
            pass

    return StreamingResponse(
        gen(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "X-Accel-Buffering": "no",  # disable nginx buffering if proxied
        },
    )
