package usageledger

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"time"
)

// Back-estimation prices records written before a price existed.
//
// A record whose cost is unknown and whose usage is final and fully known gets
// the cost a price would have given it, using the price effective at the
// record's own started_at. The write is append-only in spirit: the cost becomes
// "estimated" with backfilled_at (which marks that it was unknown when
// written) and the price id used; a settled or estimated cost is never touched,
// and running it twice changes nothing.

const (
	// DefaultBackfillBatch and MaxBackfillBatch bound the records one call scans.
	DefaultBackfillBatch = 500
	MaxBackfillBatch     = 1000
)

// CostBackfiller is implemented by stores that can fill in a record's cost
// after the fact. BackfillCost replaces the cost of tenant's record id only
// while its basis is still unknown, and reports whether it did. It returns
// ErrNotFound when the record is gone.
type CostBackfiller interface {
	BackfillCost(ctx context.Context, tenant, id string, cost Cost) (bool, error)
}

// ErrBackfillUnsupported is returned for a store that cannot backfill.
var ErrBackfillUnsupported = errors.New("usage ledger store cannot backfill costs")

// BackfillQuery selects a window of records and bounds one call.
type BackfillQuery struct {
	Filter Filter
	// Limit is the most records scanned, up to MaxBackfillBatch.
	Limit  int
	Cursor string
	DryRun bool
}

// BackfillReasons count why scanned records were left alone.
type BackfillReasons struct {
	// UsageNotFinal: the record has no usage, or a usage_bound says its
	// counters are not final.
	UsageNotFinal int64 `json:"usage_not_final,omitempty"`
	// NoPrice: no single price matched the record at its own time, or the
	// price could not cost it (an unknown quantity or an unpriced one).
	NoPrice int64 `json:"no_price,omitempty"`
	// Changed: another writer gave the record a cost first.
	Changed int64 `json:"changed,omitempty"`
}

// BackfillResult reports one call. Updated is 0 on a dry run, where Eligible
// counts what would be written. Totals sum the eligible records' new costs per
// currency (exact fractions summed before one rounding).
type BackfillResult struct {
	Scanned    int64           `json:"scanned"`
	Eligible   int64           `json:"eligible"`
	Updated    int64           `json:"updated"`
	Skipped    BackfillReasons `json:"skipped"`
	Totals     []CostTotal     `json:"totals"`
	NextCursor string          `json:"next_cursor,omitempty"`
	DryRun     bool            `json:"dry_run"`
}

// Backfillable reports whether a record's cost may be computed after the fact:
// its cost is unknown and its counters are final.
func Backfillable(r Record) bool {
	return r.Cost.Basis == BasisUnknown && r.Usage.Known() && r.UsageBound == ""
}

