package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// RouteAccess is the coarse authorization class of a mounted route. Handlers
// still apply finer action grants, ownership and admission on top.
type RouteAccess int

const (
	// RouteOperator admits the admin and deployer roles. It is the default for
	// every route without an explicit rule, so a newly mounted route is never
	// readable by ordinary callers by accident.
	RouteOperator RouteAccess = iota
	// RoutePublic needs no identity.
	RoutePublic
	// RouteInternal is authenticated by the route's own shared-secret guard.
	RouteInternal
	// RouteAuthenticated admits any verified identity. The handler must scope
	// the result to the caller or enforce an action grant.
	RouteAuthenticated
	// RouteAdmin admits only the admin role.
	RouteAdmin
)

func (a RouteAccess) String() string {
	switch a {
	case RoutePublic:
		return "public"
	case RouteInternal:
		return "internal"
	case RouteAuthenticated:
		return "authenticated"
	case RouteAdmin:
		return "admin"
	}
	return "operator"
}

// RouteRule classifies routes by their mounted chi pattern (for example
// "/api/v1/usage/records/{id}"), never by the raw request path, so encoded or
// dot-segment paths cannot select a weaker rule than the handler that serves
// them.
type RouteRule struct {
	// Methods restricts the rule to these methods; empty matches all.
	Methods []string
	// Pattern is an exact route pattern, or a prefix when Prefix is set.
	Pattern string
	Prefix  bool
	Access  RouteAccess
	// TenantScoped marks routes whose data is partitioned by the caller's
	// verified tenant. Other routes act on deployment-wide state and are
	// refused to identities bound to a different tenant.
	TenantScoped bool
}

func (r RouteRule) matches(method, pattern string) bool {
	if len(r.Methods) > 0 {
		found := false
		for _, m := range r.Methods {
			if m == method {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if r.Prefix {
		return strings.HasPrefix(pattern, r.Pattern)
	}
	return pattern == r.Pattern
}

// RoutePolicyConfig is the declarative route authorization policy.
type RoutePolicyConfig struct {
	// Rules are evaluated in order; the first match wins.
	Rules []RouteRule
	// Development admits requests without a verified identity, as the rest of
	// the server does in explicit loopback-only development mode.
	Development bool
	// DeploymentScoped reports whether a verified caller may act on
	// deployment-wide (non tenant-partitioned) state. Nil admits everyone.
	DeploymentScoped func(*http.Request) bool
}

// Lookup returns the rule for a method and mounted pattern. HEAD follows GET.
func (c RoutePolicyConfig) Lookup(method, pattern string) RouteRule {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	for _, rule := range c.Rules {
		if rule.matches(method, pattern) {
			return rule
		}
	}
	return RouteRule{Pattern: pattern, Access: RouteOperator}
}

// MatchedRoutePattern resolves the pattern router would dispatch the request
// to, using the same path chi routes on. ok is false when no route matches.
func MatchedRoutePattern(router chi.Routes, r *http.Request) (string, bool) {
	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	if path == "" {
		path = "/"
	}
	rctx := chi.NewRouteContext()
	if !router.Match(rctx, r.Method, path) {
		return "", false
	}
	return rctx.RoutePattern(), true
}

// RoutePolicy enforces cfg in front of router. It must run after identity
// extraction (WIFAuthMode). Unmatched requests pass through so the router
// answers 404 or 405 itself.
func RoutePolicy(router chi.Routes, cfg RoutePolicyConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pattern, ok := MatchedRoutePattern(router, r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			rule := cfg.Lookup(r.Method, pattern)
			if rule.Access == RoutePublic || rule.Access == RouteInternal {
				next.ServeHTTP(w, r)
				return
			}
			// Identity-dependent responses must not be stored by shared or
			// browser caches. Handlers may still set their own value.
			w.Header().Set("Cache-Control", "no-store")
			access := GetWIFAccess(r.Context())
			if access == nil || GetWIFIdentity(r.Context()) == nil {
				if cfg.Development {
					next.ServeHTTP(w, r)
					return
				}
				Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
				return
			}
			if !rule.TenantScoped && cfg.DeploymentScoped != nil && !cfg.DeploymentScoped(r) {
				Error(w, r, http.StatusForbidden, "DEPLOYMENT_SCOPE_REQUIRED", "this route acts on deployment-wide state and is not available to identities bound to another tenant")
				return
			}
			var allowed []models.IAMRole
			switch rule.Access {
			case RouteAuthenticated:
				next.ServeHTTP(w, r)
				return
			case RouteAdmin:
				allowed = []models.IAMRole{models.RoleAdmin}
			default:
				allowed = []models.IAMRole{models.RoleAdmin, models.RoleDeployer}
			}
			if !HasRole(access, allowed...) {
				Error(w, r, http.StatusForbidden, "ROLE_REQUIRED", "insufficient role; required one of: "+formatRoles(allowed))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// HasRole reports whether access carries at least one of roles.
func HasRole(access *models.ResolvedAccess, roles ...models.IAMRole) bool {
	if access == nil {
		return false
	}
	for _, have := range access.Roles {
		for _, want := range roles {
			if have == want {
				return true
			}
		}
	}
	return false
}

// privilegedOrDevelopment reports whether the request may use an
// administrative view: a verified caller holding one of roles, or an
// unverified request in explicit local development mode (the same rule
// RequireRole applies).
func privilegedOrDevelopment(r *http.Request, development bool, roles ...models.IAMRole) bool {
	access := GetWIFAccess(r.Context())
	if access == nil {
		if development {
			return true
		}
		dev, _ := r.Context().Value(keyDevelopment).(bool)
		return dev
	}
	return HasRole(access, roles...)
}
