---
title: Overview API
---

The overview API is the read-only single-pane view behind the console's overview. It joins access points, the usage ledger, agent identity bindings, client registrations, role bindings and quota coverage into three reads: a subject table, an access-point list and per-access-point and per-subject detail. It never writes. Every read is bound to the verified tenant. Every route requires the `admin` or `deployer` role outside development mode, including GET requests.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/overview/subjects` | Paginated subject table for one kind (`agent`, `user` or `client`) | `admin` or `deployer` |
| GET | `/api/v1/overview/subjects/{kind}/{id}` | Detail of one subject. Use when the id has no `/`. | `admin` or `deployer` |
| GET | `/api/v1/overview/subjects/{kind}` | Detail of one subject with the id in `?id=`. Use for ids that contain `/`, such as SPIFFE ids. | `admin` or `deployer` |
| GET | `/api/v1/overview/access-points` | Paginated list of access points with counts and quota coverage | `admin` or `deployer` |
| GET | `/api/v1/overview/access-points/{id}` | Detail of one access point: resources, callers, quota and usage | `admin` or `deployer` |
| GET | `/api/v1/overview/access-points/{id}/connect` | Connection instructions for one access point, for a chosen client | `admin` or `deployer` |

Outside development mode, a caller without the role receives `401 UNAUTHENTICATED` when no identity is verified, or `403 FORBIDDEN` when the identity lacks both roles.

Errors use this shape:

```json
{
  "code": "INVALID_QUERY",
  "message": "limit must be between 1 and 200",
  "request_id": "req-example"
}
```

## Shared query rules

- Unknown query parameters are rejected with `400 INVALID_QUERY`. Each parameter may appear once, and a value may be at most 512 characters.
- `since` accepts an RFC 3339 timestamp, or a Go duration such as `30m` or `24h`, or a whole number of days such as `7d`. A duration is measured back from now. The default is `24h`. `since` must not be in the future, and it may reach back at most 90 days, the ledger retention. Anything else returns `400 INVALID_QUERY`.
- `limit` is an integer from 1 to 200. The default is 50.
- `cursor` is opaque. It is the base64url encoding of the last `id` on the previous page. A malformed cursor returns `400 INVALID_QUERY`.
- Lists are ordered by `id`.

## GET /api/v1/overview/subjects

Returns one page of subjects of one kind. The kind is required and is one of `agent`, `user` or `client`. Subjects are gathered from several sources, which are listed in `derived_from`. Subjects that appear in none of them are not listed.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `kind` | string | Yes | `agent`, `user` or `client` |
| `since` | string | No | Usage window start. See shared rules. |
| `limit` | integer | No | 1 to 200. Default 50. |
| `cursor` | string | No | Cursor from `next_cursor` |

### Response

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | Requested kind |
| `since` | RFC 3339 timestamp | Start of the usage window |
| `subjects` | array of subject row | Subjects on this page |
| `next_cursor` | string | Present only when another page exists |
| `usage_available` | boolean | `true` when a usage ledger is configured |
| `usage_truncated` | boolean | `true` when the ledger scan reached its record bound |
| `derived_from` | array of string | The sources that produce this kind |
| `limitations` | array of string | Statements of what this list cannot show. Always an array. |

### Subject row

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | Subject kind |
| `id` | string | Subject identifier. For agents this is the registered client id when one resolves. |
| `display_name` | string | Omitted when unknown |
| `identity` | object | `source` and `detail`. See Identity sources. |
| `access_points` | array of string | Access points that admit this subject. An access point is included when its state is `yes` or `unknown`. |
| `last_seen` | RFC 3339 timestamp or null | Latest matching record in the ledger within the window. `null` when there is none or no ledger. |
| `usage` | object | Usage roll-up for the subject within the window. See Usage roll-up. |
| `known_from` | array of string | Sources that made this subject known: `identity-binding`, `access-point`, `client-registration` or `usage-ledger` |

### Identity sources

| Kind | `identity.source` values |
|---|---|
| agent | `federated-identity`, `spiffe`, `workload-identity-federation`, `oauth-client`, or `unknown` when no registered agent or binding resolves it |
| user | `workforce-principal` for a canonical principal id, or `declared-principal` for an id named by an access point |
| client | `client-registration`, or `unknown` when no registration with this id exists in the tenant |

### Usage roll-up

Used by subject rows, subject detail and access-point detail.

