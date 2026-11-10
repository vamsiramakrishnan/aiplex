# Usage ledger

The usage ledger keeps one append-only, tenant-scoped record for each managed
exchange on the LLMPlex, MCPlex and A2APlex paths, plus records reported to
`POST /api/v1/llm/usage`. It answers questions such as which agent used which
model through which client, how many tokens the provider reported, and what the
exchange cost when a price or a quota settlement establishes a cost. Records hold
identifiers, status, timing and counters. They never hold prompts, completions,
tool arguments, results or credentials.

The design is in [usage ledger, agent identity and tool auth](design/2026-11-09-usage-ledger-agent-identity-tool-auth.md).
Quota admission and settlement are documented in [managed QuotaPlex execution](quotaplex-integration.md).

## Routes

All routes are under `/api/v1/usage`. They require a verified tenant. Callers with
the admin role read every record in their tenant. Other callers read only records
where they are the principal or the actor. Writes to `/api/v1/usage/` require the
admin role.

| Route | Purpose |
| --- | --- |
| `GET /api/v1/usage/records` | List records, newest first. Filters: `plane`, `provider`, `model_id`, `tool_name`, `actor_id`, `principal_id`, `client_id`, `access_point_id`, `caller_kind`, `status`, `source`, `since`, `until`. Pagination: `limit`, `cursor`. |
| `GET /api/v1/usage/records/{id}` | One record. |
| `GET /api/v1/usage/summary` | Totals. Accepts the same filters plus `group_by` and `bucket=hour\|day`. Returns requests, errors, token sums, records with unknown usage and cost per currency. |
| `GET /api/v1/usage/prices` | List model-price resources. |
| `GET /api/v1/usage/prices/{id}` | One price with its revision. |
| `PUT /api/v1/usage/prices/{id}` | Create or replace a price. Send the current revision in `If-Match`; `0` creates. |
| `DELETE /api/v1/usage/prices/{id}` | Delete a price. Send the current revision in `If-Match`. |

Unknown query parameters and unknown `group_by` fields are rejected with
`400 INVALID_QUERY`. The valid `group_by` fields are `plane`, `provider`,
`model_id`, `tool_name`, `actor_id`, `principal_id`, `client_id`,
`access_point_id`, `caller_kind` and `status`.

## Record fields

| Field | Meaning |
| --- | --- |
| `id` | Record identifier, prefixed `ul_`. |
| `plane` | `llmplex`, `mcplex` or `a2aplex`. |
| `provider`, `model_id`, `model_version` | Set for model exchanges. Tool calls use provider `mcp`. |
| `resource_id` | Surface, tool connection or agent connection identifier. |
| `tool_name` | Tool name for `tools/call` exchanges. |
| `principal_id`, `actor_id` | Verified principal and delegated actor, when present. |
| `client_id` | The verified token's authorized party (`azp`). A public OAuth client ID is attribution, not a security boundary. |
| `access_point_id`, `caller_kind` | Set only when the exchange entered through an [access point](access-points.md). `caller_kind` is `agent`, `user` or `client`. |
| `status` | `ok`, `error`, `denied` (HTTP 401, 403, 407 or 429), or `unknown` (the response began but did not complete, or the client disconnected). |
| `source` | `managed` for records captured on AIPlex paths, `reported` for `POST /api/v1/llm/usage`. |
| `requests` | Requests counted for the record. |
| `usage` | Provider-normalized counters, each nullable: `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `reasoning_tokens`, `total_tokens`. The whole object is `null` when no counter is known. |
| `cost` | `basis`. When the basis is not `unknown`, also `currency`, `amount` (a decimal integer in minor units at `scale`) and `scale`. |
| `provider_limits` | The provider rate-limit observation from the native response, when one was reported. See [model providers](model-providers.md). |
| `quota_attempt_id` | QuotaPlex attempt, when quota admitted the exchange. |
| `request_id`, `trace_id` | Correlation identifiers. |

## Unknown is not zero

A counter that the provider did not report is `null`. A reported zero is `0`.
Summaries count records with unknown usage separately, so an absent counter never
lowers a total silently.

Cost has three bases:

| Basis | Source |
| --- | --- |
| `settled` | The QuotaPlex attempt for the exchange settled with a money meter in one currency. |
| `estimated` | A `model-price` resource matched the record. The amount is the exact rational sum of the priced quantities, rounded up once to the price's scale. |
| `unknown` | Neither source applies. The record carries no currency, amount or scale. |

A record gets `unknown` cost when:

- a known non-zero quantity has no matching meter in the price,
- a priced meter's quantity is unknown, or
- more than one price matches the model and provider.

Prices are never invented. The bundled stores contain no built-in price list.

## Prices

Prices are tenant resources of kind `model-price`, managed with `aiplex usage prices`.
Cost is computed once, when the record is written. Saving a price later does not
change existing records. A price applies only to records whose `started_at` falls
inside its `effective_from` and `effective_until` window.

```json
{
  "id": "example-flash",
  "model_id": "gemini-2.5-flash",
  "currency": "USD",
  "scale": 6,
  "meters": {
    "input_tokens": {"numerator": 1, "denominator": 1},
    "output_tokens": {"numerator": 1, "denominator": 1},
    "requests": {"numerator": 0, "denominator": 1}
  }
}
```

The rates above are placeholders that show the shape of the resource. Replace
them with your contracted prices. Each meter is `numerator` fixed-point money
units, at the price's `scale`, per `denominator` raw units. Allowed meters are
`input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`,
`reasoning_tokens` and `requests`. A zero numerator declares a free meter.

`input_tokens` is the total input. When `cache_read_tokens` is also priced, only
the uncached remainder is charged at the input rate. `reasoning_tokens` is charged
in addition to `output_tokens`, so price it only for providers that report
reasoning separately. Optional `provider` narrows a price to one provider. A
price without `provider` matches any provider for that model.

## Worked example

```sh
# Save the price, then read it back
aiplex usage prices set -f example-flash.json
aiplex usage prices list
aiplex usage prices get example-flash

