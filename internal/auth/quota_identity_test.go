package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

func quotaIdentityFixture(t *testing.T, claims map[string]any) *models.WIFIdentity {
	t.Helper()
	v := NewWIFValidator(registry.NewMemoryStore(), WIFConfig{WorkforcePoolID: "people", IsWorkforce: true, TrustedIssuers: []string{"issuer"}, Verifier: verifyFunc(func(context.Context, string) (map[string]any, error) { return claims, nil })})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer verified-by-fixture")
	identity, err := v.ExtractIdentity(r)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func quotaIdentityClaims(now time.Time) map[string]any {
	return map[string]any{"iss": "issuer", "sub": "immutable-human", "iat": json.Number(fmt.Sprint(now.Add(-time.Minute).Unix())), "exp": json.Number(fmt.Sprint(now.Add(time.Hour).Unix())), "groups": []any{"research", "engineering", "research"}}
}

func TestPrincipalMembershipUsesVerifiedSnapshotAndSeparateSource(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	claims := quotaIdentityClaims(now)
	claims["act"] = map[string]any{"sub": "agent", "groups": []any{"actor-only"}}
	identity := quotaIdentityFixture(t, claims)
	snapshot, err := PrincipalMembership(identity, "tenant", now)
	if err != nil {
		t.Fatal(err)
	}
	source := MembershipSource("issuer", "people")
	if snapshot.PrincipalID != identity.PrincipalID || snapshot.Ref.ID == "" || snapshot.Ref.Version == "" || !reflect.DeepEqual(snapshot.Groups, []string{source + ":engineering", source + ":research"}) || !snapshot.IssuedAt.Equal(now.Add(-time.Minute)) || !snapshot.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("incorrect verified view: %+v", snapshot)
	}
	context := contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", PrincipalID: identity.PrincipalID, ActorID: identity.ActorID, ResourceID: "resource", RequestID: "correlation", MembershipSnapshotRef: &snapshot.Ref}
	if err := snapshot.ValidateFor(context, now); err != nil {
		t.Fatal(err)
	}
	if snapshot.PrincipalID == identity.ActorID || strings.Contains(strings.Join(snapshot.Groups, ","), "actor-only") {
		t.Fatal("principal evidence inherited actor groups")
	}
	if source == MembershipSource("other-issuer", "people") || source == MembershipSource("issuer", "other-pool") {
		t.Fatal("source namespace collided")
	}
	snapshot.Groups[0] = "mutated"
	claims["groups"].([]any)[0] = "outside-mutation"
	again, err := PrincipalMembership(identity, "tenant", now)
	if err != nil || again.Ref != snapshot.Ref || again.Groups[0] == "mutated" {
		t.Fatalf("snapshot aliasing: %+v %v", again, err)
	}
	otherTenant, err := PrincipalMembership(identity, "other-tenant", now)
	if err != nil || otherTenant.Ref == again.Ref {
		t.Fatal("snapshot version did not bind tenant", err)
	}
	identity.Groups = append(identity.Groups, "injected")
	if _, err = PrincipalMembership(identity, "tenant", now); err == nil {
		t.Fatal("mutated identity groups accepted")
	}
}

func TestQuotaMembershipRejectsMissingIncompleteOrExpiredEvidenceOnly(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		mutate func(map[string]any)
		valid  bool
	}{
		{"authoritative empty", func(c map[string]any) { c["groups"] = []any{} }, true},
		{"absent groups", func(c map[string]any) { delete(c, "groups") }, false},
		{"null groups", func(c map[string]any) { c["groups"] = nil }, false},
		{"typed nil groups", func(c map[string]any) { c["groups"] = []any(nil) }, false},
		{"mixed groups", func(c map[string]any) { c["groups"] = []any{"engineering", 42} }, false},
		{"blank group", func(c map[string]any) { c["groups"] = []any{" "} }, false},
		{"external group reference", func(c map[string]any) { c["_claim_names"] = map[string]any{"groups": "directory"} }, false},
		{"group overage", func(c map[string]any) { c["hasgroups"] = true }, false},
		{"false overage", func(c map[string]any) { c["hasgroups"] = false }, true},
		{"missing issuance", func(c map[string]any) { delete(c, "iat") }, false},
		{"fractional issuance", func(c map[string]any) { c["iat"] = json.Number("123.5") }, false},
		{"string issuance", func(c map[string]any) { c["iat"] = "123" }, false},
		{"missing expiry", func(c map[string]any) { delete(c, "exp") }, false},
		{"invalid expiry", func(c map[string]any) { c["exp"] = int64(1 << 62) }, false},
		{"expired", func(c map[string]any) { c["exp"] = now.Unix() }, false},
		{"future issuance", func(c map[string]any) { c["iat"] = now.Add(time.Second).Unix() }, false},
		{"conflicting group views", func(c map[string]any) {
			c["google"] = map[string]any{"sign_in_attributes": map[string]any{"groups": []any{}}}
		}, false},
		{"matching group views", func(c map[string]any) {
			c["google"] = map[string]any{"sign_in_attributes": map[string]any{"groups": []any{"engineering", "research"}}}
		}, true},
		{"google empty", func(c map[string]any) {
			delete(c, "groups")
			c["google"] = map[string]any{"sign_in_attributes": map[string]any{"groups": []any{}}}
		}, true},
		{"google null", func(c map[string]any) {
			delete(c, "groups")
			c["google"] = map[string]any{"sign_in_attributes": map[string]any{"groups": nil}}
		}, false},
		{"null alongside direct", func(c map[string]any) {
			c["google"] = map[string]any{"sign_in_attributes": map[string]any{"groups": nil}}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := quotaIdentityClaims(now)
			tc.mutate(claims)
			// Authentication compatibility is deliberate: the trusted adapter has
			// accepted the credential; only quota freshness/group proof is stricter.
			identity := quotaIdentityFixture(t, claims)
			_, err := PrincipalMembership(identity, "tenant", now)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v evidence=%+v", tc.valid, err, identity.VerifiedEvidence)
			}
		})
	}
}

