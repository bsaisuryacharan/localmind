# Releasing

Releases are produced by `.github/workflows/release.yml`. The workflow cross-compiles the `localmind` binary on a Linux runner and uploads archives that match exactly what `install.sh` and `install.ps1` look for.

## Cut a release

```bash
# 1. Tag locally. Use semver.
git tag v0.0.1

# 2. Push the tag. The workflow runs on push of any tag matching v*.
git push origin v0.0.1
```

The workflow does the rest:

1. Cross-compiles `localmind` for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, `windows/arm64`.
2. Archives each build (`tar.gz` for unix, `zip` for windows) with the exact filename the install scripts expect: `localmind-<os>-<arch>.{tar.gz,zip}`.
3. Generates `checksums.txt` (SHA-256).
4. Creates a GitHub Release with auto-generated notes and attaches all archives.

## Test the workflow without cutting a release

Use the `workflow_dispatch` trigger on the Actions tab. Leave the `tag` input blank to build artifacts and upload them as a workflow artifact (no release is created).

## Build locally

If `make` is installed:

```bash
make dist        # cross-compile everything into dist/
make build       # local-only build for current OS/arch into bin/
```

Otherwise, mirror the workflow:

```bash
cd wizard
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w -X main.version=local" \
  -o ../bin/localmind ./cmd/localmind
```

## Versioning rules of thumb

- Patch (`v0.0.x`): bug fixes, doc-only changes.
- Minor (`v0.x.0`): new commands, new MCP tools, breaking changes to `models.yml` or `.env` keys.
- Major (`v1.0.0`): once we declare the install command stable for non-developers.

## What goes into a release

- The `localmind` CLI binary (the wizard).
- Nothing from `mcp/` — that runs as a docker container built from source on the user's machine via `docker compose build`.
