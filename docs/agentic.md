# Agentic mode

`localmind agent` decomposes complex queries into parallel agent runs,
shows you the plan, and on confirm executes it as a Slack-style group
chat. Trivial queries don't pay the multi-agent cost — a complexity
classifier picks the cheapest mode that fits the question.

This is a v0.3.0 feature. It runs entirely on your laptop. No cloud APIs
in the data path.

## What it does

A single command takes a natural-language query and routes it through a
classifier. Depending on the query's shape, the orchestrator either
answers it inline, spawns one specialist worker, or decomposes it into
three or more parallel workers that talk to each other in a shared chat
log. You watch the chat live; you can interject; you can confirm or
edit the plan before any spawning happens.

Underneath, every agent is a markdown file (`orchestrator/skills/<role>.md`)
with a YAML frontmatter declaring its model, its tool whitelist, and its
system prompt. Every tool call goes through the existing localmind-mcp
gateway. Every message is captured in an audit log.

## Three modes

The classifier picks one of three modes per query:

| Mode | When | What spawns |
|---|---|---|
| `direct` | Trivial / single-fact queries | Nothing. Orchestrator answers inline. |
| `light` | Single-domain task with one obvious specialist | One worker (e.g. `@researcher-1`). |
| `full`  | Multi-step / multi-domain queries | Three or more parallel workers, plan-then-confirm. |

Examples that route to each mode:

- `direct`: "what is the capital of France?", "what's 2+2?"
- `light`: "find every mention of Phoenix in my notes", "list the PDFs in ./data"
- `full`: "summarize every PDF in ./data and propose 3 follow-up questions per doc",
  "compare the architecture of A and B and write me a migration plan"

The classifier itself is a prompt-plus-heuristic combo running against
`qwen2.5:3b`. Latency is ~200 ms, token cost ~80 tokens per query. It
pays back instantly on every simple turn.

## Quick start

### Demo A — direct mode

```bash
localmind agent run "what is the capital of France?"
```

Expected output:

```
@orchestrator (1.4s) : Paris.
```

One message, no spawn. Audit log records one entry, no `spawn` events.

### Demo B — light mode

```bash
localmind agent run "find every mention of Phoenix in my notes"
```

Expected output:

```
@orchestrator   (0.3s) : This is a single-search task. Spawning @researcher-1.
@researcher-1   (0.8s) : Searching localmind index for "Phoenix"...
@researcher-1   (4.2s) : 3 hits found across 2 documents:
                          - phoenix-q3.pdf (2 chunks, top score 0.61)
                          - meeting-2026-04-15.md (1 chunk, score 0.42)
                          Want me to read any of them?
```

One worker, no synthesis pass. Total wall time ~5 s on `cpu_mid` hardware.

### Demo C — full mode (the PDF-summary demo)

```bash
localmind agent run "Look through ./data and give me a 5-bullet executive summary of every PDF, then propose 3 follow-up questions per document."
```

Within ~5 s the orchestrator posts a plan and asks to confirm:

```
@orchestrator (4.1s) : This needs decomposition. Plan:
                         1. @researcher-1: list every PDF in ./data
                         2. @researcher-2: for each, extract abstract + key terms
                         3. @reviewer-1:   summarize each doc (5 bullets)
                         4. @reviewer-2:   generate 3 follow-up questions per doc
                         5. @synthesizer:  combine into final response
                       Confirm? [y/n/edit]:
```

On `y`, four workers spawn in parallel. @-mention handoffs are visible
in the chat. `@synthesizer` posts the final answer as the last message.
Total wall time should be under 90 s on `cpu_mid` hardware
(qwen2.5:7b for synthesis, qwen2.5:3b for workers).

## The chat experience

Agents are not opaque sub-processes. They are conversation participants.
Each agent posts terse status updates as a named participant
(`@researcher-1`, `@reviewer-1`, `@synthesizer`). Inter-agent handoffs
happen via `@`-mentions in the same thread.

A representative six-line transcript:

```
@user                  : Summarize every PDF in ./data + 3 follow-ups per doc
@orchestrator (0.3s)   : Plan ready. Spawning 4 workers. Confirm? (y/n/edit)
@user                  : y
@researcher-1 (1.2s)   : Found 12 PDFs. Extracting abstracts...
@reviewer-1   (4.7s)   : @researcher-1 — can you share the Phoenix abstract?
@synthesizer  (45s)    : Final summary attached. 3,421 tokens, 87 s wall.
```

Messages are terse by construction: typically two sentences or fewer.
Internal scratchpad reasoning is not surfaced; only declarative progress
updates. The user can interject at any time with plain text or with an
explicit `@user: ...` mid-flight; the orchestrator routes the message
to the relevant worker as runtime guidance.

## Confirming and editing the plan

After the orchestrator decomposes a `full`-mode query, it posts the
plan and waits at a `Confirm? [y/n/edit]` prompt.

- `y` accepts the plan as-is and spawns the listed workers.
- `n` aborts the run; no workers spawn.
- `edit` opens an editing path. The CLI accepts a JSON array on stdin:

```json
[
  {"role": "researcher", "name": "researcher-1", "instruction": "list every PDF in ./data"},
  {"role": "reviewer",   "name": "reviewer-1",   "instruction": "summarize each doc in 5 bullets"}
]
```

Each entry is a `{role, name, instruction}` object. `role` must match a
skill file under `orchestrator/skills/`. `name` is the chat handle
(`@<name>` in the transcript). `instruction` is the natural-language
task that worker is given as its initial prompt.

The HTML chat view at `/agent/<id>` exposes the same three buttons
(`y` / `n` / `edit`) plus an inline JSON editor for the `edit` path.

## Replaying past runs

Every agent graph is persisted to a SQLite database at
`/var/lib/localmind/orchestrator.db`. List recent runs and replay any
of them by `graph_id`:

```bash
localmind agent list                # recent graph IDs and their queries
localmind agent show <graph_id>     # full chat transcript + tool calls
localmind agent cancel <graph_id>   # kill an in-flight run
```

`show` replays the chat verbatim — every message, every tool call,
every spawn — in arrival order, with the same formatting the live
view used.

## Privacy posture

Agentic mode in v0.3.0 is **local-only**. No cloud APIs are called in
the data path. Every reasoning step runs on Ollama on the host; every
tool call goes through localmind-mcp on the host. The privacy claim
from the rest of localmind carries over unchanged.

Cloud opt-in arrives in v0.3.4. It is per-agent, declared in the skill
markdown frontmatter, and gated by data privacy markers. v0.3.0 ships
with `cloud: false` hard-locked across every skill.

See [docs/agentic-architecture.md](agentic-architecture.md) for the
threat model and audit-log details.

## Limits in v0.3.0

What v0.3.0 does not do:

- **Single-tier only.** Workers cannot spawn sub-workers in this
  release. Multi-level hierarchy is deferred to v0.3.2.
- **No budget enforcement.** Depth, breadth, total-token, and
  total-wall-time budgets are tracked but not yet hard-capped.
  Hard caps are deferred to v0.3.2.
- **No persistence across responder restarts.** If the orchestrator
  container restarts mid-run, the in-flight graph is lost (the
  audit log of completed steps persists). Resume across restart
  is deferred to v0.3.3, alongside Time Machine replay.
- **No skills marketplace.** Five starter skills ship in-tree at
  `orchestrator/skills/`. Marketplace integration is deferred to v0.3.4.
