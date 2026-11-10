package aiplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentIdentityRoutesAndRevisions(t *testing.T) {
	type seen struct{ method, path, ifMatch, body string }
	var calls []seen
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, seen{r.Method, r.URL.EscapedPath(), r.Header.Get("If-Match"), string(body)})
		if r.Header.Get("Authorization") != "Bearer t" {
			t.Error("missing bearer")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/agents/identity-presets":
			_, _ = w.Write([]byte(`{"presets":[{"id":"github-actions","display_name":"GitHub","cloud":"github","inputs":[{"name":"repository","label":"Repo","required":true,"example":"o/r"}],"algorithms":["RS256"]}],"default_audience":"https://a","live_authentication":false}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/identities"):
			_, _ = w.Write([]byte(`[{"id":"gh","client_id":"agent","preset":"github-actions","issuer":"https://i","subject":"s","revision":4,"disabled":true,"principal_id":"p"}]`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"gh","client_id":"agent","revision":1,"principal_id":"p","actor_id":"a"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/plan"):
			_, _ = w.Write([]byte(`{"binding":{"client_id":"agent","id":"gh","preset":"github-actions"},"cloud":"github","steps":[{"title":"T","language":"bash","content":"echo"}],"token_snippet":{"title":"S"},"aiplex_steps":[]}`))
		default:
			_, _ = w.Write([]byte(`{"id":"gh","client_id":"agent","revision":4}`))
		}
	}))
	defer server.Close()
	c := NewClient(server.URL)
	c.SetToken("t")
	ctx := context.Background()

	cat, err := c.ListAgentIdentityPresets(ctx)
	if err != nil || cat.LiveAuthentication || cat.DefaultAudience != "https://a" || len(cat.Presets) != 1 || !cat.Presets[0].Inputs[0].Required {
		t.Fatal(cat, err)
	}
	if p, ok := cat.Preset("github-actions"); !ok || p.Cloud != "github" {
		t.Fatal("preset lookup")
	}
	list, err := c.ListAgentIdentities(ctx, "agent")
	if err != nil || len(list) != 1 || list[0].Revision != 4 || !list[0].Disabled || list[0].Issuer != "https://i" || list[0].PrincipalID != "p" {
		t.Fatal(list, err)
	}
	got, err := c.GetAgentIdentity(ctx, "agent", "gh")
	if err != nil || got.Revision != 4 {
		t.Fatal(got, err)
	}
	req := AgentIdentityRequest{Preset: "github-actions", Inputs: map[string]string{"repository": "o/r"}, DisplayName: "CI"}
	created, err := c.PutAgentIdentity(ctx, "agent", "gh", req, 0)
	if err != nil || created.Revision != 1 || created.ActorID != "a" {
		t.Fatal(created, err)
	}
	if _, err = c.PutAgentIdentity(ctx, "agent", "gh", req, 4); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteAgentIdentity(ctx, "agent", "gh", 4); err != nil {
		t.Fatal(err)
	}
	plan, err := c.PlanAgentIdentity(ctx, "agent", "gh")
	if err != nil || len(plan.Steps) != 1 || plan.Steps[0].Language != "bash" {
		t.Fatal(plan, err)
	}
	// calls: presets, list, get, put(create), put(replace), delete, plan
	if calls[3].ifMatch != "" || calls[4].ifMatch != `"4"` || calls[5].ifMatch != `"4"` {
		t.Fatalf("If-Match header wrong: %+v", calls)
	}
	if calls[3].path != "/api/v1/agents/agent/identities/gh" || calls[6].method != http.MethodPost || calls[6].path != "/api/v1/agents/agent/identities/gh/plan" {
		t.Fatalf("paths wrong: %+v", calls)
	}
	if !strings.Contains(calls[3].body, `"preset":"github-actions"`) || strings.Contains(calls[3].body, "disabled") {
		t.Fatalf("body wrong: %s", calls[3].body)
	}
}

func TestAgentIdentityClientSideGuards(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	ctx := context.Background()
	if _, err := c.ListAgentIdentities(ctx, "../x"); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err := c.GetAgentIdentity(ctx, "a", ""); err == nil {
		t.Fatal("empty binding id accepted")
	}
	if err := c.DeleteAgentIdentity(ctx, "a", "b", 0); err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatal(err)
	}
	if _, err := c.VerifyAgentIdentityToken(ctx, AgentIdentityVerifyRequest{ClientID: "a", Token: "x"}); err == nil {
		t.Fatal("half-pinned verify accepted")
	}
	if _, err := c.VerifyAgentIdentityToken(ctx, AgentIdentityVerifyRequest{}); err == nil {
		t.Fatal("empty token accepted")
	}
}

func TestAgentIdentityVerifyAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req AgentIdentityVerifyRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/agents/identities/verify" || req.Token != "SECRET" {
			t.Errorf("bad request %s", r.URL.Path)
		}
		if req.ClientID == "conflict" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"REVISION_CONFLICT","message":"changed"}`))
			return
		}
		_, _ = w.Write([]byte(`{"valid":false,"checks":[{"name":"signature","status":"pass","reason":"ok"},{"name":"audience","status":"fail","reason":"aud mismatch"},{"name":"binding","status":"skipped","reason":"n/a"}],"issuer":"https://i","live_authentication":true,"principal_id":"p"}`))
	}))
	defer server.Close()
	c := NewClient(server.URL)
	res, err := c.VerifyAgentIdentityToken(context.Background(), AgentIdentityVerifyRequest{Token: "SECRET"})
	if err != nil || res.Valid || len(res.Checks) != 3 || res.Checks[1].Status != AgentIdentityCheckFail || res.Checks[1].Reason != "aud mismatch" || !res.LiveAuthentication || res.PrincipalID != "p" {
		t.Fatal(res, err)
	}
	_, err = c.VerifyAgentIdentityToken(context.Background(), AgentIdentityVerifyRequest{Token: "SECRET", ClientID: "conflict", BindingID: "b"})
	apiErr, ok := err.(*Error)
	if !ok || apiErr.Code != "REVISION_CONFLICT" || apiErr.StatusCode != 409 || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}
	r := AgentIdentityVerifyRequest{Token: "SECRET", ClientID: "a", BindingID: "b"}
	if strings.Contains(fmt.Sprintf("%v %+v %#v %s", r, r, r, r), "SECRET") {
		t.Fatal("token printed by formatting")
	}
}
