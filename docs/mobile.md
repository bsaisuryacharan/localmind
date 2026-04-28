# Mobile and remote access

The whole stack is web-based, so once you can reach `localhost:3000` from
elsewhere, your phone gets the same experience as your laptop. localmind
ships two helpers for the common cases.

## TL;DR

```bash
localmind keepalive on    # don't let the laptop sleep while we're listening
localmind tunnel start    # publish the WebUI on a public HTTPS URL via Tailscale
localmind tunnel status   # print the URL
```

Open the printed URL on your phone. Done.

## What each piece does

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

# bring up mobile access
localmind keepalive on
localmind tunnel start

# get the URL
localmind tunnel status
```

Save the URL on your phone's home screen. It will be reachable as long
as the laptop is awake (and now, thanks to `keepalive`, it always is).
