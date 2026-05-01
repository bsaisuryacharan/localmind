# Agentic architecture

Designer / contributor doc for localmind's agentic orchestration layer
(v0.3.0+). User-facing how-to is in [docs/agentic.md](agentic.md).

## Architecture

```
phone / claude-code / cli
        │
        ▼
responder (Go, host, Tailscale-served)
        │
        ├── /chat               → Open WebUI            (unchanged)
        ├── /lm/...             → status / wake          (unchanged)
        └── /agent/run          → orchestrator sidecar  (v0.3.0+)
                                       │
                                       │  HTTP+SSE on 127.0.0.1:7950
                                       ▼
                              orchestrator (Python container)
                                       │
                                       ├─[1] CLASSIFY complexity
                                       │      direct / light / full
                                       │
                                       ├─[2] decompose query → N subtasks (full mode)
                                       ├─[3] post plan as @orchestrator; await confirm
                                       ├─[4] spawn N workers (parallel); each joins the chat
                                       │      each worker:
                                       │        - loads skills/<role>.md
                                       │        - posts terse status as @<role>-<n>
                                       │        - calls Ollama for reasoning
                                       │        - calls MCP for tools
                                       │        - may @-mention other workers
                                       ├─[5] budgets gate every spawn (v0.3.2+)
                                       ├─[6] live group-chat SSE → responder + CLI
                                       ├─[7] @synthesizer composes final answer
                                       └─[8] audit log → MCP audit DB
                                              │
                                              ▼
                                  Ollama, MCP gateway, audit DB
                                  (all already in compose)
```

The architecture is **chat-first, not graph-first**. The agent graph is
the underlying execution model, but every observable interaction is a
group-chat message. The DAG view is a secondary render of the same
underlying message stream.

## Why a Python sidecar?

The orchestrator runs as a separate Python container alongside the
existing Go services. Three reasons:

- **LangGraph is mature.** Reinventing graph primitives (cycles, state,
  multi-agent handoffs) in Go would cost weeks for no win. LangGraph
  already does this well.
- **Agent ecosystem is Python-first.** Anthropic's official SDK,
  CrewAI, AutoGen, and most reusable agent patterns target Python.
  Writing the orchestrator in Python keeps that ecosystem available.
- **Sidecar isolation.** If the orchestrator crashes, the rest of
  localmind is unaffected. The wizard, responder, MCP gateway, and
  Ollama all keep running. The sidecar is the only Python in the
  project; the rest stays Go.

The cost is one extra ~200 MB image and a runtime introduction. We pay
that cost once.

## The complexity classifier

The classifier is the gate that decides whether a query gets the
multi-agent treatment. It runs on every incoming query before any
worker is spawned.

- **Model.** Default `qwen2.5:3b` (the cheapest local model the
  hardware profiler is comfortable assuming exists). Configurable via
  the orchestrator's environment.
- **Prompt.** Asks the model to label the query as `direct`, `light`,
  or `full` and emit a one-line rationale.
- **Heuristic fallback.** If the model is unreachable or returns an
  unparseable label, a deterministic heuristic kicks in: count the
  imperative verbs in the query, look for multi-step markers (`and
  then`, `, then`, `after that`, comma-separated lists of nouns), and
  count distinct domain indicators (`./data`, `./code`, URL,
  filename). Two or more verbs *and* a multi-step marker → `full`.
  One verb on a single domain → `light`. Otherwise → `direct`.
- **Cost.** ~200 ms latency, ~80 tokens per query.

The classifier output is logged with the rationale in the audit log
so users can see why a given query was routed the way it was.

## Group-chat message bus

Every agent action becomes a typed message on a shared bus. The bus
backs both the live SSE stream and the SQLite-persisted transcript.

The Pydantic schema:

```python
class ChatMessage(BaseModel):
    graph_id: str
    seq: int                    # monotonic per graph
    ts: float                   # unix seconds
    sender: str                 # "@orchestrator", "@researcher-1", "@user", ...
    kind: Literal[
        "say",                  # plain status update
        "plan",                 # orchestrator's decomposition output
        "confirm_request",      # asks user to confirm/edit/reject
        "confirm_response",     # user's reply
        "tool_call",            # an MCP tool was invoked
        "tool_result",          # the MCP tool returned
        "spawn",                # a new worker joined the chat
        "handoff",              # @-mention from one worker to another
        "final",                # @synthesizer's final answer
        "error",                # something failed
    ]
    body: str                   # human-readable payload
    meta: dict                  # per-kind structured payload
```

