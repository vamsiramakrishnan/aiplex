package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

// toolAnnotationsKind persists gateway-observed MCP tool annotations. They are
// authorization hints recorded only from tools/list responses this gateway
// itself received from the connection's configured origin, never from a
// caller. The record is tenant-scoped, keyed by connection ID and bound to the
// connection revision, so any replica can read it and any configuration
// change invalidates it. tools/call never fetches tools/list itself: a
// read_only call whose readOnlyHint is unknown, stale (older than
// toolAnnotationTTL) or from another revision is denied before quota and
// credentials, and the caller is told to list tools through the gateway. Each
// tools/call therefore stays an independent stateless request; the record is
// authorization data, not an MCP session.
const toolAnnotationsKind = "tool-annotations"

// toolAnnotationTTL bounds how long an observed readOnlyHint authorizes calls
// on a read_only connection before a fresh tools/list is required.
const toolAnnotationTTL = 24 * time.Hour

var toolCredentialsMu sync.Mutex

// validateToolAuth checks the auth and tools blocks of a saved connection.
func (h *ConnectedHandler) validateToolAuth(ctx context.Context, tenant string, c ToolConnection) error {
	if c.Tools != nil {
		if c.Execution == nil {
			return fmt.Errorf("tools policy requires managed execution")
		}
		if e := c.Tools.Validate(); e != nil {
			return e
		}
	}
	mode := ""
	if c.Execution != nil {
		mode = c.Execution.CredentialMode
	}
	if c.Auth == nil {
		if mode == toolexec.ConnectionAuth {
			return fmt.Errorf("execution credential_mode connection-auth requires an auth block")
		}
		return nil
	}
	if e := c.Auth.Validate(); e != nil {
		return e
	}
	if c.Auth.Type == toolcredential.OAuthUser {
		if c.Execution != nil && mode != toolexec.DelegatedOAuth {
			return fmt.Errorf("auth type oauth_user requires execution credential_mode delegated-oauth")
		}
		return nil
	}
	if c.Execution == nil {
		return fmt.Errorf("auth type %s requires managed execution", c.Auth.Type)
	}
	if mode != toolexec.ConnectionAuth {
		return fmt.Errorf("auth type %s requires execution credential_mode connection-auth", c.Auth.Type)
	}
	if c.Issuer != "" || !reflect.DeepEqual(c.Client, toolauth.ClientProfile{}) || c.ClientRegistrationRef != "" {
		return fmt.Errorf("issuer, client and client_registration_ref apply only to oauth_user")
	}
	if h.Runtime == nil {
		return fmt.Errorf("outbound endpoint policy unavailable")
	}
	if c.Auth.TokenEndpoint != "" {
		policy, e := endpointpolicy.New(h.Runtime.Config.Network)
		if e != nil {
			return e
		}
		if e = policy.ValidateURL(c.Auth.TokenEndpoint); e != nil {
			return fmt.Errorf("auth.token_endpoint is not approved by the outbound endpoint policy")
		}
	}
	if c.Auth.Type == toolcredential.GoogleIDToken {
		if e := h.checkIDTokenIdentity(tenant, *c.Auth, c.ResourceURL); e != nil {
			return e
		}
	}
	if refs := c.Auth.SecretRefs(); len(refs) > 0 {
		provider, e := h.toolCredentials()
		if e != nil {
			return fmt.Errorf("tool credential provider unavailable")
		}
		for _, ref := range refs {
			if e = provider.CheckSecret(ctx, tenant, ref); e != nil {
				return e
			}
		}
	}
	return nil
}

func (h *ConnectedHandler) checkIDTokenIdentity(tenant string, auth toolcredential.Auth, resource string) error {
	identity, e := h.Runtime.Identity(tenant, auth.IdentityRef)
	if e != nil || identity.Type != "gcp-adc" {
		return fmt.Errorf("google_id_token requires a tenant identity profile of type gcp-adc")
	}
	if e = h.Runtime.CheckDestination(tenant, auth.IdentityRef, resource); e != nil {
		return fmt.Errorf("google_id_token identity profile does not approve the connection origin")
	}
	return nil
}

// connectionAuthConfiguration re-proves connection-auth prerequisites at
// dispatch and pins identity authority into the quota configuration digest.
func (h *ConnectedHandler) connectionAuthConfiguration(tenant string, connection ToolConnection, result *quotaToolConfiguration) error {
	auth := connection.Auth
	if auth == nil || auth.Type == toolcredential.OAuthUser || auth.Validate() != nil {
		return fmt.Errorf("connection auth is unavailable")
	}
	if auth.TokenEndpoint != "" && h.Runtime.Policy.ValidateURL(auth.TokenEndpoint) != nil {
		return fmt.Errorf("token endpoint is not approved")
	}
	if auth.Type == toolcredential.GoogleIDToken {
		if err := h.checkIDTokenIdentity(tenant, *auth, connection.ResourceURL); err != nil {
			return err
		}
		identity, _ := h.Runtime.Identity(tenant, auth.IdentityRef)
		result.Identity = identity
	}
	if _, err := h.toolCredentials(); err != nil {
		return err
	}
	return nil
}

