package usageledger

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GroupByFields are the record fields a summary may group by.
var GroupByFields = []string{"plane", "provider", "model_id", "tool_name", "actor_id", "principal_id", "client_id", "access_point_id", "caller_kind", "budget", "status", "source", "error_class", "usage_bound", "batch", "hook_decision", "harness"}

const (
	BucketHour = "hour"
	BucketDay  = "day"
	// MaxSummaryRecords bounds the records one summary scans.
	MaxSummaryRecords = 200_000
	// MaxSummaryGroups bounds the groups one summary returns.
	MaxSummaryGroups = 10_000
)

// SummaryQuery groups the records selected by Filter.
type SummaryQuery struct {
	Filter  Filter
	GroupBy []string
	Bucket  string
	// MergeSources adds managed and reported records into the same groups.
	// Without it, a query with no source filter keeps them in separate groups
	// (see Summarize).
	MergeSources bool
	// Feedback, when set, adds the latest feedback totals to every group.
	Feedback FeedbackStore
}

// Summary aggregates records. Truncated means the scan or group bound was
// reached and the totals cover only the newest records scanned.
type Summary struct {
	GroupBy        []string       `json:"group_by"`
	Bucket         string         `json:"bucket,omitempty"`
	Groups         []SummaryGroup `json:"groups"`
	RecordsScanned int64          `json:"records_scanned"`
	Truncated      bool           `json:"truncated"`
	// SourceSplit is true when managed and reported records are kept in
	// separate groups (the "source" key was added to group_by).
	SourceSplit bool `json:"source_split,omitempty"`
	// ExcludedCorrelatedReported counts reported records left out because
	// they share a correlation key with a managed record.
	ExcludedCorrelatedReported int64 `json:"excluded_correlated_reported,omitempty"`
	// Notes explain how sources were treated, in plain words.
	Notes []string `json:"notes,omitempty"`
}

// SummaryGroup totals one group. Token sums only include records where that
// counter was reported; TokensUnknown counts records where it was not, and
// UnknownUsageRecords counts records with no usage at all. Cost holds exact
// per-currency, per-basis totals; UnknownCostRecords counts unpriced records.
type SummaryGroup struct {
	Key map[string]string `json:"key"`
	// Labels holds display labels for identity keys (principal_id,
	// actor_id), taken from the newest record of the group that carried one.
	Labels              map[string]string `json:"labels,omitempty"`
	BucketStart         *time.Time        `json:"bucket_start,omitempty"`
	Records             int64             `json:"records"`
	Requests            int64             `json:"requests"`
	Errors              int64             `json:"errors"`
	Denied              int64             `json:"denied"`
	UnknownStatus       int64             `json:"unknown_status"`
	Tokens              map[string]int64  `json:"tokens"`
	TokensUnknown       map[string]int64  `json:"tokens_unknown"`
	UnknownUsageRecords int64             `json:"unknown_usage_records"`
	// BoundedUsageRecords counts records whose usage_bound is set; their
	// counters are included in Tokens but are not final. The per-bound counts
	// sum to it.
	BoundedUsageRecords   int64       `json:"bounded_usage_records,omitempty"`
	LowerBoundRecords     int64       `json:"lower_bound_records,omitempty"`
	AmbiguousUsageRecords int64       `json:"ambiguous_usage_records,omitempty"`
	PartialUsageRecords   int64       `json:"partial_usage_records,omitempty"`
	Cost                  []CostTotal `json:"cost"`
	UnknownCostRecords    int64       `json:"unknown_cost_records"`
	// ModalityTokens sums per-modality token splits (subsets of the token
	// totals, never added to them) over records that reported them.
	ModalityTokens map[string]int64 `json:"modality_tokens,omitempty"`
	// Units sums non-token units (milliseconds, image counts); UnitRecords
	// counts the records that reported each. Records that did not report a
	// unit are not counted as zero.
	Units       map[string]int64 `json:"units,omitempty"`
	UnitRecords map[string]int64 `json:"unit_records,omitempty"`
	// BatchRecords counts records of provider batch exchanges.
	BatchRecords int64 `json:"batch_records,omitempty"`
	// Feedback totals the latest feedback of the group's records, when the
	// summary asked for it.
	Feedback *FeedbackTotals `json:"feedback,omitempty"`
}

