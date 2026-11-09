package google

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// IdentityManager calls the Agent Identity v1 auth-provider API. Every operation
// is credential-sensitive and must be excluded from generic public HTTP output.
type IdentityManager struct {
	cfg    Config
	client *nativeapi.Client
}

func NewIdentityManager(cfg Config, client *nativeapi.Client) (*IdentityManager, error) {
	if e := cfg.validate(); e != nil {
		return nil, e
	}
	if client == nil {
		return nil, errors.New("identity manager client is required")
	}
	cfg.AllowedAgentPrincipals = slices.Clone(cfg.AllowedAgentPrincipals)
	cfg.AllowedRoles = slices.Clone(cfg.AllowedRoles)
	return &IdentityManager{cfg, client}, nil
}
func (d *IdentityManager) Operations() []nativeapi.OperationInfo {
	var out []nativeapi.OperationInfo
	for _, op := range []string{"list", "get", "create", "patch", "delete", "enable", "disable", "getIamPolicy", "setIamPolicy"} {
		out = append(out, nativeapi.OperationInfo{Name: "authProviders." + op, Credential: true, Mutating: !slices.Contains([]string{"get", "list", "getIamPolicy"}, op), Description: "Credential-sensitive Google Agent Identity auth-provider " + op})
	}
	return out
}
func (d *IdentityManager) Execute(ctx context.Context, op nativeapi.Operation) (nativeapi.Result, error) {
	if e := d.ValidateOperation(op); e != nil {
		return nativeapi.Result{}, e
	}
	kind, action, ok := strings.Cut(op.Name, ".")
	if !ok || kind != "authProviders" || !slices.Contains([]string{"list", "get", "create", "patch", "delete", "enable", "disable", "getIamPolicy", "setIamPolicy"}, action) {
		return nativeapi.Result{}, errors.New("unknown auth-manager operation")
	}
	if action == "getIamPolicy" || action == "setIamPolicy" {
		return policyExecute(ctx, d.client, d.cfg, op, "v1/", false, "authProviders")
	}
	if action == "list" {
		if op.Resource != d.cfg.parent() {
			return nativeapi.Result{}, errors.New("auth-provider list requires configured parent")
		}
		q, e := listQuery(op.Payload)
		if e != nil {
			return nativeapi.Result{}, e
		}
		return d.client.Do(ctx, http.MethodGet, "v1/"+op.Resource+"/authProviders", q, nil)
	}
	id, e := d.cfg.resource(op.Resource, "authProviders")
	if e != nil {
		return nativeapi.Result{}, e
	}
	if action == "get" {
		if e = empty(op.Payload); e != nil {
			return nativeapi.Result{}, e
		}
		r, e := d.client.Do(ctx, http.MethodGet, "v1/"+op.Resource, nil, nil)
		if e == nil {
			r.ETag, e = fingerprint(r.Body)
		}
		return r, e
	}
	m := mutation{}
	if e = strict(op.Payload, &m); e != nil {
		return nativeapi.Result{}, e
	}
	if e = requestID(m.RequestID); e != nil {
		return nativeapi.Result{}, e
	}
	q := url.Values{}
	if m.RequestID != "" {
		q.Set("requestId", m.RequestID)
	}
	var body any
	if action == "create" || action == "patch" {
		var provider AuthProvider
		if e = strict(m.Resource, &provider); e != nil {
			return nativeapi.Result{}, e
		}
		if provider.Name != "" && provider.Name != op.Resource {
			return nativeapi.Result{}, errors.New("auth-provider body/path mismatch")
		}
		provider.Name = op.Resource
		for _, p := range provider.WorkloadIDs {
			if !validPrincipal(p) || !slices.Contains(d.cfg.AllowedAgentPrincipals, p) {
				return nativeapi.Result{}, errors.New("auth-provider workload principal is not explicitly allowed")
			}
		}
		if action == "create" && len(provider.TypeParams) == 0 {
			return nativeapi.Result{}, errors.New("auth-provider type parameters required")
		}
		if len(provider.TypeParams) > 0 {
			var params map[string]json.RawMessage
			if json.Unmarshal(provider.TypeParams, &params) != nil || len(params) != 1 {
				return nativeapi.Result{}, errors.New("auth-provider requires exactly one type")
			}
			for k := range params {
				if !slices.Contains([]string{"threeLeggedOauth", "twoLeggedOauth", "apiKey", "geAuthProvider"}, k) {
					return nativeapi.Result{}, errors.New("unsupported auth-provider type")
				}
			}
		}
		body = provider
	} else if len(m.Resource) > 0 || m.UpdateMask != "" {
		return nativeapi.Result{}, errors.New("auth-provider action accepts requestId only")
	}
	if action == "create" {
		if !resourceID.MatchString(id) || op.ETag != "" || m.UpdateMask != "" {
			return nativeapi.Result{}, errors.New("invalid new auth-provider id/precondition")
		}
		q.Set("authProviderId", id)
		return d.client.Do(ctx, http.MethodPost, "v1/"+d.cfg.parent()+"/authProviders", q, body)
	}
	if !d.cfg.AllowNonAtomicMutations || !strings.HasPrefix(op.ETag, "sha256:") {
		return nativeapi.Result{}, errors.New("auth-provider mutation requires explicit non-atomic opt-in and observation fingerprint")
	}
	if action == "patch" {
		if m.UpdateMask == "" {
			return nativeapi.Result{}, errors.New("auth-provider updateMask required")
		}
		for _, f := range strings.Split(m.UpdateMask, ",") {
			if !slices.Contains([]string{"labels", "authProviderTypeParams", "allowedScopes", "blockedScopes", "description", "workloadIds"}, f) {
				return nativeapi.Result{}, errors.New("invalid auth-provider updateMask")
			}
		}
		q.Set("updateMask", m.UpdateMask)
	}
	observed, e := d.client.Do(ctx, http.MethodGet, "v1/"+op.Resource, nil, nil)
	if e != nil {
		return nativeapi.Result{}, e
	}
	tag, e := fingerprint(observed.Body)
	if e != nil {
		return nativeapi.Result{}, e
	}
	if tag != op.ETag {
		return nativeapi.Result{}, errors.New("auth-provider observation changed; review a new plan")
	}
	method, path := http.MethodPost, "v1/"+op.Resource+":"+action
	if action == "patch" {
		method, path = http.MethodPatch, "v1/"+op.Resource
	} else if action == "delete" {
		method, path = http.MethodDelete, "v1/"+op.Resource
	} else {
		q.Del("requestId")
		body = struct {
			RequestID string `json:"requestId,omitempty"`
		}{m.RequestID}
	}
	return d.client.Do(ctx, method, path, q, body)
}

