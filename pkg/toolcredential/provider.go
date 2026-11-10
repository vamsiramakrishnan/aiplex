package toolcredential

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"
)

// SecretResolver resolves an opaque reference in the tenant's namespace at
// dispatch time. Implementations must never accept a reference from another
// tenant's namespace and must not include the value in errors.
type SecretResolver interface {
	Resolve(ctx context.Context, tenant, ref string) (string, error)
}

// SecretChecker is optionally implemented by resolvers that can prove a
// reference exists without reading its value, for save-time validation.
type SecretChecker interface {
	Exists(ctx context.Context, tenant, ref string) (bool, error)
}

// IDTokenSource issues a workload ID token for a fixed audience from an
// operator identity profile. It is injected by the host.
type IDTokenSource interface {
	IDToken(ctx context.Context, tenant, identityRef, audience string) (string, error)
}

// URLPolicy is the outbound endpoint policy (pkg/endpointpolicy.Policy).
type URLPolicy interface {
	ValidateURL(string) error
}

// Error is a public-safe failure. It never carries upstream bodies, secret
// values or token material.
type Error struct{ Code string }

func (e *Error) Error() string { return "tool credential unavailable: " + e.Code }

func fail(code string) error { return &Error{Code: code} }

type Config struct {
	// HTTPClient must be bound to the outbound endpoint policy. Redirects are
	// always refused regardless of its CheckRedirect.
	HTTPClient *http.Client
	Policy     URLPolicy
	Secrets    SecretResolver
	IDTokens   IDTokenSource
	Clock      func() time.Time
	// RefreshSkew is the maximum margin before expiry at which a cached token
	// is no longer used (default 30s; it is also capped at half the lifetime).
	RefreshSkew    time.Duration
	MaxEntries     int
	RequestTimeout time.Duration
}

// Target identifies the dispatch a credential is acquired for. The host fills
// it only from the saved connection and the verified request context.
type Target struct {
	TenantID     string
	ConnectionID string
	Revision     uint64
	// Resource is the connection's configured endpoint; credentials are bound
	// to its origin.
	Resource    string
	PrincipalID string
	ActorID     string
	// SubjectToken is the caller's verified inbound token, used only for
	// token_exchange. SubjectExpiresAt caps caching when known.
	SubjectToken     string `json:"-"`
	SubjectExpiresAt time.Time
}

type entry struct {
	credential Credential
	refreshAt  time.Time
}

type flight struct {
	done       chan struct{}
	credential Credential
	err        error
}

type Provider struct {
	client   *http.Client
	policy   URLPolicy
	secrets  SecretResolver
	idTokens IDTokenSource
	clock    func() time.Time
	skew     time.Duration
	max      int
	timeout  time.Duration

	mu      sync.Mutex
	cache   map[string]entry
	flights map[string]*flight
}

type unconfiguredIDTokens struct{}

func (unconfiguredIDTokens) IDToken(context.Context, string, string, string) (string, error) {
	return "", fail("google_id_token_not_configured")
}

func present(value any) bool {
	if value == nil {
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return !v.IsNil()
	}
	return true
}

