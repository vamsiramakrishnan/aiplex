package entra

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

type AgentBinding struct {
	ObjectID string `json:"object_id"`
	AppID    string `json:"app_id"`
}

// BrokerConfig pins one tenant, one blueprint, child identities and named
// downstream services. Configure the approved Microsoft sidecar with those same
// tenant/blueprint values, credential source and downstream audiences/scopes.
// AgentIdentityOverridesEnabled is explicit operator confirmation that the
// sidecar honors AgentIdentity overrides on the selected SDK endpoints.
type BrokerConfig struct {
	TenantID                      string         `json:"tenant_id"`
	Blueprint                     Blueprint      `json:"blueprint"`
	AllowedAgents                 []AgentBinding `json:"allowed_agents"`
	Services                      []string       `json:"services"`
	SidecarURL                    string         `json:"sidecar_url"`
	AgentIdentityOverridesEnabled bool           `json:"agent_identity_overrides_enabled"`
}
type TokenRequest struct {
	TenantID          string
	BlueprintObjectID string
	AgentObjectID     string
	AgentAppID        string
	Service           string
	// UserObjectID is required for delegated OBO and is never substituted with a
	// sponsor or agent-user identity. It must match the sidecar-validated oid.
	UserObjectID string
}

// Authorization deliberately never serializes credentials. The caller must
// explicitly access Header/BearerToken inside a trusted credential injector.
type Authorization struct{ header string }

func (a Authorization) Header() string             { return a.header }
func (a Authorization) BearerToken() string        { return strings.TrimPrefix(a.header, "Bearer ") }
func (Authorization) String() string               { return "EntraAuthorization(redacted)" }
func (Authorization) GoString() string             { return "EntraAuthorization(redacted)" }
func (Authorization) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }

type TokenBroker struct {
	config   BrokerConfig
	http     *http.Client
	host     nativeapi.Authenticator
	agents   map[string]AgentBinding
	services map[string]bool
}

