#!/usr/bin/env bash
# Scripted first run of `aiplex dev`, used by `make demo` and CI.
#
# Builds the CLI and API, starts `aiplex dev` (mock model, mock MCP server,
# in-memory API on 127.0.0.1) with an isolated HOME, then does what the
# printed next steps say: a chat completion (JSON and streaming), an MCP tool
# call, `aiplex usage records`/`summary`, and the curl snippet from
# `aiplex connect`. Exits non-zero on the first failure. Needs Go and curl;
# no Docker, network or cloud account.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${DEMO_BIN:-$ROOT/bin}"
PORT="${DEMO_PORT:-18080}"
WORK="$(mktemp -d)"
DEV_PID=""

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
fail() { printf '\nDEMO FAILED: %s\n' "$*" >&2; [[ -f "$WORK/dev.out" ]] && { echo "--- aiplex dev output ---" >&2; cat "$WORK/dev.out" >&2; }; [[ -f "$WORK/home/.aiplex/dev-api.log" ]] && { echo "--- API log (tail) ---" >&2; tail -40 "$WORK/home/.aiplex/dev-api.log" >&2; }; exit 1; }
cleanup() {
    if [[ -n "$DEV_PID" ]] && kill -0 "$DEV_PID" 2>/dev/null; then
        kill -INT "$DEV_PID" 2>/dev/null || true
        wait "$DEV_PID" 2>/dev/null || true
    fi
    rm -rf "$WORK"
}
trap cleanup EXIT

command -v go >/dev/null || fail "go is required (https://go.dev/dl)"
command -v curl >/dev/null || fail "curl is required"

step "Build aiplex and aiplex-api"
mkdir -p "$BIN"
(cd "$ROOT" && go build -o "$BIN/aiplex" ./cmd/aiplex-cli && go build -o "$BIN/aiplex-api" ./cmd/aiplex-api) || fail "build"

export HOME="$WORK/home" PATH="$BIN:$PATH"
mkdir -p "$HOME"
unset AIPLEX_URL AIPLEX_TOKEN

step "aiplex dev --port $PORT --no-console"
started=$(date +%s)
aiplex dev --port "$PORT" --no-console >"$WORK/dev.out" 2>&1 &
DEV_PID=$!
for _ in $(seq 1 120); do
    if aiplex dev env >/dev/null 2>&1; then break; fi
    kill -0 "$DEV_PID" 2>/dev/null || fail "aiplex dev exited"
    sleep 0.5
done
aiplex dev env >/dev/null 2>&1 || fail "aiplex dev did not become ready"
cat "$WORK/dev.out"
echo "(ready after $(( $(date +%s) - started ))s)"

step 'eval "$(aiplex dev env)"'
eval "$(aiplex dev env)"
[[ -n "${AIPLEX_TOKEN:-}" && -n "${OPENAI_BASE_URL:-}" ]] || fail "aiplex dev env did not export AIPLEX_TOKEN and OPENAI_BASE_URL"
echo "AIPLEX_URL=$AIPLEX_URL"
echo "OPENAI_BASE_URL=$OPENAI_BASE_URL"

step "Chat completion through the access point"
out=$(curl -sS --fail-with-body "$AIPLEX_URL/ap/local/v1/chat/completions" \
    -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \
    -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello"}]}') || fail "chat completion: $out"
echo "$out"
[[ "$out" == *"Hello from the AIPlex mock model"* ]] || fail "unexpected chat reply"

step "Streaming chat completion (OPENAI_BASE_URL, as an OpenAI SDK would call it)"
out=$(curl -sS -N --fail-with-body "$OPENAI_BASE_URL/chat/completions" \
    -H "Authorization: Bearer $OPENAI_API_KEY" -H "Content-Type: application/json" \
    -d '{"model":"mock-gpt","stream":true,"messages":[{"role":"user","content":"Stream please"}]}') || fail "stream: $out"
echo "$out" | tail -4
[[ "$out" == *'"usage"'* && "$out" == *"[DONE]"* ]] || fail "stream lacks usage or [DONE]"

step "MCP: initialize, tools/list, tools/call on the access point"
mcp() {
    curl -sS --fail-with-body "$AIPLEX_URL/ap/local/mcp" -H "Authorization: Bearer $AIPLEX_TOKEN" \
        -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
        -H "MCP-Protocol-Version: 2025-06-18" -d "$1"
}
out=$(mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"demo","version":"1"}}}') || fail "initialize: $out"
[[ "$out" == *'"protocolVersion"'* ]] || fail "initialize: $out"
mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null || fail "notifications/initialized"
out=$(mcp '{"jsonrpc":"2.0","id":2,"method":"tools/list"}') || fail "tools/list: $out"
[[ "$out" == *'local-tools__echo'* ]] || fail "tools/list: $out"
out=$(mcp '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"local-tools__add","arguments":{"a":2,"b":40}}}') || fail "tools/call: $out"
echo "$out"
[[ "$out" == *'"text":"42"'* ]] || fail "tools/call add did not return 42"

step "aiplex usage records"
out=$(aiplex usage records) || fail "usage records"
echo "$out"
[[ "$out" == *"mock-gpt"* && "$out" == *"est."* && "$out" == *"add"* ]] || fail "usage records lack the model, its estimated cost or the tool call"

step "aiplex usage summary --by model"
aiplex usage summary --by model || fail "usage summary"

step "aiplex connect local, then its curl snippet verbatim"
snippet=$(aiplex connect local -o json | python3 -c 'import json,sys; print(next(s["content"] for s in json.load(sys.stdin)["snippets"] if s["title"]=="curl: OpenAI chat completions"))' 2>/dev/null) \
    || snippet=$(aiplex connect local | sed -n '/^curl: OpenAI chat completions/,/^$/p' | sed '1d;s/^    //')
[[ -n "$snippet" ]] || fail "no curl snippet in aiplex connect output"
echo "$snippet"
out=$(bash -c "$snippet") || fail "connect snippet: $out"
[[ "$out" == *"Hello from the AIPlex mock model"* ]] || fail "connect snippet reply: $out"

step "Stop aiplex dev"
kill -INT "$DEV_PID"
wait "$DEV_PID" || fail "aiplex dev exited with an error on Ctrl+C"
DEV_PID=""
printf '\nDEMO PASSED in %ss\n' "$(( $(date +%s) - started ))"
