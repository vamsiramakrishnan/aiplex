---
title: OTLP receiver
---

The OTLP receiver takes OpenTelemetry exports from coding harnesses and turns the usage events it recognizes into `source=reported` records in the [usage ledger](./usage.md). It speaks OTLP/HTTP only. What is mapped, what is not, and what was run against a real client are in [observability](../library/observability.md#harness-telemetry-received-over-otlp) and [harness onboarding](../library/harness-onboarding.md#what-was-run-against-real-clients).

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| POST | `/otlp/v1/logs` | Log events. Recognized usage events become reported records. | Verified identity and tenant. Any caller reports its own telemetry. |
| POST | `/otlp/v1/metrics` | Accepted, counted and dropped (or forwarded). | Same |
| POST | `/otlp/v1/traces` | Accepted, counted and dropped (or forwarded). | Same |

Set the exporter's endpoint to `$AIPLEX_URL/otlp` (it appends `/v1/logs`) and its headers to `Authorization=Bearer <token>`. Tenant, principal, client and caller kind come from the verified token only. Resource and record attributes are untrusted labels and never change whose record it is.

## Request

| Header | Values |
|---|---|
| `Content-Type` | `application/x-protobuf` or `application/json` (OTLP/JSON, with hexadecimal trace and span ids). Anything else is `415`. |
| `Content-Encoding` | none, `identity` or `gzip`. Anything else is `415`. |

Bodies are bounded to 4 MiB as sent and 16 MiB decompressed, and to 1000 log records (or metrics, or spans) per request. An optional `X-AIPlex-Tags: key=value,...` header adds [tags](../library/usage-ledger.md#tags) to the records for keys the operator allowed.

## Response

A full success is `200` with an empty OTLP response. Rejected records are reported in OTLP's partial success: `partial_success.rejected_log_records` and an `error_message` that names the harness, the event and a reason, never payload content. Events that are accepted but deliberately not recorded (prompts, tool results, metrics, traces) are not rejections. A retried export is `200` and stores nothing new.

Failures carry a `google.rpc.Status` body (`code`, `message`) in the request's content type:

| Status | `code` | When |
|---|---|---|
| 400 | 3 | The body is not an OTLP export request |
| 401 | 16 | No verified identity outside development mode |
| 403 | 7 | No verified tenant binding |
| 413 | 3 | Body or item count over its bound |
| 415 | 3 | Unsupported content type or encoding |
| 429 | 8 | Per-principal request or record rate exceeded; `Retry-After` says when to retry |
| 503 | 14 | The ledger cannot be written; `Retry-After` is set and a retry is safe |

## Example

```bash
curl -sS "$AIPLEX_URL/otlp/v1/logs" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \
  -d '{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},
       "scopeLogs":[{"logRecords":[{"timeUnixNano":"1794312000000000000",
         "body":{"stringValue":"claude_code.api_request"},
         "attributes":[{"key":"event.name","value":{"stringValue":"api_request"}},
                       {"key":"model","value":{"stringValue":"claude-sonnet-4-5"}},
                       {"key":"input_tokens","value":{"intValue":"100"}},
                       {"key":"output_tokens","value":{"intValue":"20"}},
                       {"key":"cache_read_tokens","value":{"intValue":"30"}},
                       {"key":"cache_creation_tokens","value":{"intValue":"0"}}]}]}]}]}'
```

The record has `input_tokens` 130 (Claude Code's `input_tokens` excludes cache tokens, the ledger's includes them), `output_tokens` 20, `cache_read_tokens` 30, an unknown cost, `source` `reported`, `harness` `claude-code` and the caller's own principal.

## Behaviour notes

- **Logs only become records.** Metrics and traces describe the same requests, so recording them as well would count a request twice. A harness that exports only metrics produces no records.
- **Idempotent.** A record's id is a hash of the sender and the event's identity, so the same event sent again is stored once, across replicas.
- **Limits are per process.** The rate limits are in memory, so each replica applies them to its own traffic.
- **Forwarding.** With `AIPLEX_OTLP_FORWARD=true`, data AIPlex does not record is forwarded to the operator's collector with the verified identity added as resource attributes; the sender's `Authorization` header is never forwarded.
