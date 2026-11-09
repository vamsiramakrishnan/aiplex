# Authoritative membership

Quota identity proves who made a request. A separate configured directory can
prove that subject's complete group view, including workloads whose native
credentials omit group claims. The directory is optional; without one, the
existing strict verified-claims membership path remains in use.

`pkg/membership.Authority` is independent of quotas and cloud identity vendors.
Its `Query` binds tenant, canonical subject ID, kind, verified issuer/pool/native
subject and the `principal` or `actor` role. Each response supplies an immutable
observation reference, explicit complete/expanded group view, source-qualified
group IDs and its actual observation/expiry times. Missing groups are invalid;
`[]` is a complete empty view. Principal and delegated actor use separate queries
and cache keys. Human groups never become an actor's groups.

`auth.DirectoryMembershipResolver` validates the original verified credential's
identity and lifetime before lookup, independently of group-claim availability.
It rejects mutated actor claims and clamps directory evidence to credential and
verified request-bound certificate expiry. Configuring a directory makes it the
membership authority: outage, partial data, invalid proof and stale evidence fail
closed rather than falling back to older signed group claims.

## Signed HTTP adapter

`membership.NewHTTP` accepts an operator-pinned HTTPS endpoint, exact authority,
allowed group sources, Ed25519 public keys and maximum observation age. Startup
must bind its destination to an approved host credential/network profile. The
caller acquires a runtime credential for the verified tenant; request headers
cannot choose the endpoint, credential or signing key.

The adapter sends a JSON `Query` with bearer authentication through the existing
endpoint-policy HTTP client. Redirects, non-JSON/error responses, unknown or
duplicate fields, oversized/deep responses, untrusted keys, wrong query proofs
and incomplete/nonexpanded observations are rejected. Response reads and HTTP
lifetime are bounded. Raw directory error bodies and credentials are not exposed.

The authority returns `membership.SignedSnapshot`:

```json
{
  "snapshot": {
    "version": "v1",
    "query": {
      "tenant_id": "example",
      "subject_id": "canonical-subject",
      "kind": "agent",
      "issuer": "https://issuer.example",
      "pool_id": "workloads",
      "native_subject": "native-agent-subject",
      "role": "principal"
    },
    "authority": "directory-production",
    "ref": {"id": "membership-view", "version": "42"},
    "groups": [{"source": "directory-production", "id": "research"}],
    "complete": true,
    "expanded": true,
    "issued_at": "2026-11-09T00:00:00Z",
    "expires_at": "2026-11-09T00:05:00Z"
  },
  "query_digest": "<SHA-256 of JSON-encoded Query>",
  "key_id": "directory-signing-key-1",
  "signature": "<unpadded-base64url Ed25519 signature>"
}
```

Use `membership.SigningBytes` to produce the canonical typed signing payload:
`{"version":"v1","query_digest":...,"snapshot":...}`. `membership.Digest`
computes SHA-256 over the typed JSON representation. Fields use the declared
struct order and ordinary Go `encoding/json` string escaping; JSON objects are
not interchangeable raw signing representations. Consumers verify the signature
against pinned public keys and then validate the exact query, authority, source
allowlist, observation lifetime and complete view. A signature alone does not
establish the correctness of an authority's upstream directory synchronization.
Private keys remain in the directory service, never Aiplex configuration.

This is an Aiplex directory-adapter contract. It does not pretend a Google,
Microsoft or AWS directory natively exposes this endpoint. An organization can
implement the authority using those native APIs or inject another typed Authority.
`membership.Expand` provides bounded transitive closure for an authoritative group
parent graph; cycles, excessive depth and group-count overflow fail rather than
producing a partial membership. It does not discover a directory or expand groups
from unrelated sources implicitly.

## Freshness and revocation

The optional `membership.Cache` has bounded capacity, per-query read coalescing
and a maximum five-minute TTL. It never rewrites `issued_at`, extends evidence
expiry, caches directory errors or serves expired data on lookup failure.
Snapshots are returned as owned copies. Tenant, role and immutable identity facts
are part of each key.

Trusted directory event consumers can call `Invalidate(query, notBefore)` to
require an observation at or after a revocation boundary. Invalidation fences
in-flight reads so an old result cannot repopulate the cache. Watermarks are
bounded and retained; reaching capacity fails the invalidation instead of silently
forgetting an earlier revocation. This process-local hook is not a distributed
event subscription. Deploy equivalent authority/freshness policy on all replicas;
no native directory watcher is started automatically.

The quota host pins the authority manifest in its durable configuration epoch.
Changing trust keys, endpoint, allowed sources or cache policy requires an
explicit policy revision. Membership is resolved again before dispatch. A new
complete observation may confirm the same exact subject/group eligibility;
new reference/timestamps alone do not force a false conflict. The admission's
original evidence and expiry remain unchanged. Changed groups, identity, expired
original evidence or unavailable current proof cannot obtain a dispatch claim.

Tests cover a real TLS/signed HTTP fixture, cross-tenant/role substitutions,
missing and partial group evidence, independent actor queries, credential expiry,
cache ownership/concurrency, revocation racing a lookup, directory outages and
actual host admission/claim behavior. Live native directory permissions,
synchronization correctness and revocation delivery require deployment validation.
