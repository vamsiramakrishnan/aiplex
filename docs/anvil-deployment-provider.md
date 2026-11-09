# Anvil deployment provider

Anvil is an optional provider for infrastructure actions that Aiplex does not
model natively. It is not a second identity authority, an automatic IaC compiler,
or a replacement for the existing native networking plans.

`pkg/deployment` defines the provider contract. Its Anvil adapter can:

1. Prepare an operation-level dry-run through an approved Anvil connector.
2. Inspect Anvil's existing Cloud Run or Kubernetes connector deployment plans.
3. Execute a previously reviewed infrastructure operation only when a host
   explicitly supplies verified approval and durable journal adapters.

The HTTP host exposes planning plus explicitly operator-enabled reviewed execution.
No cloud resource is created by enabling the provider or registering a connection.

## Objects and references

| Object | Meaning |
|---|---|
| Bundle | Tenant-owned directory with a reviewed canonical Anvil bundle digest |
| Binding | Exact tenant/resource → bundle/operation/CLI handle mapping |
| Request | Verified caller context, binding ID, inputs, stable idempotency key |
| Plan | Expiring action digest bound to caller, bundle, runtime and input bytes |
| Approval | Host-verified receipt bound to that exact action and resource |
| Receipt | Journaled invocation state; readiness remains independently unverified |

A networking intent or connection refers to a deployment provider binding.
Clients select that reference; they never supply a filesystem path, executable,
command line or arbitrary operation to run. The provider's safe binding discovery
omits bundle directories, environment values and credentials.

For a missing native infrastructure construct, first compile its actual API
contract in Anvil and review the operation's inputs, effects, confirmation,
idempotency and recovery semantics. Then pin that generated bundle and bind the
specific operation. A generated MCP server, SDK or CLI remains another surface
of the same reviewed contract. The bridge does not grant Anvil approvals or
manufacture release certification.

## Actual Anvil contract

