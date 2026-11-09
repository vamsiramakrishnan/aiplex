# Module foundations and bugbash

This change prepares independently usable Plex modules before implementing
QuotaPlex. It introduces no quota allocator, lending engine, or new spend ledger.

## Supported boundaries

- `pkg/modules`: immutable versioned descriptors, dependency validation,
  deterministic lifecycle, readiness, cleanup, and external HTTP module probes.
- `pkg/contracts`: trusted request context and expiring membership snapshots.
- `pkg/policy`: immutable JSON snapshots with tenant isolation, digest, revision,
  and compare-and-swap publication. MemoryStore is a conformance/development
  backend; a production backend must preserve these semantics durably.
- `pkg/enforcement`: required provider composition with admission receipts,
  reverse cleanup, idempotent completion contracts and retry-safe outcome binding.
  Providers own durable receipt recovery. The pipeline itself is in-process;
  it is not a distributed transaction or a QuotaPlex implementation.
- Module-owned route generators and narrow persistence interfaces. Go built-ins
  remain statically linked; remote services do not execute code inside Aiplex.
- Console/CLI use explicit trusted implementations plus server discovery.

`AIPLEX_MODULES` selects built-ins (default
`mcplex,a2aplex,llmplex,skillsplex,clientplex,networking`). Unknown/duplicate names fail startup.
Disabled modules cannot deploy through the shared instance endpoint or appear
in catalog discovery. Existing deployments retain cleanup inventory so they can
be removed after a module is disabled or its template disappears. Legacy
instances without inventory still need their original template for safe cleanup.

`AIPLEX_MODULE_MANIFEST` points to operator-owned JSON:

```json
{"modules":[{"descriptor":{"name":"example","version":"1.0.0","contract_version":"v1","capabilities":["example.echo"]},"endpoint":"https://example.internal"}]}
```

Remote endpoints must implement `GET /module/v1/descriptor` and `GET /readyz`.
Descriptors are pinned and compared at startup. Remote registration supplies
lifecycle/discovery; arbitrary API proxying, remote JavaScript and automatic
binding of claimed capabilities are deliberately absent. A required enforcement
provider must be explicitly wired through `pkg/enforcement`; a descriptor alone
never proves enforcement. Hosts can supply mTLS/audience-token transports to the
public remote adapter; the startup JSON loader supports TLS server authentication
only and needs an explicit adapter for authenticated probes.

## Identity deployment migration

The API now requires verified identity on user APIs. Set `JWT_ISSUER`,
`JWT_AUDIENCE`, and `JWT_JWKS_URL` for a single source. Signing algorithm, signature,
issuer, audience, subject and token lifetime are verified against pinned JWKS.
For mixed workforce/workload sources use `AIPLEX_IDENTITY_PROVIDERS_FILE`:

```json
{"sources":[{"issuer":"https://human-idp.example","audience":"aiplex-api","jwks_url":"https://human-idp.example/jwks","pool_id":"users","is_workforce":true},{"issuer":"https://workload-idp.example","audience":"aiplex-api","jwks_url":"https://workload-idp.example/jwks","pool_id":"agents","is_workforce":false}]}
```

The JWT verifier supports RS256 and ES256 assertions, including the signature
format needed by IAP. It does not turn opaque Google OAuth/STS tokens into JWTs.
Those credentials require an appropriate `auth.TokenVerifier` integration.
Group claims must come from the configured trusted assertion or a verified
identity adapter; groups are not assumed to be present in every Google token.
Actual Google STS, IAP, enterprise IdP and managed gateway deployments require
live integration validation with the target organization's configuration.

Canonical principal IDs bind issuer, pool and immutable subject. Email is display
metadata. Role bindings can be scoped by Provider and PoolID; multi-source mode
requires both. Workload actor claims are retained as separate canonical actors.
Manual scopes are stored separately from current group-derived grants. Existing
records which already mixed these sources cannot be safely auto-migrated:
operators must review them and provision canonical-ID grants. Verified consent
requires Hydra to use the canonical principal or an explicit identity-link
adapter; email-based subject matching is not performed.

A fresh deployment can bootstrap administrator group bindings through the
operator-owned `AIPLEX_ROLE_BINDINGS_FILE`:

```json
{"role_bindings":[{"id":"platform-admins","group":"immutable-idp-group-id","provider":"https://human-idp.example","pool_id":"users","role":"admin"}]}
```

