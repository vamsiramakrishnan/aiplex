#!/usr/bin/env bash
# Starts everything the browser checks need and leaves it running:
#   - tools/consolee2e: the local API (in memory) with seeded data, a no-role caller and a deployer
#   - e2e/serve.mjs: the built Console on :19090, proxying to the API
# then generates traffic. Usage (from the repository root, after `make build` and `cd console && npm run build`):
#   console/e2e/run.sh start [workdir]     # prints the harness file path
#   console/e2e/run.sh stop  [workdir]
set -euo pipefail
cmd="${1:-start}"
work="${2:-${TMPDIR:-/tmp}/aiplex-console-e2e}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
mkdir -p "$work"
case "$cmd" in
  start)
    "$0" stop "$work" >/dev/null 2>&1 || true
    [ -x "$root/bin/aiplex-api" ] || (cd "$root" && make build)
    go build -o "$root/bin/consolee2e" "$root/tools/consolee2e"
    [ -f "$root/console/built/index.html" ] || (cd "$root/console" && npm ci && npm run build)
    nohup "$root/bin/consolee2e" --port "${API_PORT:-18080}" --api-bin "$root/bin/aiplex-api" --console-origin "http://127.0.0.1:${CONSOLE_PORT:-19090}" >"$work/harness.json" 2>"$work/harness.log" &
    echo $! >"$work/harness.pid"
    for _ in $(seq 1 60); do [ -s "$work/harness.json" ] && break; sleep 0.5; done
    [ -s "$work/harness.json" ] || { echo "harness did not start; see $work/harness.log" >&2; exit 1; }
    nohup node "$root/console/e2e/serve.mjs" --api "http://127.0.0.1:${API_PORT:-18080}" --port "${CONSOLE_PORT:-19090}" >"$work/serve.log" 2>&1 &
    echo $! >"$work/serve.pid"
    sleep 1
    AIPLEX_BIN="$root/bin/aiplex" node "$root/console/e2e/traffic.mjs" "$work/harness.json" >"$work/traffic.log"
    echo "$work/harness.json"
    ;;
  stop)
    for f in harness serve; do
      [ -f "$work/$f.pid" ] && kill "$(cat "$work/$f.pid")" 2>/dev/null || true
      rm -f "$work/$f.pid"
    done
    pkill -f "$root/bin/aiplex-api" 2>/dev/null || true
    ;;
  *) echo "usage: $0 start|stop [workdir]" >&2; exit 2 ;;
esac