The SSE stream emits one event per message in arrival order. Event
data is the JSON-serialized `ChatMessage`. CLI and HTML clients render
each event as a chat line in the form `@<sender> (<latency>): <body>`.

## Skills as markdown

Each agent role is a markdown file at `orchestrator/skills/<role>.md`
with YAML frontmatter and a system-prompt body. Schema:

```yaml
---
name: researcher
description: Reads files, runs MCP search, summarizes findings.
recommended_model: qwen2.5:3b
cloud: false
tools:
  - search_files
  - read_file
  - list_files
max_tool_calls: 8
---
You are a research worker on a localmind agent team. ...
(system prompt body, plain markdown)
```

Frontmatter fields:

| Field | Meaning |
|---|---|
| `name` | The role name. Must match the filename. |
| `description` | One-line summary surfaced in the orchestrator's plan. |
| `recommended_model` | Ollama tag the worker reasons against. |
| `cloud` | `false` in v0.3.0 (hard-locked). v0.3.4 will accept `cloud: <model>` for opt-in cloud reasoning. |
| `tools` | Whitelist of MCP tool names this skill may call. Default-deny. |
| `max_tool_calls` | Per-invocation cap on tool calls. |

Skill bundles are PR-driven. v0.3.0 ships five starter skills:
`orchestrator`, `researcher`, `coder`, `reviewer`, `synthesizer`.

## MCP as the only tool surface

Every tool call from every agent goes through localmind-mcp. There is
no second tool path. This means:

- Adding a tool is "register a new MCP server", not "import a Python
  package and refactor the orchestrator."
- Per-skill whitelisting is enforced before the tool call leaves the
  worker. A tool not listed in the skill frontmatter raises
  `ToolNotAllowedError` and the call never reaches the gateway.
- The MCP audit log captures every tool call from every agent, with
  the requesting agent's name and graph ID stamped on the entry.

The whitelist is default-deny. A worker with no `tools:` field can
reason but cannot read files, search, or take any other action.

## Threat model

What we promise in v0.3.0:

- **Traffic stays local.** No cloud APIs are called in the data path.
  Reasoning runs on Ollama on the host. Tool calls go through MCP on
  the host. The existing Tailscale / responder threat model from
  [docs/threat-model.md](threat-model.md) carries over unchanged.
- **Audit log captures every event.** Every spawn, every tool call,
  every chat message lands in the audit log with the graph ID, the
  sender, the timestamp, and the payload. The log is queryable from
  the MCP audit DB.

What we explicitly do **not** protect against:

- **Malicious skill bundles.** A skill markdown file you install can
  contain a system prompt that tells the agent to do hostile things.
  The skill author has the same trust as a code dependency. The user
  is responsible for what they install. v0.3.4's marketplace will
  ship signed skills, but v0.3.0 has no such mechanism.
- **Prompt injection from indexed files.** A `researcher` worker
  reading a hostile PDF could be tricked into emitting unauthorized
  tool-call attempts (e.g. trying to invoke a write tool when the
  skill only whitelists read tools). This is mitigated — but not
  eliminated — by the skill whitelist: the worker can attempt the
  call, but it will be rejected before reaching the MCP gateway. We
  do not currently sanitize indexed content against injection.
- **Resource exhaustion.** v0.3.0 does not enforce hard budgets on
  depth, breadth, total tokens, or total wall time. A pathological
  query can spawn the maximum configured worker count and consume
  proportional CPU. v0.3.2 introduces hard caps.

## Phased delivery

| Phase | Tag | Scope | Effort |
|---|---|---|---|
| 1 | v0.3.0 | Orchestrator container + complexity classifier (direct/light/full) + group-chat message bus + single-tier full-mode (depth=1, ≥3 parallel workers) + skills as markdown + MCP as tool layer + CLI chat TUI + responder HTML chat view + audit-log writes | 2 weeks |
| 2 | v0.3.1 | DAG visualization tab on responder HTML (secondary to chat view). Pause / kill / resume from UI. Mobile-friendly chat. | 1 week |
| 3 | v0.3.2 | Multi-level hierarchy: workers spawn sub-workers. Depth, breadth, token, wall-time budgets hard-enforced. Sub-workers join the same group chat with `@<role>-<n>.<sub>` naming. | 2 weeks |
| 4 | v0.3.3 | Persistence: graphs survive responder restarts. Resume from any node. Replay against different models (depends on Time Machine plumbing). | 1.5 weeks |
| 5 | v0.3.4 | Skills marketplace. Cloud opt-in per agent gated by privacy markers on the data. | 2 weeks |

Total v0.3 ladder: ~9 weeks of focused work to ship the full hierarchy
+ budgets + audit + replay.