var serviceName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,127}$`)

func NewTokenBroker(cfg BrokerConfig, client *http.Client, host nativeapi.Authenticator) (*TokenBroker, error) {
	if !validID(cfg.TenantID) || !validID(cfg.Blueprint.ObjectID) || !validID(cfg.Blueprint.AppID) || !cfg.AgentIdentityOverridesEnabled || host == nil {
		return nil, errors.New("broker requires pinned tenant/blueprint, host authentication and explicit sidecar agent-override configuration")
	}
	if _, e := nativeapi.NewClient(cfg.SidecarURL, client, host); e != nil {
		return nil, errors.New("invalid approved Entra sidecar endpoint")
	}
	b := &TokenBroker{config: cfg, http: client, host: host, agents: map[string]AgentBinding{}, services: map[string]bool{}}
	for _, a := range cfg.AllowedAgents {
		if !validID(a.ObjectID) || !validID(a.AppID) {
			return nil, errors.New("agent object and application GUIDs required")
		}
		key := strings.ToLower(a.ObjectID)
		if _, ok := b.agents[key]; ok {
			return nil, errors.New("duplicate broker agent")
		}
		b.agents[key] = a
	}
	for _, s := range cfg.Services {
		if !serviceName.MatchString(s) || b.services[s] {
			return nil, errors.New("invalid or duplicate downstream service")
		}
		b.services[s] = true
	}
	if len(b.agents) == 0 || len(b.services) == 0 {
		return nil, errors.New("broker requires explicit allowed agents and downstream services")
	}
	b.config.AllowedAgents = append([]AgentBinding(nil), cfg.AllowedAgents...)
	b.config.Services = append([]string(nil), cfg.Services...)
	return b, nil
}
func (b *TokenBroker) validate(r TokenRequest, delegated bool) (AgentBinding, error) {
	if b == nil || !strings.EqualFold(r.TenantID, b.config.TenantID) || !strings.EqualFold(r.BlueprintObjectID, b.config.Blueprint.ObjectID) || !b.services[r.Service] {
		return AgentBinding{}, errors.New("broker tenant, blueprint or service denied")
	}
	a, ok := b.agents[strings.ToLower(r.AgentObjectID)]
	if !ok || !strings.EqualFold(a.AppID, r.AgentAppID) {
		return AgentBinding{}, errors.New("broker child identity binding denied")
	}
	if delegated {
		if !validID(r.UserObjectID) {
			return AgentBinding{}, errors.New("delegated user object GUID required")
		}
	} else if r.UserObjectID != "" {
		return AgentBinding{}, errors.New("autonomous acquisition cannot impersonate a user")
	}
	return a, nil
}
func (b *TokenBroker) client(assertion string) (*nativeapi.Client, error) {
	return nativeapi.NewClient(b.config.SidecarURL, b.http, nativeapi.AuthFunc(func(ctx context.Context, r *http.Request) error {
		if e := b.host.Authenticate(ctx, r); e != nil {
			return errors.New("sidecar host authentication failed")
		}
		if assertion != "" {
			for name, values := range r.Header {
				if !strings.EqualFold(name, "Authorization") {
					continue
				}
				for _, value := range values {
					if value != "" {
						return errors.New("delegated sidecar host authentication requires a separate header or mutual TLS")
					}
				}
			}
			r.Header.Set("Authorization", "Bearer "+assertion)
		}
		return nil
	}))
}
func (b *TokenBroker) AcquireAutonomous(ctx context.Context, r TokenRequest) (Authorization, error) {
	a, e := b.validate(r, false)
	if e != nil {
		return Authorization{}, e
	}
	client, e := b.client("")
	if e != nil {
		return Authorization{}, errors.New("sidecar client unavailable")
	}
	result, e := client.Do(ctx, "GET", "AuthorizationHeaderUnauthenticated/"+r.Service, url.Values{"AgentIdentity": {a.AppID}}, nil)
	return authorizationResult(result, e)
}
func (b *TokenBroker) AcquireOnBehalfOf(ctx context.Context, r TokenRequest, userAssertion string) (Authorization, error) {
	a, e := b.validate(r, true)
	if e != nil {
		return Authorization{}, e
	}
	if userAssertion == "" || len(userAssertion) > 64<<10 || strings.ContainsAny(userAssertion, "\r\n\x00 ") {
		return Authorization{}, errors.New("bounded user bearer assertion required")
	}
	client, e := b.client(userAssertion)
	if e != nil {
		return Authorization{}, errors.New("sidecar client unavailable")
	}
	validated, e := client.Do(ctx, "GET", "Validate", nil, nil)
	if e != nil {
		return Authorization{}, errors.New("sidecar rejected incoming user assertion")
	}
	var document struct {
		Protocol string                     `json:"protocol"`
		Claims   map[string]json.RawMessage `json:"claims"`
	}
	if json.Unmarshal(validated.Body, &document) != nil || !strings.EqualFold(document.Protocol, "Bearer") || !sameField(document.Claims, "tid", b.config.TenantID) || !sameField(document.Claims, "oid", r.UserObjectID) || !claimAudience(document.Claims["aud"], b.config.Blueprint.AppID) {
		return Authorization{}, errors.New("validated delegated tenant, user or blueprint audience mismatch")
	}
	result, e := client.Do(ctx, "GET", "AuthorizationHeader/"+r.Service, url.Values{"AgentIdentity": {a.AppID}}, nil)
	return authorizationResult(result, e)
}
func claimAudience(raw json.RawMessage, want string) bool {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.EqualFold(value, want)
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil {
		return false
	}
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
func authorizationResult(r nativeapi.Result, e error) (Authorization, error) {
	if e != nil {
		return Authorization{}, errors.New("Entra SDK token acquisition failed")
	}
	var response struct {
		Header string `json:"authorizationHeader"`
	}
	if json.Unmarshal(r.Body, &response) != nil || !strings.HasPrefix(response.Header, "Bearer ") || len(response.Header) <= len("Bearer ") || len(response.Header) > 64<<10 || strings.ContainsAny(response.Header, "\r\n\x00") {
		return Authorization{}, errors.New("Entra SDK returned an invalid authorization header")
	}
	return Authorization{header: response.Header}, nil
}
