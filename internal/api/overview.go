package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// OverviewHandler serves the read-only single-pane API under
// /api/v1/overview. It joins access points, the usage ledger, agent identity
// bindings, client registrations and IAM configuration. It never writes, and
// every read is bound to the verified tenant.
//
// Known users are derived, not stored: there is no user directory. See
// overviewUserSources.
type OverviewHandler struct {
	Connected *ConnectedHandler
	Ledger    usageledger.Store
	// Agents, RoleBindings and UserScopes are optional enrichments taken from
	// the registry store; nil values are reported as unavailable.
	Agents       registry.AgentStore
	RoleBindings registry.RoleBindingStore
	UserScopes   overviewUserScopeReader
	// AgentBindings are the tenant's agent identity bindings (optional).
	AgentBindings *agentfederation.Store
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// usageWithheld is set on the per-request copy served to non-admins.
	usageWithheld bool
}

type overviewUserScopeReader interface {
	GetUserScopes(context.Context, string) ([]string, error)
}

// NewOverviewHandler wires the handler. registryStore may be nil or any value
// implementing registry.AgentStore, registry.RoleBindingStore and
// GetUserScopes; each capability is optional.
func NewOverviewHandler(connected *ConnectedHandler, ledger usageledger.Store, registryStore any, bindings *agentfederation.Store) *OverviewHandler {
	h := &OverviewHandler{Connected: connected, Ledger: ledger, AgentBindings: bindings}
	if s, ok := registryStore.(registry.AgentStore); ok {
		h.Agents = s
	}
	if s, ok := registryStore.(registry.RoleBindingStore); ok {
		h.RoleBindings = s
	}
	if s, ok := registryStore.(overviewUserScopeReader); ok {
		h.UserScopes = s
	}
	return h
}

// Mount registers the routes under /api/v1/overview. Reads require the admin
// or deployer role; usage roll-ups are admin-only (usageScoped).
func (h *OverviewHandler) Mount(r chi.Router) {
	r.Route("/overview", func(r chi.Router) {
		r.Use(RequireRole(models.RoleAdmin, models.RoleDeployer))
		r.Get("/subjects", h.usageScoped((*OverviewHandler).Subjects))
		r.Get("/subjects/{kind}/{id}", h.usageScoped((*OverviewHandler).Subject))
		r.Get("/subjects/{kind}", h.usageScoped((*OverviewHandler).Subject)) // ?id=… for ids that contain "/"
		r.Get("/access-points", h.usageScoped((*OverviewHandler).AccessPointList))
		r.Get("/access-points/{id}", h.usageScoped((*OverviewHandler).AccessPointOverview))
		r.Get("/access-points/{id}/connect", h.usageScoped((*OverviewHandler).Connect))
	})
}

// usageScoped serves tenant-wide usage only to administrators, matching
// /api/v1/usage, where every other caller reads only its own records.
// Deployers get the same configuration view with the ledger withheld, so
// usage, last-seen times and ledger-derived subjects are reported as
// unavailable.
func (h *OverviewHandler) usageScoped(serve func(*OverviewHandler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		development := h.Connected != nil && h.Connected.Development
		if h.Ledger == nil || privilegedOrDevelopment(r, development, models.RoleAdmin) {
			serve(h, w, r)
			return
		}
		withheld := *h
		withheld.Ledger = nil
		withheld.usageWithheld = true
		serve(&withheld, w, r)
	}
}

// ledgerUnavailableNote explains why usage is missing from a response.
func (h *OverviewHandler) ledgerUnavailableNote() string {
	if h.usageWithheld {
		return "Tenant-wide usage requires the admin role; usage is withheld."
	}
	return "The usage ledger is not configured; usage is unavailable."
}

func (h *OverviewHandler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func (h *OverviewHandler) tenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.Connected == nil || h.Connected.Tenant == nil {
		Error(w, r, http.StatusForbidden, "TENANT_REQUIRED", "verified tenant binding required")
		return "", false
	}
	t, err := h.Connected.Tenant(r)
	if err != nil || t == "" {
		Error(w, r, http.StatusForbidden, "TENANT_REQUIRED", "verified tenant binding required")
		return "", false
	}
	return t, true
}

func overviewQueryError(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusBadRequest, "INVALID_QUERY", message)
}

