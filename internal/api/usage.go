package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// UsageHandler serves the tenant-scoped usage ledger under /api/v1/usage.
// Administrators read every record of their tenant; other callers read only
// records in which they are the principal or the actor.
type UsageHandler struct {
	Ledger      usageledger.Store
	Tenant      TenantResolver
	Development bool
	// Prices reads the tenant's model prices for back-estimation.
	Prices func(ctx context.Context, tenant string) ([]usageledger.Price, error)
	// Feedback keeps record feedback; nil answers 503 on the feedback routes.
	Feedback usageledger.FeedbackStore
	// FeedbackComments allows free-text comments on feedback (default off).
	FeedbackComments bool
	// TraceURLTemplate is an operator-configured link template for an
	// external trace viewer; "{trace_id}" is replaced by the record's trace id.
	TraceURLTemplate string
	// Tags is the tag policy (its allowlisted header keys are advertised).
	Tags *TagPolicy
	Now  func() time.Time
}

func NewUsageHandler(ledger usageledger.Store, tenant TenantResolver, development bool) *UsageHandler {
	return &UsageHandler{Ledger: ledger, Tenant: tenant, Development: development}
}

var (
	usageFilterParams  = []string{"plane", "provider", "model_id", "tool_name", "actor_id", "principal_id", "client_id", "access_point_id", "caller_kind", "budget_id", "status", "source", "error_class", "usage_bound", "batch", "harness", "hook_decision", "cost_basis", "since", "until"}
	usageListParams    = append(append([]string{}, usageFilterParams...), "limit", "cursor")
	usageSummaryParams = append(append([]string{}, usageFilterParams...), "group_by", "bucket", "merge_sources", "feedback")
)

// scope resolves the tenant and the caller's visibility. A nil visibility
// means tenant-wide (administrator) access.
func (h *UsageHandler) scope(w http.ResponseWriter, r *http.Request) (string, []string, bool) {
	if h.Ledger == nil || h.Tenant == nil {
		Error(w, r, http.StatusServiceUnavailable, "USAGE_LEDGER_UNAVAILABLE", "usage ledger is not configured")
		return "", nil, false
	}
	tenant, err := h.Tenant(r)
	if err != nil || tenant == "" {
		Error(w, r, http.StatusForbidden, "TENANT_REQUIRED", "verified tenant binding required")
		return "", nil, false
	}
	access := GetWIFAccess(r.Context())
	if access == nil {
		if h.Development {
			return tenant, nil, true
		}
		Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
		return "", nil, false
	}
	for _, role := range access.Roles {
		if role == models.RoleAdmin {
			return tenant, nil, true
		}
	}
	identity := GetWIFIdentity(r.Context())
	if identity == nil || identity.PrincipalID == "" {
		Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
		return "", nil, false
	}
	return tenant, []string{identity.PrincipalID}, true
}

func usageQueryError(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusBadRequest, "INVALID_QUERY", message)
}

// parseUsageFilter validates query parameters; unknown or repeated
// parameters and unknown enumeration values are rejected.
func parseUsageFilter(r *http.Request, allowed []string) (usageledger.Filter, error) {
	values := r.URL.Query()
	known := map[string]bool{}
	for _, name := range allowed {
		known[name] = true
	}
	tags := map[string]string{}
	for name, v := range values {
		if key, isTag := strings.CutPrefix(name, "tag."); isTag && usageledger.ValidTagKey(key) {
			if len(v) != 1 || len(tags) >= usageledger.MaxTags || !usageledger.ValidTagValue(v[0]) {
				return usageledger.Filter{}, errors.New("tag filters need one valid value each, at most " + strconv.Itoa(usageledger.MaxTags))
			}
			tags[key] = v[0]
			continue
		}
		if !known[name] {
			return usageledger.Filter{}, errors.New("unknown query parameter " + strconv.Quote(name))
		}
		if len(v) != 1 {
			return usageledger.Filter{}, errors.New("query parameter " + strconv.Quote(name) + " must appear once")
		}
		if len(v[0]) > usageledger.MaxTextLength {
			return usageledger.Filter{}, errors.New("query parameter " + strconv.Quote(name) + " is too long")
		}
	}
	f := usageledger.Filter{Plane: values.Get("plane"), Provider: values.Get("provider"), ModelID: values.Get("model_id"), ToolName: values.Get("tool_name"), ActorID: values.Get("actor_id"), PrincipalID: values.Get("principal_id"), ClientID: values.Get("client_id"), AccessPointID: values.Get("access_point_id"), CallerKind: values.Get("caller_kind"), BudgetID: values.Get("budget_id"), Status: values.Get("status"), Source: values.Get("source"), ErrorClass: values.Get("error_class"), UsageBound: values.Get("usage_bound"), Harness: values.Get("harness"), HookDecision: values.Get("hook_decision"), CostBasis: values.Get("cost_basis")}
	if len(tags) > 0 {
		f.Tags = tags
	}
	switch f.Plane {
	case "", usageledger.PlaneLLM, usageledger.PlaneMCP, usageledger.PlaneA2A:
	default:
		return f, errors.New("plane must be llmplex, mcplex or a2aplex")
	}
	switch f.CallerKind {
	case "", "agent", "user", "client":
	default:
		return f, errors.New("caller_kind must be agent, user or client")
	}
	switch f.Status {
	case "", usageledger.StatusOK, usageledger.StatusError, usageledger.StatusDenied, usageledger.StatusUnknown:
	default:
		return f, errors.New("status must be ok, error, denied or unknown")
	}
	switch f.Source {
	case "", usageledger.SourceManaged, usageledger.SourceReported:
	default:
		return f, errors.New("source must be managed or reported")
	}
	switch f.CostBasis {
	case "", usageledger.BasisSettled, usageledger.BasisEstimated, usageledger.BasisReported, usageledger.BasisUnknown:
	default:
		return f, errors.New("cost_basis must be settled, estimated, reported or unknown")
	}
	if f.HookDecision != "" && !usageledger.ValidHookDecision(f.HookDecision) {
		return f, errors.New("hook_decision must be allow, deny, transform, error, skipped or none")
	}
	switch f.UsageBound {
	case "", usageledger.UsageLowerBound, usageledger.UsageAmbiguous, usageledger.UsagePartial, usageledger.UsageBoundNone:
	default:
		return f, errors.New("usage_bound must be lower_bound, ambiguous, partial or none")
	}
	if raw := values.Get("batch"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil || (raw != "true" && raw != "false") {
			return f, errors.New("batch must be true or false")
		}
		f.Batch = &b
	}
	for name, target := range map[string]*time.Time{"since": &f.Since, "until": &f.Until} {
		if raw := values.Get(name); raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return f, errors.New(name + " must be an RFC3339 timestamp")
			}
			*target = t.UTC()
		}
	}
	if !f.Since.IsZero() && !f.Until.IsZero() && !f.Until.After(f.Since) {
		return f, errors.New("until must be after since")
	}
	return f, nil
}

