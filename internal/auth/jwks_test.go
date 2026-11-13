package auth

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
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// algorithmKeys are freshly generated keys for the algorithm-confusion matrix.
type algorithmKeys struct {
	p256, p384, p521 *ecdsa.PrivateKey
	rsa, rsaSmall    *rsa.PrivateKey
}

func newAlgorithmKeys(t *testing.T) algorithmKeys {
	t.Helper()
	var k algorithmKeys
	var err error
	if k.p256, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		t.Fatal(err)
	}
	if k.p384, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader); err != nil {
		t.Fatal(err)
	}
	if k.p521, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader); err != nil {
		t.Fatal(err)
	}
	if k.rsa, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	if k.rsaSmall, err = rsa.GenerateKey(rand.Reader, 1024); err != nil {
		t.Fatal(err)
	}
	return k
}

func fixedB64(n *big.Int, size int) string {
	buf := make([]byte, size)
	n.FillBytes(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func ecJWK(kid, crv, alg string, key *ecdsa.PublicKey, size int) map[string]string {
	j := map[string]string{"kty": "EC", "kid": kid, "use": "sig", "crv": crv, "x": fixedB64(key.X, size), "y": fixedB64(key.Y, size)}
	if alg != "" {
		j["alg"] = alg
	}
	return j
}

func rsaJWK(kid, alg string, key *rsa.PublicKey) map[string]string {
	j := map[string]string{"kty": "RSA", "kid": kid, "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
	if alg != "" {
		j["alg"] = alg
	}
	return j
}

// matrixJWKS publishes one usable key per supported algorithm and a set of
// keys the verifier must refuse to load.
func matrixJWKS(k algorithmKeys) []map[string]string {
	offCurve := ecJWK("p384-off-curve", "P-384", "ES384", &k.p384.PublicKey, 48)
	y := new(big.Int).Add(k.p384.Y, big.NewInt(1))
	offCurve["y"] = fixedB64(y, 48)
	return []map[string]string{
		rsaJWK("rsa", "RS256", &k.rsa.PublicKey),
		ecJWK("p256", "P-256", "ES256", &k.p256.PublicKey, 32),
		ecJWK("p384", "P-384", "ES384", &k.p384.PublicKey, 48),
		ecJWK("p384-no-alg", "P-384", "", &k.p384.PublicKey, 48),
		// Keys whose declared algorithm contradicts their type or curve.
		ecJWK("p256-claims-es384", "P-256", "ES384", &k.p256.PublicKey, 32),
		ecJWK("p384-claims-es256", "P-384", "ES256", &k.p384.PublicKey, 48),
		ecJWK("p384-claims-rs256", "P-384", "RS256", &k.p384.PublicKey, 48),
		rsaJWK("rsa-claims-es384", "ES384", &k.rsa.PublicKey),
		// Unsupported curve, malformed coordinates, off-curve point, small RSA.
		ecJWK("p521", "P-521", "ES512", &k.p521.PublicKey, 66),
		ecJWK("p384-short-coordinates", "P-384", "ES384", &k.p256.PublicKey, 32),
		ecJWK("p256-long-coordinates", "P-256", "ES256", &k.p256.PublicKey, 48),
		offCurve,
		rsaJWK("rsa-small", "RS256", &k.rsaSmall.PublicKey),
	}
}

func signingInput(t *testing.T, header map[string]any, claims map[string]any) string {
	t.Helper()
	h, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	c, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
}

func sum(hash crypto.Hash, input string) []byte {
	switch hash {
	case crypto.SHA256:
		d := sha256.Sum256([]byte(input))
		return d[:]
	case crypto.SHA384:
		d := sha512.Sum384([]byte(input))
		return d[:]
	case crypto.SHA512:
		d := sha512.Sum512([]byte(input))
		return d[:]
	}
	panic("unsupported hash")
}

// signer produces the signature segment bytes for a signing input.
type signer func(t *testing.T, input string) []byte

func ecdsaRaw(key *ecdsa.PrivateKey, hash crypto.Hash, size int) signer {
	return func(t *testing.T, input string) []byte {
		r, s, err := ecdsa.Sign(rand.Reader, key, sum(hash, input))
		if err != nil {
			t.Fatal(err)
		}
		sig := make([]byte, 2*size)
		r.FillBytes(sig[:size])
		s.FillBytes(sig[size:])
		return sig
	}
}

func ecdsaDER(key *ecdsa.PrivateKey, hash crypto.Hash) signer {
	return func(t *testing.T, input string) []byte {
		sig, err := ecdsa.SignASN1(rand.Reader, key, sum(hash, input))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
}

func rsaPKCS1(key *rsa.PrivateKey, hash crypto.Hash) signer {
	return func(t *testing.T, input string) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, hash, sum(hash, input))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
}

func rsaPSS(key *rsa.PrivateKey, hash crypto.Hash) signer {
	return func(t *testing.T, input string) []byte {
		sig, err := rsa.SignPSS(rand.Reader, key, hash, sum(hash, input), nil)
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
}

func hmacWith(secret []byte, hash crypto.Hash) signer {
	return func(_ *testing.T, input string) []byte {
		mac := hmac.New(hash.New, secret)
		mac.Write([]byte(input))
		return mac.Sum(nil)
	}
}

// mutateSig alters a signature after signing.
func mutateSig(s signer, f func([]byte) []byte) signer {
	return func(t *testing.T, input string) []byte { return f(s(t, input)) }
}

func constSig(b []byte) signer { return func(*testing.T, string) []byte { return b } }

const (
	matrixIssuer   = "https://issuer.test"
	matrixAudience = "https://aiplex.test"
)

func matrixClaims() map[string]any {
	return map[string]any{"iss": matrixIssuer, "aud": matrixAudience, "sub": "workload", "exp": time.Now().Add(5 * time.Minute).Unix()}
}

type algorithmCase struct {
	name   string
	header map[string]any
	sign   signer
	ok     bool
	reason string // substring of the expected error
}

// algorithmMatrix is the algorithm against key type table: every supported
// algorithm on its own key type verifies; every cross combination, unsupported
// algorithm and malformed signature is refused.
func algorithmMatrix(k algorithmKeys) []algorithmCase {
	h := func(alg, kid string) map[string]any { return map[string]any{"alg": alg, "kid": kid, "typ": "JWT"} }
	p256, p384, rsaKey := k.p256, k.p384, k.rsa
	es256 := ecdsaRaw(p256, crypto.SHA256, 32)
	es384 := ecdsaRaw(p384, crypto.SHA384, 48)
	rs256 := rsaPKCS1(rsaKey, crypto.SHA256)
	coordinates := append(append([]byte{}, p384.X.Bytes()...), p384.Y.Bytes()...)
	nOf384 := p384.Curve.Params().N
	return []algorithmCase{
		// Supported algorithm on the matching key.
		{"RS256 on RSA", h("RS256", "rsa"), rs256, true, ""},
		{"ES256 on P-256", h("ES256", "p256"), es256, true, ""},
		{"ES384 on P-384", h("ES384", "p384"), es384, true, ""},
		{"ES384 on P-384 JWK without alg", h("ES384", "p384-no-alg"), es384, true, ""},

		// Every cross combination of algorithm and key type.
		{"ES384 on P-256 key signed SHA-384", h("ES384", "p256"), ecdsaRaw(p256, crypto.SHA384, 32), false, "invalid JWT signature"},
		{"ES384 on P-256 key padded to 96 bytes", h("ES384", "p256"), ecdsaRaw(p256, crypto.SHA384, 48), false, "invalid JWT signature"},
		{"ES384 on P-256 key signed SHA-256", h("ES384", "p256"), es256, false, "invalid JWT signature"},
		{"ES384 on RSA key", h("ES384", "rsa"), rsaPKCS1(rsaKey, crypto.SHA384), false, "invalid JWT signature"},
		{"ES384 on RSA key with RS256 signature", h("ES384", "rsa"), rs256, false, "invalid JWT signature"},
		{"ES256 on P-384 key signed SHA-256", h("ES256", "p384"), ecdsaRaw(p384, crypto.SHA256, 48), false, "invalid JWT signature"},
		{"ES256 on P-384 key with ES384 signature", h("ES256", "p384"), es384, false, "invalid JWT signature"},
		{"ES256 on RSA key", h("ES256", "rsa"), rs256, false, "invalid JWT signature"},
		{"RS256 on P-256 key", h("RS256", "p256"), es256, false, "invalid JWT signature"},
		{"RS256 on P-384 key", h("RS256", "p384"), es384, false, "invalid JWT signature"},

		// Algorithms that are never accepted.
		{"none", h("none", "p384"), constSig(nil), false, "requires RS256, ES256 or ES384"},
		{"none on RSA", h("none", "rsa"), constSig(nil), false, "requires RS256, ES256 or ES384"},
		{"empty alg", h("", "p384"), es384, false, "requires RS256, ES256 or ES384"},
		{"lowercase es384", h("es384", "p384"), es384, false, "requires RS256, ES256 or ES384"},
		{"HS256 keyed with RSA modulus", h("HS256", "rsa"), hmacWith(rsaKey.N.Bytes(), crypto.SHA256), false, "requires RS256, ES256 or ES384"},
		{"HS384 keyed with P-384 point", h("HS384", "p384"), hmacWith(coordinates, crypto.SHA384), false, "requires RS256, ES256 or ES384"},
		{"HS512", h("HS512", "p384"), hmacWith(coordinates, crypto.SHA512), false, "requires RS256, ES256 or ES384"},
		{"PS256", h("PS256", "rsa"), rsaPSS(rsaKey, crypto.SHA256), false, "requires RS256, ES256 or ES384"},
		{"PS384", h("PS384", "rsa"), rsaPSS(rsaKey, crypto.SHA384), false, "requires RS256, ES256 or ES384"},
		{"RS384", h("RS384", "rsa"), rsaPKCS1(rsaKey, crypto.SHA384), false, "requires RS256, ES256 or ES384"},
		{"ES512 on P-521", h("ES512", "p521"), ecdsaRaw(k.p521, crypto.SHA512, 66), false, "requires RS256, ES256 or ES384"},
		{"ES512 on P-384", h("ES512", "p384"), ecdsaRaw(p384, crypto.SHA512, 48), false, "requires RS256, ES256 or ES384"},

		// Malformed ECDSA signatures.
		{"ES384 DER-encoded", h("ES384", "p384"), ecdsaDER(p384, crypto.SHA384), false, "invalid JWT signature"},
		{"ES256 DER-encoded", h("ES256", "p256"), ecdsaDER(p256, crypto.SHA256), false, "invalid JWT signature"},
		{"ES384 truncated to 95 bytes", h("ES384", "p384"), mutateSig(es384, func(b []byte) []byte { return b[:95] }), false, "invalid JWT signature"},
		{"ES384 truncated to 64 bytes", h("ES384", "p384"), mutateSig(es384, func(b []byte) []byte { return b[:64] }), false, "invalid JWT signature"},
		{"ES384 with a trailing byte", h("ES384", "p384"), mutateSig(es384, func(b []byte) []byte { return append(b, 0) }), false, "invalid JWT signature"},
		{"ES384 with a leading zero byte", h("ES384", "p384"), mutateSig(es384, func(b []byte) []byte { return append([]byte{0}, b...) }), false, "invalid JWT signature"},
		{"ES384 S||R", h("ES384", "p384"), mutateSig(es384, func(b []byte) []byte { return append(append([]byte{}, b[48:]...), b[:48]...) }), false, "invalid JWT signature"},
		{"ES384 bit flip", h("ES384", "p384"), mutateSig(es384, func(b []byte) []byte { b[10] ^= 1; return b }), false, "invalid JWT signature"},
		{"ES384 all zero", h("ES384", "p384"), constSig(make([]byte, 96)), false, "invalid JWT signature"},
		{"ES384 r equal to curve order", h("ES384", "p384"), mutateSig(es384, func(b []byte) []byte { nOf384.FillBytes(b[:48]); return b }), false, "invalid JWT signature"},
		{"ES384 empty signature", h("ES384", "p384"), constSig(nil), false, "invalid JWT signature"},

		// Keys the verifier must not load.
		{"ES384 on P-256 JWK claiming ES384", h("ES384", "p256-claims-es384"), ecdsaRaw(p256, crypto.SHA384, 32), false, "unknown JWT signing key"},
		{"ES256 on P-256 JWK claiming ES384", h("ES256", "p256-claims-es384"), es256, false, "unknown JWT signing key"},
		{"ES384 on P-384 JWK claiming ES256", h("ES384", "p384-claims-es256"), es384, false, "unknown JWT signing key"},
		{"ES256 on P-384 JWK claiming ES256", h("ES256", "p384-claims-es256"), ecdsaRaw(p384, crypto.SHA256, 48), false, "unknown JWT signing key"},
		{"ES384 on P-384 JWK claiming RS256", h("ES384", "p384-claims-rs256"), es384, false, "unknown JWT signing key"},
		{"RS256 on RSA JWK claiming ES384", h("RS256", "rsa-claims-es384"), rs256, false, "unknown JWT signing key"},
		{"ES384 on RSA JWK claiming ES384", h("ES384", "rsa-claims-es384"), rsaPKCS1(rsaKey, crypto.SHA384), false, "unknown JWT signing key"},
		{"ES384 on P-384 JWK with short coordinates", h("ES384", "p384-short-coordinates"), ecdsaRaw(p256, crypto.SHA384, 48), false, "unknown JWT signing key"},
		{"ES256 on P-256 JWK with long coordinates", h("ES256", "p256-long-coordinates"), es256, false, "unknown JWT signing key"},
		{"ES384 on off-curve point", h("ES384", "p384-off-curve"), es384, false, "unknown JWT signing key"},
		{"RS256 on small RSA key", h("RS256", "rsa-small"), rsaPKCS1(k.rsaSmall, crypto.SHA256), false, "unknown JWT signing key"},
		{"unknown kid", h("ES384", "nope"), es384, false, "unknown JWT signing key"},
		{"missing kid", map[string]any{"alg": "ES384", "typ": "JWT"}, es384, false, "key ID"},
		{"empty kid", h("ES384", ""), es384, false, "key ID"},
	}
}

func TestJWKSAlgorithmKeyMatrix(t *testing.T) {
	keys := newAlgorithmKeys(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": matrixJWKS(keys)})
	}))
	defer server.Close()
	verifier, err := NewJWKSVerifier(JWKSConfig{Issuer: matrixIssuer, Audience: matrixAudience, JWKSURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range algorithmMatrix(keys) {
		t.Run(tc.name, func(t *testing.T) {
			input := signingInput(t, tc.header, matrixClaims())
			token := input + "." + base64.RawURLEncoding.EncodeToString(tc.sign(t, input))
			claims, err := verifier.Verify(context.Background(), token)
			if tc.ok {
				if err != nil || claims["sub"] != "workload" {
					t.Fatalf("valid token rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("token accepted")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("error %q does not mention %q", err, tc.reason)
			}
		})
	}
}

func TestJWKSES384TamperedPayloadRejected(t *testing.T) {
	keys := newAlgorithmKeys(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": matrixJWKS(keys)})
	}))
	defer server.Close()
	verifier, err := NewJWKSVerifier(JWKSConfig{Issuer: matrixIssuer, Audience: matrixAudience, JWKSURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	input := signingInput(t, map[string]any{"alg": "ES384", "kid": "p384"}, matrixClaims())
	sig := ecdsaRaw(keys.p384, crypto.SHA384, 48)(t, input)
	parts := strings.Split(input, ".")
	forged := matrixClaims()
	forged["sub"] = "admin"
	body, _ := json.Marshal(forged)
	parts[1] = base64.RawURLEncoding.EncodeToString(body)
	if _, err := verifier.Verify(context.Background(), parts[0]+"."+parts[1]+"."+base64.RawURLEncoding.EncodeToString(sig)); err == nil {
		t.Fatal("tampered ES384 payload accepted")
	}
}

func TestJWKSRejectsDuplicateKeyIDAcrossCurves(t *testing.T) {
	keys := newAlgorithmKeys(t)
	for name, set := range map[string][]map[string]string{
		"P-384 twice":   {ecJWK("dup", "P-384", "ES384", &keys.p384.PublicKey, 48), ecJWK("dup", "P-384", "", &keys.p384.PublicKey, 48)},
		"P-256 / P-384": {ecJWK("dup", "P-256", "ES256", &keys.p256.PublicKey, 32), ecJWK("dup", "P-384", "ES384", &keys.p384.PublicKey, 48)},
		"RSA / P-384":   {rsaJWK("dup", "RS256", &keys.rsa.PublicKey), ecJWK("dup", "P-384", "ES384", &keys.p384.PublicKey, 48)},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"keys": set})
			}))
			defer server.Close()
			verifier, err := NewJWKSVerifier(JWKSConfig{Issuer: matrixIssuer, Audience: matrixAudience, JWKSURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			input := signingInput(t, map[string]any{"alg": "ES384", "kid": "dup"}, matrixClaims())
			token := input + "." + base64.RawURLEncoding.EncodeToString(ecdsaRaw(keys.p384, crypto.SHA384, 48)(t, input))
			if _, err := verifier.Verify(context.Background(), token); err == nil || !strings.Contains(err.Error(), "duplicate JWKS key ID") {
				t.Fatalf("duplicate kid not refused: %v", err)
			}
		})
	}
}

// A key set that publishes only unusable EC keys still has no signing keys.
func TestJWKSWithOnlyContradictoryECKeysHasNoSigningKeys(t *testing.T) {
	keys := newAlgorithmKeys(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			ecJWK("a", "P-256", "ES384", &keys.p256.PublicKey, 32),
			ecJWK("b", "P-384", "ES256", &keys.p384.PublicKey, 48),
			ecJWK("c", "P-521", "ES512", &keys.p521.PublicKey, 66),
		}})
	}))
	defer server.Close()
	verifier, err := NewJWKSVerifier(JWKSConfig{Issuer: matrixIssuer, Audience: matrixAudience, JWKSURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	input := signingInput(t, map[string]any{"alg": "ES384", "kid": "b"}, matrixClaims())
	token := input + "." + base64.RawURLEncoding.EncodeToString(ecdsaRaw(keys.p384, crypto.SHA384, 48)(t, input))
	if _, err := verifier.Verify(context.Background(), token); err == nil || !strings.Contains(err.Error(), "no supported signing keys") {
		t.Fatalf("unexpected result: %v", err)
	}
}
