package api

import (
	"context"
	"sort"

	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
)

// OverviewBudget is a tenant budget that applies to a subject or an access
// point, with its remaining allowance. The overview is an operator view, so
// the counters are the operator's.
type OverviewBudget struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Direct      bool     `json:"direct"`
	MatchedBy   []string `json:"matched_by,omitempty"`
	ViaChild    string   `json:"via_child,omitempty"`
	// ViaAccessPoints names the access points through which the budget applies
	// (members.access_points or members.projects); empty when the subject
	// itself matches.
	ViaAccessPoints []string `json:"via_access_points,omitempty"`
	Unit            string   `json:"unit"`
	Currency        string   `json:"currency,omitempty"`
	Limit           string   `json:"limit"`
	// Counters is "available" or "unavailable". Spent, Reserved, Remaining and
	// Window are absent when unavailable, never zero.
	Counters  string        `json:"counters"`
	Spent     string        `json:"spent,omitempty"`
	Reserved  string        `json:"reserved,omitempty"`
	Remaining string        `json:"remaining,omitempty"`
	Window    *BudgetWindow `json:"window,omitempty"`
}

// OverviewBudgets lists the budgets that apply.
type OverviewBudgets struct {
	Applicable []OverviewBudget `json:"applicable"`
	Note       string           `json:"note,omitempty"`
}

type budgetVariant struct {
	facts  budgets.Facts
	viaAP  string
	nocall bool
}

// budgetsFor resolves the budgets of every variant (a subject's own facts and
// one per access point that admits it) and reads their counters.
func (h *OverviewHandler) budgetsFor(ctx context.Context, tenant string, variants []budgetVariant, note string) OverviewBudgets {
	out := OverviewBudgets{Applicable: []OverviewBudget{}, Note: note}
	c := h.Connected
	if c == nil || c.Store == nil {
		out.Note = "budgets are unavailable"
		return out
	}
	all, err := LoadBudgets(ctx, c.Store, tenant)
	var tree *budgets.Tree
	if err == nil {
		tree, err = budgets.NewTree(all)
	}
	if err != nil {
		out.Note = "budgets are unavailable: the saved budgets could not be read or do not validate"
		return out
	}
	byID := map[string]*OverviewBudget{}
	now := h.now()
	for _, v := range variants {
		for _, m := range tree.Resolve(v.facts) {
			item := byID[m.ID]
			if item == nil {
				b := m.Budget
				item = &OverviewBudget{ID: b.ID, DisplayName: b.DisplayName, Parent: b.Parent, Unit: b.Limit.Unit, Currency: b.Limit.Currency, Limit: b.Limit.Amount, Counters: "unavailable"}
				if bal, scale, cerr := c.budgetCounters(ctx, tenant, b, now); cerr == nil {
					item.Counters = "available"
					item.Limit = budgetAmount(b, bal.Limit, scale)
					item.Spent, item.Reserved, item.Remaining = budgetAmount(b, bal.Consumed, scale), budgetAmount(b, bal.Held, scale), budgetAmount(b, bal.Available, scale)
					item.Window = &BudgetWindow{Start: bal.Window.Start, End: bal.Window.End}
				}
				byID[m.ID] = item
			}
			item.Direct = item.Direct || m.Direct
			if v.viaAP == "" {
				item.MatchedBy = appendUnique(item.MatchedBy, m.Reasons...)
				if m.ViaChild != "" && item.ViaChild == "" {
					item.ViaChild = m.ViaChild
				}
			} else {
				item.ViaAccessPoints = appendUnique(item.ViaAccessPoints, v.viaAP)
				if m.Direct {
					item.MatchedBy = appendUnique(item.MatchedBy, m.Reasons...)
				} else if item.ViaChild == "" {
					item.ViaChild = m.ViaChild
				}
			}
		}
	}
	for _, item := range byID {
		sort.Strings(item.MatchedBy)
		sort.Strings(item.ViaAccessPoints)
		out.Applicable = append(out.Applicable, *item)
	}
	sort.Slice(out.Applicable, func(i, j int) bool { return out.Applicable[i].ID < out.Applicable[j].ID })
	return out
}

func appendUnique(list []string, values ...string) []string {
	for _, v := range values {
		found := false
		for _, have := range list {
			if have == v {
				found = true
			}
		}
		if !found {
			list = append(list, v)
		}
	}
	return list
}

const (
	overviewBudgetSubjectNote = "Budgets that select by group apply per request from the token's verified groups, which this view cannot know; use `aiplex budgets preview` with a subject's groups."
	overviewBudgetAPNote      = "Only budgets that select this access point or its project are listed; budgets that select callers by group, principal, agent or client apply per request."
)