func NewProvider(c Config) (*Provider, error) {
	if c.HTTPClient == nil || !present(c.Policy) {
		return nil, fmt.Errorf("policy-bound HTTP client and endpoint policy are required")
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	if c.RefreshSkew == 0 {
		c.RefreshSkew = 30 * time.Second
	}
	if c.MaxEntries == 0 {
		c.MaxEntries = 10000
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = 15 * time.Second
	}
	if c.RefreshSkew < 0 || c.RefreshSkew > 5*time.Minute || c.MaxEntries < 1 || c.RequestTimeout < time.Second || c.RequestTimeout > time.Minute {
		return nil, fmt.Errorf("invalid tool credential provider bounds")
	}
	if !present(c.IDTokens) {
		c.IDTokens = unconfiguredIDTokens{}
	}
	client := *c.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	secrets := c.Secrets
	if !present(secrets) {
		secrets = nil
	}
	return &Provider{client: &client, policy: c.Policy, secrets: secrets, idTokens: c.IDTokens, clock: c.Clock, skew: c.RefreshSkew, max: c.MaxEntries, timeout: c.RequestTimeout, cache: map[string]entry{}, flights: map[string]*flight{}}, nil
}

// CheckSecret reports whether ref exists when the resolver can prove it.
// It returns nil when existence cannot be checked without reading a value.
func (p *Provider) CheckSecret(ctx context.Context, tenant, ref string) error {
	if p == nil || p.secrets == nil {
		return nil
	}
	checker, ok := p.secrets.(SecretChecker)
	if !ok {
		return nil
	}
	exists, err := checker.Exists(ctx, tenant, ref)
	if err != nil {
		return fmt.Errorf("secret reference validation is unavailable")
	}
	if !exists {
		return fmt.Errorf("secret reference %q does not exist in the tenant secret namespace", ref)
	}
	return nil
}

// SecretNamespacer is optionally implemented by resolvers to tell operators
// where a tenant's referenced secrets live (for example a name prefix).
type SecretNamespacer interface {
	Namespace(tenant string) string
}

// SecretNamespace describes the tenant secret namespace, or "" if unknown.
func (p *Provider) SecretNamespace(tenant string) string {
	if p == nil || p.secrets == nil {
		return ""
	}
	if namespacer, ok := p.secrets.(SecretNamespacer); ok {
		return namespacer.Namespace(tenant)
	}
	return ""
}

// Digest is a stable fingerprint of the auth configuration and resource.
func Digest(auth Auth, resource string) string {
	b, _ := json.Marshal(struct {
		Auth     Auth   `json:"auth"`
		Resource string `json:"resource"`
	}{auth, resource})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func cacheKey(auth Auth, t Target) string {
	key := struct {
		Tenant, Connection string
		Revision           uint64
		Digest             string
		Principal, Actor   string
		Subject            string
	}{Tenant: t.TenantID, Connection: t.ConnectionID, Revision: t.Revision, Digest: Digest(auth, t.Resource)}
	if auth.Type == TokenExchange {
		sum := sha256.Sum256([]byte(t.SubjectToken))
		key.Principal, key.Actor, key.Subject = t.PrincipalID, t.ActorID, hex.EncodeToString(sum[:])
	}
	b, _ := json.Marshal(key)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Acquire returns the upstream credential for one dispatch.
func (p *Provider) Acquire(ctx context.Context, auth Auth, t Target) (Credential, error) {
	if p == nil {
		return Credential{}, fail("provider_unavailable")
	}
	if err := auth.Validate(); err != nil {
		return Credential{}, fail("invalid_configuration")
	}
	if t.TenantID == "" || t.ConnectionID == "" {
		return Credential{}, fail("invalid_target")
	}
	origin, err := Origin(t.Resource)
	if err != nil || p.policy.ValidateURL(t.Resource) != nil {
		return Credential{}, fail("resource_origin_denied")
	}
	switch auth.Type {
	case None:
		return Credential{origin: origin, none: true}, nil
	case APIKey:
		value, err := p.secret(ctx, t.TenantID, auth.SecretRef)
		if err != nil {
			return Credential{}, err
		}
		if auth.In == InQuery {
			return Credential{origin: origin, query: auth.Name, value: value}, nil
		}
		if auth.Prefix != "" {
			value = auth.Prefix + " " + value
		}
		return headerCredential(origin, auth.Name, value), nil
	case Bearer:
		value, err := p.secret(ctx, t.TenantID, auth.SecretRef)
		if err != nil {
			return Credential{}, err
		}
		return bearerCredential(origin, value), nil
	case Basic:
		value, err := p.secret(ctx, t.TenantID, auth.SecretRef)
		if err != nil {
			return Credential{}, err
		}
		return basicCredential(origin, auth.Username, value), nil
	case OAuthClientCredentials, TokenExchange, GoogleIDToken:
		if auth.Type == TokenExchange && (t.SubjectToken == "" || t.PrincipalID == "" || (!t.SubjectExpiresAt.IsZero() && !p.clock().Before(t.SubjectExpiresAt))) {
			// Fail closed: never fall back to a shared or service credential.
			return Credential{}, fail("subject_token_unavailable")
		}
		return p.cached(ctx, auth, t, origin)
	}
	return Credential{}, fail("unsupported_auth_type")
}

// Invalidate drops a cached token after the upstream rejected it (HTTP 401).
// A newer token cached by a concurrent dispatch is kept.
func (p *Provider) Invalidate(auth Auth, t Target, rejected Credential) {
	if p == nil {
		return
	}
	key := cacheKey(auth, t)
	p.mu.Lock()
	defer p.mu.Unlock()
	if current, ok := p.cache[key]; ok && current.credential.value == rejected.value {
		delete(p.cache, key)
	}
}

// ResolveSecret returns the value of a tenant secret reference for a caller
// that presents it itself (a request-hook sidecar's bearer credential).
// Errors never contain the value.
func (p *Provider) ResolveSecret(ctx context.Context, tenant, ref string) (string, error) {
	if p == nil {
		return "", fail("provider_unavailable")
	}
	return p.secret(ctx, tenant, ref)
}

func (p *Provider) secret(ctx context.Context, tenant, ref string) (string, error) {
	if p.secrets == nil {
		return "", fail("secret_storage_not_configured")
	}
	if !ValidSecretRef(ref) {
		return "", fail("invalid_secret_reference")
	}
	value, err := p.secrets.Resolve(ctx, tenant, ref)
	if err != nil {
		return "", fail("secret_unavailable")
	}
	value = strings.TrimRight(value, "\r\n")
	if !headerValue(value) {
		return "", fail("secret_value_invalid")
	}
	return value, nil
}

func (p *Provider) cached(ctx context.Context, auth Auth, t Target, origin string) (Credential, error) {
	key := cacheKey(auth, t)
	p.mu.Lock()
	if current, ok := p.cache[key]; ok {
		if p.clock().Before(current.refreshAt) {
			p.mu.Unlock()
			return current.credential, nil
		}
		delete(p.cache, key)
	}
	if running, ok := p.flights[key]; ok {
		p.mu.Unlock()
		select {
		case <-running.done:
			return running.credential, running.err
		case <-ctx.Done():
			return Credential{}, fail("credential_acquisition_cancelled")
		}
	}
	current := &flight{done: make(chan struct{})}
	p.flights[key] = current
	p.mu.Unlock()

	// The shared acquisition is detached from one caller's cancellation so
	// that waiting dispatches are not failed by it; it is still bounded.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.timeout)
	credential, lifetime, err := p.fetch(fetchCtx, auth, t, origin)
	cancel()

	p.mu.Lock()
	delete(p.flights, key)
	if err == nil {
		issued := p.clock()
		skew := p.skew
		if lifetime/2 < skew {
			skew = lifetime / 2
		}
		refreshAt := issued.Add(lifetime - skew)
		if auth.Type == TokenExchange && !t.SubjectExpiresAt.IsZero() && t.SubjectExpiresAt.Before(refreshAt) {
			refreshAt = t.SubjectExpiresAt
		}
		if lifetime > 0 && refreshAt.After(issued) {
			p.evictLocked()
			p.cache[key] = entry{credential: credential, refreshAt: refreshAt}
		}
	}
	current.credential, current.err = credential, err
	close(current.done)
	p.mu.Unlock()
	return credential, err
}

func (p *Provider) evictLocked() {
	if len(p.cache) < p.max {
		return
	}
	now := p.clock()
	for key, value := range p.cache {
		if !now.Before(value.refreshAt) {
			delete(p.cache, key)
		}
	}
	for key := range p.cache {
		if len(p.cache) < p.max {
			return
		}
		delete(p.cache, key)
	}
}

func (p *Provider) fetch(ctx context.Context, auth Auth, t Target, origin string) (Credential, time.Duration, error) {
	if auth.Type == GoogleIDToken {
		token, err := p.idTokens.IDToken(ctx, t.TenantID, auth.IdentityRef, auth.Audience)
		if err != nil {
			if typed, ok := err.(*Error); ok {
				return Credential{}, 0, typed
			}
			return Credential{}, 0, fail("google_id_token_unavailable")
		}
		if !headerValue(token) || strings.ContainsAny(token, " ") {
			return Credential{}, 0, fail("google_id_token_invalid")
		}
		lifetime := time.Duration(0)
		if exp, ok := jwtExpiry(token); ok {
			lifetime = exp.Sub(p.clock())
		}
		return bearerCredential(origin, token), lifetime, nil
	}
	fields := url.Values{}
	if auth.Type == OAuthClientCredentials {
		fields.Set("grant_type", "client_credentials")
	} else {
		subjectType := auth.SubjectTokenType
		if subjectType == "" {
			subjectType = TokenTypeJWT
		}
		requested := auth.RequestedTokenType
		if requested == "" {
			requested = TokenTypeAccessToken
		}
		fields.Set("grant_type", grantTokenExchange)
		fields.Set("subject_token", t.SubjectToken)
		fields.Set("subject_token_type", subjectType)
		fields.Set("requested_token_type", requested)
		if auth.Audience != "" {
			fields.Set("audience", auth.Audience)
		}
	}
	if len(auth.Scopes) > 0 {
		fields.Set("scope", strings.Join(auth.Scopes, " "))
	}
	if auth.Resource != "" {
		fields.Set("resource", auth.Resource)
	}
	secret := ""
	if auth.ClientSecretRef != "" {
		value, err := p.secret(ctx, t.TenantID, auth.ClientSecretRef)
		if err != nil {
			return Credential{}, 0, err
		}
		secret = value
	}
	if auth.ClientID != "" && (auth.ClientAuthMethod != ClientSecretBasic || secret == "") {
		fields.Set("client_id", auth.ClientID)
	}
	if auth.ClientAuthMethod == ClientSecretPost {
		fields.Set("client_secret", secret)
	}
	if p.policy.ValidateURL(auth.TokenEndpoint) != nil {
		return Credential{}, 0, fail("token_endpoint_denied")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, auth.TokenEndpoint, strings.NewReader(fields.Encode()))
	if err != nil {
		return Credential{}, 0, fail("token_request_invalid")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if auth.ClientAuthMethod == ClientSecretBasic {
		// RFC 6749 section 2.3.1 form-encodes both members before Basic encoding.
		req.SetBasicAuth(url.QueryEscape(auth.ClientID), url.QueryEscape(secret))
	}
	response, err := p.client.Do(req)
	if err != nil {
		return Credential{}, 0, fail("token_endpoint_unreachable")
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if response.StatusCode != http.StatusOK {
		// The error body is deliberately discarded: it can echo submitted
		// secrets or subject tokens.
		return Credential{}, 0, fail("token_request_rejected")
	}
	if readErr != nil || len(body) > 1<<20 {
		return Credential{}, 0, fail("token_response_invalid")
	}
	var wire struct {
		AccessToken     string `json:"access_token"`
		TokenType       string `json:"token_type"`
		ExpiresIn       *int64 `json:"expires_in"`
		IssuedTokenType string `json:"issued_token_type"`
	}
	if json.Unmarshal(body, &wire) != nil || wire.AccessToken == "" || len(wire.AccessToken) > 16000 || !headerValue(wire.AccessToken) || strings.Contains(wire.AccessToken, " ") || !strings.EqualFold(wire.TokenType, "Bearer") {
		return Credential{}, 0, fail("token_response_invalid")
	}
	if auth.Type == TokenExchange && wire.IssuedTokenType != "" && wire.IssuedTokenType != TokenTypeAccessToken && wire.IssuedTokenType != TokenTypeJWT {
		return Credential{}, 0, fail("token_response_invalid")
	}
	lifetime := time.Duration(0)
	if wire.ExpiresIn != nil {
		if *wire.ExpiresIn < 1 || *wire.ExpiresIn > 86400 {
			return Credential{}, 0, fail("token_response_invalid")
		}
		lifetime = time.Duration(*wire.ExpiresIn) * time.Second
	}
	return bearerCredential(origin, wire.AccessToken), lifetime, nil
}