The implementation was researched against Anvil main
[`c2d31108`](https://github.com/vamsiramakrishnan/anvil/tree/c2d31108e2b02d8a543a7c1b01e8246f546bd6c9).
These are documented commands, not guessed deployment APIs:

```text
anvil --version --json
anvil run <bundle> <resource> <action> --input <private-file> --json \
  --mcp direct --no-retries --confirm --idempotency-key <scoped-key> --dry-run
anvil deploy cloud-run <bundle> --env <profile>
anvil deploy kubernetes <bundle>
```

`anvil deploy` and `anvil publish` prepare plans. Neither applies Terraform,
Kubernetes resources, or cloud APIs. Their output is always marked plan-only,
and the bridge rejects it if passed to execution. A publication record is not
proof of a deployed or healthy service.

Anvil requires confirmation flags even to preview some operations. The dry-run
flag prevents upstream execution; the `--confirm` flag used to construct that
preview is not an Aiplex execution approval. Actual execution requires a separate
host-verified approval receipt. A caller-supplied boolean is insufficient.

## Host configuration

The host constructs `deployment.Config` with:

- An absolute runtime executable and its SHA-256. Node-based installations also
  pin the absolute Anvil entry script in `PrefixArgs` / `PrefixSHA256`.
- The expected `anvil --version --json` build digest. A version string alone is
  insufficient because different Anvil builds can share the same version.
- Tenant-owned bundle IDs, absolute directories and reviewed canonical digests.
- Bindings with exact operation IDs, generated command handles and Anvil scopes.
- A minimal, explicitly selected subprocess environment. Credentials remain
  operator-owned references or runtime credential sources, never request inputs.
- An authorizer and explicit admission provider. The shared Aiplex enforcement
  pipeline can be reused through `deployment.PipelineAdmission`.

Execution defaults to disabled. Enabling it also requires `ApprovalVerifier` and
`Journal`. `deployment.ResourceJournal` uses the shared tenant-scoped CAS store;
use a durable backend in production. The memory store is for local tests.

No shell is invoked by the bridge. Inputs travel through a private file, not
argv. The child receives a principal directory synthesized from verified host
identity and the binding's exact reviewed Anvil scopes. Wildcard grants are
rejected. Upstream idempotency keys include tenant, principal, resource and
binding, preventing accidental cross-tenant key reuse.

## Integrity, secrets and process boundaries

Each operation validates the configured runtime hashes and its reported Anvil
build identity. The adapter rejects bundle symlinks/special files, reads within
an `os.Root`, applies file/byte bounds, and copies a digest-verified private
snapshot before running. Its canonical digest follows Anvil's pinned bundle hash
algorithm, including the explicit evidence-record exclusions. A second digest
also binds the complete snapshot, including evidence.

The snapshot and input file are removed after the call. The child gets private
HOME/TMPDIR, an allowlisted environment and no ambient API-process credentials.
Output, runtime and cancellation are bounded; Unix cancellation kills the process
group. This is a process boundary, not a sandbox. Install Anvil, its dependencies
and the configured runtime read-only and grant the workload only its intended
upstream authority. The Go `endpointpolicy` HTTP client does not sandbox a
subprocess: Anvil operation egress must be constrained by its reviewed host pins
and the workload network controls. Registering the provider does not intercept
Anvil internals or create those network controls automatically.

Public previews retain operation/method, target origin and structural metadata.
Header values, URL path/query, payload bytes and raw stderr are omitted. Exact
inputs remain bound by digest. Error messages expose stable Anvil error codes,
not arbitrary upstream or subprocess output.

## Execution and recovery

The optional library execution path:

1. Validates plan identity, expiry, digests, inputs and current authorization.
2. Verifies the host's approval receipt.
3. Rehashes the bundle/runtime and checks the approved snapshot again.
4. Obtains admission and atomically journals `pending` before invocation.
5. Executes once and persists the terminal receipt.

A repeated key returns the prior receipt. A key reused for a different action
is a conflict. Timeout, disconnect, output overflow or ambiguous command failure
is `unknown`; the provider does not automatically retry or claim the action did
not happen. Reconciliation must examine the upstream and journal first.

Even `invocation_succeeded` means only that the approved operation returned a
successful JSON result. It does not establish cloud readiness. Readiness needs
an independently verified observation against the actual deployed resource.

## Evidence

Package regression tests exercise typed argv, private inputs, admission and
approval gates, digest changes, tenant/actor mismatches, safe previews, durable
CAS replay, unknown outcomes, process cancellation and output bounds.

A local smoke test also compiled the synthetic Anvil payments fixture and
prepared a refund dry-run through the real Node/Anvil CLI. It required no upstream
credentials and made no upstream call. That installed build reported commit
`717527f95a992fa8feedbbca7b1faef7708f51a9` and content digest
`sha256:50baec0c959ad3fcec588670ddcbe8ef581c13509725843cd4ea5d9cd7c9ecd8`.
The same fixture also passed Anvil static assurance, and its canonical
`bundleHash` matched the bridge exactly (`82f5a3e4…42f88b`). This verifies the
runtime contract, not a live deployment or qualification of a
newer Anvil build. No Anvil repository was modified.

Sources:

- [Anvil CLI run](https://github.com/vamsiramakrishnan/anvil/blob/c2d31108e2b02d8a543a7c1b01e8246f546bd6c9/packages/cli/src/commands/run.ts)
- [Generated CLI grammar](https://github.com/vamsiramakrishnan/anvil/blob/c2d31108e2b02d8a543a7c1b01e8246f546bd6c9/packages/cli/src/tool-cli.ts)
- [Plan-only deployment commands](https://github.com/vamsiramakrishnan/anvil/blob/c2d31108e2b02d8a543a7c1b01e8246f546bd6c9/packages/cli/src/commands/deploy.ts)
- [Anvil build identity](https://github.com/vamsiramakrishnan/anvil/blob/c2d31108e2b02d8a543a7c1b01e8246f546bd6c9/packages/cli/src/build-identity.ts)
- [Canonical bundle identity](https://github.com/vamsiramakrishnan/anvil/blob/c2d31108e2b02d8a543a7c1b01e8246f546bd6c9/packages/generators/src/certify.ts)

## Managed HTTP apply and observations

When `config.allow_execute` is true, production requires durable resource storage.
The API stores the saved plan and intent/network revisions. An authorized caller
creates an approval with `POST /api/v1/network/plans/{digest}/approve`, supplying
that exact `digest` and a bounded `reason`. This requires
`aiplex:deployment:approve:<resource>`. Approval records bind the original executor,
actor, plan revision and expiry. An approval is not a request-body boolean.

The original principal and actor execute with `POST .../{digest}/execute`, passing
`digest` and `approval_id`, under a current
`aiplex:deployment:execute:<resource>` grant. The host rechecks approval grants and
saved source revisions, and the provider rechecks bundle/runtime identity. It
journals once before native execution. Repeat requests inspect the original
receipt; unknown operations are never automatically reissued. `GET .../receipt`
returns the invocation outcome, with readiness still unverified.

With QuotaPlex enabled, Anvil commands require explicit request-count bindings on
plane `mcplex`, provider `anvil`, the intent resource ID and operation
`tools/call:anvil.<phase>:<native-operation-id>`, where phase is `plan`, `execute`
or `observe`. Each command counts independently. Quota claims happen after local
preparation immediately before the operation can start. This meters the managed
command, not every internal action or dollar billed by its upstream system.

Optional provider `readiness` entries configure a read-only HTTP observation:
`tenant_id`, `binding_id`, `identity_ref`, `url`, `target_ref`, `resource_field`,
`ready_field`, `ready_value` and bounded `ttl`. The credential destination and
endpoint must pass existing policy. `POST .../{digest}/observe` requires a stable
`Idempotency-Key` and `aiplex:deployment:observe:<resource>`. It fetches native JSON,
requires the exact configured target, evaluates the configured state, and stores
a short-lived observation with response/configuration/receipt digests. Caller
assertions cannot mark a resource ready.

This observation establishes configured native health criteria. It does not prove
that every desired input was applied or that private DNS/routes are correct; a
pre-existing healthy target does not prove the write happened. It never changes
an unknown invocation into a successful write. Production must qualify the actual
probe semantics and native desired-state convergence. Approval grant refresh uses
the registry's current grants with the saved verified approver identity; it does
not silently obtain a new IdP sign-in or directory observation for that approver.

SDK methods and `aiplex infra approve|execute|receipt|observe` expose the same
workflow. No apply was performed against a live cloud during implementation.

`GET .../{digest}/observation` with the original `Idempotency-Key` recovers a saved observation after response loss. Repeating the completed observation key returns that observation rather than probing again. Check its expiry before using it as current health. Fund recovery/observation capacity independently when production permits can remain held by unknown work.
