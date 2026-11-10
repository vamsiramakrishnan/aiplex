package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

type backfillAPI struct {
	mu       sync.Mutex
	requests []map[string]any
}

func (a *backfillAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/usage/backfill" {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	a.mu.Lock()
	a.requests = append(a.requests, body)
	a.mu.Unlock()
	dry, _ := body["dry_run"].(bool)
	// Two pages of one eligible record each; each page total is 9.25 micro-USD
	// rounded up to 10, so the exact sum 18.5 rounds up once to 19, not 20.
	result := usageledger.BackfillResult{DryRun: dry, Scanned: 3, Eligible: 1, Skipped: usageledger.BackfillReasons{NoPrice: 2},
		Totals: []usageledger.CostTotal{{Currency: "USD", Basis: usageledger.BasisEstimated, Amount: "10", Scale: 6, Records: 1, Exact: "37/4"}}}
	if !dry {
		result.Updated = 1
	}
	if body["cursor"] == nil || body["cursor"] == "" {
		result.NextCursor = "page2"
	}
	_ = json.NewEncoder(w).Encode(result)
}

func TestUsagePricesBackfillDryRunAndRun(t *testing.T) {
	api := &backfillAPI{}
	server := httptest.NewServer(api)
	defer server.Close()
	out, err := runUsage(t, server, "table", "prices", "backfill", "--dry-run", "--since", "7d", "--model", "m", "--provider", "openai")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Scanned 6 records with unknown cost; 2 would be updated", "2 left unknown"[:1], "USD 0.000019 (estimated)"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	for _, r := range api.requests {
		if r["dry_run"] != true || r["model_id"] != "m" || r["provider"] != "openai" || r["since"] == nil {
			t.Fatalf("request %v", r)
		}
	}
	if len(api.requests) != 2 {
		t.Fatalf("expected two bounded pages, got %d requests", len(api.requests))
	}
	// Writing needs --yes when there is no terminal, and does the dry run first.
	api.requests = nil
	if _, err := runUsage(t, server, "table", "prices", "backfill"); err == nil {
		t.Fatal("writing without a terminal needs --yes")
	}
	for _, r := range api.requests {
		if r["dry_run"] != true {
			t.Fatalf("nothing may be written before confirmation: %v", r)
		}
	}
	api.requests = nil
	out, err = runUsage(t, server, "table", "prices", "backfill", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 updated") || !strings.Contains(out, "backfilled_at") {
		t.Fatalf("output:\n%s", out)
	}
	writes := 0
	for _, r := range api.requests {
		if r["dry_run"] != true {
			writes++
		}
	}
	if writes != 2 {
		t.Fatalf("expected two write pages, got %d", writes)
	}
}

func TestFormatCostMarksBackfilledCosts(t *testing.T) {
	now := usageledger.Cost{Basis: usageledger.BasisEstimated, Currency: "USD", Amount: "1500", Scale: 6}
	if got := aiplex.FormatCost(now); got != "USD 0.001500 (estimated)" {
		t.Fatalf("%q", got)
	}
	at := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	now.BackfilledAt = &at
	if got := aiplex.FormatCost(now); got != "USD 0.001500 (estimated, backfilled)" {
		t.Fatalf("%q", got)
	}
}
