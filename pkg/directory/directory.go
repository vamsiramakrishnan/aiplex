// Package directory resolves a federated user's group membership from the
// identity provider's own directory (Microsoft Graph, Google Cloud Identity,
// Okta) as a membership.Authority.
//
// Every adapter returns either a complete, transitively expanded view with
// source-qualified group IDs and real observation/expiry times, or an error.
// Pagination failures, truncated or malformed pages, throttling, permission
// errors, unknown users and oversized memberships are all errors: a partial
// list is never returned, because a missing group can silently drop a
// mandatory deny or eligibility constraint.
//
// Credentials are references only (tenant-scoped secret references or identity
// profile names). Resolved secrets and directory access tokens are sent only to
// the adapter's pinned origins, are never part of an error and are never logged:
// this package does not log at all. Error reasons are fixed strings that never
// include directory response bodies.
package directory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/membership"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

// Directory kinds. They are part of the public API contract.
const (
	KindEntraGraph          = "entra-graph"
	KindGoogleCloudIdentity = "google-cloud-identity"
	KindOkta                = "okta"
)

// Modes.
//
//	fallback  the directory is queried only when the verified token carries no
//	          usable group view (claim missing, null, malformed or overage).
//	always    the directory is the single group authority; token groups are
//	          ignored even when present, and never used when the directory fails.
const (
	ModeFallback = "fallback"
	ModeAlways   = "always"
)

// Okta group identifiers.
const (
	GroupIDObjectID = "id"
	GroupIDName     = "name"
)

// Spec is the optional `directory` block of a user identity provider.
type Spec struct {
	Kind       string     `json:"kind"`
	Mode       string     `json:"mode,omitempty"`
	Credential Credential `json:"credential"`
	// OrgURL is the Okta org URL (https://{yourOktaDomain}) that serves the
	// management API. It is an explicit operator setting: AIPlex does not derive
	// it from the token issuer (custom authorization servers and custom domains
	// make that guess unsafe).
	OrgURL string `json:"org_url,omitempty"`
	// GroupID selects the Okta group identifier: "id" (immutable group ID,
	// default) or "name" (profile.name, which is what Okta's groups claim
	// carries by default). Okta only.
	GroupID string `json:"group_id,omitempty"`
}

// Credential holds references only, never secret values.
//
//	entra-graph            client_id + client_secret_ref (app registration with an application permission)
//	google-cloud-identity  identity_ref (a gcp-adc identity profile of the AIPlex deployment)
//	okta                   api_token_ref, or client_id + private_key_ref [+ key_id] (OAuth service app)
type Credential struct {
	ClientID        string `json:"client_id,omitempty"`
	ClientSecretRef string `json:"client_secret_ref,omitempty"`
	IdentityRef     string `json:"identity_ref,omitempty"`
	APITokenRef     string `json:"api_token_ref,omitempty"`
	PrivateKeyRef   string `json:"private_key_ref,omitempty"`
	KeyID           string `json:"key_id,omitempty"`
}