| Field | Type | Meaning |
|---|---|---|
| `requests` | integer | Sum of request counts |
| `records` | integer | Records in the window |
| `errors` | integer | Records with status `error` |
| `denied` | integer | Records with status `denied` |
| `tokens` | object of integer | Sums of reported token counters. Counters that no record reported are absent. |
| `tokens_unknown` | object of integer | Per counter, the number of records that did not report it |
| `cost` | array of object | Exact totals per `currency`, `basis`, `amount` (decimal string) and `scale`. Unknown cost is never folded in. |
| `unknown_cost_records` | integer | Records with no amount |
| `bounded_usage_records`, `lower_bound_records`, `ambiguous_usage_records`, `partial_usage_records` | integer | Omitted when zero. A non-zero value marks the totals as not final. |

The roll-up has the same meaning as the usage API summary groups. See the usage API page for the counter and cost rules.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Page returned |
| 400 | `INVALID_QUERY` | Unknown or repeated parameter, missing or unknown `kind`, bad `since`, bad `limit`, malformed `cursor` |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 503 | `OVERVIEW_UNAVAILABLE` | Tenant configuration could not be read |
| 503 | `USAGE_LEDGER_UNAVAILABLE` | The ledger scan failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/overview/subjects?kind=client&since=7d&limit=20" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "kind": "client",
  "since": "2026-11-03T09:00:00Z",
  "subjects": [
    {
      "kind": "client",
      "id": "client-example",
      "identity": {"source": "client-registration", "detail": "profile=example mode=example protocol=example auth_mode=example"},
      "access_points": ["ap-example"],
      "last_seen": null,
      "usage": {"requests": 0, "records": 0, "errors": 0, "denied": 0, "tokens": {}, "tokens_unknown": {}, "cost": [], "unknown_cost_records": 0},
      "known_from": ["client-registration", "access-point"]
    }
  ],
  "usage_available": false,
  "usage_truncated": false,
  "derived_from": ["client registrations", "access point subjects.client_registrations", "usage ledger (client_id) within the window"],
  "limitations": ["The usage ledger is not configured; usage and last_seen are unavailable."]
}
```

## GET `/api/v1/overview/subjects/{kind}/{id}`

Returns the detail of one subject. `kind` is `agent`, `user` or `client`. The path `id` is percent-decoded once. For ids that contain `/`, use the `?id=` form below. For agents, the id may be any identifier the agent appears under (client id, SPIFFE id or actor id). It is resolved to the registered client id when one exists.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `since` | string | No | Usage window start |
| `id` | string | Only with `/subjects/{kind}` | Subject identifier, up to 1024 characters |

### Response

| Field | Type | Meaning |
|---|---|---|
| `kind`, `id` | string | Subject |
| `display_name` | string | Omitted when unknown |
| `identity` | object | `source` and `detail` |
| `aliases` | array of string | Identifiers the agent appears under. Agents only. Omitted when empty. |
| `identity_bindings` | array of object | Agent identity bindings: `id`, `preset`, `issuer`, `subject`, `subject_claim`. Agents only. Omitted when empty. |
| `user_identity_providers` | array of object | Tenant user identity providers: `id`, `display_name`, `issuer`, `disabled`. Users only. Omitted when empty. |
| `access_points` | array of object | Admission per access point: `id`, `display_name`, `admits` (`yes`, `no` or `unknown`) and `reason`. Only access points with state `yes` or `unknown` are listed. |
| `access` | object | Models, tools and skills the subject can reach. See Access. |
| `quota` | object | `access_points` (quota coverage and allocations per admitting access point) and `balance` |
| `since` | RFC 3339 timestamp | Start of the usage window |
| `last_seen` | RFC 3339 timestamp or null | Latest matching ledger record |
| `usage` | object | Usage roll-up for the window. Always an object when the ledger is configured. |
| `usage_by_model` | array of group | Roll-up grouped by `model_id`. Each entry has `key`, `last_seen`, `usage`. |
| `usage_by_tool` | array of group | Roll-up grouped by `tool_name` |
| `notes` | array of string | Statements about the view. Always an array. |
| `known_from` | array of string | Sources that made the subject known |

### Access

`access` has `models`, `tools` and `skills`, each an array of items:

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | `model`, `tool` or `skill` |
| `name` | string | Name of the item |
| `access_point_id` | string | Access point through which the item is offered |
| `resource` | string | Omitted when empty |
| `required_scope` | string | Omitted when empty. The scope the item requires. |
| `state` | string | `granted`, `not_granted` or `unknown` |
| `reasons` | array of object | Why the state holds: `type`, and optionally `ref`, `detail` and `conditional`. `type` is `access-point`, `scope`, `manual-scope`, `role-binding` or `agent-ceiling`. |

Access also carries `scopes_known` (boolean), `scopes_note` (string) and `truncated` (boolean).

A `role-binding` reason is always conditional. Group membership is not stored, so a role binding can be shown as a possible grant, and it resolves to `unknown` unless a manual scope grants the item outright. `scopes_known` is `false` when the server cannot read the facts needed to decide.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Detail returned |
| 400 | `INVALID_QUERY` | Unknown or repeated parameter, bad `since`, or unknown `kind` |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | Empty id, or the subject is not known from any source in this tenant |
| 503 | `OVERVIEW_UNAVAILABLE` | Tenant configuration could not be read |
| 503 | `USAGE_LEDGER_UNAVAILABLE` | The ledger scan failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/overview/subjects/user/user-example?since=24h" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

For an id with a slash:

```bash
curl -sS -G "$AIPLEX_URL/api/v1/overview/subjects/agent" \
  --data-urlencode "id=spiffe://example/ns/example/sa/example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET /api/v1/overview/access-points

