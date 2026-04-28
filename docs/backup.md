# Backup

`localmind backup` snapshots all user state into a single zstd-compressed tarball.

```bash
localmind backup                                 # ./localmind-backup-<UTC>.tar.zst
localmind backup ./snapshots/q1-archive.tar.zst  # custom path
```

## What's inside

The archive captures the four docker volumes that hold all persistent state:

| Volume                | Contents                                          |
| --------------------- | ------------------------------------------------- |
| `localmind_ollama`    | Pulled models, model state                        |
| `localmind_webui`     | Open WebUI database, accounts, chat history       |
| `localmind_piper`     | TTS voice files                                   |
| `localmind_mcp_index` | localmind RAG index (`index.json`)                |

Each volume's contents are placed under `./<volume_name>/` inside the archive.

## How it works

The CLI launches a throwaway `alpine:3.20` container with all four volumes
mounted read-only at `/backup/<volume>` and the destination directory mounted
writable at `/out`. Inside the container:

```sh
tar -cf - -C /backup . | zstd -T0 -19 > /out/<filename>
```

This means: no host-side dependency on `tar` or `zstd`, identical behavior on
Linux/macOS/Windows hosts that have Docker, and the running stack is never
paused — but read-only mounts ensure a consistent archive.

## Inspecting an archive

```bash
zstd -dc localmind-backup.tar.zst | tar -tf -                 # list
zstd -dc localmind-backup.tar.zst | tar -xf - -C ./extracted  # extract
```

## Restore

Restore is **not yet implemented**. To restore manually:

1. Stop the stack: `localmind down`
2. Remove existing volumes (this deletes data): `docker volume rm localmind_ollama localmind_webui localmind_piper localmind_mcp_index`
3. Recreate empty volumes: `docker volume create localmind_ollama` (etc.)
4. Run an alpine container with the volumes mounted writable at `/restore/<volume>` and the archive available at `/in/archive.tar.zst`, then `cd /restore && zstd -dc /in/archive.tar.zst | tar -xf -`
5. `localmind up`

A first-class `localmind restore <archive>` command will land in a future iteration.

## Constraints

- Requires `docker` on PATH.
- Requires the stack to have been started at least once (the named volumes must exist) — `localmind backup` errors out with a hint to run `localmind up` first if any are missing.
- The archive can be GBs in size — Ollama models are large. Compress to an external drive if disk-tight: `localmind backup /mnt/external/lm.tar.zst`.
