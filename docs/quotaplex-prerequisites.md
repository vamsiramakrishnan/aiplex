# QuotaPlex prerequisites and readiness

Status: implementation checklist, audited against the native-governance
foundation at `d3ed9d8`, updated for the [feature cohesion repairs](feature-cohesion-bugbash.md).
This checklist preserves the existing architecture and distinguishes pending
quota work from implemented reporting behavior.

Aiplex has the module, identity, policy and provider seams needed to start
QuotaPlex. Hard token or monetary enforcement additionally needs accounting
contracts, durable state, trusted usage and host integration. The existing
foundation tests establish those seams; they do not prove a quota allocator.

## Preserve the current architecture

Keep the current gateway, authentication, permission checks, Plex ownership and
native protocol forwarding. QuotaPlex participates through the existing
`enforcement.Provider` / `Receipt` boundary. LLMPlex continues to own native model
operations and usage extraction; A2APlex owns agent invocation; ClientPlex owns
client configuration. Identity owns authenticated principals, actors and trusted
membership. Networking and Anvil continue to own provisioning intent.

QuotaPlex owns its policy schema, allocation rules, funding selection, periods,
reservations, pricing, lending, ledger, balances and reconciliation. Host adapters
bind those contracts to the existing execution path. Native payloads, SSE frames,
Live messages and client protocols retain their provider semantics.

There are three different readiness questions:

1. **Before implementation:** agree the contracts and invariants below. These are
   design inputs, not a requirement to build the quota engine somewhere else first.
2. **During implementation:** build the quota-owned mechanisms and narrow host
   integrations against those contracts.
3. **Before enforcement:** demonstrate the production gates for every enabled
   operation and deployment. Untested paths cannot inherit a hard-limit claim.

## Prerequisite register

`Present` means a reusable foundation exists. `Contract` means a decision must be
made before coding the corresponding mechanism. `Integration` means the host
still needs to bind an existing seam. `Production` means evidence is required
before enabling enforcement. None of the pending rows changes module ownership.

| ID | Prerequisite | Current state and evidence | Required completion |
| --- | --- | --- | --- |
| Q01 | Independent module boundary | Present: [module contracts](modules.md), [registry](../pkg/modules/registry.go) | QuotaPlex descriptor/configuration and required-provider registration; pin compatible contracts |
| Q02 | Trusted tenant, principal and agent actor | Present: [request context](../pkg/contracts/context.go), [WIF resolution](../internal/auth/wif.go), [native identity](native-agent-governance.md) | Contract: distinguish caller, delegated user, agent, application and payer; never infer payer from email or sponsor |
| Q03 | Multiple-group membership at admission | Present: snapshot type and `ValidateFor`; current [host context](../internal/api/connected.go) omits its reference | Integration: authoritative resolver, source-qualified group IDs, freshness/revocation rules, durable decision snapshot and attached reference |
| Q04 | Funding and overlapping limits | Contract; authentication merges permissions, not money | Define eligible accounts, selected payer, account priority and all simultaneous user/group/tenant/model limits |
| Q05 | Nonuniform allocations and periods | Contract; legacy daily/monthly reports are insufficient | Explicit quantities, units, interval type, start/anchor, timezone, effective revisions and overlap rules |
| Q06 | Admission and receipt lifecycle | Present: [enforcement interfaces](../pkg/enforcement/pipeline.go) | Contract: typed quota admission and settlement envelopes; integration before billable dispatch and after actual outcomes |
| Q07 | Stable accounting identities | Present: request correlation; no durable quota identity | Contract: logical operation, attempt, reservation, provider resource and usage-event IDs; bind idempotency keys to immutable request fingerprints |
| Q08 | Immutable decision provenance | Present: [policy snapshots](../pkg/policy/snapshots.go); memory backend only | Quota policy validation/evaluation, durable policy history, pinned membership/price revisions and explainable decisions |
| Q09 | Atomic ledger and balance storage | Present: [resource CAS](../pkg/resources/store.go) and [Firestore adapter](../internal/registry/module_resources.go) | Quota-owned transaction contract enforcing all charged accounts, holds and transfers atomically; restart/recovery proof |
| Q10 | Reservation exposure bounds | Contract | Estimated versus maximum committed usage, output/session bounds, lease/renewal rules and treatment of uncertain provider work |
| Q11 | Trusted metering | Present: [observation type](../pkg/modelapi/gateway.go) and [limited extraction](../pkg/modelapi/observations.go); host observer is unset | Operation-specific meters, durable acknowledged handoff, attempt/receipt correlation, cumulative/delta semantics and deduplication |
| Q12 | Exact monetary accounting | Present: legacy floating-point input/output pricing and usage reports | Fixed-point money, integer meters, currency/scale/rounding/overflow rules, versioned prices and unknown-price policy |
| Q13 | Sharing unused allocation | Contract | Eligible lending pools, protected floors, active-hold exclusion, limits, lease expiry, recall and conservation across different periods |
| Q14 | Failure and recovery behavior | Present: in-process receipt retry/cleanup; native governance has separate write receipts | Quota-specific durable recovery for dispatch ambiguity, partial usage, late reports, retries and adjustments |
| Q15 | Enforcement coverage | Integration: native facades share `NativeModel`; raw agent/direct-provider paths differ | Explicit operation/deployment coverage matrix, no-bypass proof for hard limits, and declared treatment of uncovered paths |
| Q16 | Balance and decision APIs | Contract | Granted/held/consumed/available/borrowed/lent with period, unit, revision and as-of semantics; tenant-scoped administration and explanations |
| Q17 | Reconciliation and operations | Production | Recover outstanding holds; reconcile trusted usage; audit adjustments; alerts, backup/restore and bounded outage behavior |
| Q18 | Performance and rollout | Production | Measured latency/QPS/contention bounds, multi-replica tests, shadow comparisons and rollback preserving in-flight accounting |