// CostTotal is an exact sum of fixed-point amounts in one currency and basis.
// Amounts of different scales are rescaled to the largest scale without loss.
type CostTotal struct {
	Currency string `json:"currency"`
	Basis    string `json:"basis"`
	Amount   string `json:"amount"`
	Scale    uint8  `json:"scale"`
	Records  int64  `json:"records"`
	// Exact is the unrounded total in minor units as a fraction, set by
	// back-estimation results when Amount was rounded up, so totals of several
	// pages can be added before rounding (see SumCostTotals).
	Exact string `json:"exact,omitempty"`
}

// MarshalJSON adds amount_decimal, the exact decimal form of amount at scale
// (derived; amount and scale stay authoritative).
func (c CostTotal) MarshalJSON() ([]byte, error) {
	type plain CostTotal
	decimal, _ := DecimalString(c.Amount, c.Scale)
	return json.Marshal(struct {
		plain
		AmountDecimal string `json:"amount_decimal,omitempty"`
	}{plain(c), decimal})
}

// ParseGroupBy validates a comma-separated group_by parameter.
func ParseGroupBy(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	allowed := map[string]bool{}
	for _, f := range GroupByFields {
		allowed[f] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		field := strings.TrimSpace(part)
		if !allowed[field] && !(strings.HasPrefix(field, TagGroupPrefix) && ValidTagKey(strings.TrimPrefix(field, TagGroupPrefix))) {
			return nil, invalid("group_by field %q is not one of %s or tag:<key>", field, strings.Join(GroupByFields, ","))
		}
		if seen[field] {
			return nil, invalid("group_by field %q repeated", field)
		}
		seen[field] = true
		out = append(out, field)
	}
	return out, nil
}

// ValidateBucket accepts "", "hour" or "day".
func ValidateBucket(bucket string) error {
	switch bucket {
	case "", BucketHour, BucketDay:
		return nil
	}
	return invalid("bucket must be hour or day")
}

func groupValue(r Record, field string) string {
	switch field {
	case "budget":
		// A record that drew from several budgets is counted once in each;
		// Aggregator.Add fans out. A record with none groups under "".
		if len(r.BudgetIDs) == 1 {
			return r.BudgetIDs[0]
		}
		return ""
	case "plane":
		return r.Plane
	case "provider":
		return r.Provider
	case "model_id":
		return r.ModelID
	case "tool_name":
		return r.ToolName
	case "actor_id":
		return r.ActorID
	case "principal_id":
		return r.PrincipalID
	case "client_id":
		return r.ClientID
	case "access_point_id":
		return r.AccessPointID
	case "caller_kind":
		return r.CallerKind
	case "status":
		return r.Status
	case "source":
		return r.Source
	case "error_class":
		return r.ErrorClass
	case "usage_bound":
		return r.UsageBound
	case "batch":
		return strconv.FormatBool(r.Batch)
	case "hook_decision":
		return hookDecision(r)
	case "harness":
		return r.Harness
	}
	if strings.HasPrefix(field, TagGroupPrefix) {
		return r.Tags[strings.TrimPrefix(field, TagGroupPrefix)]
	}
	return ""
}

type costAccumulator struct {
	// sum is exact, in minor units at scale: records that carry an Exact
	// fraction add it, so rounding happens once, on the total.
	sum     *big.Rat
	scale   uint8
	records int64
}

type groupAccumulator struct {
	group    SummaryGroup
	costs    map[[2]string]*costAccumulator
	feedback feedbackAccumulator
}

func pow10(n uint8) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }

