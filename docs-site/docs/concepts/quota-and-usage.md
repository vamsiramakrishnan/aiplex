---
title: Quota and usage
description: What QuotaPlex governs, what the usage ledger records, and why unknown is never shown as zero.
---

# Quota and usage

Two separate features answer cost questions.

## The usage ledger

The [usage ledger](../library/usage-ledger.md) keeps one append-only, tenant-scoped record for each managed exchange on the LLMPlex, MCPlex and A2APlex paths. A record answers which agent used which model through which client, how many tokens the provider reported, and what the exchange cost when a price or a quota settlement establishes a cost. Records hold identifiers, status, timing and counters, never prompts, completions, tool arguments, results or credentials.

Unknown is not zero: an exchange whose usage the provider did not report is recorded as unknown. Which provider usage is exact, advisory or not observed is stated in the [usage-format coverage matrix](../library/reference/usage-formats/coverage.md).

## QuotaPlex

[QuotaPlex](../guides/quotaplex.md) is an opt-in module that shares budgets and admission controls across supported execution paths: allocations in tokens, money and requests, attribution to a human and an agent independently, periods, explicit loans, atomic admission, rate limits, concurrency and weighted scheduling.

Only explicitly covered paths are protected. Existing Envoy routes and direct provider calls do not automatically pass through the quota path. Read the [execution coverage matrix](../library/quotaplex-execution-coverage.md) before relying on a limit.
