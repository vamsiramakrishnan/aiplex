package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
)

// TokenVerifier is the authentication boundary. Implementations must verify the
// signature, issuer, audience and lifetime before returning any identity facts.
// Opaque Google OAuth access tokens require an appropriate adapter; they are not JWTs.
type TokenVerifier interface {
	Verify(context.Context, string) (map[string]any, error)
}

// JWKSConfig pins the issuer, resource audience and signing-key endpoint. The
// endpoint is configuration, never a URL taken from an unverified token.
type JWKSConfig struct {
	Issuer     string
	Audience   string
	JWKSURL    string
	HTTPClient *http.Client
	CacheTTL   time.Duration
	// Algorithms are the JWS algorithms this verifier accepts, each one of
	// jwsAlgorithms. Empty means RS256, ES256 and ES384 (EdDSA is accepted
	// only by a verifier that names it).
	Algorithms []string
}

// JWKSVerifier verifies RS256, ES256, ES384 and EdDSA JWTs with cached public
// keys. Each algorithm is bound to one key type: RS256 to RSA, ES256 to P-256,
// ES384 to P-384 and EdDSA to Ed25519. A verifier accepts only the algorithms
// its configuration names. Other signature algorithms can be supplied through
// TokenVerifier without changing WIF logic.
type JWKSVerifier struct {
	cfg         JWKSConfig
	algorithms  []string
	accepted    string // the algorithms, as named in errors
	mu          sync.Mutex
	keys        map[string]any
	refreshed   time.Time
	lastAttempt time.Time
}

