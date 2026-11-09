package nativeapi_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

func noAuth(context.Context, *http.Request) error { return nil }

func TestClientBoundedAuthenticatedRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/base/v1/resources" || r.URL.Query().Get("limit") != "5" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request shape")
		}
		w.Header().Set("ETag", `"version-1"`)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	client, err := nativeapi.NewClient(server.URL+"/base", server.Client(), nativeapi.AuthFunc(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer secret")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Do(context.Background(), http.MethodPost, "v1/resources", url.Values{"limit": {"5"}}, map[string]string{"request": "value"})
	if err != nil || string(result.Body) != `{"ok":true}` || result.ETag != `"version-1"` || result.StatusCode != 200 || calls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
}

func TestClientRejectsUnsafeTargetsBeforeAuthentication(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com?secret=token", "https://example.com/#fragment", "https://example.com/a/../b", "https://example.com/%252e%252e/b", "file:///tmp/file"} {
		if _, err := nativeapi.NewClient(endpoint, &http.Client{}, nativeapi.AuthFunc(noAuth)); err == nil {
			t.Fatalf("unsafe endpoint accepted: %q", endpoint)
		}
	}
	authCalls := 0
	client, err := nativeapi.NewClient("https://example.com", &http.Client{}, nativeapi.AuthFunc(func(context.Context, *http.Request) error { authCalls++; return errors.New("private credential") }))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/absolute", "//elsewhere/path", "https://elsewhere/path", "../path", "a/../b", "a/%2e%2e/b", "a/%252e%252e/b", "a%2fb", "a%252fb", "a\\b", "a?secret=token", "a#fragment", "a\nheader"} {
		if _, err := client.Do(context.Background(), http.MethodGet, path, nil, nil); err == nil {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
	if authCalls != 0 {
		t.Fatalf("invalid request reached authenticator: %d", authCalls)
	}
}

func TestClientNeverFollowsRedirectsOrRetries(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "/private")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := nativeapi.NewClient(server.URL, server.Client(), nativeapi.AuthFunc(noAuth))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), http.MethodGet, "entry", nil, nil)
	if err == nil || calls != 1 {
		t.Fatalf("redirect followed or request retried: calls=%d err=%v", calls, err)
	}
}

func TestClientLimitsBodiesAndRejectsInvalidJSON(t *testing.T) {
	for _, body := range []string{strings.Repeat("s", nativeapi.MaxBodyBytes+1), "not-json"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		client, err := nativeapi.NewClient(server.URL, server.Client(), nativeapi.AuthFunc(noAuth))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Do(context.Background(), http.MethodGet, "entry", nil, nil); err == nil {
			t.Fatal("unsafe response accepted")
		}
		server.Close()
	}
	calls := 0
	client, err := nativeapi.NewClient("https://example.com", &http.Client{}, nativeapi.AuthFunc(func(context.Context, *http.Request) error { calls++; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(context.Background(), http.MethodPost, "entry", nil, strings.Repeat("s", nativeapi.MaxBodyBytes)); err == nil {
		t.Fatal("oversized payload accepted")
	}
	if calls != 0 {
		t.Fatal("oversized payload reached authenticator")
	}
}

func TestClientErrorsDoNotDiscloseSecrets(t *testing.T) {
	const secret = "PRIVATE-SECRET-TOKEN"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); fmt.Fprint(w, secret) }))
	defer server.Close()
	client, err := nativeapi.NewClient(server.URL, server.Client(), nativeapi.AuthFunc(noAuth))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Do(context.Background(), http.MethodGet, "entry", url.Values{"key": {secret}}, nil)
	if err == nil || strings.Contains(err.Error(), secret) || len(result.Body) > 0 {
		t.Fatalf("secret leaked: result=%+v err=%v", result, err)
	}
	client, err = nativeapi.NewClient(server.URL, server.Client(), nativeapi.AuthFunc(func(context.Context, *http.Request) error { return errors.New(secret) }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(context.Background(), http.MethodGet, "entry", nil, nil); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("auth secret leaked: %v", err)
	}
}

func TestAuthenticatorCannotChangeOrigin(t *testing.T) {
	client, err := nativeapi.NewClient("https://example.com", &http.Client{}, nativeapi.AuthFunc(func(_ context.Context, r *http.Request) error { r.URL.Host = "elsewhere.example"; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(context.Background(), http.MethodGet, "entry", nil, nil); err == nil {
		t.Fatal("authenticator redirected request")
	}
}

func TestClientRequiresConfiguredHTTPClient(t *testing.T) {
	if _, err := nativeapi.NewClient("https://example.com", nil, nativeapi.AuthFunc(noAuth)); err == nil {
		t.Fatal("implicit unconstrained HTTP client accepted")
	}
}

func TestAuthenticatorCannotChangePinnedRequest(t *testing.T) {
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.URL.Path = "/other" },
		func(r *http.Request) { r.URL.RawPath = "/%65ntry" },
		func(r *http.Request) { r.URL.RawQuery = "secret=other" },
		func(r *http.Request) { r.URL.Opaque = "//elsewhere/entry" },
		func(r *http.Request) { r.URL.Fragment = "fragment" },
		func(r *http.Request) { r.Host = "elsewhere.example" },
		func(r *http.Request) { r.Method = http.MethodDelete },
		func(r *http.Request) { r.RequestURI = "/override" },
	} {
		client, err := nativeapi.NewClient("https://example.com", &http.Client{}, nativeapi.AuthFunc(func(_ context.Context, r *http.Request) error { mutate(r); return nil }))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Do(context.Background(), http.MethodGet, "entry", nil, nil); err == nil {
			t.Fatal("authenticator changed a pinned request field")
		}
	}
}
