package aiplex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// The single-pane overview: every caller (agents, users, clients), how it
// authenticates, what it can reach and why, its QoS class and its usage.
//
// Unknown is never zero. A token counter that no record reported is absent
// from OverviewUsage.Tokens and counted in TokensUnknown.

// OverviewIdentity says how a subject authenticates.
type OverviewIdentity struct {
	Source string `json:"source"`
	Detail string `json:"detail"`
}

// OverviewCost is one exact cost total in a currency and basis (settled or
// estimated). Amount is a decimal integer string of minor units at Scale.
type OverviewCost struct {
	Currency string `json:"currency"`
	Basis    string `json:"basis"`
	Amount   string `json:"amount"`
	Scale    uint8  `json:"scale"`
	// AmountDecimal is Amount written as a decimal string ("0.000006"); derived
	// by the server, empty from an older one.
	AmountDecimal string `json:"amount_decimal,omitempty"`
}

// OverviewUsage is a usage-ledger roll-up for a window.
type OverviewUsage struct {
	Requests           int64            `json:"requests"`
	Records            int64            `json:"records"`
	Errors             int64            `json:"errors"`
	Denied             int64            `json:"denied"`
	Tokens             map[string]int64 `json:"tokens"`
	TokensUnknown      map[string]int64 `json:"tokens_unknown"`
	Cost               []OverviewCost   `json:"cost"`
	UnknownCostRecords int64            `json:"unknown_cost_records"`
	// BoundedUsageRecords counts records whose counters are not final (their
	// usage_bound is set). They are included in Tokens; a non-zero count marks
	// the totals non-final. The three per-bound counts sum to it.
	BoundedUsageRecords   int64 `json:"bounded_usage_records,omitempty"`
	LowerBoundRecords     int64 `json:"lower_bound_records,omitempty"`
	AmbiguousUsageRecords int64 `json:"ambiguous_usage_records,omitempty"`
	PartialUsageRecords   int64 `json:"partial_usage_records,omitempty"`
}

// OverviewUsageGroup is usage under one grouping key (model_id, tool_name or
// caller_kind).
type OverviewUsageGroup struct {
	Key      map[string]string `json:"key"`
	LastSeen *time.Time        `json:"last_seen,omitempty"`
	Usage    *OverviewUsage    `json:"usage"`
}

// OverviewSubject is one row of the subject table. Usage is nil when the usage
// ledger is not configured.
type OverviewSubject struct {
	Kind         string           `json:"kind"`
	ID           string           `json:"id"`
	DisplayName  string           `json:"display_name,omitempty"`
	Identity     OverviewIdentity `json:"identity"`
	AccessPoints []string         `json:"access_points"`
	LastSeen     *time.Time       `json:"last_seen"`
	Usage        *OverviewUsage   `json:"usage"`
	KnownFrom    []string         `json:"known_from"`
}

// OverviewSubjectPage is one page of subjects.
type OverviewSubjectPage struct {
	Kind           string            `json:"kind"`
	Since          time.Time         `json:"since"`
	Subjects       []OverviewSubject `json:"subjects"`
	NextCursor     string            `json:"next_cursor,omitempty"`
	UsageAvailable bool              `json:"usage_available"`
	UsageTruncated bool              `json:"usage_truncated"`
	DerivedFrom    []string          `json:"derived_from"`
	Limitations    []string          `json:"limitations"`
}

// OverviewSubjectsQuery selects subjects. Kind is required (agent, user or
// client). Since is an RFC3339 time or a duration such as 24h or 7d.
type OverviewSubjectsQuery struct {
	Kind   string
	Since  string
	Limit  int
	Cursor string
}

