package pricecatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func TestEmbeddedSnapshotMatchesRecordedChecksum(t *testing.T) {
	if err := VerifyEmbedded(); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile("snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != string(snapshotJSON) {
		t.Fatal("embedded snapshot differs from snapshot.json on disk")
	}
	broken := snapshotSum
	snapshotSum = strings.Repeat("0", 64)
	defer func() { snapshotSum = broken }()
	if VerifyEmbedded() == nil {
		t.Fatal("a wrong recorded checksum must fail verification")
	}
}

func TestEmbeddedSnapshotIsPinnedAndWellFormed(t *testing.T) {
	c, err := Parse(snapshotJSON)
	if err != nil {
		t.Fatal(err)
	}
	m := c.Meta
	if len(m.Commit) != 40 || m.Catalog != "litellm" || !strings.Contains(m.SourceURL, m.Commit) || len(m.UpstreamSHA256) != 64 {
		t.Fatalf("meta is not pinned: %+v", m)
	}
	if _, err := time.Parse("2006-01-02", m.RetrievedAt); err != nil {
		t.Fatalf("retrieved_at: %v", err)
	}
	if len(c.Entries) < 100 {
		t.Fatalf("only %d entries", len(c.Entries))
	}
	ids := map[string]string{}
	for _, e := range c.Entries {
		if e.Rates.Input == "" && e.Rates.Output == "" {
			t.Fatalf("%s states no token rate", e.ID())
		}
		plan, err := c.Convert(e, Options{})
		if err != nil {
			t.Fatalf("%s: %v", e.ID(), err)
		}
		for _, p := range plan.Prices {
			if prior, dup := ids[p.ID]; dup {
				t.Fatalf("price id %s used by %s and %s", p.ID, prior, e.ID())
			}
			ids[p.ID] = e.ID()
		}
	}
}

