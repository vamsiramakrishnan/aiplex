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

// A Branchyard turn token (EdDSA, Ed25519 key, contract section 7) from the
// stack's loopback Branchyard stand-in is refused until an agent is bound to
// it with the branchyard preset; then it calls a model through the access
// point `local` as that agent, and the ledger record names the agent as the
// caller. Every token that is wrong in one way is refused with 401, never
// served as the anonymous developer, and the dry run names the failed check.
func TestIdentityFederationBranchyard(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	by := e.stack.Federation.Branchyard
	if by == nil || !strings.HasPrefix(by.URL, "http://127.0.0.1:") {
		t.Fatalf("branchyard stand-in: %+v", by)
	}
	good := e.mustMint(`{"issuer":"branchyard","tenant":"acme"}`)
	if good.Issuer != "branchyard" || good.IssuerURL != by.URL || good.JWKSURL != by.URL+"/.well-known/jwks.json" || good.Subject != "branchyard:acme" || good.PrincipalID == "" {
		t.Fatalf("mint answer: %+v", good)
	}
	chat := func(token string) httpResp {
		return e.do("POST", "/ap/local/v1/chat/completions", chatBody, append(bearer(token), "Content-Type", "application/json")...)
	}

	t.Run("refused until a binding names the server", func(t *testing.T) {
		if r := e.whoami(good.Token); r.Status != 401 {
			t.Fatalf("whoami before a binding: %d %s", r.Status, r.Body)
		}
		if r := chat(good.Token); r.Status != 401 {
			t.Fatalf("access point before a binding: %d %s", r.Status, r.Body)
		}
	})

	e.mustCLI("agents", "register", "by-agent", "--name", "Branchyard acme", "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*")
	// The values a server operator would give: server, tenant, audience.
	bind := e.mustCLI("agents", "identity", "bind", "by-agent", "by", "--preset", "branchyard",
		"--input", "server="+by.URL, "--input", "tenant=acme", "--input", "audience="+by.Audience)
	contains(t, bind.Out+bind.Err, "branchyard:acme", "bind reports the pinned subject")

	t.Run("a bound tenant's token calls a model as the agent", func(t *testing.T) {
		r := e.whoami(good.Token)
		if r.Status != 200 || !strings.Contains(r.Body, "by-agent") || strings.Contains(r.Body, `"developer"`) || strings.Contains(r.Body, `"admin"`) {
			t.Fatalf("whoami: %d %s", r.Status, r.Body)
		}
		// The binding grants nothing by itself.
		if r := chat(good.Token); r.Status != 403 || !strings.Contains(r.Body, good.PrincipalID) {
			t.Fatalf("bound agent without a grant: %d %s", r.Status, r.Body)
		}
		e.mustCLI("agents", "user-scopes", "set", good.PrincipalID, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*")
		if r := chat(good.Token); r.Status != 200 {
			t.Fatalf("bound agent with a grant: %d %s", r.Status, r.Body)
		}
		var records []struct {
			PrincipalID   string `json:"principal_id"`
			ClientID      string `json:"client_id"`
			CallerKind    string `json:"caller_kind"`
			AccessPointID string `json:"access_point_id"`
		}
		raw := e.mustCLI("usage", "records", "-o", "json").Out
		if err := json.Unmarshal([]byte(raw), &records); err != nil {
			var wrapped struct {
				Records []struct {
					PrincipalID   string `json:"principal_id"`
					ClientID      string `json:"client_id"`
					CallerKind    string `json:"caller_kind"`
					AccessPointID string `json:"access_point_id"`
				} `json:"records"`
			}
			if err := json.Unmarshal([]byte(raw), &wrapped); err != nil {
				t.Fatalf("usage records: %v\n%s", err, raw)
			}
			records = append(records[:0], wrapped.Records...)
		}
		found := false
		for _, rec := range records {
			if rec.PrincipalID == good.PrincipalID {
				found = true
				if rec.ClientID != "by-agent" || rec.CallerKind != "agent" || rec.AccessPointID != "local" {
					t.Fatalf("ledger record for the Branchyard caller: %+v", rec)
				}
			}
		}
		if !found {
			t.Fatalf("no ledger record names principal %s:\n%s", good.PrincipalID, raw)
		}
	})

	t.Run("the dry run accepts the token and reports EdDSA", func(t *testing.T) {
		r := e.cli("agents", "identity", "verify", "--agent", "by-agent", "--binding", "by", "--token-file", writeFile(t, "by.jwt", good.Token))
		if r.Code != 0 {
			t.Fatalf("valid Branchyard token rejected: %s", r)
		}
		contains(t, r.Out, "EdDSA", "verify reports the algorithm")
		notContains(t, r.Out+r.Err, good.Token, "verify output leaks the token")
	})

	claims := func(change func(map[string]any)) map[string]any {
		now := time.Now()
		c := map[string]any{"iss": by.URL, "aud": by.Audience, "sub": "branchyard:acme", "iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(), "jti": "e2e", "by_purpose": "aiplex", "by_tenant": "acme", "by_branch": "app/main", "by_turn": "t-1"}
		change(c)
		return c
	}
	sign := func(c map[string]any) string {
		token, err := by.Sign(c)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	other := e.mustMint(`{"issuer":"branchyard","tenant":"globex"}`)
	for _, tc := range []struct {
		name, token, check string
	}{
		{"wrong by_purpose", sign(claims(func(c map[string]any) { c["by_purpose"] = "connect" })), "required_claims"},
		{"missing by_purpose", sign(claims(func(c map[string]any) { delete(c, "by_purpose") })), "required_claims"},
		{"by_purpose a number", sign(claims(func(c map[string]any) { c["by_purpose"] = 1 })), "required_claims"},
		{"by_purpose an array", sign(claims(func(c map[string]any) { c["by_purpose"] = []string{"aiplex"} })), "required_claims"},
		{"wrong aud", sign(claims(func(c map[string]any) { c["aud"] = "somebody-else" })), "audience"},
		{"wrong sub", sign(claims(func(c map[string]any) { c["sub"] = "branchyard:globex" })), "subject"},
		{"expired", sign(claims(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() })), "lifetime"},
		{"iss with a trailing slash", sign(claims(func(c map[string]any) { c["iss"] = by.URL + "/" })), "issuer"},
		{"lifetime over an hour", sign(claims(func(c map[string]any) { c["exp"] = c["iat"].(int64) + 3601 })), "lifetime"},
		{"no iat", sign(claims(func(c map[string]any) { delete(c, "iat") })), "lifetime"},
		{"another tenant, unbound", other.Token, "subject"},
	} {
		t.Run("refused: "+tc.name, func(t *testing.T) {
			if r := e.whoami(tc.token); r.Status != 401 {
				t.Fatalf("whoami: %d %s", r.Status, r.Body)
			}
			r := chat(tc.token)
			if r.Status != 401 || strings.Contains(r.Body, `"developer"`) {
				t.Fatalf("access point: %d %s", r.Status, r.Body)
			}
			notContains(t, r.Body, tc.token, "refusal echoes the token")
			v := e.cli("agents", "identity", "verify", "--agent", "by-agent", "--binding", "by", "--token-file", writeFile(t, "bad.jwt", tc.token))
			if v.Code == 0 {
				t.Fatalf("dry run accepted it: %s", v)
			}
			if !failedCheck(v.Out, tc.check) {
				t.Fatalf("dry run does not report %s as the failed check:\n%s", tc.check, v.Out)
			}
		})
	}

	t.Run("the developer is still the developer", func(t *testing.T) {
		if r := e.whoami(e.stack.Token); r.Status != 200 || !strings.Contains(r.Body, `"admin"`) {
			t.Fatalf("developer whoami: %d %s", r.Status, r.Body)
		}
	})
}

// failedCheck reports whether the dry run's output marks check as the first
// failure: its line is the first one that says fail.
func failedCheck(out, check string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(strings.ToLower(line), "fail") {
			return strings.Contains(line, check)
		}
	}
	return false
}
