package aiplex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

func (c *Client) GetQuotaPolicy(ctx context.Context) (*quotaplex.PolicyInspection, error) {
	var out quotaplex.PolicyInspection
	err := c.do(ctx, http.MethodGet, "/api/v1/quota/policy", nil, &out)
	return &out, err
}
func (c *Client) ValidateQuotaPolicy(ctx context.Context, policy quotaplex.Policy, expectedDigest string) (*quotaplex.PolicyArtifact, error) {
	var out quotaplex.PolicyArtifact
	err := c.do(ctx, http.MethodPost, "/api/v1/quota/policy/validate", struct {
		Policy         quotaplex.Policy `json:"policy"`
		ExpectedDigest string           `json:"expected_digest"`
	}{policy, expectedDigest}, &out)
	return &out, err
}
func (c *Client) PreviewQuota(ctx context.Context, demand quotaplex.Demand) (*quotaplex.AdmissionPreview, error) {
	var out quotaplex.AdmissionPreview
	err := c.do(ctx, http.MethodPost, "/api/v1/quota/preview", demand, &out)
	return &out, err
}
func (c *Client) GetQuotaInventory(ctx context.Context, kind, after string, limit int) (*quotaplex.AdministrationPage, error) {
	switch kind {
	case "budgets", "loans", "attempts", "reconciliations", "audit", "terminal-work":
	default:
		return nil, fmt.Errorf("quota inventory kind %q is not one of budgets, loans, attempts, reconciliations, audit, terminal-work", kind)
	}
	if limit < 1 || limit > 64 {
		return nil, fmt.Errorf("quota inventory limit must be in [1,64]")
	}
	query := url.Values{"kind": []string{kind}, "limit": []string{strconv.Itoa(limit)}}
	if after != "" {
		query.Set("after", after)
	}
	var out quotaplex.AdministrationPage
	err := c.do(ctx, http.MethodGet, "/api/v1/quota/inventory?"+query.Encode(), nil, &out)
	return &out, err
}

// GetQuotaUnknownAttempts reads one page of the attempts inventory filtered to
// attempts in the unknown state that were dispatched at least olderThan ago
// (0 selects all of them). The page's Unknown field summarizes the matches on
// that page: count, oldest dispatch time and exposure per meter, where an
// amount that could not be computed has a null quantity. Merge the summaries
// of every page (UnknownExposure.Merge) for the whole tenant; a page can
// hold no match and still not be done.
func (c *Client) GetQuotaUnknownAttempts(ctx context.Context, olderThan time.Duration, after string, limit int) (*quotaplex.AdministrationPage, error) {
	if olderThan < 0 {
		return nil, fmt.Errorf("older_than must not be negative")
	}
	if limit < 1 || limit > 64 {
		return nil, fmt.Errorf("quota inventory limit must be in [1,64]")
	}
	query := url.Values{"kind": []string{"attempts"}, "limit": []string{strconv.Itoa(limit)}, "older_than": []string{olderThan.String()}}
	if after != "" {
		query.Set("after", after)
	}
	var out quotaplex.AdministrationPage
	if err := c.do(ctx, http.MethodGet, "/api/v1/quota/inventory?"+query.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if out.Unknown == nil {
		// A server that ignored the filter returned an unfiltered page;
		// treating it as "no unknown attempts" would report unknown as zero.
		return nil, fmt.Errorf("the server did not apply older_than (no unknown-attempt summary in the response)")
	}
	return &out, nil
}
func quotaAdminPath(kind, id string) (string, error) {
	if err := validateResourceID(id); err != nil {
		return "", err
	}
	return "/api/v1/quota/" + kind + "/" + url.PathEscape(id), nil
}
func quotaAdminHeaders(key string) (map[string]string, error) {
	if key == "" || strings.TrimSpace(key) != key || len(key) > 256 || strings.ContainsAny(key, "\x00\r\n") {
		return nil, fmt.Errorf("bounded idempotency key required")
	}
	return map[string]string{"Idempotency-Key": key}, nil
}
func (c *Client) GetQuotaBalance(ctx context.Context, id string) (*quotaplex.BudgetBalance, error) {
	path, err := quotaAdminPath("balances", id)
	if err != nil {
		return nil, err
	}
	var out quotaplex.BudgetBalance
	err = c.do(ctx, http.MethodGet, path, nil, &out)
	return &out, err
}
func (c *Client) GetQuotaLoan(ctx context.Context, id string) (*quotaplex.LoanInfo, error) {
	path, err := quotaAdminPath("loans", id)
	if err != nil {
		return nil, err
	}
	var out quotaplex.LoanInfo
	err = c.do(ctx, http.MethodGet, path, nil, &out)
	return &out, err
}
func (c *Client) GrantQuotaLoan(ctx context.Context, request quotaplex.LoanRequest, key string) (*quotaplex.LoanInfo, error) {
	headers, err := quotaAdminHeaders(key)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"id": request.ID, "rule_id": request.RuleID, "amount": request.Amount, "expires_at": request.ExpiresAt}
	var out quotaplex.LoanInfo
	err = c.doWithHeaders(ctx, http.MethodPost, "/api/v1/quota/loans", body, &out, headers)
	return &out, err
}
func (c *Client) ReturnQuotaLoan(ctx context.Context, id, key string) (*quotaplex.LoanInfo, error) {
	path, err := quotaAdminPath("loans", id)
	if err != nil {
		return nil, err
	}
	headers, err := quotaAdminHeaders(key)
	if err != nil {
		return nil, err
	}
	var out quotaplex.LoanInfo
	err = c.doWithHeaders(ctx, http.MethodPost, path+"/return", nil, &out, headers)
	return &out, err
}