func TestMtokDecimalIsExact(t *testing.T) {
	for in, want := range map[string]string{
		"3e-06": "3", "1.5e-07": "0.15", "2.5e-06": "2.5", "0.00000125": "1.25",
		"1e-09": "0.001", "0": "0", "0.0": "0", "5e-07": "0.5", "1.0000001e-06": "1.0000001",
		"1.25e-05": "12.5", "7.5e-08": "0.075", "0.000003": "3",
	} {
		got, err := MtokDecimal(in)
		if err != nil || got != want {
			t.Errorf("MtokDecimal(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-1e-6", "abc", "1/3", "0x10", "NaN", "Inf"} {
		if got, err := MtokDecimal(bad); err == nil {
			t.Errorf("MtokDecimal(%q) = %q, want an error", bad, got)
		}
	}
}

func TestRatePerMtokIsExact(t *testing.T) {
	for _, c := range []struct {
		mtok string
		want usageledger.Rational
	}{
		{"3", usageledger.Rational{Numerator: 3, Denominator: 1}},
		{"0.15", usageledger.Rational{Numerator: 3, Denominator: 20}},
		{"2.5", usageledger.Rational{Numerator: 5, Denominator: 2}},
		{"1.25", usageledger.Rational{Numerator: 5, Denominator: 4}},
		{"0.075", usageledger.Rational{Numerator: 3, Denominator: 40}},
		{"0", usageledger.Rational{Numerator: 0, Denominator: 1}},
	} {
		got, err := RatePerMtok(c.mtok, 6)
		if err != nil || got != c.want {
			t.Errorf("RatePerMtok(%s) = %+v, %v; want %+v", c.mtok, got, err, c.want)
		}
	}
	if _, err := RatePerMtok("1e3", 6); err == nil {
		t.Error("exponent form must be refused")
	}
	if _, err := RatePerMtok("123456789012345678901234567890", 6); err == nil {
		t.Error("a value that does not fit int64 must be refused")
	}
}

// Awkward per-token values from the source file go through the whole chain and
// the ledger's exact estimate, with no floating point anywhere.
func TestAwkwardRatesEstimateExactly(t *testing.T) {
	perMtok := func(perToken string) string {
		s, err := MtokDecimal(perToken)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	c := &Catalog{Snapshot: Snapshot{Meta: Meta{Catalog: "litellm", Commit: strings.Repeat("a", 40), RetrievedAt: "2026-11-10", SourceURL: "https://example.test/data.json"}}}
	e := Entry{Provider: "openai", Model: "awkward", Mode: "chat", Upstream: "awkward",
		Rates: Rates{Input: perMtok("1.5e-07"), Output: perMtok("2.5e-06"), CacheRead: perMtok("0.00000125")}}
	plan, err := c.Convert(e, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 1,000,000 input tokens = 0.15 USD; 1,000,000 output = 2.5 USD; 1,000,000
	// cached = 1.25 USD. Cached tokens are removed from input, so with 600,000
	// uncached input: 0.09 + 1.25*0.4 + 2.5*0.2.
	in, out, cached := int64(1_000_000), int64(200_000), int64(400_000)
	cost := usageledger.Estimate(plan.Prices, usageledger.Record{Provider: "openai", ModelID: "awkward", Requests: 1,
		Usage: &usageledger.Usage{InputTokens: &in, OutputTokens: &out, CacheReadTokens: &cached}})
	// uncached 600,000 * 0.15/1e6 = 0.09; cached 400,000 * 1.25/1e6 = 0.5; output 200,000 * 2.5/1e6 = 0.5.
	if cost.Basis != usageledger.BasisEstimated || cost.Amount != "1090000" || cost.Scale != 6 || cost.Currency != "USD" {
		t.Fatalf("cost = %+v, want estimated 1090000 at scale 6", cost)
	}
	got := plan.Prices[0].Meters
	if got[usageledger.MeterInput] != (usageledger.Rational{Numerator: 3, Denominator: 20}) || got[usageledger.MeterCacheRead] != (usageledger.Rational{Numerator: 5, Denominator: 4}) {
		t.Fatalf("meters = %+v", got)
	}
}

func TestMissingMeterStaysUnsetAndExplicitZeroStaysZero(t *testing.T) {
	c := &Catalog{Snapshot: Snapshot{Meta: Meta{Catalog: "litellm"}}}
	plan, err := c.Convert(Entry{Provider: "anthropic", Model: "m", Rates: Rates{Input: "3", Output: "15"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := plan.Prices[0].Meters
	for _, name := range []string{usageledger.MeterCacheRead, usageledger.MeterCacheWrite, usageledger.MeterReasoning, usageledger.MeterRequests} {
		if _, set := m[name]; set {
			t.Errorf("meter %s was not stated by the source and must stay unset", name)
		}
	}
	// A priced input with an unstated output rate: output usage is unknown cost.
	in, out := int64(10), int64(5)
	only := Entry{Provider: "openai", Model: "in-only", Rates: Rates{Input: "1"}}
	plan, err = c.Convert(only, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := usageledger.Estimate(plan.Prices, usageledger.Record{Provider: "openai", ModelID: "in-only", Usage: &usageledger.Usage{InputTokens: &in, OutputTokens: &out}}); got.Basis != usageledger.BasisUnknown {
		t.Fatalf("an unstated output rate must leave cost unknown, got %+v", got)
	}
	// The source says free: 0 is kept as a declared free meter.
	plan, err = c.Convert(Entry{Provider: "openai", Model: "free", Rates: Rates{Input: "0", Output: "0"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r, set := plan.Prices[0].Meters[usageledger.MeterInput]; !set || r.Numerator != 0 || r.Denominator != 1 {
		t.Fatalf("explicit zero lost: %+v", plan.Prices[0].Meters)
	}
	got := usageledger.Estimate(plan.Prices, usageledger.Record{Provider: "openai", ModelID: "free", Usage: &usageledger.Usage{InputTokens: &in, OutputTokens: &out}})
	if got.Basis != usageledger.BasisEstimated || got.Amount != "0" {
		t.Fatalf("free model cost = %+v", got)
	}
	if _, err := c.Convert(Entry{Provider: "openai", Model: "none"}, Options{}); err == nil {
		t.Fatal("an entry with no rate must not convert")
	}
}

func TestReasoningIsDeclaredOnlyOnRequest(t *testing.T) {
	c := &Catalog{Snapshot: Snapshot{Meta: Meta{Catalog: "litellm"}}}
	e := Entry{Provider: "openai", Model: "r", Rates: Rates{Input: "1", Output: "2"}}
	plain, _ := c.Convert(e, Options{})
	if _, set := plain.Prices[0].Meters[usageledger.MeterReasoning]; set {
		t.Fatal("reasoning must not be declared by default")
	}
	declared, _ := c.Convert(e, Options{ReasoningIncluded: true})
	if r, set := declared.Prices[0].Meters[usageledger.MeterReasoning]; !set || r.Numerator != 0 {
		t.Fatalf("reasoning not declared as included: %+v", declared.Prices[0].Meters)
	}
}

func TestBatchRatesBecomeASelectorPrice(t *testing.T) {
	c := &Catalog{Snapshot: Snapshot{Meta: Meta{Catalog: "litellm"}}}
	e := Entry{Provider: "openai", Model: "b", Rates: Rates{Input: "2", Output: "8"}, Batch: &Rates{Input: "1", Output: "4"}}
	plan, err := c.Convert(e, Options{})
	if err != nil || len(plan.Prices) != 2 {
		t.Fatalf("plan: %+v, %v", plan, err)
	}
	in, out := int64(1_000_000), int64(1_000_000)
	usage := &usageledger.Usage{InputTokens: &in, OutputTokens: &out}
	std := usageledger.Estimate(plan.Prices, usageledger.Record{Provider: "openai", ModelID: "b", Usage: usage})
	batch := usageledger.Estimate(plan.Prices, usageledger.Record{Provider: "openai", ModelID: "b", Batch: true, Usage: usage})
	if std.Amount != "10000000" || batch.Amount != "5000000" {
		t.Fatalf("standard %s batch %s", std.Amount, batch.Amount)
	}
}

func TestProviderMappingAndLookup(t *testing.T) {
	for cat, want := range map[string]string{"openai": "openai", "azure-openai": "azure-openai", "bedrock": "bedrock", "gemini-developer": "gemini-developer", "google": "google", "vertex-anthropic": "anthropic", "groq": "openai-compatible"} {
		if LedgerProvider(cat) != want {
			t.Errorf("LedgerProvider(%s) = %q, want %q", cat, LedgerProvider(cat), want)
		}
	}
	if p, m, err := SplitID("groq/openai/gpt-oss-120b"); err != nil || p != "groq" || m != "openai/gpt-oss-120b" {
		t.Fatalf("SplitID: %q %q %v", p, m, err)
	}
	for _, bad := range []string{"gpt-4o", "nope/x", "openai/", "/x"} {
		if _, _, err := SplitID(bad); err == nil {
			t.Errorf("SplitID(%q) must fail", bad)
		}
	}
}

// Model ids taken from the usage-format fixtures match imported prices against
// ledger records the way live records are matched: the catalogue provider maps
// to the record's provider and the model id is the native one.
func TestImportedPricesMatchFixtureRecords(t *testing.T) {
	c, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join("..", "modelapi", "testdata", "usage-formats", "anthropic")
	modelOf := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(fixtures, name))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.Model == "" {
			t.Fatalf("%s: model: %v", name, err)
		}
		return body.Model
	}
	for _, tc := range []struct{ file, catalogProvider, recordProvider string }{
		{"messages.json", "anthropic", "anthropic"},
		{"bedrock-invoke.json", "bedrock", "bedrock"},
	} {
		model := modelOf(tc.file)
		e, ok := c.Lookup(tc.catalogProvider, model)
		if !ok {
			t.Fatalf("%s/%s (from %s) is not in the catalogue", tc.catalogProvider, model, tc.file)
		}
		plan, err := c.Convert(e, Options{})
		if err != nil {
			t.Fatal(err)
		}
		rec := usageledger.Record{Provider: tc.recordProvider, ModelID: model, Requests: 1}
		p, ok := usageledger.MatchPrice(plan.Prices, rec)
		if !ok || p.ID != PriceID(e) {
			t.Fatalf("%s: imported price does not match its record (%v)", model, ok)
		}
		// The same model id under another provider must not match.
		if _, ok := usageledger.MatchPrice(plan.Prices, usageledger.Record{Provider: "openai", ModelID: model}); ok {
			t.Fatalf("%s matched a record from another provider", model)
		}
	}
	if _, ok := c.Lookup("bedrock", "claude-opus-5-5"); ok {
		t.Fatal("no fuzzy matching: a first-party id is not a Bedrock id")
	}
}

func TestSearch(t *testing.T) {
	c, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Search("GPT-4o mini", "openai"); len(got) == 0 {
		t.Fatal("expected gpt-4o-mini")
	}
	for _, e := range c.Search("claude", "anthropic") {
		if e.Provider != "anthropic" {
			t.Fatalf("provider filter leaked %s", e.ID())
		}
	}
	if len(c.Search("no-such-model-xyz", "")) != 0 {
		t.Fatal("unexpected match")
	}
}
