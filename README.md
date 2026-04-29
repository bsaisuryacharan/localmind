# localmind

Self-hosted AI in a box. One command, one machine, no cloud.

```bash
curl -fsSL https://raw.githubusercontent.com/bsaisuryacharan/localmind/main/install.sh | sh
```

That installs a single static binary, runs a setup wizard that detects your hardware (CPU / NVIDIA / Apple Silicon / RAM), picks the right model sizes for you, writes the compose files, and brings up the stack.

## Demo

> [Demo gif coming soon — see [issue #N] for tracking.]

## What's in the box

| Component        | What it does                            | Project          |
| ---------------- | --------------------------------------- | ---------------- |
| Inference        | Runs LLMs locally                       | Ollama           |
| Web UI           | Chat, model admin, multi-user           | Open WebUI       |
| Voice in (STT)   | Microphone → text                       | faster-whisper   |
| Voice out (TTS)  | Text → audio                            | Piper            |
| RAG              | Watches `./data/`, indexes files locally | in-memory cosine search (sqlite-vec backend planned) |
| MCP gateway      | Exposes RAG + tools to Claude / Cursor  | localmind-mcp    |

Everything runs as containers. Nothing leaves your machine unless you opt in.

## Architecture

```
  phone ──▶ tailscale funnel ──▶ responder ──▶ docker stack
                                  (host)         (ollama, webui,
                                                  whisper, piper, mcp)
```

The responder is a tiny host-side HTTP service that always answers, even when the docker stack is cold; it wakes the stack on demand so the public URL stays stable. The wizard CLI (`localmind`) is the only host-side binary — it dispatches `docker compose`, manages the responder, and owns install/profile/backup. See [docs/architecture.md](docs/architecture.md) for the full picture.

## Why

Each piece above already exists and has a strong following. Nobody owns the canonical bundle. localmind is opinionated glue — sensible defaults, hardware-aware setup, single backup command, optional Tailscale Funnel for remote access.

## Quick start

```bash
# install (Linux / macOS)
curl -fsSL https://raw.githubusercontent.com/bsaisuryacharan/localmind/main/install.sh | sh

# install (Windows PowerShell, run as Administrator)
iwr -useb https://raw.githubusercontent.com/bsaisuryacharan/localmind/main/install.ps1 | iex

# bring it up
localmind up

# stop it
localmind down

# back everything up (chats + RAG index + models manifest)
localmind backup ./localmind-backup.tar.zst

# restore from an archive (destructive; prompts before overwriting)
localmind restore ./localmind-backup.tar.zst

# mobile access (your phone, anywhere)
localmind responder install   # host-side service that wakes the stack on demand
localmind keepalive on        # don't let the laptop sleep
localmind tunnel start 7900   # publish via Tailscale Funnel; prints the URL
```

Open WebUI at http://localhost:3000 — or, if you ran `tunnel start`, at the
public URL it printed. See [docs/mobile.md](docs/mobile.md) for the full
phone-from-anywhere story including current limits around laptop sleep.

## Hardware-aware defaults

`localmind init` runs a profiler that picks model sizes for you:

| Detected           | Default chat model       | Embedding model        |
| ------------------ | ------------------------ | ---------------------- |
| CPU only, 16 GB    | `qwen2.5:3b`             | `nomic-embed-text`     |
| CPU only, 32 GB    | `qwen2.5:7b`             | `nomic-embed-text`     |
| NVIDIA, 12 GB VRAM | `qwen2.5:14b-instruct-q4`| `bge-m3`               |
| NVIDIA, 24 GB VRAM | `qwen2.5:32b-instruct-q4`| `bge-m3`               |
| Apple M-series, 16 GB | `qwen2.5:7b`          | `nomic-embed-text`     |
| Apple M-series, 32+ GB | `qwen2.5:14b`        | `bge-m3`               |

Override anything with `models.yml`.

## Use it from Claude Code / Cursor

The MCP gateway exposes your local RAG index + a tool registry:

```bash
claude mcp add localmind http://localhost:7800/mcp
```

Then ask Claude things like "search my notes for last week's meeting decisions" — it will tool-call into the gateway, which queries the local index. See [docs/mcp.md](docs/mcp.md) for tool schemas and indexing rules.

## Project structure

```
.
├── README.md
├── docker-compose.yml          # default (CPU)
├── compose/
│   ├── compose.gpu.nvidia.yml  # overlay
│   └── compose.gpu.apple.yml   # overlay
├── models.yml                  # declarative model list
├── install.sh                  # Linux / macOS bootstrap
├── install.ps1                 # Windows bootstrap
├── wizard/                     # Go: hardware detection, setup TUI
├── mcp/                        # Go: MCP gateway exposing RAG + tools
├── docs/                       # install, GPU notes, troubleshooting
├── scripts/                    # smoke tests
└── data/                       # watched folder for RAG ingestion
```

## Status

Pre-alpha. Active development. Issues and PRs welcome.

**Privacy**: localmind sends no telemetry by default. See [docs/telemetry.md](docs/telemetry.md) for the opt-in plan.

## License

MIT.
