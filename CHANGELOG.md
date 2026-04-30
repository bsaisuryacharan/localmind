# Changelog

All notable changes to localmind will be documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.2.0] — 2026-04-30

<https://github.com/bsaisuryacharan/localmind/releases/tag/v0.2.0>

### Added
- `localmind tunnel join` — Tailscale peer-to-peer mode, the new default for mobile access. End-to-end WireGuard, no public URL, no third party in the data path. Replaces `tunnel start` (which is now an alias for the renamed `tunnel funnel`).
- Windows Service responder install. `localmind responder install` (run elevated) registers a real Windows Service that survives logout and stays alive in Modern Standby (S0ix). Non-elevated runs fall back to the previous HKCU\Run mechanism with a warning.
- Wake-Docker-Desktop on `/wake`. The responder now ensures Docker Desktop is reachable before invoking `docker compose up`. Bumps the wake budget on Windows from 60s to 90s to accommodate Docker Desktop's cold start.
- Request-scoped wake-lock middleware on `/status`, `/wake`, and `/`. While any request is in flight, the OS is told to stay awake; when the last request finishes, the OS may sleep again. Replaces the always-on `keepalive` for the request path.
- New docs: `docs/phone-from-anywhere.md` (end-to-end setup), `docs/threat-model.md` (privacy analysis), `docs/headscale.md` (zero-third-party self-hosted coord). README gains a "Privacy" section.

### Changed
- `localmind tunnel start` is deprecated in favor of `localmind tunnel join`. The old command still works but prints a deprecation note and forwards to `funnel` (which is the renamed Funnel mode).
- Default tunnel mode is now peer-to-peer. Funnel is opt-in only.

### Fixed
- Responder no longer holds the system awake forever via `keepalive` for the request path. Wake-locks are scoped to in-flight requests only.

### Notes
- Modern Standby (S0ix) support is required on the laptop for the "phone wakes sleeping laptop" feature. Verify with `powercfg /a` on Windows. Older laptops with only S3 sleep need the WoL companion path, planned for v0.3.

## [v0.0.4] — 2026-04-29

<https://github.com/bsaisuryacharan/localmind/releases/tag/v0.0.4>

### Added

- Mobile entry page served by the responder at `/`, so the URL handed
  out by `localmind tunnel start 7900` lands on a usable page (status
  view + wake button) instead of a 404 when the docker stack is cold.
- OCR fallback for scanned PDFs: a new `ocrmypdf` sidecar service
  (`ocrmypdf` + `tesseract` + `pdftotext`) is wired into the compose
  stack. The MCP indexer routes PDFs without a text layer to the sidecar
  via `OCR_BASE_URL` (default `http://ocrmypdf:8000`); set
  `OCR_BASE_URL=""` to disable.
- `Store` interface seam in `mcp/internal/store/` so the in-memory
  cosine-search backend can be swapped out without touching the indexer
  or HTTP layer. Only `MemoryStore` is implemented today; the seam
  unblocks a future sqlite-vec backend.
- Code-signing scaffold: a `sign` job in `.github/workflows/release.yml`
  wired for AzureSignTool + Azure Key Vault. No-op until the five
  signing secrets are configured. See `docs/code-signing.md`.

### Changed

- README: clarified that the RAG layer is in-memory cosine search today
  (sqlite-vec is on the roadmap) rather than implying sqlite-vec ships.

## [v0.0.3] — 2026-04-29

<https://github.com/bsaisuryacharan/localmind/releases/tag/v0.0.3>

### Added

- `localmind responder` subcommand and host-side service. A tiny Go HTTP
  server that runs outside docker (via launchd / systemd --user / the
  Windows registry Run key) and answers `/healthz`, `/status`, and
  `/wake` even when the docker stack is cold. `/wake` brings the stack
  up and blocks until the WebUI is reachable, so the public Tailscale
  Funnel URL stays stable across laptop sleeps.
- `localmind responder install / status / uninstall / run` lifecycle
  commands. OS-specific install backends in
  `wizard/internal/wizard/responder.go`.
- Docs: `docs/mobile.md` covering the responder, keepalive, and tunnel
  end to end.

### Changed

- `localmind tunnel start` now defaults to port 7900 (the responder)
  rather than 3000 (the WebUI), so phones hit the wake-capable surface
  first.

## [v0.0.2] — 2026-04-28

<https://github.com/bsaisuryacharan/localmind/releases/tag/v0.0.2>

### Added

- `localmind restore <archive>` — first-class restore command that
  reverses `localmind backup`. Stops the stack, recreates the four
  named volumes, streams the zstd tarball back in via a throwaway
  alpine container, and brings the stack back up. Prompts before
  overwriting existing volumes.
- `localmind tunnel` subcommand wrapping `tailscale funnel start /
  status / stop`, so users don't have to memorize the Tailscale CLI
  flags.
- `localmind keepalive on / status / off` — blocks host sleep on
  macOS (`caffeinate -d`), Linux (`systemd-inhibit`), and Windows
  (`SetThreadExecutionState`). Implementations split across
  `keepalive_unix.go` and `keepalive_windows.go`.

### Fixed

- Backup command exits with a clear hint when the named docker volumes
  don't exist yet, instead of producing an empty archive.

## [v0.0.1] — 2026-04-28

<https://github.com/bsaisuryacharan/localmind/releases/tag/v0.0.1>

### Added

- Initial public release.
- `localmind` wizard CLI (Go) with `init`, `up`, `down`, `backup`,
  `mcp`, and `profile` subcommands.
- Hardware profiler (`wizard/internal/hwdetect/`) that detects CPU,
  NVIDIA, and Apple Silicon hosts plus available RAM, and picks a
  matching chat + embedding model from `models.yml`.
- Throughput profiler (`wizard/internal/profile/`) that runs a one-shot
  `/api/generate` benchmark against Ollama on first `up` and writes
  `.localmind/profile.json` with a downgrade / stay / upgrade
  recommendation.
- Docker compose stack: `ollama`, `open-webui`, `faster-whisper`,
  `piper`, and `localmind-mcp`. CPU is default; GPU overlays in
  `compose/compose.gpu.nvidia.yml` and `compose/compose.gpu.apple.yml`
  are merged in by the wizard when the matching hardware is detected.
- `localmind-mcp` gateway (`mcp/`) exposing three MCP tools —
  `search_files`, `list_files`, `read_file` — backed by an in-memory
  cosine-similarity index over `./data/`.
- fsnotify watcher in the MCP indexer with 500 ms write debounce, a
  30 s safety rescan, and a fallback to rescan-only on filesystems
  where fsnotify cannot initialize.
- File-type support: `.md`, `.markdown`, `.txt`, `.rst`, `.pdf`
  (via `ledongthuc/pdf`), and `.docx` (stdlib `archive/zip` +
  `encoding/xml`, `<w:t>` runs only).
- `localmind backup [path]` — snapshots the four docker volumes
  (`localmind_ollama`, `localmind_webui`, `localmind_piper`,
  `localmind_mcp_index`) into a single zstd-compressed tarball via a
  throwaway alpine container, with no host-side dependency on `tar` or
  `zstd`.
- Cross-platform install scripts (`install.sh`, `install.ps1`) that
  fetch the right release archive for the host OS / arch.
- GitHub Actions workflows: `ci.yml` (build + vet on every push) and
  `release.yml` (cross-compile for linux/darwin/windows × amd64/arm64
  on tag push, generate `checksums.txt`, attach to a GitHub Release).
- MIT license.
