# Independent AIPlex modules

AIPlex composes modules through `pkg/modules`. This package uses only the Go
standard library and can be imported without the AIPlex API server, Firestore,
Kubernetes, Ory, or another Plex implementation. `pkg/contracts` holds the shared
request-context and membership-snapshot wire types.

`pkg/modules` itself implements no allocation, reservations, pricing, borrowing
or accounting. QuotaPlex is a module that uses the same lifecycle and discovery
contracts as any other independently deployed service.

See [QuotaPlex requirements and limits](quotaplex-prerequisites.md) for the
contracts and what hard enforcement requires.

## Registration and compatibility

A module implements `Descriptor`, `Start`, `Ready`, and `Stop`. Register trusted
implementations in a `Registry`, then create a `Runtime`. Descriptor metadata is
copied during registration and discovery, so callers cannot change a published
module contract by mutating a slice.

```go
registry := modules.NewRegistry()
if err := registry.Register(identityModule); err != nil {
    return err
}
if err := registry.Register(modelModule); err != nil {
    return err
}
runtime := modules.NewRuntime(registry)
if err := runtime.Start(ctx); err != nil {
    return err
}
defer runtime.Stop(shutdownContext)
```

Every enabled module is required. Disable optional modules by omitting their
registration. Dependencies name the specific implementation to use; capability
names do not silently choose a provider. An optional dependency can be absent,
but a present optional dependency must satisfy its compatibility requirement.

The current module contract is `v1`. Module versions and dependency minimums use
release versions `major.minor.patch`. A minimum additionally requires the same
major version; `1.2.0` does not accept a `2.0.0` provider. Prerelease and build
suffixes are intentionally rejected. Contract-version compatibility is separate
from a module's release version.

`Registry.Plan` validates missing dependencies, compatibility, and cycles without
starting services. Dependency ordering is deterministic. Registration rejects
duplicate names, overlapping API prefixes, and conflicting navigation paths or
CLI commands. UI and CLI contributions are metadata for trusted host-owned
implementations; descriptors never contain executable JavaScript, shell
commands, or downloadable binaries.

An optional `config_schema` JSON Schema object describes module-owned
configuration. The module validates its configuration values; registry
validation only checks that this metadata is a JSON object. Module configuration
credentials are resolved by the host and must not appear in the descriptor.

## Lifecycle and readiness

Runtime startup freezes registration, starts dependencies before consumers, and
checks readiness before starting each dependent. Startup failure rolls back in
reverse order, including the partially started failing module. Rollback receives
a fresh five-second cleanup context even when startup was canceled. Cleanup
errors remain visible and a failed stop can be retried with `Stop`; incomplete
cleanup blocks restart.

Readiness evaluates every enabled module. A dependency losing readiness makes
the runtime unready rather than silently bypassing enforcement. `Stop` cleans
started modules in reverse dependency order and joins cleanup errors. A failed
consumer retains its transitive dependencies for a later cleanup attempt;
unrelated modules still stop. Successful cleanup is not repeated on retry.

Lifecycle methods are serialized. Implementations must honor their context and
make `Stop` safe after partial startup. They must not call lifecycle methods on
the same runtime from inside a lifecycle callback.

## Out-of-process services

`NewRemote` adapts a separately deployed HTTP service to the same lifecycle:

| Request | Response |
| --- | --- |
| `GET /module/v1/descriptor` | HTTP 200 and exactly one descriptor JSON object |
| `GET /readyz` | HTTP 200 while the service can fulfill its configured contract |

The descriptor is pinned in administrator-owned host configuration. Startup
rejects remote metadata that differs from the configured descriptor, including
capabilities, dependencies, or contributions. Optional empty collections may be
omitted or encoded as empty arrays. Array order is part of the pinned descriptor.

```go
remote, err := modules.NewRemote(modules.RemoteConfig{
    Descriptor: modules.Descriptor{
        Name: "external-meter", Version: "1.0.0", ContractVersion: "v1",
        Capabilities: []string{"usage.meter"},
    },
    Endpoint: "https://meter.internal",
    Transport: authenticatedTransport,
})
```

Endpoints are trusted operator configuration, never request data or advertised
remote URLs. Only an HTTPS origin is allowed. HTTP requires the explicit
`AllowInsecureLocalhost` development option and a literal loopback address or
`localhost`. Redirects are rejected. Probes have a two-second default timeout,
cannot exceed thirty seconds, and descriptor responses cannot exceed one MiB.
The transport owns authentication such as mTLS or audience-bound tokens; no
credentials appear in discovery metadata. Egress authorization and DNS policy
remain deployment concerns.

Use a remote adapter through `Runtime` so startup, readiness, and teardown are
serialized. `Start` connects to and verifies an already deployed service; `Stop`
disconnects locally. The host does not shut down a remote service shared by
other Aiplex hosts. Service-specific execution APIs remain versioned contracts
owned by that service.

`examples/modules/echo` is a runnable standalone service with a pinned host
manifest and an HTTP contract smoke test. Its source uses only the public
module package and the Go standard library.

## Request identity and policy boundaries

`contracts.RequestContext` carries a tenant, request, principal, optional actor,
resource, and optional membership snapshot reference. These are wire types, not
authentication. A receiving module must authenticate the gateway and bind the
context to the request before using it. A module must not accept a user's JSON
identity claims as trusted facts merely because `Validate` succeeds.

`MembershipSnapshot.ValidateFor` checks the context version, tenant/principal
binding, exact snapshot reference, validity interval, and duplicate group IDs.
Namespaces and sources should form stable group identifiers so independently
managed identity providers cannot collide. Preserve snapshots used in decisions
for audit; do not store group-derived permissions as permanent manual grants.

Modules own their persistence schemas and migrations. Cross-module reads use
published interfaces. Independent deployments may share infrastructure, but a
module's tables and transaction semantics remain private implementation details.

Language-neutral JSON schemas for descriptors, request contexts, and membership
snapshots are published in `schemas/v1/`; `schemas/README.md` describes structural
and semantic validation responsibilities.

## Verification

```bash
go test -race ./pkg/modules ./pkg/contracts
```

Coverage includes invalid dependency graphs, compatibility failures, mutation of
published metadata, partial startup rollback, canceled startup cleanup, retrying
failed cleanup, lost dependency readiness, remote endpoint trust boundaries,
redirects, descriptor mismatch/oversize, probe deadlines, and membership expiry.
