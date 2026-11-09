# Native model execution and metering profiles

The managed model gateway selects an exact saved surface, native operation,
model, credential/network configuration and operator quota binding. Models are
not inferred from their name or enumerated in an embedded product list.
`modelapi.Coverage` describes the installed execution adapter. A surface still
chooses its own operations and supported models; the catalog does not grant
upstream availability, entitlement or runtime permissions.

## Coverage contracts

| Binding profile | Operations | Required token mappings |
| --- | --- | --- |
| `request-count` | Explicit cataloged HTTP/SSE generation, prediction, embeddings, counting, supported native resource operations and job/cache creation | None; `requests` is required |
| `gemini-text` | Native model/endpoint `generateContent` and `streamGenerateContent` | `uncached_input_tokens`, `cached_tokens`, `output_tokens`, `thought_tokens` |
| `openai-text` | `openai.chat-completions`, `response.create` | `uncached_input_tokens`, `cached_tokens`, `output_tokens` |
| `anthropic-text` | `anthropic.messages`, or Anthropic model `rawPredict` / `streamRawPredict` on Vertex | `uncached_input_tokens`, `cached_tokens`, `output_tokens` |
| `gemini-interaction-text` | Model-backed `interaction.create` | `uncached_input_tokens`, `cached_tokens`, `output_tokens`, `thought_tokens` |

Every profile additionally maps `requests`. Mapped raw meters must be distinct;
a thought meter is deliberately rejected for the two output-inclusive profiles.
Existing versioned price books price those raw units into explicit money meters.
Different token profiles need price books matching their actual meter sets:
a Gemini price book that requires a thought meter cannot be reused for an
output-inclusive profile by silently dropping that price input.

A request-count allocation counts the managed API exchange. For a batch, cache,
background response or long-running prediction, it counts the submission/read/
cancellation HTTP request, **not** generated tokens, retained storage, upstream
job lifetime or internal tool work. Its concurrency permit spans the managed
exchange. A configured request price is a fixed per-request tariff. Native
resource IDs and ownership are still bound before delivery, but API acceptance
is not proof that an upstream job has completed. Operations without an
installed coverage contract remain unsupported by the managed host.

## WebSocket sessions

Gemini Live (`live.connect`), OpenAI Realtime (`openai.v1.realtime`) and OpenAI
Responses WebSocket mode (`openai.v1.responses-websocket`) are covered by the
`request-count` profile only. A session maps onto the one-exchange model:

- **Admission**: one lease per session, prepared once the session's model is
  known (Live: after the bounded `setup` message; Realtime: the `?model=`
  connect parameter; Responses: per `response.create`, each re-authorized) and
  before credentials or the upstream dial. A token-profile lease is aborted and
  the session refused (`UNSUPPORTED_EXECUTION_TRANSPORT`).
