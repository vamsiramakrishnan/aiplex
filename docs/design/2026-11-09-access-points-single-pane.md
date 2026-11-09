# Access points and the single pane

Status: plan, in execution. Date: 2026-11-09. Builds on
[usage ledger, agent identity and tool auth](2026-11-09-usage-ledger-agent-identity-tool-auth.md).

## Goal

One place to manage three kinds of caller reaching three kinds of resource, on
any cloud, GCP-first:

```text
 callers                         enforcement point                 resources
 ───────                         ─────────────────                 ─────────
 agents  (workload identity) ─┐                                 ┌─ models (any)
 users   (workforce identity) ─┼─▶  access point (one URL)  ───▶─┼─ tools  (MCP)
 clients (IDE / CLI / SDK)   ─┘   identity → access → QoS/quota  └─ skills
                                   → native call → usage ledger
```

Today the pieces exist but are addressed per resource: a model gateway URL per
model surface, a bespoke invoke route per tool connection, quota bindings per
surface. A client that accepts "a custom base URL" and "an MCP catalog URL"
has nothing stable to point at, and nothing ties its traffic to a QoS class.

## 1. Access point

A tenant resource (`access-point`) that is a stable URL and an enforcement
point: `https://<aiplex>/ap/{id}/…`.

```json
{
  "id": "ide-platform-team",
  "display_name": "IDE – platform team",
  "caller_kinds": ["user", "client"],
  "subjects": {"groups": [{"source": "…", "id": "platform-eng"}], "principals": [], "agents": [], "client_registrations": []},
  "models": [
    {"name": "gemini-2.5-pro", "surface_ref": "vertex-global", "native_model": "gemini-2.5-pro"},
    {"name": "claude-sonnet", "surface_ref": "vertex-anthropic-us", "native_model": "claude-sonnet-4-5"}
  ],
  "tools": [{"connection_ref": "github"}, {"connection_ref": "search", "expose": ["search*"]}],
  "skills": [{"instance_ref": "runbooks"}],
  "qos": {"class": "interactive", "weight": 4, "limits": {"requests_per_minute": 120, "tokens_per_minute": 400000, "concurrency": 8}, "budget_refs": ["platform-monthly"]}
}
```

**Model endpoints** (client wire protocols; the model name selects the route):

| Path under `/ap/{id}` | Protocol |
| --- | --- |
| `/v1/chat/completions` | OpenAI Chat Completions |
| `/v1/responses` | OpenAI Responses |
| `/v1/messages` | Anthropic Messages |
| `/v1beta/models/{model}:{op}` | Google GenAI |
| `/v1/models` | Listing, filtered to what the caller may use |

A request is resolved to a saved model surface and then handed to the existing
managed native handler, so authorization, credentials, quota admission and the
usage ledger are the ones already in place. A model name is reachable through a
protocol only when its surface speaks that protocol natively; there is no
cross-protocol translation in this increment, and an unreachable combination
returns a protocol-native error that says which protocols serve that model.

**Tool endpoints**:

| Path under `/ap/{id}` | Purpose |
| --- | --- |
| `/mcp` | One standard MCP Streamable HTTP server aggregating every tool the access point exposes; tool names are `{connection}__{tool}` |
| `/mcp/{connection}` | The same for a single connection, original tool names |
| `/v0.1/servers` | MCP registry listing of the above for clients that take a catalog URL |
| `/.well-known/oauth-protected-resource…` | RFC 9728 metadata pointing at the AIPlex authorization server |

Each `tools/call` goes through the existing managed tool path (connection
policy, caller scope, quota, upstream credential, ledger).

**Skills**: `/skills` returns the manifest of exposed skills.

**QoS and quota**: QuotaPlex stays the only limiter. `access_point_id` and
`caller_kind` become quota match dimensions, and an access point's `qos` block
is rendered into a reviewed QuotaPlex policy fragment (preview, then apply
through the existing policy flow). A request through an access point with a
`qos` block but no active matching quota binding fails closed. The usage ledger
gains `access_point_id` and `caller_kind` as fields, filters and `group_by` keys.

**Caller kind** is derived from the verified identity, never from the request:
`agent` when an actor is present or the principal is a workload identity,
`client` when the token was issued to a registered ClientPlex registration,
`user` otherwise.

## 2. User identity through workforce federation

`user-identity-provider` resources with the same shape as agent identities:
preset catalog, binding, setup plan, token dry-run.

| Preset | Issuer |
| --- | --- |
| `google-workforce-pool` | Google Workforce Identity Federation pool |
| `google-workspace` | `https://accounts.google.com`, pinned `hd` |
| `entra-id` | `https://login.microsoftonline.com/{tenant}/v2.0` |
| `okta` / `oidc` | explicit |

A provider pins issuer, audience, the subject claim and an explicit group claim
(or declares that groups come from the membership authority). It feeds the
existing workforce identity source path: verified users get canonical principal
IDs and, when groups are verified, memberships usable by role bindings and
quota eligibility. Missing groups are never treated as empty membership.

## 3. Single pane

**API** `GET /api/v1/overview`:

- `/subjects?kind=agent|user|client` — each subject with identity source, last
  seen, access points, and a usage roll-up (requests, tokens, cost per currency)
- `/subjects/{id}` — effective access to models, tools and skills, with the
  reason for each (role binding, scope, access point), quota allocations and
  remaining balance where known
- `/access-points/{id}` — callers, resources, QoS, live usage
- `/access-points/{id}/connect?client=…` — ready-to-paste configuration for a
  client profile, SDK or IDE (base URL, MCP URL, catalog URL, auth steps)

**Console**: a home view with Agents / Users / Clients tabs over the same
table (who, how they authenticate, what they can reach, QoS class, usage), an
access-point editor, and a "Connect" wizard. **CLI**: `aiplex access-points …`,
`aiplex connect <access-point> --client …`, `aiplex who <subject>`.

## Execution

| Step | Work | Tier |
| --- | --- | --- |
| A | Access point resource, model router, MCP endpoints, quota and ledger dimensions | Opus |
| B | User identity providers (follows the agent-identity pattern) | Sonnet |
| C | Overview API, then SDK, CLI and Console for A–C | Sonnet |
| D | README, guides, docs-site for everything on the branch | Haiku |
| E | Merge, full checks, review of auth-sensitive diffs | lead |

## Not in this increment

Cross-protocol translation between model wire formats; Google Agent Registry
import/publish and Agent Gateway push-down (pinned); automatic interception of
traffic that bypasses an access point.
