---
title: QuotaPlex
description: Share budgets and admission controls across supported execution paths, and read the usage ledger.
---

# QuotaPlex

QuotaPlex is an opt-in control module shared by the existing planes. It supports unequal token, money and request allocations, independent human and agent attribution, fixed, anchored and calendar periods, explicit budget loans, atomic admission, rate limits, concurrency and weighted scheduling.

For configured calls through the Go API's managed endpoints it takes a budget hold and an admitted-rate debit, issues a permit, lets the call claim once and settle once from trusted terminal evidence, and retains holds when the outcome is unknown.

Existing Envoy routes and direct provider calls do not automatically pass through this path. Hard limits require verified provider bounds, prices, identity sources and routing for each enabled profile.

## Enable it

QuotaPlex is on only when the API's `AIPLEX_MODULES` includes `quotaplex` and `AIPLEX_QUOTA_FILE` points at an operator quota file. See [Managed QuotaPlex execution](../library/quotaplex-integration.md) and [Configuration](../operations/configuration.md).

## Commands

```bash
aiplex quota policy                 # active and configured policy
aiplex quota policy-validate --help # validate an export without activating it
aiplex quota preview --help         # simulate admission without reserving
aiplex quota balance --help
aiplex quota attempt --help
aiplex quota loan-grant --help
aiplex quota reconcile-plan --help
```

## Pages

| Topic | Page |
| --- | --- |
| Library boundary, policy compilation and activation | [QuotaPlex core](../library/quotaplex-core.md) |
| Managed model and tool execution, acknowledgement and recovery | [Managed QuotaPlex execution](../library/quotaplex-integration.md) |
| Inspection, loans, reviewed reconciliation | [Quota administration](../library/quotaplex-administration.md) |
| What is and is not covered | [Execution coverage](../library/quotaplex-execution-coverage.md) |
| Everything still required before hard limits | [Prerequisites and readiness](../library/quotaplex-prerequisites.md) |
| Allocation, metering and scheduling contracts | [Foundation design](../library/quotaplex-foundation-design.md) |
| Group membership for eligibility | [Authoritative membership](../library/membership-authority.md) |

## Usage ledger

The usage ledger records every managed exchange whether or not QuotaPlex is enabled: [Usage ledger](../library/usage-ledger.md), with the CLI at `aiplex usage records`, `aiplex usage summary` and `aiplex usage prices`.

API: [Quota](../api/quota.md) and [Usage](../api/usage.md).
