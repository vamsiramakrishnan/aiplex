//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// The runs routes against the real binaries in development mode: the local
// developer sees every run as before; a caller bound to one tenant sees only
// that tenant's runs whatever it sends; an identity naming two tenants is
// refused.
func TestRunsTenantIsolationDevStack(t *testing.T) {
	const ingest = "e2e-ingest-token"
	e := start(t, func(o *devstack.Options) { o.Env = append(o.Env, "TAPE_INGEST_TOKEN="+ingest) })

	// Two tenants' runs arrive through Tape's ingestion path.
	now := time.Now().UTC().Format(time.RFC3339)
	events := `{"events":[` +
		`{"run_id":"run-dev","seq":1,"tenant_id":"development","agent_id":"a","kind":"run.started","timestamp":"` + now + `"},` +
		`{"run_id":"run-other","seq":1,"tenant_id":"other-tenant","agent_id":"a","kind":"run.started","timestamp":"` + now + `"}]}`
	if r := e.post("/internal/tape/events", events, "Authorization", "Bearer "+ingest); r.Status != 200 || !strings.Contains(r.Body, `"ingested":2`) {
		t.Fatalf("ingest: %d %s", r.Status, r.Body)
	}

	// The local developer (development mode, no tenant) sees both, as before.
	r := e.do("GET", "/api/v1/runs", "", e.auth()...)
	if r.Status != 200 || !strings.Contains(r.Body, "run-dev") || !strings.Contains(r.Body, "run-other") {
		t.Fatalf("developer list: %d %s", r.Status, r.Body)
	}
	if r = e.do("GET", "/api/v1/runs/run-other", "", e.auth()...); r.Status != 200 {
		t.Fatalf("developer get: %d %s", r.Status, r.Body)
	}

	bind := func(group string, scopes ...string) {
		body, _ := json.Marshal(map[string]any{"id": group, "group": group, "role": "deployer", "scopes": scopes})
		if r := e.post("/api/v1/iam/role-bindings", string(body), e.auth()...); r.Status/100 != 2 {
			t.Fatalf("bind %s: %d %s", group, r.Status, r.Body)
		}
	}
	mint := func(user, group string) string {
		body, _ := json.Marshal(devstack.DevTokenRequest{User: user, Role: "deployer", Groups: []string{group}})
		req, _ := http.NewRequest("POST", e.stack.IssuerURL+devstack.DevTokenPath, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+e.stack.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out devstack.DevTokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Token == "" {
			t.Fatalf("mint %s: %d %v", user, resp.StatusCode, err)
		}
		return out.Token
	}
	bind("e2e-runs-dev", "aiplex:tenant:development", "aiplex:runs:read", "aiplex:runs:cancel")
	bind("e2e-runs-two", "aiplex:tenant:development", "aiplex:tenant:other-tenant", "aiplex:runs:read")
	one := mint("runs-one", "e2e-runs-dev")
	two := mint("runs-two", "e2e-runs-two")

	hostile := []string{"Authorization", "Bearer " + one, "X-Aiplex-Tenant", "other-tenant", "X-Tenant-Id", "other-tenant"}
	r = e.do("GET", "/api/v1/runs?tenant_id=other-tenant", "", hostile...)
	if r.Status != 200 || strings.Contains(r.Body, "run-other") {
		t.Fatalf("tenant-bound list naming another tenant: %d %s", r.Status, r.Body)
	}
	r = e.do("GET", "/api/v1/runs", "", hostile...)
	if r.Status != 200 || strings.Contains(r.Body, "run-other") || !strings.Contains(r.Body, "run-dev") {
		t.Fatalf("tenant-bound list: %d %s", r.Status, r.Body)
	}
	for _, path := range []string{"/api/v1/runs/run-other", "/api/v1/runs/run-other/events", "/api/v1/runs/run-other/operator-audit", "/events/stream?run_id=run-other"} {
		if r = e.do("GET", path, "", hostile...); r.Status != 404 {
			t.Errorf("tenant-bound GET %s: %d %s", path, r.Status, r.Body)
		}
	}
	if r = e.do("POST", "/api/v1/runs/run-other/cancel", `{"reason":"x","tenant_id":"other-tenant"}`, hostile...); r.Status != 404 {
		t.Errorf("tenant-bound cancel of another tenant's run: %d %s", r.Status, r.Body)
	}
	if r = e.do("GET", "/api/v1/runs/run-dev", "", hostile...); r.Status != 200 {
		t.Errorf("tenant-bound GET own run: %d %s", r.Status, r.Body)
	}
	// Deployment-wide pipeline values go to the developer, not to a caller
	// confined to one tenant.
	if r = e.do("GET", "/api/v1/runs/_health", "", hostile...); r.Status != 200 || strings.Contains(r.Body, "last_ingest_at") || strings.Contains(r.Body, "tape_instances_count") || !strings.Contains(r.Body, `"has_runs":true`) {
		t.Errorf("tenant-bound health: %d %s", r.Status, r.Body)
	}
	if r = e.do("GET", "/api/v1/runs/_health", "", e.auth()...); r.Status != 200 || !strings.Contains(r.Body, "last_ingest_at") || !strings.Contains(r.Body, "tape_instances_count") {
		t.Errorf("developer health: %d %s", r.Status, r.Body)
	}
	// The CLI shows the confined caller's withheld values as unknown.
	res := e.cliEnv([]string{"AIPLEX_URL=" + e.stack.APIURL, "AIPLEX_TOKEN=" + one}, "runs", "health")
	if res.Code != 0 || !strings.Contains(res.Out, "Last ingest:     unknown") || !strings.Contains(res.Out, "Tape instances:  unknown") {
		t.Errorf("aiplex runs health as a tenant-bound caller:\n%s", res)
	}
	t.Logf("aiplex runs health as a tenant-bound caller:\n%s", res.Out)

	// Two tenants: refused on every runs route, never resolved to one.
	for _, path := range []string{"/api/v1/runs", "/api/v1/runs/_health", "/api/v1/runs/run-dev", "/api/v1/runs/run-other"} {
		r = e.do("GET", path, "", "Authorization", "Bearer "+two)
		code, _ := r.JSON(t)["code"].(string)
		if r.Status != 403 || (code != "TENANT_AMBIGUOUS" && code != "DEPLOYMENT_SCOPE_REQUIRED") || strings.Contains(r.Body, "run-") {
			t.Errorf("two-tenant GET %s: %d %s", path, r.Status, r.Body)
		}
		t.Logf("two-tenant GET %s: %d %s", path, r.Status, code)
	}
}
