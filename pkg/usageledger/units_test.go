package usageledger

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func unitRecord(usage *Usage) Record {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	return Record{ID: "ul_x", TenantID: "t", StartedAt: now, FinishedAt: now, Plane: PlaneLLM, Provider: "openai", ModelID: "m", Status: StatusOK, Source: SourceManaged, Requests: 1, Usage: usage, Cost: UnknownCost()}
}

func TestUnitCountersAreHeldBesideTokens(t *testing.T) {
	u := FromCounters(map[string]int64{"input_tokens": 14, "output_tokens": 101, CounterInputAudioTokens: 4, CounterInputTextTokens: 10, CounterAudioInputMilliseconds: 3000, CounterGeneratedImages: 2})
	if u == nil || *u.InputTokens != 14 || *u.InputAudioTokens != 4 || *u.AudioInputMilliseconds != 3000 || *u.GeneratedImages != 2 {
		t.Fatalf("units not mapped: %+v", u)
	}
	if u.VideoOutputMilliseconds != nil || u.OutputAudioTokens != nil {
		t.Fatal("unreported unit became known")
	}
	// Units alone make usage known; tokens stay unknown.
	only := FromCounters(map[string]int64{CounterAudioInputMilliseconds: 3000})
	if only == nil || only.InputTokens != nil || *only.AudioInputMilliseconds != 3000 {
		t.Fatalf("duration-only usage: %+v", only)
	}
	raw, _ := json.Marshal(unitRecord(only))
	if !strings.Contains(string(raw), `"audio_input_milliseconds":3000`) || !strings.Contains(string(raw), `"input_tokens":null`) {
		t.Fatalf("json: %s", raw)
	}
	// Records without units keep their previous JSON shape.
	plain, _ := json.Marshal(unitRecord(FromCounters(map[string]int64{"input_tokens": 1})))
	for _, field := range []string{"generated_images", "batch", "image_size", "modality"} {
		if strings.Contains(string(plain), field) {
			t.Fatalf("plain record grew %s: %s", field, plain)
		}
	}
	clone := unitRecord(u).Clone()
	*clone.Usage.GeneratedImages = 9
	if *u.GeneratedImages != 2 {
		t.Fatal("clone shares unit pointers")
	}
	bad := unitRecord(&Usage{UnitUsage: UnitUsage{GeneratedImages: Int64(-1)}})
	if bad.Validate() == nil {
		t.Fatal("negative unit accepted")
	}
}

func TestSummaryKeepsUnitsOutOfTokenTotals(t *testing.T) {
	agg := NewAggregator(SummaryQuery{GroupBy: []string{}})
	a := unitRecord(&Usage{InputTokens: Int64(10), UnitUsage: UnitUsage{AudioInputMilliseconds: Int64(1500), InputAudioTokens: Int64(4)}})
	b := unitRecord(&Usage{UnitUsage: UnitUsage{GeneratedImages: Int64(3)}})
	b.Batch = true
	for _, r := range []Record{a, b} {
		if err := agg.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	g := agg.Result().Groups[0]
	if g.Tokens[CounterInput] != 10 || g.Units[CounterAudioInputMilliseconds] != 1500 || g.Units[CounterGeneratedImages] != 3 || g.UnitRecords[CounterGeneratedImages] != 1 || g.ModalityTokens[CounterInputAudioTokens] != 4 || g.BatchRecords != 1 {
		t.Fatalf("summary: %+v", g)
	}
	if _, mixed := g.Tokens[CounterGeneratedImages]; mixed {
		t.Fatal("units mixed into token totals")
	}
	if g.UnknownUsageRecords != 0 {
		t.Fatal("unit-only usage counted as unknown")
	}
}

func unitPrice(meters map[string]Rational) Price {
	return Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: meters}
}

