//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// The local issuer mints a token for another caller only to the developer's own
// token, and the token carries the role the request asked for.
func TestDevTokenEndpointMintsCallersByRole(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	mint := func(bearer string, body string) (int, devstack.DevTokenResponse) {
		req, _ := http.NewRequest("POST", e.stack.IssuerURL+devstack.DevTokenPath, bytes.NewReader([]byte(body)))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out devstack.DevTokenResponse
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	roles := func(token string) []string {
		r := e.do("GET", "/api/v1/iam/whoami", "", "Authorization", "Bearer "+token)
		if r.Status != 200 {
			t.Fatalf("whoami: %d %s", r.Status, r.Body)
		}
		var body struct {
			Roles []string `json:"roles"`
		}
		_ = json.Unmarshal([]byte(r.Body), &body)
		return body.Roles
	}

	for role, want := range map[string]string{"none": "", "deployer": "deployer", "admin": "admin"} {
		status, out := mint(e.stack.Token, `{"user":"u-`+role+`","role":"`+role+`"}`)
		if status != 200 || out.Token == "" || out.Email != "u-"+role+"@localhost" {
			t.Fatalf("%s: %d %+v", role, status, out)
		}
		got := roles(out.Token)
		if want == "" && len(got) != 0 || want != "" && (len(got) != 1 || got[0] != want) {
			t.Fatalf("role %s: whoami roles = %v", role, got)
		}
	}
	// A name and groups the token carries.
	status, out := mint(e.stack.Token, `{"user":"carol","groups":["eng","research"]}`)
	if status != 200 || len(out.Groups) != 2 {
		t.Fatalf("groups: %d %+v", status, out)
	}

	// No token, a wrong token and a minted caller's token cannot ask for one.
	if status, _ := mint("", `{"user":"x"}`); status != 401 {
		t.Fatalf("no token: %d", status)
	}
	if status, _ := mint("garbage", `{"user":"x"}`); status != 401 {
		t.Fatalf("garbage token: %d", status)
	}
	_, deployer := mint(e.stack.Token, `{"user":"dana","role":"deployer"}`)
	if status, _ := mint(deployer.Token, `{"user":"x"}`); status != 401 {
		t.Fatalf("a minted caller minted a token: %d", status)
	}
	// Names and roles are validated; the reserved ones are refused.
	for _, body := range []string{`{"user":"Bad Name"}`, `{"user":"developer"}`, `{"user":"x","role":"root"}`, `{"user":"x","groups":["` + devstack.DevGroup + `"]}`} {
		if status, _ := mint(e.stack.Token, body); status != 400 {
			t.Fatalf("%s: %d", body, status)
		}
	}
	// The endpoint is on the issuer, not the API.
	if r := e.do("POST", devstack.DevTokenPath, `{"user":"x"}`, e.auth()...); r.Status == 200 {
		t.Fatalf("the API serves the token endpoint: %d", r.Status)
	}
}

// --allow-users accepts <name>@localhost under aiplex dev: the local issuer
// names a user "name" with that email.
func TestAllowUsersResolvesLocalEmails(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	// Outside a running `aiplex dev` (no state file) an unknown email is
	// refused with the exact form to pass.
	r := e.cli("access-points", "create", "team", "--model", "mock-gpt=local-models", "--allow-users", "zed@corp.example")
	if r.Code == 0 {
		t.Fatalf("unknown email accepted: %s", r)
	}
	contains(t, r.Err, "principal", "error names the form to pass")
}