// toolCredentials returns the configured provider or a default bound to the
// outbound endpoint policy with no secret storage and no ID-token source.
func (h *ConnectedHandler) toolCredentials() (*toolcredential.Provider, error) {
	toolCredentialsMu.Lock()
	defer toolCredentialsMu.Unlock()
	if h.ToolCredentials != nil {
		return h.ToolCredentials, nil
	}
	if h.Runtime == nil || h.Runtime.Policy == nil || h.Runtime.HTTP == nil {
		return nil, fmt.Errorf("tool credential provider unavailable")
	}
	provider, err := toolcredential.NewProvider(toolcredential.Config{HTTPClient: h.Runtime.HTTP, Policy: h.Runtime.Policy})
	if err != nil {
		return nil, err
	}
	h.ToolCredentials = provider
	return provider, nil
}

// verifiedSubjectToken returns the inbound token the request's verified
// identity was established from, using the same selection order as the
// identity middleware. Without a verified identity there is no subject token.
func verifiedSubjectToken(r *http.Request) (string, time.Time) {
	identity := GetWIFIdentity(r.Context())
	if identity == nil || identity.PrincipalID == "" {
		return "", time.Time{}
	}
	token := r.Header.Get("X-Goog-Iap-Jwt-Assertion")
	if token == "" {
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			return "", time.Time{}
		}
		token = strings.TrimPrefix(authorization, "Bearer ")
	}
	expires := time.Time{}
	if identity.VerifiedEvidence != nil {
		expires = identity.VerifiedEvidence.ExpiresAt
	}
	return token, expires
}

func (h *ConnectedHandler) credentialTarget(r *http.Request, tenant string, record resources.Record, connection ToolConnection) toolcredential.Target {
	target := toolcredential.Target{TenantID: tenant, ConnectionID: connection.ID, Revision: record.Revision, Resource: connection.Execution.Endpoint}
	if identity := GetWIFIdentity(r.Context()); identity != nil {
		target.PrincipalID, target.ActorID = identity.PrincipalID, identity.ActorID
	}
	if connection.Auth != nil && connection.Auth.Type == toolcredential.TokenExchange {
		target.SubjectToken, target.SubjectExpiresAt = verifiedSubjectToken(r)
	}
	return target
}

// currentScopes resolves one coherent grant snapshot, exactly as
// authorizeAllCurrent does, for filtering many tools at once.
func (h *ConnectedHandler) currentScopes(ctx context.Context, r *http.Request) ([]string, error) {
	if h.ResolveAccess == nil {
		if h.Quota != nil {
			return nil, fmt.Errorf("current access resolver unavailable")
		}
		access := GetWIFAccess(r.Context())
		if access == nil {
			return nil, fmt.Errorf("action grant required")
		}
		return access.Scopes, nil
	}
	identity := GetWIFIdentity(r.Context())
	if identity == nil {
		return nil, fmt.Errorf("verified identity required")
	}
	access, err := h.ResolveAccess(ctx, identity)
	if err != nil || access == nil || access.Identity.PrincipalID != identity.PrincipalID || access.Identity.ActorID != identity.ActorID {
		return nil, fmt.Errorf("current execution grant required")
	}
	return access.Scopes, nil
}

func denial(reason, message string) error {
	return &toolexec.Denial{Code: toolexec.RPCForbidden, Reason: reason, Message: message}
}

