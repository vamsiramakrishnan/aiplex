#!/usr/bin/env bash
# Make a few requests through the local access point, then read the usage ledger.
# Needs curl, the aiplex CLI, and AIPLEX_URL and AIPLEX_TOKEN from: eval "$(aiplex dev env)"
set -euo pipefail
AP="${AIPLEX_URL:?run: eval \"\$(aiplex dev env)\"}/ap/local"
auth=(-H "Authorization: Bearer ${AIPLEX_TOKEN:?}")

for prompt in "Explain a parabola." "Name three primes." "Hello again."; do
  curl -sS -o /dev/null "$AP/v1/chat/completions" "${auth[@]}" -H "Content-Type: application/json" \
    -d "{\"model\":\"mock-gpt\",\"messages\":[{\"role\":\"user\",\"content\":\"$prompt\"}]}"
done
curl -sS -o /dev/null "$AP/mcp" "${auth[@]}" -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" -H "MCP-Protocol-Version: 2025-06-18" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"local-tools__echo","arguments":{"text":"hi"}}}'

echo "# model calls by user and model"
aiplex usage summary --plane llmplex --by user,model
echo
echo "# everything by plane and tool"
aiplex usage summary --by plane,tool
echo
echo "# the individual records"
aiplex usage records