The evidence links describe current code. A versioned schema, registry entry or
native IAM integration alone does not satisfy a pending accounting gate.

## Decisions to settle before implementing QuotaPlex

### Funding, groups and authority

Record these as explicit policy rules:

- Which identity consumes quota: autonomous agent, delegated human, application,
  or an explicitly mapped account? Preserve the other identities for constraints
  and audit. Sponsor accountability grants no funding authority.
- Which verified groups make an account eligible, including nested-group
  expansion and membership sources? Missing or stale group information needs an
  explicit outcome rather than an assumed grant.
- Does a request select one funding account, use ordered accounts, or split a
  charge? A caller's selection is a request validated against trusted membership
  and policy, not permission to choose another tenant's budget.
- Which caps apply simultaneously? Selecting a project account may still require
  user, agent, group, organization and model caps. Evaluate each applicable cap
  atomically without treating every group membership as additional funding.
- How do priority, deny rules, exceptions and policy revision conflicts resolve?
  Produce one deterministic decision and retain the matched rules.

For example, a user belongs to Engineering and Research. Engineering has
USD 100 for a seven-day interval; Research has USD 400 for a calendar month; the
organization has a USD 1,000 monthly cap. These illustrative allocations do not
imply USD 500 of interchangeable credit. The policy must select or split funding,
apply the organization's cap, and preserve each account's own window. Authorization
scope union does not determine that choice.

Admission pins the membership and policy used for its decision. Define what a
subsequent revocation does to new requests, active sessions and already committed
provider work. It cannot erase historical consumption or change the payer of an
already settled attempt.

### Unequal periods and overlapping policies

Every allocation needs an explicit period definition:

| Concern | Required rule |
| --- | --- |
| Fixed interval | Exact start/end or anchor/duration; use half-open intervals `[start, end)` |
| Calendar interval | Calendar unit, IANA timezone and boundary behavior, including daylight-saving changes |
| Rolling interval | Duration and exact event/expiry semantics; do not label it a calendar period |
| Unequal quantities | Per-account limits independent of the period length; no hidden equal-share assumption |
| Overlap | Decide independent simultaneous caps versus replacement/priority; never accidentally sum overlapping limits |
| Mid-period changes | Effective revision/time, active holds, settled usage and reductions below committed spend |
| Boundary crossing | Choose admission-window or explicit usage-time allocation; preserve the rule for late settlement |
| Carryover and borrowing | Explicit opt-in, expiry and source-window identity; absent policy must not create rollover |
| Clock authority | Trusted timestamps and skew bounds; storage TTL is cleanup, not proof that a hold can be released |

