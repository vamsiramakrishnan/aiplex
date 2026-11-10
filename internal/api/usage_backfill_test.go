package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func backfillPost(t *testing.T, h *UsageHandler, body string, access *models.ResolvedAccess, want int) []byte {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/usage/backfill", strings.NewReader(body))
	ctx := r.Context()
	if access != nil {
		identity := access.Identity
		ctx = context.WithValue(ctx, keyWIFIdentity, &identity)
		ctx = context.WithValue(ctx, keyWIFAccess, access)
	}
	w := httptest.NewRecorder()
	h.Backfill(w, r.WithContext(ctx))
	if w.Code != want {
		t.Fatalf("POST backfill %s: expected %d, got %d: %s", body, want, w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func TestUsageBackfillAPI(t *testing.T) {
	store := usageledger.NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	add := func(id string, at time.Time, model string) {
		r := usageledger.Record{ID: id, TenantID: "tenant", StartedAt: at, FinishedAt: at, Plane: usageledger.PlaneLLM, Provider: "openai", ModelID: model, Status: usageledger.StatusOK, Source: usageledger.SourceManaged, Requests: 1,
			Usage: &usageledger.Usage{InputTokens: usageledger.Int64(1_000_000), OutputTokens: usageledger.Int64(1_000_000)}, Cost: usageledger.UnknownCost()}
		if err := store.Append(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	add("a", now.Add(-3*time.Hour), "m")
	add("b", now.Add(-2*time.Hour), "m")
	add("c", now.Add(-time.Hour), "other")
	// The price starts one hour ago: the two older records are outside it.
	from := now.Add(-150 * time.Minute)
	price := usageledger.Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, EffectiveFrom: &from,
		Meters: map[string]usageledger.Rational{usageledger.MeterInput: {Numerator: 2, Denominator: 1}, usageledger.MeterOutput: {Numerator: 8, Denominator: 1}}}
	h := NewUsageHandler(store, func(*http.Request) (string, error) { return "tenant", nil }, false)
	h.Prices = func(context.Context, string) ([]usageledger.Price, error) { return []usageledger.Price{price}, nil }

	// Not an admin: refused (the route policy also requires the admin role).
	backfillPost(t, h, `{}`, viewerAccess("bob"), http.StatusForbidden)
	backfillPost(t, h, `{"surprise":1}`, adminAccess("root"), http.StatusBadRequest)
	backfillPost(t, h, `{"limit":5000}`, adminAccess("root"), http.StatusBadRequest)
	backfillPost(t, h, `{"cursor":"nope"}`, adminAccess("root"), http.StatusBadRequest)

	var dry usageledger.BackfillResult
	if err := json.Unmarshal(backfillPost(t, h, `{"dry_run":true}`, adminAccess("root"), http.StatusOK), &dry); err != nil {
		t.Fatal(err)
	}
	// Only "b" is inside the price window: 1e6*2 + 1e6*8 = 10 USD = 10,000,000 micro-USD.
	if !dry.DryRun || dry.Scanned != 3 || dry.Eligible != 1 || dry.Updated != 0 || dry.Skipped.NoPrice != 2 || len(dry.Totals) != 1 || dry.Totals[0].Amount != "10000000" {
		t.Fatalf("dry run: %+v", dry)
	}
	if rec, _ := store.Get(context.Background(), "tenant", "b"); rec.Cost.Basis != usageledger.BasisUnknown {
		t.Fatal("a dry run wrote")
	}
	var real usageledger.BackfillResult
	if err := json.Unmarshal(backfillPost(t, h, `{"model_id":"m"}`, adminAccess("root"), http.StatusOK), &real); err != nil {
		t.Fatal(err)
	}
	if real.Updated != 1 || real.Scanned != 2 {
		t.Fatalf("real run: %+v", real)
	}
	rec, _ := store.Get(context.Background(), "tenant", "b")
	if rec.Cost.Basis != usageledger.BasisEstimated || rec.Cost.PriceID != "p" || rec.Cost.BackfilledAt == nil || rec.Cost.Amount != "10000000" {
		t.Fatalf("record b: %+v", rec.Cost)
	}
	// Summaries now include the backfilled cost, and the filter finds what remains unknown.
	var summary usageledger.Summary
	if err := json.Unmarshal(usageGet(t, h.Summary, "/api/v1/usage/summary", adminAccess("root"), 200), &summary); err != nil {
		t.Fatal(err)
	}
	if g := summary.Groups[0]; len(g.Cost) != 1 || g.Cost[0].Amount != "10000000" || g.UnknownCostRecords != 2 {
		t.Fatalf("summary: %+v", g)
	}
	var page usageledger.Page
	if err := json.Unmarshal(usageGet(t, h.ListRecords, "/api/v1/usage/records?cost_basis=unknown", adminAccess("root"), 200), &page); err != nil || len(page.Records) != 2 {
		t.Fatalf("cost_basis filter: %+v %v", page, err)
	}
	usageGet(t, h.ListRecords, "/api/v1/usage/records?cost_basis=free", adminAccess("root"), 400)
	// Idempotent.
	var again usageledger.BackfillResult
	if err := json.Unmarshal(backfillPost(t, h, `{}`, adminAccess("root"), http.StatusOK), &again); err != nil || again.Updated != 0 || again.Eligible != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
}
