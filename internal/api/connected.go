package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/deployment"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/governance"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/networking"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

type TenantResolver func(*http.Request) (string, error)
type ModelConnection = modelapi.Connection

type ToolConnection struct {
	Execution             *toolexec.Connection   `json:"execution,omitempty"`
	ClientRegistrationRef string                 `json:"client_registration_ref,omitempty"`
	ID                    string                 `json:"id"`
	ResourceURL           string                 `json:"resource_url"`
	Issuer                string                 `json:"issuer,omitempty"`
	Client                toolauth.ClientProfile `json:"client"`
	NetworkProfileRef     string                 `json:"network_profile_ref,omitempty"`
	// Auth selects the upstream credential; nil keeps the execution
	// credential mode's existing behaviour. Tools is connection-level policy.
	Auth  *toolcredential.Auth `json:"auth,omitempty"`
	Tools *toolexec.ToolPolicy `json:"tools,omitempty"`
}
type ConnectedHandler struct {
	ToolSessions    *toolauth.Runtime
	Quota           *QuotaHost
	ResolveAccess   func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error)
	EnabledModules  map[string]bool
	Governance      *governance.Controller
	NativeProviders map[string]NativeProviderInfo
	Deployments     map[string]*deployment.Anvil
	Store           resources.Store
	Runtime         *connections.Runtime
	Tenant          TenantResolver
	Tools           *toolauth.Service
	ToolCredentials *toolcredential.Provider
	Development     bool
	PublicURL       string
	Ledger          *usageledger.Recorder
	AccessPoint     AccessPointOptions
	// Capacity holds the last provider limit observation per tenant+surface.
	Capacity *providerlimits.Store
}

func NewConnectedHandler(store resources.Store, runtime *connections.Runtime, tenant TenantResolver, tools *toolauth.Service, development bool) *ConnectedHandler {
	return &ConnectedHandler{Store: store, Runtime: runtime, Tenant: tenant, Tools: tools, Development: development}
}