func TestPrincipalMembershipEvidenceCannotComeFromUnverifiedOrJSONClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":"test","n":"%s","e":"AQAB"}]}`, base64.RawURLEncoding.EncodeToString(key.N.Bytes()))
	}))
	defer server.Close()
	verifier, err := NewJWKSVerifier(JWKSConfig{Issuer: "issuer", Audience: "quota", JWKSURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	validator := NewWIFValidator(registry.NewMemoryStore(), WIFConfig{Verifier: verifier, WorkforcePoolID: "people", IsWorkforce: true, TrustedIssuers: []string{"issuer"}})
	now := time.Now().UTC().Truncate(time.Second)
	claims := quotaIdentityClaims(now)
	claims["aud"] = "quota"
	token := signJWT(t, key, claims)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	identity, err := validator.ExtractIdentity(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PrincipalMembership(identity, "tenant", now); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	claims["groups"] = []string{"forged-budget"}
	raw, _ := json.Marshal(claims)
	parts[1] = base64.RawURLEncoding.EncodeToString(raw)
	request.Header.Set("Authorization", "Bearer "+strings.Join(parts, "."))
	if forged, err := validator.ExtractIdentity(request); err == nil || forged != nil {
		t.Fatal("unverified groups produced identity evidence")
	}
	var decoded models.WIFIdentity
	if err = json.Unmarshal([]byte(`{"principal_id":"principal-forged","VerifiedEvidence":{"GroupsPresent":true,"GroupsValid":true},"verified_evidence":{"GroupsPresent":true,"GroupsValid":true}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.VerifiedEvidence != nil {
		t.Fatal("wire identity populated server-only evidence")
	}
	if _, err = PrincipalMembership(&decoded, "tenant", now); err == nil {
		t.Fatal("wire identity became verified membership")
	}
}

type quotaBoundVerifier struct{ claims map[string]any }

func (v quotaBoundVerifier) Verify(context.Context, string) (map[string]any, error) {
	return nil, fmt.Errorf("request-bound proof required")
}
func (v quotaBoundVerifier) VerifyRequest(*http.Request, string) (map[string]any, error) {
	return v.claims, nil
}

func TestRequestBoundMembershipCannotOutliveVerifiedClientCertificate(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	claims := quotaIdentityClaims(now)
	claims["groups"] = []any{}
	verifier := NewWIFValidator(registry.NewMemoryStore(), WIFConfig{WorkloadPoolID: "machines", TrustedIssuers: []string{"issuer"}, Verifier: quotaBoundVerifier{claims: claims}})
	leaf := &x509.Certificate{Raw: []byte("verified-client"), NotAfter: now.Add(30 * time.Second)}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer verified-by-request-adapter")
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	identity, err := verifier.ExtractIdentity(request)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := PrincipalMembership(identity, "tenant", now)
	if err != nil || !snapshot.ExpiresAt.Equal(leaf.NotAfter) {
		t.Fatalf("certificate-bound lifetime lost: %+v %v", snapshot, err)
	}
	if !identity.VerifiedEvidence.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatal("raw signed expiry was rewritten")
	}
	if _, err = PrincipalMembership(identity, "tenant", leaf.NotAfter); err == nil {
		t.Fatal("queued dispatch outlived request-bound credential")
	}
	request.TLS.VerifiedChains = nil
	identity, err = verifier.ExtractIdentity(request)
	if err != nil {
		t.Fatal(err)
	}
	if !identity.VerifiedEvidence.CredentialExpiresAt.IsZero() {
		t.Fatal("unverified certificate became lifetime evidence")
	}
}