// OverviewReason says which configuration grants (or conditionally grants) an item.
type OverviewReason struct {
	Type        string `json:"type"`
	Ref         string `json:"ref,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Conditional bool   `json:"conditional,omitempty"`
}

// OverviewAccessItem is one model, tool or skill a subject may reach. State is
// granted, not_granted or unknown.
type OverviewAccessItem struct {
	Kind          string           `json:"kind"`
	Name          string           `json:"name"`
	AccessPointID string           `json:"access_point_id"`
	Resource      string           `json:"resource,omitempty"`
	Required      string           `json:"required_scope,omitempty"`
	State         string           `json:"state"`
	Reasons       []OverviewReason `json:"reasons"`
}

// OverviewAccess is the effective-access section of a subject.
type OverviewAccess struct {
	Models      []OverviewAccessItem `json:"models"`
	Tools       []OverviewAccessItem `json:"tools"`
	Skills      []OverviewAccessItem `json:"skills"`
	ScopesKnown bool                 `json:"scopes_known"`
	ScopesNote  string               `json:"scopes_note"`
	Truncated   bool                 `json:"truncated"`
}

// OverviewAdmission says whether an access point admits a subject (yes,
// unknown or no) and why.
type OverviewAdmission struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Admits      string `json:"admits"`
	Reason      string `json:"reason"`
}

// OverviewQuotaCoverage reports whether the quota policy covers an access point.
type OverviewQuotaCoverage struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// OverviewAllocations are the policy controls that select an access point by id.
type OverviewAllocations struct {
	Budgets     []quotaplex.BudgetRule      `json:"budgets"`
	Rates       []quotaplex.RateRule        `json:"rates"`
	Concurrency []quotaplex.ConcurrencyRule `json:"concurrency"`
}

// OverviewAccessPointQuota is the QoS and quota view of one access point.
type OverviewAccessPointQuota struct {
	AccessPointID string                `json:"access_point_id"`
	QoSClass      string                `json:"qos_class,omitempty"`
	Coverage      OverviewQuotaCoverage `json:"coverage"`
	Allocations   OverviewAllocations   `json:"allocations"`
}

// OverviewBalance is either an available balance or the reason there is none.
type OverviewBalance struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// OverviewSubjectQuota lists quota coverage per admitting access point.
type OverviewSubjectQuota struct {
	AccessPoints []OverviewAccessPointQuota `json:"access_points"`
	Balance      OverviewBalance            `json:"balance"`
}

// OverviewBinding is a redacted agent identity binding.
type OverviewBinding struct {
	ID      string `json:"id"`
	Preset  string `json:"preset"`
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
	Claim   string `json:"subject_claim"`
}

// OverviewProvider is a configured user identity provider.
type OverviewProvider struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Issuer      string `json:"issuer"`
	Disabled    bool   `json:"disabled"`
}

// OverviewSubjectDetail is the full view of one subject.
type OverviewSubjectDetail struct {
	Kind         string               `json:"kind"`
	ID           string               `json:"id"`
	DisplayName  string               `json:"display_name,omitempty"`
	Identity     OverviewIdentity     `json:"identity"`
	Aliases      []string             `json:"aliases,omitempty"`
	Bindings     []OverviewBinding    `json:"identity_bindings,omitempty"`
	Providers    []OverviewProvider   `json:"user_identity_providers,omitempty"`
	AccessPoints []OverviewAdmission  `json:"access_points"`
	Access       OverviewAccess       `json:"access"`
	Quota        OverviewSubjectQuota `json:"quota"`
	Budgets      OverviewBudgets      `json:"budgets"`
	Since        time.Time            `json:"since"`
	LastSeen     *time.Time           `json:"last_seen"`
	Usage        *OverviewUsage       `json:"usage"`
	ByModel      []OverviewUsageGroup `json:"usage_by_model"`
	ByTool       []OverviewUsageGroup `json:"usage_by_tool"`
	Notes        []string             `json:"notes"`
	KnownFrom    []string             `json:"known_from"`
}

// OverviewCaller is one resolved caller of an access point.
type OverviewCaller struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name,omitempty"`
	Declared    bool           `json:"declared"`
	Observed    bool           `json:"observed"`
	LastSeen    *time.Time     `json:"last_seen"`
	Usage       *OverviewUsage `json:"usage"`
}

// OverviewCallers says who can use an access point.
type OverviewCallers struct {
	CallerKinds []string             `json:"caller_kinds"`
	Open        []string             `json:"open_to_all_of_kind"`
	Users       []OverviewCaller     `json:"users"`
	Agents      []OverviewCaller     `json:"agents"`
	Clients     []OverviewCaller     `json:"clients"`
	Groups      []quotaplex.GroupRef `json:"groups"`
	Note        string               `json:"note,omitempty"`
}

