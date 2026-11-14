package main

import (
	"errors"
	"strings"
	"testing"
)

const iamBindingJSON = `{"id":"rb-1","revision":3,"group":"platform-eng","role":"deployer","provider":"https://idp.example","pool_id":"people","scopes":["mcp:tools:*"],"description":"Platform","created_at":"2026-11-01T00:00:00Z","created_by":"admin"}`

func exitCodeOf(err error) int {
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	return -1
}

func TestIAMRolesListGetAndJSON(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/iam/roles":                 `[{"role":"admin","default_scopes":["mcp:tools:*","llm:model:*"]},{"role":"viewer","default_scopes":null}]`,
		"GET /api/v1/iam/role-bindings":         "[" + iamBindingJSON + "]",
		"GET /api/v1/iam/role-bindings/rb-1":    iamBindingJSON,
		"POST /api/v1/iam/validate-principal":   `{"valid":false,"error":"invalid WIF principal URI"}`,
		"DELETE /api/v1/iam/role-bindings/rb-1": "204",
	})
	out, err := runSP(t, "", iamCmd(), "roles")
	if err != nil || !strings.Contains(out, "mcp:tools:*,llm:model:*") || !strings.Contains(out, "viewer") {
		t.Fatalf("roles: %v\n%s", err, out)
	}
	out, err = runSP(t, "", iamCmd(), "role-bindings", "list", "--group", "platform-eng")
	if err != nil || !strings.Contains(out, "rb-1") || !strings.Contains(out, "deployer") || !strings.Contains(out, "https://idp.example") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if last := calls[len(calls)-1]; last.query != "group=platform-eng" {
		t.Fatalf("group filter not sent: %+v", last)
	}
	out, err = runSP(t, "", iamCmd(), "role-bindings", "get", "rb-1")
	if err != nil || !strings.Contains(out, "Role binding rb-1 (revision 3)") || !strings.Contains(out, "Pool:        people") || !strings.Contains(out, "mcp:tools:*") {
		t.Fatalf("get: %v\n%s", err, out)
	}
	output = "json"
	out, err = runSP(t, "", iamCmd(), "role-bindings", "get", "rb-1")
	output = "table"
	if err != nil || !strings.Contains(out, `"revision": 3`) {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if _, err = runSP(t, "", iamCmd(), "validate-principal", "nope"); err == nil || !strings.Contains(err.Error(), "invalid WIF principal URI") || exitCodeOf(err) != exitError {
		t.Fatalf("an invalid principal must fail: %v", err)
	}
}

func TestIAMDeleteAsksFirstAndSendsTheRevisionItRead(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/iam/role-bindings/rb-1":    iamBindingJSON,
		"DELETE /api/v1/iam/role-bindings/rb-1": "204",
	})
	if _, err := runSP(t, "", iamCmd(), "role-bindings", "delete", "rb-1"); err == nil {
		t.Fatal("delete without --yes ran")
	}
	for _, c := range calls {
		if c.method == "DELETE" {
			t.Fatalf("delete was sent: %+v", c)
		}
	}
	out, err := runSP(t, "", iamCmd(), "role-bindings", "delete", "rb-1", "--yes")
	if err != nil || !strings.Contains(out, "Deleted role binding rb-1") {
		t.Fatalf("delete: %v %s", err, out)
	}
	if last := calls[len(calls)-1]; last.method != "DELETE" || last.ifMatch != "3" {
		t.Fatalf("delete must carry the revision it read: %+v", calls)
	}
}