type QuotaEvidence struct {
	Evidence        quotaplex.TerminalEvidence     `json:"evidence"`
	Acknowledgement quotaplex.UsageAcknowledgement `json:"acknowledgement"`
}

func (c *Client) GetQuotaEvidence(ctx context.Context, id string) (*QuotaEvidence, error) {
	path, err := quotaAdminPath("evidence", id)
	if err != nil {
		return nil, err
	}
	var out QuotaEvidence
	err = c.do(ctx, http.MethodGet, path, nil, &out)
	return &out, err
}
func (c *Client) PlanQuotaReconciliation(ctx context.Context, request quotaplex.ReconciliationRequest, key string) (*quotaplex.ReconciliationPlan, error) {
	headers, err := quotaAdminHeaders(key)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"id": request.ID, "attempt_id": request.AttemptID, "reason": request.Reason, "evidence_ref": request.EvidenceRef, "usage": request.Usage, "model_version": request.ModelVersion, "provider_resource_id": request.ProviderResourceID, "expires_at": request.ExpiresAt}
	var out quotaplex.ReconciliationPlan
	err = c.doWithHeaders(ctx, http.MethodPost, "/api/v1/quota/reconciliations", body, &out, headers)
	return &out, err
}
func (c *Client) GetQuotaReconciliation(ctx context.Context, id string) (*quotaplex.ReconciliationInspection, error) {
	path, err := quotaAdminPath("reconciliations", id)
	if err != nil {
		return nil, err
	}
	var out quotaplex.ReconciliationInspection
	err = c.do(ctx, http.MethodGet, path, nil, &out)
	return &out, err
}
func (c *Client) ApplyQuotaReconciliation(ctx context.Context, id, digest, key string) (*quotaplex.ReconciliationReceipt, error) {
	path, err := quotaAdminPath("reconciliations", id)
	if err != nil {
		return nil, err
	}
	headers, err := quotaAdminHeaders(key)
	if err != nil {
		return nil, err
	}
	if digest == "" {
		return nil, fmt.Errorf("reviewed plan digest required")
	}
	var out quotaplex.ReconciliationReceipt
	err = c.doWithHeaders(ctx, http.MethodPost, path+"/apply", map[string]string{"digest": digest}, &out, headers)
	return &out, err
}