Returns one page of access points ordered by `id`. Each row is built from saved configuration and the active quota policy only. It does not scan the usage ledger and does not resolve model surfaces or tool connections.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `limit` | integer | No | 1 to 200. Default 50. |
| `cursor` | string | No | Cursor from `next_cursor` |

### Response

| Field | Type | Meaning |
|---|---|---|
| `access_points` | array of row | Rows on this page |
| `next_cursor` | string | Present only when another page exists |
| `skipped_invalid` | integer | Omitted when zero. Saved access points that failed to decode and were left out. |

Row fields:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Access point identifier |
| `display_name` | string | Omitted when empty |
| `caller_kinds` | array of string | Always an array |
| `qos_class` | string | Omitted when the access point has no QoS class |
| `counts` | object | `models`, `tools` and `skills`: the number of entries of each |
| `quota_coverage` | object | `state` and `detail`. `state` is `not_required`, `covered`, `uncovered` or `unknown`. |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Page returned |
| 400 | `INVALID_QUERY` | Unknown or repeated parameter, bad `limit`, malformed `cursor` |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 503 | `OVERVIEW_UNAVAILABLE` | Saved access points could not be listed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/overview/access-points?limit=10" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "access_points": [
    {
      "id": "ap-example",
      "caller_kinds": ["user"],
      "counts": {"models": 1, "tools": 0, "skills": 0},
      "quota_coverage": {"state": "not_required", "detail": "example"}
    }
  ]
}
```

## GET `/api/v1/overview/access-points/{id}`

Returns the detail of one access point.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `since` | string | No | Usage window start |

### Response

| Field | Type | Meaning |
|---|---|---|
| `access_point` | object | The saved specification, as described on the access points API page |
| `revision` | integer | Saved revision |
| `since` | RFC 3339 timestamp | Start of the usage window |
| `callers` | object | Who can call. See Callers. |
| `resources` | object | `models`, `tools` and `skills`, each resolved against the tenant |
| `qos` | object or null | The saved `qos` block, or `null` |
| `quota` | object | `access_point_id`, `qos_class` (omitted when empty), `coverage` and `allocations` (`budgets`, `rates`, `concurrency`) |
| `balance` | object | `available` (always `false` in this response) and `reason` |
| `usage` | object | `available`, `truncated`, `total`, `by_caller_kind`, `by_model`, `by_tool` |
| `notes` | array of string | Statements about the view. Always an array. |

### Callers

| Field | Type | Meaning |
|---|---|---|
| `caller_kinds` | array of string | Kinds the access point admits |
| `open_to_all_of_kind` | array of string | Kinds admitted without a subject constraint |
| `users`, `agents`, `clients` | array of caller | Callers the access point names or the ledger observed |
| `groups` | array of `{source, id}` | Source-qualified groups named by subjects |
| `note` | string | Omitted when empty |

A caller has `id`, `display_name` (omitted when empty), `declared` (named by the access point), `observed` (seen in the ledger within the window), `last_seen` and `usage`.

### Resources

Each resource says whether it resolved in the tenant:

- `models`: `name`, `native_model`, `model_id` (omitted when unresolved), `surface_ref`, `publisher` (omitted when empty), `surface_protocol` (omitted when empty), `wire_protocols` and `resolved`.
- `tools`: `connection_ref`, `expose` (omitted when empty), `tools` (the names the access point exposes from that connection) and `resolved`.
- `skills`: `instance_ref`, `skills` and `resolved`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Detail returned |
| 400 | `INVALID_QUERY` | Unknown or repeated parameter, bad `since` |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No access point with this identifier |
| 409 | `ACCESS_POINT_INVALID` | The saved access point cannot be decoded, or the store read failed |
| 503 | `OVERVIEW_UNAVAILABLE` | Tenant configuration could not be read |
| 503 | `USAGE_LEDGER_UNAVAILABLE` | The ledger scan failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/overview/access-points/ap-example?since=7d" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/overview/access-points/{id}/connect`