// governToolExecution adds connection-level authorization, governed
// tools/list and, for connection-auth, the credential provider. Every denial
// here precedes quota reservation and credential acquisition.
func (h *ConnectedHandler) governToolExecution(r *http.Request, tenant string, record resources.Record, connection ToolConnection, opts *toolexec.Options) error {
	policy := toolexec.ToolPolicy{}
	if connection.Tools != nil {
		policy = *connection.Tools
	}
	allowed := map[string]bool{}
	for _, name := range connection.Execution.AllowedTools {
		allowed[name] = true
	}
	scopeAuthorize := opts.Authorize
	opts.Authorize = func(ctx context.Context, trusted contracts.RequestContext, c toolexec.Connection, request toolexec.Request) error {
		if !policy.Permits(request.Name) {
			return denial("connection_policy", "tool "+request.Name+" is not permitted by this connection's tools policy")
		}
		if err := scopeAuthorize(ctx, trusted, c, request); err != nil {
			return denial("scope", "caller lacks the mcp:tools:"+request.Name+" grant")
		}
		if policy.ReadOnly {
			readOnly, known := h.readOnlyHint(ctx, tenant, record, request.Name)
			if !known {
				return denial("read_only_unknown", "connection is read_only and the readOnlyHint annotation of tool "+request.Name+" is unknown; list tools through this gateway, then retry")
			}
			if !readOnly {
				return denial("not_read_only", "connection is read_only and tool "+request.Name+" is not annotated readOnlyHint=true")
			}
		}
		return nil
	}
	visible := func(scopes []string, tool toolexec.ListedTool) bool {
		if !allowed[tool.Name] || !policy.Permits(tool.Name) || !scopeAllowed(scopes, "mcp:tools:"+tool.Name) {
			return false
		}
		return !policy.ReadOnly || (tool.ReadOnlyHint != nil && *tool.ReadOnlyHint)
	}
	var listScopes []string
	opts.List = &toolexec.ListOptions{
		Authorize: func(ctx context.Context, _ contracts.RequestContext, _ toolexec.Connection) error {
			scopes, err := h.currentScopes(ctx, r)
			if err != nil {
				return denial("scope", "current tool grant required")
			}
			for name := range allowed {
				if policy.Permits(name) && scopeAllowed(scopes, "mcp:tools:"+name) {
					listScopes = scopes
					return nil
				}
			}
			return denial("scope", "caller may not call any tool on this connection")
		},
		Visible: func(_ context.Context, _ contracts.RequestContext, tool toolexec.ListedTool) bool {
			return visible(listScopes, tool)
		},
		Observe: func(ctx context.Context, _ contracts.RequestContext, tools []toolexec.ListedTool) {
			h.observeAnnotations(ctx, tenant, record, allowed, tools)
		},
	}
	if connection.Execution.CredentialMode != toolexec.ConnectionAuth {
		return nil
	}
	provider, err := h.toolCredentials()
	if err != nil || connection.Auth == nil {
		return fmt.Errorf("tool credential provider unavailable")
	}
	auth := *connection.Auth
	target := h.credentialTarget(r, tenant, record, connection)
	opts.TokenSource = nil
	opts.Credentials = func(ctx context.Context) (toolexec.Credential, error) {
		credential, err := provider.Acquire(ctx, auth, target)
		if err != nil {
			return nil, err
		}
		return credential, nil
	}
	opts.Unauthorized = func(_ context.Context, sent toolexec.Credential) {
		if credential, ok := sent.(toolcredential.Credential); ok {
			provider.Invalidate(auth, target, credential)
		}
	}
	return nil
}

type toolAnnotations struct {
	ConnectionID string          `json:"connection_id"`
	Revision     uint64          `json:"connection_revision"`
	// ObservedAt is when the record was last written. Hint validity is per
	// tool (ObservedBy); records written before per-tool times existed have
	// ObservedBy == nil and their tools count as observed at ObservedAt.
	ObservedAt time.Time `json:"observed_at"`
	// ObservedBy is when each tool's own listing entry was last seen.
	ObservedBy map[string]time.Time `json:"observed_at_by_tool,omitempty"`
	ReadOnly   map[string]bool      `json:"read_only_hints"`
	// The other MCP annotation hints, consulted by policy rules (pkg/
	// toolpolicy). They share ReadOnly's observation times and expiry.
	Destructive map[string]bool `json:"destructive_hints,omitempty"`
	Idempotent  map[string]bool `json:"idempotent_hints,omitempty"`
	OpenWorld   map[string]bool `json:"open_world_hints,omitempty"`
}

// hintMaps are the per-tool hint maps of a record, by name.
func (a *toolAnnotations) hintMaps() []*map[string]bool {
	return []*map[string]bool{&a.ReadOnly, &a.Destructive, &a.Idempotent, &a.OpenWorld}
}

func (a toolAnnotations) names() map[string]bool {
	names := map[string]bool{}
	for _, m := range a.hintMaps() {
		for name := range *m {
			names[name] = true
		}
	}
	return names
}

// toolAnnotationClock is the clock for annotation ages; tests replace it.
var toolAnnotationClock = time.Now

// observedAt is when the tool's hint was seen, or false when it has no
// recorded observation.
func (a toolAnnotations) observedAt(name string) (time.Time, bool) {
	if a.ObservedBy == nil {
		return a.ObservedAt, true
	}
	at, ok := a.ObservedBy[name]
	return at, ok
}

// fresh reports whether the tool itself was observed within the TTL.
func (a toolAnnotations) fresh(name string, now time.Time) bool {
	at, ok := a.observedAt(name)
	return ok && now.Sub(at) <= toolAnnotationTTL && !at.After(now.Add(time.Minute))
}

