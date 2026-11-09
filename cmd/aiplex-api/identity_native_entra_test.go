package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/governance/entra"
)

func TestNativeEntraSourceVerifiesSignatureAndSeparatesAgentActor(t *testing.T) {
	const tenant = "11111111-1111-1111-1111-111111111111"
	const agent = "22222222-2222-2222-2222-222222222222"
	const agentApp = "33333333-3333-3333-3333-333333333333"
	const human = "44444444-4444-4444-4444-444444444444"
	issuer := "https://login.microsoftonline.com/" + tenant + "/v2.0"
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256", "n": encode(key.N.Bytes()), "e": "AQAB"}}})
	}))
	defer jwks.Close()
	document := identitySourceFile{Sources: []identitySourceSpec{{Issuer: issuer, Audience: "aiplex-agents", JWKSURL: jwks.URL, PoolID: tenant, NativeEntraAgentID: &entra.IdentityConfig{TenantID: tenant, AllowedAgents: []entra.AgentBinding{{ObjectID: agent, AppID: agentApp}}, AllowDelegated: true, RequiredScopes: []string{"agent.invoke"}}}}}
	raw, _ := json.Marshal(document)
	path := filepath.Join(t.TempDir(), "identities.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	validator, err := identityValidator(registry.NewMemoryStore(), &config.Config{IdentityProvidersFile: path})
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{"iss": issuer, "aud": "aiplex-agents", "sub": "pairwise-original-sub", "oid": human, "tid": tenant, "azp": agentApp, "ver": "2.0", "idtyp": "user", "xms_act_fct": "11", "xms_sub_fct": "3", "scp": "agent.invoke", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()}
	sign := func(claims map[string]any) string {
		header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "test"})
		payload, _ := json.Marshal(claims)
		input := encode(header) + "." + encode(payload)
		digest := sha256.Sum256([]byte(input))
		signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if e != nil {
			t.Fatal(e)
		}
		return input + "." + encode(signature)
	}
	request := httptest.NewRequest("POST", "https://aiplex.example/api/v1/a2a/connections/test/invoke", nil)
	request.Header.Set("Authorization", "Bearer "+sign(claims))
	identity, err := validator.ExtractIdentity(request)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != human || identity.ActorSubject != agent || identity.PrincipalID != auth.CanonicalPrincipalID(issuer, tenant, human) || identity.ActorID != auth.CanonicalPrincipalID(issuer, tenant, agent) || identity.AuthorizedParty != agentApp {
		t.Fatalf("native human/agent attribution lost: %+v", identity)
	}
	claims["aud"] = "https://graph.microsoft.com"
	request.Header.Set("Authorization", "Bearer "+sign(claims))
	if _, err = validator.ExtractIdentity(request); err == nil {
		t.Fatal("Graph audience accepted as an incoming Aiplex credential")
	}
	claims["aud"] = "aiplex-agents"
	signed := sign(claims)
	request.Header.Set("Authorization", "Bearer "+signed[:len(signed)-8]+"AAAAAAAA")
	if _, err = validator.ExtractIdentity(request); err == nil {
		t.Fatal("native claim projection accepted an invalid signature")
	}
}