Returns connection instructions for one access point, built for the chosen client. Every URL is built from the configured public URL, never from the request `Host` header. The output never contains a token or a secret. Snippets read the token from the environment variable named in `auth.token_env`, which is `AIPLEX_TOKEN`.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `client` | string | No | `generic` (default) or the id of a built-in client profile. An unknown id returns `404 UNKNOWN_CLIENT`, and the message lists the valid ids. |

### Response

| Field | Type | Meaning |
|---|---|---|
| `access_point_id` | string | Identifier of the access point |
| `client` | string | Client used for the snippets |
| `base_urls` | object of string | `openai` (`{ap}/v1`), `anthropic` and `genai` (`{ap}`). Present only for families that the access point reaches. |
| `endpoints` | array of object | Each has `protocol`, `transport` (`http`, `http-stream` or `websocket`), `url`, `label` (omitted when empty), `query` (omitted when empty, and `["model"]` for WebSocket endpoints), and `models` |
| `models` | array of object | Each has `name` and the `protocols` that reach it |
| `mcp_url` | string | Omitted unless the access point has tools and the MCP module is enabled |
| `mcp_catalog_url` | string | Omitted with `mcp_url`. The `/v0.1/servers` URL. |
| `skills_url` | string | Omitted unless the access point has skills and the skills module is enabled |
| `auth` | object | `scheme` (`bearer`), `header`, `token_env`, `caller_kinds`, `authorization_servers` (omitted when empty), `resource_metadata_url` (omitted unless there are tools), and `steps` |
| `snippets` | array of object | `title`, `language` and `content` |
| `notes` | array of string | Statements about the output. Always an array. |

The `endpoints` list uses the same wire paths as the access points API page. WebSocket endpoints use `ws://` or `wss://` when the public URL is `http://` or `https://`. The OpenAI Realtime and Gemini Live endpoints are the only WebSocket endpoints.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Output returned |
| 400 | `INVALID_QUERY` | Unknown or repeated parameter |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `UNKNOWN_CLIENT` | `client` is not `generic` or a built-in profile id |
| 404 | `NOT_FOUND` | No access point with this identifier |
| 409 | `PUBLIC_URL_REQUIRED` | The server has no configured public URL, so no URL can be built |
| 409 | `ACCESS_POINT_INVALID` | The saved access point cannot be decoded, or the store read failed |
| 422 | `CLIENT_NOT_APPLICABLE` | The chosen client speaks no protocol that this access point offers |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/overview/access-points/ap-example/connect?client=generic" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Behaviour notes

- **Read-only.** No route on this API writes. Every value is derived from saved resources, bindings, the usage ledger and the active quota policy.
- **Unknown is not zero.** `unknown` appears in admission and access states when the server does not hold the facts. Absent token counters are unknown, never zero.
- **Admission states.** `yes` means admitted. `no` means not admitted, and such access points are omitted from the subject's `access_points`. `unknown` means admission depends on facts the server does not hold. Group membership is one such fact.
- **Scopes are partial.** Manual scopes are exact. Role bindings are conditional. Agent scope ceilings are the registered agent's `allowed_scopes` only. A token carries the intersection of the ceiling, the user ceiling and the user's consent, so the overview cannot show the final scope of a token.
- **Subjects are derived, not stored.** There is no user directory. A user appears only if an access point names them or the ledger recorded them in the window. Users who appear in neither are not listed.
- **Agents are merged.** Identifiers that resolve to the same registered client are merged into one subject. An unresolved identifier is shown as it appears.
- **Bounds.** A subject list considers at most 10000 candidates and says so in `limitations`. The access detail of a subject stops after 500 items across models, tools and skills, and then sets `access.truncated`. Each ledger scan covers at most 200000 records. When that bound is reached, `usage_truncated` (subject list) or a note (detail) says so.
- **Window.** The usage window is at most 90 days, the ledger retention. Records older than retention are gone and cannot be shown.
- **Connect output** names the environment variable and the steps but contains no credential. Clients read the token at run time.
- **Errors at the store boundary.** `ACCESS_POINT_INVALID` is also returned when the store read fails for a reason other than "not found". Clients should treat it as a store error as well as a validation error.