// WithEnabledModules binds cross-Plex plans to the same explicit module set as
// route mounting. Configure before serving; nil preserves standalone defaults.
func (h *ConnectedHandler) WithEnabledModules(names []string) *ConnectedHandler {
	if names == nil {
		h.EnabledModules = nil
		return h
	}
	h.EnabledModules = map[string]bool{}
	for _, name := range names {
		h.EnabledModules[name] = true
	}
	return h
}
func idParam(r *http.Request) string {
	id := chi.URLParam(r, "id")
	if id == "" {
		id = r.PathValue("id")
	}
	return id
}
func (h *ConnectedHandler) tenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	t, e := h.Tenant(r)
	if e != nil || t == "" {
		Error(w, r, 403, "TENANT_REQUIRED", "verified tenant binding required")
		return "", false
	}
	return t, true
}
func resourceError(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, resources.ErrNotFound):
		Error(w, r, 404, "NOT_FOUND", "resource not found")
	case errors.Is(e, resources.ErrConflict):
		Error(w, r, 409, "REVISION_CONFLICT", "resource revision changed")
	default:
		Error(w, r, 500, "RESOURCE_ERROR", "resource store unavailable")
	}
}
func revision(r *http.Request) (uint64, error) {
	raw := strings.Trim(r.Header.Get("If-Match"), "\"")
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 63)
}
func (h *ConnectedHandler) Resources(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, ok := h.tenant(w, r)
		if !ok {
			return
		}
		id := idParam(r)
		if kind == governance.BindingKind && r.Method == http.MethodGet {
			resource := id
			if resource == "" {
				resource = "bindings"
			}
			if h.authorize(r, "aiplex:governance:read:"+resource) != nil {
				Error(w, r, 403, "ACTION_DENIED", "native binding read grant required")
				return
			}
		}
		if (kind == "deployment-intent" || kind == "deployment-plan") && r.Method == http.MethodGet {
			access := GetWIFAccess(r.Context())
			allowed := false
			if access != nil {
				for _, role := range access.Roles {
					if role == models.RoleAdmin || role == models.RoleDeployer {
						allowed = true
					}
				}
			}
			if !allowed && !h.Development {
				Error(w, r, 403, "RESOURCE_FORBIDDEN", "deployment resource access requires an administrative role")
				return
			}
		}
		switch r.Method {
		case http.MethodGet:
			if id == "" {
				records, e := h.Store.List(r.Context(), tenant, kind)
				if e != nil {
					resourceError(w, r, e)
					return
				}
				JSON(w, 200, records)
				return
			}
			rec, e := h.Store.Get(r.Context(), tenant, kind, id)
			if e != nil {
				resourceError(w, r, e)
				return
			}
			w.Header().Set("ETag", fmt.Sprintf("\"%d\"", rec.Revision))
			JSON(w, 200, rec)
		case http.MethodPut:
			expected, e := revision(r)
			if e != nil {
				Error(w, r, 400, "INVALID_REVISION", "If-Match must be a revision")
				return
			}
			payload, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			if e != nil || len(payload) > 1<<20 {
				Error(w, r, 400, "INVALID_SPEC", "bounded JSON required")
				return
			}
			if e = h.validate(r.Context(), tenant, kind, id, payload); e != nil {
				Error(w, r, 400, "INVALID_SPEC", e.Error())
				return
			}
			rec, e := h.Store.Put(r.Context(), resources.Record{TenantID: tenant, Kind: kind, ID: id, Spec: payload}, expected)
			if e != nil {
				resourceError(w, r, e)
				return
			}
			w.Header().Set("ETag", fmt.Sprintf("\"%d\"", rec.Revision))
			JSON(w, 200, rec)
		case http.MethodDelete:
			expected, e := revision(r)
			if e != nil || expected == 0 {
				Error(w, r, 428, "REVISION_REQUIRED", "If-Match revision required")
				return
			}
			if e = h.Store.Delete(r.Context(), tenant, kind, id, expected); e != nil {
				resourceError(w, r, e)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}
}
func (h *ConnectedHandler) validate(ctx context.Context, tenant, kind, id string, raw []byte) error {
	if kind == governance.BindingKind {
		return h.validateAgentBinding(tenant, id, raw)
	}
	if kind == usageledger.PriceKind {
		_, e := usageledger.DecodePrice(id, raw)
		return e
	}
	if id == "" {
		return fmt.Errorf("resource id required")
	}
	if kind == AccessPointKind {
		_, e := h.validateAccessPoint(ctx, tenant, id, raw)
		return e
	}
	validateNetworkRef := func(ref string) error {
		if ref == "" {
			return nil
		}
		_, e := h.Store.Get(ctx, tenant, "network-profile", ref)
		return e
	}
	switch kind {
	case "model-surface":
		var c ModelConnection
		if e := decodeSpec(raw, &c); e != nil {
			return e
		}
		if strings.ContainsAny(c.BillingProject, "\r\n /?#") {
			return fmt.Errorf("invalid billing project")
		}
		if c.Surface.ID != id {
			return fmt.Errorf("surface id mismatch")
		}
		if e := c.Surface.Validate(); e != nil {
			return e
		}
		if c.Surface.Provider != nil {
			return h.validateProviderConnection(ctx, tenant, c, validateNetworkRef)
		}
		if c.Auth != nil {
			return fmt.Errorf("auth applies only to provider surfaces; Google surfaces use identity_ref")
		}
		if e := h.Runtime.CheckDestination(tenant, c.IdentityRef, c.Surface.Origin); e != nil {
			return e
		}
		p, e := endpointpolicy.New(h.Runtime.Config.Network)
		if e != nil {
			return e
		}
		if e = p.ValidateURL(c.Surface.Origin); e != nil {
			return e
		}
		return validateNetworkRef(c.NetworkProfileRef)
	case "agent-connection":
		var c cloudagents.Connection
		if e := decodeSpec(raw, &c); e != nil {
			return e
		}
		if c.ID != id || c.TenantID != tenant {
			return fmt.Errorf("agent connection identity mismatch")
		}
		if e := c.Validate(); e != nil {
			return e
		}
		if e := h.checkAgentGovernanceRef(ctx, tenant, c); e != nil {
			return e
		}
		if e := h.Runtime.CheckDestination(tenant, c.IdentityRef, c.Endpoint); e != nil {
			return e
		}
		p, e := endpointpolicy.New(h.Runtime.Config.Network)
		if e != nil {
			return e
		}
		if e = p.ValidateURL(c.Endpoint); e != nil {
			return e
		}
		return validateNetworkRef(c.NetworkProfileRef)
	case "network-profile":
		var p networking.Profile
		if e := decodeSpec(raw, &p); e != nil {
			return e
		}
		if p.ID != id || p.TenantID != tenant {
			return fmt.Errorf("network profile identity mismatch")
		}
		return p.Validate()
	case "deployment-intent":
		var intent deployment.Intent
		if e := decodeSpec(raw, &intent); e != nil {
			return e
		}
		if intent.ID != id {
			return fmt.Errorf("intent id mismatch")
		}
		if e := intent.Validate(); e != nil {
			return e
		}
		provider := h.Deployments[intent.ProviderRef]
		if provider == nil {
			return fmt.Errorf("deployment provider not configured")
		}
		binding, bindingErr := provider.Binding(tenant, intent.BindingID)
		if bindingErr != nil || binding.ResourceID != id {
			return fmt.Errorf("deployment binding does not own this tenant intent")
		}
		return validateNetworkRef(intent.NetworkProfileRef)
	case "client-destination":
		var d clientplex.Destination
		if e := decodeSpec(raw, &d); e != nil {
			return e
		}
		if d.ID != id || len(d.Protocols) == 0 {
			return fmt.Errorf("destination identity and protocols required")
		}
		if d.Availability != "configured" && d.Availability != "unverified" && d.Availability != "unavailable" {
			return fmt.Errorf("destination availability must be configured/unverified/unavailable until probed")
		}
		p, e := endpointpolicy.New(h.Runtime.Config.Network)
		if e != nil {
			return e
		}
		return p.ValidateURL(d.Endpoint)
	case "tool-connection":
		var c ToolConnection
		if e := decodeSpec(raw, &c); e != nil {
			return e
		}
		if c.ID != id {
			return fmt.Errorf("tool connection id mismatch")
		}
		if e := h.validateToolAuth(ctx, tenant, c); e != nil {
			return e
		}
		if c.ClientRegistrationRef != "" {
			record, e := h.Store.Get(ctx, tenant, clientplex.Kind, c.ClientRegistrationRef)
			if e != nil {
				return e
			}
			var registration clientplex.Registration
			if json.Unmarshal(record.Spec, &registration) != nil || registration.ID != c.ClientRegistrationRef {
				return fmt.Errorf("invalid referenced developer-client registration")
			}
			// Developer application identity and issuer-scoped OAuth profile
			// identity are distinct. Linking them does not require equal IDs.
		}
		if c.Execution == nil || c.Issuer != "" || !reflect.DeepEqual(c.Client, toolauth.ClientProfile{}) || c.Execution.CredentialMode == toolexec.DelegatedOAuth {
			if e := toolauth.ValidateClientProfile(c.Client); e != nil {
				return e
			}
			if e := toolauth.ValidateConnection(toolauth.Connection{TenantID: tenant, ID: id, ResourceURL: c.ResourceURL, Issuer: c.Issuer}); e != nil {
				return e
			}
		}
		if c.Execution != nil {
			if c.Execution.CredentialMode == toolexec.DelegatedOAuth && c.Issuer == "" {
				return fmt.Errorf("delegated OAuth requires an exact issuer")
			}
			if c.Execution.ID != id || c.Execution.Endpoint != c.ResourceURL {
				return fmt.Errorf("execution identity and endpoint must match the saved connection")
			}
			if e := c.Execution.Validate(); e != nil {
				return e
			}
			if c.Execution.CredentialMode != toolexec.DelegatedOAuth && c.Execution.CredentialMode != toolexec.ConnectionAuth {
				if e := h.Runtime.CheckDestination(tenant, c.Execution.IdentityRef, c.ResourceURL); e != nil {
					return e
				}
				identity, e := h.Runtime.Identity(tenant, c.Execution.IdentityRef)
				if e != nil || identity.Type == "aws-default" {
					return fmt.Errorf("managed tool execution requires an approved bearer credential adapter")
				}
			}
		}
		p, e := endpointpolicy.New(h.Runtime.Config.Network)
		if e != nil {
			return e
		}
		if e = p.ValidateURL(c.ResourceURL); e != nil {
			return e
		}
		return validateNetworkRef(c.NetworkProfileRef)
	}
	return fmt.Errorf("unknown resource kind")
}
func (h *ConnectedHandler) NetworkPlan(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	rec, e := h.Store.Get(r.Context(), tenant, "network-profile", idParam(r))
	if e != nil {
		resourceError(w, r, e)
		return
	}
	var p networking.Profile
	if e = json.Unmarshal(rec.Spec, &p); e != nil {
		resourceError(w, r, e)
		return
	}
	plan, e := networking.Plan(p)
	if e != nil {
		Error(w, r, 400, "INVALID_PLAN", e.Error())
		return
	}
	JSON(w, 200, plan)
}
func (h *ConnectedHandler) RegisterTool(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	if h.Tools == nil {
		Error(w, r, 503, "REGISTRATION_UNAVAILABLE", "configure outbound endpoint policy and secure credential storage")
		return
	}
	rec, e := h.Store.Get(r.Context(), tenant, "tool-connection", idParam(r))
	if e != nil {
		resourceError(w, r, e)
		return
	}
	var c ToolConnection
	if e = json.Unmarshal(rec.Spec, &c); e != nil {
		resourceError(w, r, e)
		return
	}
	if e = h.validate(r.Context(), tenant, "tool-connection", idParam(r), rec.Spec); e != nil {
		Error(w, r, 409, "CONNECTION_UNAVAILABLE", "saved tool connection references are unavailable")
		return
	}
	expected := uint64(0)
	prior, readErr := h.Store.Get(r.Context(), tenant, "oauth-registration", c.ID)
	if readErr == nil {
		expected = prior.Revision
	} else if !errors.Is(readErr, resources.ErrNotFound) {
		resourceError(w, r, readErr)
		return
	}
	registration, e := h.Tools.Prepare(r.Context(), toolauth.Connection{TenantID: tenant, ID: c.ID, ResourceURL: c.ResourceURL, Issuer: c.Issuer}, c.Client)
	if e != nil {
		Error(w, r, 502, "REGISTRATION_FAILED", e.Error())
		return
	}
	current, readErr := h.Store.Get(r.Context(), tenant, "tool-connection", c.ID)
	if readErr != nil || current.Revision != rec.Revision {
		Error(w, r, 409, "CONNECTION_CHANGED", "tool connection changed during OAuth registration; retry with its current revision")
		return
	}
	saved, e := h.Store.Put(r.Context(), resources.Record{TenantID: tenant, Kind: "oauth-registration", ID: c.ID, Spec: registration.CopyJSON()}, expected)
	if e != nil {
		resourceError(w, r, e)
		return
	}
	JSON(w, 200, saved)
}
func (h *ConnectedHandler) ModelOperations(w http.ResponseWriter, r *http.Request) {
	if provider := r.URL.Query().Get("provider"); provider != "" {
		descriptor, ok := modelapi.ProviderByID(provider)
		if !ok {
			Error(w, r, 404, "NOT_FOUND", "unknown model provider")
			return
		}
		JSON(w, 200, descriptor)
		return
	}
	JSON(w, 200, modelapi.Operations())
}
func (h *ConnectedHandler) trusted(r *http.Request, resource string) (contracts.RequestContext, error) {
	tenant, e := h.Tenant(r)
	if e != nil {
		return contracts.RequestContext{}, e
	}
	identity := GetWIFIdentity(r.Context())
	if identity == nil {
		return contracts.RequestContext{}, fmt.Errorf("verified identity required")
	}
	return contracts.RequestContext{Version: contracts.Version, TenantID: tenant, RequestID: GetRequestID(r.Context()), PrincipalID: identity.PrincipalID, ActorID: identity.ActorID, ResourceID: resource}, nil
}
func scopeAllowed(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == required {
			return true
		}
		if strings.HasSuffix(s, ":*") && strings.HasPrefix(required, strings.TrimSuffix(s, "*")) {
			return true
		}
	}
	return false
}
func (h *ConnectedHandler) authorize(r *http.Request, required string) error {
	access := GetWIFAccess(r.Context())
	if access == nil || !scopeAllowed(access.Scopes, required) {
		return fmt.Errorf("action grant required")
	}
	return nil
}

