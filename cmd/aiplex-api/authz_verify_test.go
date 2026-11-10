package main

import "testing"

// The token dry-run endpoints are verification oracles: operator-only, and
// rate limited per caller well below the general limit.
func TestTokenVerifyDryRunsAreRateLimited(t *testing.T) {
	for _, path := range []string{"/api/v1/agents/identities/verify", "/api/v1/identity/user-providers/verify"} {
		s := newAuthzServer(t)
		if rec := s.do(t, "POST", path, string(callerViewer), `{"token":"x"}`); rec.Code != 403 {
			t.Fatalf("%s: viewer got %d", path, rec.Code)
		}
		limited := 0
		for i := 0; i < 40; i++ {
			if rec := s.do(t, "POST", path, string(callerDeployer), `{"token":"x"}`); rec.Code == 429 {
				limited++
			}
		}
		if limited == 0 {
			t.Errorf("%s: 40 dry runs in a minute were not rate limited", path)
		}
	}
}