// Backfill scans one batch of the tenant's unknown-cost records newest first
// and prices those a price covers. Call it again with NextCursor until it is
// empty. The store must implement CostBackfiller unless DryRun is set.
func Backfill(ctx context.Context, store Store, tenant string, prices []Price, q BackfillQuery, now time.Time) (BackfillResult, error) {
	result := BackfillResult{DryRun: q.DryRun, Totals: []CostTotal{}}
	limit := q.Limit
	switch {
	case limit == 0:
		limit = DefaultBackfillBatch
	case limit < 0 || limit > MaxBackfillBatch:
		return result, invalid("limit must be between 1 and %d", MaxBackfillBatch)
	}
	var writer CostBackfiller
	if !q.DryRun {
		var ok bool
		if writer, ok = store.(CostBackfiller); !ok {
			return result, ErrBackfillUnsupported
		}
	}
	filter := q.Filter
	filter.CostBasis = BasisUnknown
	page, err := store.List(ctx, tenant, Query{Filter: filter, Limit: limit, Cursor: q.Cursor})
	if err != nil {
		return result, err
	}
	totals := map[string]*costAccumulator{}
	for _, r := range page.Records {
		result.Scanned++
		if !Backfillable(r) {
			result.Skipped.UsageNotFinal++
			continue
		}
		cost := Estimate(prices, r)
		if cost.Basis != BasisEstimated {
			result.Skipped.NoPrice++
			continue
		}
		stamp := now.UTC()
		cost.BackfilledAt = &stamp
		result.Eligible++
		if !q.DryRun {
			updated, err := writer.BackfillCost(ctx, tenant, r.ID, cost)
			switch {
			case errors.Is(err, ErrNotFound):
				result.Eligible--
				continue
			case err != nil:
				return result, err
			case !updated:
				result.Eligible--
				result.Skipped.Changed++
				continue
			}
			result.Updated++
		}
		amount, _ := cost.AmountInt()
		value := new(big.Rat).SetInt(amount)
		if cost.Exact != "" {
			if exact, ok := new(big.Rat).SetString(cost.Exact); ok {
				value = exact
			}
		}
		acc := totals[cost.Currency]
		if acc == nil {
			acc = &costAccumulator{sum: new(big.Rat), scale: cost.Scale}
			totals[cost.Currency] = acc
		}
		switch {
		case cost.Scale > acc.scale:
			acc.sum.Mul(acc.sum, new(big.Rat).SetInt(pow10(cost.Scale-acc.scale)))
			acc.scale = cost.Scale
		case cost.Scale < acc.scale:
			value = new(big.Rat).Mul(value, new(big.Rat).SetInt(pow10(acc.scale-cost.Scale)))
		}
		acc.sum.Add(acc.sum, value)
		acc.records++
	}
	for currency, acc := range totals {
		total := CostTotal{Currency: currency, Basis: BasisEstimated, Amount: ceilRat(acc.sum).String(), Scale: acc.scale, Records: acc.records}
		if !acc.sum.IsInt() {
			total.Exact = acc.sum.String()
		}
		result.Totals = append(result.Totals, total)
	}
	sort.Slice(result.Totals, func(i, j int) bool { return result.Totals[i].Currency < result.Totals[j].Currency })
	result.NextCursor = page.NextCursor
	return result, nil
}

// SumCostTotals adds cost totals of one basis, per currency, exactly: totals
// that carry an Exact fraction add that, others their rounded amount, and the
// sum is rounded up once. Use it to combine the pages of a back-estimation.
func SumCostTotals(parts ...[]CostTotal) []CostTotal {
	sums := map[string]*costAccumulator{}
	basis := map[string]string{}
	for _, part := range parts {
		for _, t := range part {
			amount, ok := new(big.Int).SetString(t.Amount, 10)
			if !ok {
				continue
			}
			value := new(big.Rat).SetInt(amount)
			if t.Exact != "" {
				if exact, ok := new(big.Rat).SetString(t.Exact); ok {
					value = exact
				}
			}
			acc := sums[t.Currency]
			if acc == nil {
				acc = &costAccumulator{sum: new(big.Rat), scale: t.Scale}
				sums[t.Currency] = acc
				basis[t.Currency] = t.Basis
			}
			switch {
			case t.Scale > acc.scale:
				acc.sum.Mul(acc.sum, new(big.Rat).SetInt(pow10(t.Scale-acc.scale)))
				acc.scale = t.Scale
			case t.Scale < acc.scale:
				value = new(big.Rat).Mul(value, new(big.Rat).SetInt(pow10(acc.scale-t.Scale)))
			}
			acc.sum.Add(acc.sum, value)
			acc.records += t.Records
		}
	}
	out := []CostTotal{}
	for currency, acc := range sums {
		total := CostTotal{Currency: currency, Basis: basis[currency], Amount: ceilRat(acc.sum).String(), Scale: acc.scale, Records: acc.records}
		if !acc.sum.IsInt() {
			total.Exact = acc.sum.String()
		}
		out = append(out, total)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Currency < out[j].Currency })
	return out
}
