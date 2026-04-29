# Security policy

localmind is a small, self-hosted AI stack maintained as a side project.
We take security seriously, but please calibrate expectations: this is a
solo OSS effort, not a vendor with a 24/7 SOC.

## Supported versions

Only the latest minor release receives security fixes. Older minors are
unsupported — upgrade to the latest tag.

| Version | Supported          |
| ------- | ------------------ |
| v0.0.x  | Yes (latest patch) |
| < v0.0  | No                 |

## Reporting a vulnerability

Please **do not** file a public GitHub issue for security reports.

Preferred channel:

- **GitHub Security Advisories (private)** —
  <https://github.com/bsaisuryacharan/localmind/security/advisories/new>.
  This keeps the report private until a fix is ready and gives us a
  shared workspace for the disclosure.

Email fallback (use if you can't access advisories):

- **bsaisuryacharan@deloitte.com** — please put `localmind security` in
  the subject line so it doesn't get lost.

When reporting, include: a short description, the affected version /
commit, reproduction steps, and the impact you believe it has. A PoC is
appreciated but not required.

## Response timeline

This is a solo OSS project, so commitments are realistic, not enterprise:

- **Acknowledgement:** within 7 days of receipt.
- **Triage and fix-or-status update:** within 30 days. If a fix needs
  longer, we will say so explicitly with a rough ETA.
- **Coordinated disclosure:** we'll agree on a disclosure date with the
  reporter; default is 90 days from the initial report or release of a
  fix, whichever comes first.

Credit in the advisory and release notes is offered by default; tell us
if you'd prefer to remain anonymous.

## Threat model summary

localmind assumes a **trusted local network**. The defaults are tuned
for "my laptop on my home Wi-Fi" or a Tailscale tailnet, not the open
internet.

- The host-side `responder` and the in-stack `mcp` gateway are
  unauthenticated through v0.0.4. First-party auth on these endpoints
  is planned for v0.0.5; until then, do not bind them to a public
  interface.
- Open WebUI ships with its own multi-user auth — use it. Create
  accounts, set strong passwords, do not expose `:3000` directly.
- The recommended public-exposure path is **Tailscale Funnel** in
  front of the responder (port 7900). Funnel terminates TLS, the
  tailnet provides identity, and the responder gates wake actions.
- All inference runs locally inside Docker. No prompts or RAG content
  leave the machine unless you wire up an external integration
  yourself.
- Backups (`localmind backup`) are unencrypted zstd tarballs. Treat
  them as sensitive — they contain chat history and the RAG index.

## Out of scope

These categories are explicitly **not** considered vulnerabilities for
the purposes of this policy:

- Prompts, completions, or actions you take with a local AI model. You
  run the model; you own the output.
- Hardware or OS-level compromise of the host machine.
- Compromised upstream Docker images (Ollama, Open WebUI, faster-whisper,
  Piper, ocrmypdf). We pin tags but do not currently sign or scan them;
  a supply-chain hardening pass is on the roadmap.
- Misconfiguration that exposes services to an untrusted network against
  the documented guidance (e.g. binding `:3000` to `0.0.0.0` on a public
  IP without a tunnel).
- Denial of service from running models too large for the host. That's
  a sizing problem; see the profiler.

Thanks for helping keep localmind users safe.
