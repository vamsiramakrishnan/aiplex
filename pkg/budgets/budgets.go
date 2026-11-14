// Package budgets defines tenant budgets: a tree of spending limits above the
// per-caller quota controls. A budget is a saved tenant resource; it is
// enforced by QuotaPlex (as quotaplex.BudgetDraw values resolved inside the
// admission transaction), never by a limiter of its own.
//
// This package is pure: it validates specifications, checks trees, matches a
// request's verified facts against budget members and converts a budget into
// the integer draw QuotaPlex counts. It reads no clock except where a caller
// passes one, and it performs no I/O.
package budgets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// Kind is the tenant resource kind of a budget.
const Kind = "budget"

// Bounds.
const (
	MaxDepth      = 4  // levels in a tree, root included
	MaxPerTenant  = 24 // bounds a tenant tree and, with MaxDepth, the draws one request can accumulate
	MaxMembers    = 256
	MaxThresholds = 8
	MaxHours      = 24 * 31
)

// Units of a limit.
const (
	UnitCost     = "cost"
	UnitTokens   = "tokens"
	UnitRequests = "requests"
)

// Window kinds.
const (
	WindowCalendar = "calendar" // day, week or month in a time zone
	WindowTumbling = "tumbling" // consecutive N-hour windows from a fixed UTC anchor
)

// On-unknown-cost behaviours.
const (
	OnUnknownCostDeny         = "deny"
	OnUnknownCostAllowAndFlag = "allow_and_flag"
)

