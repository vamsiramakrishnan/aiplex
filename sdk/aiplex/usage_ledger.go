package aiplex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// The usage ledger records one entry per managed exchange on every plane.
// Unknown stays unknown: a nil Usage or nil counter is unreported (never
// zero), a cost with basis "unknown" has no amount, and a summary token total
// that is absent from SummaryGroup.Tokens was never reported by any record.

// Ledger types are the server's own, so SDK consumers and the backend cannot
// drift apart.
type (
	UsageLedgerRecord  = usageledger.Record
	UsageHookSummary   = usageledger.HookSummary
	UsageHookResult    = usageledger.HookResult
	UsageLedgerPage    = usageledger.Page
	UsageLedgerSummary = usageledger.Summary
	UsageSummaryGroup  = usageledger.SummaryGroup
	UsageCostTotal     = usageledger.CostTotal
	ModelPrice         = usageledger.Price
	ModelPriceMeter    = usageledger.Rational
)

// UsageMaxPageSize is the largest page the ledger serves.
const UsageMaxPageSize = usageledger.MaxLimit

// UsageFilter selects ledger records. Zero fields match everything. Since is
// inclusive and Until exclusive, both on the record start time.
type UsageFilter struct {
	Plane, Provider, ModelID, ToolName string
	ActorID, PrincipalID, ClientID     string
	// AccessPointID and CallerKind (agent, user or client) select exchanges
	// that entered through an access point.
	AccessPointID, CallerKind string
	Status, Source            string
	// ErrorClass selects an exact error_class. UsageBound selects
	// lower_bound, ambiguous, partial or "none" (final or unknown usage).
	ErrorClass, UsageBound string
	// HookDecision selects records by their request-hook decision (allow,
	// deny, transform, error, skipped) or "none" (no hook ran).
	HookDecision string
	// Batch, when non-nil, selects batch (true) or non-batch (false) records.
	Batch        *bool
	Since, Until time.Time
}

func (f UsageFilter) values() url.Values {
	q := url.Values{}
	for name, value := range map[string]string{"plane": f.Plane, "provider": f.Provider, "model_id": f.ModelID, "tool_name": f.ToolName, "actor_id": f.ActorID, "principal_id": f.PrincipalID, "client_id": f.ClientID, "access_point_id": f.AccessPointID, "caller_kind": f.CallerKind, "status": f.Status, "source": f.Source, "error_class": f.ErrorClass, "usage_bound": f.UsageBound, "hook_decision": f.HookDecision} {
		if value != "" {
			q.Set(name, value)
		}
	}
	if f.Batch != nil {
		q.Set("batch", strconv.FormatBool(*f.Batch))
	}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339Nano))
	}
	if !f.Until.IsZero() {
		q.Set("until", f.Until.UTC().Format(time.RFC3339Nano))
	}
	return q
}

// UsageRecordsQuery is a filtered page request, newest first.
type UsageRecordsQuery struct {
	UsageFilter
	Limit  int // 0 selects the server default (100)
	Cursor string
}

// UsageSummaryQuery groups the records selected by the filter. GroupBy takes
// wire names (plane, provider, model_id, tool_name, actor_id, principal_id,
// client_id, access_point_id, caller_kind, status, source, error_class,
// usage_bound, batch); NormalizeUsageGroupBy accepts friendly aliases and is
// applied by SummarizeUsage.
type UsageSummaryQuery struct {
	UsageFilter
	GroupBy []string
	Bucket  string // "", "hour" or "day"
}

var usageGroupAliases = map[string]string{
	"model": "model_id", "agent": "actor_id", "actor": "actor_id", "user": "principal_id",
	"principal": "principal_id", "client": "client_id", "tool": "tool_name",
	"access-point": "access_point_id", "access_point": "access_point_id", "accesspoint": "access_point_id",
	"error-class": "error_class", "errorclass": "error_class", "bound": "usage_bound", "usage-bound": "usage_bound",
	"hook": "hook_decision", "hooks": "hook_decision", "hook-decision": "hook_decision",
	"caller-kind": "caller_kind", "caller_kind": "caller_kind", "callerkind": "caller_kind",
}

// NormalizeUsageGroupBy maps friendly aliases (model, agent, user, client,
// tool, access-point, caller-kind) to wire names and rejects unknown or repeated fields.
func NormalizeUsageGroupBy(fields []string) ([]string, error) {
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, raw := range fields {
		field := strings.ToLower(strings.TrimSpace(raw))
		if field == "" {
			continue
		}
		if wire, ok := usageGroupAliases[field]; ok {
			field = wire
		}
		valid := false
		for _, allowed := range usageledger.GroupByFields {
			valid = valid || allowed == field
		}
		if !valid {
			return nil, fmt.Errorf("aiplex: cannot group usage by %q (use %s)", raw, strings.Join(usageledger.GroupByFields, ", "))
		}
		if seen[field] {
			return nil, fmt.Errorf("aiplex: usage group field %q repeated", field)
		}
		seen[field] = true
		out = append(out, field)
	}
	return out, nil
}

