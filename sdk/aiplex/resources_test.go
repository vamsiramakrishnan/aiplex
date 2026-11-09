package aiplex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResourceClientPreservesRevisionAndSafePath(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "PUT" || r.URL.Path != "/api/v1/network/profiles/edge" || r.Header.Get("If-Match") != "7" {
			t.Errorf("wrong resource request %s %s %q", r.Method, r.URL.Path, r.Header.Get("If-Match"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "edge", "revision": 8, "spec": map[string]any{"id": "edge"}})
	}))
	defer server.Close()
	client := NewClient(server.URL)
	record, e := client.PutResource(context.Background(), NetworkProfiles, "edge", map[string]any{"id": "edge"}, 7)
	if e != nil || record.Revision != 8 {
		t.Fatal(record, e)
	}
	if _, e = client.ListResources(context.Background(), ResourceKind("unknown")); e == nil || calls != 1 {
		t.Fatal("unknown module contacted server")
	}
}
