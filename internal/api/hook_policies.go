package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/hooks"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// HookPolicyKind is the tenant resource kind served at /api/v1/hooks/policies.
const HookPolicyKind = hooks.Kind

// validateHookPolicy checks a hook policy on save: the spec itself (unknown
// fields and hook types refused, every regex compiled), every sidecar
// endpoint against the outbound endpoint policy that approves tool token
// endpoints, and every sidecar secret reference where existence can be
// proved.
func (h *ConnectedHandler) validateHookPolicy(ctx context.Context, tenant, id string, raw []byte) error {
	policy, err := hooks.DecodePolicy(id, raw)
	if err != nil {
		return err
	}
	for i, hook := range policy.Hooks {
		if hook.HTTP == nil {
			continue
		}
		if h.Runtime == nil {
			return fmt.Errorf("hooks[%d]: outbound endpoint policy unavailable", i)
		}
		network, err := endpointpolicy.New(h.Runtime.Config.Network)
		if err != nil {
			return fmt.Errorf("hooks[%d]: outbound endpoint policy unavailable", i)
		}
		for _, endpoint := range hook.HTTP.Endpoints() {
			if network.ValidateURL(endpoint) != nil {
				return fmt.Errorf("hooks[%d].http endpoint is not approved by the outbound endpoint policy; add its origin to the connections network allowed_origins (aiplex dev: --allow-origin)", i)
			}
		}
		if ref := hook.HTTP.AuthSecretRef; ref != "" {
			provider, err := h.toolCredentials()
			if err != nil {
				return fmt.Errorf("hooks[%d]: secret references cannot be checked", i)
			}
			if err = provider.CheckSecret(ctx, tenant, ref); err != nil {
				return fmt.Errorf("hooks[%d].http.auth_secret_ref: %v", i, err)
			}
		}
	}
	return nil
}

// HookPolicyTestRequest is the body of POST /api/v1/hooks/policies/{id}/test.
type HookPolicyTestRequest struct {
	// Phase is request (default) or response.
	Phase string `json:"phase,omitempty"`
	// Plane is llmplex (default) or mcplex; it is passed to sidecars.
	Plane string   `json:"plane,omitempty"`
	Texts []string `json:"texts"`
}

// HookPolicyTestResult is the dry-run answer. The sample is never stored or
// logged; Texts is the output after transforms (absent when refused).
type HookPolicyTestResult struct {
	Policy           string                   `json:"policy"`
	Phase            string                   `json:"phase"`
	Decision         string                   `json:"decision"`
	Code             string                   `json:"code,omitempty"`
	Message          string                   `json:"message,omitempty"`
	Texts            []string                 `json:"texts,omitempty"`
	Results          []usageledger.HookResult `json:"results"`
	UninspectedParts int                      `json:"uninspected_parts"`
}

// TestHookPolicy runs a saved policy's hooks (regardless of its scope) over
// sample texts and returns the decisions and the transformed texts. Nothing
// is recorded: no ledger record, no log line with the sample.
func (h *ConnectedHandler) TestHookPolicy(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	id := idParam(r)
	record, err := h.Store.Get(r.Context(), tenant, hooks.Kind, id)
	if err != nil {
		resourceError(w, r, err)
		return
	}
	policy, err := hooks.DecodePolicy(id, record.Spec)
	if err != nil {
		Error(w, r, http.StatusConflict, "INVALID_STORED_POLICY", "the stored hook policy no longer validates; save it again")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		Error(w, r, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "the sample exceeds 4 MiB")
		return
	}
	var in HookPolicyTestRequest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&in); err != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "body must be {\"texts\": [...], \"phase\": \"request|response\", \"plane\": \"llmplex|mcplex\"}")
		return
	}
	if in.Phase == "" {
		in.Phase = hooks.PhaseRequest
	}
	if in.Plane == "" {
		in.Plane = hooks.PlaneModels
	}
	if (in.Phase != hooks.PhaseRequest && in.Phase != hooks.PhaseResponse) || (in.Plane != hooks.PlaneModels && in.Plane != hooks.PlaneTools) || len(in.Texts) == 0 || len(in.Texts) > 256 {
		Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "texts must list 1 to 256 samples; phase is request or response; plane is llmplex or mcplex")
		return
	}
	resolved := policy.Resolved()
	meta := hooks.Meta{Plane: in.Plane, Operation: "test"}
	if identity := GetWIFIdentity(r.Context()); identity != nil {
		meta.PrincipalID = identity.PrincipalID
	}
	out := hooks.Run(r.Context(), h.hookRuntime(tenant), resolved, meta, in.Phase, hooks.PlainText(in.Texts), nil)
	result := HookPolicyTestResult{Policy: policy.ID, Phase: in.Phase, Decision: out.Action, Results: out.Results, UninspectedParts: out.Uninspected}
	if result.Results == nil {
		result.Results = []usageledger.HookResult{}
	}
	switch {
	case out.Refused():
		result.Code, result.Message = out.Code, out.Message
	case out.Action == hooks.Transform:
		result.Texts, err = hooks.PlainTexts(out.Body)
		if err != nil {
			Error(w, r, http.StatusInternalServerError, "TEST_FAILED", "the transformed sample could not be read")
			return
		}
	default:
		result.Texts = in.Texts
	}
	w.Header().Set("Cache-Control", "no-store")
	JSON(w, http.StatusOK, result)
}
