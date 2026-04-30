# localmind-orchestrator

Agentic orchestrator sidecar for the localmind bundle. Ships in v0.3.0.

## What it is

A Python FastAPI service (port `7950`) that turns a user query into a
**group chat** of named AI participants. Every observable action — a plan,
a tool call, a worker handoff, a final answer — is published as a typed
`ChatMessage` on an in-memory pub/sub bus and persisted to SQLite. The CLI
and Open WebUI subscribe over Server-Sent Events so the user can watch the
graph unfold in real time and interject mid-flight.

## Three complexity modes

The orchestrator classifies each query into one of three modes using the
cheapest local model (`qwen2.5:3b` by default):

| Mode | When | Behaviour |
|------|------|-----------|
| **direct** | Greetings, factual one-liners, conversational acks. | Orchestrator answers in a single message. No spawn. |
| **light** | Single-action queries needing one tool call. | One specialist worker, no confirm step. |
| **full**  | Multi-step / multi-domain. | Plan → confirm-request → ≥3 parallel workers → synthesizer. |

## HTTP API

```
GET  /healthz                  liveness probe
POST /run                      {query} -> {graph_id, mode}
GET  /stream/{graph_id}        SSE stream of ChatMessage events
GET  /history/{graph_id}       JSON array of ChatMessage
POST /confirm/{graph_id}       {accepted, edits?}
POST /inject/{graph_id}        {body} — user mid-flight message
POST /cancel/{graph_id}        cancel run, close subscribers
```

## Configuration

| Env var | Default | Notes |
|---------|---------|-------|
| `OLLAMA_BASE_URL` | `http://ollama:11434` | Ollama HTTP endpoint. |
| `LOCALMIND_ORCHESTRATOR_DB` | `/var/lib/localmind/orchestrator.db` | SQLite path. WAL mode. |
| `LOCALMIND_ORCHESTRATOR_CORS_DEV` | unset | If set, adds `*` to allowed CORS origins. |

## Local dev

```
pip install -e .[dev]
uvicorn localmind_orchestrator.server:app --reload --port 7950
pytest
```

## Docker

```
docker build -t localmind/orchestrator:0.3.0 .
docker run --rm -p 7950:7950 \
    -e OLLAMA_BASE_URL=http://host.docker.internal:11434 \
    -v localmind-orch:/var/lib/localmind \
    localmind/orchestrator:0.3.0
```

In v0.3.0 the orchestrator is wired into `docker-compose.yml` as the
`orchestrator` service alongside Ollama, Open WebUI, the MCP gateway, and
the OCR sidecar.
