# QuotaPlex core

QuotaPlex is a provider-independent Go library in [pkg/quotaplex](../pkg/quotaplex).
It implements the accounting and admission core shared by model, tool,
agent and skill execution. The library installs no routes by itself. An optional
[managed execution integration](quotaplex-integration.md) binds supported model
and stateless tool calls to admission, acknowledged terminal usage and recovery.
Default traffic does not acquire quotas by registering the library. See the
[allocation and metering semantics](quotaplex-allocation-and-metering.md) and
[requirements and limits](quotaplex-prerequisites.md).

## Implemented boundary

| Capability | Current behavior |
| --- | --- |
| Funding | One explicitly eligible payer, or highest policy priority; equal priorities require a selection |
| Subjects | Independent verified principal and agent-actor membership snapshots, with tenant/subject/reference/freshness binding |
| Unequal allocations | Fixed quantities or explicit frozen group-population allocations; membership count never silently divides or increases an allowance |
| Periods | Half-open fixed windows, anchored elapsed intervals, and supported local-midnight day/week/month calendar windows |
| Meters | Integer model/tool work units and explicit money meters; unknown raw meters and caller-supplied money bounds are rejected |
| Exact pricing | Versioned target-matched rational prices; aggregate each money meter and round once, using checked arithmetic |
| Admission | All matched budget, rate and concurrency changes commit together; denial stores replay state without consuming those capacities |
| Rates | Exact-rational token buckets for admitted demand, with sustained rate and burst; accepted debits are not refunded |
| Concurrency | One active permit per admitted attempt in every matched scope; known-undispatched abort or terminal settlement releases permits |
| Lifecycle | Single dispatch claim, retained unknown exposure, idempotent terminal usage acknowledgement and original-plan settlement |
| Usage recovery | Immutable terminal outbox, fenced/backoff settlement work, bounded recovery pages and retained mismatched-model evidence |
| Allocation loans | Explicit policy-authorized transfers between compatible account budgets; source/destination windows, floors and outstanding limits are pinned |
| Weighted selection | Bounded DRR primitive plus opt-in atomic queue/admission/capacity scheduler with protected partitions, deadline/cancellation and retained replay history |
| Storage | Snapshot-isolated memory reference backend and a native multi-document Firestore transaction adapter |

Rolling windows are rejected. Calendar configurations with unsupported anchors
or ambiguous/nonexistent midnight boundaries are rejected rather than approximated.
An admitted token rate controls accepted demand; it is not a provider decoding
rate, minimum throughput commitment or latency guarantee. Concurrency holds are
not automatically reclaimed because a process, request deadline or lease expires.

## Policy compilation and activation

`Compile(Policy)` validates configuration and owns a private copy. `Policy()`,
`Lineages()` and `Resolve()` return independent snapshots. Rule/account declaration
order does not change the compiled digest. A policy selects exact target fields,
including provider, resource, operation, billing account, region and model version.
Overlapping price books are ambiguous even if one appears more specific.

`NewEngine(compiled, store, clock)` binds one tenant and a trusted host clock.
`Activate(ctx, expectedDigest)` atomically publishes immutable policy/price history
and changes the tenant's active head. First activation uses an empty expected
digest; an upgrade uses the prior active digest and a strictly increasing revision.
`Ready` requires that exact policy to remain active and effective.

Budget keys exclude policy revisions. Quantity reductions retain prior usage and
holds; a reduction below committed exposure blocks new work. Structural changes
to a saved rule's scope, meter or period fail through persistent lineage guards.
Every meter definition is guarded, including raw units referenced only by prices;
changing a definition under the same meter ID cannot reinterpret old balances.
Rate/interval/burst changes and sharing-term changes also require a future explicit
migration. Old engines cannot admit against a replaced active head; already
committed attempts settle against their original plan and prices.

The host must authenticate and authorize configuration, funding selection and
loan administration. A valid struct is not evidence of authority. Memberships
may be cached verified snapshots, but principal and actor facts remain separate.
Supply `Demand.Context.MembershipSnapshotRef` for the principal and an independent
actor snapshot when `ActorID` is present. An authoritative empty group list is
different from missing membership evidence.

## Embedding through the existing pipeline

The host configures one engine and creates a bound provider for each attempt.
`policy`, `demand`, `authorizeDispatch` and `invokeNative` below are trusted host
inputs. `invokeNative` must establish whether its usage is terminal; a timeout or
client disconnect alone cannot establish that fact.

