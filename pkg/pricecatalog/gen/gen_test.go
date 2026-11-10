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
	if got := strings.Join(by["openai/gpt-tier"].Blocking, ","); got != "input_cost_per_audio_token,input_cost_per_token_above_200k_tokens" {
		t.Fatalf("gpt-tier blocking: %s", got)
	}
	ttl := by["anthropic/claude-ttl"]
	if len(ttl.Blocking) != 0 || strings.Join(ttl.LeftOut, ",") != "cache_creation_input_token_cost_above_1hr" || ttl.Rates.CacheWrite != "3.75" {
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