The file is validated before writes; supplied IDs are operator-managed upserts.
Removing an entry from the file does not delete a persisted binding: revoke it
through the protected IAM API. Configuration is not a transactional migration.
The administration APIs require verified roles; usage and denial reporting are
restricted to administrators until a narrower service-writer policy is added.
Configure `AUTH_HOOK_TOKEN` on the API and corresponding bearer authentication
on Hydra's token-hook caller. Human consent endpoints require authenticated
caller context. Clients must request tokens for the configured API audience.

For local development only, set `AIPLEX_DEV_MODE=true` and `HOST=127.0.0.1`
(or another literal loopback bind). The process refuses a public development
bind. Missing production identity configuration fails startup. Existing Helm
installations need identity settings under `api.env`; development mode is not a
production gateway bypass.

The Rust authz service has its own pinned JWKS configuration and explicit local
mode. Its native HTTP adapter and custom JSON adapter are documented in
`authz/README.md`. Existing OPA gRPC deployment wiring is retained; this change
does not claim the HTTP adapter is a drop-in gRPC service replacement.

## Bugs exercised and repaired

- Stale group-derived scopes survived membership removal.
- Email identity keys and cross-provider bare group names could collide.
- Unsigned JWT payload extraction and missing identity permitted role bypass.
- Partial user consent was ignored; Hydra errors could appear successful.
- Rust JWT production validation used an empty key; implicit dev mode bypassed
  signature and expiry validation. Discovery-shaped payloads could bypass the
  authorization of another plane.
- Disabled modules were hidden in navigation but reachable through shared APIs.
- Negative/overflow catalog pagination could panic.
- Undeploy depended on a still-present template and deleted shared Tape runtime
  resources belonging to sibling instances.
- Cleanup failures could be reported as successful termination.
- Generated YAML scalars could inject resources or configuration; resource
  payload identity was not checked against declared deployment inventory.
- Console module pages omitted bearer credentials; native EventSource could
  not carry the API bearer token. SSE now uses authenticated streaming fetch.
- Admission cleanup ignored cancellation bounds and settlement retries could
  change usage after partial completion. Snapshot publication needed CAS and
  defensive copies to prevent mutation.

## Validation boundaries

The connected-module bugbash additionally repaired credential-origin confusion,
cross-principal stateful model/agent sessions, audit middleware buffering SSE,
resource revision reuse after deletion, and deployment-plan provenance and expiry
binding. No-op, dry-run and unknown deployment adapters now retain template
capabilities without probing cluster services. Live discovery requires explicit
adapter opt-in.

Fresh-checkout checks also cover the tracked Rust test signing fixture and Console
embedding. Source CLI builds show build instructions until the Console is built;
release packaging compiles and embeds the full Console first. CI and releases
select the Go toolchain from `go.mod`.

Connected-module validation: 416 Go tests pass with the race detector; seven Firestore
integration tests require an emulator and are skipped. Go vet/build, 24 Rust tests
and Clippy, 31 Console tests/build, public schemas, and a real Anvil CLI dry-run
and bundle-hash compatibility check pass. No cloud resources were applied.

The subsequent [native governance extension](native-agent-governance.md) adds
Google Registry/IAM/Auth Manager, AWS AgentCore Identity and Entra Agent ID
adapters, plus a shared reviewed-plan controller. Its independent bugbash covers
credential payloads entering persistence, wrong native authority, broad IAM grant
replacement, stale actor/provider revisions, ambiguous writes, receipt deletion,
duplicate execution, and Entra subject/actor attribution. Operator-declared
identity links, native APIs without atomic preconditions, and runtime-provided
credential acquisition retain their documented boundaries.

Native governance final validation: 577 Go tests/subtests pass under the race
detector; seven emulator-dependent Firestore tests skip. Go vet/build, public
schemas, 24 Rust tests/Clippy, and 31 Console tests/build pass. Protocol tests use
fake native servers, actual request signing, and signed JWT/mTLS fixtures.

Automated regression coverage includes fake JWKS servers and actual signed
RS256/ES256 tokens; module startup/readiness failures; external module descriptor
mismatch; cancellation; concurrent registration and policy publication; group
revocation; disabled deployment; cleanup ownership; and Console/CLI discovery.
Cloud deployment, IAM grants, provider billing and traffic interception are not
validated by these local tests. Legacy LLMPlex budgets remain reporting-only
advisories; they are not an admission-based hard spending limit. QuotaPlex will replace
that path with reservations and durable settlement in a later change.
The [QuotaPlex readiness checklist](quotaplex-prerequisites.md) records the exact
remaining contracts and host integration gates; the foundation test counts do
not establish production quota enforcement.

See [feature cohesion repairs](feature-cohesion-bugbash.md) for the subsequent
OAuth, routing, storage, audit, SDK/CLI and Console bugbash, with deployment
compatibility and remaining integration limits.
