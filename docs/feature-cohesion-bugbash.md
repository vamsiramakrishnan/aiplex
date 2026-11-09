# Existing feature cohesion repairs

This pass repairs existing behavior and handoffs. Gateway ownership, native
protocol forwarding, permission resolution and the independent Plex boundaries
remain intact. It implements no QuotaPlex allocator, ledger or admission limit.

## Completed repairs

| Area | Broken or incomplete behavior | Result |
| --- | --- | --- |
| OAuth registration | CLI/server grant defaults could contradict the selected auth method; callback and issued-once-secret flows were incomplete | Grant selection, redirect requirements, SDK/CLI fields and Console registration now agree; secrets are shown once and not stored in Console state beyond that flow |
| Hydra hooks | Partial hook payload support, unchecked unknown/suspended agents and stale agent ceilings | Supported native envelopes resolve one client identity, reject conflicts, recheck current agent policy and preserve session claims with a stable actor |
| Consent | Denial lacked the subject binding used by acceptance; previews could disagree with active registration policy | Both decisions bind to the verified principal; suspended agents and unavailable policy fail consistently |
| IAM | Email-keyed UI grants differed from canonical backend identities; partial updates could drop trust restrictions | Canonical principal IDs, explicit scope clears, validated roles, preserved issuer/pool restrictions and source-qualified generated binding IDs |
| Memory persistence | Caller/read pointers could mutate saved deployments, templates and route configuration without a write | Snapshot semantics isolate nested maps, slices and pointers; catalog pagination is deterministic and rejects invalid bounds |
| Firestore persistence | Queries used JSON field names while typed SDK writes persisted Go field names | Queries and indexes match actual persisted wire fields; model/agent filters precede limits and event ordering uses numeric sequence |
| Deployment lifecycle | Apply/final persistence/grant failures could leave a successful response or destroy existing manual grants | Failure is explicit, cleanup inventory survives, and failed scope reads cannot overwrite prior permissions |
| Cleanup | Resource deletion could resolve the wrong Kubernetes API group | Recorded group/kind/namespace/name drive validated cleanup |
| LLMPlex routing | Weighted YAML nesting and fallback names could produce empty/dangling backend references | Parsed routing structure is tested; fallback references resolve the saved model's enabled backends |
| LLMPlex activation | Desired configuration and native apply/cleanup could diverge while returning success | Explicit submitted/unverified status, durable retired-backend inventory and safe cleanup retry; provider credentials/defaults and dependencies are honored |
| Backend identity | Concatenated model/provider names could alias across independent writers | New/updated configurations use tuple-hashed V2 names; existing names retain a versioned migration path |
| Usage reporting | Reporting thresholds could reject evidence of work already performed | Valid reports persist with advisory headers; invalid/overflowing quantities fail; no hard quota claim is added |
| Admission lifecycle | A partially failed abort could release one provider's reservation and then allow settlement through the others | Starting abort permanently excludes settlement; failed cleanup remains retryable without rerunning successful aborts |
| A2APlex/SkillsPlex audit | Updated delegations stayed stale in lists; records aliased memory; chain/state/trace rules were incomplete | Current status, unique records, atomic final transitions, bounded recursive chains and verified reporter attribution |
| Discovery | Template capabilities could override discovered instance capabilities; authentication claims were fabricated | Discovery reflects deployed capabilities and availability; auth schemes are explicit host configuration |
| Connected resources | Client plans could advertise disabled/unusable native surfaces or pivot after a store error | Resolve the actual enabled protocol operation, recheck prerequisites, reject ambiguous destination IDs and fail closed on storage errors |
| MCP registration | Developer client identity was conflated with issuer-scoped OAuth profile identity; references could disappear before registration | Distinct IDs can be linked; live references and result storage are checked before upstream registration, and connection revisions are rechecked before saving results |
| Native governance | A deleted binding was rechecked only for the AWS credential branch | Every linked provider checks its binding before credentials or invocation; actual native target selectors constrain the link |
| Dashboard | Store errors became plausible zero counts and repeated inventory reads could disagree | Unavailable data returns an explicit error; plane counts derive from one inventory snapshot |
| Console session and SSE | Decoded tokens were treated as verified identity; caches crossed sessions; SSE used an unmounted URL or query-token authentication | Server-verified login, per-session cache isolation, stale-response guards, header-authenticated dashboard/run SSE at the real endpoint and matching development proxies |
| Console feature flows | Native governance lacked a review journey; fake scopes/deployment choices and ignored errors obscured state | Permissions includes saved-binding read/plan/apply/receipt inspection; scope choices come from actual inventories and errors remain visible |
| SDK and CLI | DTOs, query filters, scope clears and 204 handling differed from API behavior | Public shapes and filters align, scope writes read back correctly, IDs reject path aliasing and auth defaults match the server |
| Deployment configuration | File-based identity/connection configuration had no Helm mounting path | Explicit read-only ConfigMap/Secret mounts and static CLI preflight cover configured files |
| Secret and native API errors | Secret lookup hid permission/outage failures; native non-2xx lost status needed by governance receipts | Only NotFound means absent; safe status classification distinguishes definite rejection from ambiguous effects |

