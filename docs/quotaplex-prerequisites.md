# QuotaPlex prerequisites and readiness

Status: implementation checklist, audited against the native-governance
foundation and the [implemented QuotaPlex core](quotaplex-core.md), including the
[feature cohesion repairs](feature-cohesion-bugbash.md).
This checklist preserves the existing architecture and distinguishes pending
quota work from implemented reporting behavior.

Aiplex has the module, identity, policy and provider seams needed to start
QuotaPlex. Hard token or monetary enforcement additionally needs accounting
contracts, durable state, trusted usage and host integration. The existing
foundation tests establish those seams; they do not prove a quota allocator.

The [foundation design](quotaplex-foundation-design.md) extends the implementation
baseline to uneven group sizes, tool work, rates, concurrency and scheduling.
QuotaPlex has a shared control core and an [opt-in managed execution layer](quotaplex-integration.md)
for supported model/tool profiles. Default traffic and direct-provider/deployed
gateway paths do not automatically gain coverage.

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
| Q01 | Independent module boundary | Present: library lifecycle and opt-in Go API module with startup/readiness/recovery ownership | Configure explicit module/file and shared backend; authenticated remote admission RPC remains pending |
| Q02 | Trusted tenant, principal and agent actor | Present: managed host binds verified canonical subjects, target/configuration digest and funding | Configure trusted sources and independent actor evidence; no payer from email, sponsor or unchecked headers |
| Q03 | Multiple-group membership at admission | Present: signed principal group/lifetime evidence and separate configured actor snapshots; host attaches both to admission | Live directory synchronization and revocation freshness need their configured authority; request time never fabricates membership |
| Q04 | Funding and overlapping limits | Present: one eligible selected/highest-priority payer plus all matched mandatory constraints; ambiguous selection denied | Configure tenant funding rules; split charging and implicit group-balance union remain unsupported |
| Q05 | Nonuniform allocations and periods | Present: explicit quantities, fixed/anchored/calendar windows, effective revisions and frozen population strategies | Rolling budgets and structural window migrations remain unsupported; host validates trusted population evidence |
| Q06 | Admission and receipt lifecycle | Present: actual managed generation/tool dispatch uses typed bound admission, one-shot claims and explicit terminal evidence | Additional operations/transports and direct routes need their own covered dispatch/usage adapters |
| Q07 | Stable accounting identities | Present: host-derived subject-scoped attempt/logical IDs, native payload/configuration digests and durable resource/outbox linkage | Clients retain idempotency keys for transport retries; new billable attempts use new keys and preserve causal linkage |
| Q08 | Immutable decision provenance | Present: policy activation CAS, immutable meter/price revisions, pinned membership/demand/plan and stable reason codes | Authorized publication, retention and production recovery/administration integration |
| Q09 | Atomic ledger and balance storage | Present: quota-owned [transaction contract](../pkg/quotaplex/store.go), memory reference and [Firestore adapter](../internal/registry/quota_store.go) | Live multi-replica, restore, contention, IAM and deployment validation; single-resource CAS is not the ledger implementation |
| Q10 | Reservation exposure bounds | Present: request-count profile and strict Gemini-text profile with versioned provider-limit authority; unknown exposure retained | Verify real provider-enforced ceilings for each version/account/region; broader media/tool/async/renewal bounds remain pending |
| Q11 | Trusted metering | Present: acknowledged terminal outbox and supported native/model/tool producer normalization | Prove provider-specific billable composition and live billing equivalence; add uncovered cumulative/delta/async producers |
| Q12 | Exact monetary accounting | Present: integer meters/currency scale, rational prices, one aggregate rounding boundary, overflow checks and immutable prices | Verify actual commercial rates and billable composition; no implicit currency conversion or trust in legacy floating reports |
| Q13 | Sharing unused allocation | Present: explicit loans, protected allocation floors, expiry guards and opt-in bounded unused-loan maintenance | Authorized loan administration and operational reconciliation; automatic lending allocation and throughput sharing remain separate work |
| Q14 | Failure and recovery behavior | Present: fenced/backoff terminal settlement, durable evidence/work state and restart-safe bounded scans | Built-in worker cursors are process-local and rescan after restart; no native job resolver is installed, uncertain dispatched work remains held and corrections remain pending |
| Q15 | Enforcement coverage | Partial: native generation JSON/SSE, same client facade and explicit stateless tool invoke path have managed profiles | Use the exact coverage matrix; opaque managed agents fail501 when enabled; direct/native/Envoy/other specialized paths need separate proof |
| Q16 | Balance and decision APIs | Present: SDK/CLI and owner/admin attempt-status API, plus library GetAttempt/GetLoan/BudgetBalance | Broader tenant-authorized allocation/balance/loan administration and current-policy/as-of explanations |
| Q17 | Reconciliation and operations | Partial: lifecycle-owned bounded worker and immutable evidence/quarantine mechanisms | Deploy alerts/runbooks, trusted native resolvers, authorized correction/repricing, backup/restore and outage validation |
| Q18 | Performance and rollout | Production | Measured latency/QPS/contention bounds, multi-replica tests, shadow comparisons and rollback preserving in-flight accounting |
| Q19 | Rate and burst controls | Present: shared-backend atomic admitted-demand buckets integrated with supported managed dispatch | Verify configured limits across production replicas; no observed-only or provider-decoding-rate guarantee |
| Q20 | Concurrency permits | Present: managed dispatch retains scoped permits through unknown work and releases only known terminal/undispatched exposure | Long-running ownership renewal/native cancellation contracts and live recovery validation remain operation-specific |
| Q21 | Scheduling and assigned QoS | Partial: bounded serializable weighted DRR selection with deadlines/cancel/replay limits | Atomic scheduling/admission, priority/protected shares, feasible capacity pools and verified provider capacity; selection grants no dispatch right |
| Q22 | Tool and other resource meters | Present: explicit stateless JSON HTTP tools/call path produces acknowledged request/tool-call counts | Sessions, SSE, delegated OAuth runtime credentials, duration/row/byte billing and further tool operations need verified adapters |
| Q23 | Group population allocation | Present: explicit frozen per-member strategy bound to a source-qualified group snapshot; no implicit headcount normalization | Host-authorized population snapshots and effective-boundary operations; no automatic live rebalance |

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