// overviewParams rejects unknown or repeated query parameters.
func overviewParams(r *http.Request, allowed ...string) error {
	known := map[string]bool{}
	for _, a := range allowed {
		known[a] = true
	}
	for name, v := range r.URL.Query() {
		if !known[name] {
			return fmt.Errorf("unknown query parameter %q", name)
		}
		if len(v) != 1 || len(v[0]) > 512 {
			return fmt.Errorf("query parameter %q must appear once and be bounded", name)
		}
	}
	return nil
}

const (
	overviewDefaultWindow = 24 * time.Hour
	overviewMaxWindow     = usageledger.DefaultRetention
	overviewDefaultLimit  = 50
	overviewMaxLimit      = 200
	overviewMaxCandidates = 10000
	overviewMaxAccess     = 500
)

// parseOverviewSince accepts an RFC3339 timestamp or a duration such as 30m,
// 24h or 7d, measured back from now.
func parseOverviewSince(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return now.Add(-overviewDefaultWindow), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		t = t.UTC()
		if t.After(now) {
			return time.Time{}, fmt.Errorf("since must not be in the future")
		}
		if now.Sub(t) > overviewMaxWindow {
			return time.Time{}, fmt.Errorf("since is beyond the %d day ledger retention", int(overviewMaxWindow.Hours()/24))
		}
		return t, nil
	}
	var d time.Duration
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil || days < 1 {
			return time.Time{}, fmt.Errorf("since must be an RFC3339 timestamp or a duration like 24h or 7d")
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		d, err = time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return time.Time{}, fmt.Errorf("since must be an RFC3339 timestamp or a duration like 24h or 7d")
		}
	}
	if d > overviewMaxWindow {
		return time.Time{}, fmt.Errorf("since is beyond the %d day ledger retention", int(overviewMaxWindow.Hours()/24))
	}
	return now.Add(-d), nil
}

func parseOverviewLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return overviewDefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > overviewMaxLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d", overviewMaxLimit)
	}
	return n, nil
}

// Cursors are the last id of the previous page: results are ordered by id.
func encodeOverviewCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func decodeOverviewCursor(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) == 0 || len(b) > 1024 {
		return "", fmt.Errorf("cursor is malformed")
	}
	return string(b), nil
}

// ── usage views ──────────────────────────────────────────────────────────

// OverviewCost is one exact cost total. Unknown cost is never folded into it.
type OverviewCost struct {
	Currency string `json:"currency"`
	Basis    string `json:"basis"`
	Amount   string `json:"amount"`
	Scale    uint8  `json:"scale"`
}

// OverviewUsage is a ledger roll-up. Tokens lists only counters that were
// reported; TokensUnknown counts the records in which a counter was not.
// Absence is unknown, never zero.
type OverviewUsage struct {
	Requests           int64            `json:"requests"`
	Records            int64            `json:"records"`
	Errors             int64            `json:"errors"`
	Denied             int64            `json:"denied"`
	Tokens             map[string]int64 `json:"tokens"`
	TokensUnknown      map[string]int64 `json:"tokens_unknown"`
	Cost               []OverviewCost   `json:"cost"`
	UnknownCostRecords int64            `json:"unknown_cost_records"`
	// Bounded counts records whose counters are not final (usage_bound set).
	// They are included in Tokens; a non-zero count marks the totals non-final.
	BoundedUsageRecords   int64 `json:"bounded_usage_records,omitempty"`
	LowerBoundRecords     int64 `json:"lower_bound_records,omitempty"`
	AmbiguousUsageRecords int64 `json:"ambiguous_usage_records,omitempty"`
	PartialUsageRecords   int64 `json:"partial_usage_records,omitempty"`
}

func overviewUsageOf(g usageledger.SummaryGroup) *OverviewUsage {
	u := &OverviewUsage{Requests: g.Requests, Records: g.Records, Errors: g.Errors, Denied: g.Denied, Tokens: map[string]int64{}, TokensUnknown: map[string]int64{}, Cost: []OverviewCost{}, UnknownCostRecords: g.UnknownCostRecords,
		BoundedUsageRecords: g.BoundedUsageRecords, LowerBoundRecords: g.LowerBoundRecords, AmbiguousUsageRecords: g.AmbiguousUsageRecords, PartialUsageRecords: g.PartialUsageRecords}
	for k, v := range g.Tokens {
		u.Tokens[k] = v
	}
	for k, v := range g.TokensUnknown {
		u.TokensUnknown[k] = v
	}
	for _, c := range g.Cost {
		u.Cost = append(u.Cost, OverviewCost{Currency: c.Currency, Basis: c.Basis, Amount: c.Amount, Scale: c.Scale})
	}
	return u
}