func withQuery(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

func (q UsageRecordsQuery) encode() (string, error) {
	if q.Limit < 0 || q.Limit > UsageMaxPageSize {
		return "", fmt.Errorf("aiplex: usage limit must be between 1 and %d", UsageMaxPageSize)
	}
	values := q.values()
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Cursor != "" {
		values.Set("cursor", q.Cursor)
	}
	return withQuery("/api/v1/usage/records", values), nil
}

// QueryUsageRecords returns one page of ledger records, newest first.
func (c *Client) QueryUsageRecords(ctx context.Context, q UsageRecordsQuery) (*UsageLedgerPage, error) {
	path, err := q.encode()
	if err != nil {
		return nil, err
	}
	var out UsageLedgerPage
	if err = c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUsageLedgerRecord returns one visible ledger record.
func (c *Client) GetUsageLedgerRecord(ctx context.Context, id string) (*UsageLedgerRecord, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	var out UsageLedgerRecord
	if err := c.do(ctx, http.MethodGet, "/api/v1/usage/records/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SummarizeUsage aggregates ledger records. Check Truncated before treating
// totals as complete.
func (c *Client) SummarizeUsage(ctx context.Context, q UsageSummaryQuery) (*UsageLedgerSummary, error) {
	groupBy, err := NormalizeUsageGroupBy(q.GroupBy)
	if err != nil {
		return nil, err
	}
	if q.Bucket != "" && q.Bucket != usageledger.BucketHour && q.Bucket != usageledger.BucketDay {
		return nil, fmt.Errorf("aiplex: usage bucket must be hour or day")
	}
	values := q.values()
	if len(groupBy) > 0 {
		values.Set("group_by", strings.Join(groupBy, ","))
	}
	if q.Bucket != "" {
		values.Set("bucket", q.Bucket)
	}
	var out UsageLedgerSummary
	if err = c.do(ctx, http.MethodGet, withQuery("/api/v1/usage/summary", values), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UsageRecordPager walks every page of a records query:
//
//	p := client.PageUsageRecords(q)
//	for p.Next(ctx) { use(p.Record()) }
//	if err := p.Err(); err != nil { ... }
type UsageRecordPager struct {
	client  *Client
	query   UsageRecordsQuery
	buf     []UsageLedgerRecord
	current UsageLedgerRecord
	started bool
	done    bool
	err     error
	// Pages counts the pages fetched so far.
	Pages int
}

// PageUsageRecords returns a pager that starts at q.Cursor.
func (c *Client) PageUsageRecords(q UsageRecordsQuery) *UsageRecordPager {
	return &UsageRecordPager{client: c, query: q}
}

// Next advances to the next record, fetching a page when needed. It returns
// false at the end or on error; check Err.
func (p *UsageRecordPager) Next(ctx context.Context) bool {
	for len(p.buf) == 0 {
		if p.err != nil || p.done {
			return false
		}
		if p.started && p.query.Cursor == "" {
			p.done = true
			return false
		}
		page, err := p.client.QueryUsageRecords(ctx, p.query)
		if err != nil {
			p.err = err
			return false
		}
		p.started = true
		p.Pages++
		if page.NextCursor != "" && page.NextCursor == p.query.Cursor {
			p.err = errors.New("aiplex: usage pagination did not advance")
			return false
		}
		p.buf = page.Records
		p.query.Cursor = page.NextCursor
		if p.query.Cursor == "" && len(p.buf) == 0 {
			p.done = true
			return false
		}
	}
	p.current, p.buf = p.buf[0], p.buf[1:]
	return true
}

// Record returns the record Next advanced to.
func (p *UsageRecordPager) Record() UsageLedgerRecord { return p.current }

// Err reports the error that ended iteration, if any.
func (p *UsageRecordPager) Err() error { return p.err }

// Cursor is the cursor of the next unfetched page; empty once exhausted.
func (p *UsageRecordPager) Cursor() string { return p.query.Cursor }

// CollectUsageRecords drains a pager into a slice. When max > 0 it stops after
// max records and reports truncated=true if more records remain.
func (c *Client) CollectUsageRecords(ctx context.Context, q UsageRecordsQuery, max int) (records []UsageLedgerRecord, truncated bool, err error) {
	p := c.PageUsageRecords(q)
	for p.Next(ctx) {
		if max > 0 && len(records) >= max {
			return records, true, nil
		}
		records = append(records, p.Record())
	}
	return records, false, p.Err()
}

// ModelPriceResource is a stored model-price with its optimistic-concurrency
// revision. Pass Revision to PutModelPrice / DeleteModelPrice.
type ModelPriceResource struct {
	ID       string     `json:"id"`
	Revision uint64     `json:"revision"`
	Price    ModelPrice `json:"price"`
}

type priceRecord struct {
	ID       string          `json:"id"`
	Revision uint64          `json:"revision"`
	Spec     json.RawMessage `json:"spec"`
}

func (r priceRecord) resource() (ModelPriceResource, error) {
	out := ModelPriceResource{ID: r.ID, Revision: r.Revision}
	if err := json.Unmarshal(r.Spec, &out.Price); err != nil {
		return out, fmt.Errorf("aiplex: decode model price %q: %w", r.ID, err)
	}
	return out, nil
}

// ListModelPrices returns the tenant's configured prices.
func (c *Client) ListModelPrices(ctx context.Context) ([]ModelPriceResource, error) {
	var raw []priceRecord
	if err := c.do(ctx, http.MethodGet, "/api/v1/usage/prices/", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]ModelPriceResource, 0, len(raw))
	for _, r := range raw {
		item, err := r.resource()
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// GetModelPrice returns one price with its current revision.
func (c *Client) GetModelPrice(ctx context.Context, id string) (*ModelPriceResource, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	var raw priceRecord
	if err := c.do(ctx, http.MethodGet, "/api/v1/usage/prices/"+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}
	out, err := raw.resource()
	return &out, err
}

// PutModelPrice creates (revision 0) or replaces (current revision) a price.
// A stale revision fails with an *Error whose StatusCode is 409; see
// IsRevisionConflict. Administrator only.
func (c *Client) PutModelPrice(ctx context.Context, price ModelPrice, revision uint64) (*ModelPriceResource, error) {
	if err := price.Validate(); err != nil {
		return nil, err
	}
	if err := validateResourceID(price.ID); err != nil {
		return nil, err
	}
	var raw priceRecord
	headers := map[string]string{"If-Match": strconv.FormatUint(revision, 10)}
	if err := c.doWithHeaders(ctx, http.MethodPut, "/api/v1/usage/prices/"+url.PathEscape(price.ID), price, &raw, headers); err != nil {
		return nil, err
	}
	out, err := raw.resource()
	return &out, err
}

// DeleteModelPrice removes a price at the given positive revision.
func (c *Client) DeleteModelPrice(ctx context.Context, id string, revision uint64) error {
	if err := validateResourceID(id); err != nil {
		return err
	}
	if revision == 0 {
		return fmt.Errorf("aiplex: positive expected revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, "/api/v1/usage/prices/"+url.PathEscape(id), nil, nil, map[string]string{"If-Match": strconv.FormatUint(revision, 10)})
}

// IsRevisionConflict reports whether err is a 409 stale-revision response.
func IsRevisionConflict(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict
}

// FormatMoney renders a decimal-integer string of minor units at scale
// decimal places exactly, without floating point: ("1135", 6) -> "0.001135".
func FormatMoney(amount string, scale uint8) (string, error) {
	sign := ""
	digits := amount
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if digits == "" {
		return "", fmt.Errorf("aiplex: money amount %q is not a decimal integer", amount)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("aiplex: money amount %q is not a decimal integer", amount)
		}
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	if scale == 0 {
		if digits == "0" {
			sign = ""
		}
		return sign + digits, nil
	}
	n := int(scale)
	if len(digits) <= n {
		digits = strings.Repeat("0", n-len(digits)+1) + digits
	}
	whole, frac := digits[:len(digits)-n], digits[len(digits)-n:]
	if whole == "0" && strings.Trim(frac, "0") == "" {
		sign = ""
	}
	return sign + whole + "." + frac, nil
}

// FormatCost renders a record cost with currency and basis, e.g.
// "USD 0.001135 (estimated)". Unknown costs render as "unknown", never zero.
func FormatCost(c usageledger.Cost) string {
	if c.Basis == usageledger.BasisCached {
		return "0 (cached)"
	}
	if c.Basis != usageledger.BasisSettled && c.Basis != usageledger.BasisEstimated {
		return "unknown"
	}
	return formatCostParts(c.Currency, c.Amount, c.Scale, c.Basis)
}

// FormatCostTotal renders a summary cost total with currency and basis.
func FormatCostTotal(c usageledger.CostTotal) string {
	return formatCostParts(c.Currency, c.Amount, c.Scale, c.Basis)
}

func formatCostParts(currency, amount string, scale uint8, basis string) string {
	value, err := FormatMoney(amount, scale)
	if err != nil {
		return "invalid (" + basis + ")"
	}
	return fmt.Sprintf("%s %s (%s)", currency, value, basis)
}
