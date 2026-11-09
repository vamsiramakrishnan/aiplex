package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

// userFederation wires workforce user identity providers.
//
// The API (presets, providers, plans, dry-run verification) is always mounted.
// Live authentication with providers is opt-in:
//
//	AIPLEX_USER_FEDERATION=enabled   turn on live authentication
//	AIPLEX_TENANT_ID                 tenant whose providers authenticate (required unless development mode)
//
// When enabled, providers are a fallback identity source behind the statically
// configured ones (and alongside agent identity bindings; a token claimed by
// both is rejected), through the same WIFValidator and access resolution.
func userFederation(cfg *config.Config, store resources.Store, network endpointpolicy.Config, tenant api.TenantResolver, wif *auth.WIFValidator) (*api.UserIdentityProviderHandler, error) {
	if network.AllowLoopback && !cfg.DevelopmentMode {
		return nil, fmt.Errorf("loopback JWKS endpoints require development mode")
	}
	keys := auth.NewJWKSKeyResolver(network)
	engine := &userfederation.Engine{Keys: auth.UserKeyResolver{Keys: keys}}
	providers := userfederation.NewStore(store)
	handler := &api.UserIdentityProviderHandler{Providers: providers, Engine: engine, Tenant: tenant, PublicURL: cfg.PublicURL}
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
	switch mode := strings.TrimSpace(os.Getenv("AIPLEX_USER_FEDERATION")); mode {
	case "", "disabled":
	case "enabled":
		liveTenant := cfg.TenantID
		if liveTenant == "" && cfg.DevelopmentMode {
			liveTenant = "development"
		}
		if liveTenant == "" {
			return nil, fmt.Errorf("AIPLEX_USER_FEDERATION=enabled requires AIPLEX_TENANT_ID")
		}
		wif.WithUserFederation(&auth.UserFederation{Tenant: liveTenant, Providers: providers, Engine: engine})
		handler.LiveEnabled = true
	default:
		return nil, fmt.Errorf("AIPLEX_USER_FEDERATION must be enabled or disabled")
	}
	return handler, nil
}