func (h *ConnectedHandler) readOnlyHint(ctx context.Context, tenant string, record resources.Record, name string) (bool, bool) {
	saved, err := h.Store.Get(ctx, tenant, toolAnnotationsKind, record.ID)
	if err != nil {
		return false, false
	}
	var annotations toolAnnotations
	if json.Unmarshal(saved.Spec, &annotations) != nil || annotations.ConnectionID != record.ID || annotations.Revision != record.Revision || !annotations.fresh(name, toolAnnotationClock()) {
		return false, false
	}
	value, ok := annotations.ReadOnly[name]
	return value, ok
}

// observeAnnotations merges explicit readOnlyHint values for configured tools.
// Writes are best effort: a lost write leaves the hint unknown (fail closed).
func (h *ConnectedHandler) observeAnnotations(ctx context.Context, tenant string, record resources.Record, allowed map[string]bool, tools []toolexec.ListedTool) {
	now := toolAnnotationClock().UTC()
	next := toolAnnotations{ConnectionID: record.ID, Revision: record.Revision, ObservedAt: now, ObservedBy: map[string]time.Time{}, ReadOnly: map[string]bool{}, Destructive: map[string]bool{}, Idempotent: map[string]bool{}, OpenWorld: map[string]bool{}}
	expected := uint64(0)
	if saved, err := h.Store.Get(ctx, tenant, toolAnnotationsKind, record.ID); err == nil {
		expected = saved.Revision
		var previous toolAnnotations
		if json.Unmarshal(saved.Spec, &previous) == nil && previous.ConnectionID == record.ID && previous.Revision == record.Revision {
			// Other tools keep the time they were actually observed; expired
			// ones are dropped rather than refreshed by this listing.
			for name := range previous.names() {
				if at, ok := previous.observedAt(name); ok && previous.fresh(name, now) {
					next.ObservedBy[name] = at
					for i, m := range previous.hintMaps() {
						if value, had := (*m)[name]; had {
							(*next.hintMaps()[i])[name] = value
						}
					}
				}
			}
		}
	} else if !errors.Is(err, resources.ErrNotFound) {
		return
	}
	changed := false
	for _, tool := range tools {
		if !allowed[tool.Name] {
			continue
		}
		hints := []*bool{tool.ReadOnlyHint, tool.DestructiveHint, tool.IdempotentHint, tool.OpenWorldHint}
		known := false
		for _, hint := range hints {
			known = known || hint != nil
		}
		if !known {
			if next.names()[tool.Name] {
				for _, m := range next.hintMaps() {
					delete(*m, tool.Name)
				}
				delete(next.ObservedBy, tool.Name)
				changed = true
			}
			continue
		}
		for i, m := range next.hintMaps() {
			if hints[i] != nil {
				(*m)[tool.Name] = *hints[i]
			} else {
				delete(*m, tool.Name)
			}
		}
		next.ObservedBy[tool.Name] = now
		changed = true
	}
	if !changed && expected != 0 {
		return
	}
	payload, _ := json.Marshal(next)
	_, _ = h.Store.Put(ctx, resources.Record{TenantID: tenant, Kind: toolAnnotationsKind, ID: record.ID, Spec: payload}, expected)
}

// ToolAuthTypes describes every auth type and the tools policy for forms.
func (h *ConnectedHandler) ToolAuthTypes(w http.ResponseWriter, r *http.Request) {
	type secretReferences struct {
		Pattern   string `json:"pattern"`
		Namespace string `json:"namespace,omitempty"`
		Note      string `json:"note"`
	}
	references := secretReferences{Pattern: `^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$`, Note: "Secret fields take a reference name in the tenant secret namespace, never a value. Values are resolved at dispatch and never returned."}
	if tenant, err := h.Tenant(r); err == nil && tenant != "" {
		if provider, err := h.toolCredentials(); err == nil {
			references.Namespace = provider.SecretNamespace(tenant)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	JSON(w, 200, map[string]any{
		"types":             toolcredential.Catalog(),
		"default_type":      toolcredential.OAuthUser,
		"default_behaviour": "A connection without an auth block keeps its execution credential_mode: delegated-oauth uses oauth_user, operator-bearer uses the operator identity profile.",
		"secret_references": references,
		"tools_policy": []toolcredential.Field{
			{Name: "allow", Type: "string[]", Description: "Exact tool names or prefixes ending in '*'. When present, only matching tools are callable."},
			{Name: "deny", Type: "string[]", Description: "Exact tool names or prefixes ending in '*'. Deny wins over allow."},
			{Name: "read_only", Type: "boolean", Description: "Only tools whose gateway-observed MCP annotation readOnlyHint is true are callable; unknown annotations are denied."},
		},
	})
}
