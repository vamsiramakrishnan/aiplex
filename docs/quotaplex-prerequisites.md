# QuotaPlex: requirements and limits

For each area this page states what QuotaPlex enforces, what an operator must
configure, and what is not supported. The [core guide](quotaplex-core.md) describes
the library, [managed execution](quotaplex-integration.md) the host integration and
[execution coverage](quotaplex-execution-coverage.md) the exact operations covered.
The [allocation and metering](quotaplex-allocation-and-metering.md) page defines the allocation,
metering and scheduling semantics.

QuotaPlex is opt-in. Default traffic, direct-provider routes and deployed gateway
paths are not metered unless they pass through a supported managed dispatch path.
Hard token or monetary limits depend on verified provider bounds, commercial prices,
the deployed routing and per-operation qualification
(see [production qualification](production-qualification.md)).

## Ownership

QuotaPlex owns its policy schema, allocation rules, funding selection, periods,
reservations, pricing, lending, ledger, balances and reconciliation. Host adapters
bind those contracts to the existing execution path and attach through the
`enforcement.Provider` / `Receipt` boundary. LLMPlex owns native model operations and
usage extraction, A2APlex owns agent invocation, ClientPlex owns client configuration,
identity owns authenticated principals, actors and membership, and networking and
Anvil own provisioning intent. Native payloads, SSE frames, Live messages and client
protocols keep their provider semantics.

## Identity and membership

- **Enforced.** The tenant, principal, agent actor and application come from the
  verified request. Funding eligibility is checked against a trusted membership
  snapshot; client-supplied groups, email addresses, billing-project headers and
  cost-center labels never grant eligibility or select the payer. A human's groups do
  not authorize an agent's funding account, or the reverse.
- **Operator configures.** A membership source: signed claims, or a configured signed
  HTTP authority with complete, source-qualified principal and actor snapshots, plus
  signing keys and a bounded freshness and revocation policy
  (see [membership authority](membership-authority.md)).
- **Not supported.** Native directory synchronisation is not provided; a real
  directory authority must supply the snapshots.

## Funding and constraints

- **Enforced.** One eligible payer is selected per billable attempt (by priority when
  the caller selects none; an ambiguous priority is an error). Every matched
  mandatory user, agent, account, group, tenant and resource constraint must pass
  together. Group membership establishes eligibility and never adds the balances of
  several groups. Allocations are independent of headcount unless a frozen
  per-member strategy bound to a group snapshot is configured.
- **Operator configures.** Tenant funding rules and allocations.
- **Not supported.** Split charging, an implicit union of group balances, automatic
  live headcount rebalancing, and a caller choosing another tenant's budget.

## Periods and exact quantities

- **Enforced.** Fixed (half-open `[start, end)`), anchored recurring and calendar
  windows with an IANA timezone and explicit DST handling. Quantities and money are
  integers with explicit currency and scale; prices are rational and rounded once at
  a declared boundary. Policy and price revisions are pinned by each decision, and a
  revision cannot reset usage or mint another balance for the same interval. A
  reduction below committed spend blocks new work.
- **Not supported.** Rolling budgets, structural window migration, implicit currency
  conversion and floating-point amounts as an authoritative ledger value. Legacy
  trailing-interval usage summaries are reporting only and do not schedule quota
  periods.

## Metering and pricing

- **Enforced.** Raw provider counters are kept apart from billable meters; totals and
  their subsets are not summed. An unknown price or unavailable metering yields a
  declared unsupported, advisory or denied outcome, never a zero cost. Prices pin
  provider, resolved model version, region, operation and commercial mode.
- **Operator configures.** Real commercial prices and meters for each enabled
  profile. Discounts, provisioned capacity and subscription entitlements are not
  modelled.
- **Not supported.** Billing for operations without a declared metering profile:
  Live and multimodal token accounting, server tools, cache writes, background jobs,
  and opaque agent-internal model or tool work are covered only where a profile says
  so (see [model metering profiles](model-metering-profiles.md)).

## Exposure bounds

- **Enforced.** Admission reserves a bounded maximum exposure. Strict policies deny
  an operation whose exposure cannot be bounded; advisory policies label the unknown
  value. Any billable estimation call needs its own admission. An explicit overrun
  policy can describe bounded overshoot but never establishes a strict ceiling.
- **Operator configures.** Provider-enforced output ceilings per model version,
  account and region.
- **Not supported.** A strict monetary ceiling for operations without an enforceable
  upper bound, renewal or checkpoint contract.

## Admission, dispatch and settlement