```go
compiled, err := quotaplex.Compile(policy)
if err != nil { return err }
store := quotaplex.NewMemoryStore() // reference backend; use shared durable storage
engine, err := quotaplex.NewEngine(compiled, store, nil)
if err != nil { return err }
if err = engine.Activate(ctx, ""); err != nil { return err }

// Configure once above. Create this binding for one immutable billable attempt.
bound, err := engine.Bind(demand, authorizeDispatch)
if err != nil { return err }
pipeline, err := enforcement.New(
    []string{"quota"}, map[string]enforcement.Provider{"quota": bound},
)
if err != nil { return err }
ticket, err := pipeline.Admit(ctx, demand.Context)
if err != nil { return err }
claim, err := bound.ClaimDispatch(ctx)
if err != nil {
    return err // inspect/recover the stored attempt; do not assume dispatch state
}

usage, providerResourceID, terminal, invokeErr := invokeNative(ctx)
if providerResourceID != "" {
    if err = engine.RecordProviderResource(ctx, claim, providerResourceID); err != nil {
        return errors.Join(invokeErr, err) // retain the known native ID for retry
    }
}
if !terminal {
    markErr := bound.MarkUnknown(ctx, "terminal usage unavailable")
    return errors.Join(invokeErr, markErr, quotaplex.ErrTerminalEvidenceRequired)
}
if _, err = bound.SettleTerminal(ctx, terminalEventID, usage, providerResourceID); err != nil {
    return errors.Join(invokeErr, err) // acknowledgement failed; retain/retry evidence
}
return errors.Join(invokeErr, ticket.Complete(ctx, enforcement.Outcome{
    Successful: invokeErr == nil, Usage: usage,
}))
```

Use `context`, `errors`, `github.com/vamsiramakrishnan/aiplex/pkg/enforcement`
and `github.com/vamsiramakrishnan/aiplex/pkg/quotaplex` imports as needed.
In a running host, cache compiled policy/engine configuration, inject a shared
store, and activate through authorized administration rather than per execution.
The dispatch callback rechecks current verified execution authority outside the
storage transaction and compares `Target.ConfigurationDigest` with the actual
resolved connection/configuration before acquiring the claim. Both that digest
and `PayloadDigest` are canonical SHA-256 values. Binding checks exact
trusted-context equality; demand data
does not travel through arbitrary `context.Value` payloads.

Use a separate logical-operation ID, billable-attempt ID and SHA-256 payload
digest. Supply a stable tenant-unique `terminalEventID` bound to the attempt and
trusted producer; acknowledgement retries retain that ID. Caller correlation IDs
are not accounting identity. Identical attempt
replays return the pinned result; changed content conflicts. A replay of an
allowed result grants no additional dispatch. A new billable retry/fallback needs
its own attempt. A denied attempt retains its decision; a later admission attempt
uses a new identity instead of mutating that historical decision.

Every matched raw control meter needs an explicit bound. Requests/calls require
positive attempt bounds. Price-book inputs must be bounded, and applicable money
controls require explicit prices for billable inputs, including zero prices.
`SettleTerminal` requires every bounded raw meter, including explicit known zeros.
`Outcome.Successful=false` grants no refund; `Complete` requires terminal evidence.

Before known dispatch, `Abort` can release unused spending and concurrency holds.
After a dispatch claim, uncertain work uses `MarkUnknown`. Durable recovery uses
`GetAttempt`, stored dispatch/provider IDs and a trusted terminal producer. Actual
overruns are retained as consumed debt and flagged `settled_overrun`; they are not
discarded to preserve a limit. Unpriced/overflowing terminal usage fails its
acknowledgement and leaves outstanding exposure for retry or reconciliation.

`EnqueueTerminal` separately acknowledges durable terminal evidence and returns a
`UsageAcknowledgement`. `ProcessTerminal` settles it idempotently; pending evidence
retains its exposure. `NewRecovery` supplies bounded outbox, attempt and loan
passes. A configured `TrustedRecoveryResolver` can read an already submitted
native resource; it must never replay provider writes. The managed host uses the
outbox before releasing terminal data and starts these passes with no native
resolver by default. See the [operational guide](quotaplex-integration.md).

