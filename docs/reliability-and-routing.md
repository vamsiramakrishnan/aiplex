# Reliability and routing

An [access point](access-points.md) model can be served by more than one
backend. AIPlex then retries a failed request on the same backend, falls over
to the next backend, keeps a circuit breaker per backend, can choose the
cheapest backend first, and can answer an exact repeat of an earlier request
from a response cache. This page states the safety rules exactly, what each
setting does, what is recorded, and the limits. Read the
[limits](#limits-and-what-has-not-been-validated) before relying on it.

Nothing on this page changes a model entry that has only `surface_ref` and
`native_model`: such a model is served exactly as before. Routing applies to a
model with a `backends` list, a `routing` block or a `cache` block.

## Configuration

```yaml
id: ide
caller_kinds: [user, agent]
models:
  - name: gpt                       # what callers send as "model"
    backends:                       # tried in this order (strategy priority)
      - {surface_ref: openai, native_model: gpt-4o-mini}
      - {surface_ref: azure-east, native_model: gpt-4o-mini, priority: 1}
    routing:
      strategy: priority            # priority (default) | weighted | lowest_cost
      retry: {}                     # same-backend retries: off unless present
      fallback: {}                  # moving to the next backend: on by default
      breaker: {failure_threshold: 5, open_ms: 30000}
      deadline_ms: 30000
    cache: {ttl: 10m}               # exact-match response cache: off unless present
```

`aiplex access-points create` builds the common cases from flags:

```bash
aiplex access-points create ide \
  --backend gpt=openai:gpt-4o-mini --backend gpt=azure-east:gpt-4o-mini \
  --retry gpt --cache gpt=10m
aiplex access-points routing ide          # live breaker state (operators)
aiplex access-points purge-cache ide      # drop cached answers (admins)
```

### `backends`

| Field | Default | Meaning |
| --- | --- | --- |
| `surface_ref` | required | A saved model surface of the tenant. |
| `native_model` | required | The model the surface is asked for. It may differ per backend; only the model name in the request is rewritten. |
| `weight` | 1 | Share of first attempts under `weighted` (0 to 1000; 0 means only ever a fallback). At least one backend needs a weight above 0. |
| `priority` | 0 | Order under `priority` (lower first; list order within a priority) and tie-break under `lowest_cost`. |

At most 8 backends; a backend (surface and native model) may be listed once.
A model uses either `surface_ref`/`native_model` or `backends`, not both.

**Every backend must speak the caller's wire protocol natively.** AIPlex does
not translate between protocols. When the access point is saved, each backend's
served client wires (OpenAI Chat Completions, Anthropic Messages, GenAI, and so
on) are compared with the first backend's; a backend that differs is refused
with a message that names it and both wire lists, for example:

```text
model "gpt" backend 2 (emb/m) serves openai-embeddings, but backend 1 (a/m) serves openai-chat-completions: every backend of a model must speak the same client wire protocols natively (AIPlex does not translate between protocols)
```

### `routing`

| Field | Default | Meaning |
| --- | --- | --- |
| `strategy` | `priority` | `priority`, `weighted` (one weighted random pick first, then the others in priority order) or `lowest_cost` (below). |
| `retry` | absent: no retries | Present (even `{}`): retries on the same backend with the defaults below. |
| `retry.max_attempts` | 2 | Attempts per backend, the first included (1 to 5). |
| `retry.on` | `connect_error, rate_limited, overloaded, unavailable` | Outcome classes retried. May add `server_error` (500), `bad_gateway` (502), `gateway_timeout` (504), `request_timeout` (408). |
| `retry.base_backoff_ms`, `retry.max_backoff_ms` | 200, 2000 | Exponential backoff from the base, capped, with equal jitter (half fixed, half random). |
| `retry.max_retry_after_ms` | 5000 | The longest provider `Retry-After` AIPlex will wait for on the same backend (below). |
| `fallback.disabled` | false | Only the first chosen backend is tried. |
| `fallback.on` | `connect_error, rate_limited, overloaded, unavailable, capacity_refused` | Outcome classes that move to the next backend. May add the opt-in retry classes. |
| `fallback.on_quota_denied` | `fail` | `next_backend` lets a QuotaPlex denial on one backend fall through to the next (below). |
| `breaker.failure_threshold`, `breaker.open_ms` | 5, 30000 | Consecutive failures that open a backend's breaker, and how long it stays open before one probe. `breaker.disabled: true` turns it off. |
| `lowest_cost.input_weight`, `lowest_cost.output_weight` | 3, 1 | Weighting of input and output token rates. |
| `lowest_cost.require_price` | false | Exclude backends whose price is unknown instead of trying them last. |
| `deadline_ms` | 30000 | From the start of the exchange, the time within which a retry or fallback may still start (at most 10 minutes). Further bounded by the caller's own context. It never cuts an attempt that is already running; that attempt is bounded by the gateway's stream duration as before. |

Unknown fields are refused at every level.

## Safety rules

An attempt's outcome is classified when the gateway writes its status, before
any byte of that attempt reaches the caller. A failure that may be retried or
fall over is **held back** (its status, headers and body are buffered, up to
1 MiB) and never forwarded. A success, or a failure that ends the exchange, is
delivered and streamed through. So **nothing is retried or failed over once a
byte has been sent to the caller**: a stream that breaks after its first byte
is not repeated.

| Outcome class | What it is | Same-backend retry | Next backend | Breaker |
| --- | --- | --- | --- | --- |
| `connect_error` | The connection to the provider could not be established, so no request byte was written (proven: the transport failed before a connection was obtained). | Default | Default | Failure |
| `rate_limited` | Upstream 429. | Default | Default | Failure |
| `overloaded` | Upstream 529 (Anthropic's overloaded status). | Default | Default | Failure |
| `unavailable` | Upstream 503. | Default | Default | Failure |
| `server_error` | Upstream 500. | Opt-in | Opt-in | Failure |
| `bad_gateway` | Upstream 502. | Opt-in | Opt-in | Failure |
| `gateway_timeout` | Upstream 504. | Opt-in | Opt-in | Failure |
| `request_timeout` | Upstream 408. | Opt-in | Opt-in | Failure |
| `transport_error` | The request was sent (or may have been), then the connection failed before a response was read. | Never | Never | Failure |
| `upstream_error` | Any other upstream status (400, 401, 403, 404, 422, ...). | Never | Never | No effect |
| `capacity_refused` | AIPlex's [capacity gate](model-providers.md#capacity-policy) refused the backend (`CAPACITY_COOLDOWN`, `CAPACITY_SHED`). The provider was not called. | Never | Default | No effect |
| `quota_denied` | QuotaPlex refused admission (`QUOTA_*`, 429 or 403). The provider was not called. | Never | Only with `on_quota_denied: next_backend` | No effect |
| `not_granted` | The caller lacks the model or surface grant of this backend. The provider was not called. | Never | Skipped (always) | No effect |
| `breaker_open` | The backend's breaker is open. Not called. | — | Skipped (always) | — |
| `refused` | Any other AIPlex refusal: a hook denial, a body the surface refuses, an ownership check, an observation or settlement failure after the provider answered. | Never | Never | No effect |
| `body_mismatch` | The backend needs a request body that differs from the first attempt's in more than the model name, so the hooked body cannot be reused (see [hooks](#request-and-response-hooks)). Not called. | Never | Skipped | No effect |
| `backend_unavailable` | The backend's surface could not be read, or `require_price` excluded it. Not called. | — | Skipped | — |
| `deadline_exceeded` | The deadline passed before the next attempt could start. | — | — | — |

The defaults retry only signals that say the request was not executed (no
connection, 429, 503, 529). Retrying 500, 502, 504 or 408 is opt-in because the
provider may already have run the request.

**Stateful requests never fall over to a different backend**, and are retried
on the same backend only after a `connect_error` (provably unsent). A request is
stateful when:

- the operation catalogue marks it stateful or it is a WebSocket session: the
  operation tables in `pkg/modelapi` mark Responses API create, retrieve,
  cancel and delete (OpenAI, Azure OpenAI, Vertex), batches (OpenAI, Anthropic
  Message Batches, Gemini, Vertex), files (OpenAI), cached contents (Gemini,
  Vertex), Interactions, long-running prediction operations, tuning jobs, and
  the WebSocket sessions (OpenAI Realtime, Responses WebSocket mode, Gemini and
  Vertex Live); or
- the body continues or creates provider-held state: `previous_response_id`,
  `conversation`, `cachedContent`/`cached_content`, `container`,
  `previous_interaction_id`, or a Responses request without `store: false`
  (Responses are stored by default).

A follow-up that names a stored response or cached content goes only to the
backend whose surface owns it (the ownership record AIPlex keeps when the
resource was created). Batches, files and other resource routes are not routed
at all: they keep using the existing access-point resource handlers.

A stateful request may still skip a backend it was never sent to
(`not_granted`, `breaker_open`), but once one backend has been asked, no other
is.

### Retry-After

A provider's `Retry-After` (seconds or an HTTP date) or `Retry-After-Ms` is
honoured, never shortened: the retry waits at least the hint (or the backoff,
if longer). A hint above `retry.max_retry_after_ms`, or a wait that would not
leave the retry inside the deadline, means "do not retry here": AIPlex moves to
the next backend at once, or ends the exchange. The hint is recorded on the
attempt (`retry_after_ms`).

### Idempotency and double billing

Model generation is not idempotent for billing. A retried or failed-over request
**may be billed twice** by providers if the first attempt actually ran, for
example when a 503 or a timeout came back after generation started. The
defaults limit retries to "not executed" signals, but a provider can still have
done work before answering 429, 503 or 529.

AIPlex sends no idempotency key to any provider, and **never forwards a
caller's `Idempotency-Key` to any provider**. Checked on 2026-11-10:

- OpenAI: the API reference documents no idempotency header for Chat
  Completions or Responses, and the official Python SDK leaves its idempotency
  header unset (`_idempotency_header = None` in `openai/_base_client.py`; the
  OpenAI client does not override it). See
  <https://github.com/openai/openai-python/blob/main/src/openai/_base_client.py>.
- Anthropic: the errors page (<https://platform.claude.com/docs/en/api/errors>)
  documents 429, 500, 504 and 529 and SDK retries, and no idempotency key; the
  official Python SDK leaves its idempotency header unset.
- Amazon Bedrock Runtime: `InvokeModel` (and `Converse`) take no client request
  token
  (<https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html>);
  tokens exist only on control-plane and asynchronous operations such as
  `StartAsyncInvoke`.
- Gemini API: no idempotency or client-token parameter is documented for
  `generateContent` (<https://ai.google.dev/api/generate-content>); the
  response carries a `responseId` only. The request-body page was not
  available when this was checked, so this rests on the method reference.

The caller's `Idempotency-Key` keeps its existing meaning: it is QuotaPlex's
attempt identity. A retry on the same backend uses a key derived from it
(`aiplex-retry-` and a hash of the key, backend and attempt number), so each
retry is a new quota attempt while a client that replays the whole exchange
with the same key still maps onto the same attempts. A fallback to another
backend is a different quota attempt because the attempt identity includes the
surface.

## Circuit breakers

Each backend of each access-point model has a breaker (keyed by tenant, access
point, model name, surface and native model). After `failure_threshold`
consecutive breaker failures (table above) it opens: the backend is skipped
(`breaker_open`) for `open_ms`. Then exactly one request at a time is let
through as a probe (`half_open`); success closes the breaker, failure reopens
it. An outcome that is not the backend's fault (a refusal by AIPlex, a client
error, a skip) does not count either way.

**Breakers are in memory, per API process.** Replicas do not share them, and a
restart closes every breaker. When every backend is skipped, the caller gets 503
`NO_BACKEND_AVAILABLE` (in the caller's protocol, with `Retry-After: 1`).

`GET /api/v1/access-points/{id}/routing` (operators; CLI `aiplex access-points
routing <id>`) shows each backend's state, consecutive failures, when it will
half-open, and its last eight outcomes, for the process that answered.

## `lowest_cost`

Backends are ordered by their configured price for the provider and model the
ledger would record: the estimated cost of `input_weight` million input tokens
plus `output_weight` million output tokens under the tenant's
[model prices](usage-ledger.md#prices) (default weighting 3:1). Ties keep the
priority order. A backend whose price is unknown is **never assumed cheap**: it
is tried after every priced backend, or excluded with `require_price: true`
(recorded as `backend_unavailable` with reason `excluded_price_unknown`).
Prices in another currency than the cheapest priced backend's are not compared
and are ordered with the unknown ones. Breakers still apply. Each attempt
records why its backend was in the order (`reason`: `priority`,
`weighted_pick`, `weighted_fallback`, `lowest_cost`, `price_unknown`,
`price_other_currency`, `state_owner`).

## Grants, quota and capacity per attempt

Each attempt is one full pass through the managed model path for its backend,
so each attempt has its own:

- **Grants.** The caller needs the grants of the backend actually used
  (`llm:model:{model}` and `llm:surface:{surface}:{operation}`). A backend the
  caller is not granted is skipped (`not_granted`) without contacting it and
  the next one is tried. A caller is never served by a backend it lacks grants
  for. When no backend is granted, the refusal is the one the first backend
  would give (403 naming that backend's missing grants, the same message as for
  a single-backend model); other backends are not named.
- **Capacity admission.** A backend's capacity gate refusal falls over to the
  next backend by default (capacity is about the backend).
- **Quota lease.** QuotaPlex admits each attempt separately (the lease's target
  is the backend's surface, provider, billing account and region). A quota
  denial is **final by default**: quota rules mostly limit the caller (per
  principal, per access point, per tenant budgets), and trying another backend
  would only find another way around the caller's limit. Rules that match
  per-backend dimensions (`resource_id`, `provider`, `billing_account`,
  `region`) can make a denial specific to one backend; set
  `fallback.on_quota_denied: next_backend` only when your policy's limits are of
  that kind. A failed attempt settles as before: a documented not-processed
  rejection (429 everywhere, 529 for Anthropic, and the other statuses listed
  under [provider rejections](model-providers.md#provider-rejections)) as one request with zero consumption, and a
  `connect_error` as **zero** (no request reached the provider). A 503 or any
  other failure after dispatch settles as uncertain, as it always has.
  Request-count quotas therefore see each attempt that was dispatched.
- **Credential.** Each backend's surface supplies its own credential.

## Request and response hooks

[Request hooks](request-hooks.md) run **once** per exchange, on the first
attempt that reaches them, with the policies of every backend's surface
together (scopes add, never remove). Later attempts dispatch the same hooked
body with only the model name rewritten, without running the hooks again. If a
backend needs a request body that differs from the first attempt's in more than
the model (for example one surface injects `stream_options.include_usage` and
another does not), that backend is skipped (`body_mismatch`) rather than sent an
unhooked body.

Response hooks run once, on the response that is delivered. A held-back
failure is a provider error body, which hooks never inspect.

Order of one routed exchange:

1. Identity, tenant, access point admission.
2. Backend order (strategy, breakers, state owner).
3. For each attempt: the backend's grants, capacity admission, request hooks
   (first attempt only), quota lease, cache lookup (first attempt only),
   credential, dispatch.
4. Usage observation, then response hooks on the delivered response.

## Response cache

Off unless the model has a `cache` block. **Exact match only**; there is no
semantic or similarity cache.

| Field | Default | Meaning |
| --- | --- | --- |
| `ttl` | required | A Go duration, 1s to 24h. |
| `scope` | `principal` | `principal`: an answer is only ever served to the principal whose request stored it. `access_point`: shared by every caller of the access point. |
| `max_entry_bytes` | 1048576 | The largest response kept (at most 4 MiB). |

**`scope: access_point` shares answers between callers.** One caller's
prompt-and-answer pair is returned to anyone who sends the same request through
the access point. Use it only for prompts that hold no personal or confidential
data, and never on an access point shared by people who must not see each
other's answers. It must be set explicitly.

The key is a SHA-256 over the tenant, the access point, the model name callers
use (not a backend's native name), the client wire protocol, the client path,
the scope (and the principal for `principal`), the request body **after request
hooks** in canonical form (sorted keys, no insignificant whitespace, the model
replaced by the access-point name), and the `anthropic-version` and
`anthropic-beta` headers. No request content is stored outside the cache entry
itself.

The lookup runs after identity, grants, capacity admission, the request hooks
and the quota lease, so a hit still passes all of them.

**What is cached.** Only a request that is:

- on OpenAI Chat Completions, Anthropic Messages, GenAI `generateContent`,
  Bedrock Converse or OpenAI embeddings (Responses API requests are stateful
  and always bypass);
- not streamed (`"stream": true`, a stream operation or a session bypasses);
- not stateful (above);
- free of per-user identifiers the provider must receive (`user`,
  `safety_identifier`, `metadata.user_id`);
- free of provider-executed tools (any tool that is not a caller-run function:
  web search, code execution, Gemini `googleSearch`, and so on);
- not sent with `Cache-Control: no-store` (bypass) or `no-cache` (no lookup,
  but the answer may be stored);

and whose response is a complete `200` JSON answer, within `max_entry_bytes`,
with **no tool or function call** anywhere in it (and no Anthropic
`pause_turn`). The provider's answer is stored as it came, before response
hooks; a response the response hooks deny is never stored, and on every hit the
response hooks run again for that caller.

**A hit** answers without calling a provider, with the stored status, body and
`Content-Type`. It:

- takes the quota lease and settles it as **one request and zero tokens**
  (request-count limits apply; token quotas are not charged);
- is recorded as its own ledger record with `cache.status: hit`, `usage` all
  zero (the provider produced nothing for this exchange), the original usage in
  `cached_response_usage`, and cost `{"basis": "cached", "amount": "0"}`.
  Summaries count it in `cached_response_records`, never as provider usage or as
  unknown cost.

Every response of a cached model carries `X-AIPlex-Cache: hit`, `miss` or
`bypass`; the ledger's `cache.reason` says why a request bypassed
(`streaming`, `stateful`, `no_store`, `user_identifier`, `server_tools`,
`operation`, `size`, `no_principal`) or why a miss was not stored
(`not_stored_tool_calls`, `not_stored_size`, ...).

**Storage** is an in-memory LRU bounded to 64 MiB per API process, behind an
interface (`routing.CacheStore`). Replicas do not share it and a restart empties
it; there is no Redis or other shared store in this increment.
`POST /api/v1/access-points/{id}/cache/purge[?model=]` (admins; CLI
`aiplex access-points purge-cache`) empties the access point's entries in the
process that answers.

## Errors

When every attempt fails, the caller gets the **last upstream error** in the
provider's own shape (backends of one model speak the same protocol), or an
AIPlex refusal in the caller's protocol. Every routed response carries
`X-AIPlex-Attempts`: the number of attempts that were dispatched to a provider
(0 for a cache hit). Responses never name a backend; neither do error messages,
except the existing missing-grant explanation for the first backend.

## Recording

One [ledger record](usage-ledger.md) per exchange: the attempt whose response
the caller got. Its `provider`, `resource_id`, `model_id`, usage and cost are
that backend's. It adds:

| Field | Meaning |
| --- | --- |
| `attempts` | Every attempt in order: `surface_ref`, `native_model`, `outcome` (class above), `upstream_status`, `retry_after_ms`, `duration_ms`, `breaker_state` (when admitted), `reason`. No bodies. |
| `backend` | `{surface_ref, native_model}` that served (absent when none did). |
| `routing_strategy` | The strategy. |
| `cache` | `{status, reason, scope}` when the model has a cache. |
| `cached_response_usage` | On a hit, the original usage (not provider usage of this exchange). |

`started_at` and `latency_ms` cover the whole exchange, waits included. **Usage
of failed attempts that the provider billed is unknowable**: a provider that ran
a request and then answered 503, or was cut off, reports no usage to AIPlex, so
it is in no record. The record shows such attempts as failures; it cannot say
what they cost.

A caller reading its own records (not an admin) sees attempts with their order,
outcomes and timings, but backends are numbered (`backend-1`, ...) instead of
named.

Traces: the record's `span_id` is an INTERNAL span `aiplex.route {model}`
covering the exchange. Each attempt that reached the managed path has its own
CLIENT span under it, with `aiplex.routing.attempt`, `aiplex.routing.outcome`,
`aiplex.routing.backend`, `aiplex.routing.breaker_state` and
`aiplex.routing.retry_after_ms`. Metrics `aiplex.routing.attempts`,
`aiplex.routing.retries`, `aiplex.routing.fallbacks`,
`aiplex.routing.breaker.transitions`, `aiplex.routing.breakers` and
`aiplex.cache.lookups` carry outcome classes, breaker states and cache results
only. See [observability](observability.md#routing-and-the-response-cache).

## Tools

For MCP tools there is no fallback and no cache. AIPlex sends a request once
more (after 100 ms) when its transport failed (no HTTP response was read) for:

- `tools/list` (it only reads);
- `tools/call` of a tool whose gateway-observed MCP annotation `readOnlyHint`
  is `true` (the same observation the connection `read_only` policy uses).

Every other call, any HTTP status and any JSON-RPC error is never retried. A
tool's `idempotentHint` is not consulted in this increment. The access point answers
`initialize` itself; nothing is sent upstream for it, so there is nothing to
retry.

## Trying it locally

`aiplex dev` starts a second mock provider (its URL is printed as `Mock B`;
its replies start with `[b]`). Both mocks fail on demand when the model name
contains `fault` and dash-separated options:

| Option | Effect |
| --- | --- |
| `s<status>` | Answer with this status (default 503). |
| `n<count>` | Fail only the first `count` requests for this model name and path. |
| `ra<seconds>` | Send `Retry-After`. |
| `d<ms>` | Wait before answering. |
| `dropbefore` | Close the connection without a response (a `transport_error`). |
| `dropafter` | Answer 200 `text/event-stream`, send one event, then close. |

For example a surface on the first mock listing `gpt-fault-s503-n1` and a
surface on Mock B listing `gpt`, as two backends of one model, show a fallback;
`gpt-fault-s503-n1` alone with `routing.retry` shows a retry. The e2e test
`tests/e2e/routing_test.go` drives each case.

## Limits and what has not been validated

- Performance and added latency have not been measured.
- Breakers and the cache are per process and in memory.
- The behaviour has been tested against the local mock providers and in unit
  tests, not against real providers' failure modes. Whether a given provider
  billed a request it answered with 429, 503 or 529 is the provider's to say.
- A QuotaPlex denial falling through to another backend
  (`on_quota_denied: next_backend`) is covered by unit tests of the
  classification only; the e2e suite runs without QuotaPlex.
- A held-back error body larger than 1 MiB is replaced by a short AIPlex error
  if it turns out to be the answer.
- The model name is rewritten in the body of OpenAI Chat, Responses,
  embeddings and Anthropic Messages, and in the path of GenAI and Bedrock;
  wires that relay a body verbatim (multipart uploads) need the same native
  name on every backend, as for single-backend aliases.
- Routing configuration is not part of the rendered gateway (Envoy) routes in
  `internal/deploy`; it is enforced by the API process.
