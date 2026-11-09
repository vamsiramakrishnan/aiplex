# QuotaPlex foundation design

Status: proposed implementation baseline, independently reviewed against the
existing foundations. This document specifies the next work; it does not claim
that a quota engine, scheduler, durable ledger or host enforcement exists.
See [prerequisites and readiness](quotaplex-prerequisites.md) for current evidence.

Build one resource-control core for LLMPlex, MCPlex, A2APlex and SkillsPlex.
Provider adapters supply trusted demands, capacity coordinates and usage; they
do not implement their own funding, window, borrowing or rate-limit logic.
QuotaPlex stays independently pluggable. Existing gateway ownership, verified
identity, authorization and native protocol forwarding remain unchanged.

## Constructs and ownership

| Construct | Meaning and owner |
| --- | --- |
| Subject | Verified human principal, autonomous/delegated agent actor and application, retained separately; identity supplies trusted facts |
| Membership | Source-qualified groups and freshness/revision evidence; identity resolves membership, QuotaPlex pins the decision snapshot |
| Account | Tenant-scoped funding or constraint coordinate; accounts need not correspond one-to-one with identities or groups |
| Allocation | An explicit quantity, meter/currency, account, period and effective policy revision |
| Demand | Typed trusted operation, attempt, bounded resource exposure, selected funding request and capacity-pool coordinates |
| Meter | Versioned definition of an integer unit and aggregation semantics; adapters normalize native measurements |
| Budget | Cumulative consumption and active holds within an allocation window |
| Rate | Admission volume over elapsed time, with explicit burst, unit and accounting basis |
| Concurrency | Admitted active work, controlled by fenced acquire/renew/release state |
| Capacity pool | Actual constrained provider/project/region/model/tool resource; distinct from the payer account |
| Queue | Bounded waiting work with deadlines, cancellation and scheduling policy; waiting does not create a spending hold |
| Ledger | Immutable causal financial/resource entries plus atomic projections and durable replay records; QuotaPlex owns storage semantics |

Tool meters can include calls, execution milliseconds, rows, bytes, jobs and
provider charges. Models can use input/output/cached/reasoning tokens, requests
and other verified native units. Units have explicit scope: tokens from different
tokenizers or model families are interchangeable only where policy declares it.

## Proposed baseline policy decisions

These choices make the first core deterministic. Unsupported alternatives must
be rejected explicitly rather than silently approximated.

| Concern | Baseline |
| --- | --- |
| Funding | One selected eligible payer per billable attempt; deterministic policy priority resolves a missing selection; ambiguous priority is an error |
| Simultaneous limits | Every matched mandatory user, agent, account, group, tenant and resource constraint must pass together |
| Group overlap | Membership establishes eligibility or constraints; it never implicitly adds the balances of all groups |
| Group size | Allocation is independent of headcount by default; per-member or weighted allocation is an explicit strategy |
| Headcount strategy | Resolve a trusted membership-count snapshot at policy activation/window creation; freeze its revision and rounding/remainder rule |
| Money | Integer amounts with explicit currency/scale; versioned rational unit prices and a declared cumulative rounding boundary |
| Budget windows | Discrete budgets pin admission-window identity; late settlement cannot move the charge to a fresh window |
| Rate accounting | Accepted admissions debit requests or declared bounded token/tool demand; rejected/deferred admissions debit nothing |
| Rate refunds | Accepted rate debits are not refunded by cancellation or upstream failure; spending holds and concurrency follow their own lifecycle |
| Unknown usage/price | Strict policies deny unsupported exposure; advisory policies label the unknown value and never manufacture zero cost |
| Queue behavior | Reject by default; queuing requires explicit finite length, maximum wait and scheduling policy |
| Sharing | Disabled by default; budget loans and throughput borrowing have separate opt-in policies |
| Outages | Protected admission fails closed or returns a bounded deferral; cached permission or balance views cannot authorize spending |

Charge splitting, automatic live headcount rebalance, currency conversion,
unbounded queues and overdrafts are not implied by this baseline. Add them only
through versioned policy and corresponding conservation/recovery tests.

### Identity, funding and constraints

An attempt records tenant, canonical principal, actor, application, payer and all
constraint accounts. A human using an agent can consume an approved group budget
while remaining subject to both human and agent caps. An autonomous agent uses
its own policy mapping; a sponsor or delegated user is not automatically its payer.

