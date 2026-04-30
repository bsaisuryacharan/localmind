# Self-hosted Headscale

How to swap Tailscale's coordination server for self-hosted
[Headscale](https://headscale.net/), so that no third party sees even
the metadata of your tailnet.

## Why

The default localmind setup is end-to-end encrypted between your phone
and your laptop, but Tailscale's coordination server still sees your
devices' public keys, your login email, and connection timestamps. For
most users that's an acceptable trade-off; for users who want **zero
third parties at all**, Headscale removes the last one.

## Architecture

Headscale is an open-source re-implementation of Tailscale's
coordination server, MIT-licensed. It speaks the same protocol as
Tailscale's SaaS coord, so the **same official Tailscale clients** on
your laptop and phone can be pointed at your Headscale URL instead of
`login.tailscale.com`. The WireGuard end-to-end encryption between
clients is unchanged — only the key-exchange / device-discovery layer
moves to your hardware.

```
Tailscale default:    laptop ──[WG]── phone
                        │              │
                        └─── coord at login.tailscale.com (Tailscale Inc.)

With Headscale:       laptop ──[WG]── phone
                        │              │
                        └─── coord at headscale.example.com (yours)
```

## Quick install

On a small always-on machine — a $5/mo VPS, a home Raspberry Pi, or
similar:

```bash
docker run -d --name headscale \
  -v /etc/headscale:/etc/headscale \
  -p 8080:8080 \
  headscale/headscale:stable headscale serve
```

You'll need to put a TLS-terminating reverse proxy (Caddy, nginx,
Traefik) in front of port 8080 with a real cert (Let's Encrypt is
fine). The official Headscale install docs at <https://headscale.net/>
walk through the binary install path if you don't want Docker on the
coord box.

Create a user and pre-auth key:

```bash
docker exec headscale headscale users create me
docker exec headscale headscale preauthkeys create --user me --reusable
```

## Point Tailscale clients at it

**Laptop:**

```bash
tailscale up --login-server https://headscale.example.com \
             --auth-key <preauth-key-from-above>
```

Then run `localmind tunnel join` as usual; localmind doesn't care
which coord the Tailscale client is registered against.

**Phone:**

In the Tailscale app: tap the three-dot menu → Settings → "Use
alternate coordination server" → enter your Headscale URL → sign in
with another preauth key.

## Trade-offs

- **You operate the coord server.** Uptime, TLS cert renewal, and a
  reachable public IP are now your problem. If your coord goes down,
  existing peer-to-peer connections keep working but new device
  registrations and key rotations stall.
- **Some Tailscale SaaS features are unavailable.** Anything that
  depends on Tailscale Inc.'s control plane — SSH key signing,
  Funnel, Serve auto-cert — is not implemented in Headscale.
  Workaround: use bring-your-own TLS certs and plain WireGuard for
  any "share publicly" use cases. (Most localmind users on the
  peer-to-peer path don't need any of these.)
- **Mobile app polish.** Tailscale's iOS/Android apps work with
  Headscale, but the "alternate coordination server" UX is a few
  taps deeper than the default.

## Recommendation

Start with the default Tailscale path; it's the documented setup in
[docs/phone-from-anywhere.md](phone-from-anywhere.md) and works in
five minutes. Migrate to Headscale only when:

- You have a concrete reason to not trust Tailscale Inc. with
  metadata (regulatory, contractual, principle), **or**
- Tailscale's free-tier limits become a problem (3 users / 100 devices
  at the time of writing).

For most single-person setups, the Tailscale free tier is enough
forever and the metadata exposure is acceptable.
