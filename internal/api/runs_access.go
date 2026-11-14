package api

import (
	"errors"
	"net/http"
	"os"

	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// RequireTenantEnv names the operator setting for tenantless callers on the
// runs routes. Unset, or any value except "0" ("1" included), means a tenant
// is required: this is the default. "0" is the single explicit opt-out, for
// single-tenant deployments: a verified caller with no tenant then sees every
// run, as before tenants were required. Ambiguous tenants are refused either
// way.
const RequireTenantEnv = "AIPLEX_REQUIRE_TENANT"

// TenantOptional reports whether the operator has opted out of requiring a
// tenant on the runs routes (AIPLEX_REQUIRE_TENANT=0, exactly).
func TenantOptional() bool { return os.Getenv(RequireTenantEnv) == "0" }

// ErrAmbiguousTenant is returned when a verified identity names more than one
// distinct tenant, through its scopes or through its domain mapping. It is
// always refused: no grant is preferred over another.
var ErrAmbiguousTenant = errors.New("the verified identity names more than one tenant")

// runsTenantView is the set of runs one request may see: one tenant's, or
// every tenant's.
type runsTenantView struct {
	tenant string
	all    bool
}

func (v runsTenantView) visible(runTenant string) bool { return v.all || runTenant == v.tenant }

// runsTenant decides which tenant's runs a request may see, from the verified
// identity only. Nothing in the request (headers, query, body) is consulted.
// It writes the refusal and returns false when the request may see none:
//
//   - more than one distinct tenant: 403 TENANT_AMBIGUOUS, always, even with
//     the cross-tenant scope;
//   - no verified identity: 401, except in loopback-only development mode,
//     where the anonymous local developer sees every run, as before;
//   - the cross-tenant scope (aiplex:runs:read:cross_tenant): every run;
//   - one tenant: that tenant's runs;
//   - no tenant and no cross-tenant scope: 403 TENANT_REQUIRED, unless the
//     server is in development mode or the operator set
//     AIPLEX_REQUIRE_TENANT=0.
func runsTenant(w http.ResponseWriter, r *http.Request) (runsTenantView, bool) {
	dev, _ := r.Context().Value(keyDevelopment).(bool)
	tenant, cross, err := TenantFromContext(r.Context())
	if err != nil {
		Error(w, r, http.StatusForbidden, "TENANT_AMBIGUOUS",
			"the verified identity names more than one tenant; bind it to exactly one tenant")
		return runsTenantView{}, false
	}
	switch {
	case GetWIFAccess(r.Context()) == nil:
		if dev {
			return runsTenantView{all: true}, true
		}
		Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
		return runsTenantView{}, false
	case cross:
		return runsTenantView{all: true}, true
	case tenant != "":
		return runsTenantView{tenant: tenant}, true
	case dev || TenantOptional():
		return runsTenantView{all: true}, true
	}
	Error(w, r, http.StatusForbidden, "TENANT_REQUIRED",
		"caller has no tenant claim and no cross-tenant scope")
	return runsTenantView{}, false
}

// RequireRunsScope enforces the aiplex:runs:<action> grant that the gateway
// policy (policies/aiplex_authz.rego) also requires, so the API is safe when
// reached without that gateway. An unverified request in explicit local
// development mode passes, as elsewhere.
func RequireRunsScope(action string) func(http.Handler) http.Handler {
	scope := "aiplex:runs:" + action
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			access := GetWIFAccess(r.Context())
			if access == nil {
				if dev, _ := r.Context().Value(keyDevelopment).(bool); dev {
					next.ServeHTTP(w, r)
					return
				}
				Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
				return
			}
			if !scopeAllowed(access.Scopes, scope) {
				Error(w, r, http.StatusForbidden, "RUNS_SCOPE_REQUIRED", "the "+scope+" grant is required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RunVisible resolves {run_id} and refuses a run outside the caller's tenant
// exactly as if it did not exist. It makes the same tenant decision as
// RequireTenant and List (runsTenant), so a route mounted with RunVisible is
// fail-closed even without RequireTenant in front of it.
func (h *RunsHandler) RunVisible(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		view, ok := runsTenant(w, r)
		if !ok {
			return
		}
		id := pathParam(r, "run_id")
		if id == "" {
			id = r.URL.Query().Get("run_id") // the event stream's per-run mode
		}
		run, err := h.store.GetExecutionRun(r.Context(), id)
		if err != nil {
			runStoreError(w, r, err)
			return
		}
		if !view.visible(run.TenantID) {
			runStoreError(w, r, registry.ErrNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// runStoreError writes the API error shape without exposing store errors.
func runStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, registry.ErrNotFound) {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "run not found")
		return
	}
	Error(w, r, http.StatusInternalServerError, "RUN_STORE_UNAVAILABLE", "run store unavailable")
}