// OverviewResourceModel is a client-facing model of an access point.
type OverviewResourceModel struct {
	Name          string   `json:"name"`
	NativeModel   string   `json:"native_model"`
	ModelID       string   `json:"model_id,omitempty"`
	SurfaceRef    string   `json:"surface_ref"`
	Publisher     string   `json:"publisher,omitempty"`
	SurfaceType   string   `json:"surface_protocol,omitempty"`
	WireProtocols []string `json:"wire_protocols"`
	Resolved      bool     `json:"resolved"`
}

// OverviewResourceTool is one exposed tool connection.
type OverviewResourceTool struct {
	ConnectionRef string   `json:"connection_ref"`
	Expose        []string `json:"expose,omitempty"`
	Tools         []string `json:"tools"`
	Resolved      bool     `json:"resolved"`
}

// OverviewResourceSkill is one exposed skill server.
type OverviewResourceSkill struct {
	InstanceRef string   `json:"instance_ref"`
	Skills      []string `json:"skills"`
	Resolved    bool     `json:"resolved"`
}

// OverviewResources lists what an access point exposes.
type OverviewResources struct {
	Models []OverviewResourceModel `json:"models"`
	Tools  []OverviewResourceTool  `json:"tools"`
	Skills []OverviewResourceSkill `json:"skills"`
}

// OverviewAccessPointUsage is the ledger view of an access point.
type OverviewAccessPointUsage struct {
	Available    bool                 `json:"available"`
	Truncated    bool                 `json:"truncated"`
	Total        *OverviewUsage       `json:"total"`
	ByCallerKind []OverviewUsageGroup `json:"by_caller_kind"`
	ByModel      []OverviewUsageGroup `json:"by_model"`
	ByTool       []OverviewUsageGroup `json:"by_tool"`
}

// OverviewAccessPoint is the single-pane view of an access point.
type OverviewAccessPoint struct {
	AccessPoint AccessPoint              `json:"access_point"`
	Revision    uint64                   `json:"revision"`
	Since       time.Time                `json:"since"`
	Callers     OverviewCallers          `json:"callers"`
	Resources   OverviewResources        `json:"resources"`
	QoS         *AccessPointQoS          `json:"qos"`
	Quota       OverviewAccessPointQuota `json:"quota"`
	Balance     OverviewBalance          `json:"balance"`
	Budgets     OverviewBudgets          `json:"budgets"`
	Usage       OverviewAccessPointUsage `json:"usage"`
	Notes       []string                 `json:"notes"`
}

// ConnectSnippet is one ready-to-paste block.
type ConnectSnippet struct {
	Title    string `json:"title"`
	Language string `json:"language"`
	Content  string `json:"content"`
}

// ConnectAuth says how a client obtains and presents a token.
type ConnectAuth struct {
	Scheme               string   `json:"scheme"`
	Header               string   `json:"header"`
	TokenEnv             string   `json:"token_env"`
	CallerKinds          []string `json:"caller_kinds"`
	AuthorizationServers []string `json:"authorization_servers,omitempty"`
	ResourceMetadataURL  string   `json:"resource_metadata_url,omitempty"`
	Steps                []string `json:"steps"`
}

// ConnectModel is a model and the wire protocols that reach it.
type ConnectModel struct {
	Name      string   `json:"name"`
	Protocols []string `json:"protocols"`
}

// ConnectEndpoint is a model endpoint that does not fit a single base URL:
// Bedrock Runtime and WebSocket sessions. Transport is http, http-stream or
// websocket. "{model}" in URL is replaced by a model name; Query lists query
// parameters a connection requires (WebSocket URLs use ws or wss).
type ConnectEndpoint struct {
	Protocol  string   `json:"protocol"`
	Transport string   `json:"transport"`
	URL       string   `json:"url"`
	Label     string   `json:"label,omitempty"`
	Query     []string `json:"query,omitempty"`
	Models    []string `json:"models"`
}

// AccessPointConnect is ready-to-paste configuration for a client. It never
// contains a token or secret; snippets read $AIPLEX_TOKEN.
type AccessPointConnect struct {
	AccessPointID string            `json:"access_point_id"`
	Client        string            `json:"client"`
	BaseURLs      map[string]string `json:"base_urls"`
	Endpoints     []ConnectEndpoint `json:"endpoints"`
	Models        []ConnectModel    `json:"models"`
	MCPURL        string            `json:"mcp_url,omitempty"`
	MCPCatalogURL string            `json:"mcp_catalog_url,omitempty"`
	SkillsURL     string            `json:"skills_url,omitempty"`
	Auth          ConnectAuth       `json:"auth"`
	Snippets      []ConnectSnippet  `json:"snippets"`
	Notes         []string          `json:"notes"`
}

