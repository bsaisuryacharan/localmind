# Profiler

The first time you run `localmind up`, the profiler benchmarks your active chat
model on the actual hardware and writes a recommendation. It's automatic and
runs once.

## What it does

1. Waits for Ollama to come up (up to 60 s).
2. Resolves the active chat model from `.env` (`LOCALMIND_PROFILE`) and
   `models.yml`.
3. Pulls the model if not already pulled (a no-op once you've used it).
4. Sends one `/api/generate` request asking for a 50-token completion.
5. Computes tokens-per-second from `eval_count / eval_duration`.
6. Compares against thresholds and writes the result.

## Recommendation thresholds

| Measured tok/s | Recommendation                                                |
| -------------- | ------------------------------------------------------------- |
| < 5            | downgrade — pick the next-smaller profile in `models.yml`     |
| 5–30           | stay — your current profile is fine                           |
| > 30           | upgrade — your hardware can run the next-larger profile        |

The downgrade/upgrade ladder follows the obvious ordering inside each family
(cpu_low → cpu_mid → nvidia_12gb → nvidia_24gb on the discrete-GPU side,
apple_16gb → apple_32gb_plus for unified memory).

## Where the result lives

`.localmind/profile.json` at the repo root. The file is gitignored. Example:

```json
{
  "model": "qwen2.5:7b",
  "profile": "cpu_mid",
  "tokens_per_sec": 11.4,
  "recommendation": "stay",
  "timestamp": "2026-04-28T11:00:00Z"
}
```

## Re-running

The profiler skips itself if `.localmind/profile.json` already exists. To
re-run after changing models, hardware, or quantization:

```bash
localmind profile --force
```

## Skipping on `up`

```bash
localmind up --no-profile
```

Useful in CI or smoke tests where you don't want to download a model just to
measure throughput.

## Failure mode

If the profiler errors (Ollama unreachable, model pull failed, generation timed
out at the 5-minute budget), `localmind up` does **not** fail. The error is
logged and the stack stays running. Run `localmind profile` manually later to
retry.
