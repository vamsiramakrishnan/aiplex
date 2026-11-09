package aws

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// TokenBroker is an injected trusted security boundary, never request data.
// Bind must authenticate/authorize the context's tenant and principal for this
// exact workload, provider, flow, scopes and targets. It resolves any caller's
// opaque session handle under that principal's ownership. It supplies verified
// IdP proof or an agent+user-bound opaque workload token, never an unverified
// caller-supplied userId/token. Errors are deliberately redacted by the driver.
type TokenBroker interface {
	Bind(context.Context, TokenRequest) (TokenBinding, error)
}
type TokenRequest struct {
	Operation     string
	WorkloadARN   string
	ProviderName  string
	Flow          string
	Scopes        []string
	Audiences     []string
	Resources     []string
	ReturnURL     string
	SessionHandle string
}
type TokenBinding struct {
	TenantID              string            `json:"-"`
	PrincipalID           string            `json:"-"`
	WorkloadARN           string            `json:"-"`
	ProviderName          string            `json:"-"`
	Delegated             bool              `json:"-"`
	UserJWT               string            `json:"-"`
	UserID                string            `json:"-"`
	WorkloadIdentityToken string            `json:"-"`
	SessionURI            string            `json:"-"`
	CustomState           string            `json:"-"`
	CustomParameters      map[string]string `json:"-"`
}

func (TokenBinding) String() string   { return "AgentCore trusted token binding (redacted)" }
func (TokenBinding) GoString() string { return "AgentCore trusted token binding (redacted)" }

// WithDataPlane returns a new immutable driver. The client must use the native
// data endpoint, and signing credentials must have the same verified account.
// No executable token operation exists until both dependencies are installed.
func (d *Driver) WithDataPlane(client *nativeapi.Client, broker TokenBroker) (*Driver, error) {
	if d == nil || client == nil || broker == nil {
		return nil, errors.New("AgentCore credential exchange requires data client and trusted broker")
	}
	copy := *d
	copy.data = client
	copy.broker = broker
	return &copy, nil
}

type resourceCredentialInput struct {
	ProviderName        string   `json:"resourceCredentialProviderName"`
	Flow                string   `json:"oauth2Flow,omitempty"`
	Scopes              []string `json:"scopes,omitempty"`
	Audiences           []string `json:"audiences,omitempty"`
	Resources           []string `json:"resources,omitempty"`
	ReturnURL           string   `json:"resourceOauth2ReturnUrl,omitempty"`
	ForceAuthentication bool     `json:"forceAuthentication,omitempty"`
	SessionHandle       string   `json:"sessionHandle,omitempty"`
}

var sessionURI = regexp.MustCompile(`^urn:ietf:params:oauth:request_uri:[a-zA-Z0-9._~-]+$`)

