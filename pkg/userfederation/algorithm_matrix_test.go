package userfederation_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

func digest(hash crypto.Hash, input string) []byte {
	switch hash {
	case crypto.SHA256:
		d := sha256.Sum256([]byte(input))
		return d[:]
	case crypto.SHA384:
		d := sha512.Sum384([]byte(input))
		return d[:]
	}
	d := sha512.Sum512([]byte(input))
	return d[:]
}

func pad(n *big.Int, size int) string {
	b := make([]byte, size)
	n.FillBytes(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

type signFunc func(t *testing.T, input string) []byte

func ecRaw(key *ecdsa.PrivateKey, hash crypto.Hash, size int) signFunc {
	return func(t *testing.T, input string) []byte {
		r, s, err := ecdsa.Sign(rand.Reader, key, digest(hash, input))
		if err != nil {
			t.Fatal(err)
		}
		sig := make([]byte, 2*size)
		r.FillBytes(sig[:size])
		s.FillBytes(sig[size:])
		return sig
	}
}

// TestAlgorithmKeyMatrixThroughEngine runs the algorithm against key type
// table through the user verification engine with the production JWKS resolver:
// RS256, ES256 and ES384 verify only on their own key type, every cross
// combination fails the signature check, and unsupported algorithms or a
// missing kid fail the token shape check.
func TestAlgorithmKeyMatrixThroughEngine(t *testing.T) {
	p256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p521, _ := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil || p256 == nil || p384 == nil || p521 == nil {
		t.Fatal("key generation failed")
	}
	ec := func(kid, crv, alg string, k *ecdsa.PublicKey, size int) map[string]string {
		return map[string]string{"kty": "EC", "kid": kid, "use": "sig", "crv": crv, "alg": alg, "x": pad(k.X, size), "y": pad(k.Y, size)}
	}
	offCurve := ec("off-curve", "P-384", "ES384", &p384.PublicKey, 48)
	offCurve["y"] = pad(new(big.Int).Add(p384.Y, big.NewInt(1)), 48)
	jwks := []map[string]string{
		{"kty": "RSA", "kid": "rsa", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()), "e": "AQAB"},
		ec("p256", "P-256", "ES256", &p256.PublicKey, 32),
		ec("p384", "P-384", "ES384", &p384.PublicKey, 48),
		ec("p256-claims-es384", "P-256", "ES384", &p256.PublicKey, 32),
		offCurve,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": jwks})
	}))
	defer server.Close()
	roots := server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	resolver := auth.NewJWKSKeyResolver(endpointpolicy.Config{AllowLoopback: true, TLSConfig: &tls.Config{RootCAs: roots}})
	store := userfederation.NewStore(resources.NewMemoryStore())
	provider, err := userfederation.NewProvider("corp", userfederation.ProviderRequest{Preset: userfederation.PresetOIDC, Inputs: map[string]string{"issuer": "https://issuer.test", "jwks_url": server.URL + "/jwks", "audience": "https://aiplex.test", "groups_from": "claim", "group_claim": "groups"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), "tenant-a", provider, 0); err != nil {
		t.Fatal(err)
	}
	engine := &userfederation.Engine{Keys: auth.UserKeyResolver{Keys: resolver}}

	rs256 := func(t *testing.T, input string) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest(crypto.SHA256, input))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
	es384 := ecRaw(p384, crypto.SHA384, 48)
	cases := []struct {
		name, alg, kid string
		sign           signFunc
		failing        string // first failing check, "" when valid
		reason         string
	}{
		{"RS256 on RSA", "RS256", "rsa", rs256, "", ""},
		{"ES256 on P-256", "ES256", "p256", ecRaw(p256, crypto.SHA256, 32), "", ""},
		{"ES384 on P-384", "ES384", "p384", es384, "", ""},
		{"ES384 on P-256", "ES384", "p256", ecRaw(p256, crypto.SHA384, 32), userfederation.CheckSignature, "invalid JWT signature"},
		{"ES384 on RSA", "ES384", "rsa", func(t *testing.T, input string) []byte {
			sig, _ := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA384, digest(crypto.SHA384, input))
			return sig
		}, userfederation.CheckSignature, "invalid JWT signature"},
		{"ES256 on P-384", "ES256", "p384", ecRaw(p384, crypto.SHA256, 48), userfederation.CheckSignature, "invalid JWT signature"},
		{"ES256 on RSA", "ES256", "rsa", rs256, userfederation.CheckSignature, "invalid JWT signature"},
		{"RS256 on P-256", "RS256", "p256", ecRaw(p256, crypto.SHA256, 32), userfederation.CheckSignature, "invalid JWT signature"},
		{"RS256 on P-384", "RS256", "p384", es384, userfederation.CheckSignature, "invalid JWT signature"},
		{"ES384 DER-encoded", "ES384", "p384", func(t *testing.T, input string) []byte {
			sig, _ := ecdsa.SignASN1(rand.Reader, p384, digest(crypto.SHA384, input))
			return sig
		}, userfederation.CheckSignature, "invalid JWT signature"},
		{"ES384 truncated", "ES384", "p384", func(t *testing.T, input string) []byte { return es384(t, input)[:95] }, userfederation.CheckSignature, "invalid JWT signature"},
		{"JWK alg contradicts crv", "ES384", "p256-claims-es384", ecRaw(p256, crypto.SHA384, 32), userfederation.CheckSignature, "unknown JWT signing key"},
		{"off-curve point", "ES384", "off-curve", es384, userfederation.CheckSignature, "unknown JWT signing key"},
		{"unknown kid", "ES384", "nope", es384, userfederation.CheckSignature, "unknown JWT signing key"},
		{"none", "none", "p384", func(*testing.T, string) []byte { return []byte{0} }, userfederation.CheckTokenShape, "not supported"},
		{"HS256 keyed with RSA modulus", "HS256", "rsa", func(_ *testing.T, input string) []byte {
			m := hmac.New(sha256.New, rsaKey.N.Bytes())
			m.Write([]byte(input))
			return m.Sum(nil)
		}, userfederation.CheckTokenShape, "not supported"},
		{"PS384", "PS384", "rsa", func(t *testing.T, input string) []byte {
			sig, _ := rsa.SignPSS(rand.Reader, rsaKey, crypto.SHA384, digest(crypto.SHA384, input), nil)
			return sig
		}, userfederation.CheckTokenShape, "not supported"},
		{"ES512", "ES512", "p384", ecRaw(p521, crypto.SHA512, 66), userfederation.CheckTokenShape, "not supported"},
		{"missing kid", "ES384", "", es384, userfederation.CheckTokenShape, "no key ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := map[string]any{"alg": tc.alg, "typ": "JWT"}
			if tc.kid != "" {
				header["kid"] = tc.kid
			}
			h, _ := json.Marshal(header)
			c, _ := json.Marshal(map[string]any{"iss": "https://issuer.test", "sub": "workload-7", "aud": "https://aiplex.test", "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Add(-time.Minute).Unix(), "groups": []string{"g"}})
			input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
			token := input + "." + base64.RawURLEncoding.EncodeToString(tc.sign(t, input))
			r := engine.Evaluate(context.Background(), userfederation.Input{Token: token, Finder: store.Tenant("tenant-a")})
			if tc.failing == "" {
				if !r.Valid {
					t.Fatalf("valid token rejected: %+v", r.Checks)
				}
				return
			}
			if r.Valid {
				t.Fatal("token accepted")
			}
			for _, check := range r.Checks {
				if check.Status == userfederation.StatusFail {
					if check.Name != tc.failing || !strings.Contains(check.Reason, tc.reason) {
						t.Fatalf("first failure %s %q, want %s %q", check.Name, check.Reason, tc.failing, tc.reason)
					}
					return
				}
			}
			t.Fatalf("no failing check: %+v", r.Checks)
		})
	}
}

func TestPresetsListES384(t *testing.T) {
	for _, p := range userfederation.Catalog() {
		if strings.Join(p.Algorithms, ",") != "RS256,ES256,ES384" {
			t.Fatalf("%s lists %v", p.ID, p.Algorithms)
		}
	}
}
