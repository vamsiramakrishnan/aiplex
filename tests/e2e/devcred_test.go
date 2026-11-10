//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// Development mode serves a request with no credential as the anonymous local
// developer, but refuses one whose credential does not verify, so a mistyped
// token is never read as "authentication is off".
func TestDevelopmentModeRefusesAnInvalidCredential(t *testing.T) {
	e := start(t)

	if r := e.do("GET", "/api/v1/dashboard/stats", ""); r.Status != 200 {
		t.Fatalf("no credential: %d %s", r.Status, r.Body)
	}
	if r := e.do("GET", "/api/v1/iam/whoami", "", e.auth()...); r.Status != 200 {
		t.Fatalf("valid token: %d %s", r.Status, r.Body)
	}
	for name, header := range map[string]string{
		"garbage":   "Bearer not-a-token",
		"truncated": "Bearer " + e.stack.Token[:len(e.stack.Token)-6],
		"empty":     "Bearer ",
		"wrong":     "Basic Zm9vOmJhcg==",
	} {
		r := e.do("GET", "/api/v1/dashboard/stats", "", "Authorization", header)
		if r.Status != 401 {
			t.Fatalf("%s: %d %s", name, r.Status, r.Body)
		}
		contains(t, r.Body, "INVALID_CREDENTIAL", name)
		contains(t, r.Body, "aiplex dev env", name+" tells the caller how to get a token")
		notContains(t, r.Body, e.stack.Token, name+" echoes a credential")
	}
	// The CLI reports it too, rather than showing data.
	r := e.cliEnv([]string{"AIPLEX_URL=" + e.stack.APIURL, "AIPLEX_TOKEN=mistyped"}, "usage", "records")
	if r.Code == 0 || !strings.Contains(r.Err+r.Out, "did not accept your token") {
		t.Fatalf("cli with a bad token: %s", r)
	}
	// A shared-secret callback keeps its own guard (it answers 401/503, never an identity error from here).
	if r := e.do("POST", "/auth/token-hook", "{}", "Authorization", "Bearer wrong-secret"); r.Status == 200 {
		t.Fatalf("token hook accepted a wrong secret: %d", r.Status)
	}
}
