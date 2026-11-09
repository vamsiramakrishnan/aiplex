package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/deployment"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type DeploymentObservation = deployment.Observation

func deploymentObservationID(p deployment.Plan, key string) string {
	return deploymentDigest([]string{p.TenantID, p.ActionDigest, p.PrincipalID, p.ActorID, key})
}

func (h *ConnectedHandler) readinessProbe(tenant, provider, binding string) (connections.DeploymentReadiness, error) {
	var result connections.DeploymentReadiness
	found := false
	for _, p := range h.Runtime.Config.DeploymentProviders {
		if p.Config.ID != provider {
			continue
		}
		for _, probe := range p.Readiness {
			if probe.TenantID == tenant && probe.BindingID == binding {
				if found {
					return result, fmt.Errorf("ambiguous readiness probe")
				}
				result = probe
				found = true
			}
		}
	}
	if !found {
		return result, fmt.Errorf("no configured readiness contract")
	}
	return result, nil
}
func (h *ConnectedHandler) validateReadiness() error {
	for _, p := range h.Runtime.Config.DeploymentProviders {
		seen := map[string]bool{}
		for _, probe := range p.Readiness {
			key := probe.TenantID + "\x00" + probe.BindingID
			if seen[key] || probe.TenantID == "" || probe.BindingID == "" || probe.IdentityRef == "" || probe.TargetRef == "" || probe.ResourceField == "" || probe.ReadyField == "" || probe.ReadyValue == "" || probe.ResourceField == probe.ReadyField || probe.TTL < 0 || probe.TTL > time.Hour {
				return fmt.Errorf("readiness requires exact unique target, identity and native state criteria")
			}
			seen[key] = true
			binding, err := h.Deployments[p.Config.ID].Binding(probe.TenantID, probe.BindingID)
			if err != nil || binding.ResourceID == "" {
				return fmt.Errorf("readiness binding unavailable")
			}
			if h.Runtime.Policy == nil || h.Runtime.Policy.ValidateURL(probe.URL) != nil || h.Runtime.CheckDestination(probe.TenantID, probe.IdentityRef, probe.URL) != nil {
				return fmt.Errorf("readiness credential or endpoint policy unavailable")
			}
		}
	}
	return nil
}
func (h *ConnectedHandler) ObserveDeployment(w http.ResponseWriter, r *http.Request) {
	if key, err := quotaHeader(r.Header, "Idempotency-Key"); err != nil || key == "" || len(key) > 200 {
		Error(w, r, 400, "IDEMPOTENCY_REQUIRED", "one bounded Idempotency-Key required")
		return
	}
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	_, saved, err := h.loadDeploymentPlan(r.Context(), tenant, idParam(r))
	if err != nil {
		resourceError(w, r, err)
		return
	}
	trusted, err := h.trusted(r, saved.Plan.ResourceID)
	if err != nil {
		Error(w, r, 401, "IDENTITY_REQUIRED", "verified identity required")
		return
	}
	if trusted.PrincipalID != saved.Plan.PrincipalID || trusted.ActorID != saved.Plan.ActorID || h.currentDeploymentGrant(r.Context(), trusted, "aiplex:deployment:observe:"+trusted.ResourceID) != nil {
		Error(w, r, 403, "ACTION_DENIED", "original owner and current observation grant required")
		return
	}
	probe, err := h.readinessProbe(tenant, saved.Plan.ProviderID, saved.Plan.BindingID)
	if err != nil {
		Error(w, r, 501, "READINESS_UNSUPPORTED", "binding has no trusted readiness observation contract")
		return
	}
	observationID := deploymentObservationID(saved.Plan, r.Header.Get("Idempotency-Key"))
	if prior, readErr := h.Store.Get(r.Context(), tenant, "deployment-observation", observationID); readErr == nil {
		var observation DeploymentObservation
		if json.Unmarshal(prior.Spec, &observation) != nil || observation.ID != observationID || observation.PlanID != saved.Plan.ActionDigest || observation.PrincipalID != trusted.PrincipalID || observation.ActorID != trusted.ActorID || observation.ConfigurationDigest != deploymentDigest(probe) {
			Error(w, r, 409, "OBSERVATION_CONFLICT", "observation key belongs to another configuration")
			return
		}
		JSON(w, 200, observation)
		return
	} else if !errors.Is(readErr, resources.ErrNotFound) {
		resourceError(w, r, readErr)
		return
	}
	receipt, err := (deployment.ResourceJournal{Store: h.Store}).Get(r.Context(), deploymentReceiptKey(saved.Plan))
	if err != nil {
		resourceError(w, r, err)
		return
	}
	_, provenance, err := h.deploymentInputs(r.Context(), tenant, saved)
	if err != nil {
		resourceError(w, r, err)
		return
	}
	inputs, _ := json.Marshal(map[string]string{"target": probe.TargetRef, "configuration_digest": deploymentDigest(probe)})
	admission, err := (deploymentHostAdmission{h: h}).Admit(r.Context(), deployment.Action{Phase: "observe", ProviderID: saved.Plan.ProviderID, OperationID: saved.Plan.OperationID, Request: deployment.Request{Context: trusted, BindingID: saved.Plan.BindingID, Inputs: inputs, Provenance: provenance, IdempotencyKey: r.Header.Get("Idempotency-Key")}})
	if err != nil {
		Error(w, r, 503, "OBSERVATION_ADMISSION_DENIED", "readiness observation could not be admitted")
		return
	}
	completed := false
	defer func() {
		if !completed {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()
			_ = admission.Abort(ctx)
		}
	}()
	if h.Runtime.CheckDestination(tenant, probe.IdentityRef, probe.URL) != nil {
		Error(w, r, 503, "READINESS_UNAVAILABLE", "readiness credential target unavailable")
		return
	}
	token, err := h.Runtime.Token(r.Context(), tenant, probe.IdentityRef)
	if err != nil {
		Error(w, r, 503, "READINESS_UNAVAILABLE", "readiness identity unavailable")
		return
	}
	bounded, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, http.MethodGet, probe.URL, nil)
	if err != nil {
		Error(w, r, 503, "READINESS_UNAVAILABLE", "invalid configured readiness endpoint")
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if dispatch, ok := admission.(deployment.DispatchAdmissionReceipt); ok {
		if err = dispatch.BeforeDispatch(r.Context()); err != nil {
			Error(w, r, 503, "OBSERVATION_DISPATCH_DENIED", "readiness observation could not be dispatched")
			return
		}
	}
	response, err := h.Runtime.HTTP.Do(request)
	if err != nil {
		Error(w, r, 502, "OBSERVATION_UNKNOWN", "native observation unavailable")
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	var native map[string]json.RawMessage
	if err != nil || len(body) > 1<<20 || response.StatusCode < 200 || response.StatusCode >= 300 || json.Unmarshal(body, &native) != nil || native == nil {
		Error(w, r, 502, "OBSERVATION_INVALID", "bounded successful native JSON required")
		return
	}
	var target, state string
	if json.Unmarshal(native[probe.ResourceField], &target) != nil || target != probe.TargetRef || json.Unmarshal(native[probe.ReadyField], &state) != nil {
		Error(w, r, 502, "OBSERVATION_TARGET_MISMATCH", "native result does not establish the configured target and state")
		return
	}
	now := time.Now().UTC()
	ttl := probe.TTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	observation := DeploymentObservation{TenantID: tenant, PrincipalID: trusted.PrincipalID, ActorID: trusted.ActorID, PlanID: saved.Plan.ActionDigest, ActionDigest: saved.Plan.ActionDigest, ReceiptDigest: deploymentDigest(receipt), ConfigurationDigest: deploymentDigest(probe), TargetRef: target, NativeState: state, Ready: state == probe.ReadyValue, ResultDigest: deploymentDigest(json.RawMessage(body)), ObservedAt: now, ExpiresAt: now.Add(ttl)}
	observation.ID = observationID
	raw, _ := json.Marshal(observation)
	persist, cancelPersist := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancelPersist()
	if _, err = h.Store.Put(persist, resources.Record{TenantID: tenant, Kind: "deployment-observation", ID: observation.ID, Spec: raw}, 0); err != nil {
		resourceError(w, r, err)
		return
	}
	if err = admission.Complete(persist, true); err != nil {
		Error(w, r, 503, "OBSERVATION_UNACKNOWLEDGED", "observation saved but accounting acknowledgement unavailable")
		return
	}
	completed = true
	JSON(w, 200, observation)
}

func (h *ConnectedHandler) GetDeploymentObservation(w http.ResponseWriter, r *http.Request) {
	key, err := quotaHeader(r.Header, "Idempotency-Key")
	if err != nil || key == "" || len(key) > 200 {
		Error(w, r, 400, "IDEMPOTENCY_REQUIRED", "original observation key required")
		return
	}
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	_, saved, err := h.loadDeploymentPlan(r.Context(), tenant, idParam(r))
	if err != nil {
		resourceError(w, r, err)
		return
	}
	i := GetWIFIdentity(r.Context())
	if i == nil || i.PrincipalID != saved.Plan.PrincipalID || i.ActorID != saved.Plan.ActorID {
		Error(w, r, 404, "NOT_FOUND", "observation unavailable")
		return
	}
	id := deploymentObservationID(saved.Plan, key)
	record, err := h.Store.Get(r.Context(), tenant, "deployment-observation", id)
	if err != nil {
		resourceError(w, r, err)
		return
	}
	var observation DeploymentObservation
	if json.Unmarshal(record.Spec, &observation) != nil || observation.ID != id || observation.TenantID != tenant || observation.PlanID != saved.Plan.ActionDigest || observation.PrincipalID != i.PrincipalID || observation.ActorID != i.ActorID {
		Error(w, r, 503, "OBSERVATION_INVALID", "stored observation identity invalid")
		return
	}
	JSON(w, 200, observation)
}
