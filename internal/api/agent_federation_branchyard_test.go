package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
)

// branchyardKeys is a stand-in Branchyard server: an Ed25519 key published at
// /.well-known/jwks.json over TLS (httptest's certificate, which the
// federation API harness already trusts).
func branchyardKeys(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "OKP", "crv": "Ed25519", "kid": "by-1", "use": "sig", "alg": "EdDSA", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
	}))
	t.Cleanup(server.Close)
	return server.URL, priv
}

func edToken(t *testing.T, key ed25519.PrivateKey, alg string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": alg, "kid": "by-1", "typ": "JWT"})
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(input)))
}

// A branchyard binding is saved through the API with the trust tuple the
// contract derives, and the dry run names the check each wrong token fails,
// with the binding looked up from the token and pinned. Each pass has its own
// API, because dry runs are limited to 20 a minute per caller.
func TestAgentIdentityBranchyardBindingAndDryRun(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		branchyardBindingAndDryRun(t, pinned)
	}
}

func branchyardBindingAndDryRun(t *testing.T, pinned bool) {
	f := newFederationAPI(t)
	server, key := branchyardKeys(t)
	path := "/api/v1/agents/tutor-agent/identities/by"

	for name, inputs := range map[string]map[string]string{
		"trailing slash":    {"server": server + "/", "tenant": "acme"},
		"path":              {"server": server + "/acme", "tenant": "acme"},
		"query":             {"server": server + "?x", "tenant": "acme"},
		"http public host":  {"server": "http://by.example.com", "tenant": "acme"},
		"user information":  {"server": strings.Replace(server, "https://", "https://evil@", 1), "tenant": "acme"},
		"tenant with colon": {"server": server, "tenant": "acme:admin"},
		"tenant with slash": {"server": server, "tenant": "acme/admin"},
		"jwks override":     {"server": server, "tenant": "acme", "jwks_url": "https://evil.example/jwks"},
	} {
		w := f.do(t, "PUT", path, "admin", "tenant-a", "", map[string]any{"preset": "branchyard", "inputs": inputs})
		if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_BINDING") {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}

	w := f.do(t, "PUT", path, "admin", "tenant-a", "", map[string]any{"preset": "branchyard", "inputs": map[string]string{"server": server, "tenant": "acme"}})
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var saved struct {
		Issuer         string            `json:"issuer"`
		JWKSURL        string            `json:"jwks_url"`
		Audience       string            `json:"audience"`
		SubjectClaim   string            `json:"subject_claim"`
		Subject        string            `json:"subject"`
		RequiredClaims map[string]string `json:"required_claims"`
	}
	if json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Issuer != server || saved.JWKSURL != server+"/.well-known/jwks.json" || saved.Audience != "https://aiplex.test" || saved.SubjectClaim != "sub" || saved.Subject != "branchyard:acme" || len(saved.RequiredClaims) != 1 || saved.RequiredClaims["by_purpose"] != "aiplex" {
		t.Fatalf("saved binding: %s", w.Body)
	}

	claims := func(change func(map[string]any)) map[string]any {
		now := time.Now()
		c := map[string]any{"iss": server, "aud": "https://aiplex.test", "sub": "branchyard:acme", "iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(), "jti": "j-1", "by_purpose": "aiplex", "by_tenant": "acme", "by_branch": "app/main", "by_turn": "t-1"}
		if change != nil {
			change(c)
		}
		return c
	}
	for _, tc := range []struct {
		name, alg string
		claims    map[string]any
		fail      string
	}{
		{"valid", "EdDSA", claims(nil), ""},
		{"wrong by_purpose", "EdDSA", claims(func(c map[string]any) { c["by_purpose"] = "connect" }), agentfederation.CheckRequiredClaims},
		{"missing by_purpose", "EdDSA", claims(func(c map[string]any) { delete(c, "by_purpose") }), agentfederation.CheckRequiredClaims},
		{"by_purpose a number", "EdDSA", claims(func(c map[string]any) { c["by_purpose"] = 7 }), agentfederation.CheckRequiredClaims},
		{"by_purpose an array", "EdDSA", claims(func(c map[string]any) { c["by_purpose"] = []string{"aiplex"} }), agentfederation.CheckRequiredClaims},
		{"wrong aud", "EdDSA", claims(func(c map[string]any) { c["aud"] = "https://elsewhere.test" }), agentfederation.CheckAudience},
		{"wrong sub", "EdDSA", claims(func(c map[string]any) { c["sub"] = "branchyard:globex" }), agentfederation.CheckSubject},
		{"expired", "EdDSA", claims(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }), agentfederation.CheckLifetime},
		{"iss with a trailing slash", "EdDSA", claims(func(c map[string]any) { c["iss"] = server + "/" }), agentfederation.CheckIssuer},
		{"lifetime exactly an hour", "EdDSA", claims(func(c map[string]any) { c["exp"] = c["iat"].(int64) + 3600 }), ""},
		{"lifetime an hour and a second", "EdDSA", claims(func(c map[string]any) { c["exp"] = c["iat"].(int64) + 3601 }), agentfederation.CheckLifetime},
		{"no iat", "EdDSA", claims(func(c map[string]any) { delete(c, "iat") }), agentfederation.CheckLifetime},
		{"iat a string", "EdDSA", claims(func(c map[string]any) { c["iat"] = "1700000000" }), agentfederation.CheckLifetime},
		{"eddsa spelled lower case", "eddsa", claims(nil), agentfederation.CheckTokenShape},
		{"Ed25519 as alg", "Ed25519", claims(nil), agentfederation.CheckTokenShape},
	} {
		body := map[string]any{"token": edToken(t, key, tc.alg, tc.claims)}
		if pinned {
			body["client_id"], body["binding_id"] = "tutor-agent", "by"
		}
		w := f.do(t, "POST", "/api/v1/agents/identities/verify", "admin", "tenant-a", "", body)
		var resp struct {
			Valid  bool                    `json:"valid"`
			Checks []agentfederation.Check `json:"checks"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &resp) != nil {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
		}
		if tc.fail == "" {
			if !resp.Valid {
				t.Fatalf("%s (pinned %v): rejected %s", tc.name, pinned, w.Body)
			}
			continue
		}
		var first *agentfederation.Check
		for i := range resp.Checks {
			if resp.Checks[i].Status == agentfederation.StatusFail {
				first = &resp.Checks[i]
				break
			}
		}
		if resp.Valid || first == nil || first.Name != tc.fail {
			t.Fatalf("%s (pinned %v): first failure %+v, want %s: %s", tc.name, pinned, first, tc.fail, w.Body)
		}
		if strings.Contains(w.Body.String(), body["token"].(string)) {
			t.Fatalf("%s: response echoes the token", tc.name)
		}
	}
}
