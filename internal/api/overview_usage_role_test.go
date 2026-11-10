package api

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// Tenant-wide usage is admin-only (/api/v1/usage shows everyone else only
// their own records). Deployers keep the overview's configuration view but
// receive no usage roll-ups, last-seen times or ledger-derived subjects.
func TestOverviewUsageRollupsAreAdminOnly(t *testing.T) {
	f := newOverviewFixture(t)
	alice := apUser("alice").PrincipalID
	paths := []string{"/api/v1/overview/subjects?kind=user", "/api/v1/overview/access-points/ide", "/api/v1/overview/subjects/user/" + alice, "/api/v1/overview/access-points"}

	f.roles = []models.IAMRole{models.RoleAdmin}
	admin := map[string]string{}
	for _, p := range paths {
		w := f.get(p)
		if w.Code != 200 {
			t.Fatalf("admin %s: %d %s", p, w.Code, w.Body)
		}
		admin[p] = w.Body.String()
	}
	if !strings.Contains(admin[paths[0]], `"usage_available":true`) || !strings.Contains(admin[paths[1]], `"requests":`) {
		t.Fatalf("fixture lost admin usage: %s %s", admin[paths[0]], admin[paths[1]])
	}

	f.roles = []models.IAMRole{models.RoleDeployer}
	for _, p := range paths {
		w := f.get(p)
		body := w.Body.String()
		if w.Code != 200 && w.Code != 404 {
			t.Fatalf("deployer %s: %d %s", p, w.Code, body)
		}
		for _, leak := range []string{`"requests":1`, `"requests":2`, `"requests":3`, `"last_seen":"`, `"usage_available":true`, `"available":true`, "input_tokens"} {
			if strings.Contains(body, leak) {
				t.Errorf("deployer %s leaks usage %q: %s", p, leak, body)
			}
		}
	}
}
