package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

var budgetLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// BudgetSource supplies a tenant's validated budget tree to the quota host.
type BudgetSource interface {
	Tree(ctx context.Context, tenant string) (*budgets.Tree, error)
}

// budgetCacheTTL bounds how long a replica keeps a tenant's budgets. A write
// on this replica invalidates its own cache at once; other replicas see the
// change within this interval.
const budgetCacheTTL = 5 * time.Second

// budgetStaleGrace is how long a cached tree may be used after a failed
// refresh. Past it the host refuses requests that could draw from a budget.
const budgetStaleGrace = 30 * time.Second

// BudgetCache is the registry-backed BudgetSource. It fails closed: a
// record that does not decode or a tree that does not validate is an error,
// never a partial tree.
type BudgetCache struct {
	Store resources.Store
	TTL   time.Duration
	Now   func() time.Time

	mu      sync.Mutex
	entries map[string]*budgetEntry
}

type budgetEntry struct {
	at   time.Time
	tree *budgets.Tree
}

func (c *BudgetCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Invalidate drops a tenant's cached tree.
func (c *BudgetCache) Invalidate(tenant string) {
	c.mu.Lock()
	delete(c.entries, tenant)
	c.mu.Unlock()
}

// LoadAll reads and validates the tenant's budgets straight from the store.
func LoadBudgets(ctx context.Context, store resources.Store, tenant string) ([]budgets.Budget, error) {
	records, err := store.List(ctx, tenant, budgets.Kind)
	if err != nil {
		return nil, err
	}
	out := make([]budgets.Budget, 0, len(records))
	for _, rec := range records {
		if rec.TenantID != tenant || rec.Kind != budgets.Kind {
			continue
		}
		b, err := budgets.Decode(rec.ID, rec.Spec)
		if err == nil {
			err = b.Validate()
		}
		if err != nil {
			return nil, fmt.Errorf("saved budget %q: %w: %v", rec.ID, errBudgetInvalid, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// Tree returns the tenant's budget tree, refreshing it when older than TTL.
func (c *BudgetCache) Tree(ctx context.Context, tenant string) (*budgets.Tree, error) {
	ttl := c.TTL
	if ttl <= 0 {
		ttl = budgetCacheTTL
	}
	now := c.now()
	c.mu.Lock()
	entry := c.entries[tenant]
	c.mu.Unlock()
	if entry != nil && now.Sub(entry.at) < ttl {
		return entry.tree, nil
	}
	all, err := LoadBudgets(ctx, c.Store, tenant)
	var tree *budgets.Tree
	if err == nil {
		if tree, err = budgets.NewTree(all); err != nil {
			err = fmt.Errorf("%w: %v", errBudgetInvalid, err)
		}
	}
	if err != nil {
		if entry != nil && now.Sub(entry.at) < ttl+budgetStaleGrace && !errors.Is(err, errBudgetInvalid) {
			log.Warn().Err(err).Str("tenant", tenant).Msg("budget refresh failed; using the previous tree briefly")
			return entry.tree, nil
		}
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*budgetEntry{}
	}
	c.entries[tenant] = &budgetEntry{at: now, tree: tree}
	c.mu.Unlock()
	return tree, nil
}

// SetBudgets attaches the budget source. Call it before serving.
func (h *QuotaHost) SetBudgets(source BudgetSource) {
	if h != nil {
		h.budgets = source
	}
}

// AttachBudgets connects the handler's budget cache to the quota host.
func (h *ConnectedHandler) AttachBudgets() {
	if h != nil && h.Quota != nil {
		h.Quota.SetBudgets(h.budgetCache())
	}
}

func (h *ConnectedHandler) budgetCache() *BudgetCache {
	h.budgetMu.Lock()
	defer h.budgetMu.Unlock()
	if h.budgetSrc == nil {
		h.budgetSrc = &BudgetCache{Store: h.Store}
	}
	return h.budgetSrc
}

// moneyScale finds the scale of the policy's money meters in a currency. The
// bool is false when the policy has none; the error is set when its money
// meters in the currency disagree on scale (their amounts cannot be summed).
func moneyScale(meters map[string]quotaplex.Meter, currency string) (uint8, bool, error) {
	found, scale := false, uint8(0)
	for _, m := range meters {
		if m.Unit != "money" || m.Currency != currency {
			continue
		}
		if found && m.Scale != scale {
			return 0, true, fmt.Errorf("the quota policy has %s money meters of different scales", currency)
		}
		found, scale = true, m.Scale
	}
	return scale, found, nil
}

// budgetDrawScale is the scale a budget's draw uses under the policy.
func budgetDrawScale(b budgets.Budget, meters map[string]quotaplex.Meter) (uint8, error) {
	if b.Limit.Unit != budgets.UnitCost {
		return 0, nil
	}
	scale, found, err := moneyScale(meters, b.Limit.Currency)
	if err != nil {
		return 0, err
	}
	if !found {
		// No money meter: the engine denies the draw by name. Use the
		// amount's own precision so it still parses.
		_, frac, _ := strings.Cut(b.Limit.Amount, ".")
		return uint8(len(frac)), nil
	}
	return scale, nil
}

// budgetDraws is the outcome of resolving a request's budgets.
type budgetDraws struct {
	Draws   []quotaplex.BudgetDraw
	Matches []budgets.Match
	Skipped []budgets.Match
	Facts   budgets.Facts
}

// budgetFacts builds the matching facts from verified inputs only: the token's
// principal and groups, the verified actor, the access-point-resolved client
// and the access point's administrator-set project.
func budgetFacts(principal quotaplex.Subject, actor *quotaplex.Subject, identity *models.WIFIdentity, target quotaplex.Target, project string) budgets.Facts {
	f := budgets.Facts{Groups: principal.Membership.Groups, PrincipalID: principal.ID, ClientID: target.ClientID, AccessPointID: target.AccessPointID, Project: project}
	if actor != nil {
		f.AgentIDs = append(f.AgentIDs, actor.ID)
	} else if identity != nil && !identity.IsWorkforce {
		// A workload principal acting as itself is an agent.
		f.AgentIDs = append(f.AgentIDs, principal.ID)
	}
	return f
}

// resolveBudgets turns the request's verified facts into draws. With no
// budget source, or no budget the request matches, it returns none.
func (h *QuotaHost) resolveBudgets(ctx context.Context, tenantID string, tenant quotaTenant, facts budgets.Facts, plane string) (budgetDraws, error) {
	out := budgetDraws{Facts: facts}
	if h.budgets == nil {
		return out, nil
	}
	tree, err := h.budgets.Tree(ctx, tenantID)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("budgets unavailable")
		return out, quotaStatus(503, "QUOTA_BUDGETS_UNAVAILABLE", "tenant budgets cannot be verified")
	}
	meters := tenant.policy.Policy().Meters
	for _, m := range tree.Resolve(facts) {
		if !m.Budget.Applies(plane) {
			out.Skipped = append(out.Skipped, m)
			continue
		}
		scale, err := budgetDrawScale(m.Budget, meters)
		if err != nil {
			return out, quotaStatus(503, "QUOTA_BUDGET_UNUSABLE", "a budget cannot be enforced under the quota policy")
		}
		draw, err := m.Budget.Draw(scale)
		if err != nil {
			return out, quotaStatus(503, "QUOTA_BUDGET_UNUSABLE", "a budget cannot be enforced under the quota policy")
		}
		out.Draws = append(out.Draws, draw)
		out.Matches = append(out.Matches, m)
	}
	if len(out.Draws) > 16 {
		return out, quotaStatus(403, "QUOTA_BUDGET_TOO_MANY", "the caller belongs to too many budgets")
	}
	sort.Slice(out.Draws, func(i, j int) bool { return out.Draws[i].ID < out.Draws[j].ID })
	return out, nil
}

// budgetAdmissionError renders a denial that a tenant budget caused. A caller
// that is a direct member of the budget learns its name and remaining
// allowance; a caller that reaches it only through a child budget learns only
// the child's name; anything else gets the generic quota message.
func (h *QuotaHost) budgetAdmissionError(ctx context.Context, tenant quotaTenant, tenantID string, denied *quotaplex.AdmissionDenied, bd budgetDraws) error {
	base := quotaAdmissionError(denied).(*execution.StatusError)
	id := denied.Decision.BudgetID
	if id == "" {
		return base
	}
	var match *budgets.Match
	for i := range bd.Matches {
		if bd.Matches[i].ID == id {
			match = &bd.Matches[i]
		}
	}
	if match == nil {
		return base
	}
	switch denied.Decision.Reason {
	case "unpriced_meter", "budget_cost_unknown":
		base.Status, base.Code = 403, "QUOTA_BUDGET_COST_UNKNOWN"
	case "unsupported_meter":
		base.Status, base.Code = 403, "QUOTA_BUDGET_UNSUPPORTED_OPERATION"
	}
	if !match.Direct {
		child := match.ViaChild
		for _, m := range bd.Matches {
			if m.ID == child {
				child = m.Budget.Name()
			}
		}
		switch denied.Decision.Reason {
		case "budget_exhausted":
			base.Message = fmt.Sprintf("a budget above %q is exhausted", child)
		default:
			base.Message = fmt.Sprintf("a budget above %q cannot account for this request", child)
		}
		return base
	}
	name := fmt.Sprintf("%q (%s)", match.Budget.Name(), match.ID)
	switch denied.Decision.Reason {
	case "budget_exhausted":
		base.Message = fmt.Sprintf("budget %s is exhausted", name)
		if remaining, ok := h.budgetRemaining(ctx, tenant, tenantID, match.Budget, bd); ok {
			base.Message += "; remaining " + remaining
		}
		if denied.Decision.RetryAfter > 0 {
			base.Message += fmt.Sprintf("; the window resets in %s", denied.Decision.RetryAfter.Round(time.Second))
		}
	case "unpriced_meter", "budget_cost_unknown":
		base.Message = fmt.Sprintf("budget %s is a cost budget and this model has no price, so the request cannot be costed", name)
	case "unsupported_meter":
		base.Message = fmt.Sprintf("budget %s counts %s, which this operation cannot meter", name, match.Budget.Limit.Unit)
	}
	return base
}

func (h *QuotaHost) budgetRemaining(ctx context.Context, tenant quotaTenant, tenantID string, b budgets.Budget, bd budgetDraws) (string, bool) {
	for _, d := range bd.Draws {
		if d.ID != b.ID {
			continue
		}
		bal, err := tenant.engine.DrawBalance(ctx, tenantID, d, h.clock().UTC())
		if err != nil {
			return "", false
		}
		return budgetAmountText(b, bal.Available, d.Scale), true
	}
	return "", false
}

func budgetAmountText(b budgets.Budget, v int64, scale uint8) string {
	if v < 0 {
		v = 0
	}
	if b.Limit.Unit == budgets.UnitCost {
		return budgets.FormatAmount(v, scale) + " " + b.Limit.Currency
	}
	return budgets.FormatAmount(v, 0) + " " + b.Limit.Unit
}

// errBudgetInvalid marks saved budgets that do not validate; the cache never
// serves a previous tree past one.
var errBudgetInvalid = errors.New("invalid")
