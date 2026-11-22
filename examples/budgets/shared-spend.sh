#!/usr/bin/env bash
# Run against a local stack started with: aiplex dev --quota-tariff
set -euo pipefail
dev_env=$(aiplex dev env)
eval "$dev_env"
budget="demo-shared-spend-$$"
ap="$AIPLEX_URL/ap/local"

aiplex budgets create "$budget" --name "Shared mock spend" --unit cost --currency USD \
  --amount 0.003 --window day --user developer@localhost --include-tool-calls
trap 'aiplex budgets delete "$budget" --yes >/dev/null' EXIT

post() {
  curl -fsS "$ap/$1" -H "Authorization: Bearer $AIPLEX_TOKEN" \
    -H 'Content-Type: application/json' -d "$2"
}

echo '1. Spend USD 0.001 on mock-gpt'
post v1/chat/completions '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello"}]}' |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["choices"][0]["message"]["content"])'

echo '2. Spend USD 0.002 on local-tools echo'
echo_call='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"local-tools__echo","arguments":{"text":"shared budget"}}}'
post mcp "$echo_call" |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["content"][0]["text"])'

echo '3. Try echo again against the exhausted budget'
post mcp "$echo_call" | python3 -c '
import json, sys
error = json.load(sys.stdin)["error"]
print(error["data"]["reason"], error["message"])
if error["data"]["reason"] != "quota_budget_exhausted":
    sys.exit("expected the shared budget to refuse the call")
'
aiplex budgets status "$budget"
aiplex usage summary --by budget --budget "$budget"
