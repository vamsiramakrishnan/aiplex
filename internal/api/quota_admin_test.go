package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

func adminRequest(method, path, body, principal, key string, ingressScopes []string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	identity := &models.WIFIdentity{PrincipalID: principal, Subject: principal}
	ctx := context.WithValue(r.Context(), keyWIFIdentity, identity)
	ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: *identity, Scopes: ingressScopes})
	ctx = context.WithValue(ctx, keyRequestID, "request")
	return r.WithContext(ctx)
}
func adminHTTPFixture(t *testing.T) (*quotaHostTestFixture, *ConnectedHandler, http.Handler, *[]string) {
	t.Helper()
	f := quotaHostTestNew(t)
	grants := []string{"aiplex:quota:*"}
	h := NewConnectedHandler(nil, nil, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	h.Quota = f.host
	h.ResolveAccess = func(_ context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
		return &models.ResolvedAccess{Identity: *identity, Scopes: append([]string{}, grants...)}, nil
	}
	router := chi.NewRouter()
	h.MountQuotaAdministration(router)
	return f, h, router, &grants
}
func TestQuotaAdministrationUsesFreshActionGrantsAndVerifiedTenant(t *testing.T) {
	f, h, router, grants := adminHTTPFixture(t)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("GET", "/quota/policy", "", "reader", "", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var state quotaplex.PolicyInspection
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state.Reference != state.Active || state.Reference.Revision != 1 {
		t.Fatal(state, err)
	}
	*grants = []string{"aiplex:quota:read"}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("POST", "/quota/loans", `{"id":"loan","rule_id":"x","amount":1}`, "reader", "op", []string{"aiplex:quota:*"}))
	if w.Code != 403 {
		t.Fatal("stale ingress write grant used", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/quota/policy", nil))
	if w.Code != 401 {
		t.Fatal("missing identity accepted", w.Code)
	}
	*grants = []string{"aiplex:quota:*"}
	body := `{"tenant_id":"other","id":"loan","rule_id":"x","amount":1}`
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("POST", "/quota/loans", body, "reader", "op", nil))
	if w.Code != 400 {
		t.Fatal("caller tenant accepted in loan body", w.Code, w.Body.String())
	}
	h.Tenant = func(*http.Request) (string, error) { return "other", nil }
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("GET", "/quota/policy", "", "reader", "", nil))
	if w.Code != 404 {
		t.Fatal("cross tenant policy disclosed", w.Code)
	}
	if page, err := f.host.Engines()["tenant"].Inventory(context.Background(), "tenant", "audit", "", 20); err != nil || len(page.Records) != 0 {
		t.Fatal("denied operations mutated administration", page, err)
	}
}
func TestQuotaAdministrationReconciliationMountPreservesNativeFacts(t *testing.T) {
	f, _, router, grants := adminHTTPFixture(t)
	identity := quotaHostTestIdentity(t, f.now, nil)
	r, in := quotaHostTestInvocation(identity, "native", "{}")
	lease, err := f.host.Prepare(context.Background(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.BeforeDispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = lease.Observe(context.Background(), execution.Observation{Source: "upstream", ModelVersion: "different-native-version", Counters: map[string]int64{"requests": 1}, Cumulative: true, Terminal: true}); err == nil {
		t.Fatal("native version mismatch did not fail closed")
	}
	id := lease.(execution.Receipt).AttemptID()
	input := map[string]any{"id": "review", "attempt_id": id, "reason": "Investigated provider report correction", "evidence_ref": "provider/support-case/verified", "usage": map[string]int64{"requests": 1}, "model_version": "model-v1", "expires_at": f.now.Add(30 * time.Minute)}
	raw, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("POST", "/quota/reconciliations", string(raw), "planner", "plan-key", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var plan quotaplex.ReconciliationPlan
	if err = json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	apply, _ := json.Marshal(map[string]string{"digest": plan.Digest})
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("POST", "/quota/reconciliations/review/apply", string(apply), "planner", "self-key", nil))
	if w.Code != 403 {
		t.Fatal("self approval allowed", w.Code, w.Body.String())
	}
	*grants = []string{"aiplex:quota:read"}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("POST", "/quota/reconciliations/review/apply", string(apply), "reviewer", "apply-key", []string{"aiplex:quota:*"}))
	if w.Code != 403 {
		t.Fatal("revoked approval grant used", w.Code)
	}
	*grants = []string{"aiplex:quota:*"}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("POST", "/quota/reconciliations/review/apply", string(apply), "reviewer", "apply-key", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var receipt quotaplex.ReconciliationReceipt
	if err = json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.Decision.State != "settled" {
		t.Fatal(receipt, err)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("GET", "/quota/reconciliations/review", "", "reader", "", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"receipt"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = f.host.assertBindingActive(context.Background(), "tenant", "model-call"); err == nil {
		t.Fatal("settling one corrected attempt silently cleared binding quarantine")
	}
	page, err := f.host.Engines()["tenant"].Inventory(context.Background(), "tenant", "terminal-work", "", 20)
	if err != nil || len(page.Records) != 1 {
		t.Fatal(page, err)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("GET", "/quota/evidence/"+page.Records[0].ID, "", "reader", "", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "different-native-version") || !strings.Contains(w.Body.String(), `"resolution_id":"review"`) {
		t.Fatal("original facts lost", w.Code, w.Body.String())
	}
}
func TestQuotaAdministrationPolicyValidationAndInventoryBounds(t *testing.T) {
	f, _, router, _ := adminHTTPFixture(t)
	policy := f.config.Tenants[0].Policy
	policy.Revision = 2
	policy.Budgets[0].Limit = 20
	raw, _ := json.Marshal(map[string]any{"policy": policy, "expected_digest": f.host.tenants["tenant"].policy.Reference().Digest})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("POST", "/quota/policy/validate", string(raw), "editor", "", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "validated-export") || !strings.Contains(w.Body.String(), `"deployment_required":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := f.host.Ready(context.Background()); err != nil {
		t.Fatal("validation invalidated active host", err)
	}
	for _, path := range []string{"/quota/inventory?kind=attempts&limit=1000", "/quota/inventory?kind=policy-head", "/quota/inventory?kind=attempts&limit=no"} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, adminRequest("GET", path, "", "reader", "", nil))
		if w.Code != 400 {
			t.Fatal("unbounded internal scan accepted", path, w.Code)
		}
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("POST", "/quota/reconciliations", `{"id":"x"}`, "planner", "", nil))
	if w.Code != 400 {
		t.Fatal("mutation without idempotency accepted", w.Code)
	}
}
