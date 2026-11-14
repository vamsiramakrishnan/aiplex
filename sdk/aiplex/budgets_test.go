package aiplex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestBudgetClientRoutesAndRevisions(t *testing.T) {
	type seen struct{ method, path, ifMatch string }
	var calls []seen
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, seen{r.Method, r.URL.Path, r.Header.Get("If-Match")})
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(204)
		case r.URL.Path == "/api/v1/budgets/team/status":
			json.NewEncoder(w).Encode(map[string]any{"id": "team", "unit": "cost", "currency": "USD", "limit": "5", "spent": "2", "reserved": "0", "remaining": "3", "window": map[string]any{}, "include_tool_calls": true})
		case r.URL.Path == "/api/v1/budgets/preview":
			json.NewEncoder(w).Encode(map[string]any{"hypothetical": true, "draws": []any{map[string]any{"id": "team", "direct": true, "unit": "cost", "currency": "USD", "limit": "5", "window": map[string]any{}, "include_tool_calls": true}}, "skipped": []any{}, "notes": []any{}})
		case r.URL.Path == "/api/v1/budgets":
			json.NewEncoder(w).Encode([]map[string]any{{"id": "team", "revision": 3, "spec": map[string]any{"id": "team"}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"id": "team", "revision": 4, "spec": map[string]any{"id": "team"}})
		}
	}))
	defer server.Close()
	c := aiplex.NewClient(server.URL)
	ctx := context.Background()
	if list, err := c.ListBudgets(ctx); err != nil || len(list) != 1 || list[0].Revision != 3 {
		t.Fatal(list, err)
	}
	spec := budgets.Budget{ID: "team", Limit: budgets.Limit{Unit: "requests", Amount: "5"}, Window: budgets.Window{Kind: "tumbling", Hours: 1}}
	if rec, err := c.PutBudget(ctx, spec, 3); err != nil || rec.Revision != 4 {
		t.Fatal(rec, err)
	}
	if st, err := c.GetBudgetStatus(ctx, "team"); err != nil || st.Spent != "2" || st.Remaining != "3" || !st.IncludeToolCalls {
		t.Fatal(st, err)
	}
	if p, err := c.PreviewBudgets(ctx, aiplex.BudgetPreviewRequest{Subject: aiplex.BudgetPreviewSubject{Principal: "x"}}); err != nil || !p.Hypothetical || len(p.Draws) != 1 || !p.Draws[0].IncludeToolCalls {
		t.Fatal(p, err)
	}
	if err := c.DeleteBudget(ctx, "team", 0); err == nil {
		t.Fatal("delete without a revision accepted")
	}
	if err := c.DeleteBudget(ctx, "team", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetBudget(ctx, "../x"); err == nil {
		t.Fatal("path aliasing accepted")
	}
	want := []seen{{"GET", "/api/v1/budgets", ""}, {"PUT", "/api/v1/budgets/team", "3"}, {"GET", "/api/v1/budgets/team/status", ""}, {"POST", "/api/v1/budgets/preview", ""}, {"DELETE", "/api/v1/budgets/team", "4"}}
	if len(calls) != len(want) {
		t.Fatal(calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d: %+v want %+v", i, calls[i], want[i])
		}
	}
}
