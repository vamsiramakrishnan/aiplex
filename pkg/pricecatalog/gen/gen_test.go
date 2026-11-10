package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/pricecatalog"
)

func TestMapKey(t *testing.T) {
	for _, c := range []struct {
		provider, key, wantProvider, wantModel string
		ok                                     bool
	}{
		{"openai", "gpt-4o", "openai", "gpt-4o", true},
		{"openai", "ft:gpt-3.5-turbo", "", "", false},
		{"openai", "low/1024-x-1024/gpt-image-1.5", "", "", false},
		{"anthropic", "claude-haiku-4-5", "anthropic", "claude-haiku-4-5", true},
		{"azure", "azure/gpt-4o", "azure-openai", "gpt-4o", true},
		{"azure", "azure/eu/gpt-4o", "", "", false},
		{"bedrock", "anthropic.claude-sonnet-4-6", "bedrock", "anthropic.claude-sonnet-4-6", true},
		{"bedrock_converse", "us.anthropic.claude-sonnet-4-6", "bedrock", "us.anthropic.claude-sonnet-4-6", true},
		{"bedrock", "bedrock/amazon.nova-lite-v1:0", "bedrock", "amazon.nova-lite-v1:0", true},
		{"bedrock", "bedrock/us-east-1/anthropic.claude-v2", "", "", false},
		{"bedrock", "bedrock/*/1-month-commitment/cohere.command-text-v14", "", "", false},
		{"bedrock", "1024-x-1024/50-steps/stability.stable-diffusion-xl-v1", "", "", false},
		{"gemini", "gemini/gemini-2.5-flash", "gemini-developer", "gemini-2.5-flash", true},
		{"gemini", "gemini-2.5-flash", "", "", false},
		{"vertex_ai-language-models", "gemini-2.5-flash", "google", "gemini-2.5-flash", true},
		{"vertex_ai", "vertex_ai/gemini-3-pro-preview", "google", "gemini-3-pro-preview", true},
		{"vertex_ai", "vertex_ai/some-partner-model", "", "", false},
		{"vertex_ai-anthropic_models", "vertex_ai/claude-haiku-4-5@20251001", "vertex-anthropic", "claude-haiku-4-5@20251001", true},
		{"vertex_ai-anthropic_models", "vertex_ai/claude-haiku-4-5", "", "", false},
		{"groq", "groq/openai/gpt-oss-120b", "groq", "openai/gpt-oss-120b", true},
		{"deepseek", "deepseek-chat", "deepseek", "deepseek-chat", true},
		{"deepseek", "deepseek/deepseek-v3", "deepseek", "deepseek-v3", true},
		{"fireworks_ai", "fireworks_ai/x", "", "", false},
	} {
		p, m, ok := mapKey(c.provider, c.key)
		if p != c.wantProvider || m != c.wantModel || ok != c.ok {
			t.Errorf("mapKey(%s, %s) = %q %q %v; want %q %q %v", c.provider, c.key, p, m, ok, c.wantProvider, c.wantModel, c.ok)
		}
	}
}

func TestBuildEntryClassifiesWhatIsLeftOut(t *testing.T) {
	doc := `{
 "sample_spec": {"litellm_provider": "one of x", "mode": "chat"},
 "gpt-x": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":1.5e-07,"output_cost_per_token":0,
   "input_cost_per_token_priority":3e-07,"search_context_cost_per_query":{"low":0.01},"output_cost_per_reasoning_token":1e-06,
   "input_cost_per_token_batches":7.5e-08,"max_input_tokens":128000,"source":"https://example.test/pricing"},
 "gpt-tier": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":1e-06,"output_cost_per_token":2e-06,
   "input_cost_per_token_above_200k_tokens":2e-06,"input_cost_per_audio_token":1e-05},
 "gpt-image": {"litellm_provider":"openai","mode":"image_generation","input_cost_per_token":1e-06},
 "gpt-per-second": {"litellm_provider":"openai","mode":"chat","input_cost_per_second":0.001},
 "claude-ttl": {"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":3e-06,"cache_creation_input_token_cost":3.75e-06,"cache_creation_input_token_cost_above_1hr":6e-06},
 "bad": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":"cheap","output_cost_per_token":-1}
}`
	snap, err := build([]byte(doc), pricecatalog.Meta{Catalog: "litellm"})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]pricecatalog.Entry{}
	for _, e := range snap.Entries {
		by[e.ID()] = e
	}
	if len(by) != 3 {
		t.Fatalf("entries: %v", snap.Entries)
	}
	x := by["openai/gpt-x"]
	if x.Rates.Input != "0.15" || x.Rates.Output != "0" || x.Rates.CacheRead != "" || x.Batch == nil || x.Batch.Input != "0.075" || x.Batch.Output != "" {
		t.Fatalf("gpt-x rates: %+v batch %+v", x.Rates, x.Batch)
	}
	if len(x.Blocking) != 0 || strings.Join(x.LeftOut, ",") != "input_cost_per_token_priority,output_cost_per_reasoning_token,search_context_cost_per_query" || x.ContextWindow != 128000 || x.Source != "https://example.test/pricing" {
		t.Fatalf("gpt-x notes: blocking %v left out %v", x.Blocking, x.LeftOut)
	}
	// 200k is not a confirmed OpenAI threshold: the tier field stays blocking.
	// The audio rate is a modality rate OpenAI reports and is imported.
	tier := by["openai/gpt-tier"]
	if got := strings.Join(tier.Blocking, ","); got != "input_cost_per_token_above_200k_tokens" || tier.Rates.InputAudio != "10" || len(tier.Rates.Tiers) != 0 {
		t.Fatalf("gpt-tier: blocking %s rates %+v", got, tier.Rates)
	}
	ttl := by["anthropic/claude-ttl"]
	if len(ttl.Blocking) != 0 || len(ttl.LeftOut) != 0 || ttl.Rates.CacheWrite != "3.75" || ttl.Rates.CacheWrite1h != "6" {
		t.Fatalf("claude-ttl: %+v", ttl)
	}
	if _, ok := by["openai/bad"]; ok {
		t.Fatal("an entry whose only rates are invalid must not be imported")
	}
}

