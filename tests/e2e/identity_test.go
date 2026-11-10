//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// externalIssuer is an identity provider of another cloud: an HTTPS issuer
// name (it is only compared with the iss claim) whose JWKS is served on
// loopback, which the outbound endpoint policy permits in development mode.
type externalIssuer struct {
	*devstack.Issuer
	jwks string
}

func newExternalIssuer(t *testing.T, issuer, audience string) externalIssuer {
	t.Helper()
	i, err := devstack.NewIssuer(issuer, audience)
	if err != nil {
		t.Fatal(err)
	}
	srv, url, err := devstack.Listen(i.Handler())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return externalIssuer{Issuer: i, jwks: url + "/.well-known/jwks.json"}
}

// 6. Agent identity presets, bind, plan and verify; user identity providers
// apply, plan and verify; then live authentication with both, through an
// access point.
func TestIdentityFederationWithLocalJWKS(t *testing.T) {
	agents := newExternalIssuer(t, "https://agents.e2e.example", "aiplex-agents")
	users := newExternalIssuer(t, "https://login.e2e.example", "aiplex-users")
	e := start(t, func(o *devstack.Options) {
		o.Env = append(o.Env, "AIPLEX_AGENT_FEDERATION=enabled", "AIPLEX_USER_FEDERATION=enabled")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}

	t.Run("agent presets bind plan verify", func(t *testing.T) {
		e.mustCLI("agents", "register", "e2e-agent", "--name", "E2E agent", "--scopes", "llm:model:mock-gpt")
		r := e.mustCLI("agents", "identity", "presets")
		contains(t, r.Out, "oidc", "presets")
		r = e.mustCLI("agents", "identity", "bind", "e2e-agent", "ci", "--preset", "oidc",
			"--input", "issuer="+agents.IssuerURL(), "--input", "jwks_url="+agents.jwks,
			"--input", "subject=workload-1", "--input", "audience="+agents.Audience())
		r = e.mustCLI("agents", "identity", "list", "e2e-agent")
		contains(t, r.Out, "ci", "binding listed")
		r = e.mustCLI("agents", "identity", "plan", "e2e-agent", "ci")
		contains(t, r.Out, agents.IssuerURL(), "plan names the issuer")

		good, _ := agents.Mint(map[string]any{"sub": "workload-1"}, time.Hour)
		r = e.cli("agents", "identity", "verify", "--agent", "e2e-agent", "--binding", "ci", "--token-file", writeFile(t, "good.jwt", good))
		if r.Code != 0 {
			t.Fatalf("valid workload token rejected: %s", r)
		}
		notContains(t, r.Out+r.Err, good, "verify output leaks the token")
		bad, _ := agents.Mint(map[string]any{"sub": "workload-2"}, time.Hour)
		r = e.cli("agents", "identity", "verify", "--token-file", writeFile(t, "bad.jwt", bad))
		if r.Code == 0 {
			t.Fatalf("token with the wrong subject verified: %s", r)
		}
	})

	t.Run("user providers apply plan verify", func(t *testing.T) {
		r := e.mustCLI("identity", "user-providers", "presets")
		contains(t, r.Out, "oidc", "user presets")
		provider := map[string]any{"id": "corp", "preset": "oidc", "display_name": "Corp IdP",
			"inputs": map[string]string{"issuer": users.IssuerURL(), "jwks_url": users.jwks, "audience": users.Audience(), "groups_from": "claim", "group_claim": "groups"}}
		r = e.cli("identity", "user-providers", "apply", "-f", writeFile(t, "corp.json", provider))
		if r.Code != 0 {
			t.Fatalf("apply: %s", r)
		}
		r = e.mustCLI("identity", "user-providers", "plan", "corp")
		contains(t, r.Out, users.IssuerURL(), "user plan")
		tok, _ := users.Mint(map[string]any{"sub": "alice", "email": "alice@e2e.example", "groups": []string{"eng"}}, time.Hour)
		r = e.cli("identity", "user-providers", "verify", "--provider", "corp", "--token-file", writeFile(t, "alice.jwt", tok))
		if r.Code != 0 {
			t.Fatalf("valid user token rejected: %s", r)
		}
		expired, _ := users.Mint(map[string]any{"sub": "alice", "groups": []string{"eng"}, "exp": time.Now().Add(-time.Hour).Unix()}, time.Hour)
		r = e.cli("identity", "user-providers", "verify", "--token-file", writeFile(t, "expired.jwt", expired))
		if r.Code == 0 {
			t.Fatalf("expired token verified: %s", r)
		}
	})

	t.Run("live authentication", func(t *testing.T) {
		agentTok, _ := agents.Mint(map[string]any{"sub": "workload-1"}, time.Hour)
		r := e.do("GET", "/api/v1/iam/whoami", "", "Authorization", "Bearer "+agentTok)
		if r.Status != 200 {
			t.Fatalf("whoami with a bound workload token: %d %s", r.Status, r.Body)
		}
		userTok, _ := users.Mint(map[string]any{"sub": "alice", "email": "alice@e2e.example", "groups": []string{"eng"}}, time.Hour)
		r = e.do("GET", "/api/v1/iam/whoami", "", "Authorization", "Bearer "+userTok)
		if r.Status != 200 {
			t.Fatalf("whoami with a federated user token: %d %s", r.Status, r.Body)
		}
		var who map[string]any
		_ = json.Unmarshal([]byte(r.Body), &who)
		if !strings.Contains(r.Body, "alice") {
			t.Fatalf("whoami does not show the federated user: %s", r.Body)
		}
		// A federated user without model grants is admitted by the access
		// point's caller kind but refused the model: the 404 hides the model.
		r = e.do("POST", "/ap/local/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"x"}]}`, "Authorization", "Bearer "+userTok, "Content-Type", "application/json")
		if r.Status == 200 {
			t.Fatalf("federated user without grants reached the model: %s", r.Body)
		}
		forged, _ := devstack.NewIssuer(users.IssuerURL(), users.Audience())
		bad, _ := forged.Mint(map[string]any{"sub": "alice", "groups": []string{"eng"}}, time.Hour)
		r = e.do("GET", "/api/v1/iam/whoami", "", "Authorization", "Bearer "+bad)
		if r.Status == 200 && strings.Contains(r.Body, "alice") {
			t.Fatalf("token signed by an unknown key was accepted: %s", r.Body)
		}
	})
}
