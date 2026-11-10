package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// denialRouter mounts the sidecar route the way the server does: behind the
// shared bearer secret in AIPLEX_DENIAL_TOKEN.
func denialRouter(store registry.Store, h *api.DashboardHandler) chi.Router {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(api.BearerToken(api.DenialTokenEnv))
		r.Post("/internal/policy-denials", h.RecordPolicyDenial)
	})
	r.Get("/api/v1/dashboard/denials", h.ListPolicyDenials)
	r.Get("/api/v1/dashboard/stats", h.GetStats)
	return r
}

func postDenial(r http.Handler, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/internal/policy-denials", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const goodDenial = `{"plane":"mcplex","agent_id":"agent-1","user_id":"user-1","action":"/mcp/server","scope":"mcp:tools:search","reason":"scope_missing","request_id":"req-1","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}`

func TestSidecarDenialRouteNeedsTheSharedSecret(t *testing.T) {
	store := registry.NewMemoryStore()
	r := denialRouter(store, api.NewDashboardHandler(store))

	t.Setenv(api.DenialTokenEnv, "")
	if w := postDenial(r, "anything", goodDenial); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unset secret should disable the route, got %d", w.Code)
	}
	t.Setenv(api.DenialTokenEnv, "s3cret")
	for name, token := range map[string]string{"none": "", "wrong": "nope"} {
		if w := postDenial(r, token, goodDenial); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s token: %d", name, w.Code)
		}
	}
	if n, _ := store.CountPolicyDenials(context.Background()); n != 0 {
		t.Fatalf("%d denials stored without the secret", n)
	}
}

func TestSidecarDenialIsStoredAndVisibleToOperators(t *testing.T) {
	store := registry.NewMemoryStore()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	h := api.NewDashboardHandler(store)
	h.Now = func() time.Time { return now }
	r := denialRouter(store, h)
	t.Setenv(api.DenialTokenEnv, "s3cret")

	w := postDenial(r, "s3cret", goodDenial)
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var created models.PolicyDenial
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.ID, "den_") || !created.Timestamp.Equal(now) || created.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("server-assigned fields: %+v", created)
	}

	// The path the CLI and Console read.
	req := httptest.NewRequest("GET", "/api/v1/dashboard/denials", nil)
	lw := httptest.NewRecorder()
	r.ServeHTTP(lw, req)
	var list []models.PolicyDenial
	if err := json.Unmarshal(lw.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].AgentID != "agent-1" || list[0].TraceID == "" {
		t.Fatalf("list: %v %s", err, lw.Body.String())
	}
	sw := httptest.NewRecorder()
	r.ServeHTTP(sw, httptest.NewRequest("GET", "/api/v1/dashboard/stats", nil))
	var stats models.DashboardStats
	if err := json.Unmarshal(sw.Body.Bytes(), &stats); err != nil || stats.PolicyDenials != 1 {
		t.Fatalf("stats: %v %s", err, sw.Body.String())
	}
}

func TestPolicyDenialCountIsTheLast24Hours(t *testing.T) {
	store := registry.NewMemoryStore()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for i, age := range []time.Duration{time.Hour, 23 * time.Hour, 25 * time.Hour, 72 * time.Hour} {
		store.AppendPolicyDenial(context.Background(), &models.PolicyDenial{ID: string(rune('a' + i)), Timestamp: now.Add(-age)})
	}
	h := api.NewDashboardHandler(store)
	h.Now = func() time.Time { return now }
	w := httptest.NewRecorder()
	denialRouter(store, h).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/dashboard/stats", nil))
	var stats models.DashboardStats
	json.Unmarshal(w.Body.Bytes(), &stats)
	if stats.PolicyDenials != 2 {
		t.Fatalf("policy_denials_24h = %d, want 2", stats.PolicyDenials)
	}
}

func TestSidecarDenialSchemaIsClosedAndBounded(t *testing.T) {
	store := registry.NewMemoryStore()
	r := denialRouter(store, api.NewDashboardHandler(store))
	t.Setenv(api.DenialTokenEnv, "s3cret")
	long := func(n int) string { return strings.Repeat("a", n) }
	with := func(field, value string) string {
		var m map[string]any
		json.Unmarshal([]byte(goodDenial), &m)
		m[field] = value
		b, _ := json.Marshal(m)
		return string(b)
	}
	for name, body := range map[string]string{
		"tenant asserted by the sidecar": strings.Replace(goodDenial, `{`, `{"tenant_id":"acme",`, 1),
		"unknown field":                  strings.Replace(goodDenial, `{`, `{"admin":true,`, 1),
		"not json":                       `{`,
		"trailing data":                  goodDenial + `{}`,
		"two objects":                    goodDenial + goodDenial,
		"bad plane":                      with("plane", "kernel"),
		"bad reason":                     with("reason", "because"),
		"control character":              with("action", "/mcp\nINJECTED"),
		"long agent":                     with("agent_id", long(257)),
		"long action":                    with("action", long(513)),
		"long scope":                     with("scope", long(513)),
		"long request id":                with("request_id", long(129)),
		"short trace id":                 with("trace_id", "abc"),
		"upper-case trace id":            with("trace_id", strings.ToUpper("4bf92f3577b34da6a3ce929d0e0e4736")),
		"zero trace id":                  with("trace_id", strings.Repeat("0", 32)),
		"no action":                      with("action", ""),
		"no principal":                   `{"action":"/x","reason":"scope_missing"}`,
	} {
		if w := postDenial(r, "s3cret", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := postDenial(r, "s3cret", `{"agent_id":"a","action":"/x","reason":"scope_missing","scope":"`+long(5000)+`"}`); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize body: %d", w.Code)
	}
	if n, _ := store.CountPolicyDenials(context.Background()); n != 0 {
		t.Fatalf("%d invalid denials were stored", n)
	}
	// The earlier admin route's id and timestamp fields are accepted and ignored.
	w := postDenial(r, "s3cret", strings.Replace(goodDenial, `{`, `{"id":"forged","timestamp":"2001-01-01T00:00:00Z",`, 1))
	var d models.PolicyDenial
	json.Unmarshal(w.Body.Bytes(), &d)
	if w.Code != http.StatusCreated || d.ID == "forged" || d.Timestamp.Year() < 2026 {
		t.Fatalf("client id and timestamp must be ignored: %d %+v", w.Code, d)
	}
}

func TestSidecarDenialsAreRateLimitedGlobally(t *testing.T) {
	store := registry.NewMemoryStore()
	r := denialRouter(store, api.NewDashboardHandler(store))
	t.Setenv(api.DenialTokenEnv, "s3cret")
	limited := 0
	for i := 0; i < 400; i++ {
		if postDenial(r, "s3cret", goodDenial).Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("a flood of denials was never limited")
	}
	if n, _ := store.CountPolicyDenials(context.Background()); n > 260 {
		t.Fatalf("%d denials stored from one burst", n)
	}
}

func TestMemoryDenialStoreIsBounded(t *testing.T) {
	store := registry.NewMemoryStore()
	for i := 0; i < 5200; i++ {
		store.AppendPolicyDenial(context.Background(), &models.PolicyDenial{Timestamp: time.Now()})
	}
	if n, _ := store.CountPolicyDenials(context.Background()); n != 5000 {
		t.Fatalf("stored %d, want the cap of 5000", n)
	}
}
