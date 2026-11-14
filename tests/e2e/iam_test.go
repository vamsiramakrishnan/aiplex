//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// Role bindings through the CLI against the real API: the seeded bindings are
// listed, a new one is created, read, replaced (a stale revision is refused
// with exit code 5) and deleted.
func TestIAMRoleBindingsLifecycle(t *testing.T) {
	e := start(t)

	if r := e.mustCLI("iam", "roles"); !strings.Contains(r.Out, "admin") || !strings.Contains(r.Out, "viewer") {
		t.Fatalf("roles: %s", r)
	}
	if r := e.mustCLI("iam", "role-bindings", "list"); !strings.Contains(r.Out, "local-admin") || !strings.Contains(r.Out, "local-deployer") {
		t.Fatalf("the seeded bindings are missing: %s", r)
	}

	file := writeFile(t, "binding.yaml", "id: e2e-viewers\ngroup: e2e-readers\nrole: viewer\ndescription: End to end\n")
	if r := e.mustCLI("iam", "role-bindings", "apply", "-f", file); !strings.Contains(r.Out, "Created role binding e2e-viewers") {
		t.Fatalf("create: %s", r)
	}
	r := e.mustCLI("iam", "role-bindings", "get", "e2e-viewers", "-o", "json")
	var first struct {
		Revision int64    `json:"revision"`
		Role     string   `json:"role"`
		Scopes   []string `json:"scopes"`
	}
	if err := json.Unmarshal([]byte(r.Out), &first); err != nil || first.Revision < 1 || first.Role != "viewer" {
		t.Fatalf("get: %v\n%s", err, r)
	}
	if r := e.mustCLI("iam", "role-bindings", "list", "--group", "e2e-readers"); !strings.Contains(r.Out, "e2e-viewers") || strings.Contains(r.Out, "local-admin") {
		t.Fatalf("group filter: %s", r)
	}

	// Save what was read, change the role, apply it: the revision matches.
	saved := e.mustCLI("iam", "role-bindings", "get", "e2e-viewers", "-o", "yaml").Out
	edited := writeFile(t, "edited.yaml", strings.Replace(saved, "role: viewer", "role: deployer", 1))
	if r := e.mustCLI("iam", "role-bindings", "apply", "-f", edited); !strings.Contains(r.Out, "Updated role binding e2e-viewers") {
		t.Fatalf("update: %s", r)
	}
	var second struct {
		Revision int64    `json:"revision"`
		Role     string   `json:"role"`
		Scopes   []string `json:"scopes"`
	}
	if err := json.Unmarshal([]byte(e.mustCLI("iam", "role-bindings", "get", "e2e-viewers", "-o", "json").Out), &second); err != nil ||
		second.Role != "deployer" || second.Revision <= first.Revision {
		t.Fatalf("after update: %v %+v", err, second)
	}

	// The same file is now stale: refused, nothing changed, exit code 5.
	if r := e.cli("iam", "role-bindings", "apply", "-f", edited); r.Code != 5 {
		t.Fatalf("a stale revision must exit 5: %s", r)
	}
	if r := e.mustCLI("iam", "role-bindings", "get", "e2e-viewers", "-o", "json"); !strings.Contains(r.Out, `"role": "deployer"`) {
		t.Fatalf("a refused apply changed the binding: %s", r)
	}

	// The CLI-side check above never reaches the server. Send a stale
	// If-Match straight to the API: the server itself answers 412.
	stale := e.do("PUT", "/api/v1/iam/role-bindings/e2e-viewers", `{"group":"e2e-readers","role":"viewer"}`,
		append(e.auth(), "If-Match", "1", "Content-Type", "application/json")...)
	if stale.Status != 412 || !strings.Contains(stale.Body, "REVISION_CONFLICT") {
		t.Fatalf("server must refuse a stale If-Match with 412: %d %s", stale.Status, stale.Body)
	}
	if r := e.mustCLI("iam", "role-bindings", "get", "e2e-viewers", "-o", "json"); !strings.Contains(r.Out, `"role": "deployer"`) {
		t.Fatalf("a refused PUT changed the binding: %s", r)
	}

	// An update must state its scopes: omitted or null is refused (exit 2).
	for _, body := range []string{"id: e2e-viewers\ngroup: e2e-readers\nrole: viewer\n", "id: e2e-viewers\ngroup: e2e-readers\nrole: viewer\nscopes:\n"} {
		if r := e.cli("iam", "role-bindings", "apply", "-f", writeFile(t, "noscopes.yaml", body)); r.Code != 2 || !strings.Contains(r.Err, "scopes") {
			t.Fatalf("an update without scopes must exit 2: %s", r)
		}
	}
	// scopes: [] clears them.
	e.mustCLI("iam", "role-bindings", "apply", "-f", writeFile(t, "empty.yaml", "id: e2e-viewers\ngroup: e2e-readers\nrole: viewer\nscopes: []\n"))
	if r := e.mustCLI("iam", "role-bindings", "get", "e2e-viewers", "-o", "json"); !strings.Contains(r.Out, `"scopes": []`) {
		t.Fatalf("scopes: [] must clear: %s", r)
	}
	// get -o yaml applies back for a seeded (bootstrap) binding.
	seeded := e.mustCLI("iam", "role-bindings", "get", "local-deployer", "-o", "yaml").Out
	if r := e.mustCLI("iam", "role-bindings", "apply", "-f", writeFile(t, "seeded.yaml", seeded)); !strings.Contains(r.Out, "Updated role binding local-deployer") {
		t.Fatalf("round trip of a seeded binding: %s", r)
	}

	// Without an id the server derives one from the binding, so applying the
	// same file twice is refused as already existing.
	anon := writeFile(t, "anon.yaml", "group: e2e-anon\nrole: viewer\n")
	e.mustCLI("iam", "role-bindings", "apply", "-f", anon)
	if r := e.cli("iam", "role-bindings", "apply", "-f", anon); r.Code != 5 {
		t.Fatalf("creating the same binding twice must exit 5: %s", r)
	}

	// Delete asks first: with no terminal and no --yes it refuses and deletes nothing.
	if r := e.cli("iam", "role-bindings", "delete", "e2e-viewers"); r.Code == 0 {
		t.Fatalf("delete without --yes ran: %s", r)
	}
	if r := e.mustCLI("iam", "role-bindings", "delete", "e2e-viewers", "--yes"); !strings.Contains(r.Out, "Deleted role binding e2e-viewers") {
		t.Fatalf("delete: %s", r)
	}
	if r := e.cli("iam", "role-bindings", "get", "e2e-viewers"); r.Code != 4 {
		t.Fatalf("a deleted binding must be not found (exit 4): %s", r)
	}

	// A bad request is refused with the invalid-input exit code.
	bad := writeFile(t, "bad.yaml", "group: g\nrole: owner\n")
	if r := e.cli("iam", "role-bindings", "apply", "-f", bad); r.Code != 2 {
		t.Fatalf("unknown role: %s", r)
	}

	if r := e.cli("iam", "validate-principal", "not-a-principal"); r.Code != 1 || !strings.Contains(r.Err, "not valid") {
		t.Fatalf("invalid principal: %s", r)
	}
	if r := e.mustCLI("iam", "validate-principal", "principalSet://iam.googleapis.com/locations/global/workforcePools/acme/*"); !strings.Contains(r.Out, "Valid:") {
		t.Fatalf("valid principal: %s", r)
	}
}
