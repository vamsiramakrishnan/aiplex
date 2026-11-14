package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// BudgetKind is the tenant resource kind of a budget.
const BudgetKind = budgets.Kind

const budgetMaxBody = 64 << 10

// MountBudgets mounts /api/v1/budgets. Operators (admin or deployer) read the
// saved resource; writes and previews are admin only; any verified caller may
// read the status of a budget it is a direct member of.
func (h *ConnectedHandler) MountBudgets(r chi.Router) {
	h.AttachBudgets()
	r.Route("/budgets", func(r chi.Router) {
		r.Get("/", h.budgetList)
		r.Post("/preview", h.budgetPreview)
		r.Get("/{id}", h.budgetGet)
		r.Put("/{id}", h.budgetPut)
		r.Delete("/{id}", h.budgetDelete)
		r.Get("/{id}/status", h.budgetStatusHandler)
	})
}

func (h *ConnectedHandler) isOperator(r *http.Request) bool {
	return privilegedOrDevelopment(r, h.Development, models.RoleAdmin, models.RoleDeployer)
}

func (h *ConnectedHandler) isAdmin(r *http.Request) bool {
	return privilegedOrDevelopment(r, h.Development, models.RoleAdmin)
}

func (h *ConnectedHandler) budgetEngine(tenant string) *quotaplex.Engine {
	if h.Quota == nil {
		return nil
	}
	return h.Quota.Engines()[tenant]
}

func budgetStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, resources.ErrNotFound):
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "budget not found")
	case errors.Is(err, resources.ErrConflict):
		Error(w, r, http.StatusConflict, "REVISION_CONFLICT", "resource revision changed")
	default:
		Error(w, r, http.StatusServiceUnavailable, "BUDGETS_UNAVAILABLE", "budgets are unavailable")
	}
}

// budgetFieldError writes a validation failure naming the offending field.
func budgetFieldError(w http.ResponseWriter, r *http.Request, err error) {
	var fe *budgets.FieldError
	if errors.As(err, &fe) {
		JSON(w, http.StatusBadRequest, models.APIError{Code: "INVALID_SPEC", Message: err.Error(), Details: map[string]any{"field": fe.Field}, RequestID: GetRequestID(r.Context())})
		return
	}
	if field, ok := specErrorField(err); ok {
		JSON(w, http.StatusBadRequest, models.APIError{Code: "INVALID_SPEC", Message: err.Error(), Details: map[string]any{"field": field}, RequestID: GetRequestID(r.Context())})
		return
	}
	Error(w, r, http.StatusBadRequest, "INVALID_SPEC", err.Error())
}

func (h *ConnectedHandler) budgetList(w http.ResponseWriter, r *http.Request) {
	if !h.isOperator(r) {
		Error(w, r, http.StatusForbidden, "FORBIDDEN", "listing budgets requires the admin or deployer role")
		return
	}
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	records, err := h.Store.List(r.Context(), tenant, BudgetKind)
	if err != nil {
		budgetStoreError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, records)
}

