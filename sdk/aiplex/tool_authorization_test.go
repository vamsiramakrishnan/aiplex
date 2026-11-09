package aiplex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestToolAuthorizationSDKUsesOwnedRoutesAndPrivateCallbackBody(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.RawQuery != "" {
			t.Error("OAuth code/state leaked in query")
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/mcp/connections/tool/authorization") {
			t.Error("wrong authorization path", r.URL.Path)
		}
		if strings.HasSuffix(r.URL.Path, "/complete") {
			var in toolauth.AuthorizationResponse
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Code != "private-code" || in.State != "state" {
				t.Error("callback body binding lost")
			}
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toolauth.SessionInfo{GrantID: "grant", Status: "active"})
	}))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	ctx := context.Background()
	for _, id := range []string{"../other", "a/b", ""} {
		if _, e := client.GetToolAuthorization(ctx, id); e == nil {
			t.Fatal("unsafe tool ID accepted")
		}
	}
	if calls != 0 {
		t.Fatal("unsafe ID reached API")
	}
	if _, e := client.StartToolAuthorization(ctx, "tool", "https://callback.example"); e != nil {
		t.Fatal(e)
	}
	if _, e := client.CompleteToolAuthorization(ctx, "tool", toolauth.AuthorizationResponse{Code: "private-code", State: "state", FlowID: "flow", Issuer: "https://issuer.example"}); e != nil {
		t.Fatal(e)
	}
	if value, e := client.GetToolAuthorization(ctx, "tool"); e != nil || value.GrantID != "grant" {
		t.Fatal(value, e)
	}
	if e := client.RevokeToolAuthorization(ctx, "tool"); e != nil {
		t.Fatal(e)
	}
	if calls != 4 {
		t.Fatal(calls)
	}
}
