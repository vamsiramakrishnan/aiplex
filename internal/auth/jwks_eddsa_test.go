package auth

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// allAlgorithms is every algorithm the verifier knows, for verifiers that are
// meant to see every key type at once.
var allAlgorithms = []string{"RS256", "ES256", "ES384", "EdDSA"}

func newEd25519(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func okpJWK(kid, crv, alg string, x []byte) map[string]string {
	j := map[string]string{"kty": "OKP", "kid": kid, "use": "sig", "crv": crv, "x": base64.RawURLEncoding.EncodeToString(x)}
	if alg != "" {
		j["alg"] = alg
	}
	return j
}

func ed25519Sign(key ed25519.PrivateKey) signer {
	return func(_ *testing.T, input string) []byte { return ed25519.Sign(key, []byte(input)) }
}

func jwksServer(t *testing.T, keys []map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	t.Cleanup(server.Close)
	return server
}

func mustVerifier(t *testing.T, url string, algorithms []string) *JWKSVerifier {
	t.Helper()
	v, err := NewJWKSVerifier(JWKSConfig{Issuer: matrixIssuer, Audience: matrixAudience, JWKSURL: url, Algorithms: algorithms})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestJWKSEdDSAKeyMatrix is the algorithm against key type table for EdDSA:
// EdDSA verifies only with an OKP Ed25519 key, every cross combination in
// both directions is refused, as is every other spelling of the algorithm,
// every malformed OKP key and every signature that is not exactly 64 bytes.
// The verifier accepts all four algorithms, so each refusal is the binding of
// algorithm to key type, not the verifier's allow-list.
func TestJWKSEdDSAKeyMatrix(t *testing.T) {
	keys := newAlgorithmKeys(t)
	pub, priv := newEd25519(t)
	otherPub, otherPriv := newEd25519(t)
	eddsa := ed25519Sign(priv)
	set := append(matrixJWKS(keys),
		okpJWK("ed", "Ed25519", "EdDSA", pub),
		okpJWK("ed-no-alg", "Ed25519", "", otherPub),
		// Keys the verifier must refuse to load.
		okpJWK("ed-claims-rs256", "Ed25519", "RS256", pub),
		okpJWK("ed-claims-es256", "Ed25519", "ES256", pub),
		okpJWK("ed-claims-ed25519", "Ed25519", "Ed25519", pub),
		okpJWK("ed-claims-lower", "Ed25519", "eddsa", pub),
		okpJWK("ed448", "Ed448", "EdDSA", append(append([]byte{}, pub...), make([]byte, 25)...)),
		okpJWK("ed448-short", "Ed448", "EdDSA", pub),
		okpJWK("x25519", "X25519", "", pub),
		okpJWK("no-crv", "", "EdDSA", pub),
		okpJWK("lower-crv", "ed25519", "EdDSA", pub),
		okpJWK("short-x", "Ed25519", "EdDSA", pub[:31]),
		okpJWK("long-x", "Ed25519", "EdDSA", append(append([]byte{}, pub...), 0)),
		okpJWK("empty-x", "Ed25519", "EdDSA", nil),
	)
	private := okpJWK("ed-private", "Ed25519", "EdDSA", pub)
	private["d"] = base64.RawURLEncoding.EncodeToString(priv.Seed())
	padded := okpJWK("ed-padded-x", "Ed25519", "EdDSA", pub)
	padded["x"] = base64.URLEncoding.EncodeToString(pub)
	stdAlphabet := okpJWK("ed-std-alphabet", "Ed25519", "EdDSA", pub)
	stdAlphabet["x"] = base64.StdEncoding.EncodeToString(pub) + "+/"
	withY := okpJWK("ed-with-y", "Ed25519", "EdDSA", pub)
	withY["y"] = base64.RawURLEncoding.EncodeToString(pub)
	encryption := okpJWK("ed-enc", "Ed25519", "EdDSA", pub)
	encryption["use"] = "enc"
	// An OKP key dressed as EC and an EC key dressed as OKP.
	okpAsEC := map[string]string{"kty": "EC", "kid": "okp-as-ec", "use": "sig", "crv": "Ed25519", "alg": "EdDSA", "x": base64.RawURLEncoding.EncodeToString(pub)}
	ecAsOKP := okpJWK("ec-as-okp", "P-256", "ES256", []byte(fixedB64(keys.p256.X, 32)))
	ecAsOKP["x"] = fixedB64(keys.p256.X, 32)
	set = append(set, private, padded, stdAlphabet, withY, encryption, okpAsEC, ecAsOKP)

	server := jwksServer(t, set)
	verifier := mustVerifier(t, server.URL, allAlgorithms)

	h := func(alg, kid string) map[string]any { return map[string]any{"alg": alg, "kid": kid, "typ": "JWT"} }
	es256 := ecdsaRaw(keys.p256, crypto.SHA256, 32)
	es384 := ecdsaRaw(keys.p384, crypto.SHA384, 48)
	rs256 := rsaPKCS1(keys.rsa, crypto.SHA256)
	cases := []algorithmCase{
		// EdDSA on its own key type, with and without alg on the JWK.
		{"EdDSA on Ed25519", h("EdDSA", "ed"), eddsa, true, ""},
		{"EdDSA on Ed25519 JWK without alg", h("EdDSA", "ed-no-alg"), ed25519Sign(otherPriv), true, ""},
		// The other supported algorithms still verify beside an OKP key.
		{"RS256 on RSA beside OKP", h("RS256", "rsa"), rs256, true, ""},
		{"ES256 on P-256 beside OKP", h("ES256", "p256"), es256, true, ""},
		{"ES384 on P-384 beside OKP", h("ES384", "p384"), es384, true, ""},

		// EdDSA with a key of another type.
		{"EdDSA on RSA key", h("EdDSA", "rsa"), eddsa, false, "invalid JWT signature"},
		{"EdDSA on RSA key with RS256 signature", h("EdDSA", "rsa"), rs256, false, "invalid JWT signature"},
		{"EdDSA on P-256 key", h("EdDSA", "p256"), eddsa, false, "invalid JWT signature"},
		{"EdDSA on P-256 key with ES256 signature", h("EdDSA", "p256"), es256, false, "invalid JWT signature"},
		{"EdDSA on P-384 key", h("EdDSA", "p384"), eddsa, false, "invalid JWT signature"},
		{"EdDSA on P-384 key with ES384 signature", h("EdDSA", "p384"), es384, false, "invalid JWT signature"},
		// Another algorithm with an OKP key.
		{"RS256 on Ed25519 key", h("RS256", "ed"), eddsa, false, "invalid JWT signature"},
		{"RS256 on Ed25519 key with RS256 signature", h("RS256", "ed"), rs256, false, "invalid JWT signature"},
		{"ES256 on Ed25519 key", h("ES256", "ed"), eddsa, false, "invalid JWT signature"},
		{"ES256 on Ed25519 key with ES256 signature", h("ES256", "ed"), es256, false, "invalid JWT signature"},
		{"ES384 on Ed25519 key", h("ES384", "ed"), eddsa, false, "invalid JWT signature"},
		{"ES384 on Ed25519 key with ES384 signature", h("ES384", "ed"), es384, false, "invalid JWT signature"},
		{"ES256 on Ed25519 key with a 64-byte EdDSA signature", h("ES256", "ed-no-alg"), ed25519Sign(otherPriv), false, "invalid JWT signature"},

		// Every spelling of the algorithm other than exactly EdDSA.
		{"Ed25519 as alg", h("Ed25519", "ed"), eddsa, false, "requires RS256, ES256, ES384 or EdDSA"},
		{"EDDSA", h("EDDSA", "ed"), eddsa, false, "requires RS256, ES256, ES384 or EdDSA"},
		{"eddsa", h("eddsa", "ed"), eddsa, false, "requires RS256, ES256, ES384 or EdDSA"},
		{"EdDsa", h("EdDsa", "ed"), eddsa, false, "requires RS256, ES256, ES384 or EdDSA"},
		{"EdDSA with a trailing space", h("EdDSA ", "ed"), eddsa, false, "requires RS256, ES256, ES384 or EdDSA"},
		{"Ed448", h("Ed448", "ed"), eddsa, false, "requires RS256, ES256, ES384 or EdDSA"},
		{"none on Ed25519 key", h("none", "ed"), constSig(nil), false, "requires RS256, ES256, ES384 or EdDSA"},
		{"none with a signature", h("none", "ed"), eddsa, false, "requires RS256, ES256, ES384 or EdDSA"},
		{"HS256 keyed with the Ed25519 public key", h("HS256", "ed"), hmacWith(pub, crypto.SHA256), false, "requires RS256, ES256, ES384 or EdDSA"},
		{"empty alg on Ed25519 key", h("", "ed"), eddsa, false, "requires RS256, ES256, ES384 or EdDSA"},

		// Malformed signatures.
		{"EdDSA truncated to 63 bytes", h("EdDSA", "ed"), mutateSig(eddsa, func(b []byte) []byte { return b[:63] }), false, "invalid JWT signature"},
		{"EdDSA with a trailing byte", h("EdDSA", "ed"), mutateSig(eddsa, func(b []byte) []byte { return append(b, 0) }), false, "invalid JWT signature"},
		{"EdDSA with a leading zero byte", h("EdDSA", "ed"), mutateSig(eddsa, func(b []byte) []byte { return append([]byte{0}, b...) }), false, "invalid JWT signature"},
		{"EdDSA bit flip in R", h("EdDSA", "ed"), mutateSig(eddsa, func(b []byte) []byte { b[3] ^= 1; return b }), false, "invalid JWT signature"},
		{"EdDSA bit flip in S", h("EdDSA", "ed"), mutateSig(eddsa, func(b []byte) []byte { b[40] ^= 1; return b }), false, "invalid JWT signature"},
		{"EdDSA S not reduced", h("EdDSA", "ed"), mutateSig(eddsa, func(b []byte) []byte { b[63] |= 0xf0; return b }), false, "invalid JWT signature"},
		{"EdDSA all zero", h("EdDSA", "ed"), constSig(make([]byte, 64)), false, "invalid JWT signature"},
		{"EdDSA empty signature", h("EdDSA", "ed"), constSig(nil), false, "invalid JWT signature"},
		{"EdDSA signed by another key", h("EdDSA", "ed"), ed25519Sign(otherPriv), false, "invalid JWT signature"},
		{"EdDSA 128-byte signature", h("EdDSA", "ed"), mutateSig(eddsa, func(b []byte) []byte { return append(b, b...) }), false, "invalid JWT signature"},

		// Keys the verifier must not load: a token naming them is an unknown key.
		{"EdDSA on JWK claiming RS256", h("EdDSA", "ed-claims-rs256"), eddsa, false, "unknown JWT signing key"},
		{"RS256 on Ed25519 JWK claiming RS256", h("RS256", "ed-claims-rs256"), rs256, false, "unknown JWT signing key"},
		{"EdDSA on JWK claiming ES256", h("EdDSA", "ed-claims-es256"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on JWK claiming Ed25519", h("EdDSA", "ed-claims-ed25519"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on JWK claiming eddsa", h("EdDSA", "ed-claims-lower"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on Ed448 key", h("EdDSA", "ed448"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on Ed448 key with a 32-byte x", h("EdDSA", "ed448-short"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on X25519 key", h("EdDSA", "x25519"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on OKP key without crv", h("EdDSA", "no-crv"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on lower-case crv", h("EdDSA", "lower-crv"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on 31-byte x", h("EdDSA", "short-x"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on 33-byte x", h("EdDSA", "long-x"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on empty x", h("EdDSA", "empty-x"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on JWK with private d", h("EdDSA", "ed-private"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on padded base64 x", h("EdDSA", "ed-padded-x"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on standard-alphabet x", h("EdDSA", "ed-std-alphabet"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on OKP JWK with y", h("EdDSA", "ed-with-y"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on encryption key", h("EdDSA", "ed-enc"), eddsa, false, "unknown JWT signing key"},
		{"EdDSA on OKP key published as EC", h("EdDSA", "okp-as-ec"), eddsa, false, "unknown JWT signing key"},
		{"ES256 on EC key published as OKP", h("ES256", "ec-as-okp"), es256, false, "unknown JWT signing key"},
	}
	for _, tc := range cases {
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

func TestJWKSEdDSATamperedPayloadRejected(t *testing.T) {
	pub, priv := newEd25519(t)
	server := jwksServer(t, []map[string]string{okpJWK("ed", "Ed25519", "EdDSA", pub)})
	verifier := mustVerifier(t, server.URL, []string{"EdDSA"})
	input := signingInput(t, map[string]any{"alg": "EdDSA", "kid": "ed"}, matrixClaims())
	sig := ed25519.Sign(priv, []byte(input))
	if _, err := verifier.Verify(context.Background(), input+"."+base64.RawURLEncoding.EncodeToString(sig)); err != nil {
		t.Fatalf("untampered token rejected: %v", err)
	}
	parts := strings.Split(input, ".")
	forged := matrixClaims()
	forged["sub"] = "admin"
	body, _ := json.Marshal(forged)
	if _, err := verifier.Verify(context.Background(), parts[0]+"."+base64.RawURLEncoding.EncodeToString(body)+"."+base64.RawURLEncoding.EncodeToString(sig)); err == nil {
		t.Fatal("tampered EdDSA payload accepted")
	}
}

// A verifier accepts only the algorithms it was configured with. With none
// configured it accepts RS256, ES256 and ES384, as before EdDSA existed, so a
// verifier that did not ask for EdDSA refuses it even when the key set
// publishes an Ed25519 key.
func TestJWKSAlgorithmAllowList(t *testing.T) {
	keys := newAlgorithmKeys(t)
	pub, priv := newEd25519(t)
	server := jwksServer(t, []map[string]string{rsaJWK("rsa", "RS256", &keys.rsa.PublicKey), ecJWK("p384", "P-384", "ES384", &keys.p384.PublicKey, 48), okpJWK("ed", "Ed25519", "EdDSA", pub)})
	token := func(alg, kid string, s signer) string {
		input := signingInput(t, map[string]any{"alg": alg, "kid": kid}, matrixClaims())
		return input + "." + base64.RawURLEncoding.EncodeToString(s(t, input))
	}
	eddsa := token("EdDSA", "ed", ed25519Sign(priv))
	rs256 := token("RS256", "rsa", rsaPKCS1(keys.rsa, crypto.SHA256))
	es384 := token("ES384", "p384", ecdsaRaw(keys.p384, crypto.SHA384, 48))

	for _, tc := range []struct {
		name       string
		algorithms []string
		token      string
		ok         bool
		reason     string
	}{
		{"default refuses EdDSA", nil, eddsa, false, "JWT requires RS256, ES256 or ES384 and a key ID"},
		{"default accepts RS256", nil, rs256, true, ""},
		{"default accepts ES384", nil, es384, true, ""},
		{"EdDSA only accepts EdDSA", []string{"EdDSA"}, eddsa, true, ""},
		{"EdDSA only refuses RS256", []string{"EdDSA"}, rs256, false, "JWT requires EdDSA and a key ID"},
		{"EdDSA only refuses ES384", []string{"EdDSA"}, es384, false, "JWT requires EdDSA and a key ID"},
		{"RS256 and EdDSA refuses ES384", []string{"RS256", "EdDSA"}, es384, false, "JWT requires RS256 or EdDSA and a key ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := mustVerifier(t, server.URL, tc.algorithms)
			_, err := v.Verify(context.Background(), tc.token)
			if tc.ok != (err == nil) || (!tc.ok && !strings.Contains(err.Error(), tc.reason)) {
				t.Fatalf("got %v, want ok=%v %q", err, tc.ok, tc.reason)
			}
		})
	}

	for name, algorithms := range map[string][]string{
		"unknown":      {"EdDSA", "Ed25519"},
		"lower case":   {"eddsa"},
		"none":         {"none"},
		"HS256":        {"HS256"},
		"duplicate":    {"EdDSA", "EdDSA"},
		"empty string": {""},
	} {
		if _, err := NewJWKSVerifier(JWKSConfig{Issuer: matrixIssuer, Audience: matrixAudience, JWKSURL: server.URL, Algorithms: algorithms}); err == nil {
			t.Errorf("%s: verifier accepted algorithms %q", name, algorithms)
		}
	}
}

// A kid shared by two keys of different types refuses the whole set, in
// either order, as it does for RSA and EC keys.
func TestJWKSRejectsDuplicateKeyIDAcrossKeyTypes(t *testing.T) {
	keys := newAlgorithmKeys(t)
	pub, priv := newEd25519(t)
	otherPub, _ := newEd25519(t)
	ed := okpJWK("dup", "Ed25519", "EdDSA", pub)
	for name, set := range map[string][]map[string]string{
		"Ed25519 twice":   {ed, okpJWK("dup", "Ed25519", "", otherPub)},
		"RSA / Ed25519":   {rsaJWK("dup", "RS256", &keys.rsa.PublicKey), ed},
		"Ed25519 / RSA":   {ed, rsaJWK("dup", "RS256", &keys.rsa.PublicKey)},
		"P-256 / Ed25519": {ecJWK("dup", "P-256", "ES256", &keys.p256.PublicKey, 32), ed},
		"Ed25519 / P-384": {ed, ecJWK("dup", "P-384", "ES384", &keys.p384.PublicKey, 48)},
	} {
		t.Run(name, func(t *testing.T) {
			server := jwksServer(t, set)
			verifier := mustVerifier(t, server.URL, allAlgorithms)
			input := signingInput(t, map[string]any{"alg": "EdDSA", "kid": "dup"}, matrixClaims())
			token := input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(input)))
			if _, err := verifier.Verify(context.Background(), token); err == nil || !strings.Contains(err.Error(), "duplicate JWKS key ID") {
				t.Fatalf("duplicate kid not refused: %v", err)
			}
		})
	}
}

// A key set of only unusable OKP keys has no signing keys.
func TestJWKSWithOnlyUnusableOKPKeysHasNoSigningKeys(t *testing.T) {
	pub, priv := newEd25519(t)
	private := okpJWK("b", "Ed25519", "EdDSA", pub)
	private["d"] = base64.RawURLEncoding.EncodeToString(priv.Seed())
	server := jwksServer(t, []map[string]string{okpJWK("a", "X25519", "", pub), private, okpJWK("c", "Ed25519", "ES256", pub)})
	verifier := mustVerifier(t, server.URL, []string{"EdDSA"})
	input := signingInput(t, map[string]any{"alg": "EdDSA", "kid": "b"}, matrixClaims())
	token := input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(input)))
	if _, err := verifier.Verify(context.Background(), token); err == nil || !strings.Contains(err.Error(), "no supported signing keys") {
		t.Fatalf("unexpected result: %v", err)
	}
}

// Rotation, the unknown-kid refresh bound and the refresh cooldown work the
// same for OKP keys: a new kid is fetched once the cached set is a minute old,
// an unknown kid inside that minute does not refetch, and a failed refresh
// cools down.
func TestJWKSEdDSARefreshAndCooldownUnchanged(t *testing.T) {
	first, firstPriv := newEd25519(t)
	second, secondPriv := newEd25519(t)
	var fetches atomic.Int32
	var rotated atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		keys := []map[string]string{okpJWK("one", "Ed25519", "EdDSA", first)}
		if rotated.Load() {
			keys = append(keys, okpJWK("two", "Ed25519", "EdDSA", second))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	defer server.Close()
	verifier := mustVerifier(t, server.URL, []string{"EdDSA"})
	sign := func(kid string, key ed25519.PrivateKey) string {
		input := signingInput(t, map[string]any{"alg": "EdDSA", "kid": kid}, matrixClaims())
		return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(input)))
	}
	if _, err := verifier.Verify(context.Background(), sign("one", firstPriv)); err != nil {
		t.Fatal(err)
	}
	rotated.Store(true)
	// Inside the first minute an unknown kid is refused without a refetch.
	if _, err := verifier.Verify(context.Background(), sign("two", secondPriv)); err == nil || !strings.Contains(err.Error(), "unknown JWT signing key") {
		t.Fatalf("unknown kid inside the first minute: %v", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("fetched %d times, want 1", n)
	}
	// A minute later the new kid is fetched.
	verifier.mu.Lock()
	verifier.refreshed = verifier.refreshed.Add(-2 * time.Minute)
	verifier.lastAttempt = verifier.lastAttempt.Add(-2 * time.Minute)
	verifier.mu.Unlock()
	if _, err := verifier.Verify(context.Background(), sign("two", secondPriv)); err != nil {
		t.Fatalf("rotated kid: %v", err)
	}
	if n := fetches.Load(); n != 2 {
		t.Fatalf("fetched %d times, want 2", n)
	}
	// A refresh attempt inside the cooldown is refused without a fetch.
	verifier.mu.Lock()
	verifier.refreshed = verifier.refreshed.Add(-2 * time.Hour)
	verifier.mu.Unlock()
	if _, err := verifier.Verify(context.Background(), sign("three", secondPriv)); err == nil || !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("refresh inside cooldown: %v", err)
	}
	if n := fetches.Load(); n != 2 {
		t.Fatalf("fetched %d times, want 2", n)
	}
}
