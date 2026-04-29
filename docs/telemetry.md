# Telemetry (opt-in design)

**Status:** design only. Not implemented. Endpoint URL TBD, ship date TBD.

## Why telemetry

localmind v0.0.6 has shipped, and we have no idea who's using it or how. Every roadmap call is a guess: is the OCR sidecar actually used? Does anyone hit the responder `/wake` from a phone, or is everyone running locally? What's the median chunk count in the index — 100, 10k, a million? One real data point a week beats zero. Without numbers we will keep building features for an imagined user.

This is **opt-in only. Default is off.** Nothing ships until the user explicitly says yes.

## What we'd send

| event          | when                                              | data |
| -------------- | ------------------------------------------------- | ---- |
| `boot`         | `localmind up` succeeds                           | OS+arch, RAM bucket (`8-16` / `16-32` / `32-64` / `64+` GB), profile name, time-since-init bucket |
| `model_pull`   | first ollama pull for a model completes           | model id, size bucket, pull-duration bucket |
| `index_grow`   | indexer ingestion checkpoint, max once per hour   | chunk count bucket, file count bucket, file-extension distribution |
| `wake_call`    | responder `POST /wake` completes                  | wake-duration bucket, `was_already_up` bool |
| `responder_install` | `localmind responder install` succeeds       | OS, install backend (launchd / systemd-user / registry) |
| `tunnel_start` | `localmind tunnel start` succeeds                 | port (3000 vs 7900), funnel-vs-serve |
| `error`        | Doctor reports a FAIL                             | check name, OS — **no error message body, no path, no command output** |

Buckets, not raw numbers, on everything quantitative. RAM is bucketed because the raw value is close to a fingerprint when crossed with OS and arch.

## What we'd never send

Emphatically out of scope:

- file contents — anything under `data/`
- chat history — anything in the WebUI volume
- indexed text — chunks, embeddings, source paths
- model output — completions, tool-call arguments, prompts
- IP addresses, MAC addresses, hostnames
- absolute paths, anything under `~`, repository names
- environment variables, except literally the value of `LOCALMIND_PROFILE`
- the value of any token or secret env var (`LOCALMIND_RESPONDER_TOKEN`, `LOCALMIND_MCP_TOKEN`, anything matching `*_TOKEN`/`*_KEY`/`*_SECRET`)
- user names, account names, git config name/email
- install ID, machine ID, any stable per-host identifier — events are independent rows with no join key

## Architecture sketch

```
  hooks at event sites ──▶  ~/.localmind/telemetry/events.ndjson  (rolling, 100KB cap)
                                       │
                                       │ once per day, on `localmind up`
                                       ▼
                          flush goroutine in the wizard
                          (gzip, POST JSON, 3× retry, then drop)
                                       │
                                       ▼
                       https://telemetry.localmind.dev/v1/ingest  (TBD)
                          (Cloudflare Worker → SQLite, MIT-licensed)
                                       │
                                       │ also append-only mirror
                                       ▼
                          ~/.localmind/telemetry/sent.ndjson  (never auto-deleted)
```

- Events buffered locally at `~/.localmind/telemetry/events.ndjson`. Rolling 100KB cap; oldest events drop when the cap is hit. Telemetry must never grow without bound.
- Once a day, on `localmind up`, a small flush goroutine in the wizard gzips the buffer and `POST`s it as JSON to a fixed endpoint we control. Endpoint TBD; cheapest plausible answer is a Cloudflare Worker fronting a SQLite file in R2.
- Flush failures retry up to 3× with exponential backoff, then drop the buffer. We'd rather lose data than block `localmind up` or stack telemetry across days.
- Every successfully-flushed event is also appended to `~/.localmind/telemetry/sent.ndjson`. That file is never auto-deleted. `cat` it any time to see exactly what we know.

CLI surface:

```bash
localmind telemetry status     # show on/off, queued event count, last flush time
localmind telemetry flush      # push the buffer now
localmind telemetry purge      # delete events.ndjson (sent.ndjson is left alone)
localmind telemetry on         # set LOCALMIND_TELEMETRY=on in .env
localmind telemetry off        # set LOCALMIND_TELEMETRY=off in .env
```

## Opt-in mechanism

On first `localmind init`, after the hardware profile prints, ask exactly:

```
Help us prioritize features by sending anonymous usage stats?
Stays off by default — see docs/telemetry.md for what we'd send.
(y/N):
```

Empty / anything-not-`y`/`Y` is treated as no. We write `LOCALMIND_TELEMETRY=on` or `LOCALMIND_TELEMETRY=off` to `.env`. Toggle later with `localmind telemetry on|off`. The wizard checks the env var on every event-site hook; an empty/unset value is treated as off.

## Auditability

- `~/.localmind/telemetry/sent.ndjson` is the user's local audit trail. One JSON object per line, newest at the bottom, never auto-deleted. If the user is suspicious about telemetry, this is where to look.
- The collector serves a public `https://telemetry.localmind.dev/dump.json` (URL TBD), refreshed weekly, with the entire dataset. Anyone can inspect what's actually been collected — not just localmind users. This is what makes "trust us, it's coarse" verifiable.
- We never join events back to a user. There is no install ID, no session ID, no IP retention. Events are completely independent rows; right-to-be-forgotten on the server is structurally a no-op because there's nothing to forget against.

## Compliance

- **GDPR:** opt-in lawful basis (Art. 6(1)(a) consent). No PII collected, so no DSAR machinery needed. `localmind telemetry purge` deletes the local buffer. The remote dataset has no identifier so erasure requests are inherently satisfied.
- **License + transparency:** the collector is open-source at `bsaisuryacharan/localmind-telemetry`, MIT-licensed, same as localmind. Ingestion schema, storage layout, and `dump.json` query live in that repo. PRs welcome.

## Why not Posthog / Plausible / Mixpanel

Each of these would be cheaper than rolling our own. They also tie events to install IDs and IP addresses by default — even with privacy mode on, the vendor still sees the IP at request time. localmind's whole pitch is "self-hosted, your data stays local, no cloud." Routing telemetry through a third-party SaaS contradicts that on the very first run. Rolling our own collector is more work, but the contract is then auditable end-to-end: open-source ingest, public `dump.json`, no per-user identity. The premium pays for the project's credibility.

## Implementation TODO

Concrete files / PRs once this design is accepted:

- `wizard/internal/telemetry/` — `Event{Name string; Data map[string]any; TS time.Time}`, `Buffer.Append/Flush/Status/Purge`, the gzip+POST client, the redaction allowlist.
- `wizard/internal/wizard/telemetry.go` — `localmind telemetry on|off|status|flush|purge` CLI subcommands.
- `wizard/cmd/localmind/main.go` — dispatcher entry for the new subcommand.
- Hooks at the seven event sites listed in [What we'd send](#what-wed-send): `up`, model pull, indexer checkpoint, responder `/wake`, `responder install`, `tunnel start`, doctor FAIL.
- `localmind init` — append the opt-in prompt after the profile-summary block; persist `LOCALMIND_TELEMETRY` to `.env`.
- New repo `bsaisuryacharan/localmind-telemetry` — Cloudflare Worker, SQLite schema, `dump.json` generator, MIT license.