## Rates, concurrency and assigned QoS

Keep four controls distinct within the same QuotaPlex policy and admission plan:

| Control | Governs | Required semantics |
| --- | --- | --- |
| Budget | Consumption over an allocation period | Bounded reservation and trusted settlement; remaining money/tokens/work units do not imply available throughput |
| Rate | Admitted or observed work over short intervals | Sustained rate, burst and meter basis; request/s and token/s remain separate dimensions |
| Concurrency | Outstanding execution | Atomic permits, fencing, renewal and known-terminal release; include streams, jobs and long-running tools |
| Scheduling | Who receives scarce capacity next | Bounded queues/deadlines, protected shares, weights, ceilings, borrowing of idle throughput and starvation rules |

Assigned throughput requires an identified capacity pool and a feasible scheduling
policy. An application allowance cannot create provider capacity. Count admission
tokens separately from provider-generated output tokens: reserving a maximum at
dispatch limits admitted exposure, while delaying already generated SSE frames
only changes client delivery. Neither establishes an upstream generation rate.

Tool requests use the same controls with operation-appropriate meters. A tool
may need calls/s, concurrent executions and execution-time or row budgets; it
need not expose a fictitious token count. Long-lived MCP sessions are distinct
from individual tool executions.

Budget lending and throughput sharing need separate policy/state. A loan commits
consumable allocation between particular windows; a scheduler lends idle service
capacity while preserving feasible protected shares and parent throughput caps.
Queue admission must consume no billable budget or execution permit until the
defined dispatch transaction, or retain an explicit bounded queue hold. Retry and
cancellation behavior must release only the capacity known to be unused.

All applicable budget, rate and concurrency checks form one atomic admission:
failure at any check commits none of its proposed changes. A typed demand envelope
belongs to QuotaPlex and its explicit adapter; do not overload request/resource
IDs or hide funding/meter facts in arbitrary context values.

## Module-owned accounting contracts

The shared core implements the initial typed demand, plan, terminal settlement,
ledger and recovery mechanisms. The following register describes the complete
required contract, including pending streaming, renewal and adjustment work.
See the [core guide](quotaplex-core.md) for the implemented subset. These contracts
remain inside QuotaPlex and its explicit host adapter.

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
contains `Successful` and integer usage counters. `Demand`, `Plan` and the bound
QuotaPlex provider now carry the quota-owned facts through that boundary. The
adapter requires explicit dispatch and trusted terminal settlement; the generic
pipeline does not own pricing, funding selection or provider JSON.

