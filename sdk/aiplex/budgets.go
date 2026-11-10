package aiplex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Budgets: team and project limits above per-caller quota. A budget is
// enforced by QuotaPlex; see docs/budgets.md.

// Budget is the saved specification of a tenant budget.
type Budget = budgets.Budget

// BudgetRecord is a stored budget with its revision, the optimistic
// concurrency token for replace and delete.
type BudgetRecord struct {
	TenantID string `json:"tenant_id"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Spec     Budget `json:"spec"`
}

func budgetPath(id string) (string, error) {
	if err := validateResourceID(id); err != nil {
		return "", err
	}
	return "/api/v1/budgets/" + url.PathEscape(id), nil
}

// ListBudgets returns every budget of the tenant (operators only).
func (c *Client) ListBudgets(ctx context.Context) ([]BudgetRecord, error) {
	out := []BudgetRecord{}
	if err := c.do(ctx, http.MethodGet, "/api/v1/budgets", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetBudget returns one budget including its current Revision (operators only).
func (c *Client) GetBudget(ctx context.Context, id string) (*BudgetRecord, error) {
	path, err := budgetPath(id)
	if err != nil {
		return nil, err
	}
	var out BudgetRecord
	if err = c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutBudget creates (expectedRevision 0) or replaces (expectedRevision equal
// to the stored revision) the budget named by spec.ID.
func (c *Client) PutBudget(ctx context.Context, spec Budget, expectedRevision uint64) (*BudgetRecord, error) {
	path, err := budgetPath(spec.ID)
	if err != nil {
		return nil, err
	}
	var out BudgetRecord
	if err = c.doWithHeaders(ctx, http.MethodPut, path, spec, &out, map[string]string{"If-Match": strconv.FormatUint(expectedRevision, 10)}); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteBudget removes a budget; the current revision is required. Its
// counters stay in the quota store until their windows end.
func (c *Client) DeleteBudget(ctx context.Context, id string, expectedRevision uint64) error {
	path, err := budgetPath(id)
	if err != nil {
		return err
	}
	if expectedRevision == 0 {
		return fmt.Errorf("aiplex: positive expected revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, path, nil, nil, map[string]string{"If-Match": strconv.FormatUint(expectedRevision, 10)})
}

// BudgetWindow is the budget's current window.
type BudgetWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// BudgetThresholdEvent records that consumption reached a threshold.
type BudgetThresholdEvent struct {
	Percent  int       `json:"percent"`
	At       time.Time `json:"at"`
	Consumed string    `json:"consumed"`
}

// BudgetSpender is one principal's use of a budget in its window.
type BudgetSpender struct {
	PrincipalID        string                  `json:"principal_id"`
	Display            string                  `json:"display,omitempty"`
	Requests           int64                   `json:"requests"`
	Tokens             map[string]int64        `json:"tokens,omitempty"`
	Cost               []usageledger.CostTotal `json:"cost,omitempty"`
	UnknownCostRecords int64                   `json:"unknown_cost_records,omitempty"`
}

// BudgetChild is a child budget's own counters.
type BudgetChild struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Unit        string `json:"unit"`
	Currency    string `json:"currency,omitempty"`
	Limit       string `json:"limit"`
	Spent       string `json:"spent"`
	Reserved    string `json:"reserved"`
	Remaining   string `json:"remaining"`
}

// BudgetStatus is a budget's counters in its current window. Members see the
// first block (spent, reserved, remaining); operators see all of it.
type BudgetStatus struct {
	ID          string       `json:"id"`
	DisplayName string       `json:"display_name,omitempty"`
	Parent      string       `json:"parent,omitempty"`
	Unit        string       `json:"unit"`
	Currency    string       `json:"currency,omitempty"`
	Limit       string       `json:"limit"`
	Spent       string       `json:"spent"`
	Reserved    string       `json:"reserved"`
	Remaining   string       `json:"remaining"`
	OverLimit   bool         `json:"over_limit,omitempty"`
	Window      BudgetWindow `json:"window"`

	UnknownCostRequests *int64                 `json:"unknown_cost_requests,omitempty"`
	OnUnknownCost       string                 `json:"on_unknown_cost,omitempty"`
	Thresholds          []int                  `json:"thresholds,omitempty"`
	ThresholdEvents     []BudgetThresholdEvent `json:"threshold_events,omitempty"`
	Children            []BudgetChild          `json:"children,omitempty"`
	TopSpenders         []BudgetSpender        `json:"top_spenders,omitempty"`
	Notes               []string               `json:"notes,omitempty"`
}

// GetBudgetStatus returns a budget's counters. A caller that is not a direct
// member of the budget (and is not an operator) gets a not-found answer.
func (c *Client) GetBudgetStatus(ctx context.Context, id string) (*BudgetStatus, error) {
	path, err := budgetPath(id)
	if err != nil {
		return nil, err
	}
	var out BudgetStatus
	if err = c.do(ctx, http.MethodGet, path+"/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BudgetPreviewSubject is a hypothetical caller; nothing in it is verified.
type BudgetPreviewSubject struct {
	Principal string               `json:"principal,omitempty"`
	Groups    []quotaplex.GroupRef `json:"groups,omitempty"`
	Agent     string               `json:"agent,omitempty"`
	Client    string               `json:"client,omitempty"`
}

// BudgetPreviewRequest asks which budgets a request would draw from.
type BudgetPreviewRequest struct {
	Subject     BudgetPreviewSubject `json:"subject"`
	AccessPoint string               `json:"access_point,omitempty"`
	Plane       string               `json:"plane,omitempty"`
	// Draft is a specification validated as if saved and included in the
	// answer; it is never stored.
	Draft *Budget `json:"draft,omitempty"`
}

// BudgetPreviewDraw is one budget the hypothetical request would draw from.
type BudgetPreviewDraw struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name,omitempty"`
	Parent      string         `json:"parent,omitempty"`
	Direct      bool           `json:"direct"`
	Reasons     []string       `json:"reasons,omitempty"`
	ViaChild    string         `json:"via_child,omitempty"`
	Unit        string         `json:"unit"`
	Currency    string         `json:"currency,omitempty"`
	Limit       string         `json:"limit"`
	Window      budgets.Window `json:"window"`
	Draft       bool           `json:"draft,omitempty"`
}

