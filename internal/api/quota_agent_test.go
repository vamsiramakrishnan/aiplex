package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func protectedAgentFixture(t *testing.T, server *httptest.Server) (*ConnectedHandler, *QuotaHost, cloudagents.Connection, func(string) *http.Request) {
	t.Helper()
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	policy := quotaplex.Policy{Version: quotaplex.Version, TenantID: "tenant", ID: "agent-policy", Revision: 1, EffectiveAt: now.Add(-time.Hour), Meters: map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}}, Accounts: []quotaplex.Account{{ID: "team", TenantWide: true}}, Budgets: []quotaplex.BudgetRule{{ID: "agent-requests", Scope: quotaplex.Scope{Kind: "account", ID: "team"}, MeterID: "requests", Limit: 1, Period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}}, Concurrency: []quotaplex.ConcurrencyRule{{ID: "active", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 1}}}
	host, e := NewQuotaHost(context.Background(), QuotaHostConfig{Tenants: []QuotaTenantConfig{{Policy: policy, Activate: true}}, Bindings: []QuotaBinding{{ID: "agent-query", TenantID: "tenant", Plane: "a2aplex", ResourceID: "surface", OperationID: "stream-query", Provider: string(cloudagents.GoogleRuntime), Profile: "request-count", FundingAccountID: "team", Meters: map[string]string{"requests": "requests"}}}}, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	network := endpointpolicy.Config{AllowedOrigins: []string{server.URL}, AllowLoopback: true, TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
	runtime, e := connections.New(connections.Config{Network: network, Identities: []connections.Identity{{ID: "agent-server", TenantID: "tenant", Type: "bearer-env", TokenEnv: "QUOTA_AGENT_TOKEN", AllowedOrigins: []string{server.URL}}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	store := resources.NewMemoryStore()
	connection := cloudagents.Connection{ID: "surface", TenantID: "tenant", Provider: cloudagents.GoogleRuntime, Endpoint: server.URL, ResourceID: "projects/project/locations/global/reasoningEngines/runtime", IdentityRef: "agent-server", GovernanceMode: "invocation"}
	cohesionRecord(t, store, "agent-connection", "surface", connection)
	h := NewConnectedHandler(store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	h.Quota = host
	identity := models.WIFIdentity{PrincipalID: auth.CanonicalPrincipalID("https://identity.example", "people", "subject"), Subject: "subject", Provider: "https://identity.example", PoolID: "people", IsWorkforce: true, VerifiedEvidence: &models.IdentityEvidence{Issuer: "https://identity.example", PoolID: "people", Subject: "subject", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LifetimeValid: true, GroupsPresent: true, GroupsValid: true, Groups: []string{}}}
	scope := "a2a:agent:surface:stream-query"
	h.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) {
		return &models.ResolvedAccess{Identity: identity, Scopes: []string{scope}}, nil
	}
	request := func(key string) *http.Request {
		r := trustedConnectedRequest("/api/v1/a2a/connections/surface/invoke", `{"operation":"stream-query","payload":{"input":"hello"}}`, []string{scope})
		ctx := context.WithValue(r.Context(), keyWIFIdentity, &identity)
		ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: identity, Scopes: []string{scope}})
		r = r.WithContext(ctx)
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	return h, host, connection, request
}
func TestManagedNativeAgentRequestBudgetSettlesOnceAndBlocksDispatch(t *testing.T) {
	var calls atomic.Int32
	native := "data: {\"native\":true}\n\n"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer native-agent-token" {
			t.Error("wrong native identity")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, native)
	}))
	defer server.Close()
	t.Setenv("QUOTA_AGENT_TOKEN", "native-agent-token")
	h, host, _, request := protectedAgentFixture(t, server)
	w := httptest.NewRecorder()
	h.InvokeAgent(w, request("first"))
	if w.Code != 200 || w.Body.String() != native || calls.Load() != 1 {
		t.Fatal(w.Code, w.Body.String(), calls.Load())
	}
	attempt, e := host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get("X-Aiplex-Attempt-ID"))
	if e != nil || attempt.Decision.State != "settled" || attempt.Actual["requests"] != 1 {
		t.Fatal("native HTTP exchange did not settle", attempt, e)
	}
	t.Setenv("QUOTA_AGENT_TOKEN", "")
	for _, key := range []string{"second", "first"} {
		w = httptest.NewRecorder()
		h.InvokeAgent(w, request(key))
		if (key == "second" && w.Code != 429) || (key == "first" && w.Code != 409) || calls.Load() != 1 {
			t.Fatal("denied/replayed native invocation acquired credentials or dispatched", key, w.Code, w.Body.String(), calls.Load())
		}
	}
}
func TestManagedNativeAgentTruncatedHTTPRetainsUnknownHold(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: partial\n\n")
		w.(http.Flusher).Flush()
		connection, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	t.Setenv("QUOTA_AGENT_TOKEN", "native-agent-token")
	h, host, _, request := protectedAgentFixture(t, server)
	w := httptest.NewRecorder()
	h.InvokeAgent(w, request("truncated"))
	attempt, e := host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get("X-Aiplex-Attempt-ID"))
	if e != nil || attempt.Decision.State != "unknown" || len(attempt.Actual) != 0 {
		t.Fatal("truncated HTTP exchange fabricated final usage", attempt, e)
	}
	balance, e := host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", attempt.Plan.Budgets[0].Key)
	if e != nil || balance.Held != 1 || balance.Consumed != 0 {
		t.Fatal("uncertain exchange refunded exposure", balance, e)
	}
	w = httptest.NewRecorder()
	h.InvokeAgent(w, request("next"))
	if w.Code != 429 || calls.Load() != 1 {
		t.Fatal("unknown hold allowed another invocation", w.Code, calls.Load())
	}
}
func TestManagedNativeAgentRechecksSavedTargetBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	t.Setenv("QUOTA_AGENT_TOKEN", "native-agent-token")
	h, host, c, request := protectedAgentFixture(t, server)
	original := h.ResolveAccess
	checks := 0
	h.ResolveAccess = func(ctx context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
		checks++
		if checks == 2 {
			c.ResourceID = "projects/project/locations/global/reasoningEngines/changed"
			raw, _ := json.Marshal(c)
			if _, e := h.Store.Put(ctx, resources.Record{TenantID: "tenant", Kind: "agent-connection", ID: "surface", Spec: raw}, 1); e != nil {
				t.Error(e)
			}
		}
		return original(ctx, identity)
	}
	w := httptest.NewRecorder()
	h.InvokeAgent(w, request("changed"))
	if w.Code != 403 || calls.Load() != 0 {
		t.Fatal("changed agent target reached provider", w.Code, w.Body.String(), calls.Load())
	}
	attempt, e := host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get("X-Aiplex-Attempt-ID"))
	if e != nil || attempt.Decision.State != "aborted" {
		t.Fatal("undispatched changed target hold remained", attempt, e)
	}
}
