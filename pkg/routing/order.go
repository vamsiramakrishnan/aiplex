package routing

import (
	"math/big"
	"sort"
)

// Price is a backend's comparable price under the lowest_cost weighting: the
// weighted cost of InputWeight million input tokens plus OutputWeight
// million output tokens, in Currency. Known is false when no price matched.
type Price struct {
	Known    bool
	Currency string
	Amount   *big.Rat
}

// Choice is one backend in attempt order and why it is there.
type Choice struct {
	Index  int
	Reason string
}

// Choice reasons (recorded on the exchange).
const (
	ReasonPriority       = "priority"
	ReasonWeightedPick   = "weighted_pick"
	ReasonWeightedRest   = "weighted_fallback"
	ReasonLowestCost     = "lowest_cost"
	ReasonPriceUnknown   = "price_unknown"
	ReasonPriceNotComp   = "price_other_currency"
	ReasonExcludedNoCost = "excluded_price_unknown"
)

// byPriority returns indexes ordered by priority, then list order.
func byPriority(backends []Backend, indexes []int) []int {
	out := append([]int(nil), indexes...)
	sort.SliceStable(out, func(i, j int) bool { return backends[out[i]].Priority < backends[out[j]].Priority })
	return out
}

// Order returns the attempt order of backends under the strategy, and the
// backends excluded from it (lowest_cost with require_price). random(n)
// returns a value in [0, n).
func Order(e Effective, backends []Backend, prices []Price, random func(int) int) (order []Choice, excluded []Choice) {
	all := make([]int, len(backends))
	for i := range all {
		all[i] = i
	}
	switch e.Strategy {
	case StrategyWeighted:
		total := 0
		for _, b := range backends {
			total += b.EffectiveWeight()
		}
		pick := -1
		if total > 0 {
			r := 0
			if random != nil {
				r = random(total)
			}
			for i, b := range backends {
				if r < b.EffectiveWeight() {
					pick = i
					break
				}
				r -= b.EffectiveWeight()
			}
		}
		if pick >= 0 {
			order = append(order, Choice{pick, ReasonWeightedPick})
		}
		for _, i := range byPriority(backends, all) {
			if i != pick {
				order = append(order, Choice{i, ReasonWeightedRest})
			}
		}
		return order, nil
	case StrategyLowestCost:
		var known, unknown []int
		currency := ""
		for _, i := range byPriority(backends, all) {
			if i < len(prices) && prices[i].Known && prices[i].Amount != nil {
				if currency == "" {
					currency = prices[i].Currency
				}
				if prices[i].Currency == currency {
					known = append(known, i)
					continue
				}
			}
			unknown = append(unknown, i)
		}
		// Stable on priority order: equal prices keep priority order.
		sort.SliceStable(known, func(a, b int) bool { return prices[known[a]].Amount.Cmp(prices[known[b]].Amount) < 0 })
		for _, i := range known {
			order = append(order, Choice{i, ReasonLowestCost})
		}
		for _, i := range unknown {
			reason := ReasonPriceUnknown
			if i < len(prices) && prices[i].Known {
				reason = ReasonPriceNotComp
			}
			if e.RequirePrice {
				excluded = append(excluded, Choice{i, ReasonExcludedNoCost})
				continue
			}
			order = append(order, Choice{i, reason})
		}
		return order, excluded
	}
	for _, i := range byPriority(backends, all) {
		order = append(order, Choice{i, ReasonPriority})
	}
	return order, nil
}