var (
	guid       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	profileRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	opaqueID   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// Normalize validates the block's shape and fills defaults. Preset-specific
// rules (which kind fits which preset, which mode fits the group source) are
// applied by the provider.
func (s Spec) Normalize() (Spec, error) {
	if s.Mode == "" {
		s.Mode = ModeFallback
	}
	if s.Mode != ModeFallback && s.Mode != ModeAlways {
		return Spec{}, fmt.Errorf("directory.mode must be %q or %q", ModeFallback, ModeAlways)
	}
	c := s.Credential
	present := map[string]bool{"client_id": c.ClientID != "", "client_secret_ref": c.ClientSecretRef != "", "identity_ref": c.IdentityRef != "", "api_token_ref": c.APITokenRef != "", "private_key_ref": c.PrivateKeyRef != "", "key_id": c.KeyID != ""}
	allow := func(kind string, names ...string) error {
		allowed := map[string]bool{}
		for _, n := range names {
			allowed[n] = true
		}
		var extra []string
		for name, set := range present {
			if set && !allowed[name] {
				extra = append(extra, name)
			}
		}
		sort.Strings(extra)
		if len(extra) > 0 {
			return fmt.Errorf("directory.credential.%s is not used by directory kind %s", strings.Join(extra, ", directory.credential."), kind)
		}
		return nil
	}
	secretRef := func(field, value string) error {
		if !toolcredential.ValidSecretRef(value) {
			return fmt.Errorf("directory.credential.%s must be a secret reference name in the tenant's secret namespace, not a secret value", field)
		}
		return nil
	}
	if s.Kind != KindOkta && (s.OrgURL != "" || s.GroupID != "") {
		return Spec{}, fmt.Errorf("directory.org_url and directory.group_id apply to directory kind %s only", KindOkta)
	}
	switch s.Kind {
	case KindEntraGraph:
		if err := allow(s.Kind, "client_id", "client_secret_ref"); err != nil {
			return Spec{}, err
		}
		if !guid.MatchString(c.ClientID) {
			return Spec{}, fmt.Errorf("directory.credential.client_id must be the lower-case application (client) ID GUID of the Entra app registration that reads group membership")
		}
		if err := secretRef("client_secret_ref", c.ClientSecretRef); err != nil {
			return Spec{}, err
		}
	case KindGoogleCloudIdentity:
		if err := allow(s.Kind, "identity_ref"); err != nil {
			return Spec{}, err
		}
		if !profileRef.MatchString(c.IdentityRef) {
			return Spec{}, fmt.Errorf("directory.credential.identity_ref must name a gcp-adc identity profile configured for this tenant")
		}
	case KindOkta:
		if err := allow(s.Kind, "api_token_ref", "client_id", "private_key_ref", "key_id"); err != nil {
			return Spec{}, err
		}
		switch {
		case c.APITokenRef != "" && (c.ClientID != "" || c.PrivateKeyRef != "" || c.KeyID != ""):
			return Spec{}, fmt.Errorf("directory.credential: use either api_token_ref or client_id + private_key_ref, not both")
		case c.APITokenRef != "":
			if err := secretRef("api_token_ref", c.APITokenRef); err != nil {
				return Spec{}, err
			}
		case c.ClientID != "" && c.PrivateKeyRef != "":
			if !opaqueID.MatchString(c.ClientID) {
				return Spec{}, fmt.Errorf("directory.credential.client_id must be the client ID of the Okta service app")
			}
			if err := secretRef("private_key_ref", c.PrivateKeyRef); err != nil {
				return Spec{}, err
			}
			if c.KeyID != "" && !opaqueID.MatchString(c.KeyID) {
				return Spec{}, fmt.Errorf("directory.credential.key_id must be the key ID registered on the service app")
			}
		default:
			return Spec{}, fmt.Errorf("directory.credential for okta requires api_token_ref, or client_id + private_key_ref")
		}
		origin, err := orgOrigin(s.OrgURL)
		if err != nil {
			return Spec{}, err
		}
		s.OrgURL = origin
		if s.GroupID == "" {
			s.GroupID = GroupIDObjectID
		}
		if s.GroupID != GroupIDObjectID && s.GroupID != GroupIDName {
			return Spec{}, fmt.Errorf("directory.group_id must be %q or %q", GroupIDObjectID, GroupIDName)
		}
	case "":
		return Spec{}, fmt.Errorf("directory.kind is required (%s, %s or %s)", KindEntraGraph, KindGoogleCloudIdentity, KindOkta)
	default:
		return Spec{}, fmt.Errorf("directory.kind must be %s, %s or %s", KindEntraGraph, KindGoogleCloudIdentity, KindOkta)
	}
	return s, nil
}

func orgOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if raw == "" || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, " \t\r\n\\") {
		return "", fmt.Errorf("directory.org_url must be the https Okta org URL without a path, for example https://acme.okta.com")
	}
	return "https://" + strings.ToLower(u.Host), nil
}

// Error is a directory failure. Reason is a fixed, operator-facing explanation
// that never contains a directory response body, a credential or a token.
type Error struct{ Reason string }

func (e *Error) Error() string { return "directory lookup failed: " + e.Reason }

// Unwrap makes every directory failure an unavailable membership view.
func (e *Error) Unwrap() error { return membership.ErrUnavailable }

func fail(format string, args ...any) error { return &Error{Reason: fmt.Sprintf(format, args...)} }