Funding selection must be checked against the trusted membership snapshot and
tenant policy. Client-supplied groups, email addresses, billing-project headers
or cost-center labels cannot grant eligibility. A queued request revalidates
authorization and membership freshness before admission. Admission pins policy,
membership, allocation and price revisions; later edits do not rewrite history.
Human and agent group rules require separate verified subject snapshots; the
current shared request snapshot describes only the principal. A human's groups
do not implicitly authorize an agent's funding account, or vice versa.

Debit the provider charge once. Recording that charge against a payer and several
parent constraints creates multiple constraint projections, not multiple bills.
Explicit zero allocations deny consumption; absent policy is not an unlimited
grant on a protected operation. Deny rules and conflicting revisions produce
stable reasons, with no opportunistic fallback to another payer after dispatch.

### Periods and exact quantities

| Period | Required semantics |
| --- | --- |
| Fixed | Exact UTC start/end, half-open interval `[start, end)` |
| Anchored recurring | UTC anchor plus positive elapsed duration; independent durations and quantities per allocation |
| Calendar | Calendar unit, IANA timezone, anchor and explicit invalid-date/DST resolution; reject ambiguous configuration |
| Rolling | Timed consumption history, exact expiry and active exposure; not an anchored bucket or trailing reporting summary |

Discrete windows retain an allocation lineage and exact interval; decisions pin
the effective revision. Publishing a revision cannot reset usage or mint another
balance for the same interval. Mid-window changes preserve committed usage/holds;
a reduction below commitments blocks new work.
Carryover requires its own source-window identity and expiry. Storage TTL is
cleanup, never the authority for a reset, refund or release.

Rolling budgets require authoritative usage-time events, late-event handling and
active-hold treatment. A strict rolling policy remains unsupported until its
backend implements those semantics. A token bucket does not substitute for it.

Keep raw provider counters separate from billable meters. Total tokens and their
input/output/cached/reasoning subsets cannot be summed indiscriminately. Prices
pin provider, resolved model/version, operation, region and commercial mode.
Use checked integer/rational arithmetic; reject overflow and unit/currency
mismatch. Round cumulative monetary results at the declared settlement boundary,
so different stream chunking cannot change the final charge. No floating-point
legacy report is accepted as an authoritative ledger amount.

## Atomic admission and lifecycle

Expose typed QuotaPlex admission, usage, settlement and recovery contracts.
A narrow host adapter participates through `enforcement.Provider` / `Receipt`.
Keep that generic seam small: it must not learn account selection, pricing,
provider JSON or scheduler internals. Do not pass an untyped quota payload through
`context.Value`; bind and validate a typed demand against the trusted request.

For one admission transaction:

1. Authenticate and authorize the calling service/tenant, validate the typed
   envelope and compute its immutable input fingerprint.
2. Look up the tenant/attempt idempotency record. An identical replay returns its
   pinned result, without recomputing the historical decision using new windows
   or expired membership; changed content conflicts. Reading an old allowed
   result grants no additional dispatch. Dispatch requires its own durable claim.
3. For a new admission, validate subject memberships and bounded maximum exposure,
   then resolve the applicable policies, windows and capacity limits.
4. Atomically check all cumulative caps, rate buckets and concurrency constraints.
   Commit holds, accepted rate debits, fenced concurrency ownership, ledger entries
   and the decision/replay record together. A denial persists only its replay
   result; it changes no budget, rate or concurrency capacity.
5. Persist dispatch intent before invoking the provider outside the transaction.
   Revalidate current execution authorization and reservation validity for new
   dispatch. Network ambiguity becomes durable unknown work, never an assumed
   non-dispatch.
6. Accept acknowledged, deduplicated usage and settle/release against the pinned
   decision. Reconciliation resolves unknown work and applies linked adjustments.

Use separate logical-operation, billable-attempt, reservation, dispatch,
provider-resource and usage-event IDs. A retry/fallback that can incur another
provider charge needs its own attempt. A repeated rejected attempt retains its
recorded result; retrying admission after availability changes uses a new attempt.
Queued work has a durable queue identity and transitions into one admission;
replaying enqueue must not create another queue position or reservation.

The lifecycle distinguishes admitted/undispatched, dispatching, active, unknown,
settled and aborted work. Abort before dispatch can release unconsumed spending
and concurrency reservations. After possible dispatch, cancellation is not proof
of zero usage or stopped work. Lease expiry alone cannot release unknown exposure.
Renewal uses fencing; stale owners cannot extend or settle a newer generation.
Once abort starts, settlement cannot proceed through a reduced provider set.