# Records for one agent in the last day, errors only
aiplex usage records --agent tutor-agent --since 24h --status error

# Daily totals by model and status for the last week
aiplex usage summary --by model_id,status --bucket day --since 7d

# One record in full
aiplex usage get ul_<id>
```

`--by` is passed to `group_by` unchanged. It takes the API field names listed
above (`model_id`, `actor_id`, `principal_id`, `client_id` and so on), not the
shorter names in the CLI help text.

## Capture and enablement

Capture needs no switch. Each completed exchange on these paths writes one record:

| Path | Record |
| --- | --- |
| `/api/v1/llm/native/{id}/*` and `/api/v1/llm/gateway/{id}/*` | `plane=llmplex`, `source=managed` |
| `/api/v1/a2a/connections/{id}/invoke` | `plane=a2aplex`, `source=managed` |
| `/api/v1/mcp/connections/{id}/invoke` | `plane=mcplex`, `source=managed` |
| `POST /api/v1/llm/usage` | `plane=llmplex`, `source=reported`. The route requires the admin role, and a verified tenant is needed to mirror the report. |

The access-point model and tool endpoints call the same handlers, so their
exchanges are recorded with `access_point_id` and `caller_kind` set.

The ledger uses Firestore when the registry store is Firestore. Otherwise it uses
an in-process memory store that keeps up to 100,000 records per tenant and loses
them on restart. The bundled stores bound retention to 90 days. Records are
written in the background. A failed write is logged with the record ID and does
not change the response to the client.

## Limits

- Tool and agent records carry no token counts. Only model exchanges install a
  usage observer, so `usage` is `null` for them.
- Only managed paths and trusted reports are captured. Envoy routes, direct
  provider calls and other traffic that bypasses these paths produce no record.
- A reported record is only as accurate as its reporter. The ledger does not
  check it against the provider.
- `POST /api/v1/llm/usage` mirrors its counters into the ledger but does not copy
  the report's floating-point cost. The ledger cost comes from a price resource or
  is `unknown`.
- A price is not applied retroactively. Correcting a price does not correct stored
  records.
- Summaries group only on the fields listed above. There is no free-text search
  and no export route.
- The memory store is not durable. Firestore-backed records follow the bundled
  90-day retention bound.
- Live and asynchronous job billing is outside this ledger. A managed request that
  starts a provider job records the request, not the job's later work.
- Currency conversion is not performed. Costs are reported per currency.
