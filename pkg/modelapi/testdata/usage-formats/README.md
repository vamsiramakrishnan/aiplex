# Usage-format fixtures

Synthetic, doc-derived samples of how each provider reports token usage and
rate-limit metadata, used to audit usage observers. One subdirectory per
provider. Reference prose lives in
`docs/reference/usage-formats/<provider>.md`.

## File naming

`<surface>[-<variant>].<ext>` where the extension is the wire encoding:

| ext      | meaning                                                                                   |
|----------|-------------------------------------------------------------------------------------------|
| `.json`  | one complete response body (or a documented JSON blob)                                    |
| `.sse`   | raw Server-Sent Events text: `event: <name>` + `data: <json>` blocks separated by a blank line |
| `.jsonl` | one JSON value per line. For AWS event-stream transports each line is the already-decoded event payload (framing, CRCs and base64 removed); keyed by union-member name for Converse events |

Variants: `-interrupted` (stream/file cut before the final usage, or ended by an
error frame), `-error-midstream` (in-band error event after HTTP 200),
`-usage-absent` (no usage anywhere), plus feature-specific suffixes.

Prefixes: `messages` (first-party Messages API), `count-tokens`, `batch-results`,
`managed-agents`, `vertex-raw` (rawPredict / streamRawPredict), `bedrock-invoke`,
`bedrock-converse`, `bedrock-mantle`, `ratelimit`.

All values are synthetic. Ids, signatures and text are placeholders. Counter
values are deliberately distinct within a file so double counting is detectable.

## expected.json schema

Object keyed by fixture filename. Each value:

```json
{
  "input_tokens": 0, "output_tokens": 0,
  "cache_read_tokens": 0, "cache_write_tokens": 0,
  "reasoning_tokens": 0, "total_tokens": 0,
  "notes": "semantics and traps"
}
```

Normalization rules:

- Counters are `null` when the format does not report them *at that point in
  the stream/file*. `null` means unknown, never zero. A reported `0` stays `0`.
- `input_tokens` is the provider-reported "fresh" input counter, verbatim. For
  Anthropic this EXCLUDES cache reads and cache writes.
- `cache_write_tokens` sums per-TTL buckets when only buckets are reported.
- `reasoning_tokens` is a SUBSET of `output_tokens` (informational); it must
  never be added to output.
- `total_tokens` is observer-derived, not the provider's reported total:
  `input + cache_read + cache_write + output`, treating a missing component as
  0 but yielding `null` when input or output is `null`. Where a provider reports
  its own total, the notes call out differences.
- For interrupted streams, `expected` states what is known at the cut point.
  Placeholder counters (e.g. `message_start.output_tokens`) are not final and
  are reported as `null`.
- Multi-record files (`batch-results*.jsonl`) expect the aggregate over the
  lines present.
- Fixtures that carry no token usage (`count-tokens.json`, `ratelimit.json`) are
  all-`null`; their notes say why.

Anything marked UNCONFIRMED in a note was not verified in official docs.
