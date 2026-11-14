package cloudagents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func googleConnection(endpoint string) Connection {
	c := baseConnection(GoogleRuntime, endpoint)
	c.ResourceID = "projects/p/locations/l/reasoningEngines/e"
	return c
}

// The endpoint rule in both modes. Without allow_loopback (every deployment
// except development mode: the API refuses to start with it elsewhere) an
// endpoint is an https origin, exactly as before. With it, plain http is
// accepted only for a literal loopback host by the endpoint policy's own parser.
func TestEndpointOriginRuleInBothModes(t *testing.T) {
	cases := []struct {
		endpoint string
		dev      bool // accepted with allow_loopback
	}{
		{"https://agents.example.com", true},
		{"http://127.0.0.1:9000", true},
		{"http://127.0.0.1", true},
		{"http://localhost:9000", true},
		{"http://[::1]:9000", true},
		{"http://127.0.0.1.nip.io:9000", false},
		{"http://localtest.me:9000", false}, // a name that resolves to loopback
		{"http://0.0.0.0:9000", false},
		{"http://169.254.169.254", false},
		{"http://169.254.169.254:80", false},
		{"http://user:pw@127.0.0.1:9000", false},
		{"http://user@localhost:9000", false},
		{"http://127.0.0.1:9000/v1", false},
		{"http://127.0.0.1:9000?x=1", false},
		{"http://example.com", false},
		{"http://10.0.0.5", false},
		{"ftp://127.0.0.1", false},
		{"http://127.0.0.1\\@evil.example", false},
	}
	for _, tc := range cases {
		production := googleConnection(tc.endpoint).Validate() == nil
		wantProduction := tc.endpoint == "https://agents.example.com"
		if production != wantProduction {
			t.Errorf("%s: Validate (production) accepted=%v", tc.endpoint, production)
		}
		if got := googleConnection(tc.endpoint).ValidateWith(false) == nil; got != wantProduction {
			t.Errorf("%s: ValidateWith(false) accepted=%v", tc.endpoint, got)
		}
		wantDev := tc.dev || wantProduction
		if got := googleConnection(tc.endpoint).ValidateWith(true) == nil; got != wantDev {
			t.Errorf("%s: ValidateWith(true) accepted=%v, want %v", tc.endpoint, got, wantDev)
		}
	}
	// The error text of the production rule is unchanged.
	if err := googleConnection("http://127.0.0.1:9000").Validate(); err == nil || err.Error() != "agent endpoint must be a pinned HTTPS origin" {
		t.Fatalf("production error: %v", err)
	}
}

// NewInvoker applies the same rule, from the option the API sets from the
// endpoint policy, and never follows a redirect from a loopback agent.
func TestInvokerLoopbackHTTPAndRedirects(t *testing.T) {
	var hits atomic.Int64
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer elsewhere.Close()
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/stolen", http.StatusFound)
	}))
	defer agent.Close()
	creds := BearerCredentials{Token: func(context.Context) (string, error) { return "token", nil }}

	if _, err := NewInvoker(googleConnection(agent.URL), AuthorizeFunc(allow), http.DefaultClient, creds); err == nil {
		t.Fatal("http endpoint accepted without allow_loopback")
	}
	if _, err := NewInvoker(googleConnection(agent.URL), AuthorizeFunc(allow), http.DefaultClient, creds, AllowLoopbackHTTP(false)); err == nil {
		t.Fatal("http endpoint accepted with allow_loopback false")
	}
	if _, err := NewInvoker(googleConnection("http://169.254.169.254"), AuthorizeFunc(allow), http.DefaultClient, creds, AllowLoopbackHTTP(true)); err == nil {
		t.Fatal("metadata address accepted")
	}
	inv, err := NewInvoker(googleConnection(agent.URL), AuthorizeFunc(allow), http.DefaultClient, creds, AllowLoopbackHTTP(true))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := inv.Invoke(context.Background(), requestContext(), Invocation{Operation: "query", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || hits.Load() != 0 {
		t.Fatalf("redirect followed: status %d, %d request(s) reached the other host", resp.StatusCode, hits.Load())
	}
}