The existing Firestore reporting implementation uses trailing intervals for
day/month summaries. Those summaries are not the scheduler for quota periods.

### Token units, pricing and uncertainty

Define a meter catalog before interpreting provider counters. Keep raw usage and
billable units separate. Input, output, cached, reasoning/thought and total-token
counters can overlap; adding totals and their subsets can charge twice. Audio,
image, video, cache storage, tools, search, batch and other specialized operations
need their own verified billing treatment when included in the enforced scope.

Use integer quantities and fixed-point monetary values with explicit currency,
scale and rounding. Preserve fractional price calculations until the documented
rounding boundary. Check arithmetic overflow. Existing `float64` `CostUSD` and
template prices remain reporting inputs, not an authoritative monetary ledger.

Pin pricing by provider, resolved model/version, region, operation, commercial
mode and effective revision. Specify treatment of discounts, provisioned capacity,
subscription entitlements and currency conversion only where supported. Unknown
price or unavailable metering must produce a declared unsupported/advisory/denied
outcome, never a fabricated zero cost.

Reserve against a defensible exposure bound. Input estimation, output limits,
fallbacks, concurrent attempts and non-cancellable work all affect that bound.
Any billable estimation call also needs admission. A strict monetary ceiling
requires an enforceable upper bound or a renewal/checkpoint contract that limits
committed exposure. Otherwise keep the operation advisory or deny it under that
policy. An explicit overrun policy can describe bounded overshoot; it cannot
establish a strict ceiling. Provider billing latency and measurement uncertainty
must be visible in the guarantee.

### Sharing unused quota

Sharing is a quota-owned policy and ledger operation. Specify:

- Eligible lender/borrower accounts, compatible units/currencies and permitted
  scope. A monetary transfer does not implicitly transfer model-specific tokens.
- What is lendable: exclude settled usage, active reservations, protected floors
  and existing commitments. Spare allocation is different from spare throughput.
- Borrow limits, priority/fairness, lease lifetime, fees or debt only if explicitly
  part of the product. State whether nominal shares remain guaranteed after lending.
- Different-period behavior: bind each transfer to source and destination windows
  and a supported expiry. Do not create future-window capacity implicitly.
- Recall and repayment: already consumed tokens or money cannot be recalled.
  Distinguish releasing an unused loan from accounting for spent borrowed capacity.

Transfers must conserve capacity across all involved accounts and respect parent
caps. An example invariant within a supported account/window is:

```text
available = granted + authorized_net_transfers - settled_usage - active_holds
```

Borrowing cannot increase an organization's hard ceiling. Reporting a charge
against both a funding account and a parent cap must not create two independent
provider charges. Tests must cover lending races and simultaneous lender demand.

## Module-owned accounting contracts

The following are required contracts to define, not implemented APIs. Keep them
inside QuotaPlex and its host adapter; reuse the shared trusted context.

| Contract | Required facts and behavior |
| --- | --- |
| Admission input | Trusted tenant/principal/actor/application; canonical provider/model/operation; provider account/project, billing project and region; surface/connection revision; eligible or selected funding; resource/session; estimated and bounded maximum usage; membership/policy/price references |
| Operation identity | Separate logical request, billable attempt, retry/fallback relationship and provider job/session/resource IDs; immutable request fingerprint |
| Decision | Allowed/denied/deferred outcome, matched rule revisions, payer and constraints, reservation identity/expiry, remaining capacity and stable reason codes |
| Reservation | Account/window holds, units, maximum exposure, version/fencing, renew/increase/release rules, durable dispatch state |
| Usage event | Trusted producer, unique event identity, attempt/receipt linkage, actual provider/model/operation, meter units, cumulative/delta/terminal semantics, event/observation times and provenance |
| Settlement | Actual usage and price revision, settled/released amounts, idempotent outcome digest, uncertainty and correction reference |
| Ledger entry | Immutable identity, account/window, currency/unit, grant/hold/consume/release/transfer/adjustment type and causal references |
| Balance | Granted, held, consumed, available and borrowed/lent views; active windows/reset/expiry, as-of consistency and policy revision |
| Recovery | Outstanding-hold ownership, provider-state correlation, reconciliation checkpoints, bounded retries and operator adjustment authority |

