package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/governance"
	awsgovernance "github.com/vamsiramakrishnan/aiplex/pkg/governance/aws"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type NativeProviderInfo struct {
	ID           string                    `json:"id"`
	Type         string                    `json:"type"`
	TenantID     string                    `json:"-"`
	Operations   []nativeapi.OperationInfo `json:"operations"`
	ApplyEnabled bool                      `json:"apply_enabled"`
	Status       string                    `json:"status"`
}

func decodeRequestSpec(r *http.Request, destination any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, nativeapi.MaxBodyBytes+1))
	if err != nil || len(raw) > nativeapi.MaxBodyBytes {
		return fmt.Errorf("bounded JSON request required")
	}
	if err = nativeapi.ValidatePublicPayload(raw); err != nil {
		return fmt.Errorf("request contains invalid or restricted fields")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid native governance request")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing native governance request")
	}
	return nil
}

func (h *ConnectedHandler) validateAgentBinding(tenant, id string, raw []byte) error {
	if h.Governance == nil {
		return fmt.Errorf("native governance providers not configured")
	}
	if err := nativeapi.ValidatePublicPayload(raw); err != nil {
		return err
	}
	var binding governance.AgentBinding
	if err := decodeSpec(raw, &binding); err != nil {
		return err
	}
	if binding.ID != id || binding.TenantID != tenant {
		return fmt.Errorf("agent binding namespace mismatch")
	}
	return h.Governance.ValidateBinding(binding)
}

// This links the declared remote runtime to its governed native identity. It
// does not turn registration metadata or an old receipt into enforcement proof.
func (h *ConnectedHandler) checkAgentGovernanceRef(ctx context.Context, tenant string, connection cloudagents.Connection) error {
	if connection.GovernanceBindingRef == "" {
		return nil
	}
	if h.Governance == nil {
		return fmt.Errorf("referenced native governance provider is unavailable")
	}
	target, err := connection.GovernanceTargetRef()
	if err != nil {
		return err
	}
	record, err := h.Store.Get(ctx, tenant, governance.BindingKind, connection.GovernanceBindingRef)
	if err != nil {
		return fmt.Errorf("referenced native governance binding is unavailable")
	}
	var binding governance.AgentBinding
	if json.Unmarshal(record.Spec, &binding) != nil || binding.ID != connection.GovernanceBindingRef || binding.TenantID != tenant || binding.Registry == nil || binding.Registry.RuntimeRef != target {
		return fmt.Errorf("native governance binding does not identify this runtime")
	}
	return h.Governance.ValidateBinding(binding)
}

// Bedrock Classic dispatch selects names under the signing account. A declared
// alias ARN alone cannot establish that account; use the same credential source
// for a policy-constrained STS attestation before invoking it.
func (h *ConnectedHandler) verifyLinkedBedrockAccount(ctx context.Context, tenant string, connection cloudagents.Connection, credentials sdkaws.CredentialsProvider) error {
	if connection.GovernanceBindingRef == "" || connection.Provider != cloudagents.BedrockAgent {
		return nil
	}
	account, partition, err := connection.GovernanceAWSAccount()
	if err != nil {
		return err
	}
	stsOrigin := "https://sts." + connection.Region + ".amazonaws.com"
	if partition == "aws-cn" {
		stsOrigin += ".cn"
	}
	if h.Runtime == nil || h.Runtime.Policy == nil || h.Runtime.Policy.ValidateURL(stsOrigin) != nil {
		return fmt.Errorf("linked Bedrock governance requires an allowlisted STS authority")
	}
	if err = h.Runtime.CheckDestination(tenant, connection.IdentityRef, stsOrigin); err != nil {
		return fmt.Errorf("STS credential destination is not approved")
	}
	verifier, err := awsgovernance.NewSTSAccountVerifier(h.Runtime.HTTP, credentials, connection.Region)
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return verifier.VerifyAccount(bounded, account, partition)
}

func nativeGrant(ctx context.Context, request contracts.RequestContext, phase, binding string) error {
	if err := request.Validate(); err != nil {
		return err
	}
	identity, access := GetWIFIdentity(ctx), GetWIFAccess(ctx)
	if identity == nil || access == nil || identity.PrincipalID != request.PrincipalID || identity.ActorID != request.ActorID || !scopeAllowed(access.Scopes, "aiplex:governance:"+phase+":"+binding) {
		return governance.ErrDenied
	}
	return nil
}

