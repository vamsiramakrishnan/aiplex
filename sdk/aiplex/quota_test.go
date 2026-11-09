package aiplex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestQuotaAttemptSDKReadsCanonicalStatusWithoutDispatch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/quota/attempts/attempt" {
			t.Error("incorrect quota status route", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(quotaplex.AttemptInfo{Decision: quotaplex.Decision{TenantID: "tenant", AttemptID: "attempt", State: "settled", Allowed: true}, Actual: quotaplex.Vector{"requests": 1, "tokens": 12}})
	}))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	for _, id := range []string{"", "a/b", "../other", "a%2fb"} {
		if _, err := client.GetQuotaAttempt(context.Background(), id); err == nil {
			t.Fatal("unsafe attempt ID accepted", id)
		}
	}
	if calls != 0 {
		t.Fatal("unsafe identifiers reached API")
	}
	result, err := client.GetQuotaAttempt(context.Background(), "attempt")
	if err != nil || result.Decision.State != "settled" || result.Actual["tokens"] != 12 || calls != 1 {
		t.Fatal(result, err, calls)
	}
}