func (h *ConnectedHandler) budgetGet(w http.ResponseWriter, r *http.Request) {
	if !h.isOperator(r) {
		Error(w, r, http.StatusForbidden, "FORBIDDEN", "reading a budget's definition requires the admin or deployer role; members read /budgets/{id}/status")
		return
	}
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	rec, err := h.Store.Get(r.Context(), tenant, BudgetKind, idParam(r))
	if err != nil {
		budgetStoreError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", rec.Revision))
	JSON(w, http.StatusOK, rec)
}

// validateBudget checks a specification and everything it depends on: the
// whole tenant tree with the budget in place, referenced access points and
// client registrations, and, when QuotaPlex runs, that the quota policy can
// meter the budget exactly.
func (h *ConnectedHandler) validateBudget(ctx context.Context, tenant, id string, raw []byte) (budgets.Budget, error) {
	b, err := budgets.Decode(id, raw)
	if err != nil {
		return b, err
	}
	if err = b.Validate(); err != nil {
		return b, err
	}
	if h.Quota == nil || h.budgetEngine(tenant) == nil {
		return b, errBudgetsNeedQuota
	}
	if err = h.validateBudgetReferences(ctx, tenant, b); err != nil {
		return b, err
	}
	all, err := LoadBudgets(ctx, h.Store, tenant)
	if err != nil {
		if errors.Is(err, errBudgetInvalid) {
			return b, fmt.Errorf("another saved budget is invalid: %w", err)
		}
		return b, fmt.Errorf("%w: budgets could not be read", errAccessPointUnavailable)
	}
	merged := []budgets.Budget{b}
	for _, other := range all {
		if other.ID != id {
			merged = append(merged, other)
		}
	}
	if _, err = budgets.NewTree(merged); err != nil {
		return b, err
	}
	if err = h.checkBudgetMetering(tenant, b); err != nil {
		return b, err
	}
	return b, nil
}

// checkBudgetMetering refuses a budget the quota policy cannot count exactly.
func (h *ConnectedHandler) checkBudgetMetering(tenant string, b budgets.Budget) error {
	engine := h.budgetEngine(tenant)
	if engine == nil {
		return nil
	}
	meters := engine.PolicyMeters()
	if b.Limit.Unit == budgets.UnitCost {
		scale, found, err := moneyScale(meters, b.Limit.Currency)
		if err != nil {
			return withField("limit/currency", err)
		}
		if !found {
			return withField("limit/currency", fmt.Errorf("the quota policy has no money meter in %s, so a %s cost budget cannot be enforced", b.Limit.Currency, b.Limit.Currency))
		}
		if _, err = b.Draw(scale); err != nil {
			return err
		}
		return nil
	}
	unit := b.Limit.Unit
	for _, m := range meters {
		if m.Unit == unit {
			return nil
		}
	}
	return withField("limit/unit", fmt.Errorf("the quota policy has no %s meter, so this budget cannot be enforced", unit))
}

func (h *ConnectedHandler) validateBudgetReferences(ctx context.Context, tenant string, b budgets.Budget) error {
	for i, ref := range b.Members.ClientRegistrations {
		record, err := h.Store.Get(ctx, tenant, clientplex.Kind, ref)
		if err != nil && !errors.Is(err, resources.ErrNotFound) {
			return referenceUnavailable(err)
		}
		var registration clientplex.Registration
		if err != nil || json.Unmarshal(record.Spec, &registration) != nil || registration.ID != ref {
			return withField(fmt.Sprintf("members/client_registrations/%d", i), fmt.Errorf("client registration %q does not resolve in this tenant", ref))
		}
	}
	for i, ref := range b.Members.AccessPoints {
		if _, _, err := h.loadAccessPointRecord(ctx, tenant, ref); err != nil {
			if errors.Is(err, resources.ErrNotFound) {
				return withField(fmt.Sprintf("members/access_points/%d", i), fmt.Errorf("access point %q does not resolve in this tenant", ref))
			}
			if !errors.Is(err, errAccessPointRecordInvalid) {
				return referenceUnavailable(err)
			}
		}
	}
	return nil
}

func (h *ConnectedHandler) budgetPut(w http.ResponseWriter, r *http.Request) {
	if !h.requireBudgetAdministrator(w, r) {
		return
	}
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	id := idParam(r)
	expected, err := revision(r)
	if err != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_REVISION", "If-Match must be a revision")
		return
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, budgetMaxBody+1))
	if err != nil || len(payload) > budgetMaxBody {
		Error(w, r, http.StatusBadRequest, "INVALID_SPEC", "bounded JSON required")
		return
	}
	if _, err = h.validateBudget(r.Context(), tenant, id, payload); err != nil {
		if errors.Is(err, errBudgetsNeedQuota) {
			Error(w, r, http.StatusConflict, "QUOTAPLEX_REQUIRED", err.Error())
			return
		}
		if errors.Is(err, errAccessPointUnavailable) {
			Error(w, r, http.StatusServiceUnavailable, "RESOURCE_STORE_UNAVAILABLE", "a referenced resource could not be read")
			return
		}
		budgetFieldError(w, r, err)
		return
	}
	rec, err := h.Store.Put(r.Context(), resources.Record{TenantID: tenant, Kind: BudgetKind, ID: id, Spec: payload}, expected)
	if err != nil {
		budgetStoreError(w, r, err)
		return
	}
	h.budgetCache().Invalidate(tenant)
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", rec.Revision))
	JSON(w, http.StatusOK, rec)
}

