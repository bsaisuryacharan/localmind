# Troubleshooting

Run `localmind doctor` first — it checks for the most common problems.

## `docker compose version` fails

You have the legacy `docker-compose` (v1) instead of the v2 plugin. Install Docker Engine 24+ or Docker Desktop and re-run.

## Web UI shows "no models"

The first model pull happens on initial `localmind up` and can take several minutes for a 7B–14B model. Watch progress with:

```bash
docker logs -f localmind-ollama
```

If it hangs, check disk space (`df -h`).

## NVIDIA GPU not detected

```bash
docker run --rm --gpus all nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi
```

If that fails, the host does not have the NVIDIA Container Toolkit. See `docs/gpu.md`.

## Apple GPU not used / very slow on Mac

Docker Desktop cannot use Metal. Run Ollama natively:

```bash
brew install ollama
brew services start ollama
localmind down && localmind up
```

The Apple overlay (`compose/compose.gpu.apple.yml`) points the Web UI at the host Ollama.

## Port already in use

Edit `.env` and change the relevant `*_PORT` variable, then `localmind down && localmind up`.

## Reset everything

```bash
localmind down
docker volume rm localmind_ollama localmind_webui localmind_piper localmind_mcp_index
localmind init
localmind up
```