func NewJWKSVerifier(cfg JWKSConfig) (*JWKSVerifier, error) {
	if cfg.Issuer == "" || cfg.Audience == "" {
		return nil, fmt.Errorf("JWT issuer and audience are required")
	}
	endpoint, err := url.Parse(cfg.JWKSURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && (endpoint.Hostname() == "localhost" || endpoint.Hostname() == "127.0.0.1" || endpoint.Hostname() == "::1"))) {
		return nil, fmt.Errorf("JWKS endpoint must use HTTPS (HTTP permitted only on loopback)")
	}
	if endpoint.User != nil || endpoint.Fragment != "" {
		return nil, fmt.Errorf("invalid JWKS endpoint")
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = time.Hour
	}
	if cfg.CacheTTL < time.Second || cfg.CacheTTL > 24*time.Hour {
		return nil, fmt.Errorf("JWKS cache TTL must be between one second and 24 hours")
	}
	algorithms := defaultJWSAlgorithms
	if len(cfg.Algorithms) > 0 {
		algorithms = nil
		for _, alg := range cfg.Algorithms {
			if !slices.Contains(jwsAlgorithms, alg) || slices.Contains(algorithms, alg) {
				return nil, fmt.Errorf("JWT algorithm %q is not supported or is listed twice", alg)
			}
			algorithms = append(algorithms, alg)
		}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	// Never follow redirects to an unpinned key service.
	client := *cfg.HTTPClient
	if client.Timeout == 0 || client.Timeout > 10*time.Second {
		client.Timeout = 5 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cfg.HTTPClient = &client
	return &JWKSVerifier{cfg: cfg, algorithms: algorithms, accepted: agentfederation.AlgorithmList(algorithms)}, nil
}

func (v *JWKSVerifier) Verify(ctx context.Context, token string) (map[string]any, error) {
	claims, err := v.VerifySignature(ctx, token)
	if err != nil {
		return nil, err
	}
	if claims["iss"] != v.cfg.Issuer {
		return nil, fmt.Errorf("untrusted JWT issuer")
	}
	if !hasAudience(claims["aud"], v.cfg.Audience) {
		return nil, fmt.Errorf("invalid JWT audience")
	}
	subject, _ := claims["sub"].(string)
	if strings.TrimSpace(subject) == "" {
		return nil, fmt.Errorf("JWT subject is required")
	}
	exp, ok := numericDate(claims["exp"])
	if !ok || time.Now().Unix() >= exp {
		return nil, fmt.Errorf("JWT is expired or has no valid expiry")
	}
	if raw, exists := claims["nbf"]; exists {
		nbf, ok := numericDate(raw)
		if !ok || time.Now().Unix() < nbf {
			return nil, fmt.Errorf("JWT is not yet valid")
		}
	}
	return claims, nil
}

// VerifySignature checks only the JOSE header, the signature against the
// pinned key set and the payload encoding. It performs no issuer, audience,
// subject or lifetime checks; callers that use it directly (for example the
// agent federation verification engine, which reports each check separately)
// must apply those checks themselves before trusting any claim.
func (v *JWKSVerifier) VerifySignature(ctx context.Context, token string) (map[string]any, error) {
	if len(token) > 64*1024 {
		return nil, fmt.Errorf("JWT exceeds size limit")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	h, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(h, &header) != nil || !slices.Contains(v.algorithms, header.Algorithm) || header.KeyID == "" {
		return nil, fmt.Errorf("JWT requires %s and a key ID", v.accepted)
	}
	key, err := v.key(ctx, header.KeyID)
	if err != nil {
		return nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT signature encoding")
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	valid := false
	switch k := key.(type) {
	case *rsa.PublicKey:
		digest := sha256.Sum256(signingInput)
		valid = header.Algorithm == "RS256" && rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], signature) == nil
	case *ecdsa.PublicKey:
		// The curve of the pinned key decides the algorithm; the header must
		// name exactly that algorithm, and the signature must be raw R||S of
		// exactly twice the curve's coordinate size (RFC 7518 section 3.4).
		spec, ok := ecAlgorithms[k.Curve.Params().Name]
		if ok && spec.curve == k.Curve && header.Algorithm == spec.alg && len(signature) == 2*spec.size {
			valid = ecdsa.Verify(k, spec.digest(signingInput), new(big.Int).SetBytes(signature[:spec.size]), new(big.Int).SetBytes(signature[spec.size:]))
		}
	case ed25519.PublicKey:
		// Only Ed25519 keys are loaded as OKP keys. The header must name
		// exactly EdDSA, and the signature must be exactly 64 bytes
		// (RFC 8037 section 3.1). ed25519.Verify also refuses a
		// non-canonical S.
		spec := okpAlgorithms["Ed25519"]
		if header.Algorithm == spec.alg && len(k) == spec.size && len(signature) == spec.signature {
			valid = ed25519.Verify(k, signingInput, signature)
		}
	}
	if !valid {
		return nil, fmt.Errorf("invalid JWT signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT payload encoding")
	}
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.UseNumber()
	var claims map[string]any
	if dec.Decode(&claims) != nil {
		return nil, fmt.Errorf("invalid JWT payload")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("invalid trailing JWT payload")
	}
	return claims, nil
}

// jwsAlgorithms are every JWS algorithm the verifier knows. Each is bound to
// exactly one key type, and a token is checked only against the key its kid
// names, under the one algorithm that key's type allows:
//
//	RS256  RSA, at least 2048 bits
//	ES256  EC on P-256 (ecAlgorithms)
//	ES384  EC on P-384 (ecAlgorithms)
//	EdDSA  OKP on Ed25519 (okpAlgorithms)
var jwsAlgorithms = []string{"RS256", "ES256", "ES384", "EdDSA"}

// defaultJWSAlgorithms are what a verifier accepts when its configuration
// names none: every algorithm except EdDSA, which a caller asks for.
var defaultJWSAlgorithms = []string{"RS256", "ES256", "ES384"}

// okpAlgorithm binds one OKP curve to the only JWS algorithm it may verify.
type okpAlgorithm struct {
	alg       string
	size      int // bytes of the public key x
	signature int // bytes of a signature
}

// okpAlgorithms is keyed by the JWK crv value. Ed448, X25519 and X448 are not
// signing curves this verifier supports and are not loaded.
var okpAlgorithms = map[string]okpAlgorithm{
	"Ed25519": {alg: "EdDSA", size: ed25519.PublicKeySize, signature: ed25519.SignatureSize},
}

// ecAlgorithm binds one JWK curve to the only JWS algorithm it may verify.
type ecAlgorithm struct {
	curve  elliptic.Curve
	alg    string
	size   int // bytes per coordinate and per signature half
	digest func([]byte) []byte
}

// ecAlgorithms is keyed by the JWK crv value, which is also the curve's
// Params().Name.
var ecAlgorithms = map[string]ecAlgorithm{
	"P-256": {curve: elliptic.P256(), alg: "ES256", size: 32, digest: func(b []byte) []byte { d := sha256.Sum256(b); return d[:] }},
	"P-384": {curve: elliptic.P384(), alg: "ES384", size: 48, digest: func(b []byte) []byte { d := sha512.Sum384(b); return d[:] }},
}

func numericDate(raw any) (int64, bool) {
	n, ok := raw.(json.Number)
	if !ok {
		return 0, false
	}
	t, err := n.Int64()
	return t, err == nil
}
func hasAudience(raw any, want string) bool {
	if s, ok := raw.(string); ok {
		return s == want
	}
	if values, ok := raw.([]any); ok {
		for _, value := range values {
			if s, ok := value.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func (v *JWKSVerifier) key(ctx context.Context, kid string) (any, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := time.Now()
	if v.keys != nil && now.Sub(v.refreshed) < v.cfg.CacheTTL {
		if key := v.keys[kid]; key != nil {
			return key, nil
		}
		// Bound attacker-controlled unknown-kid refreshes while allowing rotation.
		if now.Sub(v.refreshed) < time.Minute {
			return nil, fmt.Errorf("unknown JWT signing key")
		}
	}
	if !v.lastAttempt.IsZero() && now.Sub(v.lastAttempt) < time.Minute {
		return nil, fmt.Errorf("JWKS refresh is cooling down")
	}
	v.lastAttempt = now
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create JWKS request: %w", err)
	}
	response, err := v.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, fmt.Errorf("invalid JWKS response size")
	}
	var set struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			Type      string `json:"kty"`
			Algorithm string `json:"alg"`
			Use       string `json:"use"`
			N         string `json:"n"`
			E         string `json:"e"`
			Curve     string `json:"crv"`
			X         string `json:"x"`
			Y         string `json:"y"`
			D         string `json:"d"`
		} `json:"keys"`
	}
	if json.Unmarshal(data, &set) != nil {
		return nil, fmt.Errorf("invalid JWKS document")
	}
	keys := make(map[string]any)
	for _, j := range set.Keys {
		if j.KeyID == "" || (j.Use != "" && j.Use != "sig") {
			continue
		}
		if j.Type == "EC" {
			spec, ok := ecAlgorithms[j.Curve]
			if !ok || (j.Algorithm != "" && j.Algorithm != spec.alg) {
				continue
			}
			x, xe := base64.RawURLEncoding.DecodeString(j.X)
			y, ye := base64.RawURLEncoding.DecodeString(j.Y)
			if xe != nil || ye != nil || len(x) != spec.size || len(y) != spec.size {
				continue
			}
			pointX, pointY := new(big.Int).SetBytes(x), new(big.Int).SetBytes(y)
			if !spec.curve.IsOnCurve(pointX, pointY) {
				continue
			}
			if _, duplicate := keys[j.KeyID]; duplicate {
				return nil, fmt.Errorf("duplicate JWKS key ID")
			}
			keys[j.KeyID] = &ecdsa.PublicKey{Curve: spec.curve, X: pointX, Y: pointY}
			continue
		}
		if j.Type == "OKP" {
			// An OKP key is loaded only on a curve in okpAlgorithms, with an
			// alg that is absent or that curve's algorithm, a public x of
			// exactly the curve's size, and neither a private d (a key set
			// that publishes a private key is not trusted for that key) nor
			// a y.
			spec, ok := okpAlgorithms[j.Curve]
			if !ok || (j.Algorithm != "" && j.Algorithm != spec.alg) || j.D != "" || j.Y != "" {
				continue
			}
			x, err := base64.RawURLEncoding.DecodeString(j.X)
			if err != nil || len(x) != spec.size {
				continue
			}
			if _, duplicate := keys[j.KeyID]; duplicate {
				return nil, fmt.Errorf("duplicate JWKS key ID")
			}
			keys[j.KeyID] = ed25519.PublicKey(x)
			continue
		}
		if j.Type != "RSA" || (j.Algorithm != "" && j.Algorithm != "RS256") {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(j.N)
		if err != nil || len(n) < 256 || len(n) > 1024 {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(j.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exponent := int64(0)
		for _, b := range e {
			exponent = exponent*256 + int64(b)
		}
		if exponent < 3 || exponent > 1<<31-1 || exponent%2 == 0 {
			continue
		}
		if _, duplicate := keys[j.KeyID]; duplicate {
			return nil, fmt.Errorf("duplicate JWKS key ID")
		}
		modulus := new(big.Int).SetBytes(n)
		if modulus.BitLen() < 2048 {
			continue
		}
		keys[j.KeyID] = &rsa.PublicKey{N: modulus, E: int(exponent)}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("JWKS contains no supported signing keys")
	}
	v.keys, v.refreshed = keys, now
	key := keys[kid]
	if key == nil {
		return nil, fmt.Errorf("unknown JWT signing key")
	}
	return key, nil
}
