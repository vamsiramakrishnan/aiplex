package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// redirecting returns a server that answers every request with status and a
// Location on a sink, and the sink's request counter.
func redirecting(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"x","token":"x"}`))
	}))
	t.Cleanup(sink.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", sink.URL+"/leak/SECRET?token=SECRET")
		w.WriteHeader(status)
	}))
	t.Cleanup(api.Close)
	return api, &hits
}

func wantRefused(t *testing.T, what string, err error, hits *atomic.Int32) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "redirect_refused") || !strings.Contains(err.Error(), "does not follow redirects") {
		t.Fatalf("%s: error %v", what, err)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "/leak") {
		t.Fatalf("%s: error leaks the Location: %v", what, err)
	}
	if hits.Load() != 0 {
		t.Fatalf("%s: the redirect target received %d request(s)", what, hits.Load())
	}
}

// Every request that carries a token, device code or refresh token refuses
// redirects, for the statuses that replay a body (307, 308) and the others.
func TestCredentialRequestsRefuseRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			api, hits := redirecting(t, status)
			_, err := mintDevToken(t.Context(), api.URL, "dev-bearer", devstack.DevTokenRequest{User: "u"})
			wantRefused(t, "mintDevToken", err, hits)
			_, err = requestDeviceCode(api.URL)
			wantRefused(t, "requestDeviceCode", err, hits)
			_, err = refreshToken(api.URL, "refresh-secret")
			wantRefused(t, "refreshToken", err, hits)
		})
	}
}

func TestDevstackSeedClientRefusesRedirects(t *testing.T) {
	api, hits := redirecting(t, 307)
	err := devstack.API{URL: api.URL, Token: "dev"}.Do(t.Context(), http.MethodPut, "/x", nil, map[string]string{"a": "b"}, nil)
	if err == nil || hits.Load() != 0 {
		t.Fatalf("err %v, target hits %d", err, hits.Load())
	}
}

// A redirect in the sign-in poll loop stops polling at once with an error,
// instead of retrying (and replaying the device code) until the timeout.
func TestPollForTokenStopsOnARedirect(t *testing.T) {
	for _, status := range []int{307, 302} {
		api, hits := redirecting(t, status)
		_, err := pollForToken(api.URL, "device-code-secret", 1)
		wantRefused(t, "pollForToken", err, hits)
	}
}

// A Location that does not parse must not be quoted. net/http fails such a
// redirect with an error that carries the whole Location, query included,
// before the redirect policy is asked; the shared client drops the header so
// the refusal is the same clear error, and the sign-in poll stops.
func TestCredentialRequestsDoNotQuoteAnUnparseableLocation(t *testing.T) {
	var hits atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Location", "http://[::1/leak?token=SECRET")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(api.Close)
	check := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "redirect_refused") {
			t.Fatalf("%s: error %v", what, err)
		}
		if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "leak") {
			t.Fatalf("%s: error quotes the Location: %v", what, err)
		}
	}
	_, err := mintDevToken(t.Context(), api.URL, "dev-bearer", devstack.DevTokenRequest{User: "u"})
	check("mintDevToken", err)
	_, err = requestDeviceCode(api.URL)
	check("requestDeviceCode", err)
	_, err = refreshToken(api.URL, "refresh-secret")
	check("refreshToken", err)
	before := hits.Load()
	_, err = pollForToken(api.URL, "device-code-secret", 1)
	check("pollForToken", err)
	if got := hits.Load() - before; got != 1 {
		t.Fatalf("pollForToken sent the device code %d times after a redirect, want 1", got)
	}
}