func (h *ConnectedHandler) budgetDelete(w http.ResponseWriter, r *http.Request) {
	if !h.requireBudgetAdministrator(w, r) {
		return
	}
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	id := idParam(r)
	expected, err := revision(r)
	if err != nil || expected == 0 {
		Error(w, r, http.StatusPreconditionRequired, "REVISION_REQUIRED", "If-Match revision required")
		return
	}
	all, err := LoadBudgets(r.Context(), h.Store, tenant)
	if err != nil && !errors.Is(err, errBudgetInvalid) {
		budgetStoreError(w, r, err)
		return
	}
	for _, other := range all {
		if other.Parent == id {
			Error(w, r, http.StatusConflict, "BUDGET_HAS_CHILDREN", fmt.Sprintf("budget %q is the parent of %q; delete or re-parent its children first", id, other.ID))
			return
		}
	}
	if err = h.Store.Delete(r.Context(), tenant, BudgetKind, id, expected); err != nil {
		budgetStoreError(w, r, err)
		return
	}
	h.budgetCache().Invalidate(tenant)
	w.WriteHeader(http.StatusNoContent)
}

func (h *ConnectedHandler) requireBudgetAdministrator(w http.ResponseWriter, r *http.Request) bool {
	if h.isAdmin(r) {
		return true
	}
	if GetWIFAccess(r.Context()) == nil {
		Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified role context required")
		return false
	}
	Error(w, r, http.StatusForbidden, "FORBIDDEN", "budget changes require the admin role")
	return false
}

// ── status ───────────────────────────────────────────────────────────────

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

// BudgetStatus is the read model of a budget's counters. Members see the
// top block only (spent, reserved, remaining); operators see all of it.
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
	// IncludeToolCalls is set on a cost budget that also counts tool calls,
	// so spent and reserved include them.
	IncludeToolCalls bool `json:"include_tool_calls,omitempty"`
	// The fields below are operator-only.
	UnknownCostRequests *int64                 `json:"unknown_cost_requests,omitempty"`
	OnUnknownCost       string                 `json:"on_unknown_cost,omitempty"`
	Thresholds          []int                  `json:"thresholds,omitempty"`
	ThresholdEvents     []BudgetThresholdEvent `json:"threshold_events,omitempty"`
	Children            []BudgetChild          `json:"children,omitempty"`
	TopSpenders         []BudgetSpender        `json:"top_spenders,omitempty"`
	Notes               []string               `json:"notes,omitempty"`
}

// budgetCounters reads one budget's counters in the current window.
func (h *ConnectedHandler) budgetCounters(ctx context.Context, tenant string, b budgets.Budget, now time.Time) (quotaplex.DrawBalance, uint8, error) {
	engine := h.budgetEngine(tenant)
	if engine == nil {
		return quotaplex.DrawBalance{}, 0, errors.New("QuotaPlex is not enabled")
	}
	scale, err := budgetDrawScale(b, engine.PolicyMeters())
	if err != nil {
		return quotaplex.DrawBalance{}, 0, err
	}
	draw, err := b.Draw(scale)
	if err != nil {
		return quotaplex.DrawBalance{}, 0, err
	}
	bal, err := engine.DrawBalance(ctx, tenant, draw, now)
	return bal, scale, err
}

func budgetAmount(b budgets.Budget, v int64, scale uint8) string {
	if v < 0 {
		v = 0
	}
	if b.Limit.Unit != budgets.UnitCost {
		scale = 0
	}
	return budgets.FormatAmount(v, scale)
}

func (h *ConnectedHandler) now() time.Time {
	if h.Quota != nil && h.Quota.clock != nil {
		return h.Quota.clock().UTC()
	}
	return time.Now().UTC()
}

