#!/usr/bin/env bash
# Runs the examples in this directory against a fresh `aiplex dev` (make examples-test).
#
#   examples/openai-sdk   chat.sh always; chat.py and chat.mjs when the OpenAI SDK is available
#   examples/mcp-tools    mcp.sh, and the MCP URL from `aiplex connect local`
#   examples/usage-report report.sh
#   examples/hooks        hooks.sh (redaction policy: masked and refused prompts)
#
# SDK handling:
#   default                   use an SDK that is already installed; otherwise print SKIP
#   EXAMPLES_INSTALL_SDKS=1   install the OpenAI SDKs into a temporary venv / node_modules
#                             (needs network access to PyPI and npm); CI sets this
#   EXAMPLES_REQUIRE_SDKS=1   treat a skipped SDK example as a failure
#
# Other settings: EXAMPLES_BIN (default ./bin), EXAMPLES_PORT (default 18081).
# Needs Go (to build when bin/ is empty), curl and python3. Uses an isolated HOME,
# no cloud account and no provider key. Exits non-zero on the first failure.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EX="$ROOT/examples"
BIN="${EXAMPLES_BIN:-$ROOT/bin}"
PORT="${EXAMPLES_PORT:-18081}"
WORK="$(mktemp -d)"
DEV_PID=""
PASSED=0
SKIPPED=0

# Pinned to the versions the examples were run with.
OPENAI_PY="openai==3.48.0"
OPENAI_NODE="openai@7.54.0"

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ok() { printf '    ok: %s\n' "$*"; PASSED=$((PASSED + 1)); }
skip() {
    printf '    SKIP: %s\n' "$*"
    SKIPPED=$((SKIPPED + 1))
    [[ "${EXAMPLES_REQUIRE_SDKS:-0}" == "1" ]] && fail "skipped, but EXAMPLES_REQUIRE_SDKS=1: $*"
    return 0
}
fail() {
    printf '\nEXAMPLES FAILED: %s\n' "$*" >&2
    [[ -f "$WORK/dev.out" ]] && { echo "--- aiplex dev output ---" >&2; cat "$WORK/dev.out" >&2; }
    [[ -f "$WORK/home/.aiplex/dev-api.log" ]] && { echo "--- API log (tail) ---" >&2; tail -40 "$WORK/home/.aiplex/dev-api.log" >&2; }
    exit 1
}
cleanup() {
    if [[ -n "$DEV_PID" ]] && kill -0 "$DEV_PID" 2>/dev/null; then
        kill -INT "$DEV_PID" 2>/dev/null || true
        wait "$DEV_PID" 2>/dev/null || true
    fi
    rm -rf "$WORK"
}
trap cleanup EXIT

command -v curl >/dev/null || fail "curl is required"
command -v python3 >/dev/null || fail "python3 is required"

if [[ ! -x "$BIN/aiplex" || ! -x "$BIN/aiplex-api" ]]; then
    command -v go >/dev/null || fail "go is required to build the binaries (https://go.dev/dl)"
    step "Build aiplex and aiplex-api"
    mkdir -p "$BIN"
    (cd "$ROOT" && go build -o "$BIN/aiplex" ./cmd/aiplex-cli && go build -o "$BIN/aiplex-api" ./cmd/aiplex-api) || fail "build"
fi

export HOME="$WORK/home" PATH="$BIN:$PATH"
mkdir -p "$HOME"
unset AIPLEX_URL AIPLEX_TOKEN OPENAI_BASE_URL OPENAI_API_KEY ANTHROPIC_BASE_URL ANTHROPIC_API_KEY

step "aiplex dev --port $PORT --no-console"
aiplex dev --port "$PORT" --no-console >"$WORK/dev.out" 2>&1 &
DEV_PID=$!
for _ in $(seq 1 120); do
    aiplex dev env >/dev/null 2>&1 && break
    kill -0 "$DEV_PID" 2>/dev/null || fail "aiplex dev exited"
    sleep 0.5
done
aiplex dev env >/dev/null 2>&1 || fail "aiplex dev did not become ready"
eval "$(aiplex dev env)"
[[ -n "${AIPLEX_TOKEN:-}" && -n "${OPENAI_BASE_URL:-}" ]] || fail "aiplex dev env did not export AIPLEX_TOKEN and OPENAI_BASE_URL"

# ─── openai-sdk ─────────────────────────────────────────────────────────────
step "openai-sdk: chat.sh (plain HTTP)"
out=$(bash "$EX/openai-sdk/chat.sh") || fail "chat.sh: $out"
[[ "$out" == *"Hello from the AIPlex mock model. You said: Hello"* ]] || fail "chat.sh reply: $out"
ok "chat.sh"

step "openai-sdk: chat.py (OpenAI Python SDK)"
py=""
if python3 -c 'import openai' 2>/dev/null; then
    py="python3"
