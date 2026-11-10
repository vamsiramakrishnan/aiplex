# Model batches

A model batch is a provider batch job that the gateway created through a provider surface.
The gateway keeps one durable record for each batch. It admits the batch once, at create.
It settles the batch's usage once, from results that a caller fetches through the gateway.
The batch's own work is not metered after that.

Provider surfaces, operation IDs and the quota profiles are in
[model providers](model-providers.md). The metering contract is in
[model metering profiles](model-metering-profiles.md#provider-batches). The usage fields a
settled batch writes are in the [usage ledger](usage-ledger.md#units-and-modality-splits).
The batch rows of the [coverage matrix](reference/usage-formats/coverage.md) state the
observation level for each provider.

## Supported providers

| Provider | Create | Status | Cancel | Results (usage source) |
| --- | --- | --- | --- | --- |
| `openai` | `POST /v1/files` with `purpose: batch`, then `POST /v1/batches` | `GET /v1/batches/{id}` | `POST /v1/batches/{id}/cancel` | Output and error files, `GET /v1/files/{id}/content` for the batch's `output_file_id` and `error_file_id` |
| `anthropic` | `POST /v1/messages/batches` | `GET /v1/messages/batches/{id}` | `POST /v1/messages/batches/{id}/cancel` | `GET /v1/messages/batches/{id}/results` |
| `gemini-developer` | `POST /v1beta/models/{model}:batchGenerateContent` | `GET /v1beta/batches/{id}` | `POST /v1beta/batches/{id}:cancel` | Inline responses in the terminal batch object, returned by `GET /v1beta/batches/{id}` |

OpenAI batches need an input file that the caller uploaded. Only the `batch` purpose is
accepted for file creation. Output and error files are readable only for a batch that the
caller owns. Anthropic requests must name models on the surface's allowlist.

Vertex AI batch prediction is not settled through the gateway. Its output is written to
Cloud Storage or BigQuery, which the gateway does not read. See [limits](#limits).

## Admission at create

Batch usage is settled under a `batch-usage` quota binding on the batch-create operation.
That binding is operator configuration in QuotaPlex. Without it, the create is metered as
one managed HTTP exchange, and no batch usage is settled from results.

Under the binding, the create is admitted once with a trusted exposure bound:

- `requests` is 1.
- `batch_requests` is the inline request count, when the body lists requests (Anthropic).
- Every other mapped meter, such as `input_tokens`, `output_tokens`, `cached_tokens`,
  `cache_write_tokens`, or `batch_requests` for an OpenAI file input, comes from the trusted
  bounds authority.

The create response's batch ID is bound to the caller. It is pinned to the admission attempt
and written to a durable record before the ID is released. The record is a tenant resource
of kind `model-batch`.

A provider rejection of the create that the provider documents as refused before processing
releases the hold. The rule is in [provider rejections](model-providers.md#provider-rejections).
Any other create result leaves the attempt open.

## Open state

A batch is `open` until its results are settled. While it is open:

- The attempt is `unknown`, with reason `batch_results_pending`.
- The admitted exposure and one concurrency permit are held.
- The batch appears in the existing aging and unknown-usage reporting.

Retrieving the batch, or cancelling it, does not settle it. Retrieval records the provider's
status (`provider_status`) and whether the batch has ended (`ended`). For Gemini, a
retrieval that returns a terminal batch with inline responses can settle the batch, as
described below. A batch whose results are never fetched through the gateway stays open. Its
exposure stays admitted.

## Settlement

Usage is taken only from results fetched through the gateway. Results that the caller
downloads directly from the provider are not read.

- **OpenAI.** The gateway reads the output file and the error file through
  `GET /v1/files/{id}/content`. Error lines are provider evidence that an item failed or
  expired. Those items are released and never charged. The batch object's `usage` object is
  a cross-check only. It is compared with the per-line sum and is never added to it. A
  disagreement leaves the batch unsettled and flags the record with `cross_check_mismatch`.
- **Anthropic.** The gateway reads `GET /v1/messages/batches/{id}/results`. The batch object
  is not a usage source.
- **Gemini Developer API.** The gateway reads the inline responses in the terminal batch
  object. A batch whose output is a `responsesFile` is never settled from it, because the
  per-line wrapper is not confirmed. Such a batch keeps its exposure.

A results body counts only when it ends cleanly and every line parses. A cut or malformed
body changes nothing.

A batch is complete when the parsed items account for every item the provider reported, with
no duplicate `custom_id`. The item count comes from the provider: the inline create count for
Anthropic, `request_counts.total` of a terminal batch for OpenAI, and `batchStats.requestCount`
for Gemini.

Usage is the per-counter sum over succeeded items only. Errored, cancelled and expired items
contribute nothing, because the results say so. An item with no line is never assumed to be
released.

If a counter is reported on some succeeded lines and not on others, its sum is unknown. The
results are still delivered, and the batch keeps its exposure. Cache writes absent on a line
are one example.

The final results line is held until the settlement is acknowledged. If quota cannot
acknowledge the settlement, the line is withheld.

## Exactly once

- Settlement is idempotent per admission attempt. The evidence ID is derived from the attempt.
  Repeating a settlement re-acknowledges the same evidence. Different totals for the same
  attempt are refused as a conflict.
- The batch record is written with compare-and-swap revisions. Only the exchange that wins
  the swap writes the batch usage to its ledger record. That record is marked `batch: true`
  and is attributed to the batch's attempt and model.
- A second or concurrent fetch of the same results settles nothing and records nothing.
- A fetch that fails part way changes nothing. A later complete fetch can settle the batch.

## Pricing

Every batch exchange's ledger record has `batch: true`. A `model-price` with `"batch": true`
matches those records in preference to the standard price. There is no built-in batch
discount. The operator configures the contracted batch rates. See
[usage ledger prices](usage-ledger.md#prices).

## Listing batches

`GET /api/v1/llm/batches` lists the tenant's batch records. It requires the admin role.
Otherwise it returns `403 ADMIN_REQUIRED`.

| Query parameter | Values | Default | Meaning |
| --- | --- | --- | --- |
| `state` | `open`, `settled` | all | Filter by state. Any other value returns `400 INVALID_QUERY`. |
| `surface_id` | surface ID | all | Only batches created through this surface. |
| `limit` | 1 to 500 | 100 | Page size. Any other value returns `400 INVALID_QUERY`. |
| `cursor` | opaque | none | Continue after the `next_cursor` of the previous page. |

Batches are ordered by record ID. `next_cursor` is present when more pages exist.

The response has a `batches` array and an optional `next_cursor`. Each entry has these fields:

| Field | Meaning |
| --- | --- |
| `id` | Record ID. |
| `surface_id`, `provider`, `batch_id` | The surface, the provider and the provider's batch ID. |
| `attempt_id` | Admission attempt. Omitted when empty. |
| `model` | Model of the batch. Omitted when empty. |
| `create_operation` | The batch-create operation ID. |
| `state` | `open` or `settled`. |
| `provider_status` | The provider's last reported status. Omitted when empty. |
| `ended` | `true` once the provider reported a terminal status. |
| `expected` | The provider's item count. `null` while unknown. |

The view carries no usage counters, prompts or results.

CLI:

```sh
aiplex llm batches list --state open
aiplex llm batches list --surface openai-prod --limit 50 --all
```

`--state`, `--surface`, `--limit` (1 to 500), `--cursor` and `--all` map to the query
parameters above. `--all` follows every page. Machine output is available with `-o json` or
`-o yaml`. The table output names the open-batch caveat when `--state` is not `settled`.

## Access points

Batches are not routed through [access points](access-points.md). No `/ap/{id}` route serves
the batch or file operations. Use the native route or the client facade.

## Limits

- Settlement happens only for results fetched through the gateway. A batch whose results
  are never fetched keeps its exposure, and its usage is not recorded.
- Admission and settlement apply only under a `batch-usage` binding. A batch without one is
  metered as its HTTP exchanges only.
- The record counts items the provider reported. Items without a result line are not
  assumed to be released.
- A Gemini batch with a `responsesFile` output is not settled. Its exposure is kept.
- A Vertex AI batch prediction is not settled through the gateway. The output is in Cloud
  Storage or BigQuery.
- Settlement maps only the counters that the binding maps. The batch-usage meter names are
  `requests`, `batch_requests`, `input_tokens`, `output_tokens`, `cached_tokens` and
  `cache_write_tokens`.
- The job's own runtime, storage and internal work are outside the request count and the
  settlement.
- A batch-level usage object is never added to the per-line totals.
- Batch pricing is the operator's configuration. No discount is built in.