// authorizeCurrent re-resolves grants at the dispatch boundary. Quota-protected
// hosts must supply their authoritative resolver rather than reuse ingress grants.
func (h *ConnectedHandler) authorizeCurrent(ctx context.Context, r *http.Request, required string) error {
	return h.authorizeAllCurrent(ctx, r, required)
}

// Resolve one coherent authority snapshot for an intersection of required
// grants. Separate refreshes could combine permissions that never coexisted.
func (h *ConnectedHandler) authorizeAllCurrent(ctx context.Context, r *http.Request, required ...string) error {
	if h.ResolveAccess == nil {
		if h.Quota != nil {
			return fmt.Errorf("current access resolver unavailable")
		}
		for _, scope := range required {
			if err := h.authorize(r, scope); err != nil {
				return err
			}
		}
		return nil
	}
	identity := GetWIFIdentity(r.Context())
	if identity == nil {
		return fmt.Errorf("verified identity required")
	}
	access, err := h.ResolveAccess(ctx, identity)
	if err != nil || access == nil || access.Identity.PrincipalID != identity.PrincipalID || access.Identity.ActorID != identity.ActorID {
		return fmt.Errorf("current execution grant required")
	}
	for _, scope := range required {
		if !scopeAllowed(access.Scopes, scope) {
			return fmt.Errorf("current execution grant required")
		}
	}
	return nil
}
func (h *ConnectedHandler) NativeModel(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	id := idParam(r)
	w, usage := h.beginUsage(w, r, tenant, usageledger.PlaneLLM, id)
	defer usage.finish()
	record, e := h.Store.Get(r.Context(), tenant, "model-surface", id)
	if e != nil {
		resourceError(w, r, e)
		return
	}
	var connection ModelConnection
	if e = json.Unmarshal(record.Spec, &connection); e != nil {
		resourceError(w, r, e)
		return
	}
	if e = h.checkNetworkRef(r.Context(), tenant, connection.NetworkProfileRef); e != nil {
		Error(w, r, 503, "NETWORK_PROFILE_UNAVAILABLE", "network profile unavailable")
		return
	}
	if e = h.checkModelDestination(tenant, connection); e != nil {
		Error(w, r, 503, "CREDENTIAL_DESTINATION_DENIED", e.Error())
		return
	}
	policy := h.Runtime.Policy
	if policy == nil {
		Error(w, r, 503, "NETWORK_UNAVAILABLE", "native outbound policy unavailable")
		return
	}
	liveDialer, e := modelapi.NewLiveDialer(policy, nil)
	if e != nil {
		Error(w, r, 503, "LIVE_UNAVAILABLE", e.Error())
		return
	}
	headers := http.Header{}
	if connection.BillingProject != "" {
		headers.Set("X-Goog-User-Project", connection.BillingProject)
	}
	opts := modelapi.Options{UpstreamHeaders: headers, HTTPClient: h.Runtime.Streaming, TokenSource: modelapi.TokenSourceFunc(func(ctx context.Context) (string, error) { return h.Runtime.Token(ctx, tenant, connection.IdentityRef) }), Context: func(req *http.Request) (contracts.RequestContext, error) { return h.trusted(req, id) }, Authorize: func(ctx context.Context, request contracts.RequestContext, s modelapi.Surface, op modelapi.Operation, m modelapi.Metadata) error {
		if e := h.authorize(r, "llm:surface:"+id+":"+op.ID); e != nil {
			return e
		}
		if m.Agent != "" {
			if e := h.authorize(r, "llm:agent:"+m.Agent); e != nil {
				return e
			}
		}
		if m.Model != "" {
			model, e := s.ModelID(m.Model)
			if e != nil {
				return e
			}
			return h.authorize(r, "llm:model:"+model)
		}
		return nil
	}, AllowShared: func(ctx context.Context, request contracts.RequestContext, s modelapi.Surface, op modelapi.Operation) bool {
		return h.authorize(r, "llm:surface:"+id+":shared-admin") == nil
	}, Ownership: resourceOwnership{store: h.Store, namespace: strconv.FormatUint(record.Revision, 10)}, LiveDialer: liveDialer, AllowHTTPForTests: h.Development}
	if h.Quota != nil {
		opts.Execution = h.ModelExecutionPreparer(r, record, connection)
	}
	if connection.Surface.Provider != nil {
		if e = h.configureProviderOptions(r, tenant, id, record, connection, &opts, usage); e != nil {
			Error(w, r, 503, "CREDENTIAL_UNAVAILABLE", "provider credential is unavailable")
			return
		}
	}
	usage.instrumentModel(&opts, connection)
	gateway, e := modelapi.NewGateway(connection.Surface, opts)
	if e != nil {
		Error(w, r, 503, "SURFACE_UNAVAILABLE", e.Error())
		return
	}
	prefix := "/api/v1/llm/native/" + id
	http.StripPrefix(prefix, gateway).ServeHTTP(w, r)
}
func (h *ConnectedHandler) InvokeAgent(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	id := idParam(r)
	w, usage := h.beginUsage(w, r, tenant, usageledger.PlaneA2A, id)
	defer usage.finish()
	record, e := h.Store.Get(r.Context(), tenant, "agent-connection", id)
	if e != nil {
		resourceError(w, r, e)
		return
	}
	var c cloudagents.Connection
	if e = json.Unmarshal(record.Spec, &c); e != nil {
		resourceError(w, r, e)
		return
	}
	trusted, e := h.trusted(r, id)
	if e != nil {
		Error(w, r, 401, "IDENTITY_REQUIRED", e.Error())
		return
	}
	var in cloudagents.Invocation
	if e = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); e != nil {
		Error(w, r, 400, "INVALID_INVOCATION", "invalid native invocation")
		return
	}
	usage.agent(c.Provider, in.Operation)
	if e = h.authorize(r, "a2a:agent:"+id+":"+in.Operation); e != nil {
		Error(w, r, 403, "ACTION_DENIED", e.Error())
		return
	}
	if e = h.checkAgentGovernanceRef(r.Context(), tenant, c); e != nil {
		Error(w, r, 503, "GOVERNANCE_BINDING_UNAVAILABLE", "native governance binding is unavailable or targets another runtime")
		return
	}
	if e = h.checkNetworkRef(r.Context(), tenant, c.NetworkProfileRef); e != nil {
		Error(w, r, 503, "NETWORK_PROFILE_UNAVAILABLE", "network profile unavailable")
		return
	}
	if e = h.Runtime.CheckDestination(tenant, c.IdentityRef, c.Endpoint); e != nil {
		Error(w, r, 503, "CREDENTIAL_DESTINATION_DENIED", e.Error())
		return
	}
	policy := h.Runtime.Policy
	if policy == nil {
		Error(w, r, 503, "NETWORK_UNAVAILABLE", "native outbound policy unavailable")
		return
	}
	identity, e := h.Runtime.Identity(tenant, c.IdentityRef)
	if e != nil {
		Error(w, r, 503, "IDENTITY_UNAVAILABLE", e.Error())
		return
	}
	var credential cloudagents.Authenticator
	if identity.Type == "aws-default" {
		service := "bedrock"
		if c.Provider == cloudagents.AgentCoreRuntime {
			service = "bedrock-agentcore"
		}
		credential = lazyAWSAgentCredentials{handler: h, tenant: tenant, connection: c, service: service}
	} else {
		credential = cloudagents.BearerCredentials{Token: func(ctx context.Context) (string, error) { return h.Runtime.Token(ctx, tenant, c.IdentityRef) }}
	}
	options := []cloudagents.Option{cloudagents.WithSessionStore(cloudSessions{h.Store})}
	if h.Quota != nil {
		options = append(options, cloudagents.WithExecutionPreparer(usage.agentPreparer(h.AgentExecutionPreparer(w, r, record, c))))
	}
	invoker, e := cloudagents.NewInvoker(c, cloudagents.AuthorizeFunc(func(ctx context.Context, a cloudagents.Action) error {
		if err := h.authorizeCurrent(ctx, r, "a2a:agent:"+id+":"+a.Operation); err != nil {
			return quotaStatus(403, "ACTION_DENIED", "current native agent action grant required")
		}
		return nil
	}), h.Runtime.Streaming, credential, options...)
	if e != nil {
		Error(w, r, 503, "AGENT_UNAVAILABLE", e.Error())
		return
	}
	invokeCtx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	response, e := invoker.Invoke(invokeCtx, trusted, in)
	if e != nil {
		usage.noteError(e)
		var failure *execution.StatusError
		if errors.As(e, &failure) {
			if failure.RetryAfter > 0 {
				seconds := failure.RetryAfter / time.Second
				if failure.RetryAfter%time.Second != 0 {
					seconds++
				}
				w.Header().Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
			}
			Error(w, r, failure.Status, failure.Code, failure.Message)
			return
		}
		Error(w, r, 502, "INVOCATION_FAILED", e.Error())
		return
	}
	defer response.Body.Close()
	for _, name := range []string{"Content-Type", "Cache-Control", "X-Request-Id", "Retry-After"} {
		if v := response.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.WriteHeader(response.StatusCode)
	copyNativeStream(w, usage.agentBody(response.Body))
}