// Reason returns the safe operator-facing reason of err.
func Reason(err error) string {
	var de *Error
	switch {
	case err == nil:
		return ""
	case errors.As(err, &de):
		return de.Reason
	case errors.Is(err, membership.ErrInvalidEvidence):
		return "the directory returned an invalid membership view"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "the directory lookup was cancelled or timed out"
	default:
		return "the directory is unavailable"
	}
}

// SecretResolver resolves a tenant-scoped secret reference (for example
// connections.ToolSecrets).
type SecretResolver interface {
	Resolve(ctx context.Context, tenant, ref string) (string, error)
}

// GoogleTokenSource returns an OAuth access token for a tenant's identity
// profile through the deployment's existing Google credential acquisition
// (connections.Runtime.Token, Application Default Credentials).
type GoogleTokenSource func(ctx context.Context, tenant, identityRef string) (string, error)

// Endpoints are the vendor origins. Production uses the defaults; tests point
// them at local servers.
type Endpoints struct {
	EntraLogin    string
	Graph         string
	CloudIdentity string
}

// DefaultEndpoints are the public vendor origins.
var DefaultEndpoints = Endpoints{EntraLogin: "https://login.microsoftonline.com", Graph: "https://graph.microsoft.com", CloudIdentity: "https://cloudidentity.googleapis.com"}

// Deps are the deployment facilities adapters use.
type Deps struct {
	// HTTP must be the outbound endpoint-policy client. Redirects are never followed.
	HTTP        *http.Client
	Secrets     SecretResolver
	GoogleToken GoogleTokenSource
	// CheckDestination optionally binds an identity profile to the origin it is
	// used for (connections.Runtime.CheckDestination).
	CheckDestination func(tenant, identityRef, origin string) error
	Clock            func() time.Time
	// MaxAge is the lifetime given to an observation (at most five minutes).
	MaxAge    time.Duration
	Endpoints Endpoints
}

// Target binds an authority to one provider of one AIPlex tenant.
type Target struct {
	Tenant string
	Issuer string
	PoolID string
	// EntraTenant is the provider's pinned tid (entra-graph).
	EntraTenant string
	// Domain is the provider's pinned Workspace domain (google-cloud-identity).
	Domain string
}

// Authority implements membership.Authority for one provider directory.
type Authority struct {
	spec      Spec
	target    Target
	deps      Deps
	client    *http.Client
	name      string
	source    string
	digest    string
	endpoints Endpoints
	mu        sync.Mutex
	token     string
	tokenEnds time.Time
}

const (
	maxBody   = 4 << 20
	maxPages  = 100
	maxGroups = 1024
)

