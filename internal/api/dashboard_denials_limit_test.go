package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func denialsRouterWith(t *testing.T, n int) chi.Router {
	t.Helper()
	store := registry.NewMemoryStore()
	for i := 0; i < n; i++ {
		if err := store.AppendPolicyDenial(context.Background(), &models.PolicyDenial{
			ID: fmt.Sprintf("d%d", i), Plane: "mcplex", AgentID: "agent-1",
			Action: "tools/call:x", Scope: "mcp:tools:x", Reason: "scope_missing", Timestamp: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	h := api.NewDashboardHandler(store)
	r := chi.NewRouter()
	r.Get("/api/v1/dashboard/denials", h.ListPolicyDenials)
	return r
}

func TestDashboard_PolicyDenialsLimit(t *testing.T) {
	r := denialsRouterWith(t, 60)
	get := func(query string) (int, string, []models.PolicyDenial) {
		req := httptest.NewRequest("GET", "/api/v1/dashboard/denials"+query, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var out []models.PolicyDenial
		if w.Code == 200 {
			if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
				t.Fatalf("%s: decode: %v", query, err)
			}
		}
		return w.Code, w.Body.String(), out
	}

	// The default stays 50 when no limit is given.
	if code, _, got := get(""); code != 200 || len(got) != 50 {
		t.Fatalf("default: code %d, %d denials; want 200 and 50", code, len(got))
	}
	if code, _, got := get("?limit=5"); code != 200 || len(got) != 5 {
		t.Fatalf("limit=5: code %d, %d denials", code, len(got))
	}
	if code, _, got := get("?limit=1000"); code != 200 || len(got) != 60 {
		t.Fatalf("limit=1000 must return all 60 seeded denials: code %d, %d", code, len(got))
	}
	for _, q := range []string{"?limit=0", "?limit=-1", "?limit=1001", "?limit=abc", "?limit=1.5"} {
		code, body, _ := get(q)
		if code != 400 || !strings.Contains(body, "INVALID_LIMIT") {
			t.Errorf("%s: code %d body %q; want 400 INVALID_LIMIT", q, code, body)
		}
	}
}