type resourceOwnership struct {
	store     resources.Store
	namespace string
}

func ownershipID(o modelapi.Owner, r modelapi.Resource) string {
	b, _ := json.Marshal([]string{o.SurfaceID, r.Kind, r.ID})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s resourceOwnership) Bind(ctx context.Context, o modelapi.Owner, r modelapi.Resource) error {
	id := ownershipID(modelapi.Owner{TenantID: o.TenantID, PrincipalID: o.PrincipalID, SurfaceID: o.SurfaceID + "@" + s.namespace}, r)
	payload, _ := json.Marshal(o)
	_, e := s.store.Put(ctx, resources.Record{TenantID: o.TenantID, Kind: "model-resource-owner", ID: id, Spec: payload}, 0)
	if errors.Is(e, resources.ErrConflict) {
		return s.Check(ctx, o, r)
	}
	return e
}
func (s resourceOwnership) Check(ctx context.Context, o modelapi.Owner, r modelapi.Resource) error {
	rec, e := s.store.Get(ctx, o.TenantID, "model-resource-owner", ownershipID(modelapi.Owner{TenantID: o.TenantID, PrincipalID: o.PrincipalID, SurfaceID: o.SurfaceID + "@" + s.namespace}, r))
	if e != nil {
		return e
	}
	var owner modelapi.Owner
	if e = json.Unmarshal(rec.Spec, &owner); e != nil {
		return e
	}
	if owner != o {
		return fmt.Errorf("resource owner mismatch")
	}
	return nil
}

