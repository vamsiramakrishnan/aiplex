#!/usr/bin/env bash
# The same request with plain HTTP. Needs only curl.
# OPENAI_BASE_URL and OPENAI_API_KEY come from: eval "$(aiplex dev env)"
set -euo pipefail
curl -sS "${OPENAI_BASE_URL:?run: eval \"\$(aiplex dev env)\"}/chat/completions" \
  -H "Authorization: Bearer ${OPENAI_API_KEY:?}" -H "Content-Type: application/json" \
  -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello"}]}'
echo
