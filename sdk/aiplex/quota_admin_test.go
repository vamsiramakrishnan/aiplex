package aiplex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestQuotaAdministrationSDKScopesTenantAtServerAndCarriesReviewDigest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer accepted-jwt" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/api/v1/quota/reconciliations":
			if r.Header.Get("Idempotency-Key") != "plan-key" {
				t.Error("planning key missing")
			}
			var input map[string]any
			_ = json.NewDecoder(r.Body).Decode(&input)
			if _, ok := input["tenant_id"]; ok {
				t.Error("client tenant treated as API authority")
			}
			if input["evidence_ref"] != "review/evidence" {
				t.Error(input)
			}
			_ = json.NewEncoder(w).Encode(quotaplex.ReconciliationPlan{ID: "review", Digest: "reviewed-digest"})
		case "/api/v1/quota/reconciliations/review/apply":
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input["digest"] != "reviewed-digest" || r.Header.Get("Idempotency-Key") != "approve-key" {
				t.Error("exact review not bound")
			}
			_ = json.NewEncoder(w).Encode(quotaplex.ReconciliationReceipt{PlanID: "review", PlanDigest: "reviewed-digest", Decision: quotaplex.Decision{State: "settled"}})
		case "/api/v1/quota/inventory":
			if r.URL.Query().Get("after") != "cursor with & reserved" || r.URL.Query().Get("limit") != "20" {
				t.Error("unsafe inventory query", r.URL.String())
			}
			_ = json.NewEncoder(w).Encode(quotaplex.AdministrationPage{Kind: "attempts", Done: true})
		case "/api/v1/quota/loans":
			var input map[string]any
			_ = json.NewDecoder(r.Body).Decode(&input)
			if _, ok := input["tenant_id"]; ok {
				t.Error("loan tenant body sent")
			}
			_ = json.NewEncoder(w).Encode(quotaplex.LoanInfo{State: "open"})
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := aiplex.NewClient(server.URL)
	c.SetToken("accepted-jwt")
	ctx := context.Background()
	plan, err := c.PlanQuotaReconciliation(ctx, quotaplex.ReconciliationRequest{TenantID: "caller-body-not-authority", ID: "review", AttemptID: "attempt", Reason: "reviewed correction", EvidenceRef: "review/evidence", Usage: quotaplex.Vector{"requests": 1}, ExpiresAt: time.Now().Add(time.Minute)}, "plan-key")
	if err != nil || plan.Digest != "reviewed-digest" {
		t.Fatal(plan, err)
	}
	receipt, err := c.ApplyQuotaReconciliation(ctx, plan.ID, plan.Digest, "approve-key")
	if err != nil || receipt.Decision.State != "settled" {
		t.Fatal(receipt, err)
	}
	if _, err = c.GetQuotaInventory(ctx, "attempts", "cursor with & reserved", 20); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GrantQuotaLoan(ctx, quotaplex.LoanRequest{TenantID: "not-authority", ID: "loan", RuleID: "lend", Amount: 1, ExpiresAt: time.Now().Add(time.Minute)}, "loan-key"); err != nil {
		t.Fatal(err)
	}
	before := requests
	for _, id := range []string{"../other", "a/b", "a%2fb", ""} {
		if _, err = c.ApplyQuotaReconciliation(ctx, id, "digest", "key"); err == nil {
			t.Fatal("unsafe plan identifier", id)
		}
		if _, err = c.GetQuotaBalance(ctx, id); err == nil {
			t.Fatal("unsafe balance identifier", id)
		}
	}
	if _, err = c.ApplyQuotaReconciliation(ctx, "review", "", "key"); err == nil {
		t.Fatal("empty reviewed digest")
	}
	if _, err = c.PlanQuotaReconciliation(ctx, quotaplex.ReconciliationRequest{}, ""); err == nil {
		t.Fatal("empty idempotency key")
	}
	if _, err = c.GetQuotaInventory(ctx, "policy-head", "", 20); err == nil {
		t.Fatal("unsupported inventory")
	}
	if requests != before {
		t.Fatal("invalid requests reached server")
	}
}

func TestQuotaUnknownAttemptsSDKSendsTheFilterAndKeepsUnknownNull(t *testing.T) {
	var requests int
	answer := `{"kind":"attempts","records":[],"done":true,"older_than":"1h30m0s","unknown":{"as_of":"2026-11-09T14:00:00Z","attempts":2,"oldest_dispatch_at":"2026-11-09T12:00:00Z","exposure":[{"meter_id":"usd","unit":"money","currency":"USD","scale":6,"quantity":null,"known_part":150,"uncomputed_attempts":1,"attempts":2}]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/quota/inventory" || q.Get("kind") != "attempts" || q.Get("older_than") != "1h30m0s" || q.Get("limit") != "64" || q.Get("after") != "cursor" {
			t.Error("unexpected request", r.URL.String())
		}
		_, _ = w.Write([]byte(answer))
	}))
	defer server.Close()
	c := aiplex.NewClient(server.URL)
	page, err := c.GetQuotaUnknownAttempts(context.Background(), 90*time.Minute, "cursor", 64)
	if err != nil || page.Unknown == nil || page.Unknown.Attempts != 2 {
		t.Fatal(page, err)
	}
	usd := page.Unknown.Exposure[0]
	if usd.Quantity != nil || usd.KnownPart != 150 || usd.UncomputedAttempts != 1 {
		t.Fatalf("an unknown amount must stay unknown: %+v", usd)
	}
	if age, ok := page.Unknown.OldestAge(); !ok || age != 2*time.Hour {
		t.Fatal(age, ok)
	}
	before := requests
	if _, err = c.GetQuotaUnknownAttempts(context.Background(), -time.Second, "", 20); err == nil {
		t.Fatal("negative age accepted")
	}
	if _, err = c.GetQuotaUnknownAttempts(context.Background(), time.Hour, "", 65); err == nil {
		t.Fatal("unbounded page accepted")
	}
	if requests != before {
		t.Fatal("invalid requests reached the server")
	}
	// A server that ignores the filter must not read as "no unknown attempts".
	answer = `{"kind":"attempts","records":[],"done":true}`
	if _, err = c.GetQuotaUnknownAttempts(context.Background(), 90*time.Minute, "cursor", 64); err == nil {
		t.Fatal("a missing summary was accepted as none")
	}
}
