// Package devstack provides the zero-dependency local development stack used by
// `aiplex dev` and the end-to-end tests: a loopback token issuer, mock model
// providers that speak the OpenAI, Anthropic and Gemini wire formats, a tiny
// MCP server, and the process wiring that boots a real aiplex-api in
// development mode against them.
//
// Nothing here is used by a production server. The issuer's private key lives
// only in memory for the lifetime of the process that created it.
package devstack

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"
)

// Issuer is an in-memory ES256 token issuer with a JWKS endpoint. It exists so
// a local API can verify identities exactly as it would in production (the
// JWKS verifier, issuer and audience pinning), without an external identity
// provider.
type Issuer struct {
	key      *ecdsa.PrivateKey
	kid      string
	issuer   string
	audience string
}

// NewIssuer creates an issuer with a fresh key. issuer is the `iss` value and
// should be the base URL the JWKS is served from.
func NewIssuer(issuer, audience string) (*Issuer, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append(key.PublicKey.X.Bytes(), key.PublicKey.Y.Bytes()...))
	return &Issuer{key: key, kid: "dev-" + hex.EncodeToString(sum[:6]), issuer: issuer, audience: audience}, nil
}

// SetIssuer changes the issuer URL (used once the listener's port is known).
func (i *Issuer) SetIssuer(issuer string) { i.issuer = issuer }

// IssuerURL is the `iss` claim of tokens this issuer mints.
func (i *Issuer) IssuerURL() string { return i.issuer }

// Audience is the `aud` claim of tokens this issuer mints.
func (i *Issuer) Audience() string { return i.audience }

func pad32(n *big.Int) []byte {
	out := make([]byte, 32)
	b := n.Bytes()
	copy(out[32-len(b):], b)
	return out
}

// JWKS returns the public key set document.
func (i *Issuer) JWKS() []byte {
	doc := map[string]any{"keys": []map[string]string{{
		"kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig", "kid": i.kid,
		"x": base64.RawURLEncoding.EncodeToString(pad32(i.key.PublicKey.X)),
		"y": base64.RawURLEncoding.EncodeToString(pad32(i.key.PublicKey.Y)),
	}}}
	out, _ := json.Marshal(doc)
	return out
}

// Handler serves /.well-known/jwks.json and a minimal OpenID discovery
// document at the issuer root.
func (i *Issuer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(i.JWKS())
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": i.issuer, "jwks_uri": i.issuer + "/.well-known/jwks.json", "id_token_signing_alg_values_supported": []string{"ES256"}})
	})
	return mux
}

// JWKSURL is where Handler serves the key set, given the issuer is the base URL.
func (i *Issuer) JWKSURL() string { return i.issuer + "/.well-known/jwks.json" }

// Mint signs claims. iss, aud, iat and exp are filled in when absent; ttl
// bounds the lifetime.
func (i *Issuer) Mint(claims map[string]any, ttl time.Duration) (string, error) {
	now := time.Now()
	out := map[string]any{"iss": i.issuer, "aud": i.audience, "iat": now.Unix(), "exp": now.Add(ttl).Unix()}
	for k, v := range claims {
		out[k] = v
	}
	if s, _ := out["sub"].(string); s == "" {
		return "", fmt.Errorf("sub is required")
	}
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "typ": "JWT", "kid": i.kid})
	payload, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, i.key, digest[:])
	if err != nil {
		return "", err
	}
	sig := append(pad32(r), pad32(s)...)
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
