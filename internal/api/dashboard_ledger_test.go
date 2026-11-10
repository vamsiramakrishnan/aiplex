package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func dashboardStats(t *testing.T, ledger usageledger.Store) models.DashboardStats {
	t.Helper()
	h := api.NewDashboardHandler(registry.NewMemoryStore())
	h.Ledger = ledger
	h.Tenant = func(*http.Request) (string, error) { return "t", nil }
	w := httptest.NewRecorder()
	h.GetStats(w, httptest.NewRequest("GET", "/api/v1/dashboard/stats", nil))
	if w.Code != 200 {
		t.Fatalf("stats: %d %s", w.Code, w.Body.String())
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	var s models.DashboardStats
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func ledgerRecord(id string, at time.Time, plane string) usageledger.Record {
	return usageledger.Record{ID: id, TenantID: "t", StartedAt: at, FinishedAt: at.Add(time.Second), Plane: plane, Status: usageledger.StatusOK, Source: usageledger.SourceManaged, Requests: 1, Cost: usageledger.UnknownCost()}
}

// The 24-hour figures come from the ledger; a record that carried no price or
// no tokens is counted as unknown, not as zero, and a ledger with only unknown
// cost leaves the cost null.
func TestDashboardStatsComeFromTheLedger(t *testing.T) {
	ctx := context.Background()
	ledger := usageledger.NewMemoryStore()
	now := time.Now()
	known := ledgerRecord("a", now.Add(-time.Hour), usageledger.PlaneLLM)
	in, out := int64(10), int64(5)
	known.Usage = &usageledger.Usage{InputTokens: &in, OutputTokens: &out}
	known.Cost = usageledger.Cost{Basis: usageledger.BasisEstimated, Currency: "USD", Amount: "1500", Scale: 6}
	unknown := ledgerRecord("b", now.Add(-2*time.Hour), usageledger.PlaneLLM)
	tool := ledgerRecord("c", now.Add(-3*time.Hour), usageledger.PlaneMCP)
	tool.ToolName = "echo"
	old := ledgerRecord("d", now.Add(-48*time.Hour), usageledger.PlaneLLM)
	for _, r := range []usageledger.Record{known, unknown, tool, old} {
		if err := ledger.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	s := dashboardStats(t, ledger)
	if s.UsageSource != "ledger" || s.DailyRequests == nil || *s.DailyRequests != 3 {
		t.Fatalf("requests = %v source %q", s.DailyRequests, s.UsageSource)
	}
	if s.DailyCostUSD == nil || *s.DailyCostUSD != 0.0015 || s.DailyUnknownCostRecords != 2 {
		t.Fatalf("cost = %v unknown %d", s.DailyCostUSD, s.DailyUnknownCostRecords)
	}
	if s.DailyTokens == nil || *s.DailyTokens != 15 || s.DailyUnknownUsageRecords != 2 {
		t.Fatalf("tokens = %v unknown %d", s.DailyTokens, s.DailyUnknownUsageRecords)
	}
	if s.ToolCalls == nil || *s.ToolCalls != 1 {
		t.Fatalf("tool calls = %v", s.ToolCalls)
	}

	// Only unknown records: cost and tokens are null, not 0.
	only := usageledger.NewMemoryStore()
	if err := only.Append(ctx, ledgerRecord("x", now.Add(-time.Hour), usageledger.PlaneLLM)); err != nil {
		t.Fatal(err)
	}
	s = dashboardStats(t, only)
	if s.DailyCostUSD != nil || s.DailyTokens != nil || s.DailyRequests == nil || *s.DailyRequests != 1 {
		t.Fatalf("unknown reported as zero: cost %v tokens %v requests %v", s.DailyCostUSD, s.DailyTokens, s.DailyRequests)
	}
}