func (g *groupAccumulator) add(r Record) error {
	s := &g.group
	s.Records++
	if s.Requests > math.MaxInt64-r.Requests {
		return fmt.Errorf("request total overflows")
	}
	s.Requests += r.Requests
	switch r.Status {
	case StatusError:
		s.Errors++
	case StatusDenied:
		s.Denied++
	case StatusUnknown:
		s.UnknownStatus++
	}
	if !r.Usage.Known() {
		s.UnknownUsageRecords++
	}
	switch r.UsageBound {
	case UsageLowerBound:
		s.LowerBoundRecords++
		s.BoundedUsageRecords++
	case UsageAmbiguous:
		s.AmbiguousUsageRecords++
		s.BoundedUsageRecords++
	case UsagePartial:
		s.PartialUsageRecords++
		s.BoundedUsageRecords++
	}
	for _, name := range TokenCounters {
		v := r.Usage.Counter(name)
		if v == nil {
			s.TokensUnknown[name]++
			continue
		}
		if s.Tokens[name] > math.MaxInt64-*v {
			return fmt.Errorf("token total overflows")
		}
		s.Tokens[name] += *v
	}
	if err := g.addUnits(r); err != nil {
		return err
	}
	if r.Cost.Basis != BasisSettled && r.Cost.Basis != BasisEstimated && r.Cost.Basis != BasisReported {
		s.UnknownCostRecords++
		return nil
	}
	amount, ok := r.Cost.AmountInt()
	if !ok {
		s.UnknownCostRecords++
		return nil
	}
	key := [2]string{r.Cost.Currency, r.Cost.Basis}
	c := g.costs[key]
	if c == nil {
		c = &costAccumulator{sum: new(big.Rat), scale: r.Cost.Scale}
		g.costs[key] = c
	}
	value := new(big.Rat).SetInt(amount)
	if r.Cost.Exact != "" {
		if exact, ok := new(big.Rat).SetString(r.Cost.Exact); ok {
			value = exact
		}
	}
	switch {
	case r.Cost.Scale > c.scale:
		c.sum.Mul(c.sum, new(big.Rat).SetInt(pow10(r.Cost.Scale-c.scale)))
		c.scale = r.Cost.Scale
	case r.Cost.Scale < c.scale:
		value = new(big.Rat).Mul(value, new(big.Rat).SetInt(pow10(c.scale-r.Cost.Scale)))
	}
	c.sum.Add(c.sum, value)
	c.records++
	return nil
}