- **Enforced.** All applicable budget, rate and concurrency checks commit together or
  not at all. Attempts have separate logical-operation, billable-attempt, reservation,
  dispatch and usage-event identities, scoped by tenant and immutable input; an
  identical replay returns the pinned result and changed input conflicts. Dispatch
  needs its own durable claim and a fresh authorization check. Network ambiguity
  becomes durable unknown work that retains its exposure. Terminal usage is
  acknowledged, deduplicated and settled once; a failed request can still carry
  billable usage. Corrections are reviewed, linked ledger entries that keep the
  original facts and prices.
- **Operator configures.** The quota module, a shared durable backend and the host
  manifest; a protected path whose required provider is missing fails readiness
  instead of skipping QuotaPlex.
- **Not supported.** A remote, independently deployed admission service (the module
  adapter verifies descriptors and readiness but does not invoke remote
  enforcement), arbitrary repricing and negative usage counters.

## Sharing unused allocation

- **Enforced.** Budget loans debit lendable capacity and credit a named borrower
  window in one transaction, exclude settled usage, active holds and protected
  floors, respect parent caps, and expire within the source and destination windows.
  Spent borrowed capacity cannot be recalled. Within a supported account and window,
  `available = granted + authorized_net_transfers - settled_usage - active_holds`.
- **Not supported.** Automatic lending, future-window allowances, and sharing of
  native throughput between accounts.

## Rates, concurrency and scheduling

- **Enforced.** Atomic admitted-demand rate buckets, fenced concurrency permits that
  release only known-terminal or undispatched exposure, and bounded queues with
  deadlines, protected admission partitions and weighted residual service. Queued
  work holds no budget or permit until admission.
- **Operator configures.** Capacity evidence per provider, pool and target, with an
  expiry.
- **Not supported.** Provider capacity creation, minimum-throughput or token-pacing
  guarantees, preemption and structural queue migration. Observed-only rates cannot
  enforce an admission ceiling. Client stream pacing changes delivery only.

## Coverage

| Path | Covered when |
| --- | --- |
| Native model HTTP and client facades | Shared admission, bounded exposure, provider-attributed usage and one settlement per billable attempt, for the supported profiles |
| Streaming (SSE) | Cumulative and final counters are deduplicated; a contradictory final tail leaves the attempt unknown |
| Live and WebSocket sessions | Only where a profile defines session ownership and renewable allowances |
| Batch, background and media jobs | Only with durable submit-to-job correlation and reconciliation |
| Tools | Stateless JSON and finite-SSE `tools/call` with request or call counts; legacy sessions and bidirectional calls need explicit adapters |
| A2APlex and managed agents | The native request boundary (request counts); internal work is unknown |
| Envoy and other deployed routes, direct ClientPlex modes | Not covered automatically; hard limits need a supported hook or a verified routing boundary |

Registered clients, network profiles, registry entries and IAM bindings do not
prove that traffic crosses a quota enforcement point. A provider charge is recorded
once; when it is also visible at the agent and the model level, a causal link joins
the entries rather than charging twice. Upstream quotas, RPM and TPM limits,
provisioned throughput and client licensing remain separate constraints, and a
QuotaPlex allowance neither creates provider capacity nor prevents upstream
throttling. IAM and token federation establish access, not allocation. Server-owned
billing-project configuration sets upstream attribution.

## Storage, recovery and operation

- **Enforced.** The transaction contract has a memory reference backend and a
  Firestore backend covering caps, transfers, immutable policy and price history and
  replay records. Store outages fail closed or defer protected admission; cached
  permission or balance views cannot authorize spending. Lease expiry alone never
  releases unknown exposure, and policy, price and membership changes do not rewrite
  earlier decisions.
- **Operator configures.** A durable shared backend for multi-replica deployments;
  alerts for reservation age, pending and unknown settlement, denial reasons,
  ingestion lag and reconciliation drift; backup and restore of every quota kind
  together (policies, prices, attempts, holds, evidence, loans, scheduler state and
  receipts).
- **Not validated.** Live multi-replica contention, restore, IAM, admission latency
  and billing equivalence against provider invoices. The reference benchmarks are not
  an admission SLA. See [production qualification](production-qualification.md) for
  the runbook.
- **Not supported.** Hot publication of a policy: validation produces a reviewed
  artifact for deployment. Application rollback must keep the durable accounting
  generation, and a binary that cannot read the stored schema should fail readiness
  rather than restart with empty balances.

The legacy LLMPlex usage endpoint stores valid reports and emits advisory warning
headers when reporting budgets or rates are exceeded. It is neither admission
enforcement nor an immutable ledger, and its reported costs are not a second charging
authority.

See [connected mechanisms](quotaplex-connected-mechanisms.md) for how the configuration,
membership, administration, scheduling, MCP, model and infrastructure mechanisms
connect.