func TestIAMApplyCreatesUpdatesAndChecksRevision(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/iam/role-bindings/rb-1": iamBindingJSON,
		"PUT /api/v1/iam/role-bindings/rb-1": strings.Replace(iamBindingJSON, `"revision":3`, `"revision":4`, 1),
		"POST /api/v1/iam/role-bindings":     "201:" + strings.Replace(iamBindingJSON, `"id":"rb-1"`, `"id":"rb-new"`, 1),
	})
	last := func() spCall { return calls[len(calls)-1] }

	// A new binding without an id: created with If-Match 0 and no scopes key.
	out, err := runSP(t, "group: eng\nrole: viewer\n", iamCmd(), "role-bindings", "apply", "-f", "-")
	if err != nil || !strings.Contains(out, "Created role binding rb-new") {
		t.Fatalf("create: %v\n%s", err, out)
	}
	if c := last(); c.method != "POST" || c.ifMatch != "0" || strings.Contains(c.body, "scopes") || !strings.Contains(c.body, `"group":"eng"`) {
		t.Fatalf("create request: %+v", c)
	}

	// An existing id: replaced at the stored revision; explicit empty scopes are sent.
	calls = nil
	out, err = runSP(t, "id: rb-1\ngroup: eng\nrole: admin\nscopes: []\n", iamCmd(), "role-bindings", "apply", "-f", "-")
	if err != nil || !strings.Contains(out, "Updated role binding rb-1 (revision 4)") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if c := last(); c.method != "PUT" || c.ifMatch != "3" || !strings.Contains(c.body, `"scopes":[]`) || !strings.Contains(c.body, `"provider":""`) {
		t.Fatalf("update request: %+v", c)
	}

	// A file from 'get -o yaml' applies (server-owned fields tolerated) when current...
	calls = nil
	if _, err = runSP(t, iamBindingJSON, iamCmd(), "role-bindings", "apply", "-f", "-"); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	// ...and is refused locally, with the conflict exit code, when stale.
	calls = nil
	stale := strings.Replace(iamBindingJSON, `"revision":3`, `"revision":2`, 1)
	_, err = runSP(t, stale, iamCmd(), "role-bindings", "apply", "-f", "-")
	if err == nil || exitCodeOf(err) != exitConflict {
		t.Fatalf("stale revision must be a conflict: %v", err)
	}
	for _, c := range calls {
		if c.method == "PUT" {
			t.Fatalf("stale apply sent a PUT: %+v", c)
		}
	}

	// An update must state its scopes: omitted or null would keep the stored
	// ones unseen. Refused as invalid input, nothing written.
	for _, file := range []string{"id: rb-1\ngroup: eng\nrole: viewer\n", "id: rb-1\ngroup: eng\nrole: viewer\nscopes:\n", "id: rb-1\ngroup: eng\nrole: viewer\nscopes: null\n"} {
		calls = nil
		_, err = runSP(t, file, iamCmd(), "role-bindings", "apply", "-f", "-")
		if exitCodeOf(err) != exitUsage || !strings.Contains(err.Error(), "scopes") {
			t.Fatalf("%q: want a usage error about scopes, got %v", file, err)
		}
		for _, c := range calls {
			if c.method == "PUT" || c.method == "POST" {
				t.Fatalf("%q wrote: %+v", file, c)
			}
		}
	}

	// A bootstrap binding with no stored scopes reads as [] and applies back.
	calls = nil
	bootstrap := strings.Replace(iamBindingJSON, `"scopes":["mcp:tools:*"]`, `"scopes":null`, 1)
	spServer(t, &calls, map[string]string{
		"GET /api/v1/iam/role-bindings/rb-1": bootstrap,
		"PUT /api/v1/iam/role-bindings/rb-1": bootstrap,
	})
	output = "yaml"
	got, err := runSP(t, "", iamCmd(), "role-bindings", "get", "rb-1")
	output = "table"
	if err != nil || !strings.Contains(got, "scopes: []") {
		t.Fatalf("get of a binding with no scopes must print scopes: []: %v\n%s", err, got)
	}
	calls = nil
	if _, err = runSP(t, got, iamCmd(), "role-bindings", "apply", "-f", "-"); err != nil {
		t.Fatalf("round trip of a bootstrap binding: %v", err)
	}
	if c := last(); c.method != "PUT" || !strings.Contains(c.body, `"scopes":[]`) {
		t.Fatalf("round trip request: %+v", c)
	}

	// Unknown fields and a revision without an id are refused before any call.
	calls = nil
	if _, err = runSP(t, "group: g\nrole: viewer\nbogus: 1\n", iamCmd(), "role-bindings", "apply", "-f", "-"); exitCodeOf(err) != exitUsage {
		t.Fatal("unknown field accepted")
	}
	if _, err = runSP(t, "group: g\nrole: viewer\nrevision: 2\n", iamCmd(), "role-bindings", "apply", "-f", "-"); exitCodeOf(err) != exitUsage {
		t.Fatalf("revision without id: %v", err)
	}
	if _, err = runSP(t, "group: g\nrole: owner\n", iamCmd(), "role-bindings", "apply", "-f", "-"); exitCodeOf(err) != exitUsage {
		t.Fatal("unknown role accepted")
	}
	if len(calls) != 0 {
		t.Fatalf("refused files must not reach the server: %+v", calls)
	}
}

func TestInvalidateMembershipCommand(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"POST /api/v1/identity/user-providers/okta/membership/invalidate": `{"principal_id":"p1","not_before":"2026-11-13T10:00:00Z","scope":"this server process"}`,
	})
	out, err := runSP(t, "", identityUserCmd(), "user-providers", "invalidate-membership", "okta", "--subject", "alice")
	if err != nil || !strings.Contains(out, "Invalidated group membership of p1") || !strings.Contains(out, "this server process") {
		t.Fatalf("invalidate: %v\n%s", err, out)
	}
	if calls[0].body != `{"subject":"alice"}` {
		t.Fatalf("body: %+v", calls[0])
	}
	if _, err = runSP(t, "", identityUserCmd(), "user-providers", "invalidate-membership", "okta"); err == nil {
		t.Fatal("missing --subject accepted")
	}
}
