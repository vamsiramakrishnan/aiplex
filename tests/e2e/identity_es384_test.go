//go:build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// es384Issuer is a minimal external issuer that signs with a P-384 key, as
// AWS GetWebIdentityToken does with --signing-algorithm ES384.
type es384Issuer struct {
	key      *ecdsa.PrivateKey
	issuer   string
	audience string
	jwks     string
}

func newES384Issuer(t *testing.T, issuer, audience string) *es384Issuer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	coord := func(b []byte) string {
		out := make([]byte, 48)
		copy(out[48-len(b):], b)
		return base64.RawURLEncoding.EncodeToString(out)
	}
	jwk := map[string]any{"keys": []map[string]string{{"kty": "EC", "crv": "P-384", "alg": "ES384", "use": "sig", "kid": "p384", "x": coord(key.X.Bytes()), "y": coord(key.Y.Bytes())}}}
	srv, url, err := devstack.Listen(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwk)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return &es384Issuer{key: key, issuer: issuer, audience: audience, jwks: url + "/jwks"}
}

// mint signs claims with the P-384 key. alg is the header value, so a test can
// claim ES256 on this key. The signature is always the 96-byte P-384 R||S.
func (i *es384Issuer) mint(t *testing.T, alg, sub string) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": alg, "kid": "p384", "typ": "JWT"})
	c, _ := json.Marshal(map[string]any{"iss": i.issuer, "aud": i.audience, "sub": sub, "iat": time.Now().Unix(), "exp": time.Now().Add(10 * time.Minute).Unix()})
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	var digest []byte
	if alg == "ES384" {
		d := sha512.Sum384([]byte(input))
		digest = d[:]
	} else {
		d := sha256.Sum256([]byte(input))
		digest = d[:]
	}
	r, s, err := ecdsa.Sign(rand.Reader, i.key, digest)
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 96)
	r.FillBytes(sig[:48])
	s.FillBytes(sig[48:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// An agent workload whose issuer signs ES384 with a P-384 key verifies in the
// dry run and authenticates live; the same key with ES256 claimed is refused.
func TestIdentityFederationES384(t *testing.T) {
	issuer := newES384Issuer(t, "https://es384.e2e.example", "aiplex-es384")
	e := start(t, func(o *devstack.Options) {
		o.Env = append(o.Env, "AIPLEX_AGENT_FEDERATION=enabled")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	e.mustCLI("agents", "register", "es384-agent", "--name", "ES384 agent", "--scopes", "llm:model:mock-gpt")
	e.mustCLI("agents", "identity", "bind", "es384-agent", "aws-like", "--preset", "oidc",
		"--input", "issuer="+issuer.issuer, "--input", "jwks_url="+issuer.jwks,
		"--input", "subject=arn:aws:iam::123456789012:role/aiplex-agent", "--input", "audience="+issuer.audience)

	good := issuer.mint(t, "ES384", "arn:aws:iam::123456789012:role/aiplex-agent")
	r := e.cli("agents", "identity", "verify", "--agent", "es384-agent", "--binding", "aws-like", "--token-file", writeFile(t, "es384.jwt", good))
	if r.Code != 0 {
		t.Fatalf("valid ES384 token rejected: %s", r)
	}
	contains(t, r.Out, "ES384", "verify reports the algorithm")
	notContains(t, r.Out+r.Err, good, "verify output leaks the token")

	confused := issuer.mint(t, "ES256", "arn:aws:iam::123456789012:role/aiplex-agent")
	r = e.cli("agents", "identity", "verify", "--agent", "es384-agent", "--binding", "aws-like", "--token-file", writeFile(t, "es256-on-p384.jwt", confused))
	if r.Code == 0 {
		t.Fatalf("ES256 claimed on a P-384 key verified: %s", r)
	}
	contains(t, r.Out+r.Err, "invalid JWT signature", "algorithm confusion refused at the signature check")

	resp := e.do("GET", "/api/v1/iam/whoami", "", "Authorization", "Bearer "+good)
	if resp.Status != 200 {
		t.Fatalf("whoami with an ES384 workload token: %d %s", resp.Status, resp.Body)
	}
	resp = e.do("GET", "/api/v1/iam/whoami", "", "Authorization", "Bearer "+confused)
	if resp.Status == 200 {
		t.Fatalf("whoami accepted ES256 claimed on a P-384 key: %s", resp.Body)
	}
}
