# A2A and Skills discovery and reported history

A2APlex and SkillsPlex discovery describe deployed instance gateways. Their audit POSTs record **reported history**; they do not execute a task/skill, authenticate the reported subject, or prove a policy decision. Native cloud-agent invocation remains the separate tenant-scoped agent-connection API.

| Coordinate | Meaning |
| --- | --- |
| Registered `Agent.ClientID` | OAuth client identity in the agent directory |
| `Instance.ID` / `Instance.TemplateID` | Deployment instance / catalog template |
| `Instance.Owner` | Deploying owner, not an inferred agent identity |
| Cloud connection `AgentID` | Native upstream provider selector, not an OAuth-client alias |
| Audit `caller_agent_id`, `callee_agent_id`, `agent_id`, `user_id` | Reported historical subjects |
| Audit `reported_by`, `reported_at` | Server-attributed verified collector and receipt time |

These coordinates are not interchangeable. Historical records may refer to removed instances or retired/unregistered subjects; current lifecycle state is not retroactively applied as live authorization. The server overwrites submitted reporter fields using verified request context. In development without verified identity, `reported_by` remains absent. Parent-chain integrity and record schema validation still apply.

## Reported delegation lifecycle

`POST /api/v1/a2a/delegations` allocates a unique ID if omitted. Caller, callee and task are required. Status defaults to `pending`; valid states are `pending`, `running`, `completed`, `failed`. Completion timing must agree with a nonnegative duration and follow the start. Historical timestamps are accepted; timestamps more than five minutes in the future are rejected.

`PATCH /api/v1/a2a/delegations/{id}` requires an explicit valid status. Pending/running records may progress to a terminal state. Running cannot revert to pending. Completed/failed records cannot be completed again, reversed, or overwritten with another result. In-memory updates check under the store lock; Firestore uses a read/write transaction. Duplicate creation returns 409 rather than overwriting audit history. Lists and gets return the same current state through independent snapshots.

A declared parent must already exist. Child caller identity must match the parent's callee; explicit instance/user coordinates must agree. Omitted child user/caller-instance coordinates inherit from the parent. Trace and parent span must match. Parent loops, missing parents and malformed/non-hex/all-zero W3C trace IDs are rejected. These are historical consistency checks, not authentication of the reported actors.

`GET /api/v1/a2a/delegations/{id}/chain` returns every descendant flattened in parent-before-child order. `depth` is the longest root-to-leaf path, including the root. `total_duration_ms` sums recorded span durations, including nested work; it is not elapsed wall time. Bundled stores query the trace before traversing the selected subtree, excluding unrelated history. Traversal is bounded to 10,000 spans and depth 100 and rejects cycles; a missing optional trace-read capability uses a bounded history scan and reports an explicit limit error rather than silently returning a partial graph.

## Skills history and discovery

`POST /api/v1/skills/invocations` requires a skill and reported agent, allocates an ID when omitted, and validates `success`/`failed`, timing and trace fields. Duplicate IDs return 409. A reported `missing scope:` failure produces a dashboard denial with reason `reported_scope_missing`, distinguishable from the actual authorization path's decision.

Both audit list endpoints accept `limit=1..1000`; filtering occurs before the limit. Firestore queries use the PascalCase fields actually persisted by the Go SDK's typed model writes, preserving the existing schema. The associated composite indexes are in `deploy/terraform/firestore.tf`.

Agent cards and skill manifests are served only for running instances. Discovered concrete task/skill scopes on the instance are authoritative; matching template entries supply descriptive metadata. Removed template capabilities are not re-advertised, and newly discovered skills appear even without template metadata. A nil scope list uses the legacy template fallback; an explicitly empty list does not. Discovery summaries and detail manifests use the same capability selection.

Agent-card authentication is supplied explicitly by the host through `WithAgentCardAuthSchemes`. Configuration is frozen when the option is created. Absent configuration means unspecified; it does not mean anonymous. The handler does not fabricate an issuer or infer working SPIFFE authentication from stored labels.

The mounted chi HTTP tests cover completion/list consistency, historical subjects vs verified collector, duplicate/final-state rejection, nested context, cycles, discovery drift and configured authentication. Store race tests cover concurrent completion and snapshot isolation. A local gRPC fixture exercises actual Firestore SDK document encoding and query filters/order/limits without cloud access.
