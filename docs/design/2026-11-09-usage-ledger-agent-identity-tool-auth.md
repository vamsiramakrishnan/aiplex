# Usage ledger, cross-cloud agent identity and tool auth types

Status: plan, in execution. Date: 2026-11-09.

Three gaps keep AIPlex from being inspectable and easy to adopt:

1. **QuotaPlex** admits and settles, but there is no way to *examine* traffic.
   `/llm/usage` is self-reported and model-only; quota attempts are readable
   only by ID. Nothing answers "what did agent X spend on model Y via client Z?"
2. **Agent identity** verifies Google agent assertions and validates WIF
   principals, but onboarding an agent that runs in AWS, Azure, GitHub Actions
   or another Kubernetes cluster requires hand-assembled trust configuration and
   gives no explanation when a token is rejected.
3. **MCPlex** tool connections support one upstream auth shape: user-delegated
   OAuth (pre-registered, CIMD, DCR). Most real tools need API keys, service
   OAuth, token exchange or cloud workload tokens, and there is no per-tool
   authorization policy on a connection.

Existing invariants are kept: fail closed, tenant-scoped resources, secrets only
as opaque references, unknown usage stays unknown (never zero), and prices are
never invented.

## 1. Usage ledger (QuotaPlex observability)

One append-only, tenant-scoped record per managed exchange, on every plane,
whether or not quota enforcement is enabled for that path.

**Record** (`pkg/usageledger`): `id`, `tenant_id`, `started_at`, `finished_at`,
`latency_ms`, `plane` (`llmplex|mcplex|a2aplex`), `provider`, `resource_id`
(surface/connection), `operation`, `model_id`, `model_version`, `tool_name`,
`principal_id`, `actor_id` (agent), `client_id` (OAuth `azp` / ClientPlex
registration), `request_id`, `trace_id`, `quota_attempt_id`, `streaming`,
`status` (`ok|error|denied|unknown`), `http_status`, `error_class`,
`source` (`managed|reported`), `requests`, `usage` (nullable counters:
`input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`,
`reasoning_tokens`, `total_tokens`), and `cost` (`currency`, fixed-point
`amount`, `scale`, `basis`: `settled|estimated|unknown`). No prompt, completion,
tool argument or credential content is stored.

**Capture points**: terminal completion of `NativeModel`, `NativeTool` and
`InvokeAgent` (including denials and aborted streams). Usage comes from the
existing normalized `pkg/execution` usage; cost comes from quota settlement when
present, else from an operator-configured price resource, else `unknown`.
`POST /api/v1/llm/usage` keeps working and also lands in the ledger with
`source=reported`, so Envoy/direct paths can be included by a trusted reporter.

**Prices**: tenant resource kind `model-price` (model/provider match, per-meter
exact rational price, currency, effective window). No built-in price list.

**API** (`/api/v1/usage`):

| Route | Purpose |
| --- | --- |
| `GET /records` | Filter by `plane, provider, model_id, tool_name, actor_id, principal_id, client_id, status, source, since, until`; cursor pagination |
| `GET /records/{id}` | One record |
| `GET /summary` | `group_by` any of `plane,provider,model_id,tool_name,actor_id,principal_id,client_id,status`; optional `bucket=hour\|day`; returns requests, errors, token sums, records with unknown usage, cost per currency |
| `GET/PUT/DELETE /prices/{id}` | Price resources |

Admins read the tenant; other callers read only records where they are the
principal or actor. Storage: memory + Firestore behind one interface, bounded
retention.

## 2. Cross-cloud agent identity (AgentPlex)

`pkg/agentfederation`: a preset catalog plus an `agent-identity` resource that
binds an external workload identity to a registered agent.

| Preset | Issuer | Subject shape |
| --- | --- | --- |
| `gcp-service-account` | `https://accounts.google.com` | numeric SA unique ID / email claim |
| `gcp-agent-identity` | Google STS agent pool (existing verifier) | SPIFFE |
| `aws-sts-web-identity` | account-scoped AWS outbound federation issuer | role ARN |
| `aws-eks-irsa` / `kubernetes` | cluster OIDC issuer | `system:serviceaccount:ns:name` |
| `azure-managed-identity` | `https://login.microsoftonline.com/{tenant}/v2.0` | object ID |
| `github-actions` | `https://token.actions.githubusercontent.com` | `repo:org/repo:ref:…` |
| `spiffe-oidc` / `oidc` | explicit | explicit |

Each preset declares required inputs, derives issuer/JWKS/audience, constrains
the subject and required claims, and never accepts wildcards. Verification
reuses the existing JWKS verifier and feeds the existing identity/actor path
rather than adding a second authentication stack.

**API**:

| Route | Purpose |
| --- | --- |
| `GET /api/v1/agents/identity-presets` | Catalog with input fields (drives CLI and Console forms) |
| `GET/PUT/DELETE /api/v1/agents/{clientId}/identities/{id}` | Bindings |
| `POST …/identities/{id}/plan` | Cloud-side setup commands and a token-acquisition snippet for that cloud |
| `POST /api/v1/agents/identities/verify` | Dry-run a presented token: returns each check (issuer, signature, audience, subject, claims, expiry) as pass/fail with the reason; never returns or logs the token |

## 3. Tool authentication and authorization types (MCPlex)

`ToolConnection` gains an explicit `auth` block. The absent block keeps today's
behaviour (`oauth_user`).

| Type | Upstream credential |
| --- | --- |
| `none` | No credential |
| `api_key` | Secret reference sent in a named header or query parameter |
| `bearer` | Static secret reference as `Authorization: Bearer` |
| `basic` | Username + secret reference |
| `oauth_user` | Existing delegated OAuth (pre-registered, CIMD, DCR) |
| `oauth_client_credentials` | Service token from a policy-checked token endpoint, cached until expiry |
| `token_exchange` | RFC 8693 exchange of the caller's token for a downstream audience |
| `google_id_token` | Workload-issued ID token for a fixed audience |

Secrets are references resolved at dispatch; token endpoints pass the existing
endpoint policy; credentials are never forwarded to a different origin.

**Authorization** on the connection: `tools.allow` / `tools.deny` exact names
or prefix patterns, `read_only` (reject `tools/call` for tools not annotated
read-only), and enforcement of `mcp:tools:{name}` for the caller on managed
`tools/call`. `tools/list` responses are filtered to what the caller may call.

`GET /api/v1/mcp/auth-types` describes each type's fields for forms.

## Execution

| Phase | Work | Model tier |
| --- | --- | --- |
| 1 | Backend for sections 1, 2 and 3 (packages, stores, handlers, tests), one isolated worktree each, in parallel | Opus |
| 2 | Go SDK, CLI and Console surfaces for each feature on the merged backend | Sonnet |
| 3 | README capability table, guides and docs-site pages | Haiku |
| 4 | Merge, full `go build`/`go test`, console typecheck + tests, review | lead |

## Out of scope

Automatic Envoy/direct-route interception, live/async job billing, currency
conversion, native directory sync, and provisioning cloud-side trust on the
operator's behalf (plans are rendered for review, not applied).
