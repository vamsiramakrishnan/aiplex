package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func TestOverviewConnectListsBedrockAndWebSocketEndpoints(t *testing.T) {
	f := newOverviewFixture(t)
	addProviderAccessPointSurfaces(t, f.apFixture)
	out := f.json("/api/v1/overview/access-points/prov/connect")
	byKey := map[string]map[string]any{}
	for _, e := range out["endpoints"].([]any) {
		m := e.(map[string]any)
		byKey[m["protocol"].(string)+"|"+m["transport"].(string)] = m
	}
	conv := byKey["bedrock-converse|http"]
	if conv == nil || conv["url"] != "https://gateway.example/ap/prov/model/{model}/converse" {
		t.Fatalf("converse endpoint = %v (all %v)", conv, out["endpoints"])
	}
	if s := byKey["bedrock-converse|http-stream"]; s == nil || s["url"] != "https://gateway.example/ap/prov/model/{model}/converse-stream" {
		t.Fatalf("converse-stream endpoint = %v", s)
	}
	rt := byKey["openai-realtime|websocket"]
	if rt == nil || rt["url"] != "wss://gateway.example/ap/prov/v1/realtime" || len(rt["query"].([]any)) != 1 || rt["query"].([]any)[0] != "model" {
		t.Fatalf("realtime endpoint = %v", rt)
	}
	if models := rt["models"].([]any); len(models) != 1 || models[0] != "voice" {
		t.Fatalf("realtime models = %v", models)
	}
	// Existing HTTP families are unchanged.
	if _, ok := out["base_urls"].(map[string]any)["openai"]; !ok {
		t.Fatalf("base_urls = %v", out["base_urls"])
	}
	found := false
	for _, s := range out["snippets"].([]any) {
		sn := s.(map[string]any)
		if sn["title"] == "curl: Amazon Bedrock Converse" {
			found = true
			if c := sn["content"].(string); !strings.Contains(c, "https://gateway.example/ap/prov/model/claude-bedrock/converse") || !strings.Contains(c, "$AIPLEX_TOKEN") {
				t.Fatalf("bedrock snippet = %s", c)
			}
		}
		if strings.Contains(strings.ToLower(sn["title"].(string)), "websocket") {
			t.Fatalf("no websocket client snippet may be invented: %v", sn["title"])
		}
	}
	if !found {
		t.Fatalf("no Bedrock Converse snippet: %v", out["snippets"])
	}
	// An http public URL yields ws.
	f.h.PublicURL = "http://gateway.local"
	out = f.json("/api/v1/overview/access-points/prov/connect")
	for _, e := range out["endpoints"].([]any) {
		if m := e.(map[string]any); m["transport"] == "websocket" && !strings.HasPrefix(m["url"].(string), "ws://gateway.local/") {
			t.Fatalf("websocket url = %v", m["url"])
		}
	}
	// A tools-only access point has an empty list, not null.
	if tools := f.json("/api/v1/overview/access-points/agents/connect"); tools["endpoints"] == nil || len(tools["endpoints"].([]any)) != 0 {
		t.Fatalf("endpoints = %v", tools["endpoints"])
	}
}

func TestOverviewAccessPointListIsCheapPaginatedAndAuthorized(t *testing.T) {
	f := newOverviewFixture(t)
	out := f.json("/api/v1/overview/access-points?limit=2")
	list := out["access_points"].([]any)
	if len(list) != 2 || out["next_cursor"] == nil {
		t.Fatalf("page 1 = %v", out)
	}
	first := list[0].(map[string]any)
	if first["id"] != "agents" {
		t.Fatalf("ordered by id: %v", first)
	}
	if first["qos_class"] != "gold" || first["quota_coverage"].(map[string]any)["state"] != "unknown" || first["counts"].(map[string]any)["tools"] != float64(1) || first["counts"].(map[string]any)["models"] != float64(0) {
		t.Fatalf("row = %v", first)
	}
	if kinds := first["caller_kinds"].([]any); len(kinds) != 1 || kinds[0] != "agent" {
		t.Fatalf("caller_kinds = %v", kinds)
	}
	rest := f.json("/api/v1/overview/access-points?limit=2&cursor=" + out["next_cursor"].(string))
	seen := map[string]bool{"agents": true, list[1].(map[string]any)["id"].(string): true}
	for _, r := range rest["access_points"].([]any) {
		id := r.(map[string]any)["id"].(string)
		if seen[id] {
			t.Fatalf("duplicate %s across pages", id)
		}
		seen[id] = true
	}
	if len(seen) < 3 {
		t.Fatalf("seen = %v", seen)
	}
	for _, bad := range []string{"?limit=0", "?cursor=!!", "?since=1h"} {
		if w := f.get("/api/v1/overview/access-points" + bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, w.Code)
		}
	}
}

