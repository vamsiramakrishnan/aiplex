package devstack

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
)

// The Branchyard stand-in has its own loopback listener whose URL is its
// issuer, serves only its public OKP key at the path the branchyard preset
// derives, and is distinct from every other issuer of the stack.
func TestBranchyardStandInIsLoopbackAndMatchesThePreset(t *testing.T) {
	s := newFederationStack(t)
	by := s.Federation.Branchyard
	if by == nil || !strings.HasPrefix(by.URL, "http://127.0.0.1:") || strings.HasSuffix(by.URL, "/") || by.URL == s.Federation.URL {
		t.Fatalf("branchyard stand-in %+v, federation listener %s", by, s.Federation.URL)
	}
	for _, other := range []string{s.Federation.IDP.IssuerURL(), s.Federation.Workload.IssuerURL(), s.Federation.Unconfigured.IssuerURL(), s.IssuerURL} {
		if other == by.URL {
			t.Fatalf("branchyard issuer %s is shared", by.URL)
		}
	}
	trust, err := agentfederation.Derive(agentfederation.PresetBranchyard, map[string]string{"server": by.URL, "tenant": "acme", "audience": by.Audience}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if trust.Issuer != by.URL || trust.JWKSURL != by.JWKSURL {
		t.Fatalf("preset derives %s %s, stand-in serves %s %s", trust.Issuer, trust.JWKSURL, by.URL, by.JWKSURL)
	}
	resp, err := http.Get(by.JWKSURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != string(by.JWKS()) {
		t.Fatalf("jwks: %d %s", resp.StatusCode, body)
	}
	var set struct {
		Keys []map[string]string `json:"keys"`
	}
	if json.Unmarshal(body, &set) != nil || len(set.Keys) != 1 || set.Keys[0]["kty"] != "OKP" || set.Keys[0]["crv"] != "Ed25519" || set.Keys[0]["alg"] != "EdDSA" || set.Keys[0]["d"] != "" {
		t.Fatalf("key set: %s", body)
	}
	// Nothing else is served: no token endpoint, no other path, no writes.
	for _, probe := range []struct{ method, path string }{{"POST", DevTokenPath}, {"GET", "/"}, {"GET", "/keys"}, {"POST", "/.well-known/jwks.json"}} {
		req, _ := http.NewRequest(probe.method, by.URL+probe.path, strings.NewReader(`{"tenant":"acme"}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Fatalf("%s %s on the branchyard listener: %d", probe.method, probe.path, resp.StatusCode)
		}
	}
}

// A minted token has the shape of contract section 7 and verifies with the
// production JWKS verifier against the stand-in's key set, for EdDSA only.
func TestMintBranchyardToken(t *testing.T) {
	s := newFederationStack(t)
	by := s.Federation.Branchyard
	out, err := s.MintCallerToken(DevTokenRequest{Issuer: IssuerBranchyard, Tenant: "acme", Role: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Issuer != IssuerBranchyard || out.IssuerURL != by.URL || out.JWKSURL != by.JWKSURL || out.Audience != by.Audience || out.Subject != "branchyard:acme" || out.Tenant != "acme" || out.Role != "none" || out.Email != "" || len(out.Groups) != 0 {
		t.Fatalf("answer: %+v", out)
	}
	if want := auth.CanonicalPrincipalID(by.URL, auth.FederationPoolID(DevTenant), "branchyard:acme"); out.PrincipalID != want {
		t.Fatalf("principal %s, want %s", out.PrincipalID, want)
	}
	parts := strings.Split(out.Token, ".")
	rawHeader, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var header map[string]string
	if json.Unmarshal(rawHeader, &header) != nil || header["alg"] != "EdDSA" || header["typ"] != "JWT" || header["kid"] == "" {
		t.Fatalf("header: %s", rawHeader)
	}
	c := claimsOf(t, out.Token)
	iat, _ := c["iat"].(float64)
	exp, _ := c["exp"].(float64)
	if c["iss"] != by.URL || c["aud"] != by.Audience || c["sub"] != "branchyard:acme" || c["by_purpose"] != "aiplex" || c["by_tenant"] != "acme" || c["by_branch"] == "" || c["by_turn"] == "" || c["jti"] == "" || exp-iat <= 0 || exp-iat > 3600 {
		t.Fatalf("claims: %v", c)
	}
	if _, isString := c["aud"].(string); !isString {
		t.Fatalf("aud is not a string: %v", c["aud"])
	}
	second, _ := s.MintCallerToken(DevTokenRequest{Issuer: IssuerBranchyard, Tenant: "acme"})
	if claimsOf(t, second.Token)["jti"] == c["jti"] {
		t.Fatal("jti repeats")
	}

	verifier, err := auth.NewJWKSVerifier(auth.JWKSConfig{Issuer: by.URL, Audience: by.Audience, JWKSURL: by.JWKSURL, Algorithms: []string{"EdDSA"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), out.Token); err != nil {
		t.Fatalf("stand-in token does not verify: %v", err)
	}
	// A verifier that did not ask for EdDSA refuses it.
	plain, _ := auth.NewJWKSVerifier(auth.JWKSConfig{Issuer: by.URL, Audience: by.Audience, JWKSURL: by.JWKSURL})
	if _, err := plain.Verify(context.Background(), out.Token); err == nil {
		t.Fatal("a verifier without EdDSA accepted the stand-in's token")
	}
}

func TestMintBranchyardRefusals(t *testing.T) {
	s := newFederationStack(t)
	for name, req := range map[string]DevTokenRequest{
		"no tenant":            {Issuer: IssuerBranchyard},
		"user instead":         {Issuer: IssuerBranchyard, User: "alice"},
		"user and tenant":      {Issuer: IssuerBranchyard, User: "alice", Tenant: "acme"},
		"role":                 {Issuer: IssuerBranchyard, Tenant: "acme", Role: "admin"},
		"groups":               {Issuer: IssuerBranchyard, Tenant: "acme", Groups: []string{"eng"}},
		"tenant with slash":    {Issuer: IssuerBranchyard, Tenant: "acme/admin"},
		"tenant with colon":    {Issuer: IssuerBranchyard, Tenant: "acme:admin"},
		"tenant with space":    {Issuer: IssuerBranchyard, Tenant: "ac me"},
		"tenant too long":      {Issuer: IssuerBranchyard, Tenant: strings.Repeat("a", 129)},
		"tenant with workload": {Issuer: IssuerWorkload, User: "ci-1", Tenant: "acme"},
		"tenant with local":    {User: "bob", Tenant: "acme"},
	} {
		if out, err := s.MintCallerToken(req); err == nil {
			t.Errorf("%s: minted %+v", name, out)
		}
	}
	if _, err := s.Federation.Branchyard.MintTurn("acme", "b", "t", CallerTokenTTL+1); err == nil {
		t.Error("minted a token that lasts more than an hour")
	}
}
