package main

import (
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
)

// Route authorization, by mounted pattern. Anything not listed here is
// RouteOperator (admin or deployer) and deployment-wide, so a new route is
// closed to ordinary callers and to other tenants until it is classified.
//
// Ordinary verified callers (no role, viewer, agent) may reach only what they
// need to use the product: their own identity, the access points that admit
// them, their own usage and quota attempts, the invocation routes (which check
// per-action grants) and static product metadata.
var routeRules = func() []api.RouteRule {
	var (
		get    = []string{http.MethodGet}
		post   = []string{http.MethodPost}
		writes = []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	)
	tenantRule := func(methods []string, pattern string, access api.RouteAccess) api.RouteRule {
		return api.RouteRule{Methods: methods, Pattern: pattern, Access: access, TenantScoped: true}
	}
	tenantPrefix := func(methods []string, prefix string, access api.RouteAccess) api.RouteRule {
		return api.RouteRule{Methods: methods, Pattern: prefix, Prefix: true, Access: access, TenantScoped: true}
	}
	globalRule := func(methods []string, pattern string, access api.RouteAccess) api.RouteRule {
		return api.RouteRule{Methods: methods, Pattern: pattern, Access: access}
	}
	return []api.RouteRule{
		// Process health and login redirect.
		tenantRule(get, "/healthz", api.RoutePublic),
		tenantRule(get, "/readyz", api.RoutePublic),
		tenantRule(get, "/auth/login", api.RoutePublic),
		// RFC 9728 protected-resource metadata and client ID metadata documents.
		tenantPrefix(get, "/.well-known/oauth-protected-resource/", api.RoutePublic),
		tenantPrefix(get, "/ap/{apid}/.well-known/oauth-protected-resource", api.RoutePublic),
		tenantPrefix(nil, "/mcp/client-metadata/", api.RoutePublic),

		// Shared-secret callbacks; each route group carries its own BearerToken guard.
		tenantRule(post, "/auth/token-hook", api.RouteInternal),
		tenantPrefix(post, "/internal/", api.RouteInternal),
		// Prometheus scrape: the AIPLEX_METRICS_TOKEN bearer secret (503 when
		// unset), so an in-cluster scraper needs no AIPlex identity. Metrics
		// carry no principal, request or trace identifiers.
		tenantRule(get, "/metrics", api.RouteInternal),

		// Access points: the handler admits by caller kind and subjects.
		tenantPrefix(nil, "/ap/{apid}/", api.RouteAuthenticated),

		// Hydra consent: the handler binds the consent subject to the caller.
		tenantRule([]string{http.MethodGet, http.MethodPost}, "/auth/consent", api.RouteAuthenticated),
		globalRule(nil, "/auth/users/{userId}/scopes", api.RouteAdmin),

		// Self-service reads and static product metadata.
		tenantRule(get, "/api/v1/iam/whoami", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/iam/roles", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/modules", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/agents/identity-presets", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/identity/user-provider-presets", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/mcp/auth-types", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/llm/operations", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/clients/profiles", api.RouteAuthenticated),
		// Access points: operators see the resource; others only the access
		// points that admit them, without subject lists (handler view).
		tenantRule(get, "/api/v1/access-points", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/access-points/{id}", api.RouteAuthenticated),
		tenantRule([]string{http.MethodPut, http.MethodDelete}, "/api/v1/access-points/{id}", api.RouteAdmin),
		tenantRule(post, "/api/v1/access-points/{id}/quota-plan", api.RouteAdmin),
		// Client configuration for an access point: operators read it in full;
		// any other verified caller only for an access point that admits it,
		// without operator-only detail (handler: OverviewHandler.ConnectForCaller).
		tenantRule(get, "/api/v1/overview/access-points/{id}/connect", api.RouteAuthenticated),
		// Usage: admins read the tenant; everyone else only their own records.
		tenantRule(get, "/api/v1/usage/records", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/usage/records/{id}", api.RouteAuthenticated),
		tenantRule(get, "/api/v1/usage/summary", api.RouteAuthenticated),
		tenantPrefix(writes, "/api/v1/usage/", api.RouteAdmin),
		// Quota attempts: the caller's own, or the aiplex:quota:read grant.
		tenantRule(get, "/api/v1/quota/attempts/{id}", api.RouteAuthenticated),

		// Invocation: each handler checks its per-resource action grant before
		// any credential is used.
		tenantRule(nil, "/api/v1/llm/native/{id}/*", api.RouteAuthenticated),
		tenantRule(nil, "/api/v1/llm/gateway/{id}/*", api.RouteAuthenticated),
		tenantRule(post, "/api/v1/a2a/connections/{id}/invoke", api.RouteAuthenticated),
		tenantRule(post, "/api/v1/mcp/connections/{id}/invoke", api.RouteAuthenticated),
		// Policy-rule dry run: describes the connection's rules, so operators only.
		tenantRule(post, "/api/v1/mcp/connections/{id}/rules/test", api.RouteOperator),
		tenantRule([]string{http.MethodGet, http.MethodPost, http.MethodDelete}, "/api/v1/mcp/connections/{id}/authorization", api.RouteAuthenticated),
		tenantRule(post, "/api/v1/mcp/connections/{id}/authorization/complete", api.RouteAuthenticated),
		tenantRule(post, "/api/v1/iam/native/bindings/{id}/read", api.RouteAuthenticated),

		// Tenant-wide administrative reads.
		tenantRule(get, "/api/v1/llm/batches", api.RouteAdmin),
		tenantRule(get, "/api/v1/llm/surfaces/{id}/capacity", api.RouteAdmin),
		tenantPrefix(writes, "/api/v1/iam/native/", api.RouteAdmin),

		// Tenant-partitioned configuration: admin or deployer (the default
		// access), plus each handler's own action grants.
		tenantPrefix(nil, "/api/v1/access-points", api.RouteOperator),
		tenantPrefix(nil, "/api/v1/overview/", api.RouteOperator),
		tenantPrefix(nil, "/api/v1/usage/", api.RouteOperator),
		tenantPrefix(nil, "/api/v1/llm/surfaces", api.RouteOperator),
		tenantPrefix(nil, "/api/v1/a2a/connections", api.RouteOperator),
		tenantPrefix(nil, "/api/v1/mcp/", api.RouteOperator),
		tenantPrefix(nil, "/api/v1/clients/", api.RouteOperator),
		tenantPrefix(nil, "/api/v1/network/", api.RouteOperator),
		tenantPrefix(nil, "/api/v1/quota/", api.RouteOperator),
		// Request-hook policies: operators read, write and dry-run them.
		// Callers never read them: a policy names detection rules, patterns
		// and sidecar endpoints, which would help a caller shape content to
		// evade it. A caller learns of a policy only from a refusal's safe
		// public message.
		tenantPrefix(nil, "/api/v1/hooks/", api.RouteOperator),
		tenantPrefix(nil, "/api/v1/iam/native/", api.RouteOperator),

		// Deployment-wide administration (refused to other tenants).
		globalRule(nil, "/api/v1/iam/role-bindings", api.RouteAdmin),
		globalRule(nil, "/api/v1/iam/role-bindings/{id}", api.RouteAdmin),
		globalRule(post, "/api/v1/iam/validate-principal", api.RouteAdmin),
		{Pattern: "/api/v1/dashboard/", Prefix: true, Access: api.RouteAdmin},
		{Pattern: "/api/v1/llm/usage", Prefix: true, Access: api.RouteAdmin},
		{Pattern: "/api/v1/agents/{clientId}/identities/{id}", Methods: []string{http.MethodPut, http.MethodDelete}, Access: api.RouteAdmin},
		{Pattern: "/api/v1/identity/user-providers/{id}", Methods: []string{http.MethodPut, http.MethodDelete}, Access: api.RouteAdmin},
		{Pattern: "/api/v1/identity/user-providers/{id}/membership/invalidate", Access: api.RouteAdmin},
		// Per-instance discovery documents a caller needs before invoking an
		// agent or skill server.
		globalRule(get, "/a2a/{instanceId}/.well-known/agent.json", api.RouteAuthenticated),
		globalRule(get, "/skills/{instanceId}/.well-known/skills.json", api.RouteAuthenticated),
	}
}()

// deploymentScope reports whether a verified caller may act on
// deployment-wide state: callers with no explicit tenant grant, or whose one
// tenant grant is the deployment's own tenant.
func deploymentScope(cfg *config.Config) func(*http.Request) bool {
	home := cfg.TenantID
	if home == "" && cfg.DevelopmentMode {
		home = "development"
	}
	return func(r *http.Request) bool {
		access := api.GetWIFAccess(r.Context())
		if access == nil {
			return true
		}
		tenants := map[string]bool{}
		for _, scope := range access.Scopes {
			if strings.HasPrefix(scope, "aiplex:tenant:") {
				tenants[strings.TrimPrefix(scope, "aiplex:tenant:")] = true
			}
		}
		if len(tenants) == 0 {
			return true
		}
		return len(tenants) == 1 && home != "" && tenants[home]
	}
}

func routePolicy(cfg *config.Config) api.RoutePolicyConfig {
	return api.RoutePolicyConfig{Rules: routeRules, Development: cfg.DevelopmentMode, DeploymentScoped: deploymentScope(cfg)}
}
