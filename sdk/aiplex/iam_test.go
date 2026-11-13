package aiplex_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestIAMClientRoutesBodiesAndRevisions(t *testing.T) {
	type seen struct{ method, path, query, ifMatch, body string }
	var calls []seen
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("If-Match"), string(b)})
		binding := map[string]any{"id": "rb-1", "group": "eng", "role": "viewer", "scopes": []string{}, "revision": 2, "created_at": "2026-11-01T00:00:00Z", "created_by": "admin"}
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(204)
		case r.URL.Path == "/api/v1/iam/roles":
			json.NewEncoder(w).Encode([]map[string]any{{"role": "admin", "default_scopes": []string{"mcp:tools:*"}}, {"role": "viewer", "default_scopes": nil}})
		case r.URL.Path == "/api/v1/iam/validate-principal":
			json.NewEncoder(w).Encode(map[string]any{"valid": false, "error": "bad principal"})
		case r.URL.Path == "/api/v1/iam/role-bindings" && r.Method == http.MethodGet:
			if r.URL.Query().Get("group") == "none" {
				w.Write([]byte("null"))
				return
			}
			json.NewEncoder(w).Encode([]any{binding})
		case r.Method == http.MethodPost:
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(binding)
		default:
			json.NewEncoder(w).Encode(binding)
		}
	}))
	defer server.Close()
	c := aiplex.NewClient(server.URL)
	ctx := context.Background()

	if roles, err := c.ListRoles(ctx); err != nil || len(roles) != 2 || roles[0].Role != "admin" || roles[0].DefaultScopes[0] != "mcp:tools:*" {
		t.Fatal(roles, err)
	}
	if list, err := c.ListRoleBindings(ctx, ""); err != nil || len(list) != 1 || list[0].Revision != 2 {
		t.Fatal(list, err)
	}
	if list, err := c.ListRoleBindings(ctx, "none"); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("a null list must read as empty: %v %v", list, err)
	}
	if rb, err := c.GetRoleBinding(ctx, "rb-1"); err != nil || rb.Group != "eng" || rb.CreatedBy != "admin" {
		t.Fatal(rb, err)
	}
	created, err := c.CreateRoleBinding(ctx, aiplex.RoleBindingInput{Group: "eng", Role: "viewer"})
	if err != nil || created.ID != "rb-1" {
		t.Fatal(created, err)
	}
	if _, err = c.UpdateRoleBinding(ctx, "rb-1", aiplex.RoleBindingInput{Group: "eng", Role: "deployer", Scopes: []string{}}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateRoleBinding(ctx, "rb-1", aiplex.RoleBindingInput{ID: "other", Group: "eng", Role: "viewer"}, 2); err == nil {
		t.Fatal("an id that differs from the path was accepted")
	}
	if v, err := c.ValidatePrincipal(ctx, "x"); err != nil || v.Valid || v.Error != "bad principal" {
		t.Fatal(v, err)
	}
	if err := c.DeleteRoleBinding(ctx, "rb-1", 0); err == nil {
		t.Fatal("delete without a revision accepted")
	}
	if err := c.DeleteRoleBinding(ctx, "rb-1", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetRoleBinding(ctx, "../x"); err == nil {
		t.Fatal("path aliasing accepted")
	}

	want := []seen{
		{"GET", "/api/v1/iam/roles", "", "", ""},
		{"GET", "/api/v1/iam/role-bindings", "", "", ""},
		{"GET", "/api/v1/iam/role-bindings", "group=none", "", ""},
		{"GET", "/api/v1/iam/role-bindings/rb-1", "", "", ""},
		{"POST", "/api/v1/iam/role-bindings", "", "0", `{"group":"eng","role":"viewer","provider":"","pool_id":"","description":""}`},
		{"PUT", "/api/v1/iam/role-bindings/rb-1", "", "2", `{"group":"eng","role":"deployer","provider":"","pool_id":"","description":"","scopes":[]}`},
		{"POST", "/api/v1/iam/validate-principal", "", "", `{"principal":"x"}`},
		{"DELETE", "/api/v1/iam/role-bindings/rb-1", "", "2", ""},
	}
	if len(calls) != len(want) {
		t.Fatalf("%d calls: %+v", len(calls), calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d: %+v want %+v", i, calls[i], want[i])
		}
	}
}

func TestIAMClientValidatesLikeTheServer(t *testing.T) {
	c := aiplex.NewClient("http://127.0.0.1:1") // never reached
	ctx := context.Background()
	long := strings.Repeat("a", 513)
	for name, in := range map[string]aiplex.RoleBindingInput{
		"blank group":    {Group: "  ", Role: "viewer"},
		"long group":     {Group: long, Role: "viewer"},
		"newline group":  {Group: "a\nb", Role: "viewer"},
		"unknown role":   {Group: "g", Role: "owner"},
		"empty role":     {Group: "g"},
		"long pool":      {Group: "g", Role: "viewer", PoolID: long},
		"newline source": {Group: "g", Role: "viewer", Provider: "a\r\nb"},
		"slash id":       {ID: "a/b", Group: "g", Role: "viewer"},
		"long id":        {ID: strings.Repeat("a", 257), Group: "g", Role: "viewer"},
		"percent id":     {ID: "a%b", Group: "g", Role: "viewer"},
		"leading space":  {ID: " lead", Group: "g", Role: "viewer"},
		"dot dot id":     {ID: "..", Group: "g", Role: "viewer"},
	} {
		if _, err := c.CreateRoleBinding(ctx, in); err == nil || strings.Contains(err.Error(), "request failed") {
			t.Errorf("%s: want a local refusal, got %v", name, err)
		}
	}
	if _, err := c.UpdateRoleBinding(ctx, "rb", aiplex.RoleBindingInput{Group: "g", Role: "viewer"}, 0); err == nil || strings.Contains(err.Error(), "request failed") {
		t.Errorf("update without a revision: %v", err)
	}
	if _, err := c.ValidatePrincipal(ctx, ""); err == nil || strings.Contains(err.Error(), "request failed") {
		t.Errorf("empty principal: %v", err)
	}
}

func TestIAMClientReadsNullScopesAsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/role-bindings") {
			w.Write([]byte(`[{"id":"b","revision":1,"group":"g","role":"viewer","scopes":null}]`))
			return
		}
		w.Write([]byte(`{"id":"b","revision":1,"group":"g","role":"viewer","scopes":null}`))
	}))
	defer server.Close()
	c := aiplex.NewClient(server.URL)
	got, err := c.GetRoleBinding(context.Background(), "b")
	if err != nil || got.Scopes == nil {
		t.Fatal(got, err)
	}
	list, err := c.ListRoleBindings(context.Background(), "")
	if err != nil || list[0].Scopes == nil {
		t.Fatal(list, err)
	}
}

