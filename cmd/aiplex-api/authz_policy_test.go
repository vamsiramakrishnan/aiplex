package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// Intended authorization policy for every mounted route, written
// independently of the production rule list in route_policy.go. The test
// fails when a route is mounted without an entry here.
//
// Value: "<class>/<scope>"
//
//	class: public   no identity needed
//	       internal shared-secret callback; never reachable with a user identity
//	       auth     any verified identity (handler scopes data or checks grants)
//	       op       admin or deployer
//	       admin    admin only
//	scope: t        partitioned by the caller's tenant (another tenant's
//	                identity may call it and sees only its own tenant)
//	       g        deployment-wide state: refused to identities bound to
//	                another tenant
//
// A trailing "+grant" documents an additional handler-level action grant.
var intendedRoutePolicy = map[string]string{
	// Health, login, metadata.
	"GET /healthz":    "public/t",
	"GET /readyz":     "public/t",
	"GET /auth/login": "public/t",
	"GET /.well-known/oauth-protected-resource/ap/{apid}/mcp":              "public/t",
	"GET /.well-known/oauth-protected-resource/ap/{apid}/mcp/{connection}": "public/t",
	"GET /ap/{apid}/.well-known/oauth-protected-resource":                  "public/t",
	"GET /ap/{apid}/.well-known/oauth-protected-resource/mcp":              "public/t",
	"GET /ap/{apid}/.well-known/oauth-protected-resource/mcp/{connection}": "public/t",

	// Internal callbacks (bearer shared secrets).
	"POST /auth/token-hook":                       "internal/t",
	"POST /internal/tape/events":                  "internal/t",
	"POST /internal/policy-denials":               "internal/t",
	"POST /internal/projections/rebuild/{run_id}": "internal/t",
	"GET /metrics":                                "internal/t",

	// Hydra consent and admin user scopes.
	"GET /auth/consent":               "auth/t",
	"POST /auth/consent":              "auth/t",
	"GET /auth/users/{userId}/scopes": "admin/g",
	"PUT /auth/users/{userId}/scopes": "admin/g",

	// Deployment-wide discovery and streams.
	"GET /events/stream":                               "op/g",
	"GET /v0.1/servers":                                "op/g",
	"GET /a2a/{instanceId}/.well-known/agent.json":     "auth/g",
	"GET /skills/{instanceId}/.well-known/skills.json": "auth/g",

	// Access-point client endpoints: admission is enforced by the handler.
	"POST /ap/{apid}/v1/chat/completions":                                             "auth/t",
	"POST /ap/{apid}/v1/responses":                                                    "auth/t",
	"GET /ap/{apid}/v1/responses/{rid}":                                               "auth/t",
	"DELETE /ap/{apid}/v1/responses/{rid}":                                            "auth/t",
	"POST /ap/{apid}/v1/responses/{rid}/cancel":                                       "auth/t",
	"POST /ap/{apid}/v1/messages":                                                     "auth/t",
	"POST /ap/{apid}/v1/messages/count_tokens":                                        "auth/t",
	"POST /ap/{apid}/v1/embeddings":                                                   "auth/t",
	"POST /ap/{apid}/v1/moderations":                                                  "auth/t",
	"POST /ap/{apid}/v1/images/generations":                                           "auth/t",
	"POST /ap/{apid}/v1/images/edits":                                                 "auth/t",
	"POST /ap/{apid}/v1/audio/speech":                                                 "auth/t",
	"POST /ap/{apid}/v1/audio/transcriptions":                                         "auth/t",
	"POST /ap/{apid}/v1/audio/translations":                                           "auth/t",
	"GET /ap/{apid}/v1/models":                                                        "auth/t",
	"GET /ap/{apid}/v1/realtime":                                                      "auth/t",
	"POST /ap/{apid}/v1beta/models/{target}":                                          "auth/t",
	"GET /ap/{apid}/v1beta/models":                                                    "auth/t",
	"POST /ap/{apid}/v1beta/cachedContents":                                           "auth/t",
	"GET /ap/{apid}/v1beta/cachedContents":                                            "auth/t",
	"GET /ap/{apid}/v1beta/cachedContents/{rid}":                                      "auth/t",
	"PATCH /ap/{apid}/v1beta/cachedContents/{rid}":                                    "auth/t",
	"DELETE /ap/{apid}/v1beta/cachedContents/{rid}":                                   "auth/t",
	"POST /ap/{apid}/v1beta/interactions":                                             "auth/t",
	"GET /ap/{apid}/v1beta/interactions/{rid}":                                        "auth/t",
	"DELETE /ap/{apid}/v1beta/interactions/{rid}":                                     "auth/t",
	"POST /ap/{apid}/v1beta/interactions/{rid}/cancel":                                "auth/t",
	"POST /ap/{apid}/model/{target}/converse":                                         "auth/t",
	"POST /ap/{apid}/model/{target}/converse-stream":                                  "auth/t",
	"POST /ap/{apid}/model/{target}/invoke":                                           "auth/t",
	"POST /ap/{apid}/model/{target}/invoke-with-response-stream":                      "auth/t",
	"GET /ap/{apid}/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent": "auth/t",
	// Azure OpenAI wire shapes of the same endpoints.
	"POST /ap/{apid}/openai/v1/chat/completions":                       "auth/t",
	"POST /ap/{apid}/openai/v1/responses":                              "auth/t",
	"GET /ap/{apid}/openai/v1/responses/{rid}":                         "auth/t",
	"DELETE /ap/{apid}/openai/v1/responses/{rid}":                      "auth/t",
	"POST /ap/{apid}/openai/v1/responses/{rid}/cancel":                 "auth/t",
	"POST /ap/{apid}/openai/v1/embeddings":                             "auth/t",
	"POST /ap/{apid}/openai/deployments/{deployment}/chat/completions": "auth/t",
	"POST /ap/{apid}/openai/deployments/{deployment}/embeddings":       "auth/t",
	"GET /ap/{apid}/v0.1/servers":                                      "auth/t",
	"GET /ap/{apid}/skills":                                            "auth/t",
	// MCP Streamable HTTP is mounted for every method.
	"* /ap/{apid}/mcp":              "auth/t",
	"* /ap/{apid}/mcp/{connection}": "auth/t",

	// Self-service and static metadata.
	"GET /api/v1/iam/whoami":                     "auth/t",
	"GET /api/v1/iam/roles":                      "auth/t",
	"GET /api/v1/modules":                        "auth/t",
	"GET /api/v1/agents/identity-presets":        "auth/t",
	"GET /api/v1/identity/user-provider-presets": "auth/t",
	"GET /api/v1/mcp/auth-types":                 "auth/t",
	"GET /api/v1/llm/operations":                 "auth/t",
	"GET /api/v1/clients/profiles":               "auth/t",

	// Access points: operators read the resource, others a filtered view.
	"GET /api/v1/access-points":                   "auth/t",
	"GET /api/v1/access-points/{id}":              "auth/t",
	"PUT /api/v1/access-points/{id}":              "admin/t",
	"DELETE /api/v1/access-points/{id}":           "admin/t",
	"POST /api/v1/access-points/{id}/quota-plan":  "admin/t",
	"GET /api/v1/access-points/{id}/routing":      "op/t",
	"POST /api/v1/access-points/{id}/cache/purge": "admin/t",

	// Budgets: operators read, admins write and preview, a verified member
	// reads the status of its own budget (handler-scoped).
	"GET /api/v1/budgets":             "op/t",
	"GET /api/v1/budgets/{id}":        "op/t",
	"PUT /api/v1/budgets/{id}":        "admin/t",
	"DELETE /api/v1/budgets/{id}":     "admin/t",
	"POST /api/v1/budgets/preview":    "admin/t",
	"GET /api/v1/budgets/{id}/status": "auth/t",

	// Usage ledger.
	"GET /api/v1/usage/records":                "auth/t",
	"GET /api/v1/usage/records/{id}":           "auth/t",
	"GET /api/v1/usage/summary":                "auth/t",
	"GET /api/v1/usage/config":                 "auth/t",
	"POST /api/v1/usage/backfill":              "admin/t",
	"GET /api/v1/usage/records/{id}/feedback":  "auth/t",
	"POST /api/v1/usage/records/{id}/feedback": "auth/t",
	"POST /otlp/v1/logs":                       "auth/t",
	"POST /otlp/v1/metrics":                    "auth/t",
	"POST /otlp/v1/traces":                     "auth/t",
	"GET /api/v1/usage/prices":                 "op/t",
	"GET /api/v1/usage/prices/{id}":            "op/t",
	"PUT /api/v1/usage/prices/{id}":            "admin/t",
	"DELETE /api/v1/usage/prices/{id}":         "admin/t",

	// Request-hook policies: operator only (they describe detection rules).
	"GET /api/v1/hooks/policies":            "op/t",
	"GET /api/v1/hooks/policies/{id}":       "op/t",
	"PUT /api/v1/hooks/policies/{id}":       "op/t",
	"DELETE /api/v1/hooks/policies/{id}":    "op/t",
	"POST /api/v1/hooks/policies/{id}/test": "op/t",

	// Overview (usage roll-ups inside it are admin-only; see overview tests).
	"GET /api/v1/overview/subjects":             "op/t",
	"GET /api/v1/overview/subjects/{kind}":      "op/t",
	"GET /api/v1/overview/subjects/{kind}/{id}": "op/t",
	"GET /api/v1/overview/access-points":        "op/t",
	"GET /api/v1/overview/access-points/{id}":   "op/t",
	// Any verified caller; the handler answers only for an access point that
	// admits the caller (404 otherwise) and trims operator-only detail.
	"GET /api/v1/overview/access-points/{id}/connect": "auth/t",

	// LLMPlex.
	"GET /api/v1/llm/surfaces":               "op/t",
	"GET /api/v1/llm/surfaces/{id}":          "op/t",
	"PUT /api/v1/llm/surfaces/{id}":          "op/t",
	"DELETE /api/v1/llm/surfaces/{id}":       "op/t",
	"GET /api/v1/llm/surfaces/{id}/capacity": "admin/t",
	"GET /api/v1/llm/batches":                "admin/t",
	"* /api/v1/llm/native/{id}/*":            "auth/t+grant",
	"* /api/v1/llm/gateway/{id}/*":           "auth/t+grant",
	"GET /api/v1/llm/routes":                 "op/g",
	"GET /api/v1/llm/routes/{modelId}":       "op/g",
	"PUT /api/v1/llm/routes/{modelId}":       "op/g",
	"DELETE /api/v1/llm/routes/{modelId}":    "op/g",
	"GET /api/v1/llm/providers":              "op/g",
	"PUT /api/v1/llm/providers/{provider}":   "op/g",
	"GET /api/v1/llm/usage":                  "admin/g",
	"POST /api/v1/llm/usage":                 "admin/g",
	"GET /api/v1/llm/usage/summary":          "admin/g",

	// A2APlex.
	"GET /api/v1/a2a/connections":              "op/t",
	"GET /api/v1/a2a/connections/{id}":         "op/t",
	"PUT /api/v1/a2a/connections/{id}":         "op/t",
	"DELETE /api/v1/a2a/connections/{id}":      "op/t",
	"POST /api/v1/a2a/connections/{id}/invoke": "auth/t+grant",
	"GET /api/v1/a2a/agents":                   "op/g",
	"POST /api/v1/a2a/delegations":             "op/g",
	"GET /api/v1/a2a/delegations":              "op/g",
	"GET /api/v1/a2a/delegations/{id}":         "op/g",
	"PATCH /api/v1/a2a/delegations/{id}":       "op/g",
	"GET /api/v1/a2a/delegations/{id}/chain":   "op/g",

	// MCPlex.
	"GET /api/v1/mcp/connections":                              "op/t",
	"GET /api/v1/mcp/connections/{id}":                         "op/t",
	"PUT /api/v1/mcp/connections/{id}":                         "op/t",
	"DELETE /api/v1/mcp/connections/{id}":                      "op/t",
	"POST /api/v1/mcp/connections/{id}/registration":           "op/t",
	"POST /api/v1/mcp/connections/{id}/invoke":                 "auth/t+grant",
	"POST /api/v1/mcp/connections/{id}/rules/test":             "op/t",
	"GET /api/v1/mcp/connections/{id}/authorization":           "auth/t+grant",
	"POST /api/v1/mcp/connections/{id}/authorization":          "auth/t+grant",
	"DELETE /api/v1/mcp/connections/{id}/authorization":        "auth/t+grant",
	"POST /api/v1/mcp/connections/{id}/authorization/complete": "auth/t+grant",

	// SkillsPlex.
	"GET /api/v1/skills/servers":      "op/g",
	"GET /api/v1/skills/invocations":  "op/g",
	"POST /api/v1/skills/invocations": "op/g",

	// ClientPlex.
	"GET /api/v1/clients/registrations":            "op/t",
	"GET /api/v1/clients/registrations/{id}":       "op/t",
	"PUT /api/v1/clients/registrations/{id}":       "op/t",
	"DELETE /api/v1/clients/registrations/{id}":    "op/t",
	"POST /api/v1/clients/registrations/{id}/plan": "op/t",
	"GET /api/v1/clients/destinations":             "op/t",
	"GET /api/v1/clients/destinations/{id}":        "op/t",
	"PUT /api/v1/clients/destinations/{id}":        "op/t",
	"DELETE /api/v1/clients/destinations/{id}":     "op/t",

	// Networking and deployment.
	"GET /api/v1/network/deployment-providers/{provider}/bindings":                 "op/t",
	"POST /api/v1/network/deployment-providers/{provider}/bindings/{binding}/plan": "op/t+grant",
	"GET /api/v1/network/deployment-intents":                                       "op/t",
	"GET /api/v1/network/deployment-intents/{id}":                                  "op/t",
	"PUT /api/v1/network/deployment-intents/{id}":                                  "op/t",
	"DELETE /api/v1/network/deployment-intents/{id}":                               "op/t",
	"POST /api/v1/network/deployment-intents/{id}/plan":                            "op/t+grant",
	"GET /api/v1/network/plans":                                                    "op/t",
	"GET /api/v1/network/plans/{id}":                                               "op/t",
	"POST /api/v1/network/plans/{id}/approve":                                      "op/t+grant",
	"POST /api/v1/network/plans/{id}/execute":                                      "op/t+grant",
	"GET /api/v1/network/plans/{id}/receipt":                                       "op/t",
	"POST /api/v1/network/plans/{id}/observe":                                      "op/t+grant",
	"GET /api/v1/network/plans/{id}/observation":                                   "op/t",
	"GET /api/v1/network/profiles":                                                 "op/t",
	"GET /api/v1/network/profiles/{id}":                                            "op/t",
	"PUT /api/v1/network/profiles/{id}":                                            "op/t",
	"DELETE /api/v1/network/profiles/{id}":                                         "op/t",
	"POST /api/v1/network/profiles/{id}/plan":                                      "op/t",

	// QuotaPlex: role plus the aiplex:quota:<action> grant.
	"GET /api/v1/quota/attempts/{id}":               "auth/t",
	"GET /api/v1/quota/policy":                      "op/t+grant",
	"POST /api/v1/quota/policy/validate":            "op/t+grant",
	"POST /api/v1/quota/preview":                    "op/t+grant",
	"GET /api/v1/quota/inventory":                   "op/t+grant",
	"GET /api/v1/quota/balances/{id}":               "op/t+grant",
	"POST /api/v1/quota/loans":                      "op/t+grant",
	"GET /api/v1/quota/loans/{id}":                  "op/t+grant",
	"POST /api/v1/quota/loans/{id}/return":          "op/t+grant",
	"GET /api/v1/quota/evidence/{id}":               "op/t+grant",
	"POST /api/v1/quota/reconciliations":            "op/t+grant",
	"GET /api/v1/quota/reconciliations/{id}":        "op/t+grant",
	"POST /api/v1/quota/reconciliations/{id}/apply": "op/t+grant",

	// Native governance (identity module).
	"GET /api/v1/iam/native/providers":           "op/t+grant",
	"GET /api/v1/iam/native/bindings":            "op/t+grant",
	"GET /api/v1/iam/native/bindings/{id}":       "op/t+grant",
	"PUT /api/v1/iam/native/bindings/{id}":       "admin/t",
	"DELETE /api/v1/iam/native/bindings/{id}":    "admin/t",
	"POST /api/v1/iam/native/bindings/{id}/read": "auth/t+grant",
	"POST /api/v1/iam/native/bindings/{id}/plan": "admin/t+grant",
	"GET /api/v1/iam/native/plans/{id}":          "op/t+grant",
	"POST /api/v1/iam/native/plans/{id}/apply":   "admin/t+grant",
	"GET /api/v1/iam/native/receipts/{id}":       "op/t+grant",

	// IAM (deployment-wide).
	"GET /api/v1/iam/role-bindings":         "admin/g",
	"POST /api/v1/iam/role-bindings":        "admin/g",
	"GET /api/v1/iam/role-bindings/{id}":    "admin/g",
	"PUT /api/v1/iam/role-bindings/{id}":    "admin/g",
	"DELETE /api/v1/iam/role-bindings/{id}": "admin/g",
	"POST /api/v1/iam/validate-principal":   "admin/g",

	// Agents registry and identity federation (live federation is bound to
	// the deployment tenant, so these are deployment-wide).
	"GET /api/v1/agents":                                              "op/g",
	"POST /api/v1/agents":                                             "op/g",
	"GET /api/v1/agents/{clientId}":                                   "op/g",
	"DELETE /api/v1/agents/{clientId}":                                "op/g",
	"GET /api/v1/agents/{clientId}/permissions":                       "op/g",
	"POST /api/v1/agents/identities/verify":                           "op/g",
	"GET /api/v1/agents/{clientId}/identities":                        "op/g",
	"GET /api/v1/agents/{clientId}/identities/{id}":                   "op/g",
	"PUT /api/v1/agents/{clientId}/identities/{id}":                   "admin/g",
	"DELETE /api/v1/agents/{clientId}/identities/{id}":                "admin/g",
	"POST /api/v1/agents/{clientId}/identities/{id}/plan":             "op/g",
	"POST /api/v1/identity/user-providers/verify":                     "op/g",
	"GET /api/v1/identity/user-providers":                             "op/g",
	"GET /api/v1/identity/user-providers/{id}":                        "op/g",
	"PUT /api/v1/identity/user-providers/{id}":                        "admin/g",
	"DELETE /api/v1/identity/user-providers/{id}":                     "admin/g",
	"POST /api/v1/identity/user-providers/{id}/plan":                  "op/g",
	"POST /api/v1/identity/user-providers/{id}/membership/invalidate": "admin/g",

	// Catalog, instances, dashboard, runs (deployment-wide).
	"GET /api/v1/catalog":                      "op/g",
	"GET /api/v1/catalog/{id}":                 "op/g",
	"GET /api/v1/instances":                    "op/g",
	"POST /api/v1/instances":                   "op/g",
	"GET /api/v1/instances/{id}":               "op/g",
	"DELETE /api/v1/instances/{id}":            "op/g",
	"GET /api/v1/instances/{id}/history":       "op/g",
	"GET /api/v1/dashboard/stats":              "admin/g",
	"GET /api/v1/dashboard/denials":            "admin/g",
	"POST /api/v1/dashboard/denials":           "admin/g",
	"GET /api/v1/runs":                         "op/g+grant",
	"GET /api/v1/runs/_health":                 "op/g+grant",
	"GET /api/v1/runs/{run_id}":                "op/g+grant",
	"GET /api/v1/runs/{run_id}/events":         "op/g+grant",
	"GET /api/v1/runs/{run_id}/effects":        "op/g+grant",
	"GET /api/v1/runs/{run_id}/obligations":    "op/g+grant",
	"GET /api/v1/runs/{run_id}/budgets":        "op/g+grant",
	"GET /api/v1/runs/{run_id}/operator-audit": "op/g+grant",
	"POST /api/v1/runs/{run_id}/redrive":       "op/g+grant",
	"POST /api/v1/runs/{run_id}/reconcile":     "op/g+grant",
	"POST /api/v1/runs/{run_id}/cancel":        "op/g+grant",
	"POST /api/v1/runs/{run_id}/signal":        "op/g+grant",
	"POST /api/v1/runs/{run_id}/compensate":    "op/g+grant",
	"POST /api/v1/runs/{run_id}/compact":       "op/g+grant",
}

