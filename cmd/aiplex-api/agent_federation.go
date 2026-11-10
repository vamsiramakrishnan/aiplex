package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// agentFederation wires cross-cloud agent identity (AgentPlex section 2).
//
// The API (presets, bindings, plans, dry-run verification) is always mounted.
// Live authentication with bindings is opt-in:
//
//	AIPLEX_AGENT_FEDERATION=enabled       turn on live authentication
//	AIPLEX_TENANT_ID                      tenant whose bindings authenticate (required unless development mode)
//	AIPLEX_AGENT_FEDERATION_AUDIENCE      optional default audience suggested by presets (falls back to PUBLIC_URL)
//
// When enabled, bindings are a fallback identity source behind the statically
// configured ones, through the same WIFValidator and access resolution.
func agentFederation(cfg *config.Config, agents registry.AgentStore, store resources.Store, network endpointpolicy.Config, tenant api.TenantResolver, wif *auth.WIFValidator) (*api.AgentFederationHandler, error) {
	if network.AllowLoopback && !cfg.DevelopmentMode {
		return nil, fmt.Errorf("loopback JWKS endpoints require development mode")
	}
	keys := auth.NewJWKSKeyResolver(network)
	engine := &agentfederation.Engine{Keys: keys, Agents: api.AgentLookup(agents)}
	bindings := agentfederation.NewStore(store)
	audience := strings.TrimSpace(os.Getenv("AIPLEX_AGENT_FEDERATION_AUDIENCE"))
	if audience == "" {
		audience = strings.TrimSuffix(cfg.PublicURL, "/")
	}
	handler := &api.AgentFederationHandler{Bindings: bindings, Agents: agents, Engine: engine, Tenant: tenant, DefaultAudience: audience, PublicURL: cfg.PublicURL}
	handler.ValidateJWKS = func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		policy := network
		policy.AllowedOrigins = []string{u.Scheme + "://" + u.Host}
		p, err := endpointpolicy.New(policy)
		if err != nil {
			return err
		}
		return p.ValidateURL(raw)
	}
	switch mode := strings.TrimSpace(os.Getenv("AIPLEX_AGENT_FEDERATION")); mode {
	case "", "disabled":
	case "enabled":
		liveTenant := cfg.TenantID
		if liveTenant == "" && cfg.DevelopmentMode {
			liveTenant = "development"
		}
		if liveTenant == "" {
			return nil, fmt.Errorf("AIPLEX_AGENT_FEDERATION=enabled requires AIPLEX_TENANT_ID")
		}
		wif.WithFederation(&auth.AgentFederation{Tenant: liveTenant, Bindings: bindings, Engine: engine})
		handler.LiveEnabled = true
	default:
		return nil, fmt.Errorf("AIPLEX_AGENT_FEDERATION must be enabled or disabled")
	}
	return handler, nil
}
