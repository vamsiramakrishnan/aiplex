# Observability (OpenTelemetry)

`aiplex-api` emits OpenTelemetry traces and metrics. Traces and metrics are
exported over OTLP only when you configure an endpoint; Prometheus text is served
at `GET /metrics` either way. The code is in [`pkg/telemetry`](../pkg/telemetry),
and every span, attribute and metric name is in one file,
[`pkg/telemetry/semconv.go`](../pkg/telemetry/semconv.go).

The GenAI and MCP semantic conventions that AIPlex follows have **Development**
status. Their names have changed between releases and will change again. AIPlex
records the revision it follows (the `open-telemetry/semantic-conventions-genai`
commit `e6658f14c9d9ea0760825f029cb82e7673f48fab` of 2026-11-06, and core
conventions v1.45.0 for HTTP and `error.type`). A rename is a change to that one
file, and it will be listed in the release notes. Dashboards built on these names
may need updating when that happens.

## What is emitted

| Signal | What |
| --- | --- |
| SERVER span | One per HTTP request. |
| CLIENT span | One per managed model, tool or agent exchange (the same exchanges the [usage ledger](usage-ledger.md) records). It is a child of the request's SERVER span. For a routed access-point model, one per attempt, under an INTERNAL `aiplex.route` span (see [below](#routing-and-the-response-cache)). |
| Trace context | `traceparent` and `tracestate` on upstream model, tool and agent requests, taken from the CLIENT span. |
| Ledger correlation | Every [usage record](usage-ledger.md) carries `trace_id` and `span_id`. For a managed record, `span_id` is the CLIENT span; for a routed access-point model, the `aiplex.route` span. |
| Log correlation | Every `request started` log line carries `trace_id`. API error bodies carry `trace_id`. |
| Metrics | Request and exchange durations, token counters, cost, unknown cost and usage, quota denials and errors. See [metrics](#metrics). |

When export is off, trace and span identifiers are still generated. Ledger
records, logs and error bodies therefore correlate without a collector. Nothing
is sent anywhere.

### SERVER span

The span name is `{method} {route}`, where the route is the mounted route
pattern, for example `POST /ap/{apid}/*` or `GET /api/v1/usage/records`. When no
route matched, the name is `{method}` only. The raw path and the query string are
never recorded, because a query string can carry a key.

| Attribute | Value |
| --- | --- |
| `http.request.method` | The method, or `_OTHER` for a non-standard one. |
| `http.route` | The route pattern. |
| `http.response.status_code` | The status sent to the caller. |
| `error.type` | The status code, only when it is 5xx. The span status is then `Error`. |

### CLIENT span

The CLIENT span covers a managed exchange from admission to the end of the
upstream response. For a stream or a WebSocket session, it ends when the stream
or session ends. Its start and end are the ledger record's `started_at` and
`finished_at`.

A refused exchange (no grant, quota denial) still has a CLIENT span. Its
`aiplex.status` is `denied` and its `error.type` is set, so the ledger record's
`span_id` always points at a span. No upstream request was made in that case.

| Plane | Span name |
| --- | --- |
| Models | `{gen_ai.operation.name} {gen_ai.request.model}`, for example `chat gpt-4o-mini` or `generate_content gemini-2.5-flash`. |
| Tools (MCP) | `{mcp.method.name} {gen_ai.tool.name}`, for example `tools/call echo`, or `tools/list`. |
| Agents | `invoke_agent`. |

Attributes follow. **An unknown value is an absent attribute, never `0`.** A
token count that the provider did not report is not on the span. A reported zero
is `0`.

| Attribute | Value |
| --- | --- |
| `gen_ai.operation.name` | `chat`, `generate_content`, `embeddings`, `text_completion`, `realtime_inference`, `fetch_response`, `execute_tool` or `invoke_agent`. A native operation without a well-known value keeps its native ID (for example `models.countTokens`). |
| `gen_ai.provider.name` | `openai`, `anthropic`, `aws.bedrock`, `azure.ai.openai`, `gcp.gemini` or `gcp.vertex_ai`. Other providers keep AIPlex's provider ID (for example `openai-compatible`). Not set for MCP. |
| `gen_ai.request.model` | The model ID, set only when the surface recognised it. |
| `gen_ai.response.model` | The model version the provider reported. |
| `gen_ai.request.stream` | Model plane only: `true` for a streamed response. |
| `gen_ai.response.time_to_first_chunk` | Streamed model exchanges only: seconds from the start of the exchange to the first bytes written to the caller. |
| `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`, `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.cache_write.input_tokens`, `gen_ai.usage.reasoning.output_tokens` | The ledger record's normalized counters. `input_tokens` includes cached tokens, as in the ledger. |
| `mcp.method.name` | `tools/call` or `tools/list`. |
| `gen_ai.tool.name` | The upstream tool name. |
| `error.type` | The ledger `error_class` (for example `rate_limited`, `upstream_error`, `quota_budget_exhausted`), only when the status is not `ok`. The span status is then `Error`. |
| `aiplex.plane` | `llmplex`, `mcplex` or `a2aplex`. |
| `aiplex.operation` | The native operation ID. |
| `aiplex.provider` | AIPlex's own provider label. |
| `aiplex.resource.id` | The model surface, tool connection or agent connection. |
| `aiplex.access_point.id`, `aiplex.caller.kind` | Set when the exchange entered through an access point. |
| `aiplex.principal.id`, `aiplex.actor.id`, `aiplex.client.id` | The verified caller (canonical IDs only; no email or display name). |
| `aiplex.request.id` | The request ID. |
| `aiplex.status` | The ledger status: `ok`, `error`, `denied` or `unknown`. |
| `aiplex.response.status_code` | The HTTP status AIPlex sent to the caller. This is not necessarily the upstream's status. |
| `aiplex.streaming`, `aiplex.session` | Tool and agent planes: streamed response. Any plane: WebSocket session. |
| `aiplex.usage.total_tokens` | The ledger's total, when known. |
| `aiplex.usage.bound` | `lower_bound`, `ambiguous` or `partial` when the counters are not final. See [bounded usage](usage-ledger.md#bounded-usage). |
| `aiplex.usage.record_id` | The usage ledger record ID. |
| `aiplex.cost.basis` | `settled`, `estimated` or `unknown`. |
| `aiplex.cost.amount`, `aiplex.cost.currency` | The exact cost as a decimal string (for example `0.000014`) and its currency, only when the cost is known. |
| `aiplex.quota.attempt_id` | The QuotaPlex attempt. |
| `aiplex.quota.decision` | `denied` for a quota refusal; `admitted` when an attempt exists and quota did not refuse. |
| `aiplex.hooks.decision` | The exchange's [request-hook](request-hooks.md) decision (`deny`, `error`, `transform`, `allow` or `skipped`), when a hook ran. |
| `aiplex.hooks.count` | The number of hook runs on the exchange (both phases). |
| `aiplex.hooks.denied_rule` | The rule identifier of a denying hook (an operator-chosen identifier such as `private_key`, never the matched text). |
| `aiplex.hooks.fail_open_used` | `true` when a `fail_open` hook failed and the exchange continued without its decision. |
| `aiplex.tool_policy.decision` | `tool_policy_denied` or `tool_policy_error`, on a tool call a [policy rule](tool-policy-rules.md) refused. |
| `aiplex.tool_policy.rule` | The identifier of the rule that decided (an operator-chosen identifier, never arguments); absent for the default deny. |

The span ends after the cost is resolved (settlement or price), on the ledger's
background writer, with the exchange's own finish time. It can therefore arrive
at the collector slightly after the SERVER span.

### What is never recorded

Prompts, completions, system instructions, tool definitions, tool arguments and
tool results are never recorded. The opt-in content attributes of the conventions
(`gen_ai.input.messages`, `gen_ai.tool.call.arguments` and others) are not
implemented. `Authorization`, provider key headers (`x-api-key`,
`x-goog-api-key`, `api-key`), cookies, query strings and request bodies are
never recorded. Neither are email addresses or display names. Every attribute
comes from the finished usage ledger record or the route pattern, and the record
itself holds no payload.

Tests plant a secret in the prompt, the caller's token, the query string and a
provider key header, and fail if it appears in any exported span:
`internal/api/telemetry_spans_test.go` and `tests/e2e/telemetry_test.go`.
Request hooks add no content either: `internal/api/hooks_test.go` plants a marker
in a prompt and a tool argument and fails if it appears in a span, ledger record,
log line, metric label or error body, whether the hook masked it or denied it.

## Trace context

- A valid inbound W3C `traceparent` is continued, and its `tracestate` is kept.
  An invalid or absent one starts a new trace. Only W3C Trace Context is read;
  `baggage` is neither read nor forwarded.
- With the default sampler (`parentbased_always_on`), a caller's sampled flag
  is honoured. A caller can therefore ask for its requests to be sampled. Set
  `OTEL_TRACES_SAMPLER` (for example `parentbased_traceidratio` with
  `OTEL_TRACES_SAMPLER_ARG=0.1`) to bound that.
- **Upstream propagation is on by default.** Model providers, MCP servers and
  agent runtimes receive `traceparent` (and the caller's `tracestate`, if any)
  for the CLIENT span. A third-party provider therefore learns a random trace ID
  and span ID per request, and the caller's `tracestate` if one was sent. It
  learns nothing about the tenant or caller. The default is chosen so that
  traces continue into self-hosted models, MCP servers and agents.
  `AIPLEX_TRACE_PROPAGATE_UPSTREAM=false` turns it off; AIPlex then strips
  `traceparent` and `tracestate` from every upstream request. The ledger and
  telemetry are unchanged.
- MCP: for a managed `tools/call`, trace context goes in the HTTP headers and also in
  `params._meta.traceparent` (and `tracestate`), which the MCP specification reserves for
  OpenTelemetry trace context
  ([modelcontextprotocol.io/specification/draft/basic/index](https://modelcontextprotocol.io/specification/draft/basic/index),
  "General fields", `_meta`). The values are the exchange's CLIENT span, the same as the
  header. The edit changes only that `_meta` object: AIPlex splices text into the caller's
  JSON-RPC body and copies every other byte, so large integers, floats, escapes, key order and
  spacing are unchanged (tested with 30-digit integers, `1.0e+100`, surrogate pairs and a
  nested `_meta` inside arguments). A `traceparent` or `tracestate` the caller put in
  `_meta` is replaced; a caller's `baggage` is removed, because baggage is neither read nor
  forwarded. With `AIPLEX_TRACE_PROPAGATE_UPSTREAM=false` they are removed from `_meta`
  as they are from the headers. Other `_meta` keys (`progressToken`) are kept on the direct
  `/api/v1/mcp/connections/{id}/invoke` path. Through an access point the call is rebuilt
  from `name` and `arguments`, so only the trace context is in `_meta`. A `_meta` that is not
  an object, or a duplicated key, is refused (`400`). `tools/list` requests are built by
  AIPlex and carry no `_meta`.
- Tool credential configuration still refuses `traceparent` and `tracestate` as
  credential header names (`pkg/toolcredential`). That rule is about operator
  credential configuration and does not affect propagation.

## Harness telemetry received over OTLP

AIPlex has an OTLP/HTTP receiver, so a coding harness can report what it did to the usage
ledger. It is separate from AIPlex's own export above.

| Route | Content |
| --- | --- |
| `POST /otlp/v1/logs` | Log events. Recognized usage events become `source=reported` ledger records. |
| `POST /otlp/v1/metrics` | Accepted, counted, not recorded. |
| `POST /otlp/v1/traces` | Accepted, counted, not recorded. |

Set the harness's `OTEL_EXPORTER_OTLP_ENDPOINT` to `<AIPlex URL>/otlp` (the exporter appends
`/v1/logs`) and send the AIPlex token as `Authorization: Bearer` in
`OTEL_EXPORTER_OTLP_HEADERS`. Both `application/x-protobuf` and `application/json` work, with
or without `Content-Encoding: gzip`.

**Identity.** The sender is authenticated like any other caller. The route policy is
`authenticated, tenant-scoped`: any verified caller of the tenant may report its own
telemetry, and nothing else. Tenant, principal, client and caller kind (`agent`, `user` or
`client`) come from the verified token only. Resource and record attributes (`user.email`,
`user.account_uuid`, `organization.id`, `session.id`, anything named `aiplex.*`) are untrusted
labels: only the documented subset is stored as `reported_attributes`, and none of them can
change who the record belongs to. Development mode accepts an unauthenticated sender on its
loopback listener, like every other route, and its records have no principal.

**One source of truth.** Per-request log events are the only signal turned into records.
Metrics (cumulative or delta counters) and traces (Claude Code's `llm_request` spans) describe
the same requests, so storing them as well would count a request twice; they are accepted and
dropped. A harness that exports only metrics therefore produces no ledger records.
`aiplex connect --with-telemetry` enables logs.

**What is mapped** (from the harnesses' documentation; see
[harness onboarding](harness-onboarding.md#observed-with-real-clients) for what has
been observed with a real client):

| Harness | Event | Ledger record |
| --- | --- | --- |
| Claude Code | `claude_code.api_request` | `model`, `input_tokens` + `cache_read_tokens` + `cache_creation_tokens` as `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens` as `cache_write_tokens`, cost `cost_usd_micros` (exact) or `cost_usd` with basis `reported`, `duration_ms`, `session.id`, `client_request_id`, `request_id` (kept as an attribute), `prompt.id`, `query_source`, `speed`, `effort` |
| Claude Code | `claude_code.api_error` | A record with status `error`, `error_class` `upstream_error` (`rate_limited` for 429), `status_code`, `duration_ms`, no usage and unknown cost. The error text is never stored. |
| Codex | `codex.sse_event` with `event.kind=response.completed` | `model`, `input_token_count`, `output_token_count`, `cached_token_count` as cache read, `cache_write_token_count`, `reasoning_token_count`, `conversation.id` as the session. Codex exports no cost, and no duration is used (the `duration_ms` on that event is not a request latency). |
| Gemini CLI | `gemini_cli.api_response` | `model`, `input_token_count`, `output_token_count`, `cached_content_token_count` as cache read, `thoughts_token_count` as reasoning, `total_token_count`, `status_code`, `duration_ms`, `session.id`, `prompt_id`. `tool_token_count` is not mapped. |
| Gemini CLI | `gemini_cli.api_error` | A record with status `error`, as for Claude Code. |

Every other event (prompts, tool results and decisions, startup, config) is accepted and not
recorded. A missing token count stays unknown; a negative one rejects that record
(`rejected_log_records` in the OTLP partial-success response, with a content-free reason).
Events whose time is in the future (by more than 10 minutes) or older than the ledger retains
are rejected.

**Idempotency.** A record's id is a hash of the sender and the event's identity (session,
timestamp, name, sequence, prompt and request ids), so the same event sent again, even to
another replica, is stored once. A retried export answers `200` as before.

**Limits and statuses.** Bodies are bounded (4 MiB as sent, 16 MiB decompressed,
`AIPLEX_OTLP_MAX_BODY_BYTES`), as are log records, metrics and spans per request (1000,
`AIPLEX_OTLP_MAX_RECORDS_PER_REQUEST`) and rates per principal (20 requests and 2000
records a second, `AIPLEX_OTLP_REQUESTS_PER_SECOND` and `AIPLEX_OTLP_RECORDS_PER_SECOND`; in
process, so per replica). Refusals use OTLP's status body (`google.rpc.Status` as protobuf or
JSON): `413` for too large, `429` with `Retry-After` for rate limits, `415` for an unsupported
content type or encoding, `400` for a body that is not an OTLP request, `401` and `403`
without a verified identity or tenant, `503` with `Retry-After` when the ledger cannot be
written (the exporter retries; the deterministic ids make the retry safe).

**Forwarding.** With `AIPLEX_OTLP_FORWARD=true`, what AIPlex does not record (unmapped log
events, all metrics and traces) is sent on to your collector (`OTEL_EXPORTER_OTLP_ENDPOINT`
or the per-signal endpoints, `http/protobuf`, the headers in `OTEL_EXPORTER_OTLP_HEADERS`).
The verified identity is added as resource attributes (`aiplex.principal.id`,
`aiplex.tenant.id`, `aiplex.caller.kind`, `aiplex.client.id`); any `aiplex.*` attribute the
sender supplied is removed first. The sender's `Authorization` header is never forwarded.
Forwarding is best effort (a bounded queue, a 5 second timeout) and never fails the sender's
export. Default off; a misconfiguration (no endpoint, `grpc`) stops startup. Do not point it
at AIPlex's own `/otlp`.

Counters: `aiplex.otlp.ingest` by signal, harness (`claude-code`, `codex`, `gemini-cli`,
`unknown`) and outcome (`recorded`, `duplicate`, `rejected`, `dropped`, `forwarded`,
`forward_failed`, `rate_limited`, `too_large`, `malformed`), and `aiplex.usage.tags_rejected`
by reason. Neither carries a principal, session or request id.

## Metrics

Metric names follow the conventions where one exists. Prometheus names
(`GET /metrics`) replace dots with underscores and add unit and `_total`
suffixes. For example, `gen_ai.client.inference.duration` is served as
`gen_ai_client_inference_duration_seconds`.

Attributes are bounded: `aiplex.plane`, `gen_ai.provider.name`,
`gen_ai.operation.name`, `gen_ai.request.model`, `mcp.method.name`,
`aiplex.access_point.id`, `aiplex.caller.kind` and `aiplex.status`, plus
`error.type`, currency and basis where noted. **No principal, request, record or
trace ID is ever a metric attribute**, and exemplars are off.

| Metric | Type | Unit | Notes |
| --- | --- | --- | --- |
| `http.server.request.duration` | histogram | s | Every request. Attributes: `http.request.method`, `http.route`, `http.response.status_code`, and `error.type` on 5xx. |
| `gen_ai.client.inference.duration` | histogram | s | Managed model exchanges, with `error.type` when not `ok`. |
| `gen_ai.client.inference.time_to_first_chunk` | histogram | s | Streamed model exchanges, when bytes were written. |
| `mcp.client.operation.duration` | histogram | s | Managed tool requests. |
| `gen_ai.client.invoke_agent.duration` | histogram | s | Managed agent invocations. |
| `gen_ai.client.inference.usage.input_tokens`, `.output_tokens`, `.cache_read.input_tokens`, `.cache_write.input_tokens`, `.reasoning.output_tokens` | counter | `{token}` | Model plane only, and only for counters that are known and final. |
| `aiplex.usage.cost` | counter | currency units | Known cost only, by `aiplex.cost.currency` and `aiplex.cost.basis`. This is a float approximation; the ledger keeps the exact amount. |
| `aiplex.usage.unknown_cost` | counter | `{exchange}` | Exchanges whose cost is unknown. They are not added to `aiplex.usage.cost`. |
| `aiplex.usage.unknown_usage` | counter | `{exchange}` | Exchanges with unknown usage (`aiplex.usage.bound="unknown"`) or non-final usage (`lower_bound`, `ambiguous`). Their tokens are not added to the token counters. |
| `aiplex.quota.denials` | counter | `{exchange}` | Exchanges refused by QuotaPlex admission, by `error.type`. A provider's own 429 is not counted here. |
| `aiplex.upstream.errors` | counter | `{exchange}` | Exchanges that ended in `error` or `unknown`, by `error.type`. This includes upstream errors passed through to the caller. |
| `aiplex.hooks.decisions` | counter | `{decision}` | One per [request-hook](request-hooks.md) run, by `aiplex.hook.type` (`redact`, `size_limit`, `http`, `policy`), `aiplex.hook.phase` (`request`, `response`) and `aiplex.hook.decision` (`allow`, `deny`, `transform`, `error`, `skipped`). No policy, hook or rule identifier is a label. |
| `aiplex.hooks.duration` | histogram | s | Duration of one hook run, with the same three attributes. |
| `aiplex.tool_policy.decisions` | counter | `{decision}` | One per rule-set evaluation on a tool call (the connection's and the access point's are separate evaluations), by `aiplex.tool_policy.decision` (`allow`, `deny`, `error`). No rule, tool, principal or connection is a label. |

`GET /metrics` serves one replica's view. Scrape every replica, or use OTLP
metrics export (each replica pushes its own).

### `/metrics` access

`GET /metrics` is a shared-secret internal route, like `/internal/*`. The
scraper sends `Authorization: Bearer <AIPLEX_METRICS_TOKEN>`. When
`AIPLEX_METRICS_TOKEN` is unset the route answers 503, so it fails closed.
Development mode without a token serves it to the local developer, as it serves
everything else on its loopback-only listener.

The public gateway does not route `/metrics`. Scrape the `aiplex-api` service
inside the cluster. The route is not public because metrics reveal access point and model names
and traffic volumes, and it is not role-based because a scraper would then
need an AIPlex identity token that expires.

## Configuration

AIPlex reads the standard OpenTelemetry environment variables. The SDK and the
OTLP exporters read most of them directly.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Collector base URL. Export is off unless this (or a per-signal endpoint) is set. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | empty | Per-signal endpoints. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` (and the per-signal `_PROTOCOL` variables) | `http/protobuf` | `http/protobuf` or `grpc`. With an endpoint set, any other value stops startup. |
| `OTEL_EXPORTER_OTLP_HEADERS` | empty | Collector headers (for example an API key). Values are never logged. |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | `aiplex-api` | Resource attributes. |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | `parentbased_always_on` | Sampling. |
| `OTEL_TRACES_EXPORTER`, `OTEL_METRICS_EXPORTER` | `otlp` | `none` turns a signal's export off. `prometheus` is accepted for metrics; `/metrics` is always served. |
| `OTEL_SDK_DISABLED` | `false` | `true` turns OTLP export off. Identifiers and `/metrics` remain. |
| `OTEL_BSP_*`, `OTEL_METRIC_EXPORT_INTERVAL` | SDK defaults | Batch queue size, export delay and interval. |
| `OTEL_ENDPOINT` | empty | Deprecated alias of `OTEL_EXPORTER_OTLP_ENDPOINT`, used only when the standard variable is unset. A warning is logged. |
| `AIPLEX_TRACE_PROPAGATE_UPSTREAM` | `true` | `false` strips trace context from upstream requests. Any value other than a boolean stops startup. |
| `AIPLEX_METRICS_TOKEN` | empty | Bearer secret for `GET /metrics`. |
| `AIPLEX_TRACE_URL_TEMPLATE` | empty | A trace viewer link for the Console, for example `https://tempo.example/trace/{trace_id}`. Must contain `{trace_id}` and be an `http` or `https` URL without credentials; anything else stops startup. The Console shows "Open in trace viewer" on a record. |
| `AIPLEX_OTLP_FORWARD` | `false` | Forward the harness telemetry AIPlex does not record to your collector. See above. |
| `AIPLEX_OTLP_MAX_BODY_BYTES`, `AIPLEX_OTLP_MAX_RECORDS_PER_REQUEST`, `AIPLEX_OTLP_REQUESTS_PER_SECOND`, `AIPLEX_OTLP_RECORDS_PER_SECOND` | 4 MiB, 1000, 20, 2000 | Receiver bounds. |
| `AIPLEX_TAG_KEYS_ALLOWED`, `AIPLEX_TAG_MAX_VALUES` | empty, 50 | Caller-supplied record tags (see the [usage ledger](usage-ledger.md#tags)). |
| `AIPLEX_FEEDBACK_COMMENTS` | `false` | Accept free-text comments on record feedback. |

The startup log says what is in effect: `telemetry configured
traces_exported=... metrics_exported=... upstream_trace_propagation=...`.

Export is designed not to fail or block a request. Spans go through a bounded batch queue
(2048 by default; spans are dropped when it is full) and are exported on a
background goroutine. Metrics are exported periodically on another. The first
export error is logged once, with any configured header values redacted, and
later errors are not logged. Whether that holds under a slow or failing collector
has not been measured (see the limits below).

## Turn it on locally

```bash
# Any OTLP/HTTP collector on 4318 works; for example Jaeger all-in-one, then
# open its UI on :16686.
aiplex dev --otel http://127.0.0.1:4318
```

`--otel` sets `OTEL_EXPORTER_OTLP_ENDPOINT` for the local API. `OTEL_*`
variables already in your environment are passed through unchanged (for example
`OTEL_EXPORTER_OTLP_PROTOCOL=grpc` with port 4317). The `aiplex dev` summary
shows the endpoint. Every usage record shows its `trace_id` and `span_id` in
`aiplex usage get <id>` (and in `-o json`), so you can look a request up in the
trace UI.

Without a collector, `curl -s http://127.0.0.1:8080/metrics` shows the metrics.
Development mode needs no token.

A coding harness can send its own telemetry to the same collector:
`aiplex connect <access-point> --client <client> --write --with-telemetry <endpoint>`
sets the client's OpenTelemetry export (Claude Code, Codex, Gemini CLI). That is the
client's telemetry, not AIPlex's. To read it into the usage ledger instead, send it to AIPlex
itself: `--with-telemetry aiplex` (see
[harness telemetry received over OTLP](#harness-telemetry-received-over-otlp) and
[harness onboarding](harness-onboarding.md)).

## Turn it on in GKE (Helm)

```yaml
otel:
  enabled: true              # sets OTEL_EXPORTER_OTLP_* on the API Deployment
  endpoint: ""               # empty: the chart's collector, http://otel-collector.aiplex-system:4318
  protocol: http/protobuf
  propagateUpstream: true    # false strips traceparent from upstream requests
  collector: true            # run the chart's collector (Cloud Trace and Cloud Monitoring)
  image: otel/opentelemetry-collector-contrib:0.164.0   # pinned
api:
  metricsTokenSecret:
    name: aiplex-metrics     # an existing Secret; the chart never holds the value
    key: token
```

Usage ledger settings are under `usage:` in the chart values: `traceUrlTemplate`,
`feedbackComments`, `tagKeysAllowed`, `tagMaxValues` and `otlp.*` (receiver bounds and
`forward`), each passed to the API as the `AIPLEX_*` variable of the same name. The Gateway
routes `/otlp/` to the API. The GKE Gateway has no request-body-size setting, so none is
configured; the receiver's own bound (4 MiB as sent by default) applies, and the load
balancer's limits apply in front of it. Feedback collections need Firestore TTL on
`usage_feedback_latest` and `usage_feedback_events` (`firestore.tf`). The chart templates were
not rendered here (no `helm` available).

The chart's collector exports to Cloud Trace and Cloud Monitoring in
`global.projectId`. It runs as its own `otel-collector` Kubernetes service
account (no mounted token, non-root, read-only root filesystem), mapped by
Workload Identity to the `aiplex-otel-collector` Google service account.
Terraform creates that account with `roles/cloudtrace.agent` and
`roles/monitoring.metricWriter` on the project when `otel_collector = true`
(the default, matching the chart's `otel.enabled` and `otel.collector`
defaults); both roles can only be granted at project level. It does not use the
API's identity. To use a collector you already run, set `otel.endpoint` to its
URL and `otel.collector: false`, and `otel_collector = false` in
`terraform.tfvars`. See [Google Cloud permissions](gcp-permissions.md).

The raw manifests in `deploy/k8s` carry a `PROJECT_ID` placeholder. Substitute it
before applying (see the comment at the top of `deploy/k8s/otel-collector.yaml`).

## Limits and what has not been validated

- The OTLP receiver is exercised by unit tests on payloads built from the harnesses'
  documentation, an end-to-end test against `aiplex dev`, and a real Claude Code 2.1.322
  exporting to a local `aiplex dev` (see
  [harness onboarding](harness-onboarding.md#observed-with-real-clients)). Codex and
  Gemini CLI mappings have not been run against the real clients. The receiver has not been
  measured under load.
- Export has been verified only against the in-process OTLP/HTTP receiver in
  `tests/e2e/telemetry_test.go`. It has **not** been run against a real
  collector, Jaeger, Cloud Trace or Cloud Monitoring. The gRPC exporter path has
  no end-to-end test. The Helm templates are not rendered by the repository's checks.
- The conventions are in Development status, and names may change (see the top
  of this page).
- The CLIENT span covers admission as well as the upstream call, so its duration
  is the ledger's `latency_ms` and not only the provider's time.
- Agent invocations use the GenAI `invoke_agent` client convention (matching
  `docs/gen-ai/client-invoke-agent.md`: operation name, provider name, usage attributes and
  `gen_ai.client.invoke_agent.duration` match; the span carries `gen_ai.agent.id`, the agent
  connection id). The convention names the span `invoke_agent {gen_ai.agent.name}` when the
  name is known; AIPlex has no agent name and uses `invoke_agent`, which the convention
  allows. The A2A protocol conventions (`docs/gen-ai/a2a.md`: `a2a.method.name`,
  `a2a.task.id`, ...) describe A2A protocol messages and are not emitted, because the managed
  agent connections call native agent runtimes rather than A2A endpoints.
- `mcp.session.id` is never set: managed tool execution is stateless and opens no
  MCP session.
- The token counters (`gen_ai.client.inference.usage.*`) are told apart by
  `gen_ai.operation.name`, so they cover model exchanges (`chat`, `generate_content`, ...) and
  agent exchanges (`invoke_agent`) when the ledger record has known, final usage. An agent
  runtime that reports no usage adds nothing to the counters and is counted in
  `aiplex.usage.unknown_usage`. MCP exchanges have no tokens. The conventions define no
  `invoke_agent`-specific token metric; the counters are the generic ones.
- Time to first chunk is measured to the first bytes AIPlex writes to the
  caller, not to the first upstream byte.
- Upstream model requests are made by AIPlex's own HTTP client. No HTTP-level
  child span is created for an upstream request; the CLIENT span covers it. A
  routed access-point model has one CLIENT span per attempt (see
  [routing](#routing-and-the-response-cache)); a tool call's transport retry
  stays inside its one CLIENT span.
- No performance measurement of the telemetry overhead has been recorded.

## Routing and the response cache

An exchange of a routed access-point model ([reliability and
routing](reliability-and-routing.md)) has an INTERNAL span named
`aiplex.route {model}` under the request's SERVER span. It covers the whole
exchange, waits between attempts included, and is the span the ledger record's
`span_id` names. Each attempt that reached the managed path has its own CLIENT
span under it, with the attributes above for its backend. Only the attempt whose
response was delivered records the exchange metrics (durations, tokens, cost);
the other attempts' CLIENT spans are named `attempt {operation}` and add no
exchange metrics. A backend that was skipped (breaker open, not granted, priced
out) has no span; it is in the record's `attempts`.

| Attribute | Span | Value |
| --- | --- | --- |
| `aiplex.routing.strategy` | route | `priority`, `weighted` or `lowest_cost`. |
| `aiplex.routing.attempts` | route | Number of entries in the record's `attempts`. |
| `aiplex.routing.backend` | route, attempt | The backend surface that served (route) or was tried (attempt). Spans are operator data; this is never a metric label. |
| `aiplex.cache.status` | route | `hit`, `miss` or `bypass`, when the model has a cache. |
| `aiplex.routing.attempt` | attempt | The attempt's number on its backend (1 is the first). |
| `aiplex.routing.outcome` | attempt | The attempt's outcome class. |
| `aiplex.routing.breaker_state` | attempt | The backend's breaker state when the attempt was admitted. |
| `aiplex.routing.retry_after_ms` | attempt | The provider's `Retry-After`, when it sent one on a failure. |

| Metric | Type | Unit | What |
| --- | --- | --- | --- |
| `aiplex.routing.attempts` | counter | `{attempt}` | One per attempt (skips included), by `aiplex.routing.outcome`. |
| `aiplex.routing.retries` | counter | `{retry}` | Retries on the same backend, by the outcome that caused them. |
| `aiplex.routing.fallbacks` | counter | `{fallback}` | Moves to the next backend, by the outcome that caused them. |
| `aiplex.routing.breaker.transitions` | counter | `{transition}` | Breakers entering `aiplex.routing.breaker_state` (`closed`, `open`, `half_open`). |
| `aiplex.routing.breakers` | gauge | `{backend}` | Backends tracked by this process, by breaker state. Per process. |
| `aiplex.cache.lookups` | counter | `{lookup}` | Response-cache decisions by `aiplex.cache.status`. |

Labels are bounded sets; no backend, model, principal or access point is a
metric label.

The span tree of a routed exchange is therefore SERVER `POST /ap/{apid}/*` →
INTERNAL `aiplex.route {model}` → one CLIENT span per attempt. Each attempt's
upstream request carries that attempt's CLIENT span in `traceparent`. The
record's `trace_id` is the request's trace (the caller's, when it sent a valid
`traceparent`) and its `caller_span_id` is the caller's span, as for any managed
record, so a harness's reported records correlate with a routed record exactly as
with an unrouted one.
