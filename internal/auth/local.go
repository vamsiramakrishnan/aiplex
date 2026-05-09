package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// LocalSigner implements local-mode auth: an Ed25519 keypair stored on disk
// signs JWTs carrying the structured `caps` claim. No Hydra, no Kratos, no
// network roundtrip. The "user" is whoever's running the binary.
//
// This is what makes `aiplex up` viable: the same Capability primitive,
// the same OPA-compatible claim shape, dialed down to single-user trust.
// Local-mode tokens are interchangeable with Hydra-issued tokens at the
// authz layer — the policy reads `caps` regardless of who signed.
type LocalSigner struct {
	keyPath    string
	priv       ed25519.PrivateKey
	pub        ed25519.PublicKey
	keyID      string // base64 of first 8 bytes of public key (for kid)
	issuer     string // local URI; embedded in JWT iss claim
	defaultTTL time.Duration
}

// NewLocalSigner loads the keypair from keyPath, generating one if it
// doesn't exist. The directory is created with mode 0700; the key file
// with mode 0600. The issuer string ends up in the JWT iss claim and is
// used by verifiers to scope trust to local mode.
func NewLocalSigner(keyPath, issuer string) (*LocalSigner, error) {
	if keyPath == "" {
		home, _ := os.UserHomeDir()
		keyPath = filepath.Join(home, ".aiplex", "local.key")
	}
	if issuer == "" {
		issuer = "aiplex://local"
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, fmt.Errorf("create key dir: %w", err)
	}

	var priv ed25519.PrivateKey
	if data, err := os.ReadFile(keyPath); err == nil {
		raw, err := base64.StdEncoding.DecodeString(string(data))
		if err != nil || len(raw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("local key at %s is corrupted: %w", keyPath, err)
		}
		priv = ed25519.PrivateKey(raw)
	} else {
		_, generated, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate ed25519 key: %w", err)
		}
		priv = generated
		encoded := base64.StdEncoding.EncodeToString(priv)
		if err := os.WriteFile(keyPath, []byte(encoded), 0o600); err != nil {
			return nil, fmt.Errorf("persist local key: %w", err)
		}
	}

	pub := priv.Public().(ed25519.PublicKey)
	kid := base64.RawURLEncoding.EncodeToString(pub[:8])

	return &LocalSigner{
		keyPath:    keyPath,
		priv:       priv,
		pub:        pub,
		keyID:      kid,
		issuer:     issuer,
		defaultTTL: 24 * time.Hour,
	}, nil
}

// Issuer returns the iss claim local tokens will carry.
func (s *LocalSigner) Issuer() string { return s.issuer }

// PublicKey returns the verifying key (for JWKS endpoints / external verifiers).
func (s *LocalSigner) PublicKey() ed25519.PublicKey { return s.pub }

// KeyID returns the kid stamped into local-mode tokens.
func (s *LocalSigner) KeyID() string { return s.keyID }

// Mint signs a JWT for the given subject with the supplied capability set.
// agentSpiffe (optional) is set in the RFC 8693 actor claim so downstream
// caps see who is acting on behalf of subject. ttl=0 uses the default;
// negative ttl mints an already-expired token (useful in tests).
func (s *LocalSigner) Mint(subject string, caps capability.CapSet, agentSpiffe string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = s.defaultTTL
	}
	now := time.Now()

	claims := map[string]any{
		"iss":  s.issuer,
		"sub":  subject,
		"iat":  now.Unix(),
		"nbf":  now.Unix(),
		"exp":  now.Add(ttl).Unix(),
		"caps": caps,
	}
	if agentSpiffe != "" {
		claims["act"] = map[string]string{"sub": agentSpiffe}
		claims["azp"] = agentSpiffe
	}

	header := map[string]any{
		"alg": "EdDSA",
		"typ": "JWT",
		"kid": s.keyID,
	}

	encH, err := encodeJSON(header)
	if err != nil {
		return "", err
	}
	encC, err := encodeJSON(claims)
	if err != nil {
		return "", err
	}
	signingInput := encH + "." + encC
	sig := ed25519.Sign(s.priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// LocalClaims is what Verify returns: the unpacked, validated payload of a
// local-mode token. Callers should treat (Subject, Caps) as authoritative.
type LocalClaims struct {
	Subject  string            `json:"sub"`
	Issuer   string            `json:"iss"`
	Caps     capability.CapSet `json:"caps"`
	Act      map[string]string `json:"act,omitempty"`
	Audience string            `json:"aud,omitempty"`
	IssuedAt int64             `json:"iat,omitempty"`
	Expires  int64             `json:"exp,omitempty"`
}

// Verify checks the signature, expiry, and issuer of a local-mode token.
// Returns the unpacked claims or an error.
func (s *LocalSigner) Verify(token string) (*LocalClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed token: expected 3 parts, got %d", len(parts))
	}
	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(s.pub, []byte(signingInput), sig) {
		return nil, fmt.Errorf("signature verification failed")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var claims LocalClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	if claims.Issuer != s.issuer {
		return nil, fmt.Errorf("issuer %q does not match local %q", claims.Issuer, s.issuer)
	}
	if claims.Expires > 0 && time.Now().Unix() > claims.Expires {
		return nil, fmt.Errorf("token expired")
	}
	return &claims, nil
}

func encodeJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