// OverviewAccessPointCounts are the resources an access point exposes.
type OverviewAccessPointCounts struct {
	Models int `json:"models"`
	Tools  int `json:"tools"`
	Skills int `json:"skills"`
}

// OverviewAccessPointSummary is one row of the access point list; it is built
// without scanning the usage ledger.
type OverviewAccessPointSummary struct {
	ID            string                    `json:"id"`
	DisplayName   string                    `json:"display_name,omitempty"`
	CallerKinds   []string                  `json:"caller_kinds"`
	QoSClass      string                    `json:"qos_class,omitempty"`
	Counts        OverviewAccessPointCounts `json:"counts"`
	QuotaCoverage OverviewQuotaCoverage     `json:"quota_coverage"`
}

// OverviewAccessPointList is one page of access point summaries.
type OverviewAccessPointList struct {
	AccessPoints   []OverviewAccessPointSummary `json:"access_points"`
	NextCursor     string                       `json:"next_cursor,omitempty"`
	SkippedInvalid int                          `json:"skipped_invalid,omitempty"`
}

// OverviewSubjectKinds are the values of OverviewSubjectsQuery.Kind.
var OverviewSubjectKinds = []string{CallerKindAgent, CallerKindUser, CallerKindClient}

func validOverviewKind(kind string) bool {
	for _, k := range OverviewSubjectKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ListOverviewSubjects returns one page of subjects of a kind.
func (c *Client) ListOverviewSubjects(ctx context.Context, q OverviewSubjectsQuery) (*OverviewSubjectPage, error) {
	if !validOverviewKind(q.Kind) {
		return nil, fmt.Errorf("aiplex: kind must be agent, user or client")
	}
	v := url.Values{"kind": {q.Kind}}
	if q.Since != "" {
		v.Set("since", q.Since)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	var out OverviewSubjectPage
	if err := c.do(ctx, http.MethodGet, "/api/v1/overview/subjects?"+v.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetOverviewSubject returns the detail of one subject. since is optional.
func (c *Client) GetOverviewSubject(ctx context.Context, kind, id, since string) (*OverviewSubjectDetail, error) {
	if !validOverviewKind(kind) {
		return nil, fmt.Errorf("aiplex: kind must be agent, user or client")
	}
	if id == "" || len(id) > 1024 {
		return nil, fmt.Errorf("aiplex: subject ID is required")
	}
	// Ids that contain "/" (SPIFFE ids) cannot travel as an escaped path
	// segment; the server also accepts them as ?id=.
	query := url.Values{}
	path := "/api/v1/overview/subjects/" + kind + "/" + url.PathEscape(id)
	if strings.Contains(id, "/") {
		path = "/api/v1/overview/subjects/" + kind
		query.Set("id", id)
	}
	if since != "" {
		query.Set("since", since)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var out OverviewSubjectDetail
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListOverviewAccessPoints returns one page of access point summaries ordered
// by id. limit 0 selects the server default; cursor is a previous next_cursor.
func (c *Client) ListOverviewAccessPoints(ctx context.Context, limit int, cursor string) (*OverviewAccessPointList, error) {
	v := url.Values{}
	if limit < 0 {
		return nil, fmt.Errorf("aiplex: limit must not be negative")
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	path := "/api/v1/overview/access-points"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	var out OverviewAccessPointList
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetOverviewAccessPoint returns the single-pane view of an access point.
// since is optional.
func (c *Client) GetOverviewAccessPoint(ctx context.Context, id, since string) (*OverviewAccessPoint, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	path := "/api/v1/overview/access-points/" + url.PathEscape(id)
	if since != "" {
		path += "?" + url.Values{"since": {since}}.Encode()
	}
	var out OverviewAccessPoint
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ConnectAccessPoint returns ready-to-paste configuration for a client
// profile ID (for example claude-code) or "generic" (also the empty string).
func (c *Client) ConnectAccessPoint(ctx context.Context, id, client string) (*AccessPointConnect, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	path := "/api/v1/overview/access-points/" + url.PathEscape(id) + "/connect"
	if client != "" {
		path += "?" + url.Values{"client": {client}}.Encode()
	}
	var out AccessPointConnect
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