// budgetStatus builds the status of one budget. full adds the operator-only
// blocks; spenders adds the ledger-derived top spenders (admin only).
func (h *ConnectedHandler) budgetStatus(ctx context.Context, tenant string, tree *budgets.Tree, b budgets.Budget, full, spenders bool) (BudgetStatus, error) {
	now := h.now()
	bal, scale, err := h.budgetCounters(ctx, tenant, b, now)
	if err != nil {
		return BudgetStatus{}, err
	}
	st := BudgetStatus{ID: b.ID, DisplayName: b.DisplayName, Parent: b.Parent, Unit: b.Limit.Unit, Currency: b.Limit.Currency, Window: BudgetWindow{Start: bal.Window.Start, End: bal.Window.End}, IncludeToolCalls: b.IncludeToolCalls}
	st.Limit = budgetAmount(b, bal.Limit, scale)
	st.Spent = budgetAmount(b, bal.Consumed, scale)
	st.Reserved = budgetAmount(b, bal.Held, scale)
	st.Remaining = budgetAmount(b, bal.Available, scale)
	st.OverLimit = bal.Available < 0
	if !full {
		return st, nil
	}
	unknown := bal.UnknownCost
	if b.Limit.Unit == budgets.UnitCost {
		st.UnknownCostRequests = &unknown
		st.OnUnknownCost = b.EffectiveOnUnknownCost()
	}
	st.Thresholds = b.EffectiveThresholds()
	for _, c := range bal.Crossed {
		st.ThresholdEvents = append(st.ThresholdEvents, BudgetThresholdEvent{Percent: c.Percent, At: c.At, Consumed: budgetAmount(b, c.Consumed, scale)})
	}
	for _, childID := range tree.Children(b.ID) {
		child, _ := tree.Get(childID)
		cb, cs, cerr := h.budgetCounters(ctx, tenant, child, now)
		if cerr != nil {
			st.Notes = append(st.Notes, fmt.Sprintf("child %q counters are unavailable", childID))
			continue
		}
		st.Children = append(st.Children, BudgetChild{ID: child.ID, DisplayName: child.DisplayName, Unit: child.Limit.Unit, Currency: child.Limit.Currency, Limit: budgetAmount(child, cb.Limit, cs), Spent: budgetAmount(child, cb.Consumed, cs), Reserved: budgetAmount(child, cb.Held, cs), Remaining: budgetAmount(child, cb.Available, cs)})
	}
	if b.EffectiveOnUnknownCost() == budgets.OnUnknownCostAllowAndFlag && unknown > 0 {
		st.Notes = append(st.Notes, fmt.Sprintf("%d request(s) in this window could not be costed and are not in spent", unknown))
	}
	if spenders {
		top, note := h.budgetSpenders(ctx, tenant, b, bal.Window)
		st.TopSpenders = top
		if note != "" {
			st.Notes = append(st.Notes, note)
		}
	}
	return st, nil
}

// spenderTokenRank orders token spenders. The ledger's token counters overlap
// (total_tokens covers input and output; cache and reasoning counts can be
// subsets of those), so they are never added together: the rank is the known
// total, or input plus output where no total was recorded. It is an ordering
// over ledger records, not the budget's own count.
func spenderTokenRank(tokens map[string]int64) int64 {
	if total := tokens[usageledger.CounterTotal]; total > 0 {
		return total
	}
	return tokens[usageledger.CounterInput] + tokens[usageledger.CounterOutput]
}

// budgetSpenders ranks principals by their ledger usage under the budget in
// its current window. The ledger is a separate record from the budget's
// counters; see docs/budgets.md for how the two relate.
func (h *ConnectedHandler) budgetSpenders(ctx context.Context, tenant string, b budgets.Budget, w quotaplex.Window) ([]BudgetSpender, string) {
	if h.Ledger == nil || h.Ledger.Store() == nil {
		return nil, "top spenders need the usage ledger, which is not configured"
	}
	summary, err := usageledger.Summarize(ctx, h.Ledger.Store(), tenant, usageledger.SummaryQuery{Filter: usageledger.Filter{BudgetID: b.ID, Since: w.Start, Until: w.End}, GroupBy: []string{"principal_id"}})
	if err != nil {
		return nil, "top spenders are unavailable: the usage ledger could not be read"
	}
	rank := func(g usageledger.SummaryGroup) *bigRank {
		switch b.Limit.Unit {
		case budgets.UnitCost:
			for _, c := range g.Cost {
				if c.Currency == b.Limit.Currency {
					return newBigRank(c.Amount, c.Scale)
				}
			}
			return newBigRank("0", 0)
		case budgets.UnitTokens:
			return newBigRank(fmt.Sprint(spenderTokenRank(g.Tokens)), 0)
		}
		return newBigRank(fmt.Sprint(g.Requests), 0)
	}
	groups := summary.Groups
	sort.SliceStable(groups, func(i, j int) bool {
		if c := rank(groups[i]).cmp(rank(groups[j])); c != 0 {
			return c > 0
		}
		return groups[i].Key["principal_id"] < groups[j].Key["principal_id"]
	})
	var out []BudgetSpender
	for _, g := range groups {
		if len(out) == 10 {
			break
		}
		out = append(out, BudgetSpender{PrincipalID: g.Key["principal_id"], Display: g.Labels["principal_id"], Requests: g.Requests, Tokens: g.Tokens, Cost: g.Cost, UnknownCostRecords: g.UnknownCostRecords})
	}
	note := ""
	if summary.Truncated {
		note = "top spenders cover the newest ledger records only"
	}
	return out, note
}