An independently deployed QuotaPlex also needs a versioned, authenticated
admission/reservation/settlement RPC adapter with deadlines and the same durable
idempotency rules. `modules.Remote` currently verifies descriptors and readiness;
it does not invoke remote enforcement. In-process and remote deployments retain
the same ownership and request path; registration alone never proves admission.

`RequestID` is correlation, not durable accounting identity: the current middleware
accepts a caller request ID or produces a process-local value. The core scopes
attempt idempotency by tenant and immutable input, including separate payload and
configuration digests; changed input conflicts and correlation changes do not
create another accounting operation. The operator response cache and in-process
pipeline ticket are not durable reservation recovery.

## Required host integrations

### Admission and durable metering

The configured managed host now uses acknowledged execution hooks in `NativeModel`
and the explicit stateless tool invoker. It binds membership, application, purpose,
funding, target and configuration facts before admission, then rechecks current
execution grants/configuration before dispatch. The client facade shares that
native path. Unsupported protected operations fail closed; the module remains opt-in.

The quota adapter must admit before billable dispatch, carry its receipt through
the existing execution lifecycle, and durably record outcomes even when the
client disconnects. Bind resolved model/operation, membership, application and
funding facts at their existing trusted owners. Readiness and required-provider
configuration must prevent a protected path from silently skipping QuotaPlex.

The legacy observation callback remains separate from the new acknowledged
execution lease/outbox. Supported producers now bind terminal evidence to attempt,
claim, configuration, producer and observed model proof. Their extraction still
covers only declared profiles. Forwarding Anthropic events, multimodal payloads or
an AWS event stream does not establish quota coverage; every added operation needs
its own cumulative/delta/finality and exposure contract.

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

The quota transaction contract has memory-reference and native Firestore
implementations for caps, transfers, immutable policy/price history and replay.
SDK tests cover composition and recovery mechanisms; production must validate
the chosen backend's live contention, permissions, durability and restore behavior.
Existing single-resource CAS and the generic in-memory policy store remain
separate foundations. Additional backends can implement the quota contract
without imposing another database on every Plex.

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

## Core implementation and ready-to-enforce checklists

The initial foundation now implements:

- [x] Typed subjects, independent memberships, selected payer and simultaneous constraints.
- [x] Explicit unequal allocations and supported fixed, anchored and calendar windows.
- [x] Extensible integer meters, exact money, immutable price revisions and overflow handling.
- [x] Typed admission, dispatch/resource pinning, unknown retention and terminal settlement.
- [x] Atomic memory-reference/Firestore storage and durable replay mechanisms.
- [x] Explicit compatible-budget loans, protected allocation floors and expiry guards.
- [x] Admitted rate/burst controls, concurrency permits and bounded weighted queue selection.
- [x] Frozen source-qualified population allocation; no automatic headcount rebalance.

The core guide identifies unsupported rolling windows, structural migrations and
partial/correction/renewal workflows. Capacity floors and atomic scheduling/admission
remain separate mechanisms to finish before claiming assigned-throughput guarantees.

Before enabling enforcement, require:

- [ ] Validate allocation, reservations, ledger, configured prices and immutable
  policy/membership/price history on the chosen production backend and deployment.
- [x] Supported managed paths supply trusted context, admission and acknowledged usage.
- [ ] Additional enabled operations and deployment routes pass the same coverage gate.
- [ ] Per-operation metering, streaming/async recovery and bypass coverage verified.
- [ ] Deployment-level concurrency, retry, crash, expiry, rollover and reconciliation tests pass.
- [ ] Compound budget/rate/concurrency admission, bounded queues, weighted sharing,
  tool metering and supported throughput guarantees pass their own invariant tests.
- [ ] Latency/capacity, audit, outage, backup/restore and administrative controls verified.
- [ ] Shadow comparison, migration, activation and rollback evidence recorded.

These checks preserve the present flow. Remaining automation, capacity scheduling,
recovery and enforcement integration stay with QuotaPlex and its explicit host
adapters. Identity, LLMPlex, A2APlex, ClientPlex, networking and Anvil retain their
existing responsibilities.