// policyFor returns the intended policy for a walked route. "*" entries
// cover routes chi mounts for every method.
func policyFor(r mountedRoute) (string, bool) {
	if p, ok := intendedRoutePolicy[r.key()]; ok {
		return p, true
	}
	p, ok := intendedRoutePolicy["* "+r.Pattern]
	return p, ok
}

type routeExpectation struct {
	class  string
	tenant bool
}

func parsePolicy(t *testing.T, policy string) routeExpectation {
	t.Helper()
	policy = strings.TrimSuffix(policy, "+grant")
	class, scope, ok := strings.Cut(policy, "/")
	if !ok || (scope != "t" && scope != "g") {
		t.Fatalf("malformed policy %q", policy)
	}
	switch class {
	case "public", "internal", "auth", "op", "admin":
	default:
		t.Fatalf("unknown policy class %q", class)
	}
	return routeExpectation{class: class, tenant: scope == "t"}
}

// allowedFor reports whether the caller passes the route's authorization
// layer under the intended policy.
func (e routeExpectation) allowedFor(c authzCaller) bool {
	switch e.class {
	case "public":
		return true
	case "internal":
		return false
	}
	if c == callerAnonymous {
		return false
	}
	if c == callerForeign && !e.tenant {
		return false
	}
	switch e.class {
	case "auth":
		return true
	case "op":
		return c == callerAdmin || c == callerDeployer || c == callerForeign
	case "admin":
		return c == callerAdmin || c == callerForeign
	}
	return false
}