// callerFacts returns the matching facts for the verified caller: one set
// without an access point and one for each access point that admits it.
// Group membership the identity source did not verify is treated as no
// membership: unknown never makes a caller a member.
func (h *ConnectedHandler) callerFacts(ctx context.Context, tenant string, identity *models.WIFIdentity) ([]budgets.Facts, error) {
	if identity == nil || identity.PrincipalID == "" {
		return nil, errors.New("verified identity required")
	}
	base := budgets.Facts{PrincipalID: identity.PrincipalID}
	source := auth.MembershipSource(identity.Provider, identity.PoolID)
	for _, g := range identityGroups(identity) {
		base.Groups = append(base.Groups, quotaplex.GroupRef{Source: source, ID: g})
	}
	if identity.ActorID != "" {
		base.AgentIDs = append(base.AgentIDs, identity.ActorID)
	} else if !identity.IsWorkforce {
		base.AgentIDs = append(base.AgentIDs, identity.PrincipalID)
	}
	caller, err := h.resolveAccessPointCaller(ctx, tenant, identity)
	if err != nil {
		return nil, err
	}
	base.ClientID = caller.ClientID
	out := []budgets.Facts{base}
	records, err := h.Store.List(ctx, tenant, AccessPointKind)
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		ap, err := decodeAccessPoint(rec.ID, rec.Spec)
		if err != nil || !ap.admits(identity, caller) {
			continue
		}
		f := base
		f.AccessPointID, f.Project = ap.ID, ap.Project
		out = append(out, f)
	}
	return out, nil
}

func (h *ConnectedHandler) budgetStatusHandler(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	identity := GetWIFIdentity(r.Context())
	operator := h.isOperator(r)
	if identity == nil && !operator {
		Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
		return
	}
	id := idParam(r)
	all, err := LoadBudgets(r.Context(), h.Store, tenant)
	var tree *budgets.Tree
	if err == nil {
		tree, err = budgets.NewTree(all)
	}
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "BUDGETS_UNAVAILABLE", "budgets are unavailable")
		return
	}
	b, found := tree.Get(id)
	if !found {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "budget not found")
		return
	}
	if !operator {
		facts, err := h.callerFacts(r.Context(), tenant, identity)
		if err != nil {
			Error(w, r, http.StatusServiceUnavailable, "BUDGETS_UNAVAILABLE", "budgets are unavailable")
			return
		}
		member := false
		for _, f := range facts {
			if tree.DirectMember(id, f) {
				member = true
			}
		}
		if !member {
			// The same answer as a missing budget: non-members learn nothing.
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "budget not found")
			return
		}
	}
	st, err := h.budgetStatus(r.Context(), tenant, tree, b, operator, operator && h.isAdmin(r))
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "BUDGET_STATUS_UNAVAILABLE", "budget counters are unavailable")
		return
	}
	if operator && !h.isAdmin(r) {
		st.Notes = append(st.Notes, "top spenders are usage data and require the admin role")
	}
	JSON(w, http.StatusOK, st)
}

// ── preview ──────────────────────────────────────────────────────────────

// BudgetPreviewSubject is a hypothetical caller. Nothing in it is verified;
// the preview says what would happen if a request carried these facts.
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
	Plane       string               `json:"plane,omitempty"` // llmplex (default), mcplex or a2aplex
	// Draft is a budget specification to include as if it were saved. It is
	// validated like a save; nothing is stored.
	Draft *budgets.Budget `json:"draft,omitempty"`
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
	// IncludeToolCalls is set on a cost budget that also counts tool calls.
	IncludeToolCalls bool `json:"include_tool_calls,omitempty"`
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

func previewDraw(m budgets.Match, draft bool) BudgetPreviewDraw {
	b := m.Budget
	return BudgetPreviewDraw{ID: b.ID, DisplayName: b.DisplayName, Parent: b.Parent, Direct: m.Direct, Reasons: m.Reasons, ViaChild: m.ViaChild, Unit: b.Limit.Unit, Currency: b.Limit.Currency, Limit: b.Limit.Amount, Window: b.Window, Draft: draft, IncludeToolCalls: b.IncludeToolCalls}
}

