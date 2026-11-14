package aiplex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestListSkillInvocations_LimitInQuery(t *testing.T) {
	var seen url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		_, _ = w.Write([]byte(`[{"id":"i1","agent_id":"a1","skill_name":"s1","status":"ok"}]`))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	ctx := context.Background()

	// Existing three-argument callers keep working and send no limit.
	if _, err := c.ListSkillInvocations(ctx, "a1", "s1"); err != nil {
		t.Fatal(err)
	}
	if seen.Get("limit") != "" || seen.Get("agent_id") != "a1" || seen.Get("skill") != "s1" {
		t.Fatalf("legacy call query %v", seen)
	}

	invs, err := c.ListSkillInvocations(ctx, "", "", aiplex.ListSkillInvocationsOpts{Limit: 7})
	if err != nil || len(invs) != 1 {
		t.Fatalf("%v %+v", err, invs)
	}
	if seen.Get("limit") != "7" || seen.Has("agent_id") || seen.Has("skill") {
		t.Errorf("limit query %v", seen)
	}

	if _, err := c.ListSkillInvocations(ctx, "", "", aiplex.ListSkillInvocationsOpts{}, aiplex.ListSkillInvocationsOpts{}); err == nil {
		t.Error("two options must be refused")
	}
}

// A negative limit is refused before any request; zero still means "not set".
func TestListSkillInvocations_NegativeLimitRefused(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.ListSkillInvocations(ctx, "", "", aiplex.ListSkillInvocationsOpts{Limit: -1}); err == nil {
		t.Fatal("a negative limit must be refused")
	}
	if requests != 0 {
		t.Fatalf("a refused limit must not call the API: %d request(s)", requests)
	}
	if _, err := c.ListSkillInvocations(ctx, "", "", aiplex.ListSkillInvocationsOpts{Limit: 0}); err != nil || requests != 1 {
		t.Fatalf("zero must call the API with the server default: %v, %d request(s)", err, requests)
	}
}
