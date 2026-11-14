package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/deployment"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type savedDeploymentPlan struct {
	Plan              deployment.Plan `json:"plan"`
	IntentID          string          `json:"intent_id"`
	IntentRevision    uint64          `json:"intent_revision"`
	NetworkProfileRef string          `json:"network_profile_ref"`
	NetworkRevision   uint64          `json:"network_profile_revision"`
}
type deploymentApprovalRecord struct {
	ID                string             `json:"id"`
	PlanID            string             `json:"plan_id"`
	PlanRevision      uint64             `json:"plan_revision"`
	ActionDigest      string             `json:"action_digest"`
	TenantID          string             `json:"tenant_id"`
	ExecutorPrincipal string             `json:"executor_principal"`
	ExecutorActor     string             `json:"executor_actor,omitempty"`
	ResourceID        string             `json:"resource_id"`
	Approver          models.WIFIdentity `json:"approver"`
	Reason            string             `json:"reason"`
	ApprovedAt        time.Time          `json:"approved_at"`
	ExpiresAt         time.Time          `json:"expires_at"`
}

func deploymentDigest(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func (h *ConnectedHandler) currentDeploymentGrant(ctx context.Context, c contracts.RequestContext, scope string) error {
	if err := deploymentGrantIdentity(ctx, c); err != nil {
		return err
	}
	identity := GetWIFIdentity(ctx)
	if h.ResolveAccess != nil {
		access, err := h.ResolveAccess(ctx, identity)
		if err != nil || access == nil || access.Identity.PrincipalID != c.PrincipalID || access.Identity.ActorID != c.ActorID || !scopeAllowed(access.Scopes, scope) {
			return fmt.Errorf("current deployment action grant required")
		}
		return nil
	}
	return deploymentGrant(ctx, c, scope)
}
func deploymentGrantIdentity(ctx context.Context, c contracts.RequestContext) error {
	if err := c.Validate(); err != nil {
		return err
	}
	i := GetWIFIdentity(ctx)
	if i == nil || i.PrincipalID != c.PrincipalID || i.ActorID != c.ActorID {
		return fmt.Errorf("verified deployment identity required")
	}
	return nil
}
func deploymentJSON(r *http.Request, out any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(b) > 65536 {
		return fmt.Errorf("bounded request required")
	}
	if _, err = quotaJSONObject(b); err != nil {
		return err
	}
	if err = quotaAdminUniqueFields(b, reflect.TypeOf(out)); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
func (h *ConnectedHandler) loadDeploymentPlan(ctx context.Context, tenant, id string) (resources.Record, savedDeploymentPlan, error) {
	var saved savedDeploymentPlan
	record, err := h.Store.Get(ctx, tenant, "deployment-plan", id)
	if err != nil {
		return record, saved, err
	}
	if json.Unmarshal(record.Spec, &saved) != nil || saved.Plan.ActionDigest != id || saved.Plan.TenantID != tenant {
		return record, saved, fmt.Errorf("deployment plan identity invalid")
	}
	return record, saved, nil
}
func (h *ConnectedHandler) deploymentInputs(ctx context.Context, tenant string, saved savedDeploymentPlan) (json.RawMessage, json.RawMessage, error) {
	record, err := h.Store.Get(ctx, tenant, "deployment-intent", saved.IntentID)
	if err != nil || record.Revision != saved.IntentRevision {
		return nil, nil, resources.ErrConflict
	}
	var intent deployment.Intent
	if json.Unmarshal(record.Spec, &intent) != nil || intent.ID != saved.IntentID || intent.ProviderRef != saved.Plan.ProviderID || intent.BindingID != saved.Plan.BindingID || intent.NetworkProfileRef != saved.NetworkProfileRef {
		return nil, nil, resources.ErrConflict
	}
	if intent.NetworkProfileRef != "" {
		network, e := h.Store.Get(ctx, tenant, "network-profile", intent.NetworkProfileRef)
		if e != nil || network.Revision != saved.NetworkRevision {
			return nil, nil, resources.ErrConflict
		}
	}
	provenance, _ := json.Marshal(map[string]any{"intent_id": intent.ID, "intent_revision": record.Revision, "network_profile_ref": intent.NetworkProfileRef, "network_profile_revision": saved.NetworkRevision})
	return intent.Inputs, provenance, nil
}
func (h *ConnectedHandler) ApproveDeployment(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	record, saved, err := h.loadDeploymentPlan(r.Context(), tenant, idParam(r))
	if err != nil {
		resourceError(w, r, err)
		return
	}
	trusted, err := h.trusted(r, saved.Plan.ResourceID)
	if err != nil {
		Error(w, r, 401, "IDENTITY_REQUIRED", "verified identity required")
		return
	}
	if h.currentDeploymentGrant(r.Context(), trusted, "aiplex:deployment:approve:"+trusted.ResourceID) != nil {
		Error(w, r, 403, "ACTION_DENIED", "deployment approval grant required")
		return
	}
	var body struct {
		Digest string `json:"digest"`
		Reason string `json:"reason"`
	}
	if deploymentJSON(r, &body) != nil || body.Digest != saved.Plan.ActionDigest || strings.TrimSpace(body.Reason) == "" || len(body.Reason) > 2048 {
		Error(w, r, 400, "APPROVAL_INVALID", "exact plan digest and bounded reason required")
		return
	}
	if saved.Plan.PlanOnly || !saved.Plan.ExpiresAt.After(time.Now()) {
		Error(w, r, 409, "PLAN_NOT_EXECUTABLE", "plan expired or prepares artifacts only")
		return
	}
	if _, _, err = h.deploymentInputs(r.Context(), tenant, saved); err != nil {
		resourceError(w, r, err)
		return
	}
	identity := *GetWIFIdentity(r.Context())
	identity.VerifiedEvidence = nil
	approval := deploymentApprovalRecord{PlanID: record.ID, PlanRevision: record.Revision, ActionDigest: saved.Plan.ActionDigest, TenantID: tenant, ExecutorPrincipal: saved.Plan.PrincipalID, ExecutorActor: saved.Plan.ActorID, ResourceID: saved.Plan.ResourceID, Approver: identity, Reason: body.Reason, ApprovedAt: time.Now().UTC(), ExpiresAt: saved.Plan.ExpiresAt}
	approval.ID = deploymentDigest([]string{tenant, record.ID, identity.PrincipalID, identity.ActorID})
	raw, _ := json.Marshal(approval)
	result, err := h.Store.Put(r.Context(), resources.Record{TenantID: tenant, Kind: "deployment-approval", ID: approval.ID, Spec: raw}, 0)
	if errors.Is(err, resources.ErrConflict) {
		result, err = h.Store.Get(r.Context(), tenant, "deployment-approval", approval.ID)
	}
	if err != nil {
		resourceError(w, r, err)
		return
	}
	JSON(w, 201, result)
}

type deploymentApprovals struct{ h *ConnectedHandler }

func (v deploymentApprovals) Verify(ctx context.Context, a deployment.Approval) error {
	record, err := v.h.Store.Get(ctx, a.TenantID, "deployment-approval", a.Reference)
	if err != nil {
		return err
	}
	var approval deploymentApprovalRecord
	if json.Unmarshal(record.Spec, &approval) != nil || approval.ID != a.Reference || approval.TenantID != a.TenantID || approval.ExecutorPrincipal != a.PrincipalID || approval.ResourceID != a.ResourceID || approval.ActionDigest != a.ActionDigest || !approval.ExpiresAt.After(time.Now()) {
		return fmt.Errorf("deployment approval expired or mismatched")
	}
	i := GetWIFIdentity(ctx)
	if i == nil || i.PrincipalID != approval.ExecutorPrincipal || i.ActorID != approval.ExecutorActor {
		return fmt.Errorf("deployment approval executor mismatch")
	}
	plan, saved, err := v.h.loadDeploymentPlan(ctx, a.TenantID, approval.PlanID)
	if err != nil || plan.Revision != approval.PlanRevision || saved.Plan.ActionDigest != a.ActionDigest {
		return resources.ErrConflict
	}
	if _, _, err = v.h.deploymentInputs(ctx, a.TenantID, saved); err != nil {
		return err
	}
	if v.h.ResolveAccess != nil {
		access, err := v.h.ResolveAccess(ctx, &approval.Approver)
		if err != nil || access == nil || access.Identity.PrincipalID != approval.Approver.PrincipalID || access.Identity.ActorID != approval.Approver.ActorID || !scopeAllowed(access.Scopes, "aiplex:deployment:approve:"+a.ResourceID) {
			return fmt.Errorf("deployment approval grant revoked")
		}
	}
	return nil
}
func deploymentReceiptKey(p deployment.Plan) deployment.Receipt {
	return deployment.Receipt{ProviderID: p.ProviderID, TenantID: p.TenantID, PrincipalID: p.PrincipalID, ResourceID: p.ResourceID, IdempotencyKey: p.IdempotencyKey, ActionDigest: p.ActionDigest}
}
func (h *ConnectedHandler) ExecuteDeployment(w http.ResponseWriter, r *http.Request) {
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
	if trusted.PrincipalID != saved.Plan.PrincipalID || trusted.ActorID != saved.Plan.ActorID || h.currentDeploymentGrant(r.Context(), trusted, "aiplex:deployment:execute:"+trusted.ResourceID) != nil {
		Error(w, r, 403, "ACTION_DENIED", "original executor and current action grant required")
		return
	}
	var body struct {
		Digest     string `json:"digest"`
		ApprovalID string `json:"approval_id"`
	}
	if deploymentJSON(r, &body) != nil || body.Digest != saved.Plan.ActionDigest || body.ApprovalID == "" {
		Error(w, r, 400, "APPROVAL_REQUIRED", "reviewed digest and approval reference required")
		return
	}
	prior, err := (deployment.ResourceJournal{Store: h.Store}).Get(r.Context(), deploymentReceiptKey(saved.Plan))
	if err == nil {
		JSON(w, 200, prior)
		return
	}
	if !errors.Is(err, resources.ErrNotFound) {
		resourceError(w, r, err)
		return
	}
	inputs, provenance, err := h.deploymentInputs(r.Context(), tenant, saved)
	if err != nil {
		resourceError(w, r, err)
		return
	}
	provider := h.Deployments[saved.Plan.ProviderID]
	if provider == nil {
		Error(w, r, 503, "PROVIDER_UNAVAILABLE", "configured provider unavailable")
		return
	}
	receipt, err := provider.Execute(r.Context(), deployment.Request{Context: trusted, BindingID: saved.Plan.BindingID, Inputs: inputs, Provenance: provenance, IdempotencyKey: saved.Plan.IdempotencyKey}, saved.Plan, body.ApprovalID)
	if err != nil {
		if receipt.ActionDigest != "" {
			JSON(w, 202, receipt)
			return
		}
		Error(w, r, 409, "DEPLOYMENT_REFUSED", "execution refused before a durable invocation receipt")
		return
	}
	JSON(w, 200, receipt)
}
func (h *ConnectedHandler) GetDeploymentReceipt(w http.ResponseWriter, r *http.Request) {
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
		Error(w, r, 404, "NOT_FOUND", "deployment receipt unavailable")
		return
	}
	receipt, err := (deployment.ResourceJournal{Store: h.Store}).Get(r.Context(), deploymentReceiptKey(saved.Plan))
	if err != nil {
		resourceError(w, r, err)
		return
	}
	JSON(w, 200, receipt)
}

type deploymentHostAdmission struct{ h *ConnectedHandler }

func (a deploymentHostAdmission) Admit(ctx context.Context, action deployment.Action) (deployment.AdmissionReceipt, error) {
	if a.h.currentDeploymentGrant(ctx, action.Request.Context, "aiplex:deployment:"+action.Phase+":"+action.Request.Context.ResourceID) != nil {
		return nil, fmt.Errorf("deployment admission denied")
	}
	if a.h.Quota == nil {
		return authorizedPlanReceipt{}, nil
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://aiplex.invalid/managed-deployment", nil)
	request.Header.Set("Idempotency-Key", action.Request.IdempotencyKey+":"+action.Phase)
	operation := "tools/call:anvil." + action.Phase + ":" + action.OperationID
	manifestDigest := deploymentDigest(a.h.Runtime.Config.DeploymentProviders)
	lease, err := a.h.Quota.Prepare(ctx, request, QuotaInvocation{Context: action.Request.Context, Plane: "mcplex", ResourceID: action.Request.Context.ResourceID, OperationID: operation, Target: quotaplex.Target{Provider: "anvil"}, Body: action.Request.Inputs, Headers: request.Header, Configuration: struct {
		Provider, Binding, Phase, ManifestDigest string
		Provenance                               json.RawMessage
	}{action.ProviderID, action.Request.BindingID, action.Phase, manifestDigest, action.Request.Provenance}, Reauthorize: func(c context.Context) error {
		if deploymentDigest(a.h.Runtime.Config.DeploymentProviders) != manifestDigest {
			return resources.ErrConflict
		}
		if err := a.h.currentDeploymentGrant(c, action.Request.Context, "aiplex:deployment:"+action.Phase+":"+action.Request.Context.ResourceID); err != nil {
			return err
		}
		var p struct {
			IntentID        string `json:"intent_id"`
			IntentRevision  uint64 `json:"intent_revision"`
			NetworkID       string `json:"network_profile_ref"`
			NetworkRevision uint64 `json:"network_profile_revision"`
		}
		if json.Unmarshal(action.Request.Provenance, &p) != nil {
			return resources.ErrConflict
		}
		_, _, err := a.h.deploymentInputs(c, action.Request.Context.TenantID, savedDeploymentPlan{Plan: deployment.Plan{ProviderID: action.ProviderID, BindingID: action.Request.BindingID}, IntentID: p.IntentID, IntentRevision: p.IntentRevision, NetworkProfileRef: p.NetworkID, NetworkRevision: p.NetworkRevision})
		return err
	}, DeploymentAction: true})
	if err != nil {
		return nil, err
	}
	return deploymentQuotaReceipt{lease: lease}, nil
}

type deploymentQuotaReceipt struct{ lease execution.Lease }

func (r deploymentQuotaReceipt) BeforeDispatch(ctx context.Context) error {
	return r.lease.BeforeDispatch(ctx)
}

func (r deploymentQuotaReceipt) Complete(ctx context.Context, success bool) error {
	if !success {
		return r.lease.Finish(ctx, execution.Result{Err: fmt.Errorf("native deployment outcome uncertain")})
	}
	if err := r.lease.Observe(ctx, execution.Observation{Source: "upstream", Terminal: true, Cumulative: true, Counters: map[string]int64{"requests": 1, "tool_calls": 1}}); err != nil {
		return err
	}
	return r.lease.Finish(ctx, execution.Result{ResponseComplete: true, Terminal: true})
}
func (r deploymentQuotaReceipt) Abort(ctx context.Context) error {
	return r.lease.Finish(ctx, execution.Result{Err: fmt.Errorf("deployment admission ended before confirmed completion")})
}

// MountDeploymentExecution contributes only the owning networking module's API.
func (h *ConnectedHandler) MountDeploymentExecution(r chi.Router) {
	r.Post("/network/plans/{id}/approve", h.ApproveDeployment)
	r.Post("/network/plans/{id}/execute", h.ExecuteDeployment)
	r.Get("/network/plans/{id}/receipt", h.GetDeploymentReceipt)
	r.Post("/network/plans/{id}/observe", h.ObserveDeployment)
	r.Get("/network/plans/{id}/observation", h.GetDeploymentObservation)
}