// BudgetPreviewSkipped is a matched budget that does not count this plane.
type BudgetPreviewSkipped struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// BudgetPreviewDraft reports the draft's validation.
type BudgetPreviewDraft struct {
	Valid bool   `json:"valid"`
	Field string `json:"field,omitempty"`
	Error string `json:"error,omitempty"`
}

// BudgetPreview is the dry-run answer.
type BudgetPreview struct {
	Hypothetical bool                   `json:"hypothetical"`
	Plane        string                 `json:"plane"`
	AccessPoint  string                 `json:"access_point,omitempty"`
	Project      string                 `json:"project,omitempty"`
	Draws        []BudgetPreviewDraw    `json:"draws"`
	Skipped      []BudgetPreviewSkipped `json:"skipped"`
	Draft        *BudgetPreviewDraft    `json:"draft,omitempty"`
	Notes        []string               `json:"notes"`
}

// PreviewBudgets answers which budgets a hypothetical request would draw from
// and why (POST /budgets/preview, admin only). It changes nothing.
func (c *Client) PreviewBudgets(ctx context.Context, req BudgetPreviewRequest) (*BudgetPreview, error) {
	var out BudgetPreview
	if err := c.do(ctx, http.MethodPost, "/api/v1/budgets/preview", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OverviewBudget is a budget that applies to a subject or access point.
type OverviewBudget struct {
	ID              string        `json:"id"`
	DisplayName     string        `json:"display_name,omitempty"`
	Parent          string        `json:"parent,omitempty"`
	Direct          bool          `json:"direct"`
	MatchedBy       []string      `json:"matched_by,omitempty"`
	ViaChild        string        `json:"via_child,omitempty"`
	ViaAccessPoints []string      `json:"via_access_points,omitempty"`
	Unit            string        `json:"unit"`
	Currency        string        `json:"currency,omitempty"`
	Limit           string        `json:"limit"`
	Counters        string        `json:"counters"`
	Spent           string        `json:"spent,omitempty"`
	Reserved        string        `json:"reserved,omitempty"`
	Remaining       string        `json:"remaining,omitempty"`
	Window          *BudgetWindow `json:"window,omitempty"`
}

// OverviewBudgets lists the budgets that apply.
type OverviewBudgets struct {
	Applicable []OverviewBudget `json:"applicable"`
	Note       string           `json:"note,omitempty"`
}
