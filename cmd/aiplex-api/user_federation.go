package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
	"github.com/vamsiramakrishnan/aiplex/pkg/directory"
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
//
// directories (optional) resolves groups for providers with a directory block,
// for live authentication and dry-runs alike; without it those providers report
// groups as unavailable.
func userFederation(cfg *config.Config, store resources.Store, network endpointpolicy.Config, tenant api.TenantResolver, wif *auth.WIFValidator, directories ...*auth.UserDirectories) (*api.UserIdentityProviderHandler, error) {
	if network.AllowLoopback && !cfg.DevelopmentMode {
		return nil, fmt.Errorf("loopback JWKS endpoints require development mode")
	}
	if len(directories) > 1 {
		return nil, fmt.Errorf("at most one directory resolver")
	}
	keys := auth.NewJWKSKeyResolver(network)
	engine := &userfederation.Engine{Keys: auth.UserKeyResolver{Keys: keys}}
	providers := userfederation.NewStore(store)
	handler := &api.UserIdentityProviderHandler{Providers: providers, Engine: engine, Tenant: tenant, PublicURL: cfg.PublicURL}
	if len(directories) == 1 && directories[0] != nil {
		engine.Directory = directories[0]
		handler.Directories = directories[0]
	}
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

// userDirectories wires directory group resolution for user identity
// providers onto the deployment's outbound policy and credentials:
//
//   - HTTP through the outbound endpoint-policy client (vendor origins such as
//     https://graph.microsoft.com, https://login.microsoftonline.com,
//     https://cloudidentity.googleapis.com or the Okta org must be allowed);
//   - secret references resolved in the tenant's tool secret namespace
//     (Secret Manager, or AIPLEX_TOOL_SECRET_<REF> in development mode);
//   - Google access tokens from gcp-adc identity profiles (Application Default
//     Credentials), bound to their allowed_origins.
//
//	AIPLEX_USER_DIRECTORY_CACHE_TTL   resolved-view cache lifetime (default 1m, at most 5m)
//
// Without an outbound endpoint policy there is no resolver and providers with
// a directory report groups as unavailable (fail closed).
func userDirectories(runtime *connections.Runtime, sm *secrets.Manager, development bool) (*auth.UserDirectories, func(tenant, ref string) string, error) {
	ttl := time.Minute
	if raw := strings.TrimSpace(os.Getenv("AIPLEX_USER_DIRECTORY_CACHE_TTL")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < 0 || parsed > 5*time.Minute {
			return nil, nil, fmt.Errorf("AIPLEX_USER_DIRECTORY_CACHE_TTL must be a duration of at most 5m")
		}
		ttl = parsed
	}
	if runtime == nil || runtime.Policy == nil || runtime.HTTP == nil {
		return nil, nil, nil
	}
	var resolver directory.SecretResolver
	var secretName func(tenant, ref string) string
	switch {
	case sm != nil:
		resolver = connections.ToolSecrets{Manager: sm}
		secretName = connections.ToolSecretID
	case development:
		resolver = connections.DevelopmentToolSecrets{}
	}
	deps := directory.Deps{
		HTTP:             runtime.HTTP,
		Secrets:          resolver,
		CheckDestination: runtime.CheckDestination,
		GoogleToken: func(ctx context.Context, tenant, ref string) (string, error) {
			identity, err := runtime.Identity(tenant, ref)
			if err != nil || identity.Type != "gcp-adc" {
				return "", fmt.Errorf("google-cloud-identity directories require a gcp-adc identity profile")
			}
			return runtime.Token(ctx, tenant, ref)
		},
	}
	return &auth.UserDirectories{Deps: deps, CacheTTL: ttl}, secretName, nil
}
