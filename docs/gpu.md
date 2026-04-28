# GPU notes

`localmind init` auto-detects your accelerator and writes the active profile into `.env` and `models.yml`. You can override by editing those files and running `localmind up` again.

## NVIDIA (Linux, Windows-WSL)

Install the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html). Then:

```bash
docker run --rm --gpus all nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi
```

If that prints your GPU table, `localmind up` will pass `--gpus all` to Ollama via `compose/compose.gpu.nvidia.yml`.

| VRAM   | Default chat model        |
| ------ | ------------------------- |
| 8 GB   | `qwen2.5:7b`              |
| 12 GB  | `qwen2.5:14b-instruct-q4` |
| 24 GB+ | `qwen2.5:32b-instruct-q4` |

## Apple Silicon

Docker Desktop on macOS does **not** pass the Apple GPU into containers. The recommended setup is:

1. `brew install ollama` and `brew services start ollama`. This runs Ollama natively with full Metal acceleration.
2. `localmind up` — the Apple overlay points the Web UI and MCP gateway at `host.docker.internal:11434` instead of starting a containerized Ollama.

| Unified RAM | Default chat model |
| ----------- | ------------------ |
| 16 GB       | `qwen2.5:7b`       |
| 32 GB+      | `qwen2.5:14b`      |

## CPU only

Default profiles fit comfortably on CPU. Expect ~4–8 tokens/sec on a modern laptop with `qwen2.5:3b`.

## AMD / ROCm

Not yet supported. PRs welcome — the path is an extra `compose/compose.gpu.amd.yml` overlay plus a `rocm`-tagged Ollama image.