func (h *ConnectedHandler) ResolveDestination(ctx context.Context, tenant, ref string) (clientplex.Destination, error) {
	rec, e := h.Store.Get(ctx, tenant, "model-surface", ref)
	if e != nil && !errors.Is(e, resources.ErrNotFound) {
		return clientplex.Destination{}, e
	}
	if e == nil {
		if _, collision := h.Store.Get(ctx, tenant, "client-destination", ref); collision == nil {
			return clientplex.Destination{}, fmt.Errorf("destination reference is ambiguous across resource kinds")
		} else if !errors.Is(collision, resources.ErrNotFound) {
			return clientplex.Destination{}, collision
		}
		if h.EnabledModules != nil && !h.EnabledModules["llmplex"] {
			return clientplex.Destination{}, fmt.Errorf("destination LLMPlex module is disabled")
		}
		var c ModelConnection
		if e = json.Unmarshal(rec.Spec, &c); e != nil {
			return clientplex.Destination{}, e
		}
		if c.Surface.ID != ref {
			return clientplex.Destination{}, fmt.Errorf("surface identity mismatch")
		}
		if e = h.validate(ctx, tenant, "model-surface", ref, rec.Spec); e != nil {
			return clientplex.Destination{}, e
		}
		if h.PublicURL == "" {
			return clientplex.Destination{}, fmt.Errorf("trusted public gateway URL not configured")
		}
		if !clientSurfaceReady(c.Surface) {
			return clientplex.Destination{}, fmt.Errorf("surface has no enabled inference operation for its client protocol")
		}
		return clientplex.Destination{ID: ref, Provider: "aiplex", Endpoint: strings.TrimSuffix(h.PublicURL, "/") + "/api/v1/llm/gateway/" + ref, Protocols: []string{c.Surface.Protocol}, ProjectID: c.Surface.Project, Location: c.Surface.Location, Availability: "configured", ModelIDs: c.Surface.Models}, nil
	}
	rec, e = h.Store.Get(ctx, tenant, "client-destination", ref)
	if e != nil {
		return clientplex.Destination{}, e
	}
	var d clientplex.Destination
	if e = json.Unmarshal(rec.Spec, &d); e != nil {
		return d, e
	}
	if d.ID != ref {
		return d, fmt.Errorf("destination id mismatch")
	}
	return d, nil
}

func decodeSpec(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(value); e != nil {
		return e
	}
	if e := decoder.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("trailing resource specification")
	}
	return nil
}
func (h *ConnectedHandler) checkNetworkRef(ctx context.Context, tenant, ref string) error {
	if ref == "" {
		return nil
	}
	_, e := h.Store.Get(ctx, tenant, "network-profile", ref)
	return e
}

func copyNativeStream(w http.ResponseWriter, reader io.Reader) {
	buf := make([]byte, 32<<10)
	for {
		n, e := reader.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flush, ok := w.(http.Flusher); ok {
				flush.Flush()
			}
		}
		if e != nil {
			return
		}
	}
}
