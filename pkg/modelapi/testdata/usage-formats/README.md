# Usage-format fixtures

Wire-format fixtures for auditing model-usage observers. One directory per
provider; this commit adds `google/`. Reference prose lives in
`docs/reference/usage-formats/<provider>.md`.

## File naming

`<surface>-<operation>[-<variant>].<ext>` inside `<provider>/`.

| Extension | Meaning |
|---|---|
| `.json` | One complete HTTP response body (or one object per file when a transport returns a single document). |
| `.sse` | Raw Server-Sent Events text exactly as it would arrive on the wire: optional `event:` line, `data:` line, blank line. A trailing `data: [DONE]` is kept where the provider sends it. |
| `.jsonl` | One JSON object per line. Used for WebSocket server messages (`*.ws.jsonl`, one frame per line, in arrival order) and for batch output files. |

Surface prefixes for Google: `gemini-` = Gemini Developer API, `vertex-` =
Vertex AI (now documented as Gemini Enterprise Agent Platform).

Variant suffixes:

- `-interrupted`: the stream or session ends before the final usage signal. The
  `expected.json` entry states what is known and what is `null`.
- `-cumulative` / default: stream usage placement variants (see notes in each entry).
- `-error-429`: error body, no usage.
- `.stream.json`: the JSON-array form of `streamGenerateContent` (no `alt=sse`).

All identifiers, text and most counter values are synthetic. Counters are
chosen to be distinct (for example prompt 1200, cached 1024, output 85,
thoughts 342) so double counting, wrong-field mapping and subset/additive
confusion change the result. A few Vertex values copy documented examples
verbatim; those are noted in their `notes`.

## `expected.json` schema

Top level: an object keyed by fixture filename (every fixture in the directory
has exactly one entry). Each value:

```json
{
  "input_tokens": 1200,
  "output_tokens": 85,
  "cache_read_tokens": 1024,
  "cache_write_tokens": null,
  "reasoning_tokens": 342,
  "total_tokens": 1627,
  "notes": "free text on semantics, UNCONFIRMED items, and pitfalls"
}
```

Normalization conventions (apply them when auditing an observer):

- `input_tokens`: the full prompt/context tokens for the call, INCLUDING cache
  reads and cache writes. For Google generate this is `promptTokenCount` as
  reported. For formats where the provider's input field excludes cache
  (partner Claude `input_tokens`), the expected value is the provider field
  plus cache read plus cache write, and `notes` says so.
- `cache_read_tokens` / `cache_write_tokens`: subsets of `input_tokens`. Not
  additive.
- `output_tokens`: visible/model output, EXCLUDING separately reported reasoning
  tokens. Where a provider folds reasoning into output (partner Claude),
  `reasoning_tokens` is `null`.
- `reasoning_tokens`: provider-reported thinking tokens, reported separately.
- `total_tokens`: the provider-reported total when the format has one. It can
  exceed the sum of the other counters (tool-use prompt tokens are inside the
  total but have no normalized slot). `null` when the format reports no total.
- `null` means: the counter is not present in that payload, or is unknowable
  because the stream or session was interrupted. It is NOT a statement that the
  value is zero. Whether an absent field implies zero is discussed per format in
  the reference doc.
- Multi-record fixtures (batch output) give the AGGREGATE over successful records.
- Streams and sessions give the value an observer should settle on at the end
  of the fixture (usually the last cumulative snapshot), never a sum of
  snapshots.
- Interrupted fixtures: any non-null value is a lower bound for what was
  actually consumed; the `notes` field says so.
- Error fixtures: all counters `null`; the entry documents the error shape.