func bucketStart(t time.Time, bucket string) time.Time {
	t = t.UTC()
	switch bucket {
	case BucketHour:
		return t.Truncate(time.Hour)
	case BucketDay:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	return time.Time{}
}

// Aggregator summarizes records that already passed the filter.
type Aggregator struct {
	query     SummaryQuery
	groups    map[string]*groupAccumulator
	scanned   int64
	truncated bool
	// trackFeedback adds feedback totals to every group (AddWithFeedback).
	trackFeedback bool
}

func NewAggregator(q SummaryQuery) *Aggregator {
	return &Aggregator{query: q, groups: map[string]*groupAccumulator{}}
}

// Add folds one record into its group. Grouping by budget counts a record in
// the group of every budget it drew from, so budget groups overlap by design
// (a child's spend is also its ancestors' spend) and do not sum to the total.
func (a *Aggregator) Add(r Record) error { return a.AddWithFeedback(r, nil) }

// AddWithFeedback folds one record and its latest feedback (nil: none) into
// its group. Feedback totals are kept only when the summary asked for them.
func (a *Aggregator) AddWithFeedback(r Record, fb *Feedback) error {
	a.scanned++
	for _, f := range a.query.GroupBy {
		if f == "budget" && len(r.BudgetIDs) > 1 {
			for _, id := range r.BudgetIDs {
				one := r
				one.BudgetIDs = []string{id}
				if err := a.fold(one, fb); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return a.fold(r, fb)
}

func (a *Aggregator) fold(r Record, fb *Feedback) error {
	key := make([]string, 0, len(a.query.GroupBy)+1)
	values := map[string]string{}
	for _, field := range a.query.GroupBy {
		v := groupValue(r, field)
		values[field] = v
		key = append(key, field+"="+v)
	}
	var start *time.Time
	if a.query.Bucket != "" {
		b := bucketStart(r.StartedAt, a.query.Bucket)
		start = &b
		key = append(key, "@"+b.Format(time.RFC3339))
	}
	joined := strings.Join(key, "\x00")
	g := a.groups[joined]
	if g == nil {
		if len(a.groups) >= MaxSummaryGroups {
			a.truncated = true
			return nil
		}
		g = &groupAccumulator{group: SummaryGroup{Key: values, BucketStart: start, Tokens: map[string]int64{}, TokensUnknown: map[string]int64{}, Cost: []CostTotal{}}, costs: map[[2]string]*costAccumulator{}}
		a.groups[joined] = g
	}
	for field, label := range map[string]string{"principal_id": r.PrincipalDisplay, "actor_id": r.ActorDisplay} {
		if _, grouped := values[field]; grouped && label != "" {
			if g.group.Labels == nil {
				g.group.Labels = map[string]string{}
			}
			if _, set := g.group.Labels[field]; !set {
				g.group.Labels[field] = label
			}
		}
	}
	if a.trackFeedback {
		g.feedback.add(fb)
	}
	return g.add(r)
}

// Result returns groups ordered by bucket then key values.
func (a *Aggregator) Result() Summary {
	out := Summary{GroupBy: append([]string{}, a.query.GroupBy...), Bucket: a.query.Bucket, Groups: []SummaryGroup{}, RecordsScanned: a.scanned, Truncated: a.truncated}
	for _, g := range a.groups {
		group := g.group
		if a.trackFeedback {
			group.Feedback = g.feedback.result()
		}
		for key, c := range g.costs {
			group.Cost = append(group.Cost, CostTotal{Currency: key[0], Basis: key[1], Amount: ceilRat(c.sum).String(), Scale: c.scale, Records: c.records})
		}
		sort.Slice(group.Cost, func(i, j int) bool {
			if group.Cost[i].Currency == group.Cost[j].Currency {
				return group.Cost[i].Basis < group.Cost[j].Basis
			}
			return group.Cost[i].Currency < group.Cost[j].Currency
		})
		out.Groups = append(out.Groups, group)
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		x, y := out.Groups[i], out.Groups[j]
		if x.BucketStart != nil && y.BucketStart != nil && !x.BucketStart.Equal(*y.BucketStart) {
			return x.BucketStart.Before(*y.BucketStart)
		}
		for _, field := range a.query.GroupBy {
			if x.Key[field] != y.Key[field] {
				return x.Key[field] < y.Key[field]
			}
		}
		return false
	})
	return out
}

// Summarize pages through the store newest first and aggregates up to
// MaxSummaryRecords records.
//
// Managed records (access point exchanges) and reported records (harness
// telemetry) can describe the same request, so they are never added together
// silently:
//
//   - With no source filter, the "source" key is added to the grouping so the
//     two are separate groups, and reported records that share a correlation
//     key with a managed record (see CorrelationKeys) are left out and counted
//     in ExcludedCorrelatedReported.
//   - With source=reported the query asked for them; every reported record is
//     included, correlated or not.
//   - With MergeSources the groups are combined, still leaving out the
//     correlated reported records. Reported records with no key in common with
//     a managed record may still describe requests that are also counted as
//     managed; the notes say so.
func Summarize(ctx context.Context, store Store, tenant string, q SummaryQuery) (Summary, error) {
	if err := ValidateBucket(q.Bucket); err != nil {
		return Summary{}, err
	}
	if q.GroupBy == nil {
		q.GroupBy = []string{}
	}
	if _, err := ParseGroupBy(strings.Join(q.GroupBy, ",")); err != nil {
		return Summary{}, err
	}
	budgetManaged := false
	for _, f := range q.GroupBy {
		if f == "budget" && q.Filter.Source == "" {
			// Budgets are drawn only by managed exchanges: reported records
			// (for example OTLP-ingested) never drew from one, so a summary by
			// budget counts managed records only.
			q.Filter.Source, budgetManaged = SourceManaged, true
		}
	}
	source := q.Filter.Source
	split := source == "" && !q.MergeSources
	if split {
		hasSource := false
		for _, f := range q.GroupBy {
			hasSource = hasSource || f == "source"
		}
		if !hasSource {
			q.GroupBy = append(append([]string{}, q.GroupBy...), "source")
		}
	}
	var managed *ManagedKeys
	keysTruncated := false
	if source == "" {
		var err error
		if managed, keysTruncated, err = managedKeys(ctx, store, tenant, q.Filter); err != nil {
			return Summary{}, err
		}
	}
	agg := NewAggregator(q)
	agg.trackFeedback = q.Feedback != nil
	var excluded int64
	sawReported := false
	finish := func() Summary {
		out := agg.Result()
		out.SourceSplit = split
		out.ExcludedCorrelatedReported = excluded
		out.Notes = summaryNotes(source, split, q.MergeSources, sawReported || excluded > 0, keysTruncated)
		if budgetManaged {
			out.Notes = append(out.Notes, "Grouped by budget: only managed records are counted, because reported records never draw from a budget.")
		}
		return out
	}
	cursor := ""
	for {
		page, err := store.List(ctx, tenant, Query{Filter: q.Filter, Limit: MaxLimit, Cursor: cursor})
		if err != nil {
			return Summary{}, err
		}
		kept := make([]Record, 0, len(page.Records))
		for _, r := range page.Records {
			if source == "" && managed.Correlates(r) {
				excluded++
				continue
			}
			if r.Source == SourceReported {
				sawReported = true
			}
			kept = append(kept, r)
		}
		var latest map[string]Feedback
		if q.Feedback != nil && len(kept) > 0 {
			ids := make([]string, len(kept))
			for i, r := range kept {
				ids[i] = r.ID
			}
			if latest, err = q.Feedback.Latest(ctx, tenant, ids); err != nil {
				return Summary{}, err
			}
		}
		for _, r := range kept {
			if agg.scanned >= MaxSummaryRecords {
				agg.truncated = true
				return finish(), nil
			}
			var fb *Feedback
			if f, ok := latest[r.ID]; ok {
				fb = &f
			}
			if err := agg.AddWithFeedback(r, fb); err != nil {
				return Summary{}, err
			}
		}
		if page.NextCursor == "" {
			return finish(), nil
		}
		cursor = page.NextCursor
	}
}

// ceilRat rounds a nonnegative rational up to an integer.
func ceilRat(r *big.Rat) *big.Int {
	whole, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() > 0 {
		whole.Add(whole, big.NewInt(1))
	}
	return whole
}

// correlationWindow widens the managed-key scan beyond the summary window so
// a managed record that started just before a reported one is still seen.
const correlationWindow = time.Hour

// managedKeys collects the correlation keys of managed records in the
// summary's window and visibility.
func managedKeys(ctx context.Context, store Store, tenant string, f Filter) (*ManagedKeys, bool, error) {
	keys := NewManagedKeys()
	scan := Filter{Source: SourceManaged, Visibility: f.Visibility}
	if !f.Since.IsZero() {
		scan.Since = f.Since.Add(-correlationWindow)
	}
	if !f.Until.IsZero() {
		scan.Until = f.Until.Add(correlationWindow)
	}
	cursor, scanned := "", 0
	for {
		page, err := store.List(ctx, tenant, Query{Filter: scan, Limit: MaxLimit, Cursor: cursor})
		if err != nil {
			return nil, false, err
		}
		for _, r := range page.Records {
			if scanned++; scanned > MaxSummaryRecords {
				return keys, true, nil
			}
			keys.AddManaged(r)
		}
		if page.NextCursor == "" {
			return keys, false, nil
		}
		cursor = page.NextCursor
	}
}

func summaryNotes(source string, split, merged, reported, keysTruncated bool) []string {
	var notes []string
	switch {
	case source == SourceReported:
		notes = append(notes, "Reported records are included whether or not a managed record describes the same request; do not add these totals to managed totals.")
	case split && reported:
		notes = append(notes, "Managed and reported records are separate groups and are not added together. Reported records that share a trace, client request id or request id with a managed record are left out.")
	case merged && reported:
		notes = append(notes, "Managed and reported records are combined. Reported records that share no correlation key with a managed record may describe requests that are also counted as managed.")
	}
	if keysTruncated && source == "" {
		notes = append(notes, "The managed records scanned for correlation were truncated, so some correlated reported records may be included.")
	}
	return notes
}