type AuthProvider struct {
	Name          string            `json:"name,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	TypeParams    json.RawMessage   `json:"authProviderTypeParams,omitempty"`
	AllowedScopes []string          `json:"allowedScopes,omitempty"`
	BlockedScopes []string          `json:"blockedScopes,omitempty"`
	Description   string            `json:"description,omitempty"`
	WorkloadIDs   []string          `json:"workloadIds,omitempty"`
}

// Delegation separates the authenticated agent from an end user on whose
// behalf an OAuth credential is requested. The host must resolve/authorize both.
type Delegation struct {
	AgentPrincipal string
	UserID         string
	AuthProvider   string
	Scopes         []string
}
type DelegationAuthorizer interface {
	AuthorizeDelegation(context.Context, Delegation) error
}
type DelegationAuthorizeFunc func(context.Context, Delegation) error

func (f DelegationAuthorizeFunc) AuthorizeDelegation(ctx context.Context, d Delegation) error {
	if f == nil {
		return errors.New("delegation authorizer unavailable")
	}
	return f(ctx, d)
}

type CredentialsClient struct {
	cfg       Config
	client    *nativeapi.Client
	authorize DelegationAuthorizer
}

// NewCredentialsClient is deliberately not a Driver: credential responses may
// only flow to an approved outbound adapter, never through the governance API.
func NewCredentialsClient(cfg Config, client *nativeapi.Client, a DelegationAuthorizer) (*CredentialsClient, error) {
	if e := cfg.validate(); e != nil {
		return nil, e
	}
	if client == nil || a == nil {
		return nil, errors.New("credentials client and delegation authorizer are required")
	}
	cfg.AllowedAgentPrincipals = slices.Clone(cfg.AllowedAgentPrincipals)
	return &CredentialsClient{cfg, client, a}, nil
}

type RetrieveRequest struct {
	Delegation        Delegation
	ContinueURI       string
	ForceRefreshToken string
}
type RetrievedCredential struct {
	Token      string    `json:"token"`
	Header     string    `json:"header"`
	ExpireTime time.Time `json:"expireTime"`
	Scopes     []string  `json:"scopes"`
}
type ConsentRequired struct {
	AuthorizationURI string `json:"authorizationUri"`
	ConsentNonce     string `json:"consentNonce"`
	UID              string `json:"uid"`
}
type RetrieveResult struct {
	Success         *RetrievedCredential `json:"success,omitempty"`
	Pending         json.RawMessage      `json:"pending,omitempty"`
	ConsentRequired *ConsentRequired     `json:"uriConsentRequired,omitempty"`
	ConsentRejected json.RawMessage      `json:"consentRejected,omitempty"`
}

func (c *CredentialsClient) check(ctx context.Context, d Delegation) error {
	if _, e := c.cfg.resource(d.AuthProvider, "authProviders"); e != nil {
		return e
	}
	if d.UserID == "" || !validPrincipal(d.AgentPrincipal) || !slices.Contains(c.cfg.AllowedAgentPrincipals, d.AgentPrincipal) {
		return errors.New("explicit authorized agent and delegated user are required")
	}
	copy := d
	copy.Scopes = slices.Clone(d.Scopes)
	return c.authorize.AuthorizeDelegation(ctx, copy)
}
func (c *CredentialsClient) Retrieve(ctx context.Context, in RetrieveRequest) (RetrieveResult, error) {
	if in.ContinueURI != "" && !httpsURI(in.ContinueURI) {
		return RetrieveResult{}, errors.New("continue URI requires HTTPS")
	}
	if e := c.check(ctx, in.Delegation); e != nil {
		return RetrieveResult{}, e
	}
	p := struct {
		UserID            string   `json:"userId"`
		Scopes            []string `json:"scopes,omitempty"`
		ContinueURI       string   `json:"continueUri,omitempty"`
		ForceRefreshToken string   `json:"forceRefreshToken,omitempty"`
	}{in.Delegation.UserID, in.Delegation.Scopes, in.ContinueURI, in.ForceRefreshToken}
	r, e := c.client.Do(ctx, http.MethodPost, "v1/"+in.Delegation.AuthProvider+"/credentials:retrieve", nil, p)
	if e != nil {
		return RetrieveResult{}, e
	}
	var out RetrieveResult
	if json.Unmarshal(r.Body, &out) != nil {
		return RetrieveResult{}, errors.New("invalid credential broker response")
	}
	n := 0
	if out.Success != nil {
		n++
	}
	if out.Pending != nil {
		n++
	}
	if out.ConsentRequired != nil {
		n++
	}
	if out.ConsentRejected != nil {
		n++
	}
	if n != 1 {
		return RetrieveResult{}, errors.New("credential broker returned ambiguous result")
	}
	return out, nil
}

type FinalizeRequest struct {
	Delegation            Delegation
	UserIDValidationState string
	ConsentNonce          string
}

func (c *CredentialsClient) Finalize(ctx context.Context, in FinalizeRequest) error {
	if in.UserIDValidationState == "" || in.ConsentNonce == "" {
		return errors.New("consent state and nonce are required")
	}
	if e := c.check(ctx, in.Delegation); e != nil {
		return e
	}
	_, e := c.client.Do(ctx, http.MethodPost, "v1/"+in.Delegation.AuthProvider+"/credentials:finalize", nil, map[string]string{"userId": in.Delegation.UserID, "userIdValidationState": in.UserIDValidationState, "consentNonce": in.ConsentNonce})
	return e
}
