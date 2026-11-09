package membership

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// HTTPConfig pins a signed directory authority. Keys are Ed25519 public keys in
// unpadded base64url; private signing keys never belong in Aiplex configuration.
type HTTPConfig struct {
	Endpoint       string            `json:"endpoint"`
	Authority      string            `json:"authority"`
	AllowedSources []string          `json:"allowed_sources"`
	PublicKeys     map[string]string `json:"public_keys"`
	MaxAge         time.Duration     `json:"max_age"`
}
type TokenSource func(context.Context, string) (string, error)
type HTTPAuthority struct {
	config HTTPConfig
	client *http.Client
	token  TokenSource
	keys   map[string]ed25519.PublicKey
	digest string
	clock  func() time.Time
}

// SignedSnapshot's signature covers the exact query digest and typed snapshot.
// This prevents a directory proxy/cache from substituting another tenant,
// principal, role, authority or membership observation.
type SignedSnapshot struct {
	Snapshot    Snapshot `json:"snapshot"`
	QueryDigest string   `json:"query_digest"`
	KeyID       string   `json:"key_id"`
	Signature   string   `json:"signature"`
}

func SigningBytes(s SignedSnapshot) ([]byte, error) {
	return json.Marshal(struct {
		Version     string   `json:"version"`
		QueryDigest string   `json:"query_digest"`
		Snapshot    Snapshot `json:"snapshot"`
	}{Version, s.QueryDigest, s.Snapshot})
}
func NewHTTP(cfg HTTPConfig, client *http.Client, token TokenSource, clocks ...func() time.Time) (*HTTPAuthority, error) {
	clock := time.Now
	if len(clocks) > 1 || (len(clocks) == 1 && clocks[0] == nil) {
		return nil, ErrInvalidEvidence
	}
	if len(clocks) == 1 {
		clock = clocks[0]
	}
	u, e := url.Parse(cfg.Endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || strings.ContainsAny(cfg.Endpoint, "\r\n\\") || !Text(cfg.Authority) || len(cfg.AllowedSources) == 0 || len(cfg.AllowedSources) > 64 || len(cfg.PublicKeys) == 0 || len(cfg.PublicKeys) > 8 || cfg.MaxAge < time.Second || cfg.MaxAge > 24*time.Hour || client == nil || token == nil {
		return nil, fmt.Errorf("signed HTTPS membership authority configuration required")
	}
	cfg.AllowedSources = append([]string(nil), cfg.AllowedSources...)
	sort.Strings(cfg.AllowedSources)
	for i, v := range cfg.AllowedSources {
		if !Text(v) || (i > 0 && v == cfg.AllowedSources[i-1]) {
			return nil, ErrInvalidEvidence
		}
	}
	keys := map[string]ed25519.PublicKey{}
	copyKeys := map[string]string{}
	for id, encoded := range cfg.PublicKeys {
		key, err := base64.RawURLEncoding.DecodeString(encoded)
		if !Text(id) || err != nil || len(key) != ed25519.PublicKeySize {
			return nil, ErrInvalidEvidence
		}
		keys[id] = append(ed25519.PublicKey(nil), key...)
		copyKeys[id] = encoded
	}
	cfg.PublicKeys = copyKeys
	c := *client
	if c.Timeout == 0 || c.Timeout > 10*time.Second {
		c.Timeout = 10 * time.Second
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPAuthority{config: cfg, client: &c, token: token, keys: keys, digest: Digest(cfg), clock: clock}, nil
}
func (a *HTTPAuthority) ConfigurationDigest() string { return a.digest }
func (a *HTTPAuthority) Resolve(ctx context.Context, q Query, at time.Time) (Snapshot, error) {
	if q.Validate() != nil {
		return Snapshot{}, ErrInvalidEvidence
	}
	raw, _ := json.Marshal(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	token, err := a.token(ctx, q.TenantID)
	if err != nil || token == "" || len(token) > 16384 || strings.ContainsAny(token, "\r\n") {
		return Snapshot{}, ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	defer resp.Body.Close()
	contentType, _, contentErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != 200 || contentErr != nil || contentType != "application/json" {
		return Snapshot{}, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return Snapshot{}, ErrUnavailable
	}
	if err = validateWireJSON(data); err != nil {
		return Snapshot{}, ErrInvalidEvidence
	}
	var signed SignedSnapshot
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&signed) != nil || decoder.Decode(new(any)) != io.EOF {
		return Snapshot{}, ErrInvalidEvidence
	}
	key := a.keys[signed.KeyID]
	sig, err := base64.RawURLEncoding.DecodeString(signed.Signature)
	if len(key) != ed25519.PublicKeySize || err != nil || signed.QueryDigest != Digest(q) {
		return Snapshot{}, ErrInvalidEvidence
	}
	payload, err := SigningBytes(signed)
	if err != nil || !ed25519.Verify(key, payload, sig) {
		return Snapshot{}, ErrInvalidEvidence
	}
	// Validate at receipt, rather than treating lookup start as the observation
	// time. The signed directory timestamp is never synthesized or extended.
	checkedAt := a.clock().UTC()
	if at.After(checkedAt) {
		checkedAt = at
	}
	if err = Validate(signed.Snapshot, q, a.config.Authority, a.config.AllowedSources, checkedAt, a.config.MaxAge); err != nil {
		return Snapshot{}, err
	}
	return Clone(signed.Snapshot), nil
}

func validateWireJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return ErrInvalidEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := 100000
	var value func(int) error
	value = func(depth int) error {
		remaining--
		if depth > 32 || remaining < 0 {
			return ErrInvalidEvidence
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, nested := token.(json.Delim)
		if !nested {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				token, err = decoder.Token()
				name, ok := token.(string)
				name = strings.ToLower(name)
				if err != nil || !ok || seen[name] {
					return ErrInvalidEvidence
				}
				seen[name] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return ErrInvalidEvidence
			}
		case '[':
			for decoder.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return ErrInvalidEvidence
			}
		default:
			return ErrInvalidEvidence
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidEvidence
	}
	return nil
}
