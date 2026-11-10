---
title: Model Batches API
---

The model batches API lists the tenant's durable records of provider batch jobs. A batch record is created when a client creates a batch through a model surface, and it is settled when the results are fetched through the gateway. The list is administrative and read-only. It shows the state, the provider's last reported status and the item count. It never shows prompts, results or usage counters. Batch creation, status, cancellation and results are made through the client gateway, as described at the end of this page.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/llm/batches` | List batch records of the tenant, ordered by record id | `admin` role |

The route is under `/api/v1` and requires a verified identity and a tenant binding. The check is on the verified role context, so a caller without the `admin` role receives `403 ADMIN_REQUIRED` even in development mode.

## GET /api/v1/llm/batches

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `state` | string | No | `open` or `settled`. Omitted means both. |
| `surface_id` | string | No | Only batches created through this model surface |
| `limit` | integer | No | 1 to 500. Default 100. |
| `cursor` | string | No | The `next_cursor` of the previous page |

Any other value of `state`, a `limit` outside 1 to 500, or a non-integer `limit` returns `400 INVALID_QUERY`.

### Response

| Field | Type | Meaning |
|---|---|---|
| `batches` | array of batch view | Batches on this page. Always an array. |
| `next_cursor` | string | Present only when another page may exist. Pass it as `cursor`. |

### Batch view

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Record identifier. A digest of the surface, provider and provider batch id. |
| `surface_id` | string | Model surface that created the batch |
| `provider` | string | Provider of the batch |
| `batch_id` | string | The provider's batch identifier |
| `attempt_id` | string | Omitted when empty. Attempt identifier recorded with the batch. |
| `model` | string | Omitted when empty. Model the batch was created for. |
| `create_operation` | string | Operation that created the batch |
| `state` | string | `open` until the results are fetched through the gateway and settled, then `settled` |
| `provider_status` | string | Omitted when empty. The provider's last reported batch status. |
| `ended` | boolean | `true` when the provider reports that the batch has ended |
| `expected` | integer or null | The provider's item count. `null` while unknown. |

An `open` batch keeps the quota exposure and the concurrency slot that it was admitted with.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Page returned |
| 400 | `INVALID_QUERY` | `state` is not `open` or `settled`, or `limit` is not an integer from 1 to 500 |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 403 | `ADMIN_REQUIRED` | The caller lacks the `admin` role |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/llm/batches?state=open&limit=20" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "batches": [
    {
      "id": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "surface_id": "surface-example",
      "provider": "openai",
      "batch_id": "batch-example",
      "create_operation": "openai.v1.batches-create",
      "state": "open",
      "provider_status": "in_progress",
      "ended": false,
      "expected": null
    }
  ],
  "next_cursor": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}
```

## Behaviour notes

- **Read-only.** The route cannot create, change, cancel or settle a batch. It only reads the durable records.
- **Records, not jobs.** A listed item is a record of a batch that a gateway created. The list does not query the provider. `provider_status` and `ended` are the values the provider reported last, so they can be older than the provider's current state.
- **Hidden records.** Records that are result-file pointers, records of another tenant, and records that cannot be read are skipped. They never appear in a page.
- **Ordering and paging.** Batches are ordered by record `id`, ascending. `next_cursor` is the `id` of the last batch on the page, and the next page returns records with an `id` greater than it. The filters apply to every page. The cursor is the record identifier and should be passed back unchanged.
- **Unknown is null.** `expected` is `null` until the provider reports an item count. It is never zero for an unknown count.
- **No content.** The view carries no prompts, no results, no usage counters and no credentials.

## Batch operations through the client gateway

Batches are created, read, cancelled and fetched through the client gateway, at `/api/v1/llm/gateway/{surface_id}/…`, using the wire protocol of the client's own SDK. The operation identifiers are listed by `GET /api/v1/llm/operations`, and they include:

| Provider family | Operation identifiers |
|---|---|
| OpenAI | `openai.v1.batches-create`, `openai.v1.batches-get`, `openai.v1.batches-list`, `openai.v1.batches-cancel` |
| Anthropic | `anthropic.message-batches-create`, `anthropic.message-batches-get`, `anthropic.message-batches-list`, `anthropic.message-batches-cancel`, `anthropic.message-batches-results` |
| Gemini | `gemini.batches.get`, `gemini.batches.cancel` |

Requests to the gateway follow the rules in the model surfaces page: a non-GET method needs the `admin` or `deployer` role outside development mode, and each operation needs its `llm:surface:{surface}:{operation}` grant. Results are fetched through the gateway, and that fetch settles the batch.
