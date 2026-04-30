# Phone from anywhere

End-to-end setup guide for the v0.2 mobile experience: a phone bookmark
that works from anywhere — coffee shop, airport, in-laws' wifi — and
that wakes your laptop on demand even when the lid is closed.

## What you'll have when you're done

A bookmark on your phone that loads localmind's Open WebUI from anywhere
in the world, even when your laptop is asleep with the lid closed.
Traffic is end-to-end encrypted between your phone and your laptop:
WireGuard with Curve25519 + ChaCha20-Poly1305, no third party in the
data path. Tailscale's coordination server sees your devices' public
keys and connection times — never traffic content. For the full
analysis, see [docs/threat-model.md](threat-model.md).

## Prereqs

- A free [Tailscale](https://tailscale.com/) account (Personal plan is
  fine; you'll have at most two devices on it: laptop + phone).
- A laptop with **Modern Standby** support. On Windows, run
  `powercfg /a` from an admin terminal — you should see
  `Standby (S0 Low Power Idle) Network Connected` in the list of
  available sleep states. If it only lists S3, see "Honest limits"
  below.
- Windows admin rights for the responder service install (Linux/macOS
  only need a regular user account).
- Docker Desktop (Windows/macOS) or Docker Engine (Linux) installed
  locally and configured to start at boot.

## Setup, in order

### Step 1: install Tailscale on the laptop

```bash
# Windows
winget install tailscale.tailscale

# macOS
brew install --cask tailscale

# Linux
curl -fsSL https://tailscale.com/install.sh | sh
```

Sign in with your Tailscale account. The Tailscale tray icon should go
from grey to coloured.

### Step 2: join the tailnet

```bash
localmind tunnel join
```

This is the **new v0.2 default**: peer-to-peer mode. It registers the
laptop with your tailnet (no public URL, no Funnel, no third party in
the data path) and prints your laptop's tailnet hostname plus its
tailnet IPv4/IPv6 — something like `localmind-laptop.tail0b9d07.ts.net`.
Save that hostname; you'll bookmark it on your phone in step 5.

> Note: `localmind tunnel start` from v0.1 still works but now prints a
> deprecation note. Funnel mode (the old default) lives at
> `localmind tunnel funnel` for the rare cases you actually need a
> public URL — e.g. sharing with a friend who isn't on your tailnet.
> See [docs/threat-model.md](threat-model.md) for why peer-to-peer is
> stronger.

### Step 3: install Tailscale on the phone

Install the Tailscale app from the App Store or Play Store. Sign in with
the same account you used on the laptop. The app should show your
laptop in the device list with a green dot.

### Step 4: install the responder as an OS service

```bash
# Windows: run from an elevated PowerShell / cmd
localmind responder install

# macOS / Linux
localmind responder install
```

On Windows, this registers a real Windows Service named
`LocalmindResponder` that survives logout and stays alive in Modern
Standby (S0ix). **The install must be elevated.** If you run it from a
non-admin shell, it falls back to the v0.1 mechanism (an `HKCU\...\Run`
registry value) and prints a warning that Modern Standby wake won't
work in fallback mode — your phone can still reach the laptop while
you're logged in and the screen is on, but a closed-lid sleeping laptop
will not respond.

On macOS, this writes a launchd user agent. On Linux, a systemd `--user`
unit. Both are non-elevated.

### Step 5: open the URL on your phone

In your phone's browser, visit:

```
https://localmind-laptop.tail0b9d07.ts.net/
```

(substitute the hostname `localmind tunnel join` printed in step 2).

First load runs the Open WebUI admin signup; pick a username and
password. Then "Add to Home Screen" (iOS) or "Install app" (Android) to
get a standalone PWA window with no browser chrome.

## Power settings checklist

A few Windows settings determine whether closed-lid sleep can be
woken by an incoming Tailscale request.

- **Modern Standby must be enabled.** From an admin terminal,
  `powercfg /a` should list `Standby (S0 Low Power Idle) Network
  Connected`. If it lists S3 or only "The system firmware does not
  support …", your laptop or BIOS doesn't expose Modern Standby; see
  "Honest limits" below.
- **Use the "Balanced" power plan, not "High performance."** "High
  performance" disables S0ix on many vendor BIOSes — the laptop will
  refuse to enter the low-power idle state and your battery will burn
  through overnight.
- **Check BIOS for a Modern Standby toggle.** Some Lenovo / HP / Dell
  laptops ship with Modern Standby disabled at the firmware level.
  Enabling it sometimes requires a BIOS firmware update plus a reboot;
  consult your vendor's support page.
- **Don't use third-party "battery saver" tools** that aggressively
  kill background services. They will kill the responder service and
  your phone won't be able to reach the laptop.

## Verifying it works (the demo)

1. Close the laptop lid. Wait ~2 minutes for it to actually enter
   Modern Standby (you can verify with `powercfg /sleepstudy` after
   the fact).
2. On your phone, tap the localmind bookmark.
3. Expect a 10-25 second delay on the **first** wake while:
   - Tailscale routes the request to the responder service.
   - The responder calls `/wake`, which boots Docker Desktop.
   - The responder waits for Open WebUI to be reachable on `localhost:3000`.
4. The page loads. Subsequent wakes (within an hour or so, before
   Docker Desktop tears itself back down) are noticeably faster.

The wake budget on Windows is 90s in v0.2 (up from 60s in v0.1) to
accommodate Docker Desktop's cold start. If `/wake` times out, check
that Docker Desktop is set to "Start Docker Desktop when you log in"
in its settings.

## Honest limits

- **Laptops without Modern Standby.** S3-only laptops physically cannot
  run code while sleeping — the CPU is off. The planned v0.3 path uses
  a Wake-on-LAN companion (a Pi or always-on home box that receives
  the request, sends a magic packet to wake the laptop, and proxies
  the response). Not yet shipped.
- **Corporate laptops where IT disabled S0ix.** Same path as above;
  there's nothing localmind can do from userspace.
- **macOS.** PowerNap is much more limited than Windows Modern
  Standby — it can run scheduled iCloud syncs and Time Machine backups
  but does not reliably keep a long-lived HTTP listener responsive.
  In practice, the macOS path needs the lid open or an external
  display attached to keep the laptop in a wake-capable state.
- **The one third party in the picture.** Tailscale's coordination
  server sees your devices' public keys, login email, and connection
  times. It cannot read traffic content (WireGuard is end-to-end). For
  users who want zero third parties at all, see
  [docs/headscale.md](headscale.md) for the self-hosted alternative.