elif [[ "${EXAMPLES_INSTALL_SDKS:-0}" == "1" ]]; then
    python3 -m venv "$WORK/venv" && "$WORK/venv/bin/pip" install -q "$OPENAI_PY" && py="$WORK/venv/bin/python" \
        || fail "could not install $OPENAI_PY"
fi
if [[ -n "$py" ]]; then
    out=$("$py" "$EX/openai-sdk/chat.py") || fail "chat.py: $out"
    [[ "$out" == *"Hello from the AIPlex mock model. You said: Hello"* ]] || fail "chat.py reply: $out"
    ok "chat.py"
else
    skip "the openai Python package is not installed (run with EXAMPLES_INSTALL_SDKS=1 to install it temporarily)"
fi

step "openai-sdk: chat.mjs (OpenAI Node SDK)"
if command -v node >/dev/null && command -v npm >/dev/null && [[ "${EXAMPLES_INSTALL_SDKS:-0}" == "1" ]]; then
    mkdir -p "$WORK/node"
    (cd "$WORK/node" && npm init -y >/dev/null && npm install --silent "$OPENAI_NODE") || fail "could not install $OPENAI_NODE"
    cp "$EX/openai-sdk/chat.mjs" "$WORK/node/chat.mjs"
    out=$(cd "$WORK/node" && node chat.mjs) || fail "chat.mjs: $out"
    [[ "$out" == *"Hello from the AIPlex mock model. You said: Hello"* ]] || fail "chat.mjs reply: $out"
    ok "chat.mjs"
else
    skip "the openai Node package is not installed (needs node and npm, and EXAMPLES_INSTALL_SDKS=1)"
fi

# ─── mcp-tools ──────────────────────────────────────────────────────────────
step "mcp-tools: mcp.sh"
out=$(bash "$EX/mcp-tools/mcp.sh") || fail "mcp.sh: $out"
[[ "$out" == *'"protocolVersion"'* ]] || fail "mcp.sh initialize: $out"
[[ "$out" == *'local-tools__echo'* && "$out" == *'local-tools__add'* ]] || fail "mcp.sh tools/list: $out"
[[ "$out" == *'"text":"42"'* ]] || fail "mcp.sh tools/call add did not return 42: $out"
ok "mcp.sh"

step "mcp-tools: the client configuration printed by aiplex connect"
cfg=$(aiplex connect local -o json | python3 -c '
import json, sys
snips = json.load(sys.stdin)["snippets"]
cfg = next(s["content"] for s in snips if s["title"].startswith("MCP client configuration"))
print(cfg)') || fail "no MCP client configuration in aiplex connect output"
mcp_url=$(printf '%s' "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["mcpServers"]["aiplex-local"]["url"])') || fail "unexpected MCP configuration: $cfg"
[[ "$mcp_url" == "$AIPLEX_URL/ap/local/mcp" ]] || fail "MCP URL in configuration is $mcp_url, expected $AIPLEX_URL/ap/local/mcp"
# The committed copy of the snippet must still be what the command prints (token placeholder included).
diff <(printf '%s\n' "$cfg" | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin), indent=2, sort_keys=True))' | sed "s#$AIPLEX_URL#http://127.0.0.1:8080#") \
     <(python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1])), indent=2, sort_keys=True))' "$EX/mcp-tools/mcp-config.json") \
    || fail "examples/mcp-tools/mcp-config.json differs from the snippet 'aiplex connect local' prints"
