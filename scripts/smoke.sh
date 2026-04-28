#!/usr/bin/env bash
# Boots the smallest possible localmind subset (ollama + mcp), waits for both
# health endpoints, then tears down. Used in CI to catch compose regressions.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

cleanup() { docker compose down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker compose up -d ollama mcp

wait_for() {
  local url="$1" name="$2" tries=60
  while [ $tries -gt 0 ]; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      echo "ok: $name"
      return 0
    fi
    sleep 2
    tries=$((tries - 1))
  done
  echo "FAIL: $name not healthy at $url" >&2
  docker compose logs --tail=100
  return 1
}

wait_for "http://localhost:11434/api/tags" "ollama"
wait_for "http://localhost:7800/healthz"   "mcp"

echo "smoke ok"