// OverviewUsageGroup is usage under one grouping key.
type OverviewUsageGroup struct {
	Key      map[string]string `json:"key"`
	LastSeen *time.Time        `json:"last_seen,omitempty"`
	Usage    *OverviewUsage    `json:"usage"`
}

type overviewScan struct {
	Groups    []usageledger.SummaryGroup
	Last      map[string]time.Time
	Scanned   int64
	Truncated bool
}

func overviewField(r usageledger.Record, field string) string {
	switch field {
	case "plane":
		return r.Plane
	case "model_id":
		return r.ModelID
	case "tool_name":
		return r.ToolName
	case "principal_id":
		return r.PrincipalID
	case "actor_id":
		return r.ActorID
	case "client_id":
		return r.ClientID
	case "caller_kind":
		return r.CallerKind
	case "access_point_id":
		return r.AccessPointID
	}
	return ""
}

func overviewGroupKey(r usageledger.Record, groupBy []string) string {
	parts := make([]string, len(groupBy))
	for i, f := range groupBy {
		parts[i] = overviewField(r, f)
	}
	return strings.Join(parts, "\x00")
}

// scan pages through the ledger newest first, bounded by
// usageledger.MaxSummaryRecords. mutate may rewrite a copy of each record
// (for example to carry a derived subject id in PrincipalID) and returns
// false to skip it.
func (h *OverviewHandler) scan(ctx context.Context, tenant string, filter usageledger.Filter, groupBy []string, mutate func(*usageledger.Record) bool) (overviewScan, error) {
	out := overviewScan{Last: map[string]time.Time{}, Groups: []usageledger.SummaryGroup{}}
	if h.Ledger == nil {
		return out, errOverviewNoLedger
	}
	agg := usageledger.NewAggregator(usageledger.SummaryQuery{Filter: filter, GroupBy: groupBy})
	cursor := ""
	for {
		page, err := h.Ledger.List(ctx, tenant, usageledger.Query{Filter: filter, Limit: usageledger.MaxLimit, Cursor: cursor})
		if err != nil {
			return out, err
		}
		for _, rec := range page.Records {
			if rec.TenantID != tenant {
				continue
			}
			if mutate != nil && !mutate(&rec) {
				continue
			}
			if out.Scanned >= usageledger.MaxSummaryRecords {
				out.Truncated = true
				res := agg.Result()
				out.Groups = res.Groups
				return out, nil
			}
			if err := agg.Add(rec); err != nil {
				return out, err
			}
			out.Scanned++
			key := overviewGroupKey(rec, groupBy)
			if t, ok := out.Last[key]; !ok || rec.StartedAt.After(t) {
				out.Last[key] = rec.StartedAt.UTC()
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	res := agg.Result()
	out.Groups, out.Truncated = res.Groups, res.Truncated || out.Truncated
	return out, nil
}

var errOverviewNoLedger = fmt.Errorf("usage ledger is not configured")

// lastSeen returns the newest ledger record start for a filter, ignoring the
// reporting window. ok is false when there is none.
func (h *OverviewHandler) lastSeen(ctx context.Context, tenant string, filter usageledger.Filter, keep func(usageledger.Record) bool) (time.Time, bool) {
	if h.Ledger == nil {
		return time.Time{}, false
	}
	cursor := ""
	for i := 0; i < 20; i++ {
		page, err := h.Ledger.List(ctx, tenant, usageledger.Query{Filter: filter, Limit: 100, Cursor: cursor})
		if err != nil {
			return time.Time{}, false
		}
		for _, rec := range page.Records {
			if rec.TenantID == tenant && (keep == nil || keep(rec)) {
				return rec.StartedAt.UTC(), true
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return time.Time{}, false
}

func overviewSortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// overviewUsageGroups renders summary groups keyed by their group_by values.
func overviewUsageGroups(s overviewScan, groupBy []string) []OverviewUsageGroup {
	out := make([]OverviewUsageGroup, 0, len(s.Groups))
	for _, g := range s.Groups {
		item := OverviewUsageGroup{Key: map[string]string{}, Usage: overviewUsageOf(g)}
		parts := make([]string, len(groupBy))
		for i, f := range groupBy {
			item.Key[f] = g.Key[f]
			parts[i] = g.Key[f]
		}
		if t, ok := s.Last[strings.Join(parts, "\x00")]; ok {
			tt := t
			item.LastSeen = &tt
		}
		out = append(out, item)
	}
	return out
}
