package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// An answer that is not for the issuer requested is refused: a stack started
// by an older aiplex dev ignores the issuer field and answers with a local
// token (and no issuer field).
func TestMintDevTokenRefusesAnswerForAnotherIssuer(t *testing.T) {
	for name, tc := range map[string]struct {
		answer    string
		requested string
		wantErr   string
	}{
		"older stack, idp asked":     {`{"token":"t","subject":"a","email":"a@localhost"}`, "idp", "Restart it"},
		"local answer, idp asked":    {`{"token":"t","issuer":"local"}`, "idp", "not the idp token"},
		"idp answer, workload asked": {`{"token":"t","issuer":"idp"}`, "workload", "not the workload token"},
		"older stack, default":       {`{"token":"t","subject":"a"}`, "", ""},
		"idp answer, idp asked":      {`{"token":"t","issuer":"idp"}`, "idp", ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer dev" || r.URL.Path != devstack.DevTokenPath {
				http.Error(w, "no", 401)
				return
			}
			_, _ = w.Write([]byte(tc.answer))
		}))
		out, err := mintDevToken(context.Background(), srv.URL, "dev", devstack.DevTokenRequest{User: "a", Issuer: tc.requested})
		srv.Close()
		if tc.wantErr == "" {
			if err != nil || out.Token != "t" {
				t.Errorf("%s: %+v %v", name, out, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) || out.Token != "" {
			t.Errorf("%s: %+v %v", name, out, err)
		}
	}
}
