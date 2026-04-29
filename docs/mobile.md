# Mobile and remote access

The whole stack is web-based, so once you can reach `localhost:3000` from
elsewhere, your phone gets the same experience as your laptop. localmind
ships two helpers for the common cases.

## TL;DR

```bash
localmind responder install   # tiny HTTP service that always answers, wakes the stack on demand
localmind keepalive on        # don't let the laptop sleep while we're listening
localmind tunnel start 7900   # expose the responder (port 7900), not the WebUI directly
localmind tunnel status       # print the URL
```

Open the printed URL on your phone. The responder will route you through to the WebUI, bringing it up first if the stack isn't already running.

## What each piece does

### `localmind responder`

A tiny HTTP service that runs on the host (not in docker) and answers
even when the docker stack is down.

```bash
localmind responder install     # register to start at user login
localmind responder status      # is it installed and running?
localmind responder uninstall   # remove it
localmind responder run         # foreground; usually called by the OS service unit
```

Endpoints:

| Path        | Method | Returns |
|-------------|--------|---------|
| `/healthz`  | GET    | `ok` (200) — proves the responder itself is up |
| `/status`   | GET    | JSON: is the docker stack reachable? what's the WebUI URL? |
| `/wake`     | POST   | brings the stack up if down; blocks until reachable (≤60 s) |

OS install mechanism:

| OS      | Backend                                                              |
| ------- | -------------------------------------------------------------------- |
| macOS   | `~/Library/LaunchAgents/dev.localmind.responder.plist` + `launchctl` |
| Linux   | `~/.config/systemd/user/localmind-responder.service` + `systemctl --user` |
| Windows | `HKCU\...\Run\LocalmindResponder` registry value (no admin needed)   |

The responder is the answer to "what does my phone hit when the laptop
just woke up and the docker stack is still cold?" — your phone gets a
fast HTTP 200 from `/status`, sees `stack_running: false`, calls
`/wake`, and gets the WebUI URL back as soon as the stack is reachable.

### `localmind tunnel`

Wraps `tailscale funnel` so you don't have to remember the syntax.
Funnel is Tailscale's feature for exposing a local port at a public
HTTPS URL with a real cert, accessible from anywhere — no port
forwarding, no public IP, end-to-end encrypted.

Prerequisites:

- Tailscale installed and logged in (`tailscale up`).
- Funnel enabled for your tailnet. Run `tailscale funnel check` to verify
  or enable in the [admin console](https://login.tailscale.com/admin/dns).

Subcommands:

```bash
localmind tunnel start [port]    # default port 3000 (the Open WebUI)
localmind tunnel status          # show whether it's up + the URL
localmind tunnel stop            # disable funnel
```

### `localmind keepalive`

Prevents the host from sleeping. Without it, closing your laptop lid
turns the responder off and your phone can't reach the WebUI.

```bash
localmind keepalive on
localmind keepalive status
localmind keepalive off
```

Mechanism per OS:

| OS      | Tool                                                              |
| ------- | ----------------------------------------------------------------- |
| macOS   | `caffeinate -d`                                                   |
| Linux   | `systemd-inhibit --what=sleep:idle:handle-lid-switch`             |
| Windows | `SetThreadExecutionState(ES_SYSTEM_REQUIRED \| ES_AWAYMODE_REQUIRED)` |

Costs: the laptop draws ~10–20 W more than when sleeping. Fine plugged
in, edge case unplugged.

## What about *real* sleep?

A genuinely sleeping laptop (S3 / hibernate) cannot run code; the CPU is
off. Modern Standby (S0ix on Windows, similar on Linux/macOS) can run
*lightweight* services on ~1–2 W of power but not GPU inference.

The roadmap has a richer answer:

1. **Lightweight responder service** registered with the OS as
   background-capable. Stays alive in Modern Standby. Receives mobile
   requests, queues them, wakes the rest of the stack via
   `SetThreadExecutionState` (Windows) / `caffeinate -u` (macOS) /
   `rtcwake` (Linux). Optionally answers from a tiny 1.5B draft model
   while the heavy stack warms up.
2. **Wake-on-LAN companion** for older hardware that only supports S3
   sleep. A Raspberry Pi or tiny VPS receives the request, sends a WoL
   packet to wake the laptop, queues the request, returns the answer.

Neither has shipped yet. For now, `keepalive on` is the practical
answer. Track progress in the GitHub issues tagged `mobile`.

## Putting it together

```bash
# install + initial setup (one-time)
curl -fsSL https://raw.githubusercontent.com/bsaisuryacharan/localmind/main/install.sh | sh
localmind init
localmind up

# install the responder so the host always answers, even when docker is cold
localmind responder install

# keep the laptop awake (until Modern-Standby support lands)
localmind keepalive on

# expose the responder publicly (NOT port 3000 — let the responder gate access)
localmind tunnel start 7900

# print the URL
localmind tunnel status
```

Save the URL on your phone's home screen. After tapping "Add to Home
Screen" on iOS or "Install app" on Android, the page opens in a
standalone window with no browser chrome. The PWA manifest at
`/manifest.json` declares the app metadata. The phone always hits the
responder first; the responder either proxies you to a running stack
or wakes one for you. With `keepalive on`, the laptop stays available
indefinitely while plugged in.
