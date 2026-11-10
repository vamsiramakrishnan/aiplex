package main

import (
	"context"
	"fmt"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// seedMore adds the data the Console is walked against: a second and third
// model surface (Anthropic and Gemini wires served by the same mock), prices,
// several access points (an alias, a QoS scope, a private one), a registered
// agent with an identity binding and a user identity provider.
func seedMore(ctx context.Context, s *devstack.Stack) (map[string]string, error) {
	extra := map[string]string{}
	mk := func(issuer, audience string) (*devstack.Issuer, string, error) {
		i, err := devstack.NewIssuer(issuer, audience)
		if err != nil {
			return nil, "", err
		}
		_, url, err := devstack.Listen(i.Handler())
		return i, url + "/.well-known/jwks.json", err
	}
	agents, agentsJWKS, err := mk("https://agents.console.example", "aiplex-agents")
	if err != nil {
		return nil, err
	}
	users, usersJWKS, err := mk("https://login.console.example", "aiplex-users")
	if err != nil {
		return nil, err
	}
	extra["workload_token"], _ = agents.Mint(map[string]any{"sub": "workload-1"}, 6*time.Hour)
	extra["user_token"], _ = users.Mint(map[string]any{"sub": "alice", "email": "alice@console.example", "groups": []string{"eng"}}, 6*time.Hour)

	api := devstack.API{URL: s.APIURL, Token: s.Token}
	for _, sf := range []struct{ id, provider, model string }{
		{"claude-models", "anthropic", "claude-haiku-4-5"},
		{"gemini-models", "gemini", "gemini-2.5-flash"},
	} {
		spec, _, err := devstack.SurfaceSpec(sf.id, sf.provider, s.ModelsURL, sf.model)
		if err != nil {
			return nil, err
		}
		if err = api.Create(ctx, "/api/v1/llm/surfaces/"+sf.id, spec); err != nil {
			return nil, fmt.Errorf("surface %s: %w", sf.id, err)
		}
		if err = api.Create(ctx, "/api/v1/usage/prices/"+sf.model, devstack.MockPrice(sf.model)); err != nil {
			return nil, fmt.Errorf("price %s: %w", sf.model, err)
		}
	}
	model := func(name, ref, native string) map[string]any {
		return map[string]any{"name": name, "surface_ref": ref, "native_model": native}
	}
	tools := []map[string]any{{"connection_ref": devstack.SeedConnection}}
	aps := []map[string]any{
		{"id": "team-research", "display_name": "Research team", "caller_kinds": []string{"user", "agent"}, "subjects": map[string]any{},
			"models": []map[string]any{model("fast", "local-models", "mock-gpt"), model("smart", "claude-models", "claude-haiku-4-5"), model("gemini-2.5-flash", "gemini-models", "gemini-2.5-flash")},
			"tools":  tools},
		{"id": "premium-chat", "display_name": "Premium chat (QoS per caller)", "caller_kinds": []string{"user", "agent"}, "subjects": map[string]any{},
			"models": []map[string]any{model("mock-gpt", "local-models", "mock-gpt")},
			"qos":    map[string]any{"class": "interactive", "weight": 10, "scope": "principal", "limits": map[string]any{"requests_per_minute": 120}}},
		{"id": "batch-pool", "display_name": "Shared batch pool", "caller_kinds": []string{"agent", "client"}, "subjects": map[string]any{},
			"models": []map[string]any{model("mock-gpt", "local-models", "mock-gpt")},
			"qos":    map[string]any{"class": "batch", "weight": 1, "scope": "access_point", "limits": map[string]any{"requests_per_minute": 600}}},
		{"id": "private", "display_name": "Private (someone else only)", "caller_kinds": []string{"user"}, "subjects": map[string]any{"principals": []string{"principal-someone-else"}},
			"models": []map[string]any{model("mock-gpt", "local-models", "mock-gpt")}},
	}
	for _, ap := range aps {
		if err := api.Create(ctx, "/api/v1/access-points/"+ap["id"].(string), ap); err != nil {
			return nil, fmt.Errorf("access point %v: %w", ap["id"], err)
		}
	}
	if err := api.Do(ctx, "POST", "/api/v1/agents", nil, map[string]any{
		"client_id": "tutor-agent", "display_name": "Tutor agent", "description": "Console e2e agent",
		"auth_method": "client_credentials", "grant_types": []string{"client_credentials"}, "allowed_scopes": []string{"llm:model:mock-gpt", "mcp:tools:*"},
	}, nil); err != nil {
		return nil, fmt.Errorf("register agent: %w", err)
	}
	if err := api.Do(ctx, "PUT", "/api/v1/agents/tutor-agent/identities/ci", nil, map[string]any{
		"preset": "oidc", "display_name": "CI workload",
		"inputs": map[string]string{"issuer": agents.IssuerURL(), "jwks_url": agentsJWKS, "subject": "workload-1", "audience": "aiplex-agents"},
	}, nil); err != nil {
		return nil, fmt.Errorf("agent identity: %w", err)
	}
	if err := api.Do(ctx, "PUT", "/api/v1/identity/user-providers/corp", nil, map[string]any{
		"preset": "oidc", "display_name": "Corp IdP",
		"inputs": map[string]string{"issuer": users.IssuerURL(), "jwks_url": usersJWKS, "audience": "aiplex-users", "groups_from": "claim", "group_claim": "groups"},
	}, nil); err != nil {
		return nil, fmt.Errorf("user provider: %w", err)
	}
	return extra, nil
}
