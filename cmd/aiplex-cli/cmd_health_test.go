package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const healthTestToken = "tok-health-secret"

// healthAPI stands in for the AIPlex API. /healthz and /readyz answer 200; every
// /api/v1 route answers apiStatus. It records the Authorization header each
// path received, so a test can see where the token went.
func healthAPI(t *testing.T, apiStatus int) (*httptest.Server, func(path string) (string, bool)) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if apiStatus != http.StatusOK {
			w.WriteHeader(apiStatus)
			_, _ = w.Write([]byte(`{"code":"TEST","message":"test answer"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	return server, func(path string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		v, ok := seen[path]
		return v, ok
	}
}

// healthServerWith answers each path with the status in statuses (200 when a
// path is not listed). It records the Authorization header of every request.
func healthServerWith(t *testing.T, statuses map[string]int) (*httptest.Server, func(path string) (string, bool)) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		code, ok := statuses[r.URL.Path]
		if !ok {
			code = http.StatusOK
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	return server, func(path string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		v, ok := seen[path]
		return v, ok
	}
}

// healthEnv points the CLI at url with bearer, in an empty config home, and
// restores the flag globals afterwards.
func healthEnv(t *testing.T, url, bearer string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AIPLEX_URL", url)
	t.Setenv("AIPLEX_TOKEN", bearer)
	oldURL, oldToken := apiURL, token
	apiURL, token = "", ""
	t.Cleanup(func() { apiURL, token = oldURL, oldToken })
}

func runHealth(t *testing.T, verbose bool) (string, error) {
	t.Helper()
	cmd := healthCmd()
	if verbose {
		if err := cmd.Flags().Set("verbose", "true"); err != nil {
			t.Fatal(err)
		}
	}
	return captureStdout(t, func() error { return cmd.RunE(cmd, nil) })
}

// The token is sent to the configured API's signed-in routes, never to the
// health endpoints, and it is never printed.
func TestHealthSendsTokenOnlyToAPIProbes(t *testing.T) {
	server, seen := healthAPI(t, http.StatusOK)
	healthEnv(t, server.URL, healthTestToken)

	out, err := runHealth(t, true)
	if err != nil {
		t.Fatalf("healthy deployment failed: %v\n%s", err, out)
	}
	for _, path := range []string{"/api/v1/catalog", "/api/v1/instances", "/api/v1/dashboard/stats"} {
		got, ok := seen(path)
		if !ok || got != "Bearer "+healthTestToken {
			t.Errorf("%s: Authorization = %q (requested %v), want the bearer token", path, got, ok)
		}
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		if got, _ := seen(path); got != "" {
			t.Errorf("%s must stay unauthenticated, got Authorization %q", path, got)
		}
	}
	if strings.Contains(out, healthTestToken) {
		t.Fatalf("the token was printed:\n%s", out)
	}
}

// Auth services are other hosts. The auth probe passes no bearer, so it sends
// no Authorization header; a signed-in probe built from the API URL does.
func TestAuthProbeNeverGetsToken(t *testing.T) {
	api, _ := healthAPI(t, http.StatusOK)
	var authCalls atomic.Int32
	var authHeader atomic.Value
	authHeader.Store("")
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCalls.Add(1)
		authHeader.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer auth.Close()
	client := newHealthHTTPClient()

	if res := runHealthCheck(client, auth.URL+"/auth/login", "", false, false); !res.healthy {
		t.Fatalf("auth probe failed: %+v", res)
	}
	if authCalls.Load() != 1 {
		t.Fatalf("auth host was not probed")
	}
	if got := authHeader.Load().(string); got != "" {
		t.Fatalf("token leaked to the auth host: Authorization = %q", got)
	}

	// Control: a signed-in probe on the configured API does carry the token.
	if res := runHealthCheck(client, api.URL+"/api/v1/catalog", healthTestToken, true, false); !res.healthy {
		t.Fatalf("API probe failed: %+v", res)
	}
}

// Go would keep the Authorization header on a redirect to another port of the
// same host. The health client must not follow it: the second server gets no
// request at all, and the redirect is a failed probe.
func TestHealthRedirectNeverCarriesToken(t *testing.T) {
	var otherHits atomic.Int32
	var apiSawToken atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/catalog" {
			if r.Header.Get("Authorization") == "Bearer "+healthTestToken {
				apiSawToken.Store(true)
			}
			http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer api.Close()
	healthEnv(t, api.URL, healthTestToken)

	out, err := runHealth(t, false)
	if !apiSawToken.Load() {
		t.Fatal("the token was not sent to the configured API; the test proves nothing")
	}
	if otherHits.Load() != 0 {
		t.Fatalf("redirect target received %d request(s): the token was carried to it", otherHits.Load())
	}
	if err == nil || !strings.Contains(out, "[FAIL] Catalog") || !strings.Contains(out, "redirect not followed") {
		t.Fatalf("redirect should fail the Catalog probe, error=%v\n%s", err, out)
	}
	if strings.Contains(out, healthTestToken) {
		t.Fatalf("the token was printed:\n%s", out)
	}
}

// Only a 2xx answer is healthy. Only 401 and 403 on the signed-in routes are
// softer, and they warn; everything else fails the command.
func TestHealthProbeStatusRules(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		status   int
		wantLine string
		wantFail bool
	}{
		{name: "healthz 404 is a wrong URL", path: "/healthz", status: 404, wantLine: "[FAIL] API Server", wantFail: true},
		{name: "healthz 302 is not followed", path: "/healthz", status: 302, wantLine: "[FAIL] API Server", wantFail: true},
		{name: "readyz 401 is not a sign-in answer", path: "/readyz", status: 401, wantLine: "[FAIL] API Readiness", wantFail: true},
		{name: "catalog 404", path: "/api/v1/catalog", status: 404, wantLine: "[FAIL] Catalog", wantFail: true},
		{name: "catalog 429", path: "/api/v1/catalog", status: 429, wantLine: "[FAIL] Catalog", wantFail: true},
		{name: "catalog 401 is a sign-in warning", path: "/api/v1/catalog", status: 401, wantLine: "[WARN] Catalog"},
		{name: "catalog 403 is a role warning", path: "/api/v1/catalog", status: 403, wantLine: "[WARN] Catalog"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := healthServerWith(t, map[string]int{tc.path: tc.status})
			healthEnv(t, server.URL, healthTestToken)

			out, err := runHealth(t, false)
			if (err != nil) != tc.wantFail {
				t.Fatalf("error = %v, want failure %v\n%s", err, tc.wantFail, out)
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Errorf("missing %q in:\n%s", tc.wantLine, out)
			}
		})
	}
}

// With no token configured, no Authorization header is sent to any path. The
// signed-in probes say they are not signed in, and that is a warning.
func TestHealthNoTokenSendsNoCredentials(t *testing.T) {
	server, seen := healthAPI(t, http.StatusUnauthorized)
	healthEnv(t, server.URL, "")

	out, err := runHealth(t, true)
	if err != nil {
		t.Fatalf("no token must warn, not fail: %v\n%s", err, out)
	}
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/catalog", "/api/v1/instances", "/api/v1/dashboard/stats"} {
		got, ok := seen(path)
		if !ok {
			t.Errorf("%s was not probed", path)
		}
		if got != "" {
			t.Errorf("%s: Authorization = %q with no token configured", path, got)
		}
	}
	if !strings.Contains(out, "[WARN] Catalog") || !strings.Contains(out, "not signed in") {
		t.Fatalf("missing the not-signed-in warning:\n%s", out)
	}
}

// An API URL with a path prefix: every probe goes to prefix + path, and the
// token is sent only on the signed-in ones.
func TestHealthAPIURLWithPathPrefix(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		seen[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/prefix/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	healthEnv(t, server.URL+"/prefix", healthTestToken)

	out, err := runHealth(t, false)
	if err != nil {
		t.Fatalf("prefixed API failed: %v\n%s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range paths {
		if !strings.HasPrefix(p, "/prefix/") {
			t.Errorf("probe went outside the prefix: %s", p)
		}
	}
	if seen["/prefix/api/v1/catalog"] != "Bearer "+healthTestToken {
		t.Errorf("signed-in probe under the prefix: Authorization = %q", seen["/prefix/api/v1/catalog"])
	}
	if seen["/prefix/healthz"] != "" {
		t.Errorf("/healthz under the prefix got Authorization %q", seen["/prefix/healthz"])
	}
	if !strings.Contains(out, "[pass] Catalog") {
		t.Errorf("missing pass for Catalog:\n%s", out)
	}
}

// 200 passes. 401 and 403 on the signed-in routes warn and do not fail the
// command. 5xx and an unreachable API fail it.
func TestHealthStatusClassification(t *testing.T) {
	cases := []struct {
		name     string
		status   int // 0: the API is not listening
		wantErr  bool
		wantLine string
		wantText string
		forbid   string
	}{
		{name: "200 healthy", status: 200, wantLine: "[pass] Catalog", forbid: "[FAIL]"},
		{name: "401 not signed in", status: 401, wantLine: "[WARN] Catalog", wantText: "not signed in", forbid: "[FAIL]"},
		{name: "403 lacks role", status: 403, wantLine: "[WARN] Catalog", wantText: "lacks the role", forbid: "[FAIL]"},
		{name: "500 down", status: 500, wantErr: true, wantLine: "[FAIL] Catalog"},
		{name: "unreachable", status: 0, wantErr: true, wantLine: "[FAIL] API Server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var url string
			if tc.status == 0 {
				server, _ := healthAPI(t, 200)
				url = server.URL
				server.Close()
			} else {
				server, _ := healthAPI(t, tc.status)
				url = server.URL
			}
			healthEnv(t, url, healthTestToken)

			out, err := runHealth(t, false)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %v\n%s", err, tc.wantErr, out)
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Errorf("missing %q in:\n%s", tc.wantLine, out)
			}
			if tc.wantText != "" && !strings.Contains(out, tc.wantText) {
				t.Errorf("missing %q in:\n%s", tc.wantText, out)
			}
			if tc.forbid != "" && strings.Contains(out, tc.forbid) {
				t.Errorf("unexpected %q in:\n%s", tc.forbid, out)
			}
			if strings.Contains(out, healthTestToken) {
				t.Errorf("the token was printed:\n%s", out)
			}
		})
	}
}

// The probe client must not follow redirects at all, and the SDK client used by
// the platform-status call must not either.
func TestHealthClientsDoNotFollowRedirects(t *testing.T) {
	if err := newHealthHTTPClient().CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("probe client follows redirects: %v", err)
	}
	c := withoutRedirects(newClient())
	if c.HTTPClient == nil || c.HTTPClient.CheckRedirect == nil {
		t.Fatal("SDK client follows redirects")
	}
	if err := c.HTTPClient.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("SDK client follows redirects: %v", err)
	}
}
