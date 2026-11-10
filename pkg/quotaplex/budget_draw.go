package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

// Budget units a BudgetDraw may use.
const (
	DrawRequests = "requests"
	DrawTokens   = "tokens"
	DrawCost     = "cost"
)

// On-unknown-cost behaviours of a cost draw.
const (
	UnknownCostDeny         = "deny"
	UnknownCostAllowAndFlag = "allow_and_flag"
)

const (
	maxDrawsPerDemand   = 16
	maxDrawThresholds   = 8
	maxCrossedThreshold = 8
)

// BudgetDraw is one tenant budget a request draws from. The host builds draws
// from verified facts (token groups, principal, actor, registered client,
// access point) and trusted configuration; a caller cannot supply or remove
// one. Draws travel in the Demand, so they are part of the attempt's replay
// identity, and they are resolved by the same transaction as every policy
// control: one admission decision.
//
// Limit is an integer quantity of the unit: requests, the sum of the token
// meters the binding maps, or, for cost, minor units at Scale of the policy's
// money meters in Currency. The counter is keyed by the budget id, the
// draw's shape (unit, currency, scale, period) and the window, so editing the
// amount keeps the counter and editing the unit or window starts a new one.
type BudgetDraw struct {
	ID            string `json:"id"`
	Unit          string `json:"unit"`
	Currency      string `json:"currency,omitempty"`
	Scale         uint8  `json:"scale,omitempty"`
	Limit         int64  `json:"limit"`
	Period        Period `json:"period"`
	OnUnknownCost string `json:"on_unknown_cost,omitempty"`
	Thresholds    []int  `json:"thresholds,omitempty"`
}

// ThresholdCross records that consumption reached Percent of the limit.
type ThresholdCross struct {
	Percent  int       `json:"percent"`
	At       time.Time `json:"at"`
	Consumed int64     `json:"consumed"`
}

// DrawShape is the digest of the draw's structural fields.
func DrawShape(d BudgetDraw) string {
	return policyDigest(struct {
		Unit, Currency string
		Scale          uint8
		Period         Period
	}{d.Unit, d.Currency, d.Scale, d.Period})
}

func (d BudgetDraw) lineage(policyID string) string {
	return policyLineage(policyID, "tenant-budget", d.ID+"\x00"+DrawShape(d))
}

// DrawKey is the counter key of a draw in a resolved window under a policy.
func DrawKey(policyID string, d BudgetDraw, w Window) string {
	return policyKey(d.lineage(policyID), "budget", w.Start.UTC().Format(time.RFC3339Nano), w.End.UTC().Format(time.RFC3339Nano))
}

// ValidateDraw checks one draw's shape without a policy.
func ValidateDraw(d BudgetDraw) error {
	if policyText(d.ID, false) != nil || d.Limit < 0 {
		return fmt.Errorf("budget draw needs a bounded id and a nonnegative limit")
	}
	switch d.Unit {
	case DrawRequests, DrawTokens:
		if d.Currency != "" || d.Scale != 0 || d.OnUnknownCost != "" {
			return fmt.Errorf("currency and on_unknown_cost apply only to cost draws")
		}
	case DrawCost:
		if !validDrawCurrency(d.Currency) || d.Scale > 18 {
			return fmt.Errorf("cost draw needs a three-letter currency and a scale up to 18")
		}
		switch d.OnUnknownCost {
		case "", UnknownCostDeny, UnknownCostAllowAndFlag:
		default:
			return fmt.Errorf("on_unknown_cost must be deny or allow_and_flag")
		}
	default:
		return fmt.Errorf("budget draw unit must be requests, tokens or cost")
	}
	if len(d.Thresholds) > maxDrawThresholds {
		return fmt.Errorf("at most %d thresholds", maxDrawThresholds)
	}
	for _, t := range d.Thresholds {
		if t < 1 || t > 100 {
			return fmt.Errorf("thresholds are percentages from 1 to 100")
		}
	}
	return d.Period.Validate()
}

