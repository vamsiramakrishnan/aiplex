package aiplex_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// redirectPair starts an API that answers every request with a redirect and a
// second server that counts what reaches it. The Location carries a secret in
// its path and query, which must never appear in an error.
func redirectPair(t *testing.T, status int, otherHost bool) (api *httptest.Server, hits *atomic.Int32, target string) {
	t.Helper()
	hits = new(atomic.Int32)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(sink.Close)
	target = sink.URL
	if otherHost {
		target = strings.Replace(sink.URL, "127.0.0.1", "localhost", 1)
	}
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target+"/leak/SECRET?token=SECRET")
		w.WriteHeader(status)
	}))
	t.Cleanup(api.Close)
	return api, hits, target
}

func wantRedirectRefused(t *testing.T, err error, status int, target string) {
	t.Helper()
	var apiErr *aiplex.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %T %v is not *aiplex.Error", err, err)
	}
	if apiErr.Code != "redirect_refused" || apiErr.StatusCode != status {
		t.Fatalf("code %q status %d, want redirect_refused %d", apiErr.Code, apiErr.StatusCode, status)
	}
	u, _ := url.Parse(target)
	if !strings.Contains(apiErr.Message, u.Scheme+"://"+u.Host) {
		t.Fatalf("message does not name the target host: %q", apiErr.Message)
	}
	for _, s := range []string{apiErr.Error(), apiErr.Message, fmt.Sprintf("%+v", apiErr), fmt.Sprintf("%v", err)} {
		if strings.Contains(s, "SECRET") || strings.Contains(s, "/leak") {
			t.Fatalf("the error leaks the Location path or query: %q", s)
		}
	}
	if !strings.Contains(apiErr.Message, "AIPLEX_URL") {
		t.Fatalf("message does not name the likely cause: %q", apiErr.Message)
	}
}

func TestClientRefusesRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, otherHost := range []bool{false, true} {
			name := fmt.Sprintf("%d/otherHost=%v", status, otherHost)
			t.Run(name, func(t *testing.T) {
				api, hits, target := redirectPair(t, status, otherHost)
				c := aiplex.NewClient(api.URL)
				c.SetToken("tok")

				_, err := c.GetBudgetStatus(context.Background(), "b")
				wantRedirectRefused(t, err, status, target)

				_, err = c.PutBudget(context.Background(), aiplex.Budget{ID: "b"}, 1)
				wantRedirectRefused(t, err, status, target)

				// InvokeAgent is a streaming call with its own request path.
				_, err = c.InvokeAgent(context.Background(), "agent-1", aiplex.InvokeAgentRequest{Operation: "run"})
				wantRedirectRefused(t, err, status, target)

				if n := hits.Load(); n != 0 {
					t.Fatalf("the redirect target received %d requests", n)
				}
			})
		}
	}
}

func TestClientRedirectEdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		location string
		code     string
		want     string // substring of the message
		notWant  string
	}{
		{"relative location names the API host", 301, "/other/SECRET?token=SECRET", "redirect_refused", "http://", "SECRET"},
		{"scheme-relative location", 302, "//elsewhere.example/p?token=SECRET", "redirect_refused", "elsewhere.example", "SECRET"},
		{"missing location", 307, "", "redirect_refused", "no usable Location", ""},
		{"location that does not parse", 308, "http://[::1/SECRET?token=SECRET", "redirect_refused", "no usable Location", "SECRET"},
		{"location without a host", 303, "mailto:a@b.example?token=SECRET", "redirect_refused", "no usable Location", "SECRET"},
		{"host longer than a DNS name", 302, "http://" + strings.Repeat("a", 2000) + ".example/p?token=SECRET", "redirect_refused", "no usable Location", "aaaaaaaaaa"},
		{"not modified is not a redirect", 304, "http://elsewhere.example/", "unknown", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
			}))
			defer api.Close()
			c := aiplex.NewClient(api.URL)
			_, err := c.GetBudgetStatus(context.Background(), "b")
			var apiErr *aiplex.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code || apiErr.StatusCode != tc.status {
				t.Fatalf("got %v, want code %s status %d", err, tc.code, tc.status)
			}
			if !strings.Contains(apiErr.Message, tc.want) {
				t.Fatalf("message %q lacks %q", apiErr.Message, tc.want)
			}
			if tc.notWant != "" && strings.Contains(apiErr.Error(), tc.notWant) {
				t.Fatalf("error %q contains %q", apiErr.Error(), tc.notWant)
			}
		})
	}
}

// A client the caller supplies keeps its own redirect policy; the SDK does not
// edit it. Only a 3xx that reaches the SDK is turned into the typed error.
func TestClientLeavesACallerSuppliedHTTPClientAlone(t *testing.T) {
	api, _, target := redirectPair(t, 302, false)
	var called atomic.Int32
	c := aiplex.NewClient(api.URL)
	c.HTTPClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		called.Add(1)
		return http.ErrUseLastResponse
	}}
	_, err := c.GetBudgetStatus(context.Background(), "b")
	wantRedirectRefused(t, err, 302, target)
	if called.Load() == 0 {
		t.Fatal("the caller's CheckRedirect was replaced by the SDK")
	}
}

func TestNoFollowRedirectsIsExported(t *testing.T) {
	if err := aiplex.NoFollowRedirects(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("got %v", err)
	}
	if aiplex.NewClient("http://x").HTTPClient.CheckRedirect == nil {
		t.Fatal("the default client follows redirects")
	}
}

// CallConnectionTool and InvokeAgent have their own response handling: both
// must refuse a redirect the same way, without reaching the target.
func TestCallConnectionToolRefusesRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		api, hits, target := redirectPair(t, status, false)
		c := aiplex.NewClient(api.URL)
		c.SetToken("tok")
		_, err := c.CallConnectionTool(context.Background(), "conn", "echo", nil)
		wantRedirectRefused(t, err, status, target)
		if hits.Load() != 0 {
			t.Fatalf("%d: the redirect target received a request", status)
		}
	}
}
