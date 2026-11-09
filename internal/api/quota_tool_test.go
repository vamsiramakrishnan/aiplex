package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

const managedToolBody = `{"jsonrpc":"2.0","id":"rpc","method":"tools/call","params":{"name":"search","arguments":{"query":"hello"}}}`
const managedToolResponse = `{"jsonrpc":"2.0","id":"rpc","result":{"content":[{"type":"text","text":"native tool result"}]}}`

func managedToolConnection(endpoint string) ToolConnection {
	return ToolConnection{ID: "surface", ResourceURL: endpoint, Execution: &toolexec.Connection{ID: "surface", Endpoint: endpoint, IdentityRef: "host", AllowedTools: []string{"search"}, ProtocolVersion: "2025-03-26", Mode: toolexec.StatelessJSON}}
}

func protectedToolFixture(t *testing.T, endpoint string) (*ConnectedHandler, *QuotaHost, func(string) *http.Request) {
	t.Helper()
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	policy := quotaplex.Policy{Version: quotaplex.Version, TenantID: "tenant", ID: "tool-policy", Revision: 1, EffectiveAt: now.Add(-time.Hour), Meters: map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}, "calls": {ID: "calls", Unit: "calls"}}, Accounts: []quotaplex.Account{{ID: "team", TenantWide: true}}, Budgets: []quotaplex.BudgetRule{{ID: "team-calls", Scope: quotaplex.Scope{Kind: "account", ID: "team"}, MeterID: "calls", Limit: 1, Period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}}, Concurrency: []quotaplex.ConcurrencyRule{{ID: "active", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 1}}}
	config := QuotaHostConfig{Tenants: []QuotaTenantConfig{{Policy: policy, Activate: true}}, Bindings: []QuotaBinding{{ID: "tool-search", TenantID: "tenant", Plane: "mcplex", ResourceID: "surface", OperationID: "tools/call:search", Profile: "request-count", FundingAccountID: "team", Provider: "mcp", Meters: map[string]string{"requests": "requests", "tool_calls": "calls"}}}}
	quota, err := NewQuotaHost(context.Background(), config, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := resources.NewMemoryStore()
	origin := strings.TrimSuffix(endpoint, "/mcp")
	h := cohesionHandler(t, store, origin)
	h.Quota = quota
	cohesionRecord(t, store, "tool-connection", "surface", managedToolConnection(endpoint))
	identity := models.WIFIdentity{PrincipalID: auth.CanonicalPrincipalID("https://identity.example", "people", "subject"), Subject: "subject", Provider: "https://identity.example", PoolID: "people", IsWorkforce: true, VerifiedEvidence: &models.IdentityEvidence{Issuer: "https://identity.example", PoolID: "people", Subject: "subject", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LifetimeValid: true, GroupsPresent: true, GroupsValid: true, Groups: []string{}}}
	h.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) {
		return &models.ResolvedAccess{Identity: identity, Scopes: []string{"mcp:tools:search"}}, nil
	}
	request := func(key string) *http.Request {
		r := trustedConnectedRequest("/api/v1/mcp/connections/surface/invoke", managedToolBody, []string{"mcp:tools:search"})
		ctx := context.WithValue(r.Context(), keyWIFIdentity, &identity)
		ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: identity, Scopes: []string{"mcp:tools:search"}})
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		return r
	}
	return h, quota, request
}

func TestManagedToolQuotaSettlesOnceAndBlocksFurtherDispatch(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, managedToolResponse)
	}))
	defer upstream.Close()
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	h, quota, request := protectedToolFixture(t, upstream.URL+"/mcp")
	w := httptest.NewRecorder()
	h.NativeTool(w, request("first"))
	if w.Code != 200 || calls.Load() != 1 || w.Header().Get("X-Aiplex-Attempt-ID") == "" {
		t.Fatalf("protected tool did not complete: status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body.String())
	}
	engine := quota.Engines()["tenant"]
	info, err := engine.GetAttempt(context.Background(), "tenant", w.Header().Get("X-Aiplex-Attempt-ID"))
	if err != nil || info.Decision.State != "settled" || info.Actual["calls"] != 1 || info.Actual["requests"] != 1 {
		t.Fatalf("tool usage was not settled %+v %v", info, err)
	}
	balance, err := engine.BudgetBalance(context.Background(), "tenant", info.Plan.Budgets[0].Key)
	if err != nil || balance.Held != 0 || balance.Consumed != 1 || balance.Available != 0 {
		t.Fatal("incorrect tool balance", balance, err)
	}
	w = httptest.NewRecorder()
	h.NativeTool(w, request("second"))
	if w.Code != 429 || calls.Load() != 1 {
		t.Fatal("exhausted tool quota reached provider", w.Code, calls.Load(), w.Body.String())
	}
	w = httptest.NewRecorder()
	h.NativeTool(w, request("first"))
	if w.Code != 409 || calls.Load() != 1 {
		t.Fatal("settled replay dispatched again", w.Code, calls.Load(), w.Body.String())
	}
}

