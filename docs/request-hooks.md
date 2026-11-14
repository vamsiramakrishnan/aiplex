# Request hooks

Request hooks inspect model and tool traffic on its way through AIPlex. A hook can
**allow** it, **deny** it with a safe public message, or **transform** it by
replacing text. Hooks run in two phases:

- `request`: before anything is sent upstream (after identity, admission and
  capacity checks, before the quota lease and the provider credential).
- `response`: before any byte of a successful response reaches the caller.

They apply to two planes: `llmplex` (model requests through managed native
handlers, the per-surface gateway and access points) and `mcplex` (MCP
`tools/call` through managed tool connections, directly or through an access
point).

On tool calls, [policy rules](tool-policy-rules.md) run first and decide allow or deny
from structured facts (the tool, its arguments, the caller, the time). They do not look
at content or change it; hooks do. A call a rule refuses never reaches the hook chain,
and hooks see the arguments of a call the rules allowed.

This page states what is implemented, what is inspected and what is not. Read
the [limits](#limits-and-what-has-not-been-validated) before relying on hooks
for compliance.

## Hook policies

A hook policy is a tenant resource of kind `hook-policy`. It binds an ordered list
of hooks to one scope:

| `scope.kind` | Applies to |
| --- | --- |
| `tenant` | Every exchange of the tenant (both planes unless `planes` narrows it). |
| `access_point` | Exchanges that entered through access point `scope.id`. |
| `model_surface` | Model exchanges on surface `scope.id`. |
| `tool_connection` | Tool calls on connection `scope.id`. |

```yaml
id: pii-guard
description: Mask email addresses; refuse private keys.
scope: {kind: tenant}
planes: [llmplex, mcplex]      # optional; default both
streaming: deny                 # deny (default) | buffer | passthrough_uninspected
non_text: allow                 # allow (default) | deny
hooks:
  - id: redact
    type: redact
    phases: [request, response]   # required
    timeout_ms: 2000              # default 2000, at most 30000
    on_uninspectable: deny        # deny (default) | allow
    message: "The request contains a private key."   # optional safe public message
    redact:
      rules:
        - {id: email, builtin: email, action: mask}
        - {id: private_key, builtin: private_key, action: deny}
        - {id: employee, pattern: 'EMP-[0-9]{6}', action: mask, placeholder: '[EMPLOYEE_ID]'}
```

**Scopes add; they never remove.** Every policy whose scope matches an exchange
applies: tenant policies first, then access point, then model surface or tool
connection; within a scope, in policy id order. A narrower scope cannot remove
or weaken a broader policy's hooks: there is no removal syntax, a policy must
list at least one hook, and `streaming` and `non_text` take the most restrictive
value across all matching policies (`deny` over `buffer` over
`passthrough_uninspected`; `deny` over `allow`). A tenant policy's default
`streaming: deny` therefore holds even if an access-point policy says `buffer`.

Hooks run in order. The first `deny` wins and later hooks do not run. A
`transform` is visible to the hooks after it.

**Validation at save.** Unknown fields at any level and unknown hook types are
refused. Every regex is compiled (RE2; a pattern that does not compile, matches
the empty string or is longer than 1024 bytes is refused). At most 16 hooks per
policy and 64 rules per redact hook. Sidecar endpoints are checked against the
outbound endpoint policy (below), and a sidecar secret reference is checked to
exist when the secret store can prove it.

### Who can read and write policies

Operators (`admin` or `deployer`) read, write and dry-run policies. Callers
cannot read them: a policy names detection rules, patterns and sidecar endpoints,
which would help a caller shape content to slip past it. A caller learns of a
policy only from a refusal's safe public message.

## Hook types

### `redact`

Deterministic pattern rules. Each rule has an `action`: `mask` replaces every
match with a placeholder, `deny` refuses the exchange (deny rules are checked on
every match before overlaps are resolved, so a mask rule cannot hide a deny
match). Overlapping masks keep the earliest, longest match.

| `builtin` | Placeholder | Notes |
| --- | --- | --- |
| `email` | `[EMAIL]` | |
| `phone` | `[PHONE]` | North American `(415) 555-0123` style and `+` followed by 8 to 15 digits. Many national formats are not matched. |
| `credit_card` | `[CREDIT_CARD]` | 13 to 19 digits with optional spaces or dashes, kept only when the Luhn check passes. |
| `iban` | `[IBAN]` | Kept only when the ISO 13616 mod-97 check passes. |
| `us_ssn` | `[US_SSN]` | `ddd-dd-dddd`, excluding area 000, 666 and 9xx, group 00 and serial 0000. |
| `ipv4` | `[IP_ADDRESS]` | |
| `ipv6` | `[IP_ADDRESS]` | Candidates are confirmed with Go's address parser. |
| `aws_access_key_id` | `[AWS_ACCESS_KEY_ID]` | `AKIA`, `ASIA`, `ABIA`, `ACCA` prefixes. Secret access keys are not matched. |
| `github_token` | `[GITHUB_TOKEN]` | `ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`. |
| `jwt` | `[JWT]` | Three base64url segments, the first two starting `eyJ`. |
| `private_key` | `[PRIVATE_KEY]` | PEM `-----BEGIN ... PRIVATE KEY-----` blocks (and a lone header). |
| `bearer_token` | `[BEARER_TOKEN]` | `Bearer` followed by at least 16 token characters. |
| `google_api_key` | `[GOOGLE_API_KEY]` | `AIza` followed by 35 characters. |

Operator rules use `pattern` (RE2 syntax only: no backreferences or lookaround)
and an optional `placeholder` (default `[RULE_ID]`).

**This is pattern matching, not a guarantee.** It finds text shaped like the
patterns. It misses personal data and secrets in other shapes, split across
parts, encoded (base64, URL encoding, Unicode look-alikes), in images or files,
or in fields AIPlex does not inspect, and it can match text that is not
sensitive. Use it to reduce accidental exposure, not as a control that personal
data cannot leave.

A redact hook refuses (`inspect_limit`) when the text of one exchange exceeds
4 MiB.

### `size_limit`

`max_chars` (Unicode code points over all text parts) and `max_parts` (text plus
uninspected parts). Exceeding either denies with rule `max_chars` or `max_parts`.

### `http` (sidecar)

Calls an operator-run sidecar. `format` selects the contract:

| `format` | Calls | Notes |
| --- | --- | --- |
| `aiplex` (default) | `POST {endpoint}` once per phase with every part | The AIPlex contract below. |
| `presidio` | `POST {endpoint}/analyze` once per text part; optionally `POST {anonymizer_endpoint}/anonymize` | `presidio.action: mask` (default) replaces each detected span with `[ENTITY_TYPE]`, or uses the Anonymizer's text when `anonymizer_endpoint` is set; `deny` refuses on any detection. `language` (default `en`), `entities`, `score_threshold`. |
| `llm-guard` | `POST {endpoint}/analyze/prompt` (request) or `/analyze/output` (response) once per text part; `/scan/...` with `llm_guard.mode: scan` | `is_valid: false` denies (rule: the first scanner with a score above 0); in `analyze` mode the sanitized text replaces the part. `scanners_suppress` is passed through. The output scanners receive an empty `prompt`: the request prompt is not kept for the response phase. |

```yaml
  - id: presidio
    type: http
    phases: [request]
    timeout_ms: 1500
    http:
      endpoint: http://presidio-analyzer.hooks.svc.cluster.local:3000
      format: presidio
      auth_secret_ref: presidio-token     # optional; sent as Authorization: Bearer
      fail_open: false                     # default
      presidio: {entities: [EMAIL_ADDRESS, PERSON], score_threshold: 0.5}
```

**Endpoint policy.** Every sidecar URL must be approved by the deployment's
outbound endpoint policy, the same policy that approves tool connection token
endpoints (`connections.network`: an explicit origin allowlist, private CIDRs
only when listed, loopback only when `allow_loopback` is set, cloud metadata
addresses never). It is checked when the policy is saved and on every call (the
HTTP client dials only approved, re-resolved addresses). Redirects are never
followed: a 3xx is an error. With `aiplex dev`, approve a local sidecar with
`aiplex dev --allow-origin http://127.0.0.1:5005`.

**Credentials.** `auth_secret_ref` names a tenant secret; only the reference is
stored. The value is sent as `Authorization: Bearer <value>` and never logged.

**Fail closed.** A timeout, a refused or reset connection, a non-200 status, a
non-JSON or ambiguous body (duplicate keys), a body that does not match the
contract (unknown fields, an unknown decision, a transform naming an unknown
part), a missing secret: each is an `error`, and the exchange is refused with
HTTP 503 `hook_unavailable`. `fail_open: true` is an explicit per-hook choice:
the exchange then continues without that hook's decision, and every affected
ledger record carries `hooks.fail_open_used: true` and the span
`aiplex.hooks.fail_open_used=true`. Use it only when availability matters more
than the check.

#### The AIPlex sidecar contract (`aiplex.hooks.v1`)

Request (`POST`, `Content-Type: application/json`):

```json
{
  "version": "aiplex.hooks.v1",
  "phase": "request",
  "plane": "llmplex",
  "operation": "openai.v1.chat-completions",
  "provider": "openai",
  "model": "gpt-4.1-mini",
  "caller_kind": "user",
  "principal_id": "principal-…",
  "access_point_id": "local",
  "parts": [
    {"index": 0, "locator": "messages[0].content", "role": "system", "kind": "system", "text": "Be brief."},
    {"index": 1, "locator": "messages[1].content[0].text", "role": "user", "kind": "text", "text": "Mail jane@example.com"}
  ],
  "uninspected": [{"locator": "messages[1].content[1]", "type": "image_url"}]
}
```

`kind` is `text`, `system`, `tool_arguments` or `tool_result`. For tool calls
`plane` is `mcplex`, `provider` is `mcp`, `operation` is `tools/call` and `tool`
names the tool. Empty fields are omitted.

Response (`200`, `Content-Type: application/json`; unknown fields are refused):

```json
{"decision": "allow"}
{"decision": "deny", "reason": "confidential"}
{"decision": "transform", "parts": [{"index": 1, "text": "Mail [EMAIL]"}], "rules": [{"id": "email", "count": 1}]}
```

`reason` and `rules[].id` are recorded only when they are short lowercase
identifiers (`[a-z0-9][a-z0-9_.-]{0,62}`); anything else is recorded as
`sidecar`. The caller never sees them: a denial shows the hook's `message`, or
the default "the request was refused by a content policy". A sidecar's
replacement text is trusted as given.

`examples/hooks/sidecar` is a reference implementation in Go (standard library
only).

#### Presidio and LLM Guard adapter status

The `presidio` and `llm-guard` adapters were written from each project's
published API documentation and source (Presidio's `api-docs.yml`; LLM Guard's
`llm_guard_api` schemas and client documentation), read on 2026-11-10, and are
tested against hand-written example payloads in `pkg/hooks/testdata`. **They have
not been run against real Presidio or LLM Guard services.** Presidio's `start`
and `end` are treated as Unicode code point offsets (the service is Python); a
span outside the text is an error. Verify both against your deployment with
`aiplex hooks policies test` before relying on them.

## What is inspected, per wire format

A part is a JSON string extracted from the native body with a stable locator.
A transform rewrites exactly those string literals: every other byte (key order,
whitespace, unknown fields, number spelling, escapes) is unchanged, and a body
no hook transformed is forwarded byte for byte.

| Wire format (operations) | Request parts | Response parts | Streamed response |
| --- | --- | --- | --- |
| OpenAI Chat Completions (OpenAI, Azure OpenAI, OpenAI-compatible, Vertex OpenAI endpoint) | `messages[].content` (string, or `text` parts), `refusal`, `tool_calls[].function.arguments`, legacy `function_call.arguments` | `choices[].message` the same fields | `buffer` supported: `delta.content`, `delta.refusal`, `delta.tool_calls[].function.arguments` |
| OpenAI Responses (`response.create`, Azure, Vertex) | `instructions`, `input` (string or items: message `content` text parts, `function_call.arguments`, `function_call_output.output`) | `output[]` the same item types | `buffer` supported: output text and refusal deltas, `function_call` arguments and `custom_tool_call` input, with every repeated copy (see [Buffered streams that repeat content](#buffered-streams-that-repeat-content)) |
| Anthropic Messages (Anthropic, Vertex `rawPredict`/`streamRawPredict` for the anthropic publisher, Anthropic-shaped Bedrock `InvokeModel`, `count_tokens`) | `system` (string or text blocks), `messages[].content` (string, `text`, `tool_use.input` string leaves, `tool_result` content) | `content[]` the same block types | `buffer` supported: `text_delta`, `input_json_delta` |
| Gemini `generateContent` / `streamGenerateContent` / `countTokens` (Vertex and Gemini Developer API) | `systemInstruction`/`system_instruction` parts, `contents[].parts[]`: `text`, `functionCall.args` and `functionResponse.response` string leaves, `executableCode.code`, `codeExecutionResult.output` | `candidates[].content.parts[]` the same (also the JSON-array form of `streamGenerateContent`) | `buffer` supported, as server-sent events (`alt=sse`) and as the JSON array: `text` parts of a candidate assembled across chunks, `functionCall.args` leaves, `executableCode.code`, `codeExecutionResult.output` |
| Bedrock Converse / ConverseStream | `system[].text`, `messages[].content[]`: `text`, `toolUse.input` leaves, `toolResult.content` (`text`, `json` leaves) | `output.message.content[]` the same | Not supported (event stream): refused under `buffer` |
| OpenAI embeddings and moderations | `input` (string or strings; token arrays are uninspected) | No text (vectors, scores) | — |
| MCP `tools/call` | `params.arguments` string leaves | `result.content[]` `text`, `resource.text`, `result.structuredContent` string leaves (JSON or one SSE event) | — |

**Uninspected parts.** Images, audio, files, documents, `inlineData`/`fileData`,
`cachedContent` references, reasoning and thinking blocks, and block types the
extractor does not know are reported as uninspected parts with their type. With
`non_text: allow` (default) the exchange continues and the ledger counts them in
`hooks.uninspected_parts`; `non_text: deny` refuses it.

**Cannot inspect.** An operation with no extractor (Bedrock `InvokeModel` with a
non-Anthropic body, Vertex `predict`, OpenAI images and audio, batches, files,
cached contents, Interactions, multipart uploads, opaque non-JSON bodies) or a
body that does not parse as its format is uninspectable. Each applicable hook's
`on_uninspectable` decides: `deny` (default) refuses the exchange (`hook_denied`,
rule `uninspectable`); `allow` records the hook as `skipped`. When a response
hook applies to an operation with no response extractor, the decision is made
before the request is sent.

**Ambiguity is refused.** A body with duplicate keys at any depth, a field
spelled differently only by letter case (`Messages` beside `messages`, which a
case-insensitive upstream parser could read), or Gemini's camelCase and
snake_case spellings of the same field both present is uninspectable. This keeps
AIPlex from inspecting one value while the provider reads another.

### What is not inspected

- Tool definitions (`tools`, `functions`, `toolConfig`) and their descriptions,
  JSON schema, `response_format`, `prediction`, metadata and other parameters.
- Object keys (only string values are parts).
- Text inside images, audio, PDFs or files; content referenced by ID (stored
  responses, cached contents, file IDs, `previous_response_id`).
- Provider error bodies (non-2xx upstream responses pass through unchanged) and
  JSON-RPC error messages from tool servers.
- `tools/list`, `initialize` and other MCP methods; agent (A2A) traffic.
- WebSocket sessions (OpenAI Realtime, Responses WebSocket mode, Gemini Live):
  client messages are not inspected one by one. **A WebSocket session on a scope
  with any applicable hook is refused at connect** (`hook_denied`, before the
  upgrade).

## Streaming

When a response hook applies and the request streams (`stream: true`, or a
`stream*` operation), the effective `streaming` setting decides:

| `streaming` | Behaviour |
| --- | --- |
| `deny` (default) | Refused before the request is sent: HTTP 403 `hook_denied` with a message asking for a non-streaming request. |
| `buffer` | OpenAI Chat SSE, Anthropic Messages SSE, OpenAI Responses SSE and Gemini `streamGenerateContent` (SSE or JSON array) only. The whole stream is read (bounded by the 8 MiB body limit) through the same usage observers as an unbuffered stream, so metering is unchanged. The response hooks see the assembled text of each choice or block; the stream is then re-emitted with every event in place. A transformed part's full text goes into its first fragment and the later fragments become empty strings. Time to first token becomes the time to the whole response. Any other streamed format is refused (Bedrock ConverseStream, for one). Responses and Gemini streams have extra rules: see below. |
| `passthrough_uninspected` | The stream is relayed unchanged and uninspected; the response hooks are recorded as `skipped` with rule `streaming_passthrough`. |

Request hooks run on streaming requests as on any other.

### Buffered streams that repeat content

Chat Completions and Anthropic streams carry each piece of text once. OpenAI
Responses and Gemini streams repeat it, so a redaction that rewrote only the
deltas would leave the original in a later event. For these two formats:

- **Every copy is rewritten.** A Responses stream sends the text as
  `response.output_text.delta` events and again whole in
  `response.output_text.done`, `response.content_part.done`,
  `response.output_item.done` and the `output` of `response.completed`
  (likewise refusals, `function_call` arguments and `custom_tool_call` input).
  The assembler registers every one of those copies. A copy whose text equals
  the assembled deltas (or an earlier copy) is inspected once with them, and a
  transform writes the whole replacement into the first delta (the later deltas
  become empty strings) and into each repeated copy. A copy that differs from
  everything before it is inspected as a part of its own, so no copy escapes the
  hooks whatever its event says. All text parts of a Gemini candidate
  (answer text and thought text apart), across chunks and around any function
  call or other part between them, are one part, because the SDKs' text
  accessors join them; a transform writes the whole replacement into the first
  text part and empties the others. The non-streamed Gemini extractor joins a
  candidate's text parts the same way.
- **Characters split across deltas** are joined from their escaped form before
  the hooks see them, so a surrogate pair written as two `\u` escapes in two
  events is one character.
- **Error events are inspected.** The `message` of a Responses `error` event or
  failed response, and of a Gemini `{"error": ...}` chunk, is a part like any
  other (its code and status are left as sent).
- **Metering is unchanged.** Only the string literals of text are rewritten.
  Usage, ids, finish reasons and terminal events are the provider's bytes, and
  every event is re-emitted in its place; an unchanged stream is returned byte
  for byte. The conformance tests run the usage observers over the original and
  the re-emitted fixtures and compare the observations.
- **Comments and padding are dropped when a stream is rewritten.** A
  server-sent-event comment line (`: ...`) is removed from every event of a
  stream the hooks changed, and a Responses delta's `obfuscation` padding is
  emptied, so neither can carry a copy of replaced text. Other provider
  metadata (ids, names, status and finish reasons, usage) is relayed as sent;
  a message from a role other than `assistant` (Responses) or `model` (Gemini)
  is refused.

Anything the assembler does not read is refused (`hook_denied`, rule
`uninspectable`, unless the hook's `on_uninspectable` is `allow`), rather than
passed on:

- Responses: an event type that is not one of the lifecycle, output item,
  content part, text, refusal, function call, custom tool call, reasoning,
  tool-status, audio or `error` events; any field an event, item or part does
  not list (for example a `text` key on a delta, or an unknown item type);
  `response.output_text.annotation.added` and non-empty `annotations` or
  `logprobs` (they index into or repeat the text, which a transform changes);
  an `output_text` field on the response; an `event:` line that does not name the
  event's `type`.
- Gemini: a chunk, candidate, content, part, `functionCall`, `executableCode` or
  `codeExecutionResult` with a field not listed in the extractor (so
  `groundingMetadata`, `logprobsResult` and streamed call arguments
  `partialArgs`/`willContinue` are refused); a `text` that is not a string; an
  `error` chunk with other fields.
- Every streamed format (including Chat Completions, Anthropic and MCP): an
  event that is not unambiguous JSON, a stream whose last event is not
  terminated by a blank line, and a carriage return that is not part of a CRLF
  (the SSE specification ends a line at a lone CR, and a client that follows it
  would read a different stream than the one inspected). CRLF-framed streams
  work and are inspected. For Responses and Gemini also: a duplicate or
  differently cased key in a field the assembler reads, and an SSE field other
  than `data` (and `event` for Responses).
- Buffers over the 8 MiB bound are refused before the hooks run (HTTP 502), as
  for every format.

**The `response` object of a Responses stream.** What the provider or model
produces is inspected: the output items and every repeat of them, refusals,
tool-call arguments, `error` (including `error.message`), `incomplete_details`,
and any field the guide does not list, whatever its name. A number, boolean or
null in an unlisted field passes; a string is a text part that a transform
rewrites in place; an array or object is walked and each string in it is a part
(at most 8 levels and 2000 strings in each field, beyond which the stream is refused). The
provider's own identifiers, enumerations and numbers (`id`, `object`, `status`,
`model`, `service_tier`, `truncation`, `previous_response_id`, `created_at`,
`completed_at`, `max_output_tokens`, `max_tool_calls`, `temperature`, `top_p`,
`top_logprobs`, `parallel_tool_calls`, `store`, `background`) are relayed as
sent while they have their expected JSON type; with another type they are
treated as unlisted fields. No list of allowed values is kept. The caller's own
request, echoed back (`instructions`, `tools`, `tool_choice`, `text`,
`reasoning`, `prompt`, `conversation`, `metadata`, `user`,
`safety_identifier`, `prompt_cache_key`, `billing`, `input`), is relayed as sent:
it is not inspected as model output and not reported as uninspected, because
request hooks already cover it. The non-streamed Responses extractor reads only
`output` and treats these fields the same way, so a streamed and a non-streamed
response with the same content get the same decision, the same redacted text and
the same uninspected reports (a test checks this under `non_text: deny` and
`allow`).

Two behaviours differ from before and are deliberate. When a redaction applies
to a Gemini candidate whose text surrounds a function call, the whole
replacement is written into the first text part, so text that followed the call
now comes before it (nothing is duplicated or dropped; a stream no hook changed
is returned byte for byte). And when an upstream drops the connection in the
middle of an event, the partial last event is refused in every streamed format;
Chat Completions and Anthropic used to relay it unseen.

Reasoning summaries, reasoning text, audio, code interpreter and MCP call
payloads, image data and `inlineData`/`fileData` parts are reported as
uninspected parts (the ledger's `hooks.uninspected_parts`; `non_text: deny`
refuses them), exactly as the non-streamed extractor reports the same output
items. They are relayed as sent, so a hook never sees or redacts text that only
appears in them.

## Refusals

| Situation | HTTP | Code |
| --- | --- | --- |
| A hook denied (including `uninspectable`, `non_text`, streaming and WebSocket refusals) | 403 | `hook_denied` |
| A hook could not decide and is not `fail_open` | 503 | `hook_unavailable` |
| Hook policies could not be read, or a stored policy no longer validates | 503 | `hook_policy_unavailable` |

Through an access point the refusal uses the caller's protocol:

| Wire | Shape |
| --- | --- |
| OpenAI Chat, Responses, embeddings | `{"error": {"message", "type": "permission_error", "param": null, "code": "hook_denied"}}` |
| Anthropic Messages | `{"type": "error", "error": {"type": "permission_error", "message", "code": "hook_denied"}}` |
| Gemini | `{"error": {"code": 403, "message", "status": "PERMISSION_DENIED", "details": [{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "HOOK_DENIED", "domain": "aiplex"}]}}` |
| Bedrock | `{"message", "code": "hook_denied"}` with `X-Amzn-ErrorType: AccessDeniedException` |
| MCP `tools/call` | JSON-RPC error `-32001` with `data.reason: "hook_denied"` (or `hook_unavailable`) and the request's `id` |

Every refusal also carries `X-Aiplex-Error-Code`. On the managed native routes
(`/api/v1/llm/native/...`) the body is AIPlex's `{"code": "HOOK_DENIED",
"message": ...}`. The message is the hook's `message` or a fixed default; it
never contains the matched text or any part of the request.

## Order and quota

Where hooks sit among the other checks, for models and for tools, is in
[request lifecycle](request-lifecycle.md). In short: request hooks run after
admission and the grants, before the quota and budget lease, and response hooks
run before the response is delivered. A request refused by a hook reserves no
quota and calls no provider.

For a routed model, request hooks run once, on the first attempt, with the
policies of every backend's surface; later attempts reuse the hooked body (see
[reliability and routing](reliability-and-routing.md#request-and-response-hooks)).
A response refused by a response hook has already been metered and settled: the
tokens were produced and are charged.

## Recording

Each managed ledger record carries a [`hooks` summary](usage-ledger.md#record-fields):
per hook run, its `policy/hook` id, type, phase, decision (`allow`, `deny`,
`transform`, `error`, `skipped`), the rule identifiers that matched with match
counts, `duration_ms` and `fail_open_used`. Filter with `hook_decision` and group
with `group_by=hook_decision` (CLI: `--hook-decision`, `--by hook`). The exchange
span carries `aiplex.hooks.decision`, `aiplex.hooks.count`,
`aiplex.hooks.denied_rule` and `aiplex.hooks.fail_open_used`; metrics
`aiplex.hooks.decisions` and `aiplex.hooks.duration` are labelled by hook type,
phase and decision only ([observability](observability.md)). None of these hold
content or matched text; a test plants a marker in a prompt and in a tool
argument and checks it appears in no record, span, log line, metric label or
error body, masked or denied.

## API and CLI

| Route | Who | Purpose |
| --- | --- | --- |
| `GET /api/v1/hooks/policies` | operator | List policies with revisions. |
| `GET /api/v1/hooks/policies/{id}` | operator | One policy and its revision. |
| `PUT /api/v1/hooks/policies/{id}` | operator | Create (`If-Match: 0`) or replace (current revision). |
| `DELETE /api/v1/hooks/policies/{id}` | operator | Delete at the current revision. |
| `POST /api/v1/hooks/policies/{id}/test` | operator | Dry run: `{"texts": [...], "phase": "request", "plane": "llmplex"}` returns the decision, each hook's result and the output texts. The sample is not stored or logged; `http` hooks call their sidecar. |

```bash
aiplex hooks policies set -f examples/hooks/policy.yaml
aiplex hooks policies list -o wide
aiplex hooks policies test pii-guard --text "mail jane@example.com"
aiplex hooks policies delete pii-guard --yes
aiplex usage records --hook-decision deny -o wide
```

The Console has a **Hooks** screen (operators only, like the routes): it lists
policies with scope, hooks and revision, creates, replaces and deletes them at the
revision it read (a conflict is shown, never overwritten), and dry-runs a sample
against a chosen policy. The editor takes the JSON document the API returns; a YAML
file is applied with the CLI (`aiplex hooks policies set -f FILE`). The server's
validation message is shown verbatim beside the editor. The sample is sent only to the test route and is kept in
memory only; it is cleared when you leave the screen or choose another policy.

The Go SDK has `ListHookPolicies`, `GetHookPolicy`, `PutHookPolicy`,
`DeleteHookPolicy` and `TestHookPolicy`. `aiplex dev` starts with no hook
policy. See `examples/hooks/`.

## Provider-side guardrails

AIPlex hooks are separate from provider guardrails (Bedrock `guardrailConfig`,
Azure content filters, Gemini safety settings). Those fields are relayed to the
provider like any other field and are not interpreted by hooks. One existing
limit is unrelated to hooks: a Bedrock Converse request on a surface with
token-metered QuotaPlex bounds refuses `guardrailConfig` and other fields whose
token cost cannot be bounded before dispatch.

## Limits and what has not been validated

- Performance has not been measured. Each exchange with an applicable policy
  reads the tenant's hook policies from the resource store, and a `redact` hook
  compiles its operator patterns on each run.
- The Presidio and LLM Guard adapters have not been run against the real
  services (see above).
- `buffer` holds the whole response in memory (up to 8 MiB) and removes
  streaming's latency benefit.
- Masking a tool argument string inside a JSON-encoded argument string (OpenAI
  `function.arguments`) replaces text inside that JSON; an operator pattern that
  matches quotes can make it invalid JSON.
- A WebSocket session cannot be used on a scope with any hook (this includes the
  Responses WebSocket mode and Gemini Live).
- Buffered Responses and Gemini streams were tested against the repository's
  recorded fixtures and hand-built streams, not against the live services.
  Server-side grounding (`groundingMetadata`), log probabilities and
  annotations make a stream be refused under `buffer` rather than inspected.
  Object keys inside tool arguments are not inspected (as in every format).
  After a transform, offsets that the provider computed on the original text
  (Gemini `citationMetadata` indexes) are not adjusted.
- Hook decisions are not part of QuotaPlex replay: a retried request with the
  same idempotency key runs the hooks again.