var (
	idPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	amountPattern   = regexp.MustCompile(`^(0|[1-9][0-9]{0,30})(\.[0-9]{1,18})?$`)
	labelPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Budget is the saved specification.
type Budget struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"display_name,omitempty"`
	Parent      string  `json:"parent,omitempty"`
	Limit       Limit   `json:"limit"`
	Window      Window  `json:"window"`
	Members     Members `json:"members,omitempty"`
	// OnUnknownCost applies to cost budgets: deny (default) refuses a request
	// whose cost cannot be computed; allow_and_flag admits it and counts it as
	// unknown cost.
	OnUnknownCost string `json:"on_unknown_cost,omitempty"`
	// IncludeToolCalls makes a cost budget also count managed tool calls
	// (the mcplex plane) at the price the quota policy's price book gives
	// them. Off by default: without it a cost budget counts model calls only,
	// as budgets saved before the field existed do. It is refused on request
	// budgets (they already count every plane) and token budgets (tool calls
	// have no token meters). It is not part of the draw's shape, so turning it
	// on or off keeps the budget's counter.
	IncludeToolCalls bool `json:"include_tool_calls,omitempty"`
	// Thresholds are percentages of the limit that are recorded as events when
	// consumption crosses them. Default 50, 80, 100.
	Thresholds []int `json:"thresholds,omitempty"`
}

// Limit is what the budget caps. Amount is an exact decimal string: an
// integer for tokens and requests, a decimal in Currency for cost.
type Limit struct {
	Unit     string `json:"unit"`
	Currency string `json:"currency,omitempty"`
	Amount   string `json:"amount"`
}

// Window is the budget period.
type Window struct {
	Kind     string `json:"kind"`
	Unit     string `json:"unit,omitempty"`      // calendar: day, week (starts Monday) or month
	TimeZone string `json:"time_zone,omitempty"` // calendar: IANA zone
	Hours    int    `json:"hours,omitempty"`     // tumbling: window length
}

// Members select who draws from the budget. A request draws when any selector
// matches. Every selector is a verified fact or trusted configuration.
type Members struct {
	// Groups are source-qualified groups from the verified token.
	Groups []quotaplex.GroupRef `json:"groups,omitempty"`
	// Principals are canonical principal ids.
	Principals []string `json:"principals,omitempty"`
	// Agents are agent ids: the verified actor of a delegated token, or a
	// workload principal acting as itself.
	Agents []string `json:"agents,omitempty"`
	// ClientRegistrations are ClientPlex registration ids equal to the
	// verified authorized party.
	ClientRegistrations []string `json:"client_registrations,omitempty"`
	// AccessPoints are access point ids the request entered through.
	AccessPoints []string `json:"access_points,omitempty"`
	// Projects are labels set on an access point by its administrator
	// (access point `project`). A label is never read from a request header,
	// query or body.
	Projects []string `json:"projects,omitempty"`
}

// Empty reports whether no selector is set.
func (m Members) Empty() bool {
	return len(m.Groups)+len(m.Principals)+len(m.Agents)+len(m.ClientRegistrations)+len(m.AccessPoints)+len(m.Projects) == 0
}

// FieldError names the specification field a validation error is about.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

func fieldErr(field, format string, args ...any) error {
	return &FieldError{Field: field, Err: fmt.Errorf(format, args...)}
}

// Decode parses a saved specification strictly (unknown fields refused) and
// checks that its id equals the resource id.
func Decode(id string, raw []byte) (Budget, error) {
	var b Budget
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, fmt.Errorf("invalid budget specification: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return b, fmt.Errorf("invalid budget specification: trailing data")
	}
	if b.ID != id {
		return b, fieldErr("id", "id %q must equal the resource id %q", b.ID, id)
	}
	return b, nil
}

func text(v string, max int) bool {
	if v == "" || len(v) > max || !utf8.ValidString(v) || strings.TrimSpace(v) != v {
		return false
	}
	for _, c := range v {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func uniqueTexts(field string, values []string, pattern *regexp.Regexp, max int) error {
	if len(values) > MaxMembers {
		return fieldErr(field, "%s is bounded to %d entries", field, MaxMembers)
	}
	seen := map[string]bool{}
	for i, v := range values {
		ok := text(v, max)
		if pattern != nil {
			ok = pattern.MatchString(v)
		}
		if !ok || seen[v] {
			return fieldErr(fmt.Sprintf("%s/%d", field, i), "%s entries must be unique bounded identifiers", field)
		}
		seen[v] = true
	}
	return nil
}

// Validate checks one specification on its own.
func (b Budget) Validate() error {
	if !idPattern.MatchString(b.ID) {
		return fieldErr("id", "id must match [A-Za-z0-9][A-Za-z0-9._-]{0,63}")
	}
	if b.DisplayName != "" && !text(b.DisplayName, 128) {
		return fieldErr("display_name", "display_name must be bounded printable text")
	}
	if b.Parent != "" && (!idPattern.MatchString(b.Parent) || b.Parent == b.ID) {
		return fieldErr("parent", "parent must be another budget id")
	}
	switch b.Limit.Unit {
	case UnitCost:
		if !currencyPattern.MatchString(b.Limit.Currency) {
			return fieldErr("limit/currency", "a cost limit needs a three-letter upper-case currency")
		}
	case UnitTokens, UnitRequests:
		if b.Limit.Currency != "" {
			return fieldErr("limit/currency", "currency applies only to cost limits")
		}
	default:
		return fieldErr("limit/unit", "limit.unit must be cost, tokens or requests")
	}
	if !amountPattern.MatchString(b.Limit.Amount) {
		return fieldErr("limit/amount", "limit.amount must be an exact non-negative decimal string such as \"250.00\"")
	}
	if b.Limit.Unit != UnitCost && strings.Contains(b.Limit.Amount, ".") {
		return fieldErr("limit/amount", "a %s limit must be a whole number", b.Limit.Unit)
	}
	if _, err := b.Period(); err != nil {
		return err
	}
	m := b.Members
	if len(m.Groups) > MaxMembers {
		return fieldErr("members/groups", "members.groups is bounded to %d entries", MaxMembers)
	}
	seenGroup := map[quotaplex.GroupRef]bool{}
	for i, g := range m.Groups {
		if !text(g.Source, 1024) || !text(g.ID, 1024) || seenGroup[g] {
			return fieldErr(fmt.Sprintf("members/groups/%d", i), "members.groups entries must be unique source-qualified groups")
		}
		seenGroup[g] = true
	}
	for _, f := range []struct {
		name string
		v    []string
		re   *regexp.Regexp
		max  int
	}{
		{"members/principals", m.Principals, nil, 1024},
		{"members/agents", m.Agents, nil, 1024},
		{"members/client_registrations", m.ClientRegistrations, nil, 256},
		{"members/access_points", m.AccessPoints, idPattern, 128},
		{"members/projects", m.Projects, labelPattern, 128},
	} {
		if err := uniqueTexts(f.name, f.v, f.re, f.max); err != nil {
			return err
		}
	}
	switch b.OnUnknownCost {
	case "":
	case OnUnknownCostDeny, OnUnknownCostAllowAndFlag:
		if b.Limit.Unit != UnitCost {
			return fieldErr("on_unknown_cost", "on_unknown_cost applies only to cost limits")
		}
	default:
		return fieldErr("on_unknown_cost", "on_unknown_cost must be deny or allow_and_flag")
	}
	if b.IncludeToolCalls {
		switch b.Limit.Unit {
		case UnitRequests:
			return fieldErr("include_tool_calls", "include_tool_calls applies only to cost limits: a requests budget already counts tool calls, as it counts every plane")
		case UnitTokens:
			return fieldErr("include_tool_calls", "include_tool_calls applies only to cost limits: tool calls have no token meters, so a tokens budget cannot count them")
		}
	}
	if len(b.Thresholds) > MaxThresholds {
		return fieldErr("thresholds", "at most %d thresholds", MaxThresholds)
	}
	seen := map[int]bool{}
	for i, t := range b.Thresholds {
		if t < 1 || t > 100 || seen[t] {
			return fieldErr(fmt.Sprintf("thresholds/%d", i), "thresholds are unique percentages from 1 to 100")
		}
		seen[t] = true
	}
	return nil
}

// EffectiveThresholds returns the configured thresholds or the default set.
func (b Budget) EffectiveThresholds() []int {
	if len(b.Thresholds) == 0 {
		return []int{50, 80, 100}
	}
	out := append([]int(nil), b.Thresholds...)
	sort.Ints(out)
	return out
}

// EffectiveOnUnknownCost returns the behaviour for a cost budget.
func (b Budget) EffectiveOnUnknownCost() string {
	if b.Limit.Unit != UnitCost {
		return ""
	}
	if b.OnUnknownCost == "" {
		return OnUnknownCostDeny
	}
	return b.OnUnknownCost
}

// Name is the display name or, without one, the id.
func (b Budget) Name() string {
	if b.DisplayName != "" {
		return b.DisplayName
	}
	return b.ID
}

var anchorDay = [3]int{2024, 1, 1} // a Monday and the first of a month

// Period converts the window to the QuotaPlex period. Rolling windows are not
// supported: QuotaPlex rejects them, because exact rolling accounting needs
// per-event expiry history that its counters do not keep.
func (b Budget) Period() (quotaplex.Period, error) {
	w := b.Window
	switch w.Kind {
	case WindowCalendar:
		if w.Hours != 0 {
			return quotaplex.Period{}, fieldErr("window/hours", "hours applies only to tumbling windows")
		}
		switch w.Unit {
		case "day", "week", "month":
		default:
			return quotaplex.Period{}, fieldErr("window/unit", "a calendar window unit is day, week or month")
		}
		if w.TimeZone == "" || w.TimeZone == "Local" {
			return quotaplex.Period{}, fieldErr("window/time_zone", "a calendar window needs an explicit IANA time_zone")
		}
		loc, err := time.LoadLocation(w.TimeZone)
		if err != nil {
			return quotaplex.Period{}, fieldErr("window/time_zone", "unknown time zone %q", w.TimeZone)
		}
		anchor := time.Date(anchorDay[0], time.Month(anchorDay[1]), anchorDay[2], 0, 0, 0, 0, loc)
		p := quotaplex.Period{Kind: "calendar", Anchor: anchor.UTC(), CalendarUnit: w.Unit, TimeZone: w.TimeZone}
		if err := p.Validate(); err != nil {
			return quotaplex.Period{}, fieldErr("window", "calendar window is not usable in %s: %v", w.TimeZone, err)
		}
		return p, nil
	case WindowTumbling:
		if w.Unit != "" || w.TimeZone != "" {
			return quotaplex.Period{}, fieldErr("window", "a tumbling window has only hours")
		}
		if w.Hours < 1 || w.Hours > MaxHours {
			return quotaplex.Period{}, fieldErr("window/hours", "hours must be 1 to %d", MaxHours)
		}
		return quotaplex.Period{Kind: "anchored", Anchor: time.Date(anchorDay[0], time.Month(anchorDay[1]), anchorDay[2], 0, 0, 0, 0, time.UTC), Duration: time.Duration(w.Hours) * time.Hour}, nil
	case "rolling":
		return quotaplex.Period{}, fieldErr("window/kind", "rolling windows are not supported; use a calendar window or a tumbling window of N hours")
	}
	return quotaplex.Period{}, fieldErr("window/kind", "window.kind must be calendar or tumbling")
}

// ParseAmount converts the exact decimal amount into integer minor units at
// the given scale (cost) or a whole count (scale 0). It refuses an amount with
// more fractional digits than the scale and anything above the int64 range.
func ParseAmount(amount string, scale uint8) (int64, error) {
	if !amountPattern.MatchString(amount) {
		return 0, fmt.Errorf("amount %q is not an exact decimal", amount)
	}
	whole, frac, _ := strings.Cut(amount, ".")
	if len(frac) > int(scale) {
		return 0, fmt.Errorf("amount %q has more decimal places than the meter's scale of %d", amount, scale)
	}
	frac += strings.Repeat("0", int(scale)-len(frac))
	n, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok || !n.IsInt64() || n.Int64() > math.MaxInt64 {
		return 0, fmt.Errorf("amount %q is too large", amount)
	}
	return n.Int64(), nil
}

// FormatAmount renders integer minor units as an exact decimal string.
func FormatAmount(v int64, scale uint8) string {
	neg := v < 0
	n := new(big.Int).SetInt64(v)
	n.Abs(n)
	s := n.String()
	if scale > 0 {
		if len(s) <= int(scale) {
			s = strings.Repeat("0", int(scale)-len(s)+1) + s
		}
		s = s[:len(s)-int(scale)] + "." + s[len(s)-int(scale):]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// Draw converts the budget to the integer draw QuotaPlex counts. scale is the
// scale of the policy's money meter for a cost budget (ignored otherwise).
func (b Budget) Draw(scale uint8) (quotaplex.BudgetDraw, error) {
	period, err := b.Period()
	if err != nil {
		return quotaplex.BudgetDraw{}, err
	}
	d := quotaplex.BudgetDraw{ID: b.ID, Unit: b.Limit.Unit, Period: period, Thresholds: b.EffectiveThresholds()}
	switch b.Limit.Unit {
	case UnitCost:
		d.Currency, d.Scale, d.OnUnknownCost = b.Limit.Currency, scale, b.EffectiveOnUnknownCost()
		d.Limit, err = ParseAmount(b.Limit.Amount, scale)
	default:
		d.Limit, err = ParseAmount(b.Limit.Amount, 0)
	}
	if err != nil {
		return quotaplex.BudgetDraw{}, fieldErr("limit/amount", "%v", err)
	}
	return d, nil
}

// Tree is a validated set of budgets.
type Tree struct {
	byID     map[string]Budget
	children map[string][]string
}

// NewTree validates a tenant's budgets together: parents must exist, there
// must be no cycle, the depth is bounded, and a cost budget's currency must
// equal that of its nearest cost ancestors (no cross-currency roll-up).
func NewTree(all []Budget) (*Tree, error) {
	if len(all) > MaxPerTenant {
		return nil, fmt.Errorf("a tenant has at most %d budgets", MaxPerTenant)
	}
	t := &Tree{byID: map[string]Budget{}, children: map[string][]string{}}
	for _, b := range all {
		if _, dup := t.byID[b.ID]; dup {
			return nil, fieldErr("id", "duplicate budget %q", b.ID)
		}
		t.byID[b.ID] = b
	}
	for _, b := range all {
		if b.Parent == "" {
			continue
		}
		if _, ok := t.byID[b.Parent]; !ok {
			return nil, fieldErr("parent", "parent %q does not exist", b.Parent)
		}
		t.children[b.Parent] = append(t.children[b.Parent], b.ID)
	}
	for id := range t.children {
		sort.Strings(t.children[id])
	}
	for _, b := range all {
		depth, seen := 0, map[string]bool{}
		for cur := b; ; {
			depth++
			if seen[cur.ID] {
				return nil, fieldErr("parent", "budget %q is in a cycle", b.ID)
			}
			seen[cur.ID] = true
			if cur.Parent == "" {
				break
			}
			cur = t.byID[cur.Parent]
		}
		if depth > MaxDepth {
			return nil, fieldErr("parent", "budget %q is %d levels deep; the limit is %d", b.ID, depth, MaxDepth)
		}
		if b.Limit.Unit == UnitCost {
			for cur := b; cur.Parent != ""; {
				cur = t.byID[cur.Parent]
				if cur.Limit.Unit == UnitCost && cur.Limit.Currency != b.Limit.Currency {
					return nil, fieldErr("limit/currency", "cost budget %q is %s but its ancestor %q is %s: budgets do not roll up across currencies", b.ID, b.Limit.Currency, cur.ID, cur.Limit.Currency)
				}
			}
		}
	}
	return t, nil
}

// Get returns a budget by id.
func (t *Tree) Get(id string) (Budget, bool) { b, ok := t.byID[id]; return b, ok }

// All returns every budget ordered by id.
func (t *Tree) All() []Budget {
	out := make([]Budget, 0, len(t.byID))
	for _, b := range t.byID {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Children returns the ids of a budget's children.
func (t *Tree) Children(id string) []string { return append([]string(nil), t.children[id]...) }

// Ancestors returns the parent chain of a budget, nearest first.
func (t *Tree) Ancestors(id string) []Budget {
	var out []Budget
	for cur, ok := t.byID[id]; ok && cur.Parent != ""; {
		cur, ok = t.byID[cur.Parent]
		if ok {
			out = append(out, cur)
		}
	}
	return out
}

// Facts are the verified or trusted properties of one request. None of them
// comes from a header, query or body the caller controls.
type Facts struct {
	Groups        []quotaplex.GroupRef // verified token groups; unknown membership never reaches here
	PrincipalID   string
	AgentIDs      []string
	ClientID      string
	AccessPointID string
	Project       string // the access point's configured project label
}

// Match is one budget a request draws from and why.
type Match struct {
	Budget Budget `json:"-"`
	ID     string `json:"id"`
	// Direct is true when the request matches the budget's own members; false
	// when it draws only because a descendant budget matched.
	Direct bool `json:"direct"`
	// Reasons name the selectors that matched (kind and value) for a direct
	// match; ViaChild names the matching descendant otherwise.
	Reasons  []string `json:"reasons,omitempty"`
	ViaChild string   `json:"via_child,omitempty"`
}

// Reasons lists the selectors of b that match the facts.
func (b Budget) Reasons(f Facts) []string {
	var out []string
	for _, g := range b.Members.Groups {
		for _, have := range f.Groups {
			if g == have {
				out = append(out, "group:"+g.ID)
			}
		}
	}
	has := func(list []string, v string) bool {
		if v == "" {
			return false
		}
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	if has(b.Members.Principals, f.PrincipalID) {
		out = append(out, "principal:"+f.PrincipalID)
	}
	for _, a := range f.AgentIDs {
		if has(b.Members.Agents, a) {
			out = append(out, "agent:"+a)
		}
	}
	if has(b.Members.ClientRegistrations, f.ClientID) {
		out = append(out, "client:"+f.ClientID)
	}
	if has(b.Members.AccessPoints, f.AccessPointID) {
		out = append(out, "access_point:"+f.AccessPointID)
	}
	if has(b.Members.Projects, f.Project) {
		out = append(out, "project:"+f.Project)
	}
	return out
}

// Resolve returns every budget the request draws from: each budget whose own
// members match, and every ancestor of those. The result is ordered leaf-most
// first, then by id, so it is deterministic.
func (t *Tree) Resolve(f Facts) []Match {
	matches := map[string]*Match{}
	for _, b := range t.byID {
		if reasons := b.Reasons(f); len(reasons) > 0 {
			sort.Strings(reasons)
			matches[b.ID] = &Match{Budget: b, ID: b.ID, Direct: true, Reasons: reasons}
		}
	}
	direct := make([]string, 0, len(matches))
	for id := range matches {
		direct = append(direct, id)
	}
	sort.Strings(direct)
	for _, id := range direct {
		for _, anc := range t.Ancestors(id) {
			if m, ok := matches[anc.ID]; ok {
				if !m.Direct && m.ViaChild == "" {
					m.ViaChild = id
				}
				continue
			}
			matches[anc.ID] = &Match{Budget: anc, ID: anc.ID, ViaChild: id}
		}
	}
	out := make([]Match, 0, len(matches))
	for _, m := range matches {
		out = append(out, *m)
	}
	depth := func(id string) int { return len(t.Ancestors(id)) }
	sort.Slice(out, func(i, j int) bool {
		if di, dj := depth(out[i].ID), depth(out[j].ID); di != dj {
			return di > dj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Applies reports whether the budget can be drawn by a request on the plane:
// request budgets count every plane; token budgets count model traffic only;
// cost budgets count model traffic, and tool calls (mcplex) too when they set
// IncludeToolCalls.
func (b Budget) Applies(plane string) bool {
	switch {
	case b.Limit.Unit == UnitRequests:
		return true
	case plane == PlaneModels:
		return true
	case plane == PlaneTools:
		return b.Limit.Unit == UnitCost && b.IncludeToolCalls
	}
	return false
}

// AppliesToDeploymentAction reports whether the budget counts a managed
// deployment action. Deployment actions run on the tool plane, but only
// request budgets (which count every plane) count them: include_tool_calls
// is about tool calls and never charges, or refuses, a deployment.
func (b Budget) AppliesToDeploymentAction() bool {
	return b.Limit.Unit == UnitRequests
}

// Planes a budget may count beyond requests.
const (
	PlaneModels = "llmplex"
	PlaneTools  = "mcplex"
)

// NotApplied explains why the budget does not count a request on the plane,
// or returns "" when it does.
func (b Budget) NotApplied(plane string) string {
	switch {
	case b.Applies(plane):
		return ""
	case plane == PlaneTools && b.Limit.Unit == UnitCost:
		return "this cost budget counts model traffic only; set include_tool_calls to count tool calls"
	case b.Limit.Unit == UnitCost && b.IncludeToolCalls:
		return fmt.Sprintf("cost budgets count model and tool traffic only; this request is on %s", plane)
	}
	return fmt.Sprintf("%s budgets count model traffic only; this request is on %s", b.Limit.Unit, plane)
}

// Contains reports whether the facts make the caller a member of the budget
// or of any budget below it.
func (t *Tree) Contains(id string, f Facts) bool {
	for _, m := range t.Resolve(f) {
		if m.ID == id {
			return true
		}
	}
	return false
}

// DirectMember reports whether the caller matches the budget's own members.
func (t *Tree) DirectMember(id string, f Facts) bool {
	b, ok := t.byID[id]
	return ok && len(b.Reasons(f)) > 0
}