// ListRecords returns one page of records, newest first.
// GET /api/v1/usage/records
func (h *UsageHandler) ListRecords(w http.ResponseWriter, r *http.Request) {
	tenant, visibility, ok := h.scope(w, r)
	if !ok {
		return
	}
	filter, err := parseUsageFilter(r, usageListParams)
	if err != nil {
		usageQueryError(w, r, err.Error())
		return
	}
	filter.Visibility = visibility
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > usageledger.MaxLimit {
			usageQueryError(w, r, "limit must be between 1 and "+strconv.Itoa(usageledger.MaxLimit))
			return
		}
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" {
		if _, err := usageledger.DecodeCursor(cursor); err != nil {
			usageQueryError(w, r, "cursor is malformed")
			return
		}
	}
	page, err := h.Ledger.List(r.Context(), tenant, usageledger.Query{Filter: filter, Limit: limit, Cursor: cursor})
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, page)
}

// GetRecord returns one visible record.
// GET /api/v1/usage/records/{id}
func (h *UsageHandler) GetRecord(w http.ResponseWriter, r *http.Request) {
	tenant, visibility, ok := h.scope(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		id = r.PathValue("id")
	}
	if id == "" || len(id) > usageledger.MaxTextLength {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "usage record not found")
		return
	}
	record, err := h.Ledger.Get(r.Context(), tenant, id)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	// Invisible records are indistinguishable from missing ones.
	if record.TenantID != tenant || !(usageledger.Filter{Visibility: visibility}).Matches(record) {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "usage record not found")
		return
	}
	detail := usageRecordDetail{Record: record}
	if h.Feedback != nil {
		if latest, err := h.Feedback.Latest(r.Context(), tenant, []string{record.ID}); err == nil {
			if fb, ok := latest[record.ID]; ok {
				detail.Feedback = &fb
			}
		}
	}
	JSON(w, http.StatusOK, detail)
}

// usageRecordDetail is a record with its latest feedback.
type usageRecordDetail struct {
	usageledger.Record
	Feedback *usageledger.Feedback `json:"feedback,omitempty"`
}

// Summary aggregates visible records.
// GET /api/v1/usage/summary
func (h *UsageHandler) Summary(w http.ResponseWriter, r *http.Request) {
	tenant, visibility, ok := h.scope(w, r)
	if !ok {
		return
	}
	filter, err := parseUsageFilter(r, usageSummaryParams)
	if err != nil {
		usageQueryError(w, r, err.Error())
		return
	}
	filter.Visibility = visibility
	groupBy, err := usageledger.ParseGroupBy(r.URL.Query().Get("group_by"))
	if err != nil {
		usageQueryError(w, r, "group_by must be a comma-separated subset of "+strings.Join(usageledger.GroupByFields, ",")+" or tag:<key>")
		return
	}
	bucket := r.URL.Query().Get("bucket")
	if usageledger.ValidateBucket(bucket) != nil {
		usageQueryError(w, r, "bucket must be hour or day")
		return
	}
	query := usageledger.SummaryQuery{Filter: filter, GroupBy: groupBy, Bucket: bucket}
	switch r.URL.Query().Get("merge_sources") {
	case "", "false":
	case "true":
		if filter.Source != "" {
			usageQueryError(w, r, "merge_sources applies only without a source filter")
			return
		}
		query.MergeSources = true
	default:
		usageQueryError(w, r, "merge_sources must be true or false")
		return
	}
	switch r.URL.Query().Get("feedback") {
	case "", "false":
	case "true":
		if h.Feedback == nil {
			Error(w, r, http.StatusServiceUnavailable, "FEEDBACK_UNAVAILABLE", "feedback is not configured")
			return
		}
		query.Feedback = h.Feedback
	default:
		usageQueryError(w, r, "feedback must be true or false")
		return
	}
	summary, err := usageledger.Summarize(r.Context(), h.Ledger, tenant, query)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, summary)
}

func (h *UsageHandler) storeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, usageledger.ErrNotFound):
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "usage record not found")
	case errors.Is(err, usageledger.ErrInvalid):
		usageQueryError(w, r, "invalid usage query")
	default:
		Error(w, r, http.StatusInternalServerError, "USAGE_LEDGER_ERROR", "usage ledger unavailable")
	}
}
