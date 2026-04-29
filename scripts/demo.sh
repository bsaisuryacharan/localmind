#!/usr/bin/env bash
# Records the localmind happy-path demo as an asciinema cast.
#
# Usage:
#   asciinema rec scripts/demo.cast -c "bash scripts/demo.sh"
#
# Then optionally convert to gif:
#   agg scripts/demo.cast scripts/demo.gif
#
# Total runtime: ~90 seconds on a warm machine, ~3 minutes from cold.

set -e

say() {
  printf '\n\033[1;36m%s\033[0m\n' "$*"
  sleep 1.5
}

# 1. Install
say "==> 1/5 install localmind (one line)"
echo 'curl -fsSL https://raw.githubusercontent.com/bsaisuryacharan/localmind/main/install.sh | sh'
sleep 2

# 2. Init
say "==> 2/5 detect hardware and configure"
localmind init
sleep 1

# 3. Up
say "==> 3/5 bring the docker stack up (open-webui, ollama, mcp gateway, ...)"
localmind up --no-profile
sleep 2

# 4. Wait for WebUI
say "==> 4/5 wait for the web UI"
until curl -fsSL http://localhost:3000 > /dev/null 2>&1; do
  printf '.'
  sleep 1
done
echo
sleep 1

# 5. Show one query
say "==> 5/5 ask the model a question (via curl, mirrors what the WebUI does)"
echo '{"model":"qwen2.5:3b","prompt":"explain MCP in 1 sentence","stream":false}' \
  | curl -fsS -X POST http://localhost:11434/api/generate \
       -H 'Content-Type: application/json' \
       --data @- \
  | python3 -c 'import sys,json; r=json.load(sys.stdin); print(r["response"])'

say "==> done. open http://localhost:3000 in your browser."
