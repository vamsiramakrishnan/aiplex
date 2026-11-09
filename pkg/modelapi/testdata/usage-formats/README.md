# usage-formats test data

Provider wire-format fixtures for auditing usage observers. One subdirectory per
provider (`openai/`, ...). Contents are data only; no Go code reads them yet.

## File naming

`<surface>[.<variant>].<ext>`

| ext | meaning |
|-----|---------|
| `.json` | one non-streaming HTTP response body |
| `.sse` | raw `text/event-stream` body; frames separated by a blank line; some have `event:` lines, all have `data:` lines; Chat Completions ends with `data: [DONE]` when complete |
| `.jsonl` | WebSocket (or batch file): one JSON message per line, in arrival order |

Variants such as `.interrupted`, `.failed`, `.incomplete`, `.cancelled`, `.nousage`
mark cases where usage is absent or unconfirmed; their expected totals are all `null`.
All ids, text and base64 payloads are synthetic. Counter values are deliberately
distinct so double counting shows up as a wrong sum.

## expected.json

Maps each fixture filename in the directory to the normalized totals an observer
should produce for that whole file:

```json
{
  "chat.json": {
    "input_tokens": 1500,
    "output_tokens": 420,
    "cache_read_tokens": 640,
    "cache_write_tokens": 96,
    "reasoning_tokens": 192,
    "total_tokens": 1920,
    "notes": "free text on semantics"
  }
}
```

- The six counters are integers, or `null` when the format does not report that
  counter (null means "not reported", which is different from `0`, "reported as zero").
- `input_tokens` is the provider-reported input figure as-is (for OpenAI it includes
  cached and cache-write tokens); `cache_*` and `reasoning_tokens` are the subset
  breakdowns. `total_tokens` is the provider-reported total, not recomputed.
- A file with several responses (WebSocket, batch) expects the sum over distinct
  responses, each counted once.
- Non-token units (seconds, images) are not representable; those fixtures expect
  `null` for the affected counters and say so in `notes`.
- `notes` is free text; tests should not assert on it.
