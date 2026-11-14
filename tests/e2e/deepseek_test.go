//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// A compatible surface that declares DeepSeek usage semantics records the
// context-cache hit as cached input, and a DeepSeek catalogue price, imported
// without --accept-partial, prices the hits at the cache-hit rate. The mock
// upstream adds DeepSeek's hit/miss fields for a model named deepseek-*.
func TestDeepSeekCacheHitsArePricedFromTheCatalogue(t *testing.T) {
	e := start(t)
	spec := `surface:
  id: dsk
  protocol: openai-chat
  origin: ` + e.stack.ModelsURL + `
  models: [deepseek-v4-pro]
  operations: [compat.chat-completions]
  provider: {id: openai-compatible, usage_semantics: deepseek, stream_usage: inject}
auth: {type: none}
`
	e.mustCLI("llm", "surfaces", "apply", "-f", writeFile(t, "dsk.yaml", spec))
	e.mustCLI("access-points", "apply", "-f", writeFile(t, "ap.yaml", "id: dsk\ndisplay_name: DeepSeek e2e\ncaller_kinds: [user, agent, client]\nmodels:\n  - {name: deepseek-v4-pro, surface_ref: dsk, native_model: deepseek-v4-pro}\n"))

	r := e.mustCLI("usage", "prices", "catalog", "import", "deepseek/deepseek-v4-pro", "--source", "litellm", "--yes")
	contains(t, r.Out, "created   price deepseek-deepseek-v4-pro", "imported without --accept-partial")

	body := `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"one two three four five six seven eight"}]}`
	if resp := e.post("/ap/dsk/v1/chat/completions", body); resp.Status != 200 {
		t.Fatalf("json: %d %s", resp.Status, resp.Body)
	}
	stream := `{"model":"deepseek-v4-pro","stream":true,"messages":[{"role":"user","content":"one two three four five six seven eight"}]}`
	if resp := e.post("/ap/dsk/v1/chat/completions", stream); resp.Status != 200 {
		t.Fatalf("sse: %d %s", resp.Status, resp.Body)
	}

	r = e.mustCLI("usage", "records", "--model", "deepseek-v4-pro", "-o", "json")
	var page catalogRecords
	if err := json.Unmarshal([]byte(r.Out), &page); err != nil {
		t.Fatalf("records: %v\n%s", err, r.Out)
	}
	if len(page.Records) != 2 {
		t.Fatalf("want 2 records:\n%s", r.Out)
	}
	for _, rec := range page.Records {
		u := rec.Usage
		if u == nil || u.InputTokens == nil || u.OutputTokens == nil || u.CacheReadTokens == nil || u.CacheWrite == nil {
			t.Fatalf("incomplete usage: %+v", rec)
		}
		in, hit, out := *u.InputTokens, *u.CacheReadTokens, *u.OutputTokens
		if hit != in/2 || hit == 0 || *u.CacheWrite != 0 || rec.Provider != "openai-compatible" {
			t.Fatalf("cache hits not recorded as cached input: in=%d hit=%d write=%d provider=%s", in, hit, *u.CacheWrite, rec.Provider)
		}
		// USD 1.32 per million misses, 0.044 per million hits and 3.96 per
		// million output tokens, in thousandths of a micro-dollar, rounded up once.
		milli := (in-hit)*1320 + hit*44 + out*3960
		want := (milli + 999) / 1000
		if rec.Cost.Basis != "estimated" || rec.Cost.Amount != itoa(want) || rec.Cost.Scale != 6 || rec.Cost.Currency != "USD" {
			t.Fatalf("cost %+v, want %d for in=%d hit=%d out=%d", rec.Cost, want, in, hit, out)
		}
	}
}