`enforcement.Provider.Admit` currently receives only `RequestContext`; `Outcome`
contains `Successful` and integer usage counters. They do not yet carry all quota
facts above. Define a typed quota envelope/adapter through that boundary without
making the shared pipeline own pricing, funding selection or provider JSON.

An independently deployed QuotaPlex also needs a versioned, authenticated
admission/reservation/settlement RPC adapter with deadlines and the same durable
idempotency rules. `modules.Remote` currently verifies descriptors and readiness;
it does not invoke remote enforcement. In-process and remote deployments retain
the same ownership and request path; registration alone never proves admission.

`RequestID` is correlation, not durable accounting identity: the current middleware
accepts a caller request ID or produces a process-local value. Scope accounting
idempotency by tenant and operation/attempt, bind it to immutable input, and reject
the same key with changed content. The operator response cache and in-process
pipeline ticket are not durable reservation recovery.

## Required host integrations

### Admission and durable metering

The current `NativeModel` handler configures authorization, ownership, credentials
and transports. It does not invoke the enforcement pipeline or configure the
usage observer. Its `trusted()` context does not attach a membership snapshot,
application or purpose. The client wire facade delegates to that same handler,
so its future integration should reuse that path.

The quota adapter must admit before billable dispatch, carry its receipt through
the existing execution lifecycle, and durably record outcomes even when the
client disconnects. Bind resolved model/operation, membership, application and
funding facts at their existing trusted owners. Readiness and required-provider
configuration must prevent a protected path from silently skipping QuotaPlex.

The current model observer is a callback, not an acknowledged durable event sink.
Its observations lack a populated durable event/attempt/receipt identity and do
not carry full pricing or actor context. Its extraction covers a subset of native
usage shapes. In particular, forwarding Anthropic events, multimodal payloads or
an AWS event stream does not prove accounting coverage. Protocol-specific
cumulative/delta rules must replace assumptions based only on statefulness.

Define durable handoff, acknowledgement, duplicate/out-of-order event handling,
failure/backpressure behavior and crash recovery. Retain native streaming
framing and cancellation semantics. A failed request can still have billable
usage; `Successful=false` is not a refund instruction.

### Coverage matrix before enabling limits

| Path | Prerequisite for an enforcement claim |
| --- | --- |
| Native model HTTP and client facade | Shared admission, bounded exposure, provider-attributed usage and one durable settlement per billable attempt |
| SSE | Partial/cumulative/final counter semantics, replay deduplication, dropped final frames, disconnect and cancellation accounting |
| Live/WebSocket | Session/resource ownership, bounded or renewable holds, reconnect/resumption identity and partial-session usage |
| Interactions/background/batch/tuning/media jobs | Durable submit-to-job correlation, eventual completion/cancel/reconcile and no double charge from polls or history |
| Caches and ancillary operations | Explicit operation classification, creation/use/storage/expiry treatment and authoritative units |
| A2APlex/managed agents | Native boundary coverage plus trustworthy billable work attribution; opaque internal model/tool work remains unknown until instrumented |
| Envoy or other deployed inference routes | Verified admission and usage integration at the actual route used; configuring the Go API alone does not cover another data path |
| Direct ClientPlex modes and external native gateways | Keep the mode; declare its coverage explicitly. Hard Aiplex limits require a supported enforcement hook or verified routing/control boundary |

Registered clients, saved network profiles, native registry entries and IAM
bindings do not prove all traffic crosses a quota enforcement point. Agent
internal calls may bypass the invocation boundary. Do not count the same provider
charge once at the agent level and again at the model level; record causal links
and aggregate one canonical consumption entry.

Upstream project/model quotas, RPM/TPM limits, provisioned throughput and client
subscription/licensing remain distinct constraints. A QuotaPlex allowance neither
creates provider capacity nor prevents upstream throttling. IAM and token
federation establish identity/access, not an amount of available allocation.
Server-owned billing-project configuration establishes upstream attribution;
caller-supplied billing or cost-center headers cannot select the payer or ledger.

## Durability, invariants and recovery gates

Select and test a private storage implementation that can atomically enforce all
applicable caps and transfers. Existing tenant-scoped resource CAS is useful for
configuration and revisions; it does not expose a multi-account ledger transaction.
The in-memory policy backend similarly needs durable publication/history for a
production deployment. A backend choice should follow measured contention and
consistency needs rather than impose a new database on every Plex.

