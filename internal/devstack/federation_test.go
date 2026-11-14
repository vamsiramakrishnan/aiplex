package devstack

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func newFederationStack(t *testing.T) *Stack {
	t.Helper()
	issuer, err := NewIssuer("http://127.0.0.1:1", DevAudience)
	if err != nil {
		t.Fatal(err)
	}
	s := &Stack{Issuer: issuer, IssuerURL: "http://127.0.0.1:1"}
	if err := s.startFederation(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, srv := range s.servers {
			srv.Close()
		}
	})
	return s
}

func claimsOf(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestFederationIssuersAreLoopbackAndDistinct(t *testing.T) {
	s := newFederationStack(t)
	f := s.Federation
	if !strings.HasPrefix(f.URL, "http://127.0.0.1:") {
		t.Fatalf("federation listener %q is not loopback", f.URL)
	}
	seen := map[string]bool{}
	for _, fi := range []*FederatedIssuer{f.IDP, f.Workload, f.Unconfigured} {
		if !strings.HasPrefix(fi.IssuerURL(), "https://") || !strings.HasPrefix(fi.JWKSURL, f.URL+"/") {
			t.Fatalf("%s: issuer %q jwks %q", fi.Name, fi.IssuerURL(), fi.JWKSURL)
		}
		if seen[fi.IssuerURL()] {
			t.Fatalf("duplicate issuer %s", fi.IssuerURL())
		}
		seen[fi.IssuerURL()] = true
		resp, err := http.Get(fi.JWKSURL)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != string(fi.Issuer.JWKS()) {
			t.Fatalf("%s jwks: %d %s", fi.Name, resp.StatusCode, body)
		}
	}
	// Only keys are served: no token endpoint on the federation listener.
	resp, err := http.Post(f.URL+"/idp"+DevTokenPath, "application/json", strings.NewReader(`{"user":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("token endpoint on the federation listener: %d", resp.StatusCode)
	}
}

func TestMintFederatedTokens(t *testing.T) {
	s := newFederationStack(t)
	idp, err := s.MintCallerToken(DevTokenRequest{User: "alice", Issuer: IssuerIDP, Groups: []string{"eng"}})
	if err != nil {
		t.Fatal(err)
	}
	c := claimsOf(t, idp.Token)
	if c["iss"] != s.Federation.IDP.IssuerURL() || c["aud"] != federationUserAudience || c["sub"] != "alice" || c["email"] != "alice@idp.localhost" {
		t.Fatalf("idp claims: %v", c)
	}
	if idp.Role != "none" || idp.PrincipalID == "" || idp.JWKSURL != s.Federation.IDP.JWKSURL {
		t.Fatalf("idp answer: %+v", idp)
	}
	wl, err := s.MintCallerToken(DevTokenRequest{User: "ci-1", Issuer: IssuerWorkload})
	if err != nil {
		t.Fatal(err)
	}
	c = claimsOf(t, wl.Token)
	if c["iss"] != s.Federation.Workload.IssuerURL() || c["aud"] != federationAgentAudience || c["email"] != nil || c["groups"] != nil {
		t.Fatalf("workload claims: %v", c)
	}
	// The unconfigured issuer is a different issuer with its own key.
	st, err := s.MintCallerToken(DevTokenRequest{User: "alice", Issuer: IssuerUnconfigured})
	if err != nil {
		t.Fatal(err)
	}
	if claimsOf(t, st.Token)["iss"] == c["iss"] || st.PrincipalID != "" {
		t.Fatalf("unconfigured answer: %+v", st)
	}
	// The default is the local issuer, with its role.
	local, err := s.MintCallerToken(DevTokenRequest{User: "bob", Role: "deployer"})
	if err != nil || local.Issuer != IssuerLocal || len(local.Groups) != 1 || local.Groups[0] != DevDeployerGroup {
		t.Fatalf("local: %+v %v", local, err)
	}
}

func TestMintFederatedRefusals(t *testing.T) {
	s := newFederationStack(t)
	for name, req := range map[string]DevTokenRequest{
		"role with idp":        {User: "a", Issuer: IssuerIDP, Role: "admin"},
		"deployer with wl":     {User: "a", Issuer: IssuerWorkload, Role: "deployer"},
		"reserved admin group": {User: "a", Issuer: IssuerIDP, Groups: []string{DevGroup}},
		"reserved deployer":    {User: "a", Issuer: IssuerIDP, Groups: []string{DevDeployerGroup}},
		"developer name":       {User: DevSubject, Issuer: IssuerIDP},
		"unknown issuer":       {User: "a", Issuer: "other"},
		"workload with groups": {User: "a", Issuer: IssuerWorkload, Groups: []string{"g"}},
		"bad name":             {User: "A b", Issuer: IssuerIDP},
	} {
		if out, err := s.MintCallerToken(req); err == nil {
			t.Errorf("%s: minted %+v", name, out)
		}
	}
}

func TestMergeEnvReplacesDefaults(t *testing.T) {
	got := mergeEnv([]string{"A=1", "AIPLEX_USER_FEDERATION=enabled", "B=2", "AIPLEX_AGENT_FEDERATION=enabled"}, []string{"AIPLEX_USER_FEDERATION=disabled", "C=3"})
	want := []string{"A=1", "B=2", "AIPLEX_AGENT_FEDERATION=enabled", "AIPLEX_USER_FEDERATION=disabled", "C=3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}
