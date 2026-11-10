package main

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/pricecatalog"
)

func TestMapIDKeepsExactIdsOnly(t *testing.T) {
	for _, c := range []struct {
		dir, id, provider string
		ok                bool
	}{
		{"openai", "gpt-4o", "openai", true},
		{"openai", "azure/gpt-4o", "", false},
		{"anthropic", "claude-haiku-4-5", "anthropic", true},
		{"azure", "gpt-4o", "azure-openai", true},
		{"amazon-bedrock", "anthropic.claude-sonnet-4-6", "bedrock", true},
		{"amazon-bedrock", "claude-sonnet", "", false},
		{"google", "gemini-2.5-flash", "gemini-developer", true},
		{"google-vertex", "gemini-3-pro-preview", "google", true},
		{"google-vertex", "claude-haiku-4-5@20251001", "", false},
		{"google-vertex-anthropic", "claude-haiku-4-5@20251001", "vertex-anthropic", true},
		{"google-vertex-anthropic", "claude-haiku-4-5", "", false},
		{"groq", "openai/gpt-oss-120b", "groq", true},
		{"openrouter", "anthropic/claude-opus-4.6", "", false},
	} {
		p, _, ok := mapID(c.dir, c.id)
		if ok != c.ok || (ok && p != c.provider) {
			t.Errorf("mapID(%s, %s) = %q %v; want %q %v", c.dir, c.id, p, ok, c.provider, c.ok)
		}
	}
}

const haiku = `
[cost.text.input]
price = 0.1
cache = { read = 0.01, write = { "5m" = 0.125, "1h" = 0.2 } }

[[cost.text.input.overrides]]
when = { context = { gt = 100_000 } }
price = 0.5
cache = { read = 0.05, write = { "5m" = 0.625, "1h" = 1 } }

[cost.text.output]
price = 0.5

[[cost.text.output.overrides]]
when = { context = { gt = 100_000 } }
price = 2.5

[cost.web_search]
price = 10

[[cost.multipliers]]
when = { region = { equals = "us" } }
factor = { text = 1.1 }
`

func TestBuildReadsRatesTiersAndWebSearchExactly(t *testing.T) {
	files := map[string][]byte{
		"providers/anthropic/models/claude-haiku-5-5.toml": []byte(haiku),
		// 200k is not a confirmed threshold for OpenAI, so it stays blocking.
		"providers/openai/models/gpt-x.toml": []byte("[cost.text.input]\nprice = 2.5\ncache = { read = 1.25 }\n[[cost.text.input.overrides]]\nwhen = { context = { gt = 200_000 } }\nprice = 5\n[cost.text.output]\nprice = 10\n[cost.audio.input]\nprice = 40\n"),
		// A condition other than a plain context threshold is never read as a tier.
		"providers/openai/models/gpt-y.toml":   []byte("[cost.text.input]\nprice = 1\n[[cost.text.input.overrides]]\nwhen = { context = { gte = 272_000 } }\nprice = 2\n[cost.text.output]\nprice = 4\n"),
		"providers/openrouter/models/a/b.toml": []byte("[cost.text.input]\nprice = 1\n[cost.text.output]\nprice = 1\n"),
		"providers/openai/models/free.toml":    []byte("[limit]\ncontext = 1000\n"),
	}
	snap, err := build(files, pricecatalog.Meta{Catalog: "models.dev", SourceURL: "https://example.test/tree/abc/providers"})
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
	h := by["anthropic/claude-haiku-5-5"]
	if h.Rates.Input != "0.1" || h.Rates.CacheWrite1h != "0.2" || len(h.Rates.Tiers) != 1 || h.Rates.Tiers[0].Above != 100_000 || h.Rates.Tiers[0].Rates.Output != "2.5" || h.Rates.Tiers[0].Rates.CacheWrite1h != "1" {
		t.Fatalf("haiku: %+v", h.Rates)
	}
	if h.ServerTools["web_search"] != "0.01" || len(h.Blocking) != 0 || strings.Join(h.LeftOut, ",") != "cost.multipliers[when:region]" {
		t.Fatalf("haiku notes: tools %v blocking %v left out %v", h.ServerTools, h.Blocking, h.LeftOut)
	}
	if !strings.HasSuffix(h.Source, "/blob/abc/providers/anthropic/models/claude-haiku-5-5.toml") {
		t.Fatalf("source %q", h.Source)
	}
	x := by["openai/gpt-x"]
	if len(x.Rates.Tiers) != 0 || strings.Join(x.Blocking, ",") != "cost.audio,cost.text.overrides[context>200000]" {
		t.Fatalf("gpt-x: %+v blocking %v", x.Rates, x.Blocking)
	}
	y := by["openai/gpt-y"]
	if len(y.Rates.Tiers) != 0 || strings.Join(y.Blocking, ",") != "cost.text.input.overrides[when:context]" {
		t.Fatalf("gpt-y: %+v blocking %v", y.Rates, y.Blocking)
	}
	if _, routed := by["openai/a/b"]; routed {
		t.Fatal("a router provider's nested id must not be imported")
	}
}

func TestExactNumberRefusesInexactLiterals(t *testing.T) {
	for v, want := range map[any]string{int64(5): "5", 0.1: "0.1", 0.125: "0.125", 1e-7: "0.0000001", 12.5: "12.5"} {
		if got, ok := exactNumber(v); !ok || got != want {
			t.Errorf("exactNumber(%v) = %q %v, want %q", v, got, ok, want)
		}
	}
	for _, v := range []any{-1.0, 0.1234567890123456789, "5", nil} {
		if _, ok := exactNumber(v); ok {
			t.Errorf("exactNumber(%v) must refuse", v)
		}
	}
}
