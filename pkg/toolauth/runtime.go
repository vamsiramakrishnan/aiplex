package toolauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// Owner is obtained from the host's verified identity, never from an OAuth
// callback. Delegated actors have separate grants even for the same human.
type Owner struct {
	TenantID    string `json:"tenant_id"`
	PrincipalID string `json:"principal_id"`
	ActorID     string `json:"actor_id,omitempty"`
}
type SessionKey struct {
	Owner              Owner  `json:"owner"`
	ConnectionID       string `json:"connection_id"`
	ConnectionRevision uint64 `json:"connection_revision"`
	Resource           string `json:"resource"`
	Issuer             string `json:"issuer"`
	ProfileID          string `json:"profile_id"`
}

// SessionSecrets must only cross the host-owned encrypted vault boundary.
// None of its values can be serialized into a public resource or API response.
type SessionSecrets struct {
	Verifier     string `json:"-"`
	AccessToken  string `json:"-"`
	RefreshToken string `json:"-"`
}

func (SessionSecrets) String() string   { return "OAuthSessionSecrets(redacted)" }
func (SessionSecrets) GoString() string { return "OAuthSessionSecrets(redacted)" }

type SessionVault interface {
	Seal(context.Context, string, SessionSecrets) (string, error)
	Open(context.Context, string, string) (SessionSecrets, error)
	Destroy(context.Context, string) error
}
type RuntimeConfig struct {
	Registration                              *Service
	Store                                     resources.Store
	Vault                                     SessionVault
	Clock                                     func() time.Time
	AuthorizationTTL, SessionTTL, RefreshSkew time.Duration
}
type Runtime struct {
	registration                              *Service
	store                                     resources.Store
	vault                                     SessionVault
	clock                                     func() time.Time
	authorizationTTL, sessionTTL, refreshSkew time.Duration
}
type RuntimeError struct{ Code string }

func (e *RuntimeError) Error() string { return "tool OAuth: " + e.Code }
func runtimeError(code string) error  { return &RuntimeError{Code: code} }

type AuthorizationStart struct {
	Key          SessionKey
	Registration Registration
	RedirectURI  string
	Scopes       []string
}
type AuthorizationChallenge struct {
	FlowID           string    `json:"flow_id"`
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}
type AuthorizationResponse struct {
	FlowID string `json:"flow_id"`
	State  string `json:"state"`
	Code   string `json:"code"`
	Issuer string `json:"iss,omitempty"`
}

