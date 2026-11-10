#!/usr/bin/env bash
# Apply the redaction policy, then show a masked prompt and a refused one.
# Needs curl, the aiplex CLI, and AIPLEX_URL and AIPLEX_TOKEN from: eval "$(aiplex dev env)"
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AP="${AIPLEX_URL:?run: eval \"\$(aiplex dev env)\"}/ap/local"
auth=(-H "Authorization: Bearer ${AIPLEX_TOKEN:?}")

aiplex hooks policies set -f "$HERE/policy.yaml"

echo "# dry run (nothing is sent to a model or stored)"
aiplex hooks policies test pii-guard --text "mail jane@example.com"

echo "# masked: the mock model echoes what it received"
curl -sS "$AP/v1/chat/completions" "${auth[@]}" -H "Content-Type: application/json" \
  -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Write to jane@example.com"}]}'
echo

echo "# refused: a private key never reaches the model"
curl -sS "$AP/v1/chat/completions" "${auth[@]}" -H "Content-Type: application/json" \
  -d '{"model":"mock-gpt","messages":[{"role":"user","content":"-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----"}]}'
echo

echo "# the ledger records decisions, never content"
aiplex usage records --hook-decision deny -o wide

aiplex hooks policies delete pii-guard --yes
