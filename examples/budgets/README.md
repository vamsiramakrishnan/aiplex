# Budgets

Team and project limits above per-caller quota. A budget caps cost, tokens or requests for a set of callers over a calendar window. Budgets form a tree: a request draws from its own budget and every parent. QuotaPlex enforces them. See [docs/budgets.md](../../docs/budgets.md).

[budgets.sh](budgets.sh) shows the whole loop against `aiplex dev --quota`, which turns QuotaPlex on for the mock surfaces and tools so budgets that count **requests** work without a policy file:

```bash
aiplex dev --quota                  # terminal 1
eval "$(aiplex dev env)"            # terminal 2
bash examples/budgets/budgets.sh
```

It creates a parent and a child budget, previews which budgets an `eng` request draws from, spends the child's allowance of two requests as a second caller, shows the refusal that names the budget, the status, and the same spend in the usage ledger (`aiplex usage summary --by budget`).

`make examples-test` runs this script against its own `aiplex dev --quota`.

## Cost and token budgets

Cost and token budgets need prices and bounds that a local default cannot honestly invent. They need an operator-written quota file: [quota-cost.json](quota-cost.json) is a minimal one with a **placeholder** per-request tariff (0.001 USD, not any provider's price) for one model surface. Use it as a starting point:

```bash
export AIPLEX_MODULES=mcplex,a2aplex,llmplex,skillsplex,clientplex,networking,quotaplex
export AIPLEX_QUOTA_FILE=$PWD/examples/budgets/quota-cost.json
```

A model that the file's price book does not cover is refused under a cost budget by default (`on_unknown_cost: deny`); `allow_and_flag` admits it and counts it as unknown cost. Real token prices come from price books keyed to your providers, with operator-attested provider limits for the token profiles. See [QuotaPlex integration](../../docs/quotaplex-integration.md).

State: under `aiplex dev` budgets and counters are in memory and are lost when the local API restarts.
