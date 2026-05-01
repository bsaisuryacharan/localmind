#!/usr/bin/env bash
# docker-entrypoint.sh — exec CMD as PID 1 so SIGTERM/SIGINT propagate
# directly to uvicorn for clean shutdown.
set -euo pipefail
exec "$@"