func TestOverviewAccessPointListRoles(t *testing.T) {
	f := newOverviewFixture(t)
	f.roles = nil
	if w := f.get("/api/v1/overview/access-points"); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", w.Code)
	}
}

func TestOverviewUsageCarriesBoundedCounts(t *testing.T) {
	f := newOverviewFixture(t)
	at := f.now.Add(-time.Hour)
	for i, bound := range []string{usageledger.UsageLowerBound, usageledger.UsageAmbiguous, usageledger.UsagePartial, ""} {
		r := ovRecord("b"+string(rune('a'+i)), "tenant", at.Add(time.Duration(i)*time.Minute))
		r.AccessPointID, r.CallerKind, r.PrincipalID, r.UsageBound = "chat-only", CallerUser, "carol", bound
		r.Usage = &usageledger.Usage{InputTokens: i64(1)}
		if err := f.ledger.Append(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	total := f.json("/api/v1/overview/access-points/chat-only")["usage"].(map[string]any)["total"].(map[string]any)
	if total["bounded_usage_records"] != float64(3) || total["lower_bound_records"] != float64(1) || total["ambiguous_usage_records"] != float64(1) || total["partial_usage_records"] != float64(1) {
		t.Fatalf("total = %v", total)
	}
	if total["tokens"].(map[string]any)["input_tokens"] != float64(4) {
		t.Fatalf("bounded records must stay in the sums: %v", total)
	}
	// Exact usage omits the keys (backward compatible).
	ide := f.json("/api/v1/overview/access-points/ide")["usage"].(map[string]any)["total"].(map[string]any)
	if _, ok := ide["bounded_usage_records"]; ok {
		t.Fatalf("ide has no bounded records: %v", ide)
	}
}

// Empty lists are [] in every overview response, never null.
func TestOverviewEmptyListsAreArraysNotNull(t *testing.T) {
	f := newOverviewFixture(t)
	addProviderAccessPointSurfaces(t, f.apFixture)
	f.put(AccessPointKind, "bare", AccessPoint{ID: "bare", CallerKinds: []string{CallerUser}})
	f.put(AccessPointKind, "legacy", AccessPoint{ID: "legacy"}) // predates save-time validation: caller_kinds unset
	arrayKeys := map[string]bool{"reasons": true, "access_points": true, "limitations": true, "snippets": true, "notes": true, "derived_from": true, "known_from": true, "subjects": true, "models": true, "tools": true, "skills": true, "caller_kinds": true, "endpoints": true, "steps": true, "cost": true, "users": true, "agents": true, "clients": true, "groups": true, "open_to_all_of_kind": true, "by_caller_kind": true, "by_model": true, "by_tool": true, "usage_by_model": true, "usage_by_tool": true, "wire_protocols": true, "budgets": true, "rates": true, "concurrency": true, "protocols": true}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if child == nil && arrayKeys[k] {
					t.Errorf("%s.%s is null", path, k)
				}
				walk(path+"."+k, child)
			}
		case []any:
			for i, child := range x {
				walk(path+"[]", child)
				_ = i
			}
		}
	}
	paths := []string{"/api/v1/overview/access-points", "/api/v1/overview/access-points/bare", "/api/v1/overview/access-points/bare/connect", "/api/v1/overview/access-points/legacy", "/api/v1/overview/access-points/legacy/connect", "/api/v1/overview/access-points/bare/connect?client=claude-code", "/api/v1/overview/access-points/ide", "/api/v1/overview/access-points/ide/connect", "/api/v1/overview/access-points/prov/connect", "/api/v1/overview/access-points/agents", "/api/v1/overview/access-points/agents/connect", "/api/v1/overview/subjects?kind=user", "/api/v1/overview/subjects?kind=agent", "/api/v1/overview/subjects?kind=client", "/api/v1/overview/subjects/user/nobody-seen", "/api/v1/overview/subjects/agent/tutor", "/api/v1/overview/subjects/client/ide"}
	for _, p := range paths {
		w := f.get(p)
		if w.Code != http.StatusOK {
			t.Logf("skip %s = %d", p, w.Code)
			continue
		}
		walk(p, decodeJSON(t, w))
	}
}
