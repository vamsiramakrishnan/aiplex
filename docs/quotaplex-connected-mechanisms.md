# QuotaPlex connected mechanisms

This page summarises how the Plex boundaries connect. The connections do not
change the permission intersection, move provider semantics into QuotaPlex, or
infer live cloud enforcement from a registry entry.

| Construct | Implemented connection | Contract and evidence |
| --- | --- | --- |
| Configuration | Atomic revisions, tombstones and dependency generations for legacy route/provider/agent/IAM/manual grants; coherent access snapshots | [Consistency and migration](configuration-consistency.md) |
| Membership | Independently verified principal/actor directory snapshots, missing-JWT-group support, signed authority protocol and bounded revocation-aware cache | [Authority contract](membership-authority.md) |
| Accounting operations | Read-only admission preview, validated policy export, paged inventories/balances, audited loans, reviewed corrections and original evidence links | [Administration](quotaplex-administration.md) |
| Scheduling | Queue selection, admitted-capacity debit and quota admission in one transaction; protected partitions, weighted shared credits, deadline/cancellation and proof renewal | `pkg/quotaplex/scheduler_atomic.go`, actual hosted MCP dispatch regressions |
| Native agents | Explicit native HTTP request-count admission, deferred credential acquisition, config/grant refresh, actor-separated controlled sessions and acknowledged transport EOF | `pkg/cloudagents`, actual protected InvokeAgent regressions |
| Native models | Explicit execution coverage and strict text profiles for Gemini, model-backed Interactions, OpenAI Chat/Responses and Anthropic Messages/Vertex | [Metering profiles](model-metering-profiles.md) |
| MCP credentials | Owner/actor-scoped PKCE, code exchange, refresh rotation, revocation and exact-version secret storage feed actual managed tool dispatch | [Runtime authorization](mcp-runtime-authorization.md) |
| Infrastructure | Operator-enabled saved-plan approval/apply, one-shot invocation journal, independent configured native health observations, SDK/CLI | [Anvil execution](anvil-deployment-provider.md) |
| Console | Module-discovered QuotaPlex inspection, loans, policy artifact validation and reviewed correction; stable operation IDs and exact-integer gates | `console/src/pages/QuotaPlex.tsx` |
| Qualification | Dedicated real Firestore emulator CI, replica/CAS/restart/rollback tests, reference benchmarks and exact local billing-evidence comparison | [Qualification runbook](production-qualification.md) |

## Scheduling authority

The host derives a flow from the verified demand's exact resource, operation and
eligible selected payer. Caller-selected priority/flow headers are not authority.
Queue weights, protected admission partitions and residual credits share the same
bounded accounting transaction as budgets, arrival rates and concurrency.
An admitted request still needs fresh dispatch authorization and a single claim.

Expired/cancelled queued work creates no quota hold. A cancelled HTTP waiter only
aborts a core hold if it remains definitely undispatched. Core/queue replay
history survives bounded active-index compaction, client retries, process restart,
quantity-only quota revision changes and CAS-renewed capacity evidence.
Funding eligibility/priority, payer-to-flow mapping and capacity structure cannot
change beneath queued work without explicit migration. A scheduler is not a
provider reservation or stream pacer. Its capacity evidence is operator-attested,
scoped by provider/pool/target and expiry; native minimum throughput must be proven
separately.

Trusted accounting time is sampled after consistent transaction reads. Waiting
behind another transaction cannot backdate a new rate decision or dispatch claim;
actual clock rewind still fails closed. This does not eliminate cross-host clock
skew or replace production clock monitoring.

## Operational invariants

- A correction never edits the original provider event or silently selects new
  prices. The reviewed attempt/evidence state, typed proof, settlement, approval
  receipt and audit commit together. Correcting an attempt does not clear a
  binding quarantine.
- OAuth secrets live in the vault, not resource JSON, URLs, argv or logs.
  Ambiguous code/refresh outcomes do not automatically repeat writes. Revocation
  fences pending callbacks and new tool dispatch even if secret cleanup retries.
- Infrastructure approval binds the original caller/actor, saved intent/network
  revisions, native bundle/runtime identity and expiry. Successful invocation and
  observed native health remain separate evidence. Health of an existing target
  does not prove desired-spec convergence or establish an unknown write outcome.
- Browser quota mutations retain operation identity after an uncertain response.
  Decimal fractions, exponent underflow and browser-unsafe integers are rejected
  before JSON parsing can round them. Exact larger int64 values use the API/CLI.

## Deployment and capability limits

No live cloud apply, native directory synchronization, enterprise sign-in or
provider invoice collection has been exercised. The HTTP membership
protocol requires a real trusted directory authority; it does not manufacture
Google/Entra/AWS directory evidence. Existing native credential brokers still
need their supported runtime credential sources and target-environment wiring.

Direct-provider/deployed Envoy paths do not gain automatic quota interception.
Opaque cloud-agent internal work, Live/multimodal/server-tool/cache-write/background
token accounting, legacy stateful/bidirectional MCP and native cancellation/
preemption need explicit metering and lifecycle adapters. Request-count coverage
of an async API counts that exchange, not internal execution or storage.

Policy validation produces a reviewed deployment artifact, not an unsafe live
hot-publication. Arbitrary repricing, implicit FX, split charging, rolling budgets,
automatic lending allocation, live group-population rebalancing and structural
queue migration are not installed. Current unequal fixed/anchored/calendar
allocations and explicit loans remain available.

Qualify real provider ceilings/prices, native identity/consent flows, target client
compatibility, private routing, desired-state convergence, production contention,
backup restore, admission latency and billing equivalence before expanding the
hard-enforcement claim. The executable local/CI evidence and the live gates are
recorded separately in the [qualification runbook](production-qualification.md).