## Backend naming and configuration compatibility

`LLMRouteConfig.backend_naming_version` and `retired_backends` are server-owned
metadata. An absent naming version retains the legacy rendering for existing
saved configurations. A new or updated configuration selects V2 automatically;
the model/provider tuple determines a bounded hash-based Kubernetes name. Two
different tuples no longer alias because they contain the same hyphenated text.

Migration retains prior resource names as cleanup inventory. Failed cleanup
does not lose that inventory, and a retry does not delete resources owned by
another saved route. Caller-supplied inventory/naming fields never grant deletion
authority. A referenced legacy fallback target must have its dependencies removed
before a naming migration; this prevents a saved referring route from silently
losing its target. Fallback resolution uses each target's saved naming version.

A successful native apply is a submitted configuration, not proof of healthy
upstream inference. Kubernetes secret materialization remains operator-owned.
Provider endpoint/region/project metadata and cache hints must be read with the
returned support warnings; they do not imply a feature the installed routing
adapter cannot implement. Saved fallback lists resolve direct enabled backends;
they do not recursively inherit another route's fallback policy.

Legacy route/agent/IAM configuration stores still expose blind writes. The
snapshot fixes and per-handler serialization do not create cross-process CAS or
a distributed configuration transaction. Operators must coordinate simultaneous
edits to the same legacy object. V2 naming removes cross-model tuple aliasing
without claiming to solve that separate concurrency problem.

## Usage remains reporting, not quota enforcement

Valid legacy `/llm/usage` reports return success even when reporting budgets or
rate thresholds are exceeded. `X-Budget-Warning`, `X-RateLimit-Warning` and
`X-LLM-Policy-Mode: reporting-only` identify advisory results. There is no
`Retry-After` instruction to retry and duplicate already consumed usage.

This preserves evidence rather than attempting to undo provider work. Authoritative
reservation, deduplication, pricing and settlement remain in the
[QuotaPlex prerequisites](quotaplex-prerequisites.md). Native model/agent admission
and acknowledged accounting usage handoff are still pending there.

## Audit and native identity boundaries

Historical delegation/skill subject IDs remain reported facts; `reported_by` and
`reported_at` identify the authenticated collector. A telemetry POST is not a
live invocation or proof that an unknown/retired subject was authorized. See
[A2A and Skills audit contracts](a2a-skills-audit.md).

Governance links remain operator-declared relationships. They validate the actual
configured target and current binding, not all managed-agent internal calls.
Registration or a successful control-plane response never proves gateway routing
or observed enforcement. Hydra hooks recheck current agent status/ceiling but
cannot refresh an external directory's group membership from the hook payload.
Resource authorization still uses its configured verified identity/access path.

The Console uses existing saved bindings; binding creation/editing remains in the
API/SDK. No arbitrary native operation or credential-vending form was added.
Explicit approval digests and durable receipts remain the authority for native
governance apply. Failed/unknown responses require receipt inspection.

## Deployment and verification

The legacy Firestore schema remains unchanged: typed model writes use SDK field
names, and explicitly tagged module resources retain their existing field names.
Apply the updated [Firestore index configuration](../deploy/terraform/firestore.tf)
before relying on new compound queries in a live deployment. Existing data does
not need a casing migration. Index creation/rebuild was not performed here.

[Helm identity configuration](helm-identity-config.md) documents the read-only
mounts. CLI preflight checks declared mounts and configuration shape; it does
not assert that cluster resources, credentials or provider services are ready.

Regression coverage includes mounted API flows, actual SDK Commit/RunQuery
encoding over an in-process gRPC server, native HTTP error receipts, failed
deployment persistence, two independent routing handlers, datastore snapshots,
OAuth/consent policy boundaries, frontend session races and reviewed governance.
Tests do not require live cloud writes. No cloud resources were changed.
