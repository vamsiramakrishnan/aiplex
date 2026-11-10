#!/usr/bin/env bash
# Talk to the access point's MCP endpoint with plain HTTP (JSON-RPC), the way an
# MCP client does: initialize, list tools, call one. Needs curl and AIPLEX_URL and
# AIPLEX_TOKEN from: eval "$(aiplex dev env)"
set -euo pipefail
URL="${AIPLEX_URL:?run: eval \"\$(aiplex dev env)\"}/ap/local/mcp"

rpc() {
  curl -sS "$URL" -H "Authorization: Bearer ${AIPLEX_TOKEN:?}" \
    -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
    -H "MCP-Protocol-Version: 2025-06-18" -d "$1"
  echo
}

echo "# initialize"
rpc '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"example","version":"1"}}}'
rpc '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null

echo "# tools/list"
rpc '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'

echo "# tools/call local-tools__add {a:2, b:40}"
rpc '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"local-tools__add","arguments":{"a":2,"b":40}}}'
