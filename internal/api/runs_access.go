package api

import (
	"errors"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

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
// exactly as if it did not exist. Callers without a tenant claim (single
// tenant deployments) and holders of aiplex:runs:read:cross_tenant see every
// run, as List does.
func (h *RunsHandler) RunVisible(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := pathParam(r, "run_id")
		if id == "" {
			id = r.URL.Query().Get("run_id") // the event stream's per-run mode
		}
		run, err := h.store.GetExecutionRun(r.Context(), id)
		if err != nil {
			runStoreError(w, r, err)
			return
		}
		if tenant, cross := TenantFromContext(r.Context()); tenant != "" && !cross && run.TenantID != tenant {
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