// SessionInfo is safe metadata. Token values and vault references are omitted.
type SessionInfo struct {
	Key             SessionKey `json:"binding"`
	GrantID         string     `json:"grant_id"`
	Status          string     `json:"status"`
	Scopes          []string   `json:"scopes"`
	CreatedAt       time.Time  `json:"created_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	AccessExpiresAt time.Time  `json:"access_expires_at"`
}
type authorizationRecord struct {
	Key                SessionKey   `json:"binding"`
	Registration       Registration `json:"registration"`
	RedirectURI        string       `json:"redirect_uri"`
	Scopes             []string     `json:"scopes"`
	StateHash          string       `json:"state_hash"`
	SecretRef          string       `json:"secret_ref"`
	Status             string       `json:"status"`
	ExpiresAt          time.Time    `json:"expires_at"`
	SessionRevision    uint64       `json:"session_revision"`
	PreviousSecretRefs []string     `json:"previous_secret_refs,omitempty"`
}
type sessionRecord struct {
	SessionInfo
	Registration Registration `json:"registration"`
	SecretRef    string       `json:"secret_ref"`
	CleanupRefs  []string     `json:"cleanup_refs,omitempty"`
}

func NewRuntime(c RuntimeConfig) (*Runtime, error) {
	if c.Registration == nil || !runtimeDependencyPresent(c.Store) || !runtimeDependencyPresent(c.Vault) {
		return nil, runtimeError("runtime_dependencies_missing")
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	if c.AuthorizationTTL == 0 {
		c.AuthorizationTTL = 5 * time.Minute
	}
	if c.SessionTTL == 0 {
		c.SessionTTL = 30 * 24 * time.Hour
	}
	if c.RefreshSkew == 0 {
		c.RefreshSkew = 30 * time.Second
	}
	if c.AuthorizationTTL < time.Minute || c.AuthorizationTTL > 15*time.Minute || c.SessionTTL < time.Minute || c.SessionTTL > 90*24*time.Hour || c.RefreshSkew < 0 || c.RefreshSkew > 5*time.Minute {
		return nil, runtimeError("invalid_lifetime")
	}
	return &Runtime{registration: c.Registration, store: c.Store, vault: c.Vault, clock: c.Clock, authorizationTTL: c.AuthorizationTTL, sessionTTL: c.SessionTTL, refreshSkew: c.RefreshSkew}, nil
}

func runtimeDependencyPresent(value any) bool {
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
func boundedIdentity(s string, optional bool) bool {
	if optional && s == "" {
		return true
	}
	if s == "" || len(s) > 1024 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
func (k SessionKey) validate() error {
	if !boundedIdentity(k.Owner.TenantID, false) || !boundedIdentity(k.Owner.PrincipalID, false) || !boundedIdentity(k.Owner.ActorID, true) || !boundedIdentity(k.ConnectionID, false) || k.ConnectionRevision == 0 || !boundedIdentity(k.ProfileID, false) || validateURL(k.Resource, true) != nil || validateURL(k.Issuer, true) != nil {
		return runtimeError("invalid_owner_binding")
	}
	return nil
}
func sessionHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func sessionID(k SessionKey) string {
	return sessionHash(struct {
		Owner      Owner
		Connection string
	}{k.Owner, k.ConnectionID})
}
func sessionVaultBinding(k SessionKey, grant string) string {
	return sessionHash(struct {
		Key   SessionKey
		Grant string
	}{k, grant})
}
func randomValue() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func validScopes(scopes []string) ([]string, error) {
	if len(scopes) > 128 {
		return nil, runtimeError("invalid_scopes")
	}
	out := append([]string{}, scopes...)
	sort.Strings(out)
	for i, v := range out {
		if !boundedIdentity(v, false) || strings.ContainsAny(v, " \t\r\n") || (i > 0 && v == out[i-1]) {
			return nil, runtimeError("invalid_scopes")
		}
	}
	return out, nil
}
func sameKey(a, b SessionKey) bool { return a == b }
func (r *Runtime) loadSession(ctx context.Context, k SessionKey) (resources.Record, sessionRecord, error) {
	if e := k.validate(); e != nil {
		return resources.Record{}, sessionRecord{}, e
	}
	rec, e := r.store.Get(ctx, k.Owner.TenantID, "tool-oauth-session", sessionID(k))
	if e != nil {
		return resources.Record{}, sessionRecord{}, e
	}
	var s sessionRecord
	if json.Unmarshal(rec.Spec, &s) != nil || !sameKey(s.Key, k) {
		return resources.Record{}, sessionRecord{}, runtimeError("session_binding_changed")
	}
	return rec, s, nil
}
func encodeResource(tenant, kind, id string, value any) resources.Record {
	b, _ := json.Marshal(value)
	return resources.Record{TenantID: tenant, Kind: kind, ID: id, Spec: b}
}
func (r *Runtime) Begin(ctx context.Context, in AuthorizationStart) (AuthorizationChallenge, error) {
	if e := in.Key.validate(); e != nil {
		return AuthorizationChallenge{}, e
	}
	reg := in.Registration
	if reg.TenantID != in.Key.Owner.TenantID || reg.Resource != in.Key.Resource || reg.Issuer != in.Key.Issuer || reg.ProfileID != in.Key.ProfileID || reg.ClientID == "" || reg.PKCEMethod != "S256" || reg.ResourceParameter != reg.Resource || r.registration.allowedURL(reg.AuthorizationEndpoint) != nil || r.registration.allowedURL(reg.TokenEndpoint) != nil {
		return AuthorizationChallenge{}, runtimeError("registration_binding_invalid")
	}
	if ValidateRedirectURI(in.RedirectURI) != nil || !slices.Contains(reg.RedirectURIs, in.RedirectURI) {
		return AuthorizationChallenge{}, runtimeError("redirect_not_registered")
	}
	scopes, e := validScopes(in.Scopes)
	if e != nil {
		return AuthorizationChallenge{}, e
	}
	sessionRevision := uint64(0)
	var previousSecretRefs []string
	if current, e := r.store.Get(ctx, in.Key.Owner.TenantID, "tool-oauth-session", sessionID(in.Key)); e == nil {
		sessionRevision = current.Revision
		var old sessionRecord
		if json.Unmarshal(current.Spec, &old) != nil || old.Key.Owner != in.Key.Owner || old.Key.ConnectionID != in.Key.ConnectionID {
			return AuthorizationChallenge{}, runtimeError("session_binding_changed")
		}
		previousSecretRefs = append(previousSecretRefs, old.CleanupRefs...)
		if old.SecretRef != "" {
			previousSecretRefs = append(previousSecretRefs, old.SecretRef)
		}
	} else if !errors.Is(e, resources.ErrNotFound) {
		return AuthorizationChallenge{}, runtimeError("session_lookup_failed")
	}
	state, e := randomValue()
	if e != nil {
		return AuthorizationChallenge{}, runtimeError("entropy_unavailable")
	}
	verifier, e := randomValue()
	if e != nil {
		return AuthorizationChallenge{}, runtimeError("entropy_unavailable")
	}
	flow, e := randomValue()
	if e != nil {
		return AuthorizationChallenge{}, runtimeError("entropy_unavailable")
	}
	ref, e := r.vault.Seal(ctx, sessionVaultBinding(in.Key, flow), SessionSecrets{Verifier: verifier})
	if e != nil || ref == "" {
		return AuthorizationChallenge{}, runtimeError("secret_persistence_failed")
	}
	expires := r.clock().UTC().Add(r.authorizationTTL)
	record := authorizationRecord{Key: in.Key, Registration: reg, RedirectURI: in.RedirectURI, Scopes: scopes, StateHash: sessionHash(state), SecretRef: ref, Status: "pending", ExpiresAt: expires, SessionRevision: sessionRevision, PreviousSecretRefs: previousSecretRefs}
	if _, e = r.store.Put(ctx, encodeResource(in.Key.Owner.TenantID, "tool-oauth-flow", flow, record), 0); e != nil {
		_ = r.destroySecret(ctx, ref)
		return AuthorizationChallenge{}, runtimeError("authorization_persistence_failed")
	}
	endpoint, _ := url.Parse(reg.AuthorizationEndpoint)
	q := endpoint.Query()
	challenge := sha256.Sum256([]byte(verifier))
	q.Set("response_type", "code")
	q.Set("client_id", reg.ClientID)
	q.Set("redirect_uri", in.RedirectURI)
	q.Set("resource", reg.Resource)
	q.Set("code_challenge_method", "S256")
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	q.Set("state", state)
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	endpoint.RawQuery = q.Encode()
	return AuthorizationChallenge{FlowID: flow, AuthorizationURL: endpoint.String(), ExpiresAt: expires}, nil
}
func (r *Runtime) Complete(ctx context.Context, key SessionKey, in AuthorizationResponse) (SessionInfo, error) {
	if key.validate() != nil || !boundedIdentity(in.FlowID, false) || len(in.FlowID) > 128 {
		return SessionInfo{}, runtimeError("invalid_callback")
	}
	rec, e := r.store.Get(ctx, key.Owner.TenantID, "tool-oauth-flow", in.FlowID)
	if e != nil {
		return SessionInfo{}, runtimeError("authorization_not_found")
	}
	var flow authorizationRecord
	if json.Unmarshal(rec.Spec, &flow) != nil || !sameKey(flow.Key, key) {
		return SessionInfo{}, runtimeError("authorization_not_found")
	}
	if flow.Status != "pending" || !r.clock().Before(flow.ExpiresAt) {
		return SessionInfo{}, runtimeError("authorization_consumed_or_expired")
	}
	actual := sessionHash(in.State)
	if subtle.ConstantTimeCompare([]byte(actual), []byte(flow.StateHash)) != 1 || in.State == "" || !boundedIdentity(in.Code, false) || len(in.Code) > 8192 || (in.Issuer != "" && in.Issuer != flow.Registration.Issuer) || (flow.Registration.RequireIssuer && in.Issuer == "") {
		return SessionInfo{}, runtimeError("callback_binding_invalid")
	}
	currentSession, sessionErr := r.store.Get(ctx, key.Owner.TenantID, "tool-oauth-session", sessionID(key))
	if (sessionErr == nil && currentSession.Revision != flow.SessionRevision) || (errors.Is(sessionErr, resources.ErrNotFound) && flow.SessionRevision != 0) {
		return SessionInfo{}, runtimeError("session_generation_changed")
	}
	if sessionErr != nil && !errors.Is(sessionErr, resources.ErrNotFound) {
		return SessionInfo{}, runtimeError("session_lookup_failed")
	}
	secrets, e := r.vault.Open(ctx, sessionVaultBinding(key, in.FlowID), flow.SecretRef)
	if e != nil || secrets.Verifier == "" {
		return SessionInfo{}, runtimeError("authorization_secret_unavailable")
	}
	flow.Status = "exchanging"
	claimed, e := r.store.Put(ctx, encodeResource(key.Owner.TenantID, "tool-oauth-flow", in.FlowID, flow), rec.Revision)
	if e != nil {
		return SessionInfo{}, runtimeError("authorization_already_claimed")
	}
	fail := func(code string) (SessionInfo, error) {
		flow.Status = "unknown"
		bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = r.store.Put(bounded, encodeResource(key.Owner.TenantID, "tool-oauth-flow", in.FlowID, flow), claimed.Revision)
		return SessionInfo{}, runtimeError(code)
	}
	fields := url.Values{"grant_type": {"authorization_code"}, "code": {in.Code}, "redirect_uri": {flow.RedirectURI}, "code_verifier": {secrets.Verifier}, "resource": {key.Resource}}
	token, e := r.exchange(ctx, flow.Registration, fields, flow.Scopes, "")
	if e != nil {
		return fail("code_exchange_requires_reauthorization")
	}
	secretRef, e := r.vault.Seal(ctx, sessionVaultBinding(key, in.FlowID), SessionSecrets{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken})
	if e != nil {
		return fail("token_persistence_failed")
	}
	now := r.clock().UTC()
	session := sessionRecord{SessionInfo: SessionInfo{Key: key, GrantID: in.FlowID, Status: "active", Scopes: token.Scopes, CreatedAt: now, ExpiresAt: now.Add(r.sessionTTL), AccessExpiresAt: now.Add(time.Duration(token.ExpiresIn) * time.Second)}, Registration: flow.Registration, SecretRef: secretRef, CleanupRefs: append([]string(nil), flow.PreviousSecretRefs...)}
	if _, e = r.persistSession(ctx, key, session, flow.SessionRevision); e != nil {
		if errors.Is(e, resources.ErrConflict) {
			_ = r.destroySecret(ctx, secretRef)
		}
		return fail("session_generation_changed")
	}
	flow.Status = "complete"
	flow.SecretRef = ""
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = r.store.Put(bounded, encodeResource(key.Owner.TenantID, "tool-oauth-flow", in.FlowID, flow), claimed.Revision)
	_ = r.vault.Destroy(bounded, claimedSecretRef(rec))
	_ = r.cleanupSession(bounded, key)
	return session.SessionInfo, nil
}
func claimedSecretRef(rec resources.Record) string {
	var f authorizationRecord
	_ = json.Unmarshal(rec.Spec, &f)
	return f.SecretRef
}
func (r *Runtime) Info(ctx context.Context, key SessionKey) (SessionInfo, error) {
	_, s, e := r.loadSession(ctx, key)
	if e != nil {
		return SessionInfo{}, e
	}
	if s.Status != "active" || !r.clock().Before(s.ExpiresAt) {
		return SessionInfo{}, runtimeError("authorization_required")
	}
	return s.SessionInfo, nil
}

// Status includes revoked, refreshing and unknown metadata for operational
// inspection. It grants no access to tokens and no permission to dispatch.
func (r *Runtime) Status(ctx context.Context, key SessionKey) (SessionInfo, error) {
	_, s, err := r.loadSession(ctx, key)
	if err != nil {
		return SessionInfo{}, err
	}
	return s.SessionInfo, nil
}
func (r *Runtime) CheckGrant(ctx context.Context, key SessionKey, grant string) error {
	info, e := r.Info(ctx, key)
	if e != nil {
		return e
	}
	if info.GrantID != grant {
		return runtimeError("session_generation_changed")
	}
	return nil
}
func (r *Runtime) AccessToken(ctx context.Context, key SessionKey, grant string) (string, error) {
	rec, s, e := r.loadSession(ctx, key)
	if e != nil {
		return "", runtimeError("authorization_required")
	}
	if s.Status != "active" || s.GrantID != grant || !r.clock().Before(s.ExpiresAt) {
		return "", runtimeError("authorization_required")
	}
	secret, e := r.vault.Open(ctx, sessionVaultBinding(key, s.GrantID), s.SecretRef)
	if e != nil {
		return "", runtimeError("session_secret_unavailable")
	}
	if r.clock().Add(r.refreshSkew).Before(s.AccessExpiresAt) {
		if secret.AccessToken == "" {
			return "", runtimeError("session_secret_unavailable")
		}
		return secret.AccessToken, nil
	}
	if secret.RefreshToken == "" || !s.Registration.AllowRefresh {
		return "", runtimeError("reauthorization_required")
	}
	// Persist the one-shot refresh claim before using a rotating refresh token.
	// A lost response is unknown; another replica must never replay that token.
	s.Status = "refreshing"
	claimed, e := r.store.Put(ctx, encodeResource(key.Owner.TenantID, "tool-oauth-session", sessionID(key), s), rec.Revision)
	if e != nil {
		return "", runtimeError("refresh_already_claimed")
	}
	fail := func(code string) (string, error) {
		s.Status = "unknown"
		bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = r.store.Put(bounded, encodeResource(key.Owner.TenantID, "tool-oauth-session", sessionID(key), s), claimed.Revision)
		return "", runtimeError(code)
	}
	token, e := r.exchange(ctx, s.Registration, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {secret.RefreshToken}, "resource": {key.Resource}}, s.Scopes, secret.RefreshToken)
	if e != nil {
		return fail("refresh_requires_reauthorization")
	}
	ref, e := r.vault.Seal(ctx, sessionVaultBinding(key, s.GrantID), SessionSecrets{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken})
	if e != nil {
		return fail("token_persistence_failed")
	}
	previous := s.SecretRef
	s.SecretRef = ref
	s.Status = "active"
	s.Scopes = token.Scopes
	s.AccessExpiresAt = r.clock().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	s.CleanupRefs = append(s.CleanupRefs, previous)
	if _, e = r.persistSession(ctx, key, s, claimed.Revision); e != nil {
		if errors.Is(e, resources.ErrConflict) {
			_ = r.destroySecret(ctx, ref)
		}
		return "", runtimeError("refresh_acknowledgement_unknown")
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = r.cleanupSession(bounded, key)
	return token.AccessToken, nil
}
func (r *Runtime) Revoke(ctx context.Context, key SessionKey) error {
	rec, s, e := r.loadSession(ctx, key)
	if errors.Is(e, resources.ErrNotFound) {
		// A revocation tombstone fences authorization flows created before the
		// first token exchange too; absence must not reset generation history.
		grant, err := randomValue()
		if err != nil {
			return runtimeError("entropy_unavailable")
		}
		tombstone := sessionRecord{SessionInfo: SessionInfo{Key: key, GrantID: "revocation-" + grant, Status: "revoked", Scopes: []string{}, CreatedAt: r.clock().UTC()}}
		if _, err = r.persistSession(ctx, key, tombstone, 0); err != nil {
			return runtimeError("revocation_conflict")
		}
		return nil
	}
	if e != nil {
		return e
	}
	if s.Status == "revoked" {
		return r.cleanupSession(ctx, key)
	}
	ref := s.SecretRef
	s.Status = "revoked"
	s.SecretRef = ""
	if ref != "" {
		s.CleanupRefs = append(s.CleanupRefs, ref)
	}
	if _, e = r.store.Put(ctx, encodeResource(key.Owner.TenantID, "tool-oauth-session", sessionID(key), s), rec.Revision); e != nil {
		return runtimeError("revocation_conflict")
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return r.cleanupSession(bounded, key)
}

func (r *Runtime) persistSession(ctx context.Context, key SessionKey, value sessionRecord, expected uint64) (resources.Record, error) {
	record, err := r.store.Put(ctx, encodeResource(key.Owner.TenantID, "tool-oauth-session", sessionID(key), value), expected)
	if err == nil {
		return record, nil
	}
	// A committed write with a lost acknowledgement must not cause destruction
	// of the live token version. Prove the exact result with a bounded read.
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	current, saved, readErr := r.loadSession(bounded, key)
	if readErr == nil && sessionHash(saved) == sessionHash(value) {
		return current, nil
	}
	return resources.Record{}, err
}

func (r *Runtime) cleanupSession(ctx context.Context, key SessionKey) error {
	record, value, err := r.loadSession(ctx, key)
	if err != nil {
		return err
	}
	if len(value.CleanupRefs) == 0 {
		return nil
	}
	remaining := make([]string, 0, len(value.CleanupRefs))
	seen := map[string]bool{}
	for _, ref := range value.CleanupRefs {
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		if ref == value.SecretRef || r.vault.Destroy(ctx, ref) != nil {
			remaining = append(remaining, ref)
		}
	}
	value.CleanupRefs = remaining
	if _, err = r.store.Put(ctx, encodeResource(key.Owner.TenantID, "tool-oauth-session", sessionID(key), value), record.Revision); err != nil {
		return runtimeError("secret_cleanup_acknowledgement_pending")
	}
	if len(remaining) != 0 {
		return runtimeError("revoked_secret_cleanup_pending")
	}
	return nil
}

type runtimeToken struct {
	AccessToken, RefreshToken string
	ExpiresIn                 int64
	Scopes                    []string
}

func (r *Runtime) exchange(ctx context.Context, reg Registration, fields url.Values, requested []string, oldRefresh string) (runtimeToken, error) {
	if r.registration.allowedURL(reg.TokenEndpoint) != nil {
		return runtimeToken{}, runtimeError("token_endpoint_denied")
	}
	fields.Set("client_id", reg.ClientID)
	method, secret := "none", ""
	if reg.Mode != CIMD && r.registration.credentials != nil {
		credential, _, err := r.registration.credentials.Get(ctx, CredentialKey{reg.TenantID, reg.Issuer, reg.ProfileID})
		if err == nil {
			if credential.ClientID != reg.ClientID {
				return runtimeToken{}, runtimeError("client_registration_changed")
			}
			method, secret = credential.TokenEndpointAuthMethod, credential.ClientSecret
			if method == "" {
				method = "none"
			}
		} else if !errors.Is(err, ErrCredentialsNotFound) {
			return runtimeToken{}, runtimeError("client_credentials_unavailable")
		}
	}
	if method != "none" && method != "client_secret_basic" && method != "client_secret_post" {
		return runtimeToken{}, runtimeError("unsupported_client_authentication")
	}
	if method != "none" && secret == "" {
		return runtimeToken{}, runtimeError("client_credentials_unavailable")
	}
	if method == "client_secret_post" {
		fields.Set("client_secret", secret)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, reg.TokenEndpoint, strings.NewReader(fields.Encode()))
	if e != nil {
		return runtimeToken{}, runtimeError("token_request_invalid")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if method == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(reg.ClientID), url.QueryEscape(secret))
	}
	response, e := r.registration.client.Do(req)
	if e != nil {
		return runtimeToken{}, runtimeError("token_exchange_unknown")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return runtimeToken{}, runtimeError("token_exchange_rejected")
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if e != nil || len(body) > 1<<20 {
		return runtimeToken{}, runtimeError("token_response_invalid")
	}
	var wire struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		TokenType    string  `json:"token_type"`
		ExpiresIn    int64   `json:"expires_in"`
		Scope        *string `json:"scope"`
		Resource     string  `json:"resource"`
	}
	if json.Unmarshal(body, &wire) != nil || wire.AccessToken == "" || len(wire.AccessToken) > 65536 || strings.ContainsAny(wire.AccessToken, "\r\n\x00") || !strings.EqualFold(wire.TokenType, "Bearer") || wire.ExpiresIn < 1 || wire.ExpiresIn > 86400 || len(wire.RefreshToken) > 65536 || strings.ContainsAny(wire.RefreshToken, "\r\n\x00") || (wire.Resource != "" && wire.Resource != reg.Resource) {
		return runtimeToken{}, runtimeError("token_response_invalid")
	}
	scopes := append([]string{}, requested...)
	if wire.Scope != nil {
		scopes, e = validScopes(strings.Fields(*wire.Scope))
		if e != nil {
			return runtimeToken{}, e
		}
		for _, scope := range scopes {
			if !slices.Contains(requested, scope) {
				return runtimeToken{}, runtimeError("token_scope_escalation")
			}
		}
	}
	if oldRefresh != "" && (wire.RefreshToken == "" || wire.RefreshToken == oldRefresh) {
		return runtimeToken{}, runtimeError("refresh_rotation_required")
	}
	if !reg.AllowRefresh {
		wire.RefreshToken = ""
	}
	return runtimeToken{AccessToken: wire.AccessToken, RefreshToken: wire.RefreshToken, ExpiresIn: wire.ExpiresIn, Scopes: scopes}, nil
}

func (r *Runtime) destroySecret(ctx context.Context, ref string) error {
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return r.vault.Destroy(bounded, ref)
}
