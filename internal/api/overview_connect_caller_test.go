package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// Any verified caller whom an access point admits may read its connect
// response; the response omits operator-only detail. A caller it does not
// admit gets exactly what a missing access point gets.
func TestOverviewConnectForAdmittedCallers(t *testing.T) {
	f := newOverviewFixture(t)
	// "team" admits one principal, one group and one registered client.
	f.put(AccessPointKind, "team", AccessPoint{ID: "team", DisplayName: "Team", CallerKinds: []string{CallerUser, CallerAgent},
		Subjects: AccessPointSubjects{Principals: []string{apUser("carol").PrincipalID}, Groups: []quotaplex.GroupRef{{Source: "s", ID: "g"}}},
		Models:   []AccessPointModel{{Name: "chat-model", SurfaceRef: "chat", NativeModel: "chat-model"}, {Name: "ghost", SurfaceRef: "no-such-surface", NativeModel: "ghost"}},
		Tools:    []AccessPointTool{{ConnectionRef: "search"}}})
	f.put(AccessPointKind, "regs", AccessPoint{ID: "regs", CallerKinds: []string{CallerClient}, Subjects: AccessPointSubjects{ClientRegistrations: []string{"ide"}},
		Models: []AccessPointModel{{Name: "chat-model", SurfaceRef: "chat", NativeModel: "chat-model"}}})

	f.roles = []models.IAMRole{}

	// Admitted by principal, no role: 200, reduced response.
	f.caller = apUser("carol")
	w := f.get("/api/v1/overview/access-points/team/connect")
	if w.Code != http.StatusOK {
		t.Fatalf("admitted caller = %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	out := decodeJSON(t, w)
	if out["access_point_id"] != "team" || out["base_urls"].(map[string]any)["openai"] != "https://gateway.example/ap/team/v1" {
		t.Fatalf("response = %v", out)
	}
	if len(out["snippets"].([]any)) == 0 {
		t.Fatal("no snippets")
	}
	auth := out["auth"].(map[string]any)
	if kinds := auth["caller_kinds"].([]any); len(kinds) != 1 || kinds[0] != CallerUser {
		t.Fatalf("caller_kinds = %v, want only the caller's own", kinds)
	}
	for _, step := range auth["steps"].([]any) {
		if strings.HasPrefix(step.(string), "Agents:") {
			t.Fatalf("step for another kind: %v", step)
		}
	}
	for _, leak := range []string{"no-such-surface", "does not resolve"} {
		if strings.Contains(body, leak) {
			t.Errorf("restricted response contains %q: %s", leak, body)
		}
	}

	// The operator response keeps the detail the restricted one drops.
	f.roles = []models.IAMRole{models.RoleDeployer}
	full := f.get("/api/v1/overview/access-points/team/connect").Body.String()
	for _, want := range []string{"no-such-surface", `"agent"`} {
		if !strings.Contains(full, want) {
			t.Errorf("operator response lost %q", want)
		}
	}
	f.roles = []models.IAMRole{}

	// Client registrations are other subjects: the restricted response must not
	// list them; the admitted registered client still gets the response.
	client := apUser("dave")
	client.AuthorizedParty = "ide"
	f.caller = client
	w = f.get("/api/v1/overview/access-points/regs/connect")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "must equal one of") {
		t.Fatalf("registered client: %d %s", w.Code, w.Body.String())
	}

	// Not admitted: identical to a missing access point (status and body).
	f.caller = apUser("mallory")
	notAdmitted := f.get("/api/v1/overview/access-points/team/connect")
	missing := f.get("/api/v1/overview/access-points/nonexistent/connect")
	if notAdmitted.Code != http.StatusNotFound || notAdmitted.Code != missing.Code || notAdmitted.Body.String() != missing.Body.String() {
		t.Fatalf("not admitted = %d %q, missing = %d %q", notAdmitted.Code, notAdmitted.Body.String(), missing.Code, missing.Body.String())
	}
	// The client query does not change that: a profile check cannot tell the
	// two apart either.
	for _, q := range []string{"?client=claude-code", "?client=claude-desktop", "?client=nosuch"} {
		a := f.get("/api/v1/overview/access-points/team/connect" + q)
		b := f.get("/api/v1/overview/access-points/nonexistent/connect" + q)
		if a.Code != b.Code || a.Body.String() != b.Body.String() {
			t.Fatalf("%s: not admitted %d %q vs missing %d %q", q, a.Code, a.Body.String(), b.Code, b.Body.String())
		}
	}

	// Another tenant never sees it.
	f.caller = apUser("carol")
	f.apFixture.tenant = "other"
	if w = f.get("/api/v1/overview/access-points/team/connect"); w.Code != http.StatusNotFound {
		t.Fatalf("other tenant = %d", w.Code)
	}
}
