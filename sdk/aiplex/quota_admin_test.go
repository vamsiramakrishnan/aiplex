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