// New builds the authority for spec (already normalized) and target.
func New(spec Spec, target Target, deps Deps) (*Authority, error) {
	spec, err := spec.Normalize()
	if err != nil {
		return nil, err
	}
	if deps.HTTP == nil {
		return nil, fmt.Errorf("directory lookups require the outbound endpoint policy client")
	}
	if !membership.Text(target.Tenant) || !membership.Text(target.Issuer) || !membership.Text(target.PoolID) {
		return nil, fmt.Errorf("directory target requires tenant, issuer and pool")
	}
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.MaxAge == 0 {
		deps.MaxAge = 5 * time.Minute
	}
	if deps.MaxAge < time.Second || deps.MaxAge > 5*time.Minute {
		return nil, fmt.Errorf("directory observation lifetime must be between 1s and 5m")
	}
	endpoints := DefaultEndpoints
	if deps.Endpoints.EntraLogin != "" {
		endpoints.EntraLogin = deps.Endpoints.EntraLogin
	}
	if deps.Endpoints.Graph != "" {
		endpoints.Graph = deps.Endpoints.Graph
	}
	if deps.Endpoints.CloudIdentity != "" {
		endpoints.CloudIdentity = deps.Endpoints.CloudIdentity
	}
	a := &Authority{spec: spec, target: target, deps: deps, name: "directory:" + spec.Kind, endpoints: endpoints}
	switch spec.Kind {
	case KindEntraGraph:
		if !guid.MatchString(target.EntraTenant) {
			return nil, fmt.Errorf("entra-graph directory requires the provider's pinned tenant ID")
		}
		if deps.Secrets == nil {
			return nil, fmt.Errorf("entra-graph directory requires secret storage for the client secret")
		}
		a.source = KindEntraGraph + ":" + target.EntraTenant
	case KindGoogleCloudIdentity:
		if !membership.Text(target.Domain) {
			return nil, fmt.Errorf("google-cloud-identity directory requires the provider's pinned Workspace domain")
		}
		if deps.GoogleToken == nil {
			return nil, fmt.Errorf("google-cloud-identity directory requires Google credential acquisition (gcp-adc identity profiles)")
		}
		a.source = KindGoogleCloudIdentity + ":" + target.Domain
	case KindOkta:
		if deps.Secrets == nil {
			return nil, fmt.Errorf("okta directory requires secret storage for its credential")
		}
		a.source = KindOkta + ":" + strings.TrimPrefix(spec.OrgURL, "https://")
	}
	c := *deps.HTTP
	if c.Timeout == 0 || c.Timeout > 10*time.Second {
		c.Timeout = 10 * time.Second
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	a.client = &c
	a.digest = membership.Digest(struct {
		Spec      Spec
		Target    Target
		MaxAge    time.Duration
		Endpoints Endpoints
	}{spec, target, deps.MaxAge, endpoints})
	return a, nil
}

// ConfigurationDigest implements membership.Authority.
func (a *Authority) ConfigurationDigest() string { return a.digest }

// Name is the authority name recorded in snapshots ("directory:<kind>").
func (a *Authority) Name() string { return a.name }

// Source is the group source qualifying every group ID of this directory.
func (a *Authority) Source() string { return a.source }

// Kind is the directory kind.
func (a *Authority) Kind() string { return a.spec.Kind }

type lookupKey struct{}

// WithLookupKey attaches a verified directory lookup key that differs from the
// query's native subject. google-cloud-identity needs the user's verified
// primary email because Cloud Identity keys members by email address while the
// AIPlex identity (and so the cache key) stays the immutable Google subject.
func WithLookupKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, lookupKey{}, key)
}

func lookupFrom(ctx context.Context) string {
	key, _ := ctx.Value(lookupKey{}).(string)
	return key
}

// Resolve implements membership.Authority. Only principal queries for human
// subjects of the bound tenant, issuer and pool are answered.
func (a *Authority) Resolve(ctx context.Context, q membership.Query, at time.Time) (membership.Snapshot, error) {
	if q.Validate() != nil || q.TenantID != a.target.Tenant || q.Issuer != a.target.Issuer || q.PoolID != a.target.PoolID || q.Role != "principal" || q.Kind != "human" {
		return membership.Snapshot{}, membership.ErrInvalidEvidence
	}
	// Observation time is taken before the first request: the view can only be
	// newer than this, so freshness is never overstated.
	start := a.deps.Clock().UTC()
	var ids []string
	var err error
	switch a.spec.Kind {
	case KindEntraGraph:
		ids, err = a.entraGroups(ctx, q.NativeSubject)
	case KindGoogleCloudIdentity:
		ids, err = a.googleGroups(ctx, lookupFrom(ctx))
	case KindOkta:
		ids, err = a.oktaGroups(ctx, q.NativeSubject)
	default:
		err = fail("directory kind is not configured")
	}
	if err != nil {
		return membership.Snapshot{}, err
	}
	sort.Strings(ids)
	groups := make([]membership.Group, 0, len(ids))
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			continue
		}
		if !membership.Text(id) {
			return membership.Snapshot{}, fail("the directory returned a malformed group identifier")
		}
		groups = append(groups, membership.Group{Source: a.source, ID: id})
	}
	if len(groups) > maxGroups {
		return membership.Snapshot{}, fail("the user is a member of more than %d groups; AIPlex refuses a truncated view", maxGroups)
	}
	stamp := membership.Digest(struct {
		Q      membership.Query
		Groups []membership.Group
		At     time.Time
	}{q, groups, start})
	snapshot := membership.Snapshot{Version: membership.Version, Query: q, Authority: a.name, Ref: contracts.SnapshotRef{ID: a.name + ":" + a.source, Version: stamp}, Groups: groups, Complete: true, Expanded: true, IssuedAt: start, ExpiresAt: start.Add(a.deps.MaxAge)}
	checked := a.deps.Clock().UTC()
	if at.After(checked) {
		checked = at
	}
	if err := membership.Validate(snapshot, q, a.name, []string{a.source}, checked, a.deps.MaxAge); err != nil {
		return membership.Snapshot{}, err
	}
	return membership.Clone(snapshot), nil
}