- **Observation**: usage server events are forwarded as advisory, non-terminal
  observations: Live `usageMetadata` as cumulative session snapshots (last
  replaces earlier, never added), Realtime `response.done` and Responses
  `response.completed` as per-response deltas summed over distinct response ids.
  Realtime input-transcription usage is a separate `transcription_*` meter
  (billed at the transcription model's rate) and is never merged into the
  session's token counters. Realtime `rate_limits.updated` feeds the provider
  limit observation.
- **Settlement**: exactly once at session end. Only a clean close with complete
  evidence (every started response finished with usage; no Live model output,
  turn signal or client input after the last snapshot) yields a terminal
  observation with session totals. Otherwise the observed usage is labelled
  `lower_bound`, `ambiguous` (a resumed Live session, whose counter reset is
  unconfirmed) or `partial` (non-token units such as transcription seconds),
  and the ledger records the exchange as `unknown` with that `usage_bound`.
  Either way the request-count lease settles its one request and releases its
  permit.
- **Exposure bound**: per session, one admitted request against request
  budgets/rates and one concurrency permit for at most the gateway's
  `MaxStreamDuration` (default 30 minutes, at most 24 hours), after which both
  sockets are closed. Tokens on these paths are not bounded by quota, only by
  provider session limits and that duration; no token-metered claim is made.

## Strict text request contracts

All strict profiles require an exact model and observed native model identifier,
operator-attested provider-enforced input/output ceilings, unexpired evidence,
and configured prices for any money budget. The existing `provider_limits`
configuration applies to the exact binding/provider/model/version/account/region.
A configured maximum does not establish that a provider enforces it. Check the
actual native model contract before activation; tokenizer estimates are not
substitutes for a conservative exposure bound.

The profiles reserve a conservative input ceiling for each input subset and the
explicit requested output cap for each applicable output subset. They recheck
the evidence at dispatch and quarantine contradictory observed exposure. They
do not estimate exact input tokens from prompt bytes.

The current supported payloads are intentionally explicit:

- Gemini generation keeps the existing text-only, one-candidate contract.
- OpenAI Chat requires inline string message content, one completion and
  `max_completion_tokens`. Streaming additionally requires
  `stream_options.include_usage: true` so its final usage chunk is available.
- Responses requires inline string/text-message input, `max_output_tokens`, and
  explicit `store: false`. Tools, conversation/previous-response references,
  remote files, multimodal content and background execution are refused.
- Anthropic requires inline string message/system content and `max_tokens`.
  An explicit thinking budget must fit that output cap. Tools, cache controls,
  image/document blocks, context-management work and unknown fields are refused.
  The existing Messages client facade can translate to the corresponding native
  Vertex Anthropic operation; it uses the same quota admission and accounting.
- Interactions requires a direct model, one inline string input, a bounded
  generation configuration and explicit `store: false`. Agent selectors, tools,
  previous interactions, continuations, background work and media are refused.
  Both documented snake-case SDK and camel-case generation-limit spellings are
  accepted; conflicting aliases are refused.

Strict profiles reject `Anthropic-Beta` toggles and `Last-Event-ID` resumption.
Protocol headers that are forwarded are pinned into the invocation configuration
along with the provider configuration; changing them cannot reuse an admitted
idempotency key. A request-count profile makes no token-billing promise and can
forward supported native fields without acquiring a stricter profile implicitly.

## Native usage composition

Gemini reports prompt tokens including cached input, separate candidate/output
and thought counters, and a total. Settlement verifies that composition, removes
cached input from uncached input, and never adds prompt totals to their subsets.
Interactions uses its corresponding total input/cache/output/thought counters.

The Vertex OpenAI-compatible endpoint (`openai.chat-completions` on a Google
surface) reuses OpenAI field names with different semantics for Gemini:
`completion_tokens` **excludes** `reasoning_tokens` (prompt + completion +
reasoning = total). The observer folds reasoning into output only when the
native total proves that composition, keeps an inclusive composition when the
total proves that instead (open models), and otherwise leaves output unknown.
The endpoint documents no cached-input breakdown, so the strict `openai-text`
profile fails closed there.

Usage-format conformance against provider-documented samples, and the
generated provider x surface x transport coverage matrix, are in
[the usage-format reference](reference/usage-formats/coverage.md).

OpenAI reports reasoning **inside** output/completion tokens. Its output meter
therefore retains the full billed output count; reasoning details are diagnostic
and are not charged a second time. Cached prompt tokens are a subset of input,
so uncached input is total input minus cached input. The strict profile requires
that cached-input breakdown and verifies the native total when supplied.

Anthropic input tokens exclude cache-read input. Its uncached meter is therefore
the native input count; cache-read input is a separate meter. Thinking remains
inside billed output. No separately billable thought count is invented.

Positive cache-write/cache-creation quantities and native server-tool quantities
are currently outside the output-inclusive strict profiles. They fail closed
rather than using ordinary input prices. These dimensions need their own
attested exposure and price contracts. Thus a provider/version that automatically
reports cache writes cannot acquire this token-accounting profile simply because
it speaks an otherwise compatible Messages/Responses protocol.

The provider's actual native model identifier is recorded separately from the
configured target. Missing or different required proof retains exposure and
quarantines that binding generation; it never silently selects a different price
book. Identifier granularity is provider-specific. Deployment must establish that
the configured native identifier and attestation represent the intended version;
a stable alias alone does not prove that its serving implementation cannot change.

## Completion and streaming

JSON settlement requires a valid native terminal response and complete usage.
OpenAI Chat SSE waits for a native finish marker, its final usage chunk, `[DONE]`
and clean EOF. Responses and Interactions use their native terminal events.
Anthropic SSE preserves the input facts from `message_start`, merges cumulative
`message_delta` usage without summing repeated counts, requires a final usage
update and `message_stop`, and then waits for clean EOF. A stop marker alone is
not a usage report. New data after the Anthropic stop marker is refused.

Final native frames remain buffered until terminal evidence is durably
acknowledged. Earlier content and HTTP headers may already have been sent;
an incomplete stream must be treated as failure even if its HTTP status is 200.
Version-only/drift metadata is acknowledged before rejection. A clean final
response submits version and usage together so quarantine can preserve the known
terminal facts. Truncated streams do not manufacture terminal usage or release
uncertain holds.

Every cumulative update is checked, including updates inside the withheld final
tail. Later counters cannot replace higher observed usage; repeated equal
counters are not summed. Contradictions retain unknown exposure and suppress the
final tail. Request-count profiles keep token telemetry advisory. See the
[execution coverage and regression matrix](quotaplex-execution-coverage.md).

The local regression suite covers native JSON/SSE, client facade translation,
exact money composition, retry exclusion and unsupported exposure. It does not
prove billing equivalence, native model availability or live cloud entitlement.

## Primary contracts

- [OpenAI output token counting](https://developers.openai.com/api/docs/guides/token-counting)
- [OpenAI Responses creation and usage](https://developers.openai.com/api/reference/resources/responses/methods/create)
- [Anthropic Messages reference](https://platform.claude.com/docs/en/api/http/messages)
- [Anthropic streaming lifecycle](https://platform.claude.com/docs/en/build-with-claude/streaming)
- [Google Cloud Interactions API](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/interactions-api)
- [Google Interactions generation configuration](https://googleapis.github.io/js-genai/release_docs/types/gaos_models_interactions_generation-config.GenerationConfig.html)
- [Google model output/context semantics](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/guides/gemini-4-argon)
