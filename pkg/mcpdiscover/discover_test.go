package mcpdiscover

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type noArgs struct{}

func newServer(t *testing.T, pageSize, tools int) *mcp.Server {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, &mcp.ServerOptions{PageSize: pageSize})
	for i := 0; i < tools; i++ {
		name := fmt.Sprintf("tool_%02d", i)
		mcp.AddTool(s, &mcp.Tool{Name: name, Description: "does " + name}, func(context.Context, *mcp.CallToolRequest, noArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
	return s
}

func serve(t *testing.T, s *mcp.Server, wrap func(http.Handler) http.Handler) *httptest.Server {
	t.Helper()
	var h http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	if wrap != nil {
		h = wrap(h)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func client() *http.Client { return &http.Client{Timeout: 5 * time.Second} }

func TestListToolsPlain(t *testing.T) {
	ts := serve(t, newServer(t, 0, 2), nil)
	tools, err := ListTools(context.Background(), client(), ts.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "tool_00" || tools[0].Description != "does tool_00" || tools[0].InputSchema == nil {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestListToolsPaginates(t *testing.T) {
	ts := serve(t, newServer(t, 2, 5), nil)
	tools, err := ListTools(context.Background(), client(), ts.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 5 {
		t.Fatalf("got %d tools, want 5 across 3 pages", len(tools))
	}
}

func TestListToolsSendsHeadersToOriginOnly(t *testing.T) {
	var mu sync.Mutex
	seen := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("credential leaked to a different origin")
		}
	}))
	defer other.Close()
	ts := serve(t, newServer(t, 0, 1), func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			if r.Header.Get("Authorization") == "Bearer s3cret" && r.Header.Get("X-Extra") == "1" {
				seen++
			}
			mu.Unlock()
			next.ServeHTTP(w, r)
		})
	})
	h := http.Header{"Authorization": {"Bearer s3cret"}, "X-Extra": {"1"}}
	if _, err := ListTools(context.Background(), client(), ts.URL, Options{Headers: h}); err != nil {
		t.Fatal(err)
	}
	if seen < 2 {
		t.Fatalf("headers seen on %d requests, want every request", seen)
	}

	// A redirect to another origin must not carry the headers.
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	_, _ = ListTools(context.Background(), client(), redir.URL, Options{Headers: h})
}

func TestListToolsRefusedRedirect(t *testing.T) {
	target := serve(t, newServer(t, 0, 1), nil)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	c := client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if _, err := ListTools(context.Background(), c, redir.URL, Options{}); err == nil {
		t.Fatal("expected the redirect to be refused")
	}
	if _, err := ListTools(context.Background(), client(), redir.URL, Options{}); err != nil {
		t.Fatalf("redirect allowed by default: %v", err)
	}
}

func TestListToolsCredentialRequired(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer ts.Close()
	_, err := ListTools(context.Background(), client(), ts.URL, Options{Headers: http.Header{"Authorization": {"Bearer s3cret"}}})
	if !errors.Is(err, ErrCredentialRequired) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error leaks header value: %v", err)
	}
}

func TestListToolsResponseCap(t *testing.T) {
	ts := serve(t, newServer(t, 0, 20), nil)
	_, err := ListTools(context.Background(), client(), ts.URL, Options{MaxResponseBytes: 300})
	if err == nil {
		t.Fatal("expected an error when a response exceeds the cap")
	}
}

func TestListToolsEndpointErrors(t *testing.T) {
	if _, err := ListTools(context.Background(), client(), "", Options{}); err == nil {
		t.Fatal("empty endpoint accepted")
	}
	if _, err := ListTools(context.Background(), client(), "http://127.0.0.1:1", Options{}); err == nil {
		t.Fatal("unreachable endpoint accepted")
	}
}