func subset(values, allowed []string, max int) bool {
	if len(values) > max {
		return false
	}
	for _, v := range values {
		if len(v) == 0 || len(v) > 2048 || !slices.Contains(allowed, v) {
			return false
		}
	}
	return true
}
func (d *Driver) tokenRequest(op nativeapi.Operation) (TokenRequest, resourceCredentialInput, error) {
	var b resourceCredentialInput
	r := TokenRequest{Operation: op.Name, WorkloadARN: op.Resource}
	if _, e := d.name(op.Resource, "workloadIdentities"); e != nil {
		return r, b, e
	}
	if op.ETag != "" {
		return r, b, errors.New("credential exchange accepts no etag")
	}
	if op.Name == "workloadTokens.get" || op.Name == "workloadTokens.forJWT" || op.Name == "workloadTokens.forUserID" {
		if e := empty(op.Payload); e != nil {
			return r, b, e
		}
		if op.Name == "workloadTokens.forUserID" && !d.cfg.AllowUserIDExchange {
			return r, b, errors.New("user-ID exchange is disabled; prefer verified JWT exchange")
		}
		return r, b, nil
	}
	if op.Name != "resourceCredentials.oauth2" && op.Name != "resourceCredentials.apiKey" {
		return r, b, errors.New("unknown AgentCore credential operation")
	}
	if e := strict(op.Payload, &b); e != nil {
		return r, b, e
	}
	if !slices.Contains(d.cfg.AllowedProviderNames, b.ProviderName) || !providerName.MatchString(b.ProviderName) || len(b.SessionHandle) > 1024 {
		return r, b, errors.New("resource credential provider/session is outside configured scope")
	}
	if op.Name == "resourceCredentials.apiKey" {
		if b.Flow != "" || len(b.Scopes) > 0 || len(b.Audiences) > 0 || len(b.Resources) > 0 || b.ReturnURL != "" || b.ForceAuthentication || b.SessionHandle != "" {
			return r, b, errors.New("API-key exchange accepts only the provider name")
		}
	} else {
		if !slices.Contains(d.cfg.AllowedOAuthFlows, b.Flow) || len(b.Scopes) == 0 || !subset(b.Scopes, d.cfg.AllowedScopes, 128) || !subset(b.Audiences, d.cfg.AllowedAudiences, 128) || !subset(b.Resources, d.cfg.AllowedResources, 128) || (b.ReturnURL != "" && !slices.Contains(d.cfg.AllowedReturnURLs, b.ReturnURL)) {
			return r, b, errors.New("OAuth flow, scopes or targets are not allowlisted")
		}
		if b.Flow == "M2M" && (b.ReturnURL != "" || b.SessionHandle != "" || b.ForceAuthentication) {
			return r, b, errors.New("M2M does not accept user authorization-session fields")
		}
		if b.Flow == "USER_FEDERATION" && b.ReturnURL == "" {
			return r, b, errors.New("user federation requires an allowlisted return URL")
		}
	}
	r.ProviderName = b.ProviderName
	r.Flow = b.Flow
	r.Scopes = slices.Clone(b.Scopes)
	r.Audiences = slices.Clone(b.Audiences)
	r.Resources = slices.Clone(b.Resources)
	r.ReturnURL = b.ReturnURL
	r.SessionHandle = b.SessionHandle
	return r, b, nil
}
func (d *Driver) credentials(ctx context.Context, op nativeapi.Operation) (nativeapi.Result, error) {
	r, b, e := d.tokenRequest(op)
	if e != nil {
		return nativeapi.Result{}, e
	}
	if d.data == nil || d.broker == nil {
		return nativeapi.Result{}, errors.New("trusted AgentCore credential broker is not configured")
	}
	binding, e := d.broker.Bind(ctx, r)
	if e != nil {
		return nativeapi.Result{}, errors.New("AgentCore credential authorization failed")
	}
	if binding.TenantID == "" || binding.PrincipalID == "" || binding.WorkloadARN != r.WorkloadARN || binding.ProviderName != r.ProviderName {
		return nativeapi.Result{}, errors.New("AgentCore credential binding does not match the authorized request")
	}
	n, _ := d.name(op.Resource, "workloadIdentities")
	var path string
	body := map[string]any{}
	switch op.Name {
	case "workloadTokens.get":
		if binding.Delegated || binding.UserJWT != "" || binding.UserID != "" {
			return nativeapi.Result{}, errors.New("autonomous token exchange cannot discard delegated identity")
		}
		path = "identities/GetWorkloadAccessToken"
		body["workloadName"] = n
	case "workloadTokens.forJWT":
		if !binding.Delegated || binding.UserJWT == "" || binding.UserID != "" {
			return nativeapi.Result{}, errors.New("JWT exchange requires exactly one verified delegated JWT")
		}
		path = "identities/GetWorkloadAccessTokenForJWT"
		body["workloadName"] = n
		body["userToken"] = binding.UserJWT
	case "workloadTokens.forUserID":
		if !binding.Delegated || binding.UserID == "" || len(binding.UserID) > 128 || binding.UserJWT != "" {
			return nativeapi.Result{}, errors.New("user-ID exchange requires an authenticated broker subject")
		}
		path = "identities/GetWorkloadAccessTokenForUserId"
		body["workloadName"] = n
		body["userId"] = binding.UserID
	case "resourceCredentials.apiKey", "resourceCredentials.oauth2":
		if binding.WorkloadIdentityToken == "" || len(binding.WorkloadIdentityToken) > 131072 {
			return nativeapi.Result{}, errors.New("resource exchange requires an opaque agent/user-bound workload token")
		}
		body["resourceCredentialProviderName"] = b.ProviderName
		body["workloadIdentityToken"] = binding.WorkloadIdentityToken
		path = "identities/api-key"
		if op.Name == "resourceCredentials.oauth2" {
			if (b.Flow == "M2M") == binding.Delegated {
				return nativeapi.Result{}, errors.New("OAuth flow and delegated identity binding disagree")
			}
			path = "identities/oauth2/token"
			body["oauth2Flow"] = b.Flow
			body["scopes"] = b.Scopes
			if len(b.Audiences) > 0 {
				body["audiences"] = b.Audiences
			}
			if len(b.Resources) > 0 {
				body["resources"] = b.Resources
			}
			if b.ReturnURL != "" {
				body["resourceOauth2ReturnUrl"] = b.ReturnURL
			}
			if b.ForceAuthentication {
				body["forceAuthentication"] = true
			}
			if binding.SessionURI != "" {
				if !sessionURI.MatchString(binding.SessionURI) || len(binding.SessionURI) > 1024 {
					return nativeapi.Result{}, errors.New("invalid broker-owned OAuth session")
				}
				body["sessionUri"] = binding.SessionURI
			} else if b.SessionHandle != "" {
				return nativeapi.Result{}, errors.New("broker did not resolve the caller's owned OAuth session")
			}
			if b.Flow == "USER_FEDERATION" && binding.SessionURI == "" && len(binding.CustomState) < 32 {
				return nativeapi.Result{}, errors.New("new user authorization requires broker-owned CSRF state")
			}
			if len(binding.CustomState) > 4096 {
				return nativeapi.Result{}, errors.New("broker OAuth state exceeds limit")
			}
			if binding.CustomState != "" {
				body["customState"] = binding.CustomState
			}
			if len(binding.CustomParameters) > 0 {
				body["customParameters"] = binding.CustomParameters
			}
		}
	}
	// Broker authorization precedes AWS credential retrieval and network I/O.
	if e := d.verifyAccount(ctx); e != nil {
		return nativeapi.Result{}, e
	}
	return d.data.Do(ctx, http.MethodPost, path, nil, body)
}