func (h *ConnectedHandler) budgetPreview(w http.ResponseWriter, r *http.Request) {
	if !h.requireBudgetAdministrator(w, r) {
		return
	}
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, budgetMaxBody+1))
	if err != nil || len(raw) > budgetMaxBody {
		Error(w, r, http.StatusBadRequest, "INVALID_SPEC", "bounded JSON required")
		return
	}
	var req BudgetPreviewRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&req); err != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_SPEC", "invalid preview request: "+err.Error())
		return
	}
	plane := req.Plane
	switch plane {
	case "":
		plane = "llmplex"
	case "llmplex", "mcplex", "a2aplex":
	default:
		Error(w, r, http.StatusBadRequest, "INVALID_SPEC", "plane must be llmplex, mcplex or a2aplex")
		return
	}
	all, err := LoadBudgets(r.Context(), h.Store, tenant)
	if err != nil && !errors.Is(err, errBudgetInvalid) {
		budgetStoreError(w, r, err)
		return
	}
	if err != nil {
		Error(w, r, http.StatusConflict, "BUDGETS_INVALID", err.Error())
		return
	}
	out := BudgetPreview{Hypothetical: true, Plane: plane, Draws: []BudgetPreviewDraw{}, Skipped: []BudgetPreviewSkipped{}, Notes: []string{"The subject is hypothetical; nothing in it is verified. A real request uses the facts of its verified token."}}
	if req.Draft != nil {
		d := &BudgetPreviewDraft{}
		out.Draft = d
		spec, _ := json.Marshal(req.Draft)
		if _, verr := h.validateBudget(r.Context(), tenant, req.Draft.ID, spec); verr != nil {
			d.Error = verr.Error()
			var fe *budgets.FieldError
			if errors.As(verr, &fe) {
				d.Field = fe.Field
			} else if f, ok := specErrorField(verr); ok {
				d.Field = f
			}
		} else {
			d.Valid = true
			next := []budgets.Budget{*req.Draft}
			for _, o := range all {
				if o.ID != req.Draft.ID {
					next = append(next, o)
				}
			}
			all = next
		}
	}
	tree, err := budgets.NewTree(all)
	if err != nil {
		Error(w, r, http.StatusConflict, "BUDGETS_INVALID", err.Error())
		return
	}
	facts := budgets.Facts{Groups: req.Subject.Groups, PrincipalID: req.Subject.Principal, ClientID: req.Subject.Client}
	if req.Subject.Agent != "" {
		facts.AgentIDs = []string{req.Subject.Agent}
	}
	if req.AccessPoint != "" {
		_, ap, lerr := h.loadAccessPointRecord(r.Context(), tenant, req.AccessPoint)
		if lerr != nil && !errors.Is(lerr, errAccessPointRecordInvalid) {
			if errors.Is(lerr, resources.ErrNotFound) {
				Error(w, r, http.StatusNotFound, "NOT_FOUND", "access point not found")
			} else {
				Error(w, r, http.StatusServiceUnavailable, "ACCESS_POINT_UNAVAILABLE", "access point configuration is unavailable")
			}
			return
		}
		facts.AccessPointID, facts.Project = req.AccessPoint, ap.Project
		out.AccessPoint, out.Project = req.AccessPoint, ap.Project
	}
	for _, m := range tree.Resolve(facts) {
		isDraft := req.Draft != nil && out.Draft != nil && out.Draft.Valid && m.ID == req.Draft.ID
		if reason := m.Budget.NotApplied(plane); reason != "" {
			out.Skipped = append(out.Skipped, BudgetPreviewSkipped{ID: m.ID, Reason: reason})
			continue
		}
		out.Draws = append(out.Draws, previewDraw(m, isDraft))
	}
	if len(out.Draws) > 16 {
		out.Notes = append(out.Notes, "a real request would be refused: it draws from more than 16 budgets")
	}
	JSON(w, http.StatusOK, out)
}

// errBudgetsNeedQuota is returned when a budget would be saved without an
// enforcer. A budget that cannot be enforced must not look active.
var errBudgetsNeedQuota = errors.New("budgets are enforced by QuotaPlex, which is not enabled for this tenant; enable the quotaplex module first")

type bigRank struct{ r *big.Rat }

func newBigRank(amount string, scale uint8) *bigRank {
	n, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		n = new(big.Int)
	}
	r := new(big.Rat).SetInt(n)
	div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	return &bigRank{r: r.Quo(r, new(big.Rat).SetInt(div))}
}

func (a *bigRank) cmp(b *bigRank) int { return a.r.Cmp(b.r) }
