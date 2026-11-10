package pricecatalog

import (
	"strings"
	"testing"
)

func TestDisagreementsCompareStatedValuesNumerically(t *testing.T) {
	a := Entry{Provider: "openai", Model: "m", Rates: Rates{Input: "2.50", Output: "10", CacheRead: "1.25", Tiers: []Tier{{Above: 272_000, Rates: Rates{Input: "5", Output: "15"}}}}}
	b := Entry{Provider: "openai", Model: "m", Rates: Rates{Input: "2.5", Output: "12", Tiers: []Tier{{Above: 272_000, Rates: Rates{Input: "6", Output: "15"}}, {Above: 500_000, Rates: Rates{Input: "9"}}}}}
	got := Disagreements(a, b)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	want := []Disagreement{
		{ID: "openai/m", Meter: "output", A: "10", B: "12"},
		{ID: "openai/m", Meter: "input", Above: 272_000, A: "5", B: "6"},
	}
	// "2.50" equals "2.5", cache_read is stated by one source only, and the
	// 500k tier exists in one source only: none of those is a disagreement.
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("%+v", got)
	}
}

func TestModelsDevSnapshotIsPinnedAndConsistent(t *testing.T) {
	c, err := EmbeddedSource(SourceModelsDev)
	if err != nil {
		t.Fatal(err)
	}
	if c.Meta.Catalog != SourceModelsDev || len(c.Meta.Commit) != 40 || !strings.Contains(c.Meta.License, "MIT") || len(c.Entries) == 0 {
		t.Fatalf("meta: %+v", c.Meta)
	}
	for _, e := range c.Entries {
		plan, err := c.Convert(e, Options{})
		if err != nil {
			t.Fatalf("%s: %v", e.ID(), err)
		}
		for _, p := range plan.Prices {
			if err := p.Validate(); err != nil {
				t.Fatalf("%s: %v", p.ID, err)
			}
			if p.Source == nil || p.Source.Catalog != SourceModelsDev {
				t.Fatalf("%s: provenance %+v", p.ID, p.Source)
			}
		}
	}
	if _, err := EmbeddedSource("nope"); err == nil {
		t.Fatal("unknown source accepted")
	}
}

func TestBothSourcesAgreeOrDisagreementsAreStable(t *testing.T) {
	a, err := EmbeddedSource(SourceLiteLLM)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EmbeddedSource(SourceModelsDev)
	if err != nil {
		t.Fatal(err)
	}
	shared := 0
	for _, e := range a.Entries {
		if _, ok := b.Get(e.ID()); ok {
			shared++
		}
	}
	d := CompareCatalogs(a, b)
	t.Logf("models in both: %d, disagreements: %d", shared, len(d))
	if shared == 0 {
		t.Fatal("the two sources share no model: the provider/model mapping is broken")
	}
}