func TestBadRatesOnlyEntryIsDropped(t *testing.T) {
	doc := `{"bad": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":"cheap","output_cost_per_token":-1}}`
	snap, err := build([]byte(doc), pricecatalog.Meta{})
	if err != nil || len(snap.Entries) != 0 {
		t.Fatalf("entries %v err %v", snap.Entries, err)
	}
}

func TestConflictingDuplicatesAreLeftOutAndOutputIsDeterministic(t *testing.T) {
	doc := `{
 "gemini-a": {"litellm_provider":"vertex_ai-language-models","mode":"chat","input_cost_per_token":1e-06},
 "vertex_ai/gemini-a": {"litellm_provider":"vertex_ai","mode":"chat","input_cost_per_token":2e-06},
 "gemini-b": {"litellm_provider":"vertex_ai-language-models","mode":"chat","input_cost_per_token":1e-06},
 "vertex_ai/gemini-b": {"litellm_provider":"vertex_ai","mode":"chat","input_cost_per_token":1e-06}
}`
	meta := pricecatalog.Meta{Catalog: "litellm", Commit: strings.Repeat("a", 40)}
	first, err := build([]byte(doc), meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 1 || first.Entries[0].ID() != "google/gemini-b" || len(skipped) != 1 {
		t.Fatalf("entries %+v skipped %v", first.Entries, skipped)
	}
	a, _ := first.Marshal()
	second, _ := build([]byte(doc), meta)
	b, _ := second.Marshal()
	if !bytes.Equal(a, b) {
		t.Fatal("output is not deterministic")
	}
}

func entryFor(t *testing.T, doc, id string) pricecatalog.Entry {
	t.Helper()
	snap, err := build([]byte(doc), pricecatalog.Meta{Catalog: "litellm"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range snap.Entries {
		if e.ID() == id {
			return e
		}
	}
	t.Fatalf("%s not in %v", id, snap.Entries)
	return pricecatalog.Entry{}
}

func TestConfirmedTiersAreCarriedAndOthersStayBlocking(t *testing.T) {
	// Anthropic 100k and OpenAI 272k are confirmed; the same fields at an
	// unconfirmed threshold, or on an unconfirmed provider, stay blocking.
	doc := `{
 "claude-haiku": {"litellm_provider":"anthropic","mode":"chat",
   "input_cost_per_token":1e-07,"output_cost_per_token":5e-07,"cache_read_input_token_cost":1e-08,"cache_creation_input_token_cost":1.25e-07,"cache_creation_input_token_cost_above_1hr":2e-07,
   "input_cost_per_token_above_100k_tokens":5e-07,"output_cost_per_token_above_100k_tokens":2.5e-06,"cache_read_input_token_cost_above_100k_tokens":5e-08,"cache_creation_input_token_cost_above_100k_tokens":6.25e-07,"cache_creation_input_token_cost_above_1hr_above_100k_tokens":1e-06,
   "input_cost_per_token_batches":5e-08,"output_cost_per_token_batches":2.5e-07,"input_cost_per_token_above_100k_tokens_batches":2.5e-07,"output_cost_per_token_above_100k_tokens_batches":1.25e-06,
   "search_context_cost_per_query":{"search_context_size_low":0.01,"search_context_size_medium":0.01,"search_context_size_high":0.01}},
 "gpt-long": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":5e-06,"output_cost_per_token":3e-05,
   "input_cost_per_token_above_272k_tokens":1e-05,"output_cost_per_token_above_272k_tokens":4.5e-05,
   "input_cost_per_token_priority":1.25e-05,"output_cost_per_token_priority":7.5e-05,"input_cost_per_token_above_272k_tokens_priority":2.5e-05,"output_cost_per_token_above_272k_tokens_priority":1.1e-04,
   "input_cost_per_token_flex":2.5e-06,"output_cost_per_token_flex":1.5e-05,
   "search_context_cost_per_query":{"search_context_size_low":0.01,"search_context_size_medium":0.01,"search_context_size_high":0.01}},
 "gpt-odd": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":5e-06,"output_cost_per_token":3e-05,
   "input_cost_per_token_above_272k_tokens":1e-05,
   "input_cost_per_token_priority":1.25e-05,"output_cost_per_token_priority":7.5e-05},
 "gemini/g-long": {"litellm_provider":"gemini","mode":"chat","input_cost_per_token":2e-06,"output_cost_per_token":1.2e-05,
   "input_cost_per_token_above_200k_tokens":4e-06,"output_cost_per_token_above_200k_tokens":1.8e-05,
   "input_cost_per_token_priority":3.6e-06,"output_cost_per_token_priority":2.16e-05,"input_cost_per_token_above_200k_tokens_priority":7.2e-06,"output_cost_per_token_above_200k_tokens_priority":3.24e-05,
   "input_cost_per_audio_token":3e-06}
}`
	haiku := entryFor(t, doc, "anthropic/claude-haiku")
	if len(haiku.Blocking) != 0 || len(haiku.Rates.Tiers) != 1 || haiku.Rates.Tiers[0].Above != 100_000 || haiku.Rates.Tiers[0].Rates.Input != "0.5" || haiku.Rates.Tiers[0].Rates.CacheWrite1h != "1" {
		t.Fatalf("haiku: %+v blocking %v", haiku.Rates, haiku.Blocking)
	}
	if haiku.Batch == nil || len(haiku.Batch.Tiers) != 1 || haiku.Batch.Tiers[0].Rates.Output != "1.25" {
		t.Fatalf("haiku batch: %+v", haiku.Batch)
	}
	if haiku.ServerTools["web_search"] != "0.01" || len(haiku.LeftOut) != 0 {
		t.Fatalf("haiku web search: %v left out %v", haiku.ServerTools, haiku.LeftOut)
	}
	long := entryFor(t, doc, "openai/gpt-long")
	if len(long.Blocking) != 0 || len(long.Rates.Tiers) != 1 || long.Rates.Tiers[0].Above != 272_000 || long.Rates.Tiers[0].Rates.Output != "45" {
		t.Fatalf("gpt-long: %+v blocking %v", long.Rates, long.Blocking)
	}
	if long.Priority == nil || len(long.Priority.Tiers) != 1 || long.Priority.Output != "75" || long.Flex != nil {
		t.Fatalf("service tiers: %+v %+v", long.Priority, long.Flex)
	}
	// Flex states no tier rates while the standard rates have a 272k tier, so
	// it is dropped whole (a flex request above 272k would misprice). OpenAI
	// search is not imported as a per-call price (the search models report no
	// call count): it stays a left-out field.
	if long.ServerTools != nil || !strings.Contains(strings.Join(long.LeftOut, ","), "search_context_cost_per_query") || !strings.Contains(strings.Join(long.LeftOut, ","), "input_cost_per_token_flex") {
		t.Fatalf("gpt-long search: %v %v", long.ServerTools, long.LeftOut)
	}
	// A priority rate set that prices different meters than the standard one
	// cannot be imported exactly: it is dropped whole and left out.
	odd := entryFor(t, doc, "openai/gpt-odd")
	if odd.Priority != nil || strings.Join(odd.Blocking, ",") != "input_cost_per_token_above_272k_tokens" {
		t.Fatalf("gpt-odd: priority %+v blocking %v", odd.Priority, odd.Blocking)
	}
	if !strings.Contains(strings.Join(odd.LeftOut, ","), "input_cost_per_token_priority") {
		t.Fatalf("gpt-odd left out: %v", odd.LeftOut)
	}
	// Gemini's whole-prompt rule is not confirmed: tiers stay blocking, and the
	// priority rates, which would be wrong above 200k, are not imported either.
	g := entryFor(t, doc, "gemini-developer/g-long")
	if len(g.Rates.Tiers) != 0 || g.Priority != nil || g.Rates.InputAudio != "3" {
		t.Fatalf("gemini: %+v priority %+v", g.Rates, g.Priority)
	}
	if got := strings.Join(g.Blocking, ","); got != "input_cost_per_token_above_200k_tokens,output_cost_per_token_above_200k_tokens" {
		t.Fatalf("gemini blocking: %s", got)
	}
}

func TestRegenerationIsDeterministic(t *testing.T) {
	doc := `{"a":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":1e-06,"output_cost_per_token":2e-06,"input_cost_per_token_above_100k_tokens":2e-06,"output_cost_per_token_above_100k_tokens":4e-06}}`
	first, _ := build([]byte(doc), pricecatalog.Meta{})
	again, _ := build([]byte(doc), pricecatalog.Meta{})
	a, _ := first.Marshal()
	b, _ := again.Marshal()
	if !bytes.Equal(a, b) {
		t.Fatal("not deterministic")
	}
}