The storage contract must atomically enforce every affected account/window,
rate/concurrency state, transfer and replay record. Transaction callbacks must
be deterministic and side-effect free under retries. Bound transaction fanout
and reject unsupported policy combinations before touching state. Existing
`resources.Store` CAS is suitable for configuration, not this multi-account ledger.
An in-memory reference backend proves semantics only; production needs durable
recovery, multi-replica tests and measured contention limits.

## QoS, rates and capacity scheduling

| Control | It establishes | It does not establish |
| --- | --- | --- |
| Token/money/tool budget | Maximum cumulative admitted exposure and settled consumption | Provider capacity or request latency |
| Requests/s | Accepted request arrival ceiling and burst allowance | Equal work per request or output speed |
| Admitted tokens/s | Bounded token demand accepted over time | Exact generated output tokens/s |
| Concurrency | Maximum admitted active work under the supported lease/cancel contract | That timed-out upstream work has stopped |
| Weighted scheduling | Relative service among eligible queued demand in a capacity pool | A guaranteed share exceeding physical/provider capacity |
| Client stream pacing | Delivery rate to a client | Provider decoding rate, provider cost or upstream token consumption |

Rate policies name the meter, accounting basis, sustained rate, burst and scope.
Observed-only measurements cannot retroactively enforce an admission ceiling.
Provider capacity coordinates include the actual credential/project/account,
region, model/tool and commercial capacity where applicable. A payer's unused
money does not create spare capacity in a saturated provider pool.

An opt-in scheduler uses bounded queues and explicit deadlines. Weighted,
work-conserving scheduling assigns spare capacity among eligible demand without
starving protected classes. Policies distinguish protected floors, weights and
hard ceilings. Validate that the sum of protected commitments fits configured
capacity; refuse infeasible guarantees. Weights alone are relative shares.
Queued work holds no spending/concurrency allocation until actual admission;
deadline expiry and cancellation remove its claim. Protect against head-of-line
blocking from oversized demands using explicit cost classes or a documented
multi-resource scheduler, not arbitrary queue skipping.

Strict generated-token throughput requires an enforceable upstream scheduling,
checkpoint or cancellation boundary and verified provider capacity semantics.
Admission shaping can protect load without claiming those guarantees. Local
leases/escrow may reduce central-store overhead later only if total outstanding
capacity remains bounded and conserved; avoid a durable write per output token.

## Sharing rules

Budget loans debit lendable source capacity and credit a named borrower/window in
one transaction. Exclude settled usage, active holds, protected floors and prior
loans. Restrict units/currencies and parent caps. Expiry is bounded by the source
window, destination window and loan policy. Never mint a future-window allowance.
Unused loan capacity can be returned; spent borrowed capacity cannot be recalled.
An expired loan with committed work retains the exposure needed for settlement.

Throughput borrowing instead permits temporary use of idle capacity in a shared
scheduler. It transfers neither money nor permanent token entitlement. State
whether floors stay protected and when new work must yield to returning demand;
already dispatched non-preemptible work cannot be instantly reclaimed.

For each supported budget account/window, maintain the invariant:
`available = granted + authorized_net_transfers - settled_usage - active_holds`.
Parent ceilings and transfer conservation must hold under simultaneous lender
demand, borrower demand, expiry, retries and policy changes.

## Implementation order and acceptance gates

| Order | Deliverable | Required evidence before the next layer |
| --- | --- | --- |
| 1 | Versioned typed subjects, accounts, meters, money, periods and policy compiler | Unequal group quantities/windows, dual human/agent constraints, frozen headcount strategy, unit errors, overflow and DST/boundary tests |
| 2 | Transaction contract and executable reference core | Atomic multi-cap budget/rate/concurrency admission, identical/changed replay tests, concurrent contention and rollback/conservation properties |
| 3 | Durable storage and lifecycle/recovery | Restart at every dispatch/usage/settlement boundary, fencing, duplicate/out-of-order usage, late settlement, unknown work and restore tests |
| 4 | Sharing and bounded scheduling mechanisms | Protected floors, hard ceilings, unequal weights, finite queues/deadlines, infeasible-capacity rejection and loan/throughput separation |
| 5 | Narrow host adapters and capability coverage | Trusted membership/demand binding, admit before billable dispatch, acknowledged usage, no silent bypass and preserved native stream semantics |
| 6 | Shadow rollout, then scoped enforcement | Decision explanations, reconciliation, failure-mode coverage and measured latency/QPS/contention; enable only proven operations/deployments |

Implement the shared mechanisms before a provider-specific vertical slice.
Declare unsupported capabilities and return stable denied/deferred/advisory
outcomes explicitly. Registration, a policy document, a native identity link or
successful unit tests alone must never advertise production quota/QoS enforcement.
