package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/deployment"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// ConfigureDeploymentProviders wires optional infrastructure providers. Execute
// remains operator-enabled and requires a persisted, separately granted approval.
func (h *ConnectedHandler) ConfigureDeploymentProviders() error {
	h.Deployments = map[string]*deployment.Anvil{}
	authorize := deployment.AuthorizeFunc(func(ctx context.Context, a deployment.Action) error {
		return h.currentDeploymentGrant(ctx, a.Request.Context, "aiplex:deployment:"+a.Phase+":"+a.Request.Context.ResourceID)
	})
	for _, spec := range h.Runtime.Config.DeploymentProviders {
		cfg := spec.Config
		if cfg.AllowExecute && !h.Development {
			if _, memory := h.Store.(*resources.MemoryStore); memory || h.Store == nil {
				return fmt.Errorf("deployment execution requires durable resource storage")
			}
		}
		for _, name := range spec.EnvironmentRefs {
			value, ok := os.LookupEnv(name)
			if !ok {
				return fmt.Errorf("deployment environment reference unavailable: %s", name)
			}
			cfg.Environment = append(cfg.Environment, name+"="+value)
		}
		provider, e := deployment.NewAnvil(cfg, authorize, deploymentHostAdmission{h: h}, deploymentApprovals{h: h}, deployment.ResourceJournal{Store: h.Store})
		if e != nil {
			return e
		}
		if h.Deployments[cfg.ID] != nil {
			return fmt.Errorf("duplicate deployment provider")
		}
		h.Deployments[cfg.ID] = provider
	}
	return h.validateReadiness()
}
func deploymentGrant(ctx context.Context, request contracts.RequestContext, scope string) error {
	if e := request.Validate(); e != nil {
		return e
	}
	identity := GetWIFIdentity(ctx)
	access := GetWIFAccess(ctx)
	if identity == nil || access == nil || identity.PrincipalID != request.PrincipalID || !scopeAllowed(access.Scopes, scope) {
		return fmt.Errorf("deployment resource action grant required")
	}
	return nil
}

type deploymentAdmission struct{}
type authorizedPlanReceipt struct{}

func (deploymentAdmission) Admit(ctx context.Context, a deployment.Action) (deployment.AdmissionReceipt, error) {
	if e := deploymentGrant(ctx, a.Request.Context, "aiplex:deployment:plan:"+a.Request.Context.ResourceID); e != nil {
		return nil, e
	}
	return authorizedPlanReceipt{}, nil
}

// This is explicitly authorization-only admission. No spend/quota allocation is
// claimed until a QuotaPlex provider is configured through the shared contract.
func (authorizedPlanReceipt) Complete(context.Context, bool) error { return nil }
func (authorizedPlanReceipt) Abort(context.Context) error          { return nil }
func (h *ConnectedHandler) DeploymentBindings(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	provider := h.Deployments[chi.URLParam(r, "provider")]
	if provider == nil {
		Error(w, r, 404, "PROVIDER_NOT_FOUND", "deployment provider not configured")
		return
	}
	JSON(w, 200, provider.Bindings(tenant))
}
func (h *ConnectedHandler) PrepareDeployment(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	provider := h.Deployments[chi.URLParam(r, "provider")]
	if provider == nil {
		Error(w, r, 404, "PROVIDER_NOT_FOUND", "deployment provider not configured")
		return
	}
	bindingID := chi.URLParam(r, "binding")
	binding, bindingErr := provider.Binding(tenant, bindingID)
	if bindingErr != nil {
		Error(w, r, 404, "BINDING_NOT_FOUND", "deployment binding not configured")
		return
	}
	trusted, e := h.trusted(r, binding.ResourceID)
	if e != nil {
		Error(w, r, 401, "IDENTITY_REQUIRED", e.Error())
		return
	}
	intentRecord, e := h.Store.Get(r.Context(), tenant, "deployment-intent", binding.ResourceID)
	if e != nil {
		resourceError(w, r, e)
		return
	}
	var intent deployment.Intent
	if e = json.Unmarshal(intentRecord.Spec, &intent); e != nil || intent.ProviderRef != chi.URLParam(r, "provider") || intent.BindingID != bindingID {
		Error(w, r, 400, "INTENT_BINDING_MISMATCH", "saved intent must match provider binding")
		return
	}
	if e = h.checkNetworkRef(r.Context(), tenant, intent.NetworkProfileRef); e != nil {
		Error(w, r, 400, "NETWORK_PROFILE_UNAVAILABLE", "intent network profile unavailable")
		return
	}
	// Inputs are the saved, revisioned intent; request bodies cannot substitute
	// infrastructure input after a provider/binding was reviewed.
	if r.Body != nil {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 1025))
		if len(bytes.TrimSpace(data)) > 0 && string(bytes.TrimSpace(data)) != "{}" {
			Error(w, r, 400, "SAVED_INTENT_REQUIRED", "plan uses saved intent; body overrides are prohibited")
			return
		}
	}
	idempotency := r.Header.Get("Idempotency-Key")
	if idempotency == "" {
		Error(w, r, 400, "IDEMPOTENCY_REQUIRED", "Idempotency-Key required for deployment preparation")
		return
	}
	networkRevision := uint64(0)
	if intent.NetworkProfileRef != "" {
		network, e := h.Store.Get(r.Context(), tenant, "network-profile", intent.NetworkProfileRef)
		if e != nil {
			resourceError(w, r, e)
			return
		}
		networkRevision = network.Revision
	}
	provenance, _ := json.Marshal(map[string]any{"intent_id": intent.ID, "intent_revision": intentRecord.Revision, "network_profile_ref": intent.NetworkProfileRef, "network_profile_revision": networkRevision})
	plan, e := provider.Prepare(r.Context(), deployment.Request{Context: trusted, BindingID: bindingID, Provenance: provenance, Inputs: intent.Inputs, IdempotencyKey: idempotency})
	if e != nil {
		Error(w, r, 400, "PLAN_REFUSED", e.Error())
		return
	}
	spec, e := json.Marshal(savedDeploymentPlan{Plan: plan, IntentID: intent.ID, IntentRevision: intentRecord.Revision, NetworkProfileRef: intent.NetworkProfileRef, NetworkRevision: networkRevision})
	if e != nil {
		Error(w, r, 500, "PLAN_ERROR", "plan serialization failed")
		return
	}
	record, e := h.Store.Put(r.Context(), resources.Record{TenantID: tenant, Kind: "deployment-plan", ID: plan.ActionDigest, Spec: spec}, 0)
	if e != nil {
		if !errors.Is(e, resources.ErrConflict) {
			resourceError(w, r, e)
			return
		}
		if old, readErr := h.Store.Get(r.Context(), tenant, "deployment-plan", plan.ActionDigest); readErr == nil {
			record = old
		} else {
			resourceError(w, r, e)
			return
		}
	}
	JSON(w, 200, record)
}

func (h *ConnectedHandler) PlanDeploymentIntent(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	record, e := h.Store.Get(r.Context(), tenant, "deployment-intent", idParam(r))
	if e != nil {
		resourceError(w, r, e)
		return
	}
	var intent deployment.Intent
	if e = json.Unmarshal(record.Spec, &intent); e != nil {
		resourceError(w, r, e)
		return
	}
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("provider", intent.ProviderRef)
	routeContext.URLParams.Add("binding", intent.BindingID)
	clone := r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeContext))
	h.PrepareDeployment(w, clone)
}
