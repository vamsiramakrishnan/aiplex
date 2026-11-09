package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// QuotaAdminOptions is operator configuration, never a request-body option.
// Independent planning/approval principals are required unless explicitly relaxed.
type QuotaAdminOptions struct {
	AllowSelfApproval bool `json:"allow_self_approval,omitempty"`
}

func (h *ConnectedHandler) MountQuotaAdministration(r chi.Router) {
	h.MountQuotaAdministrationWithOptions(r, QuotaAdminOptions{})
}
func (h *ConnectedHandler) MountQuotaAdministrationWithOptions(r chi.Router, options QuotaAdminOptions) {
	r.Get("/quota/policy", h.quotaPolicy)
	r.Post("/quota/policy/validate", h.quotaValidatePolicy)
	r.Post("/quota/preview", h.quotaPreview)
	r.Get("/quota/inventory", h.quotaInventory)
	r.Get("/quota/balances/{id}", h.quotaBalance)
	r.Post("/quota/loans", h.quotaGrantLoan)
	r.Get("/quota/loans/{id}", h.quotaGetLoan)
	r.Post("/quota/loans/{id}/return", h.quotaReturnLoan)
	r.Get("/quota/evidence/{id}", h.quotaEvidence)
	r.Post("/quota/reconciliations", func(w http.ResponseWriter, r *http.Request) { h.quotaPlanReconciliation(w, r, options) })
	r.Get("/quota/reconciliations/{id}", h.quotaGetReconciliation)
	r.Post("/quota/reconciliations/{id}/apply", h.quotaApplyReconciliation)
}
func (h *ConnectedHandler) quotaAdministrator(w http.ResponseWriter, r *http.Request, action string, mutation bool) (*quotaplex.Engine, string, quotaplex.Administrator, bool) {
	if h.Quota == nil {
		Error(w, r, 503, "QUOTA_UNAVAILABLE", "quota administration unavailable")
		return nil, "", quotaplex.Administrator{}, false
	}
	tenant, err := h.Tenant(r)
	if err != nil || tenant == "" {
		Error(w, r, 403, "TENANT_REQUIRED", "verified tenant required")
		return nil, "", quotaplex.Administrator{}, false
	}
	identity := GetWIFIdentity(r.Context())
	if identity == nil {
		Error(w, r, 401, "IDENTITY_REQUIRED", "verified identity required")
		return nil, "", quotaplex.Administrator{}, false
	}
	if err = h.authorizeCurrent(r.Context(), r, "aiplex:quota:"+action); err != nil {
		Error(w, r, 403, "QUOTA_ACTION_DENIED", "current quota administration grant required")
		return nil, "", quotaplex.Administrator{}, false
	}
	engine := h.Quota.Engines()[tenant]
	if engine == nil {
		Error(w, r, 404, "NOT_FOUND", "quota tenant unavailable")
		return nil, "", quotaplex.Administrator{}, false
	}
	operation := GetRequestID(r.Context())
	if mutation {
		values := r.Header.Values("Idempotency-Key")
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" || len(values[0]) > 256 || strings.ContainsAny(values[0], "\r\n\x00") {
			Error(w, r, 400, "IDEMPOTENCY_REQUIRED", "one bounded Idempotency-Key required")
			return nil, "", quotaplex.Administrator{}, false
		}
		operation = values[0]
	}
	return engine, tenant, quotaplex.Administrator{PrincipalID: identity.PrincipalID, ActorID: identity.ActorID, RequestID: operation}, true
}
func quotaAdminDecode(r *http.Request, out any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return fmt.Errorf("bounded JSON object required")
	}
	_, err = quotaJSONObject(raw)
	if err != nil {
		return err
	}
	if err = quotaAdminUniqueFields(raw, reflect.TypeOf(out)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("single JSON object required")
	}
	return nil
}
func quotaAdminError(w http.ResponseWriter, r *http.Request, err error) {
	status := 400
	code := "QUOTA_INVALID_REQUEST"
	switch {
	case errors.Is(err, quotaplex.ErrNotFound):
		status = 404
		code = "QUOTA_NOT_FOUND"
	case errors.Is(err, quotaplex.ErrConflict), errors.Is(err, quotaplex.ErrInvalidTransition):
		status = 409
		code = "QUOTA_STATE_CONFLICT"
	case errors.Is(err, quotaplex.ErrDenied):
		status = 403
		code = "QUOTA_OPERATION_DENIED"
	case errors.Is(err, quotaplex.ErrTransactionLimit):
		status = 413
		code = "QUOTA_OPERATION_BOUND_EXCEEDED"
	}
	Error(w, r, status, code, "quota operation could not be completed")
}
func (h *ConnectedHandler) quotaPolicy(w http.ResponseWriter, r *http.Request) {
	e, tenant, _, ok := h.quotaAdministrator(w, r, "read", false)
	if !ok {
		return
	}
	out, err := e.InspectPolicy(r.Context(), tenant)
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaValidatePolicy(w http.ResponseWriter, r *http.Request) {
	e, tenant, _, ok := h.quotaAdministrator(w, r, "simulate", false)
	if !ok {
		return
	}
	var input struct {
		Policy         quotaplex.Policy `json:"policy"`
		ExpectedDigest string           `json:"expected_digest"`
	}
	if err := quotaAdminDecode(r, &input); err != nil {
		quotaAdminError(w, r, err)
		return
	}
	if input.Policy.TenantID != tenant {
		Error(w, r, 403, "TENANT_MISMATCH", "policy tenant must match verified tenant")
		return
	}
	out, err := e.ValidatePolicy(r.Context(), input.Policy, input.ExpectedDigest)
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaPreview(w http.ResponseWriter, r *http.Request) {
	e, tenant, _, ok := h.quotaAdministrator(w, r, "simulate", false)
	if !ok {
		return
	}
	var input quotaplex.Demand
	if err := quotaAdminDecode(r, &input); err != nil {
		quotaAdminError(w, r, err)
		return
	}
	if input.Context.TenantID != tenant {
		Error(w, r, 403, "TENANT_MISMATCH", "demand tenant must match verified tenant")
		return
	}
	out, err := e.Preview(r.Context(), input)
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaInventory(w http.ResponseWriter, r *http.Request) {
	e, tenant, _, ok := h.quotaAdministrator(w, r, "read", false)
	if !ok {
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil {
			quotaAdminError(w, r, err)
			return
		}
	}
	out, err := e.Inventory(r.Context(), tenant, r.URL.Query().Get("kind"), r.URL.Query().Get("after"), limit)
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaBalance(w http.ResponseWriter, r *http.Request) {
	e, tenant, _, ok := h.quotaAdministrator(w, r, "read", false)
	if !ok {
		return
	}
	out, err := e.BudgetBalance(r.Context(), tenant, idParam(r))
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaGetLoan(w http.ResponseWriter, r *http.Request) {
	e, tenant, _, ok := h.quotaAdministrator(w, r, "read", false)
	if !ok {
		return
	}
	out, err := e.GetLoan(r.Context(), tenant, idParam(r))
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaGrantLoan(w http.ResponseWriter, r *http.Request) {
	e, tenant, admin, ok := h.quotaAdministrator(w, r, "loans", true)
	if !ok {
		return
	}
	var input struct {
		ID        string    `json:"id"`
		RuleID    string    `json:"rule_id"`
		Amount    int64     `json:"amount"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := quotaAdminDecode(r, &input); err != nil {
		quotaAdminError(w, r, err)
		return
	}
	out, err := e.GrantLoanAdmin(r.Context(), quotaplex.LoanRequest{TenantID: tenant, ID: input.ID, RuleID: input.RuleID, Amount: input.Amount, ExpiresAt: input.ExpiresAt}, admin)
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaReturnLoan(w http.ResponseWriter, r *http.Request) {
	e, tenant, admin, ok := h.quotaAdministrator(w, r, "loans", true)
	if !ok {
		return
	}
	out, err := e.ReturnUnusedLoanAdmin(r.Context(), tenant, idParam(r), admin)
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaEvidence(w http.ResponseWriter, r *http.Request) {
	e, tenant, _, ok := h.quotaAdministrator(w, r, "read", false)
	if !ok {
		return
	}
	facts, ack, err := e.GetTerminalEvidence(r.Context(), tenant, idParam(r))
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, struct {
		Evidence        quotaplex.TerminalEvidence     `json:"evidence"`
		Acknowledgement quotaplex.UsageAcknowledgement `json:"acknowledgement"`
	}{facts, ack})
}
func (h *ConnectedHandler) quotaPlanReconciliation(w http.ResponseWriter, r *http.Request, options QuotaAdminOptions) {
	e, tenant, admin, ok := h.quotaAdministrator(w, r, "reconcile:plan", true)
	if !ok {
		return
	}
	var input struct {
		ID                 string           `json:"id"`
		AttemptID          string           `json:"attempt_id"`
		Reason             string           `json:"reason"`
		EvidenceRef        string           `json:"evidence_ref"`
		Usage              quotaplex.Vector `json:"usage"`
		ModelVersion       string           `json:"model_version,omitempty"`
		ProviderResourceID string           `json:"provider_resource_id,omitempty"`
		ExpiresAt          time.Time        `json:"expires_at"`
	}
	if err := quotaAdminDecode(r, &input); err != nil {
		quotaAdminError(w, r, err)
		return
	}
	out, err := e.PrepareReconciliation(r.Context(), quotaplex.ReconciliationRequest{TenantID: tenant, ID: input.ID, AttemptID: input.AttemptID, Reason: input.Reason, EvidenceRef: input.EvidenceRef, Usage: input.Usage, ModelVersion: input.ModelVersion, ProviderResourceID: input.ProviderResourceID, ExpiresAt: input.ExpiresAt}, admin, options.AllowSelfApproval)
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaGetReconciliation(w http.ResponseWriter, r *http.Request) {
	e, tenant, _, ok := h.quotaAdministrator(w, r, "read", false)
	if !ok {
		return
	}
	out, err := e.GetReconciliation(r.Context(), tenant, idParam(r))
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
func (h *ConnectedHandler) quotaApplyReconciliation(w http.ResponseWriter, r *http.Request) {
	e, tenant, admin, ok := h.quotaAdministrator(w, r, "reconcile:approve", true)
	if !ok {
		return
	}
	var input struct {
		Digest string `json:"digest"`
	}
	if err := quotaAdminDecode(r, &input); err != nil {
		quotaAdminError(w, r, err)
		return
	}
	out, err := e.ApplyReconciliation(r.Context(), tenant, idParam(r), input.Digest, admin)
	if err != nil {
		quotaAdminError(w, r, err)
		return
	}
	JSON(w, 200, out)
}
