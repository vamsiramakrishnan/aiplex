# Revisioned legacy configuration

Agent registrations, LLM routes/providers, IAM role bindings and manual grants
use atomic desired-state revisions in both bundled registry backends. This
applies to the legacy tables; it does not move their objects into another
Plex or change their gateway ownership.

## HTTP contract

Reads expose `revision` and individual resource reads include a strong `ETag`.
An update or deletion of an existing object requires that exact revision in
`If-Match`, for example `If-Match: "7"`. Missing preconditions return `428`; stale
object or dependency revisions return `412`. Weak tags, wildcards and tag lists
are rejected. A mutation without a precondition is **create-only**, equivalent
to revision zero, and cannot replace an existing object.

Creates commit only if the identity is absent. Deletion retains a revision
tombstone. Recreating a deleted identity advances the revision, so an ETag from
an earlier incarnation never authorizes a later update or deletion. Caller JSON
`revision` values do not replace the HTTP precondition.

The Go SDK carries revisions on route/provider writes and provides
`DeleteAgentAtRevision`, `DeleteLLMRouteAtRevision` and
`SetUserScopesAtRevision`. Its convenience delete/manual-grant methods read the
current representation first, then submit a conditional mutation. Console edit
and delete flows use the revision of their selected object. Declarative route
manifests accept `revision`; omitting it creates only and will not overwrite a
saved route. Reload and review the newer object after a conflict rather than
blindly replaying a stale mutation.

## Store mechanism

`registry.VersionedConfigurationStore` reads and writes native legacy objects
with a sidecar revision/tombstone and a shared configuration generation. A
Firestore mutation reads all three in one native transaction and commits them
together; the reference backend performs the equivalent work under one lock.
Changing any participating configuration increments the generation. HTTP handlers
read it before validating related routes/providers and supply it with their CAS
write. A dependency changed during validation therefore invalidates the write
before native submission, even when the target object's revision is unchanged.

`ModifyUserScopes` performs deployment owner-grant union in one transaction,
preserving concurrent manual grants instead of a read followed by blind replace.
Its callback must be deterministic and perform no external effects: native
transaction contention can invoke it more than once.

Bundled identity stores expose `ReadIdentityAccess`. WIF scope resolution reads
role bindings and manual grants from the same authoritative snapshot and reports
its configuration revision. Derived group grants remain separate from persisted
manual grants. Custom read-only identity adapters retain their compatibility
interface; they must implement this snapshot capability to provide the same
cross-read consistency guarantee.

Trusted bootstrap/reconciliation `Put*` methods remain available and advance
the same revision and generation. They intentionally replace the current object;
public handlers use the conditional interface. Direct database writes are not
part of that contract.

## Native effects and unfinished operations

Desired-state CAS is not a transaction spanning Firestore, Kubernetes and Hydra.
The existing explicit submitted/unverified status remains meaningful.

A route is saved with `activation_pending` before native apply. An unfinished
activation may be retried only with the same desired routing effect and its
current revision; a different intent cannot overtake it. Successful native
submission and cleanup clear the marker through another CAS. A failed or
unacknowledged operation retains its desired configuration and cleanup inventory.
Deletion similarly records `deletion_pending` before native deletion, preventing
recreation while owned cleanup remains unfinished. A pending activation must be
resolved before deletion, and pending routes cannot become new fallback targets.

Agent registration claims the local identity as `provisioning` before asking
Hydra to create a client. Only that claimed revision can become `active`, and
client secrets are never persisted. A concurrent registration cannot create or
roll back another caller's winning credential. Failed/ambiguous issuance or
activation acknowledgement retains the non-active claim for inspection; it does
not blindly delete or reissue a potentially successful external operation.
Deletion marks `deleting` before native revocation and retains that state if
revocation fails. Operators must inspect unresolved native effects; there is no
lease expiry that silently declares those effects absent.

These guards coordinate writers using this API. They do not turn external
Kubernetes/Hydra writers into revision-aware participants or prove runtime
readiness, private connectivity or end-to-end admission coverage.

## Upgrade and verification

Quiesce or upgrade every legacy configuration writer before relying on this
contract. Older binaries and direct database edits bypass its generation
updates. Existing native rows without revision metadata are read as revision one;
the first conditional mutation creates the sidecar transactionally. Native table
names, SDK field casing and existing query indexes remain unchanged. Protect the
new `configuration_revisions` collection with the same operator workload IAM as
the legacy configuration tables; clients must not receive direct write access.

Regression coverage includes competing replicas, stale updates/deletes,
recreation, dependency generation conflicts, atomic grant union, native SDK
transaction retries, coherent identity snapshots, conditional SDK/Console flows,
and preventing duplicate Hydra writes or destructive rollback after lost
acknowledgement. Live Firestore contention, restore and native convergence remain
production qualification steps.
