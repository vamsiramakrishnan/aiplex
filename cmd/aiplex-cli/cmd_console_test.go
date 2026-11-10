package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The Console proxy forwards the Console's own API paths with the developer
// token, and deliberately not /ap/: endpoint URLs the Console shows are built
// by the API from its public URL (the API port), so clients never need the
// Console origin, and the Console origin must not become a token-injecting
// gateway to the data plane.
func TestConsoleProxyForwardsAPIButNotAccessPoints(t *testing.T) {
	var hits atomic.Int32
	var auth atomic.Value
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		auth.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, "api")
	}))
	defer api.Close()
	h, err := consoleHandler(api.URL, "dev-token")
	if err != nil {
		t.Fatal(err)
	}
	console := httptest.NewServer(h)
	defer console.Close()

	resp, err := http.Get(console.URL + "/api/v1/iam/whoami")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "api" || auth.Load() != "Bearer dev-token" {
		t.Fatalf("API path not proxied with the token: %q %v", body, auth.Load())
	}
	before := hits.Load()
	resp, err = http.Get(console.URL + "/ap/local/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if hits.Load() != before || strings.Contains(string(body), "api") && string(body) == "api" {
		t.Fatalf("/ap/ was forwarded to the API through the Console origin")
	}
}
