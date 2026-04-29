# Contributing to localmind

Thanks for considering a contribution. localmind is opinionated glue
around a stack of well-loved OSS pieces (Ollama, Open WebUI,
faster-whisper, Piper, an MCP gateway). The goal is a single command
that brings up a sensible self-hosted AI box on a laptop or a small
server, with hardware-aware defaults, a stable mobile entry point, and
a one-shot backup. If a change moves the project closer to that goal
without bloating the install footprint, it is probably welcome.

## Local dev setup

You need:

- **Go 1.22+** — both `wizard/` and `mcp/` are Go modules.
- **Docker Desktop** (or Docker Engine on Linux) — the AI services run
  as containers. The wizard shells out to `docker compose`.
- **git** — obvious.
- *(optional)* `make` if you want the convenience targets in the
  `Makefile`.

Clone and build:

```bash
git clone https://github.com/bsaisuryacharan/localmind.git
cd localmind

# host-side CLI (the wizard)
cd wizard
go build ./...
cd ..

# MCP gateway (runs in docker normally, but build it locally to iterate)
cd mcp
go mod tidy
go build ./...
cd ..
```

Run the stack the same way an end user would, against your local checkout:

```bash
go run ./wizard/cmd/localmind init
go run ./wizard/cmd/localmind up
```

## Tests

The existing test surface is small — there are **no Go tests yet** at
the time this guide was written. A first round of tests is landing under
the `feat/tests` branch; once that merges, the standard commands will
work:

```bash
cd wizard && go test ./...
cd mcp    && go test ./...
```

Until then, smoke-test changes by running `localmind up`, hitting the
WebUI at `http://localhost:3000`, and exercising the MCP gateway via
`scripts/smoke.sh` if applicable.

## Branch naming

Branch off `main` with one of these prefixes:

- `feat/<short-name>` — new functionality
- `fix/<short-name>` — bug fixes
- `docs/<short-name>` — documentation only

Keep branches focused. One topic per branch makes review faster and PR
revert easier.

## Commit messages

Short imperative subject (under ~65 chars), blank line, then 2–4
sentences explaining the approach and the reason. Look at recent commits
for tone:

```bash
git log --oneline
git log -5
```

Example:

```
mcp: debounce fsnotify writes by 500ms

Editors trigger 4–8 write events per save. Re-embedding on each one
wasted Ollama time and produced duplicate index rows. Coalesce events
on a per-path timer; fall back to the 30s safety rescan if fsnotify
drops anything.
```

Avoid noise commits ("wip", "fix typo"); squash them locally before
pushing.

## Pull request checklist

Before opening a PR, please confirm:

- [ ] CI is green on your branch.
- [ ] No new third-party dependencies added under `wizard/` (the wizard
      stays stdlib-first; new deps require justification in the PR
      description).
- [ ] Tests added for new behavior, **or** the PR explicitly notes that
      tests are deferred until `feat/tests` lands.
- [ ] Docs updated for any user-facing change (`README.md`,
      `docs/*.md`, command help text).
- [ ] `CHANGELOG.md` has a line under `## [Unreleased]` describing the
      change.

## Code style

- **Stdlib first.** Both modules avoid heavy dependencies on principle.
  If you need an HTTP client, use `net/http`. If you need JSON, use
  `encoding/json`. Add a dep only if the stdlib answer is genuinely
  worse.
- **Follow existing patterns.** `wizard/internal/` and `mcp/internal/`
  are the reference for package layout, error handling, and test style.
  When in doubt, mirror what the surrounding code does.
- **`go fmt` + `go vet`** must pass. CI enforces this.
- **No global state in new code.** The wizard has some legacy globals;
  please don't add more.

## Sign-off

Developer Certificate of Origin (DCO) sign-off is **not required** for
v0.0.x. If the project grows or attracts external contributors at scale
we may revisit; for now, a regular `git commit` is fine.

## Good first issues

Look for the [`good first issue`](https://github.com/bsaisuryacharan/localmind/labels/good%20first%20issue)
label on the issue tracker for things sized for a first contribution.
If you don't see one that fits, open a discussion and we'll find
something.