func (h *ConnectedHandler) ConfigureNativeGovernance() error {
	h.NativeProviders = map[string]NativeProviderInfo{}
	if len(h.Runtime.Config.GovernanceProviders) == 0 {
		return nil
	}
	drivers := map[string]governance.DriverRegistration{}
	allowApply := false
	for _, spec := range h.Runtime.Config.GovernanceProviders {
		if _, exists := drivers[spec.ID]; exists {
			return fmt.Errorf("duplicate native governance provider ID")
		}
		driver, fingerprint, err := h.Runtime.GovernanceDriver(spec)
		if err != nil {
			return fmt.Errorf("native provider %s: %w", spec.ID, err)
		}
		etagReads := map[string]string{}
		switch spec.Type {
		case "google-agent-registry":
			etagReads = map[string]string{"services.patch": "services.get", "services.delete": "services.get", "bindings.patch": "bindings.get", "bindings.delete": "bindings.get", "aiApplications.setIamPolicy": "aiApplications.getIamPolicy"}
		case "google-iap":
			etagReads["egress.setIamPolicy"] = "egress.getIamPolicy"
		case "aws-agentcore-identity":
			etagReads = map[string]string{"workloadIdentities.update": "workloadIdentities.get", "workloadIdentities.delete": "workloadIdentities.get"}
		}
		drivers[spec.ID] = governance.DriverRegistration{Driver: driver, TenantID: spec.TenantID, ProviderRevision: fingerprint, AllowApply: spec.AllowApply, ETagReads: etagReads, InventoryProjection: func(_ nativeapi.Operation, result nativeapi.Result) (json.RawMessage, error) {
			if err := nativeapi.ValidatePublicPayload(result.Body); err != nil {
				return nil, fmt.Errorf("native inventory contains restricted fields")
			}
			return append(json.RawMessage(nil), result.Body...), nil
		}}
		allowApply = allowApply || spec.AllowApply
		h.NativeProviders[spec.ID] = NativeProviderInfo{ID: spec.ID, Type: spec.Type, TenantID: spec.TenantID, Operations: driver.Operations(), ApplyEnabled: spec.AllowApply, Status: "configured"}
	}
	controller, err := governance.NewController(governance.Config{Store: h.Store, Drivers: drivers, AllowApply: allowApply, MaxPlanTTL: 15 * time.Minute,
		Authorize: governance.AuthorizeFunc(func(ctx context.Context, action governance.Action) error {
			return nativeGrant(ctx, action.Context, action.Phase, action.BindingID)
		}),
		Approvals: governance.ApprovalFunc(func(ctx context.Context, approval governance.Approval) error {
			if approval.Reference == "" || approval.Reference != approval.PlanDigest {
				return governance.ErrDenied
			}
			identity := GetWIFIdentity(ctx)
			if identity == nil || identity.PrincipalID != approval.PrincipalID || identity.ActorID != approval.ActorID {
				return governance.ErrDenied
			}
			access := GetWIFAccess(ctx)
			if access == nil || !scopeAllowed(access.Scopes, "aiplex:governance:apply:"+approval.BindingID) {
				return governance.ErrDenied
			}
			return nil
		}),
	})
	if err != nil {
		return err
	}
	h.Governance = controller
	return nil
}

func (h *ConnectedHandler) MountNativeGovernance(r chi.Router) {
	r.Route("/native", func(r chi.Router) {
		r.Get("/providers", h.NativeProviderList)
		r.Route("/bindings", func(r chi.Router) {
			resourceRoutesAPI(r, h, governance.BindingKind)
			r.Post("/{id}/read", h.NativeRead)
			r.Post("/{id}/plan", h.NativePlan)
		})
		r.Get("/plans/{id}", h.NativePlanGet)
		r.Post("/plans/{id}/apply", h.NativeApply)
		r.Get("/receipts/{id}", h.NativeReceiptGet)
	})
}
func resourceRoutesAPI(r chi.Router, h *ConnectedHandler, kind string) {
	handler := h.Resources(kind)
	r.Get("/", handler)
	r.Get("/{id}", handler)
	r.Put("/{id}", handler)
	r.Delete("/{id}", handler)
}

func (h *ConnectedHandler) NativeProviderList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	if err := h.authorize(r, "aiplex:governance:providers:read"); err != nil {
		Error(w, r, 403, "ACTION_DENIED", "native provider discovery grant required")
		return
	}
	out := []NativeProviderInfo{}
	for _, provider := range h.NativeProviders {
		if provider.TenantID == tenant {
			out = append(out, provider)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	JSON(w, 200, out)
}
func (h *ConnectedHandler) nativeReady(w http.ResponseWriter, r *http.Request) bool {
	if h.Governance == nil {
		Error(w, r, 503, "NATIVE_GOVERNANCE_UNAVAILABLE", "native governance providers not configured")
		return false
	}
	return true
}
func nativeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, governance.ErrDenied):
		Error(w, r, 403, "ACTION_DENIED", "native governance action grant or approval required")
	case errors.Is(err, governance.ErrApplyDisabled):
		Error(w, r, 409, "APPLY_DISABLED", "native governance apply is disabled for this provider")
	case errors.Is(err, governance.ErrStale), errors.Is(err, governance.ErrAlreadyAttempted), errors.Is(err, resources.ErrConflict):
		Error(w, r, 409, "GOVERNANCE_CONFLICT", "review binding, plan and receipt before proceeding")
	case errors.Is(err, resources.ErrNotFound):
		Error(w, r, 404, "NOT_FOUND", "native governance resource not found")
	default:
		Error(w, r, 400, "NATIVE_GOVERNANCE_REFUSED", "native governance operation refused; inspect configuration and saved binding")
	}
}

