package aiplex_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func isInput(err error) bool {
	var in *aiplex.InputError
	return errors.As(err, &in)
}

func TestInvokeAgentStreamsTheReplyAsItArrives(t *testing.T) {
	release := make(chan struct{})
	var gotBody, gotAuth, gotPath, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotAuth, gotPath, gotCT = string(b), r.Header.Get("Authorization"), r.URL.EscapedPath(), r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "req-1")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: two\n\n")
	}))
	defer srv.Close()
	c := aiplex.NewClient(srv.URL)
	c.SetToken("tok")
	resp, err := c.InvokeAgent(context.Background(), "agent-1", aiplex.InvokeAgentRequest{
		Operation: "invoke", SessionID: "s1", Payload: []byte(`{"input":"hi"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("X-Request-Id") != "req-1" {
		t.Fatalf("%+v", resp)
	}
	// The first event must be readable while the server still holds the reply open.
	buf := make([]byte, len("data: one\n\n"))
	if _, err = io.ReadFull(resp.Body, buf); err != nil || string(buf) != "data: one\n\n" {
		t.Fatalf("first event: %q %v", buf, err)
	}
	close(release)
	rest, _ := io.ReadAll(resp.Body)
	if string(rest) != "data: two\n\n" {
		t.Fatalf("rest: %q", rest)
	}
	if gotPath != "/api/v1/a2a/connections/agent-1/invoke" || gotAuth != "Bearer tok" || gotCT != "application/json" {
		t.Fatal(gotPath, gotAuth, gotCT)
	}
	if !strings.Contains(gotBody, `"operation":"invoke"`) || !strings.Contains(gotBody, `"session_id":"s1"`) || !strings.Contains(gotBody, `"payload":{"input":"hi"}`) {
		t.Fatal(gotBody)
	}
}

func TestInvokeAgentIsNotCutOffByTheClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(w, "late")
	}))
	defer srv.Close()
	c := aiplex.NewClient(srv.URL)
	c.HTTPClient.Timeout = 50 * time.Millisecond
	resp, err := c.InvokeAgent(context.Background(), "a", aiplex.InvokeAgentRequest{Operation: "invoke"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, _ := io.ReadAll(resp.Body); string(b) != "late" {
		t.Fatal(string(b))
	}
}

func TestInvokeAgentRefusesWhatTheServerWouldRefuseBeforeSending(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	c := aiplex.NewClient(srv.URL)
	cases := map[string]struct {
		id  string
		req aiplex.InvokeAgentRequest
	}{
		"empty id":          {"", aiplex.InvokeAgentRequest{Operation: "invoke"}},
		"traversal id":      {"..", aiplex.InvokeAgentRequest{Operation: "invoke"}},
		"slash id":          {"a/b", aiplex.InvokeAgentRequest{Operation: "invoke"}},
		"no operation":      {"a", aiplex.InvokeAgentRequest{}},
		"padded operation":  {"a", aiplex.InvokeAgentRequest{Operation: " invoke"}},
		"payload not json":  {"a", aiplex.InvokeAgentRequest{Operation: "invoke", Payload: []byte(`{nope`)}},
		"payload too large": {"a", aiplex.InvokeAgentRequest{Operation: "invoke", Payload: []byte(`"` + strings.Repeat("x", 1<<20) + `"`)}},
	}
	for name, tc := range cases {
		resp, err := c.InvokeAgent(context.Background(), tc.id, tc.req)
		if err == nil || resp != nil || !isInput(err) {
			t.Errorf("%s: want an InputError, got %v", name, err)
		}
	}
	if calls != 0 {
		t.Fatalf("%d requests were sent", calls)
	}
}

func TestInvokeAgentReturnsTheAPIErrorWithoutAResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"code":"ACTION_DENIED","message":"missing grant"}`)
	}))
	defer srv.Close()
	resp, err := aiplex.NewClient(srv.URL).InvokeAgent(context.Background(), "a", aiplex.InvokeAgentRequest{Operation: "invoke"})
	var apiErr *aiplex.Error
	if resp != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != 403 || apiErr.Code != "ACTION_DENIED" {
		t.Fatalf("%v %v", resp, err)
	}
}

func TestListDelegationsWithOptionsSendsAgentAndLimit(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `[{"id":"d1","status":"running"}]`)
	}))
	defer srv.Close()
	c := aiplex.NewClient(srv.URL)
	list, err := c.ListDelegationsWithOptions(context.Background(), aiplex.ListDelegationsOptions{AgentID: "a&b=c", Limit: 5})
	if err != nil || len(list) != 1 || list[0].ID != "d1" {
		t.Fatal(list, err)
	}
	if query != "agent_id=a%26b%3Dc&limit=5" {
		t.Fatal(query)
	}
	if _, err = c.ListDelegationsWithOptions(context.Background(), aiplex.ListDelegationsOptions{}); err != nil || query != "" {
		t.Fatalf("empty options must send no query: %q %v", query, err)
	}
	for _, bad := range []aiplex.ListDelegationsOptions{{Limit: -1}, {Limit: 1001}, {AgentID: "a\nb"}, {AgentID: " a"}} {
		if _, err = c.ListDelegationsWithOptions(context.Background(), bad); !isInput(err) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	// The unfiltered method keeps working.
	if _, err = c.ListDelegations(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDelegationIDsAreValidatedOnEveryCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = io.WriteString(w, `{}`) }))
	defer srv.Close()
	c := aiplex.NewClient(srv.URL)
	ctx := context.Background()
	for _, id := range []string{"", "..", "a/b", " a", "a\nb"} {
		if _, err := c.GetDelegation(ctx, id); !isInput(err) {
			t.Errorf("get %q: %v", id, err)
		}
		if _, err := c.GetDelegationChain(ctx, id); !isInput(err) {
			t.Errorf("chain %q: %v", id, err)
		}
		if _, err := c.UpdateDelegation(ctx, id, &aiplex.UpdateDelegationRequest{Status: "completed"}); !isInput(err) {
			t.Errorf("update %q: %v", id, err)
		}
	}
	for _, st := range []string{"", "done", "COMPLETED"} {
		if _, err := c.UpdateDelegation(ctx, "d1", &aiplex.UpdateDelegationRequest{Status: st}); !isInput(err) {
			t.Errorf("status %q: %v", st, err)
		}
	}
	if _, err := c.UpdateDelegation(ctx, "d1", &aiplex.UpdateDelegationRequest{Status: "failed", Error: strings.Repeat("x", 8193)}); !isInput(err) {
		t.Errorf("long error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("%d requests were sent", calls)
	}
	if _, err := c.GetDelegation(ctx, "d1"); err != nil {
		t.Fatal(err)
	}
}