Pin a native job/session ID through `RecordProviderResource` as soon as the host
knows it, before waiting for terminal usage. The pin survives restart, permits an
identical retry and rejects a changed resource. `GetAttempt.ProviderResource`
exposes it during active/unknown work; terminal settlement must retain that exact
ID. Pinning grants no additional dispatch and releases no exposure.

## Storage and module lifecycle

Use `existingFirestoreStore.QuotaStore()` to obtain the backend adapter from
[the configured registry store](../internal/registry/quota_store.go). It uses
tenant-scoped opaque coordinates, checks stored namespace metadata, stages writes
until reads finish, and retries native Firestore transactions from clean state.
Admission prefetches its known control/ledger records in one native batch read,
then checks cached snapshots. Unused loan indexes do not add reads to accounts
without borrowed credit. No directory lookup, native provider call or per-token
durable write is added to the admission transaction.
Construction enables no quota policy or enforcement. SDK wire tests cover commit,
rollback, retry and namespace behavior; they do not prove a production deployment,
live contention targets, restore procedures or cloud permissions.

`Store.Update` commits the callback's changes atomically. `View` supplies a
consistent read-only transaction. Callbacks can retry: perform no provider I/O,
directory lookup, clock read or external mutation inside them. Copy results out
only after the store method succeeds. `Tx.Scan` supports bounded recovery pages;
separate pages are separate snapshots, so workers must periodically rescan and
retain idempotent processing. `Tx.List` fails instead of truncating inventory.

`NewModule(engine)` participates in the existing module lifecycle. It requires
prior policy activation, installs no routes, and leaves store ownership with the
host. Stopping a module does not erase accounting obligations. Remote discovery
does not implement remote admission/reservation/settlement RPC.

| Bound | Behavior when exceeded |
| --- | --- |
| 32 combined budget/rate/concurrency/sharing rules | Compilation fails |
| 128 distinct transaction records | Transaction fails atomically |
| 256 KiB per serialized record | Encoding fails; policy compilation checks its activation envelope |
| 4 MiB touched-original plus final staged payloads | Transaction fails atomically |
| Queue `MaxEntries` / `MaxWait` / `MaxCost` | Admission to the queue fails or expired entries are removed |
| Queue `MaxReceipts` | New queue identities fail closed; replay history is never silently evicted |
| Bounded borrower loan indexes | Further loans fail closed instead of creating unbounded admission fanout |

These limits are deliberate failure boundaries, not capacity tuning advice.
`BudgetBalance.Limit` is its last committed limit snapshot; a new policy can change
the applicable limit before the next successful admission updates that snapshot.
Use the current compiled policy with held/consumed state for authoritative planning.

## Sharing and scheduling boundaries

`GrantLoan` checks the active policy and uncommitted lender allocation, excluding
settled usage, active holds, protected floors and prior commitments. Borrowed
credit cannot be lent again. `MaxAmount` bounds outstanding loans for the sharing
rule/borrower window. Expiry fits both windows and the configured duration.
`ReturnUnusedLoan` returns only currently available credit; it cannot recall spent
or committed work. Expired credit that becomes available after late settlement
requires another maintenance pass. New admission can return
`loan_reconciliation_required` until that maintenance completes. The library
implements these operations. Embedding the library alone starts no worker; the
opt-in managed API module runs bounded loan/usage recovery passes.

`FairQueue` returns new serializable state for `Enqueue`, `Pick` and `Cancel`.
Persist that state in a quota transaction before acting on a selected request.
Selection allocates relative service only: it creates no budget hold, concurrency
permit, provider capacity or dispatch claim. Queue state is bound to tenant, pool
and policy generation. Changing generations requires explicit migration retaining
attempt deduplication; creating fresh state is not a replay-history reset mechanism.

The opt-in atomic scheduler now composes queue selection, capacity debit and core
admission in one transaction. It partitions operator-attested admission credits;
it does not establish native protected throughput, preemption or token pacing.
Managed model/tool paths have explicit coverage profiles and acknowledged usage.
Scoped administration and reviewed original-price corrections are implemented.
Additional billable dimensions, native async lookups, direct-route/remote
enforcement and live provider/billing/restore/shadow-rollout qualification remain.
Enable hard limits only for paths that pass their operation-specific gates.

See [connected mechanisms](quotaplex-connected-mechanisms.md) for the connected operational mechanisms.
