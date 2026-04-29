# Architecture

A one-page mental model. For detail on individual components see the
linked docs.

## The shape of it

```
   ┌──────────┐         ┌──────────────┐         ┌──────────────────────────────┐
   │  phone   │  HTTPS  │   tailscale  │  HTTP   │            host              │
   │  / web   ├────────▶│    funnel    ├────────▶│  ┌────────────────────────┐  │
   │  client  │         │ (public URL) │         │  │      responder         │  │
   └──────────┘         └──────────────┘         │  │ :7900 — always on,     │  │
                                                 │  │ wakes the stack on /wake│  │
                                                 │  └───────────┬────────────┘  │
                                                 │              │ docker compose│
                                                 │              ▼               │
                                                 │  ┌────────────────────────┐  │
                                                 │  │      docker stack      │  │
                                                 │  │  ┌────────┐ ┌────────┐ │  │
                                                 │  │  │ ollama │ │ webui  │ │  │
                                                 │  │  └────────┘ └────────┘ │  │
                                                 │  │  ┌────────┐ ┌────────┐ │  │
                                                 │  │  │whisper │ │ piper  │ │  │
                                                 │  │  └────────┘ └────────┘ │  │
                                                 │  │  ┌────────┐            │  │
                                                 │  │  │  mcp   │            │  │
                                                 │  │  └────────┘            │  │
                                                 │  └────────────────────────┘  │
                                                 └──────────────────────────────┘
```

## Components

### Tailscale Funnel

Provides a stable public HTTPS URL backed by a real cert, with no port
forwarding or public IP. Funnel terminates TLS and forwards plaintext
HTTP to a single local port — by convention, the responder's port 7900,
not the WebUI's 3000. Wrapped by `localmind tunnel` so users don't have
to memorise the Tailscale CLI flags. See [docs/mobile.md](mobile.md).

### Responder

A tiny Go HTTP service that runs on the host (not in docker) under a
user-level service unit (launchd / systemd --user / Windows registry
Run key). It answers `/healthz`, `/status`, and `/wake` even when the
docker stack is cold. On `/wake` it shells out to `docker compose up`,
blocks until the WebUI is reachable, and returns the URL. This is what
keeps the public URL stable — the docker stack's port can be down and
the phone still gets an instant 200.

### Docker stack

The actual AI workload. Five services:

- **ollama** — LLM inference. Pulls the model picked by the profiler.
- **webui** — Open WebUI. Chat, model admin, multi-user accounts.
- **whisper** — `faster-whisper` server for speech-to-text.
- **piper** — Piper TTS for text-to-speech.
- **mcp** — `localmind-mcp` gateway. Exposes the local RAG index and
  tool registry over HTTP for Claude Code, Cursor, and friends.

Brought up and down as a single unit by `docker compose`. CPU is
default; GPU overlays in `compose/compose.gpu.*.yml` are merged in by
the wizard when it detects an NVIDIA or Apple Silicon host.

### Wizard CLI (`localmind`)

The single host-side binary, written in Go (`wizard/`). It is the only
thing the install scripts drop on the user's machine. Its
responsibilities:

- **`init`** — profile hardware, write `.env` and chosen overlay paths.
- **`up` / `down`** — dispatch `docker compose` with the right overlays.
- **`responder`** — install / run the host-side wake service.
- **`tunnel`** — wrap `tailscale funnel`.
- **`keepalive`** — block sleep on the host OS.
- **`backup` / `restore`** — tar the docker volumes and config.
- **`mcp`** — local-developer helpers for the MCP gateway.

Nothing inside docker depends on the wizard at runtime; it's purely a
control plane.

## The key insight

The responder exists so the **public URL stays stable even when the
docker stack is cold**. Without it, every minute the laptop dozes and
ollama unloads, the phone sees a TCP RST or a long stall. With it, the
phone always gets a fast HTTP 200 from the host, and the responder
takes the latency hit of starting docker on the user's behalf.

That decision drives most of the rest of the architecture: that's why
`tunnel start` defaults to port 7900 (responder), not 3000 (webui);
why the responder is a host binary and not another container; and why
`keepalive` is its own subcommand rather than a flag on `up`.
