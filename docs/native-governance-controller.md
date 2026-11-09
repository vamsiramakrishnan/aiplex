# Native governance controller

`pkg/governance` is a reusable control-plane module. A host supplies a
tenant-scoped `resources.Store`, configured `nativeapi.Driver` instances,
authorization and approval verification. Provider adapters remain separate Go
packages. Neither the controller nor a provider adapter allocates model quota.

## Identity and registration

An `AgentBinding` names an Aiplex tenant, a host-configured driver and an agent's
native `PrincipalRef`. A principal reference contains a provider, native identity
authority tenant/account/directory, immutable identifier and kind. An email
alias is rejected as an immutable identifier. The binding's application tenant
and the principal's native tenant are separate coordinates.

The agent, optional delegated user and optional sponsor have separate reference
fields. A sponsor does not contribute group memberships or confer permissions.
The authenticated caller's authorization remains a host decision against the
trusted request context. `RegistryRef` also keeps the provider's native resource
name, discovery URN and runtime reference separate. Import or discovery never
silently substitutes one for another.

These references describe an intended relationship. The generic controller does
not prove that a registry entry belongs to the named native agent or that the
configured credential represents that principal. Effective authority comes from
the tenant-bound driver configuration, exact operation/resource grants and
provider-specific principal/role allowlists. Verifying a native relationship
requires an authorized provider observation and provider-specific assessment.

A binding has exact `(operation, resource)` grants and a bounded list of saved
desired operations. There are no wildcard grants or caller-selected HTTP
endpoints. Provider-specific pure validation constrains each operation's native
resource namespace and schema. The host calls `ValidateBinding` before storage;
the controller repeats validation when loading it. Public payload validation
rejects credential material, normalized duplicate keys, excessive complexity and
invalid JSON before it can enter a durable plan. Credential references remain
possible; credential values do not.

Every configured driver implements both `nativeapi.Driver` and the pure,
network-free `nativeapi.Validator`. Registration freezes its operation metadata,
application tenant and `ProviderRevision`, a SHA-256 fingerprint calculated by
the host from the effective provider configuration, credential reference and
endpoint origin. Changing a credential value in place requires the host to
advance this fingerprint, for example by including its secret version reference.

## Read, plan and apply

`Read` accepts only a granted nonmutating, noncredential operation. It authorizes
the caller before invoking the native driver and requires an exact saved binding
revision. The default observation stores status, response digest, observation
token and identity/configuration provenance; it omits the response body. A host
can explicitly supply an `InventoryProjection`. Its output is validated again
before persistence. `RedactInventory` provides an optional generic redactor;
provider-specific allowlisted projections can impose tighter output contracts.
Read errors and provider response bodies are not copied into public error text.

`Plan` selects a desired operation already saved in the binding. It does not
accept an operation or payload override. The public plan binds:

| Coordinate | Binding |
| --- | --- |
| Identity | Application tenant, authenticated principal and actor |
| Resource | Binding ID, monotonic saved revision and complete specification digest |
| Provider | Configured driver ID and frozen provider configuration fingerprint |
| Action | Desired operation ID, native operation/resource, payload digest and observation token |
| Approval lifetime | Creation time and bounded expiry, at most the host-configured TTL |

The persisted private envelope additionally seals the exact payload bytes; they
are not returned by public plan responses. Approval verification receives the
exact public plan digest and saved plan revision. A host must bind its approval
to those coordinates and to the authenticated caller. A caller-supplied boolean
is not approval. Global and per-driver apply flags both default to disabled.

`Apply` validates the saved envelope, current binding revision, provider
fingerprint, caller, approval and expiry. It durably claims a `pending` receipt
with `resources.Store` compare-and-swap before invoking the native driver. It
rechecks binding revision, authorization, pure operation validation, observation
freshness and cancellation immediately before invocation. Independent concurrent
apply requests can observe the existing receipt; they cannot execute that exact
saved action a second time.

| Receipt state | Meaning |
| --- | --- |
| `pending` | Durable claim exists; execution or final persistence may still be in progress. Never automatically repeat it. |
| `succeeded` | Driver returned an uninterrupted HTTP success. Cloud readiness remains `unverified`. |
| `failed` | Execution was refused before invocation, or the provider returned a definite client rejection. |
| `unknown` | Timeout, disconnect, panic, server failure or final receipt persistence failure leaves the native effect uncertain. |

The store must preserve monotonic revisions across deletion and recreation.
Deleting a receipt does not reenable the same action: a recreated receipt with a
revision above one is treated as an unknown prior attempt and is not executed.
Durable journal and plan records must be protected from arbitrary resource edits.
Pending or unknown outcomes require a new authorized native observation and an
operator's explicit decision about a new binding revision; the controller does
not retry, infer rollback or declare the cloud resource ready.

## Native concurrency limits

The application store's CAS protects local binding/plan/receipt history. It does
not make a cloud API transaction atomic. `DriverRegistration.ETagReads` can bind
a mutation to a recent read observation by the same actor/principal, resource,
binding identity and provider configuration. A desired operation must present
the exact observed token. Altering the binding's desired actions preserves this
proof only while its agent, delegated user, sponsor, registry and driver identity
remain unchanged; the normal saved binding revision still changes.

Some adapters use real policy ETags that the provider enforces in the mutation.
Other native APIs expose no server-side conditional mutation. An adapter may use
a canonical resource fingerprint solely as an explicit, opt-in preflight check;
its provider configuration must permit non-atomic mutations. Such fingerprints
detect changes observed before invocation but cannot close the provider's
read/write race. The controller does not manufacture a conditional HTTP header
or claim provider-side CAS for those APIs.

This phase plans and applies one native operation at a time. It does not claim
cross-provider transactions, aggregate rollback, semantic desired/observed
equality from arbitrary JSON, or complete reconciliation after an ambiguous
effect. Host callers should use observations for provider-specific assessment.
Native credential acquisition and credential-broker libraries remain outside
this generic public controller: every `Credential` operation is rejected even
when marked read-only.

## Verification

Package tests use fixture drivers and the revisioned store. They cover pure
validation, secret payloads/projections, native/application tenant separation,
authorization before reads, exact actor-bound approval, provider/binding drift,
global/per-driver gates, concurrent claims, ambiguous outcomes, journal failure,
receipt tombstones and fresh observation tokens. No live provider mutation is
required to exercise these mechanisms. Drivers must honor context cancellation;
the controller cannot forcibly terminate arbitrary host code executing in-process.
