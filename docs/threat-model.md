# Threat model

This document describes localmind v0.2's privacy and security properties
honestly: what we promise, what we don't, and what an attacker against
each component can and cannot do.

## What we promise

- **End-to-end encryption between phone and laptop.** Traffic is
  encrypted on the phone, decrypted on the laptop, and at no point in
  between is it readable by a third party.
- **No plaintext at any third party in the data path.** With the
  default peer-to-peer tunnel, your messages, files, and model
  outputs never traverse a server that holds the keys to read them.
- **Bearer-token defense in depth.** Even if the tunnel layer is
  bypassed somehow, a separate `LOCALMIND_RESPONDER_TOKEN` gates
  access to the responder's wake endpoint.

## Tailscale peer-to-peer (default)

This is what `localmind tunnel join` configures.

- **Traffic crypto.** WireGuard with Curve25519 key exchange and
  ChaCha20-Poly1305 AEAD. Only the two endpoints (your phone and your
  laptop) hold the symmetric session keys.
- **Relay nodes (DERP).** Tailscale runs encrypted relay servers used
  when direct UDP between the phone and laptop isn't possible (NAT,
  carrier-grade NAT, etc.). DERP nodes see only encrypted WireGuard
  frames; they cannot decrypt traffic.
- **Coordination server.** Tailscale's coord at `login.tailscale.com`
  sees your devices' WireGuard public keys, login email (from your
  identity provider), human-readable device names, and connection
  timestamps. It does **not** see traffic content.
- **Trust assumption.** You trust Tailscale Inc. with metadata, not
  content.

## Tailscale Funnel (NOT default in v0.2)

Funnel publishes a localhost service at a public HTTPS URL.

- **TLS terminates at Tailscale's edge.** Tailscale Inc. runs the
  TLS endpoint, which means in principle Tailscale could observe the
  plaintext bytes of any request that traverses it.
- **When to use anyway.** Sharing your local UI with someone who isn't
  on your tailnet — e.g. demoing to a friend over coffee. For your
  own phone, prefer peer-to-peer.
- **How to invoke.** `localmind tunnel funnel` (renamed from
  `tunnel start` in v0.2). The default in v0.2 is peer-to-peer.

## Bearer token: `LOCALMIND_RESPONDER_TOKEN`

Defense in depth on top of the tunnel.

- **Threat addressed.** A device on your tailnet that gets compromised
  (or a Tailscale ACL misconfiguration) can otherwise reach the
  responder. The token forces a second factor: even with network
  access, you need the token to invoke `/wake`.
- **Configuration.** Set as an environment variable on the laptop
  before installing the responder service. The phone PWA stores it
  in `localStorage` after first-run pairing.
- **Comparison.** Constant-time string compare; does not leak length
  via timing.

## What an attacker who compromises Tailscale's coord could do

Worst-case scenario, fully assumed.

- **Could.** Register a new node into your tailnet, allowing them to
  reach your laptop's responder over the WireGuard mesh — at which
  point the bearer token is the only line of defense.
- **Could not.** Decrypt past traffic between your phone and laptop
  (WireGuard provides forward secrecy; coord never had the session
  keys). Inject traffic without holding the rogue node's WireGuard
  keys (the laptop will reject WireGuard packets that don't
  authenticate).
- **Mitigation.** Tailscale's admin console offers ACL `nodeAttrs` and
  signed-by-key device approval. With device approval enabled, new
  devices joining the tailnet must be approved manually before they
  can route traffic — neutralizing the rogue-node path.

## What we don't protect against

localmind is a privacy product, not an HSM. It does not defend against:

- **Physical access to the laptop.** Anyone with the unlocked laptop
  has the same access as you.
- **Kernel-level malware on the laptop.** A rootkit can read RAM,
  intercept WireGuard before encryption, or harvest the bearer token.
- **Side-channel attacks on the laptop's RAM.** Cold-boot attacks,
  Rowhammer, and similar are out of scope.
- **Malicious Open WebUI extensions** installed by you. The extension
  runs with the same trust as Open WebUI itself.
- **Compromised upstream Docker images.** localmind pins image tags
  but does not pin digests in v0.2. A compromised registry could ship
  a backdoored image.

## For users who want zero third parties at all

If you cannot accept Tailscale Inc. seeing connection metadata, see
[docs/headscale.md](headscale.md). You replace Tailscale's coord with
your own self-hosted Headscale instance. Same Tailscale clients (the
official binaries), same WireGuard end-to-end encryption, but the
coordination server runs on your own hardware (small VPS or always-on
home Pi). Trade-offs: you operate the coord (uptime, TLS cert
renewal); some Tailscale SaaS features (Funnel, Serve auto-cert) are
not available.
