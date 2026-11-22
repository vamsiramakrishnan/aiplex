---
title: QuotaPlex
description: Try one spending limit across a model call and an MCP tool, then trace how admission, settlement and recovery work.
---

# QuotaPlex

QuotaPlex lets an operator give a team a spending limit across supported managed operations. Before dispatch, it checks the caller's funding, reserves exposure against matching budgets and acquires any required concurrency permit. Trusted usage settles that attempt. If the upstream outcome is uncertain, the hold stays open.

## Spend one budget across two gateways

From a source checkout with the local stack stopped, run the example with Python 3 and curl:

```bash
make build
export PATH="$PWD/bin:$PATH"
aiplex dev --quota-tariff --detach
bash examples/budgets/shared-spend.sh
aiplex dev stop
```

The example gives one caller a USD 0.003 budget. The local price book charges per managed exchange.

| Call | Cost | Remaining |
| --- | ---: | ---: |
| LLMPlex calls the mock model | USD 0.001 | USD 0.002 |
| MCPlex calls the mock tool | USD 0.002 | USD 0.000 |
| MCPlex tries the tool again | Refused before dispatch | USD 0.000 |

`--quota-tariff` selects fixture prices and a mock provider. A request-count budget already counts covered tool calls. A cost budget includes priced tool calls when created with `--include-tool-calls`. The operator's **quota price book** supplies the enforceable prices. Separately, ledger catalogue prices can estimate cost for reporting. [Budget units and prices](../library/budgets.md)

## Enable it

Use `aiplex dev --quota` to exercise request limits locally. On a deployment, enable `quotaplex` in `AIPLEX_MODULES` and point `AIPLEX_QUOTA_FILE` at the operator configuration. Each binding names the operation, resource, funding account and metering profile. [Managed execution](../library/quotaplex-integration.md) · [Configuration](../operations/configuration.md)

| Path | What a binding can cover |
| --- | --- |
| Managed model operation | Requests, supported native usage meters and their configured prices. Profile and provider evidence determine which dimensions are enforceable. |
| Managed MCP call | Calls and optional tool tariffs. The tool-usage profile also meters response bytes and duration. |
| Managed native agent invocation | A request-count binding covers the native HTTP exchange. The agent's internal model and tool work is opaque. |
| Envoy routes, direct providers, skill manifests, reported delegation history | Outside managed invocation accounting. |

## Commands

| Operator question | Command |
| --- | --- |
| Which budgets apply to this group and access point? | `aiplex budgets preview --group eng --access-point local` |
| What is spent, reserved and still available? | `aiplex budgets status <budget-id>` |
| Which policy is active? | `aiplex quota policy` |
| What happened to an admission? | `aiplex quota attempt <id>` |
| Which dispatched attempts still hold exposure? | `aiplex quota unknown --older-than 1h` |

## Pages

[Policy and activation](../library/quotaplex-core.md) · [Allocation and metering](../library/quotaplex-allocation-and-metering.md) · [Membership](../library/membership-authority.md) · [Connected mechanisms](../library/quotaplex-connected-mechanisms.md) · [Loans and reconciliation](../library/quotaplex-administration.md) · [Execution coverage](../library/quotaplex-execution-coverage.md) · [Production prerequisites](../library/quotaplex-prerequisites.md)

## Usage ledger

`aiplex usage records` shows managed activity even when QuotaPlex is disabled. Missing usage or price stays unknown. [Usage ledger](../library/usage-ledger.md) · [Quota API](../api/quota.md) · [Usage API](../api/usage.md)