// concretePath fills route parameters with stable placeholder values.
func concretePath(pattern string) string {
	replacer := strings.NewReplacer(
		"{apid}", "ap-1", "{connection}", "conn-1", "{rid}", "resp-1", "{target}", "model-1",
		"{id}", "id-1", "{clientId}", "client-1", "{userId}", "principal-"+strings.Repeat("0", 64),
		"{kind}", "user", "{provider}", "provider-1", "{binding}", "binding-1", "{modelId}", "model-1",
		"{run_id}", "run-1", "{instanceId}", "instance-1", "{deployment}", "deployment-1",
	)
	path := replacer.Replace(pattern)
	if strings.HasSuffix(path, "/*") {
		path = strings.TrimSuffix(path, "*") + "v1/models"
	}
	return path
}

// authzErrorCode extracts the API error code, if the body is one.
func authzErrorCode(body []byte) string {
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Code
}

// policyDenial reports whether a response is the authorization layer
// refusing the caller (as opposed to a handler-level validation, grant or
// not-found outcome, which is acceptable for an allowed caller).
func policyDenial(status int, code string) bool {
	switch {
	case status == http.StatusUnauthorized:
		return true
	case status == http.StatusForbidden:
		switch code {
		case "ROLE_REQUIRED", "DEPLOYMENT_SCOPE_REQUIRED", "FORBIDDEN", "ADMIN_REQUIRED", "RESOURCE_FORBIDDEN":
			return true
		}
	}
	return false
}