func (h *ConnectedHandler) NativeRead(w http.ResponseWriter, r *http.Request) {
	if !h.nativeReady(w, r) {
		return
	}
	trusted, err := h.trusted(r, idParam(r))
	if err != nil {
		Error(w, r, 401, "IDENTITY_REQUIRED", "verified identity required")
		return
	}
	var input struct {
		BindingRevision uint64              `json:"binding_revision"`
		Operation       nativeapi.Operation `json:"operation"`
	}
	if err = decodeRequestSpec(r, &input); err != nil {
		Error(w, r, 400, "INVALID_SPEC", err.Error())
		return
	}
	result, err := h.Governance.Read(r.Context(), governance.ReadRequest{Context: trusted, BindingID: idParam(r), BindingRevision: input.BindingRevision, Operation: input.Operation})
	if err != nil {
		nativeError(w, r, err)
		return
	}
	JSON(w, 200, result)
}
func (h *ConnectedHandler) NativePlan(w http.ResponseWriter, r *http.Request) {
	if !h.nativeReady(w, r) {
		return
	}
	trusted, err := h.trusted(r, idParam(r))
	if err != nil {
		Error(w, r, 401, "IDENTITY_REQUIRED", "verified identity required")
		return
	}
	var input struct {
		BindingRevision uint64 `json:"binding_revision"`
		DesiredID       string `json:"desired_id"`
		TTLSeconds      int64  `json:"ttl_seconds"`
	}
	if err = decodeRequestSpec(r, &input); err != nil || input.TTLSeconds < 0 || input.TTLSeconds > 900 {
		Error(w, r, 400, "INVALID_SPEC", "saved binding revision, desired operation and bounded TTL required")
		return
	}
	result, err := h.Governance.Plan(r.Context(), governance.PlanRequest{Context: trusted, BindingID: idParam(r), BindingRevision: input.BindingRevision, DesiredID: input.DesiredID, TTL: time.Duration(input.TTLSeconds) * time.Second})
	if err != nil {
		nativeError(w, r, err)
		return
	}
	JSON(w, 200, result)
}
func (h *ConnectedHandler) NativeApply(w http.ResponseWriter, r *http.Request) {
	if !h.nativeReady(w, r) {
		return
	}
	plan, err := h.nativePlan(r)
	if err != nil {
		nativeError(w, r, err)
		return
	}
	trusted, err := h.trusted(r, plan.BindingID)
	if err != nil {
		Error(w, r, 401, "IDENTITY_REQUIRED", "verified identity required")
		return
	}
	var input struct {
		PlanRevision   uint64 `json:"plan_revision"`
		ApprovalDigest string `json:"approval_digest"`
	}
	if err = decodeRequestSpec(r, &input); err != nil {
		Error(w, r, 400, "INVALID_SPEC", err.Error())
		return
	}
	result, err := h.Governance.Apply(r.Context(), governance.ApplyRequest{Context: trusted, PlanID: idParam(r), PlanRevision: input.PlanRevision, ApprovalRef: input.ApprovalDigest})
	if err != nil {
		nativeError(w, r, err)
		return
	}
	JSON(w, 200, result)
}
func (h *ConnectedHandler) nativePlan(r *http.Request) (governance.Plan, error) {
	var plan governance.Plan
	tenant, err := h.Tenant(r)
	if err != nil {
		return plan, governance.ErrDenied
	}
	record, err := h.Store.Get(r.Context(), tenant, governance.PlanKind, idParam(r))
	if err != nil {
		return plan, err
	}
	var saved struct {
		Plan governance.Plan `json:"plan"`
	}
	if json.Unmarshal(record.Spec, &saved) != nil || saved.Plan.ID != idParam(r) || saved.Plan.TenantID != tenant {
		return plan, fmt.Errorf("invalid plan")
	}
	plan = saved.Plan
	plan.Revision = record.Revision
	return plan, nil
}
func (h *ConnectedHandler) NativePlanGet(w http.ResponseWriter, r *http.Request) {
	plan, err := h.nativePlan(r)
	if err != nil {
		nativeError(w, r, err)
		return
	}
	if err = h.authorize(r, "aiplex:governance:read:"+plan.BindingID); err != nil {
		nativeError(w, r, governance.ErrDenied)
		return
	}
	JSON(w, 200, plan)
}
func (h *ConnectedHandler) NativeReceiptGet(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	record, err := h.Store.Get(r.Context(), tenant, governance.ReceiptKind, idParam(r))
	if err != nil {
		nativeError(w, r, err)
		return
	}
	var receipt governance.Receipt
	if json.Unmarshal(record.Spec, &receipt) != nil {
		nativeError(w, r, fmt.Errorf("invalid receipt"))
		return
	}
	if err = h.authorize(r, "aiplex:governance:read:"+receipt.BindingID); err != nil {
		nativeError(w, r, governance.ErrDenied)
		return
	}
	JSON(w, 200, receipt)
}