func origin(raw string) (string, error) { return toolcredential.Origin(raw) }

// get performs one bounded GET against the pinned origin. authorize adds the
// credential; it is called only after the origin check.
func (a *Authority) get(ctx context.Context, raw, pinned string, authorize func(*http.Request), accept string) (http.Header, []byte, error) {
	got, err := origin(raw)
	want, werr := origin(pinned)
	if err != nil || werr != nil || got != want {
		return nil, nil, fail("a pagination link pointed outside the pinned directory origin")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, fail("could not build the directory request")
	}
	req.Header.Set("Accept", accept)
	authorize(req)
	return a.do(req)
}

func (a *Authority) do(req *http.Request) (http.Header, []byte, error) {
	resp, err := a.client.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, nil, fail("the directory lookup was cancelled or timed out")
		}
		return nil, nil, fail("the directory is unreachable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, nil, fail("the directory throttled the lookup (HTTP 429); membership stays unavailable until a later request succeeds")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, nil, fail("the directory denied the lookup (HTTP %d); check the permission granted to the configured credential", resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil, fail("the directory does not know this user (HTTP 404)")
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, nil, fail("the directory answered with a redirect (HTTP %d), which is never followed", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, nil, fail("the directory lookup failed (HTTP %d)", resp.StatusCode)
	}
	if err != nil {
		return nil, nil, fail("the directory response was cut off")
	}
	if len(body) > maxBody {
		return nil, nil, fail("the directory response exceeded %d bytes", maxBody)
	}
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return nil, nil, fail("the directory response is not JSON")
	}
	return resp.Header, body, nil
}

// decode parses a complete JSON document. A truncated or trailing body fails.
func decode(body []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(into); err != nil {
		return fail("a directory page was truncated or malformed")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fail("a directory page was truncated or malformed")
	}
	return nil
}

// cachedToken returns a directory access token, refreshing it with fetch.
func (a *Authority) cachedToken(ctx context.Context, fetch func(context.Context) (string, time.Duration, error)) (string, error) {
	a.mu.Lock()
	if a.token != "" && a.deps.Clock().Before(a.tokenEnds) {
		token := a.token
		a.mu.Unlock()
		return token, nil
	}
	a.mu.Unlock()
	token, lifetime, err := fetch(ctx)
	if err != nil {
		return "", err
	}
	if !headerSafe(token) {
		return "", fail("the token endpoint returned an unusable access token")
	}
	a.mu.Lock()
	a.token = token
	a.tokenEnds = a.deps.Clock().Add(lifetime - time.Minute)
	a.mu.Unlock()
	return token, nil
}

func headerSafe(v string) bool {
	if v == "" || len(v) > 16384 {
		return false
	}
	for _, r := range v {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// postToken posts a client-credentials form to a pinned token endpoint.
func (a *Authority) postToken(ctx context.Context, endpoint string, form url.Values) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fail("could not build the token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	_, body, err := a.do(req)
	if err != nil {
		var de *Error
		if errors.As(err, &de) {
			return "", 0, fail("the token endpoint rejected the directory credential: %s", de.Reason)
		}
		return "", 0, err
	}
	var tr tokenResponse
	if decode(body, &tr) != nil {
		return "", 0, fail("the token endpoint returned a malformed response")
	}
	switch {
	case strings.EqualFold(tr.TokenType, "DPoP"):
		return "", 0, fail("the token endpoint issued a DPoP-bound token, which this adapter does not support")
	case !strings.EqualFold(tr.TokenType, "Bearer"):
		return "", 0, fail("the token endpoint did not issue a bearer token")
	case tr.ExpiresIn <= 60:
		return "", 0, fail("the token endpoint issued a token without a usable lifetime")
	}
	return tr.AccessToken, time.Duration(tr.ExpiresIn) * time.Second, nil
}

func (a *Authority) secret(ctx context.Context, ref string) (string, error) {
	value, err := a.deps.Secrets.Resolve(ctx, a.target.Tenant, ref)
	if err != nil || value == "" {
		return "", fail("the directory credential secret reference %q could not be resolved in the tenant's secret namespace", ref)
	}
	return value, nil
}