func TestEveryMountedRouteHasAnIntendedPolicy(t *testing.T) {
	s := newAuthzServer(t)
	var missing []string
	for _, r := range walkRoutes(t, s.handler) {
		policy, ok := policyFor(r)
		if !ok {
			missing = append(missing, r.key())
			continue
		}
		parsePolicy(t, policy)
	}
	if len(missing) > 0 {
		t.Fatalf("routes mounted without an entry in intendedRoutePolicy:\n  %s", strings.Join(missing, "\n  "))
	}
}

// TestRouteAuthorizationMatrix sends every mounted route a request as each
// caller and checks the authorization outcome against intendedRoutePolicy.
func TestRouteAuthorizationMatrix(t *testing.T) {
	routes := walkRoutes(t, newAuthzServer(t).handler)
	type failure struct{ route, caller, detail string }
	var failures []failure
	for _, caller := range authzCallers {
		// The in-process limiter allows 200 requests per caller per minute.
		for start := 0; start < len(routes); start += 150 {
			end := min(start+150, len(routes))
			s := newAuthzServer(t)
			for _, route := range routes[start:end] {
				if route.Method == http.MethodConnect || route.Method == http.MethodTrace || route.Method == http.MethodOptions {
					continue // CORS short-circuits OPTIONS; CONNECT/TRACE only exist on catch-all mounts
				}
				policy, ok := policyFor(route)
				if !ok {
					continue // reported by TestEveryMountedRouteHasAnIntendedPolicy
				}
				want := parsePolicy(t, policy)
				token := ""
				if caller != callerAnonymous {
					token = string(caller)
				}
				body := ""
				if route.Method != http.MethodGet && route.Method != http.MethodHead && route.Method != http.MethodDelete {
					body = "{}"
				}
				timeout := 2 * time.Second
				if route.Pattern == "/events/stream" {
					timeout = 200 * time.Millisecond
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				req := authzRequest(route.Method, concretePath(route.Pattern), token, body).WithContext(ctx)
				if route.Method == http.MethodPut || route.Method == http.MethodDelete {
					req.Header.Set("If-Match", `"1"`)
				}
				rec := httptest.NewRecorder()
				s.handler.ServeHTTP(rec, req)
				cancel()
				code := authzErrorCode(rec.Body.Bytes())
				allowed := want.allowedFor(caller)
				switch {
				case want.class == "internal":
					if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusServiceUnavailable {
						failures = append(failures, failure{route.key(), string(caller), fmt.Sprintf("internal route answered %d %s without its shared secret", rec.Code, code)})
					}
				case allowed && policyDenial(rec.Code, code):
					failures = append(failures, failure{route.key(), string(caller), fmt.Sprintf("want allowed (%s), got %d %s", policy, rec.Code, code)})
				case !allowed && caller == callerAnonymous && rec.Code != http.StatusUnauthorized:
					failures = append(failures, failure{route.key(), string(caller), fmt.Sprintf("want 401 (%s), got %d %s", policy, rec.Code, code)})
				case !allowed && caller != callerAnonymous && rec.Code != http.StatusForbidden:
					failures = append(failures, failure{route.key(), string(caller), fmt.Sprintf("want 403 (%s), got %d %s", policy, rec.Code, code)})
				}
			}
		}
	}
	sort.Slice(failures, func(i, j int) bool {
		if failures[i].route != failures[j].route {
			return failures[i].route < failures[j].route
		}
		return failures[i].caller < failures[j].caller
	})
	for _, f := range failures {
		t.Errorf("%-60s %-14s %s", f.route, f.caller, f.detail)
	}
}