out=$(curl -sS --fail-with-body "$mcp_url" -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" -H "MCP-Protocol-Version: 2025-06-18" \
    -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}') || fail "tools/list at the configured URL: $out"
[[ "$out" == *'local-tools__echo'* ]] || fail "tools/list at the configured URL: $out"
ok "connect snippet URL serves tools/list"

# ─── usage-report ───────────────────────────────────────────────────────────
step "usage-report: report.sh"
out=$(bash "$EX/usage-report/report.sh") || fail "report.sh: $out"
echo "$out"
[[ "$out" == *"mock-gpt"* ]] || fail "report.sh output lacks the model"
[[ "$out" == *"llmplex"* && "$out" == *"mcplex"* ]] || fail "report.sh output lacks both planes"
[[ "$out" == *"echo"* ]] || fail "report.sh output lacks the tool call"
[[ "$out" == *"(estimated)"* ]] || fail "report.sh output lacks the estimated cost"
ok "report.sh"

# ─── hooks ──────────────────────────────────────────────────────────────────
step "hooks: hooks.sh"
out=$(bash "$EX/hooks/hooks.sh" 2>&1) || fail "hooks.sh: $out"
echo "$out"
[[ "$out" == *"Output 1: mail [EMAIL]"* ]] || fail "hooks.sh dry run did not mask: $out"
[[ "$out" == *"You said: Write to [EMAIL]"* ]] || fail "hooks.sh: the model did not receive the masked prompt: $out"
[[ "$out" == *'"code":"hook_denied"'* ]] || fail "hooks.sh: the private key was not refused: $out"
[[ "$out" == *"Deleted hook policy pii-guard"* ]] || fail "hooks.sh did not clean up: $out"
ok "hooks.sh"

# ─── declarative manifests ──────────────────────────────────────────────────
# Every manifest in examples/ passes `aiplex validate` (the checks `aiplex apply`
# makes before it writes). The JSON files that are not manifests are the MCP
# client configuration (checked above against `aiplex connect`) and the
# QuotaPlex policy (loaded by a Go test).
step "manifests: aiplex validate on every manifest file"
for f in quickstart.yaml quickstart.json mcplex-only.yaml durable-tape-runtime.yaml aiplex-tape-treasury/treasury.yaml; do
    out=$(aiplex validate "$EX/$f" 2>&1) || fail "aiplex validate $f: $out"
    [[ "$out" == *"is valid"* ]] || fail "aiplex validate $f: $out"
done
# Every manifest-shaped file is covered by the list above.
for f in "$EX"/*.yaml "$EX"/*/*.yaml; do
    # Hook policies are not manifests; hooks.sh applies policy.yaml above.
    case "$f" in */modules/*|*/hooks/*) continue ;; esac
    rel="${f#"$EX"/}"
    case " quickstart.yaml mcplex-only.yaml durable-tape-runtime.yaml aiplex-tape-treasury/treasury.yaml " in
        *" $rel "*) ;;
        *) fail "$rel is a YAML file in examples/ that this script does not validate" ;;
    esac
done
ok "validate"

step "manifests: apply quickstart.yaml under aiplex dev (agents and routes apply, instances are skipped with a reason)"
out=$(aiplex apply -f "$EX/quickstart.yaml" 2>&1) || fail "apply quickstart.yaml: $out"
[[ "$out" == *"skipped"*"instance Knowledge Base"* && "$out" == *"aiplex dev has no Kubernetes"* ]] || fail "apply did not explain skipping instances: $out"
[[ "$out" == *"created"*"agent tutor-agent"* && "$out" == *"created"*"route gemini-2.5-flash"* ]] || fail "apply quickstart.yaml: $out"
ok "apply quickstart.yaml"

step "manifests: applying quickstart.yaml again converges (nothing created, everything unchanged)"
out=$(aiplex apply -f "$EX/quickstart.yaml" 2>&1) || fail "second apply quickstart.yaml: $out"
[[ "$out" == *"Summary: 0 created, 0 updated, 3 unchanged, 2 skipped, 0 failed"* ]] || fail "second apply was not idempotent: $out"
out=$(aiplex apply -f "$EX/quickstart.yaml" --dry-run 2>&1) || fail "apply --dry-run: $out"
[[ "$out" == *"0 created, 0 updated, 3 unchanged"* ]] || fail "dry run disagrees with the second apply: $out"
ok "apply is idempotent"

step "Stop aiplex dev"
kill -INT "$DEV_PID"
wait "$DEV_PID" || fail "aiplex dev exited with an error on Ctrl+C"
DEV_PID=""

# ─── budgets (needs QuotaPlex on: aiplex dev --quota) ───────────────────────
step "budgets: aiplex dev --quota"
aiplex dev --port "$PORT" --no-console --quota >"$WORK/dev.out" 2>&1 &
DEV_PID=$!
for _ in $(seq 1 120); do
    aiplex dev env >/dev/null 2>&1 && break
    kill -0 "$DEV_PID" 2>/dev/null || fail "aiplex dev --quota exited"
    sleep 0.5
done
aiplex dev env >/dev/null 2>&1 || fail "aiplex dev --quota did not become ready"
eval "$(aiplex dev env)"
step "budgets: budgets.sh"
out=$(bash "$EX/budgets/budgets.sh" 2>&1) || fail "budgets.sh: $out"
echo "$out"
[[ "$out" == *"Created budget demo-team"* ]] || fail "budgets.sh did not create the team budget: $out"
[[ "$out" == *"group:eng"* && "$out" == *"via demo-team"* ]] || fail "budgets.sh preview did not explain the draws: $out"
[[ "$out" == *'budget \"Demo team\" (demo-team) is exhausted'* ]] || fail "budgets.sh: the third request was not refused naming the budget: $out"
[[ "$out" == *"Spent:     2"* && "$out" == *"Remaining: 0"* ]] || fail "budgets.sh status: $out"
[[ "$out" == *"demo-org"* && "$out" == *"demo-team"* ]] || fail "budgets.sh: the ledger summary lacks the budgets: $out"
[[ "$out" == *"Deleted budget demo-org"* ]] || fail "budgets.sh did not clean up: $out"
ok "budgets.sh"
step "Stop aiplex dev --quota"
kill -INT "$DEV_PID"
wait "$DEV_PID" || fail "aiplex dev --quota exited with an error on Ctrl+C"
DEV_PID=""
printf '\nEXAMPLES PASSED (%s checks passed, %s skipped)\n' "$PASSED" "$SKIPPED"