func TestEstimatePricesUnitsAndModalitySplits(t *testing.T) {
	one := Rational{Numerator: 1, Denominator: 1}
	// Duration-billed transcription: priced per millisecond only.
	r := unitRecord(&Usage{UnitUsage: UnitUsage{AudioInputMilliseconds: Int64(3000)}})
	if c := Estimate([]Price{unitPrice(map[string]Rational{CounterAudioInputMilliseconds: {Numerator: 1, Denominator: 1000}, MeterRequests: {Denominator: 1}})}, r); c.Basis != BasisEstimated || c.Amount != "3" {
		t.Fatalf("duration cost: %+v", c)
	}
	// An unpriced known unit makes cost unknown.
	if c := Estimate([]Price{unitPrice(map[string]Rational{MeterInput: one, MeterRequests: one})}, r); c.Basis != BasisUnknown {
		t.Fatalf("unpriced unit was priced: %+v", c)
	}
	// Audio input tokens priced separately are removed from input once.
	tokens := unitRecord(&Usage{InputTokens: Int64(14), OutputTokens: Int64(101), UnitUsage: UnitUsage{InputAudioTokens: Int64(4)}})
	c := Estimate([]Price{unitPrice(map[string]Rational{MeterInput: one, MeterOutput: one, CounterInputAudioTokens: {Numerator: 10, Denominator: 1}})}, tokens)
	if c.Amount != "151" { // 10 text*1 + 4 audio*10 + 101 output
		t.Fatalf("modality cost: %+v", c)
	}
	// A priced split that was not reported cannot be split out of input.
	missing := unitRecord(&Usage{InputTokens: Int64(14), OutputTokens: Int64(1)})
	if c := Estimate([]Price{unitPrice(map[string]Rational{MeterInput: one, MeterOutput: one, CounterInputAudioTokens: one})}, missing); c.Basis != BasisUnknown {
		t.Fatalf("unknown split priced: %+v", c)
	}
	// Cache and modality subsets may overlap: only exact when cache is zero.
	overlap := unitRecord(&Usage{InputTokens: Int64(14), OutputTokens: Int64(1), CacheReadTokens: Int64(5), UnitUsage: UnitUsage{InputAudioTokens: Int64(4)}})
	if c := Estimate([]Price{unitPrice(map[string]Rational{MeterInput: one, MeterOutput: one, MeterCacheRead: one, CounterInputAudioTokens: one})}, overlap); c.Basis != BasisUnknown {
		t.Fatalf("overlapping subsets priced: %+v", c)
	}
}

func TestPriceSelectorsMatchBatchAndImageDimensions(t *testing.T) {
	one := Rational{Numerator: 1, Denominator: 1}
	yes, no := true, false
	standard := unitPrice(map[string]Rational{CounterGeneratedImages: {Numerator: 100, Denominator: 1}, MeterRequests: {Denominator: 1}})
	batch := standard
	batch.ID, batch.Batch, batch.Meters = "batch", &yes, map[string]Rational{CounterGeneratedImages: {Numerator: 50, Denominator: 1}, MeterRequests: {Denominator: 1}}
	large := standard
	large.ID, large.ImageSize, large.ImageQuality, large.Meters = "large", "1024x1536", "high", map[string]Rational{CounterGeneratedImages: {Numerator: 250, Denominator: 1}, MeterRequests: one}
	r := unitRecord(&Usage{UnitUsage: UnitUsage{GeneratedImages: Int64(2)}})
	if c := Estimate([]Price{standard, batch, large}, r); c.Amount != "200" {
		t.Fatalf("standard: %+v", c)
	}
	r.Batch = true
	if c := Estimate([]Price{standard, batch, large}, r); c.Amount != "100" {
		t.Fatalf("batch: %+v", c)
	}
	r.Batch, r.ImageSize, r.ImageQuality = false, "1024x1536", "high"
	if c := Estimate([]Price{standard, batch, large}, r); c.Amount != "501" {
		t.Fatalf("dimensions: %+v", c)
	}
	nonBatch := standard
	nonBatch.ID, nonBatch.Batch = "nonbatch", &no
	r = unitRecord(&Usage{UnitUsage: UnitUsage{GeneratedImages: Int64(1)}})
	r.Batch = true
	if _, ok := MatchPrice([]Price{nonBatch}, r); ok {
		t.Fatal("batch:false price matched a batch record")
	}
	other := batch
	other.ID = "batch-2"
	if _, ok := MatchPrice([]Price{batch, other}, r); ok {
		t.Fatal("equally specific prices must not match")
	}
	if _, err := DecodePrice("p", []byte(`{"id":"p","model_id":"m","currency":"USD","scale":6,"meters":{"generated_images":{"numerator":1,"denominator":1}},"batch":true,"image_size":"1024x1024"}`)); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
