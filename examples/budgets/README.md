# Budgets

Team and project limits above per-caller quota. A budget caps cost, tokens or requests for a set of callers over a calendar window. Budgets form a tree: a request draws from its own budget and every parent. QuotaPlex enforces them. See [docs/budgets.md](../../docs/budgets.md).

[budgets.sh](budgets.sh) shows the whole loop against `aiplex dev --quota`, which turns QuotaPlex on for the mock surfaces and tools so budgets that count **requests** work without a policy file:

```bash
aiplex dev --quota                  # terminal 1
eval "$(aiplex dev env)"            # terminal 2
bash examples/budgets/budgets.sh
```

It creates a parent and a child budget, previews which budgets an `eng` request draws from, spends the child's allowance of two requests as a second caller, shows the refusal that names the budget, the status, and the same spend in the usage ledger (`aiplex usage summary --by budget`).

## Share one USD budget across a model and a tool

Restart the local stack with its mock tariff enabled. From another terminal in the repository, run the example.

```bash
aiplex dev --quota-tariff
# In another terminal
bash examples/budgets/shared-spend.sh
```

The script creates a USD 0.003 daily budget for the local developer with `--include-tool-calls`. It removes the budget when it exits.

| Call through `/ap/local` | Charge | Remaining |
| --- | ---: | ---: |
| Ask `mock-gpt` on `local-models` | USD 0.001 | USD 0.002 |
| Call `local-tools__echo` | USD 0.002 | USD 0.000 |
| Call echo again | Refused before dispatch | USD 0.000 |

The final commands show the settled budget status and its usage summary. These local prices are per request and per call. Token-based budgets require operator-attested bounds. `make examples-test` runs both examples against the mock tariff.

## Configure a real price policy

[quota-cost.json](quota-cost.json) shows a minimal operator file for one surface. Replace its placeholder tariff and bindings for your deployment, then set these variables on the `aiplex-api` process.

```bash
export AIPLEX_MODULES=mcplex,a2aplex,llmplex,skillsplex,clientplex,networking,quotaplex
export AIPLEX_QUOTA_FILE=$PWD/examples/budgets/quota-cost.json
```

A model that the file's price book does not cover is refused under a cost budget by default (`on_unknown_cost: deny`); `allow_and_flag` admits it and counts it as unknown cost. Real token prices come from price books keyed to your providers, with operator-attested provider limits for the token profiles. See [QuotaPlex integration](../../docs/quotaplex-integration.md).

State: under `aiplex dev` budgets and counters are in memory and are lost when the local API restarts.
