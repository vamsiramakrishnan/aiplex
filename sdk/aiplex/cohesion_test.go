package aiplex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestScopesDTOAnd204UpdateRoundTrip(t *testing.T) {
	principal := "principal-" + strings.Repeat("a", 64)
	var granted []string
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method)
		if r.URL.Path != "/auth/users/"+principal+"/scopes" {
			t.Error("canonical principal route changed")
		}
		if r.Method == http.MethodPut {
			var body struct {
				Scopes []string `json:"scopes"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Fatal("invalid write payload")
			}
			if body.Scopes == nil {
				t.Error("manual grants must be an explicit array")
			}
			granted = body.Scopes
			w.WriteHeader(204)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"user_id": principal, "scopes": granted, "by_plane": map[string][]string{"llmplex": granted}})
	}))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	result, err := client.SetUserScopes(context.Background(), principal, []string{"llm:model:test"})
	if err != nil || result.UserID != principal || len(result.Scopes) != 1 || len(result.ByPlane["llmplex"]) != 1 || strings.Join(requests, ",") != "PUT,GET" {
		t.Fatal(result, err, requests)
	}
	if cleared, err := client.SetUserScopes(context.Background(), principal, nil); err != nil || len(cleared.Scopes) != 0 {
		t.Fatal("explicit empty grant clear failed", cleared, err)
	}
	if _, err := client.SetUserScopes(context.Background(), "user@example.com", nil); err == nil {
		t.Fatal("email accepted as canonical principal")
	}
}

func TestLegacyAndGenericSDKRejectResourcePathAliasing(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; json.NewEncoder(w).Encode(map[string]any{}) }))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	for _, id := range []string{"../other", "a/b", "a%2fb", "%252e%252e", "a\\b"} {
		if _, err := client.GetTemplate(context.Background(), id); err == nil {
			t.Fatalf("legacy resource path accepted %q", id)
		}
		if _, err := client.GetResource(context.Background(), aiplex.AgentBindings, id); err == nil {
			t.Fatalf("generic resource path accepted %q", id)
		}
	}
	if err := client.DeleteResource(context.Background(), aiplex.AgentBindings, "binding", 0); err == nil {
		t.Fatal("unconditional delete accepted")
	}
	if calls != 0 {
		t.Fatalf("unsafe resource input made %d requests", calls)
	}
}

func TestLLMSDKPreservesPolicyDTOAndUsageFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/llm/routes/model":
			var route aiplex.LLMRouteConfig
			if json.NewDecoder(r.Body).Decode(&route) != nil || route.RateLimit == nil || route.RateLimit.TokensPerMinute != 500 {
				t.Error("rate limit omitted from route DTO")
			}
			json.NewEncoder(w).Encode(route)
		case "/api/v1/llm/usage/summary":
			if r.URL.Query().Get("model_id") != "model" || r.URL.Query().Get("agent_id") != "agent" || r.URL.Query().Get("period") != "week" {
				t.Error("summary filters omitted")
			}
			json.NewEncoder(w).Encode(aiplex.UsageSummary{ModelID: "model", AgentID: "agent", Provider: "google", Period: "week"})
		case "/api/v1/llm/usage":
			if r.URL.Query().Get("model_id") != "model" || r.URL.Query().Get("agent_id") != "agent" || r.URL.Query().Get("since") != "2026-11-09T00:00:00Z" || r.URL.Query().Get("limit") != "25" {
				t.Error("record filters omitted")
			}
			json.NewEncoder(w).Encode([]aiplex.UsageRecord{{ModelID: "model", AgentID: "agent"}})
		default:
			t.Error("unexpected route", r.URL.Path)
		}
	}))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	ctx := context.Background()
	if route, err := client.PutLLMRoute(ctx, "model", &aiplex.LLMRouteConfig{RateLimit: &aiplex.ModelRateLimit{TokensPerMinute: 500}}); err != nil || route.RateLimit == nil {
		t.Fatal(route, err)
	}
	if summary, err := client.GetUsageSummaryWithOpts(ctx, &aiplex.UsageSummaryOpts{ModelID: "model", AgentID: "agent", Period: "week"}); err != nil || summary.AgentID != "agent" || summary.Provider != "google" {
		t.Fatal(summary, err)
	}
	if records, err := client.ListUsageRecords(ctx, &aiplex.ListUsageRecordsOpts{ModelID: "model", AgentID: "agent", Since: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), Limit: 25}); err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
}

func TestLLMBackendNamingMetadataRoundTrip(t *testing.T) {
	var route aiplex.LLMRouteConfig
	if err := json.Unmarshal([]byte(`{"model_id":"model","backend_naming_version":2,"retired_backends":["legacy-backend"]}`), &route); err != nil {
		t.Fatal(err)
	}
	if route.BackendNamingVersion != 2 || len(route.RetiredBackends) != 1 {
		t.Fatal("server naming metadata lost")
	}
	raw, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["backend_naming_version"]) != "2" {
		t.Fatalf("read-only version missing from round trip: %s", raw)
	}
}
