package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// doctor's authenticated probe is whoami, which any signed-in identity may
// call. The admin-only dashboard must not decide whether the token works.
func TestDoctorUsesWhoamiAsAuthenticatedProbe(t *testing.T) {
	cases := []struct {
		name      string
		role      string
		status    int
		hangup    bool // the connection is dropped without an answer
		wantErr   bool // doctor exits non-zero
		wantLines []string
		forbid    []string
	}{
		{
			name:      "viewer identity is information, not a token problem",
			role:      "viewer",
			status:    200,
			wantLines: []string{"[pass] Authenticated as dev@localhost (roles: viewer)", "[INFO] Admin role not held"},
			forbid:    []string{"check your token", "Token rejected", "[FAIL]"},
		},
		{
			name:      "admin identity",
			role:      "admin",
			status:    200,
			wantLines: []string{"[pass] Authenticated as dev@localhost (roles: admin)"},
			forbid:    []string{"[INFO] Admin role not held", "check your token", "[FAIL]"},
		},
		{
			name:      "token rejected is a warning",
			role:      "viewer",
			status:    401,
			wantLines: []string{"Token rejected or not signed in"},
			forbid:    []string{"[FAIL]"},
		},
		{
			name:      "403 on whoami is information about roles",
			role:      "viewer",
			status:    403,
			wantLines: []string{"[INFO] Signed in, but this identity lacks the role"},
			forbid:    []string{"[FAIL]", "Token rejected"},
		},
		{
			name:      "500 on whoami is a failure",
			role:      "viewer",
			status:    500,
			wantErr:   true,
			wantLines: []string{"[FAIL] Identity check failed (aiplex: TEST (500)"},
		},
		{
			name:      "unreachable whoami is a failure",
			role:      "viewer",
			hangup:    true,
			wantErr:   true,
			wantLines: []string{"[FAIL] Identity check failed"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The handler runs on the server's goroutines; count with atomics.
			var dashboardHits, whoamiHits atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/healthz":
					w.WriteHeader(http.StatusOK)
				case "/api/v1/iam/whoami":
					whoamiHits.Add(1)
					if tc.hangup {
						hj, ok := w.(http.Hijacker)
						if !ok {
							t.Error("test server cannot hijack")
							return
						}
						conn, _, err := hj.Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						conn.Close()
						return
					}
					if tc.status != http.StatusOK {
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(`{"code":"TEST","message":"no"}`))
						return
					}
					_, _ = w.Write([]byte(`{"identity":{"principal_id":"p","email":"dev@localhost","subject":"dev"},"roles":["` + tc.role + `"],"scopes":[],"group_scopes":[]}`))
				case "/api/v1/dashboard/stats":
					dashboardHits.Add(1)
					w.WriteHeader(http.StatusForbidden)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			healthEnv(t, server.URL, healthTestToken)

			cmd := doctorCmd()
			out, err := captureStdout(t, func() error { return cmd.RunE(cmd, nil) })
			if (err != nil) != tc.wantErr {
				t.Fatalf("exit error = %v, want failure %v\n%s", err, tc.wantErr, out)
			}
			if whoamiHits.Load() == 0 || dashboardHits.Load() != 0 {
				t.Fatalf("probe calls: whoami=%d dashboard=%d", whoamiHits.Load(), dashboardHits.Load())
			}
			for _, want := range tc.wantLines {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
			for _, bad := range tc.forbid {
				if strings.Contains(out, bad) {
					t.Errorf("unexpected %q in:\n%s", bad, out)
				}
			}
			if strings.Contains(out, healthTestToken) {
				t.Errorf("the token was printed:\n%s", out)
			}
		})
	}
}
