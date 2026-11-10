#!/usr/bin/env bash
# Create a budget tree, spend a team's allowance through the local access point,
# and read the refusal, the status and the ledger. Needs curl, the aiplex CLI
# and a stack started with QuotaPlex on:
#
#   aiplex dev --quota                  # terminal 1
#   eval "$(aiplex dev env)"            # terminal 2
set -euo pipefail
AP="${AIPLEX_URL:?run: eval \"\$(aiplex dev env)\"}/ap/local"

# A parent budget for everything, and a child for the "eng" group.
aiplex budgets create demo-org  --name "Demo organisation" --unit requests --amount 10 --window day
aiplex budgets create demo-team --name "Demo team" --parent demo-org --unit requests --amount 2 --window day --group eng

# Which budgets would an "eng" request draw from, and why?
aiplex budgets preview --group eng --access-point local

# A second caller in the "eng" group, allowed to use the mock model.
token=$(aiplex dev token --user alice --groups eng 2>/dev/null)
principal=$(aiplex dev token --user alice --groups eng -o json 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin)["principal_id"])')
aiplex agents user-scopes set "$principal" --scopes llm:model:mock-gpt,llm:surface:local-models:* >/dev/null

chat() {
  curl -sS "$AP/v1/chat/completions" -H "Authorization: Bearer $token" -H "Content-Type: application/json" \
    -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello budgets"}]}'
  echo
}

echo "# two requests fit the team budget, the third is refused with the budget's name"
chat; chat; chat

echo "# status for operators (members see spent and remaining only)"
aiplex budgets status demo-team
echo "# the same spend in the ledger, by budget"
aiplex usage summary --by budget

aiplex budgets delete demo-team --yes
aiplex budgets delete demo-org --yes