Before enabling enforcement, demonstrate:

- Concurrent admissions cannot overspend the admitted bounds; user/group/tenant
  constraints succeed or fail together. Transfers and active holds conserve capacity.
- Identical reserve/settle/release replays have one effect; changed replays fail.
  Retries, fallbacks and hedged provider attempts retain separate billable identities.
- A crash before dispatch, after dispatch, after usage and during settlement has
  a recoverable state. Expiring a local lease does not prove upstream work stopped.
- Missing terminal usage, timeouts, lost responses or client cancellation retain
  the appropriate exposure until trusted reconciliation permits release. A
  duplicate event, late report or job poll cannot spend or refund twice.
- Policy, price and membership changes do not rewrite previous decisions. Window
  rollover, daylight-saving changes and delayed settlement preserve allocation rules.
- Corrections use authorized linked ledger entries with bounded refunds and audit,
  not mutable history or negative raw usage counters passed through the pipeline.
- Store outages and stale caches have explicit fail-closed/deferred behavior for
  protected admission. Continuing active work needs a documented bounded policy.
- Restore and recovery preserve deduplication, outstanding holds and prior
  settlements across replicas. Time, retention and reconciliation ownership are explicit.

## Operational and migration gates

Set measurable admission/settlement latency, throughput, hot-account contention
and recovery targets for the deployment. Keep immutable policy/price reads cached
by revision and the reservation critical section bounded. Native identity-directory
or registry administration does not belong on each token's hot path. If local
leases/escrow are introduced later, their total outstanding exposure needs the
same conservation proof as central reservations.

Expose reservation age, pending/unknown settlement, denied admission reasons,
borrowed capacity, ingestion lag, reconciliation drift and provider throttling.
Budget visibility and administrative allocation/transfer/refund APIs require
tenant/account authorization. Audit attribution must retain human and agent;
avoid putting raw credentials, prompts or unbounded identity labels in metrics.

The legacy LLMPlex usage endpoint retains valid reports and emits advisory
warnings when reporting budgets/rates are exceeded. It is not admission
enforcement or an immutable ledger. Its reported costs, trailing day/month
summaries and warning headers must not become a second charging authority.
Accounting must retain actual trusted consumption even when it exceeds a budget;
rejecting a report cannot undo spend.

Run shadow decisions and reconciliation before activating hard limits. Define
cutover, historical import provenance and how legacy reports map to read views.
Rollback must preserve outstanding reservations and settlement/reconciliation;
disabling a UI or module must not discard accounting obligations.

Production validation is scoped to enabled providers and operations. It includes
the chosen durable backend, multi-replica races, restart/restore, actual gateway
routing, native credentials and billing/usage comparisons. Deploying every cloud
identity product is not a prerequisite to writing QuotaPlex. Enable coverage only
where these checks pass.

## Ready-to-start and ready-to-enforce checklists

Before implementing the module, record decisions for:

- [ ] Identity/payer mapping, multi-group funding and simultaneous constraints.
- [ ] Nonuniform allocation periods, overlap, effective changes and clock authority.
- [ ] Meter units, exact money, price revisions and uncertainty/exposure bounds.
- [ ] Typed admission, usage, ledger and reservation/recovery contracts.
- [ ] Atomic persistence and durable idempotency semantics.
- [ ] Sharing/borrowing invariants and the initial operation/deployment coverage matrix.

Before enabling enforcement, require:

- [ ] Quota-owned allocation, reservations and ledger implemented; monetary pricing
  for enabled money limits and lending for enabled sharing policies; required
  immutable policy/membership/price history durable.
- [ ] Existing host paths supply trusted context, admission and acknowledged usage.
- [ ] Per-operation metering, streaming/async recovery and bypass coverage verified.
- [ ] Concurrency, retry, crash, expiry, rollover and reconciliation tests pass.
- [ ] Latency/capacity, audit, outage, backup/restore and administrative controls verified.
- [ ] Shadow comparison, migration, activation and rollback evidence recorded.

These checks preserve the present flow. The pending allocator, lending and
accounting mechanisms are QuotaPlex's implementation backlog, not new responsibilities
for Identity, LLMPlex, A2APlex, ClientPlex, networking or Anvil.