func validDrawCurrency(c string) bool {
	if len(c) != 3 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// drawsPriceTolerance reports whether every cost draw tolerates an unpriced
// request (allow_and_flag) and there is at least one; otherwise pricing
// failures deny. firstDeny is the first deny-mode cost draw, if any.
func drawsPriceTolerance(draws []BudgetDraw) (tolerant bool, firstDeny string) {
	cost := 0
	for _, d := range draws {
		if d.Unit != DrawCost {
			continue
		}
		cost++
		if d.OnUnknownCost != UnknownCostAllowAndFlag && firstDeny == "" {
			firstDeny = d.ID
		}
	}
	return cost > 0 && firstDeny == "", firstDeny
}

func validateDraws(draws []BudgetDraw) error {
	if len(draws) > maxDrawsPerDemand {
		return fmt.Errorf("a demand draws from at most %d budgets", maxDrawsPerDemand)
	}
	seen := map[string]bool{}
	for _, d := range draws {
		if seen[d.ID] {
			return fmt.Errorf("duplicate budget draw")
		}
		seen[d.ID] = true
		if err := ValidateDraw(d); err != nil {
			return err
		}
	}
	return nil
}

func budgetMeters(l BudgetLimit) []string {
	if l.UnknownCost {
		return nil
	}
	if len(l.MeterIDs) > 0 {
		return l.MeterIDs
	}
	if l.MeterID == "" {
		return nil
	}
	return []string{l.MeterID}
}

// crossThresholds appends thresholds consumption has newly reached. The list
// is bounded by the (bounded) threshold set of the draw.
func crossThresholds(state budgetRecord, limit BudgetLimit, now time.Time) []ThresholdCross {
	out := state.Crossed
	if len(limit.Thresholds) == 0 || limit.Limit <= 0 {
		return out
	}
	have := map[int]bool{}
	for _, c := range out {
		have[c.Percent] = true
	}
	for _, t := range limit.Thresholds {
		if have[t] || len(out) >= maxCrossedThreshold {
			continue
		}
		lhs := new(big.Int).Mul(big.NewInt(state.Consumed), big.NewInt(100))
		rhs := new(big.Int).Mul(big.NewInt(limit.Limit), big.NewInt(int64(t)))
		if lhs.Cmp(rhs) >= 0 {
			out = append(out, ThresholdCross{Percent: t, At: now.UTC(), Consumed: state.Consumed})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Percent < out[j].Percent })
	return out
}

// drawMeters selects the policy meters a draw sums, among those the demand
// carries (raw bounds, plus priced money meters).
func drawMeters(d BudgetDraw, meters map[string]Meter, bounds Vector) []string {
	var ids []string
	for id := range bounds {
		m, ok := meters[id]
		if !ok {
			continue
		}
		switch d.Unit {
		case DrawRequests:
			if m.Unit == "requests" {
				ids = append(ids, id)
			}
		case DrawTokens:
			if m.Unit == "tokens" {
				ids = append(ids, id)
			}
		case DrawCost:
			if m.Unit == "money" && m.Currency == d.Currency && m.Scale == d.Scale {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// drawCostPriced reports whether the selected price book prices every billable
// bounded meter into the money meter, so the draw's cost is computable.
func drawCostPriced(selected *PriceBook, money string, bounds Vector, meters map[string]Meter) bool {
	if selected == nil {
		return false
	}
	covered := map[string]bool{}
	for _, price := range selected.Prices {
		if price.MoneyMeterID == money {
			covered[price.MeterID] = true
		}
	}
	if len(covered) == 0 {
		return false
	}
	for raw := range bounds {
		if meters[raw].Billable && !covered[raw] {
			return false
		}
	}
	return true
}

func drawDenied(code, message, budget string) error {
	return &ResolveError{Code: code, Message: message, BudgetID: budget}
}

// resolveDraws turns the demand's budget draws into budget limits.
func (c *CompiledPolicy) resolveDraws(demand Demand, plan *Plan, selected *PriceBook, now time.Time) error {
	policy := c.policy
	for _, d := range demand.Budgets {
		window, err := d.Period.Resolve(now)
		if err != nil {
			return drawDenied("period_inactive", err.Error(), d.ID)
		}
		lineage := d.lineage(policy.ID)
		limit := BudgetLimit{Key: DrawKey(policy.ID, d, window), LineageKey: lineage, Shape: DrawShape(d), RuleID: "budget:" + d.ID, Window: window, Limit: d.Limit, BudgetID: d.ID, Thresholds: append([]int(nil), d.Thresholds...)}
		ids := drawMeters(d, policy.Meters, plan.Bounds)
		if d.Unit == DrawCost {
			known := false
			for _, m := range policy.Meters {
				if m.Unit == "money" && m.Currency == d.Currency && m.Scale == d.Scale {
					known = true
				}
			}
			if !known {
				return drawDenied("unsupported_meter", "the quota policy has no money meter for this currency and scale", d.ID)
			}
			// Cost is computable only through a matching price book that
			// prices every billable bounded meter. Unknown is never zero.
			priced := len(ids) > 0
			for _, money := range ids {
				if !drawCostPriced(selected, money, demand.Bounds, policy.Meters) {
					priced = false
				}
			}
			if !priced {
				if d.OnUnknownCost == UnknownCostAllowAndFlag {
					limit.UnknownCost = true
					plan.Budgets = append(plan.Budgets, limit)
					continue
				}
				return drawDenied("budget_cost_unknown", "the request cannot be priced under a cost budget", d.ID)
			}
		}
		if len(ids) == 0 {
			return drawDenied("unsupported_meter", "the budget's unit has no bounded meter on this operation", d.ID)
		}
		var demandSum int64
		for _, id := range ids {
			if demandSum, err = checkedAdd(demandSum, plan.Bounds[id]); err != nil {
				return drawDenied("quantity_overflow", "budget exposure overflows", d.ID)
			}
		}
		if d.Unit == DrawRequests && demandSum == 0 {
			return drawDenied("unsupported_meter", "requests require a positive attempt bound", d.ID)
		}
		limit.MeterID, limit.MeterIDs, limit.Demand = ids[0], ids, demandSum
		plan.Budgets = append(plan.Budgets, limit)
	}
	return nil
}

// DrawBalance is the committed state of a draw's counter in the window that
// contains at. A counter that no request has touched reads as zero.
type DrawBalance struct {
	Key         string           `json:"key"`
	Window      Window           `json:"window"`
	Limit       int64            `json:"limit"`
	Held        int64            `json:"held"`
	Consumed    int64            `json:"consumed"`
	Available   int64            `json:"available"`
	UnknownCost int64            `json:"unknown_cost"`
	Crossed     []ThresholdCross `json:"crossed,omitempty"`
}

// DrawBalance reads one draw's counter without mutating anything.
func (e *Engine) DrawBalance(ctx context.Context, tenant string, d BudgetDraw, at time.Time) (DrawBalance, error) {
	if err := e.checkTenant(tenant); err != nil {
		return DrawBalance{}, err
	}
	if err := ValidateDraw(d); err != nil {
		return DrawBalance{}, err
	}
	window, err := d.Period.Resolve(at)
	if err != nil {
		return DrawBalance{}, err
	}
	key := DrawKey(e.policy.policy.ID, d, window)
	var record budgetRecord
	err = e.store.View(ctx, tenant, func(tx Tx) error {
		record = budgetRecord{}
		err := tx.Get(budgetKind, key, &record)
		if err != nil && !isNotFound(err) {
			return err
		}
		return nil
	})
	if err != nil {
		return DrawBalance{}, err
	}
	out := DrawBalance{Key: key, Window: window, Limit: d.Limit, Held: record.Held, Consumed: record.Consumed, UnknownCost: record.UnknownCost, Crossed: record.Crossed}
	available := new(big.Int).SetInt64(d.Limit)
	available.Sub(available, big.NewInt(record.Held))
	available.Sub(available, big.NewInt(record.Consumed))
	if available.IsInt64() {
		out.Available = available.Int64()
	}
	return out, nil
}

// PolicyID is the identity of the engine's compiled policy.
func (e *Engine) PolicyID() string { return e.policy.policy.ID }

// PolicyMeters returns a copy of the compiled policy's meters.
func (e *Engine) PolicyMeters() map[string]Meter { return policyCopy(e.policy.policy.Meters) }

func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