func TestManagedToolRevokedDispatchGrantReleasesOnlyUndispatchedHold(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, managedToolResponse)
	}))
	defer upstream.Close()
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	h, quota, request := protectedToolFixture(t, upstream.URL+"/mcp")
	initial := h.ResolveAccess
	checks := 0
	h.ResolveAccess = func(ctx context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
		checks++
		access, err := initial(ctx, identity)
		if checks > 1 {
			access.Scopes = nil
		}
		return access, err
	}
	w := httptest.NewRecorder()
	h.NativeTool(w, request("revoked"))
	if w.Code != 403 || calls.Load() != 0 || checks < 2 {
		t.Fatal("revoked tool grant passed dispatch boundary", w.Code, calls.Load(), checks, w.Body.String())
	}
	info, err := quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get("X-Aiplex-Attempt-ID"))
	if err != nil || info.Decision.State != "aborted" {
		t.Fatal("undispatched hold was not released", info, err)
	}
	balance, err := quota.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", info.Plan.Budgets[0].Key)
	if err != nil || balance.Held != 0 || balance.Consumed != 0 || balance.Available != 1 {
		t.Fatal("revoked work changed consumption", balance, err)
	}
}

func TestManagedToolDispatchUsesVerifiedScopeAndOperatorIdentity(t *testing.T) {
	var calls atomic.Int32
	var authorization, body string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		authorization = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, managedToolResponse)
	}))
	defer upstream.Close()
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	store := resources.NewMemoryStore()
	h := cohesionHandler(t, store, upstream.URL)
	connection := managedToolConnection(upstream.URL + "/mcp")
	cohesionRecord(t, store, "tool-connection", connection.ID, connection)
	r := trustedConnectedRequest("/api/v1/mcp/connections/surface/invoke", managedToolBody, []string{"mcp:tools:search"})
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer caller-token")
	w := httptest.NewRecorder()
	h.NativeTool(w, r)
	if w.Code != 200 || w.Body.String() != managedToolResponse || authorization != "Bearer operator-tool-token" || body != managedToolBody || calls.Load() != 1 {
		t.Fatalf("managed dispatch failed: status=%d calls=%d auth=%q body=%s", w.Code, calls.Load(), authorization, body)
	}
	r = trustedConnectedRequest("/api/v1/mcp/connections/surface/invoke", managedToolBody, []string{"mcp:tools:other"})
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.NativeTool(w, r)
	if w.Code != 403 || calls.Load() != 1 {
		t.Fatal("caller without tool grant reached upstream", w.Code, calls.Load())
	}
}

func TestManagedToolRejectsMissingAndRetargetedExecutionPrerequisites(t *testing.T) {
	for _, test := range []string{"registration-only", "different-execution-id", "different-endpoint", "different-identity", "missing-network"} {
		t.Run(test, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, managedToolResponse)
			}))
			defer upstream.Close()
			t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
			store := resources.NewMemoryStore()
			h := cohesionHandler(t, store, upstream.URL)
			connection := managedToolConnection(upstream.URL + "/mcp")
			switch test {
			case "registration-only":
				connection.Execution = nil
			case "different-execution-id":
				connection.Execution.ID = "other"
			case "different-endpoint":
				connection.Execution.Endpoint = upstream.URL + "/other"
			case "different-identity":
				connection.Execution.IdentityRef = "unknown-profile"
			case "missing-network":
				connection.NetworkProfileRef = "removed-network"
			}
			cohesionRecord(t, store, "tool-connection", connection.ID, connection)
			r := trustedConnectedRequest("/api/v1/mcp/connections/surface/invoke", managedToolBody, []string{"mcp:tools:search"})
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.NativeTool(w, r)
			if w.Code != 503 || calls.Load() != 0 || !strings.Contains(w.Body.String(), "TOOL_CONFIGURATION_UNAVAILABLE") {
				t.Fatal("unusable connection reached credentials or dispatch", w.Code, calls.Load(), w.Body.String())
			}
		})
	}
}

func TestManagedToolConfigurationPinsSavedAndReferencedRecords(t *testing.T) {
	store := resources.NewMemoryStore()
	h := cohesionHandler(t, store, "https://tools.example")
	connection := managedToolConnection("https://tools.example/mcp")
	connection.NetworkProfileRef = "network"
	profile := cohesionRecord(t, store, "network-profile", "network", map[string]any{"revision": "original"})
	record := cohesionRecord(t, store, "tool-connection", connection.ID, connection)
	before, err := h.toolConfiguration(context.Background(), "tenant", record, connection)
	if err != nil {
		t.Fatal(err)
	}
	if before.NetworkProfile == nil || before.NetworkProfile.Revision != profile.Revision || before.Identity.ID != "host" {
		t.Fatal("configuration omitted resolved authority", before)
	}
	raw, _ := json.Marshal(map[string]any{"revision": "changed"})
	if _, err = store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "network-profile", ID: "network", Spec: raw}, profile.Revision); err != nil {
		t.Fatal(err)
	}
	after, err := h.toolConfiguration(context.Background(), "tenant", record, connection)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) == string(b) {
		t.Fatal("network revision did not change the execution configuration")
	}
}

func TestManagedToolExecutionConfigurationDoesNotRequireOAuthRegistration(t *testing.T) {
	store := resources.NewMemoryStore()
	h := cohesionHandler(t, store, "https://tools.example")
	connection := managedToolConnection("https://tools.example/mcp")
	raw, err := json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.validate(context.Background(), "tenant", "tool-connection", "surface", raw); err != nil {
		t.Fatal("operator identity execution was conflated with OAuth client registration", err)
	}
	connection.Execution.IdentityRef = "unknown"
	raw, _ = json.Marshal(connection)
	if err = h.validate(context.Background(), "tenant", "tool-connection", "surface", raw); err == nil {
		t.Fatal("unknown execution credential profile accepted")
	}
}