func TestIAMClientSurfacesConflicts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(412)
		w.Write([]byte(`{"code":"REVISION_CONFLICT","message":"configuration changed; reload before retrying"}`))
	}))
	defer server.Close()
	_, err := aiplex.NewClient(server.URL).UpdateRoleBinding(context.Background(), "rb", aiplex.RoleBindingInput{Group: "g", Role: "viewer"}, 1)
	var apiErr *aiplex.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 412 || apiErr.Code != "REVISION_CONFLICT" {
		t.Fatal(err)
	}
}

func TestInvalidateUserMembership(t *testing.T) {
	var path, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path, body = r.Method+" "+r.URL.Path, string(b)
		w.Write([]byte(`{"principal_id":"p1","not_before":"2026-11-13T10:00:00Z","scope":"this server process"}`))
	}))
	defer server.Close()
	c := aiplex.NewClient(server.URL)
	res, err := c.InvalidateUserMembership(context.Background(), "okta", "alice")
	if err != nil || res.PrincipalID != "p1" || res.Scope != "this server process" || res.NotBefore.IsZero() {
		t.Fatal(res, err)
	}
	if path != "POST /api/v1/identity/user-providers/okta/membership/invalidate" || body != `{"subject":"alice"}` {
		t.Fatal(path, body)
	}
	for _, subject := range []string{"", " a", "a\n"} {
		if _, err := c.InvalidateUserMembership(context.Background(), "okta", subject); err == nil {
			t.Errorf("subject %q accepted", subject)
		}
	}
	if _, err := c.InvalidateUserMembership(context.Background(), "../x", "a"); err == nil {
		t.Error("path aliasing accepted")
	}
}
