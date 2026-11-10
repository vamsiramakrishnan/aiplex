// Package usageledger keeps one append-only, tenant-scoped record per managed
// exchange on every plane. Records describe who called what, how it ended, the
// provider-normalized usage that was observed and the cost basis. They never
// contain prompts, completions, tool arguments, results or credentials.
//
// Unknown stays unknown: a usage counter the provider did not report is null,
// never zero, and a cost that neither a quota settlement nor an operator price
// establishes has basis "unknown" and no amount.
package usageledger

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
)

const (
	PlaneLLM = "llmplex"
	PlaneMCP = "mcplex"
	PlaneA2A = "a2aplex"

	StatusOK      = "ok"
	StatusError   = "error"
	StatusDenied  = "denied"
	StatusUnknown = "unknown"

	SourceManaged  = "managed"
	SourceReported = "reported"

	UsageLowerBound = "lower_bound"
	UsageAmbiguous  = "ambiguous"
	UsagePartial    = "partial"
	// UsageBoundNone is a filter value only: records with no usage_bound.
	UsageBoundNone = "none"

	BasisSettled   = "settled"
	BasisEstimated = "estimated"
	BasisUnknown   = "unknown"

	// MaxTextLength bounds every identifier stored on a record.
	MaxTextLength = 512
	// MaxScale bounds fixed-point money precision, matching QuotaPlex meters.
	MaxScale = 18
)

var (
	ErrNotFound = errors.New("usage record not found")
	ErrConflict = errors.New("usage record already exists")
	ErrInvalid  = errors.New("invalid usage record")
)

// Usage holds provider-normalized token counters. A nil counter is unknown;
// a zero counter is a reported zero.
type Usage struct {
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	CacheReadTokens  *int64 `json:"cache_read_tokens"`
	CacheWriteTokens *int64 `json:"cache_write_tokens"`
	ReasoningTokens  *int64 `json:"reasoning_tokens"`
	TotalTokens      *int64 `json:"total_tokens"`
	// UnitUsage holds per-modality token splits and non-token units (see
	// units.go). Each field is omitted when unknown.
	UnitUsage
}

// Counter names usable in group sums and price meters.
const (
	CounterInput      = "input_tokens"
	CounterOutput     = "output_tokens"
	CounterCacheRead  = "cache_read_tokens"
	CounterCacheWrite = "cache_write_tokens"
	CounterReasoning  = "reasoning_tokens"
	CounterTotal      = "total_tokens"
)

// TokenCounters lists the usage counters in their wire order.
var TokenCounters = []string{CounterInput, CounterOutput, CounterCacheRead, CounterCacheWrite, CounterReasoning, CounterTotal}

// Counter returns a counter by wire name; nil means unknown.
func (u *Usage) Counter(name string) *int64 {
	if u == nil {
		return nil
	}
	switch name {
	case CounterInput:
		return u.InputTokens
	case CounterOutput:
		return u.OutputTokens
	case CounterCacheRead:
		return u.CacheReadTokens
	case CounterCacheWrite:
		return u.CacheWriteTokens
	case CounterReasoning:
		return u.ReasoningTokens
	case CounterTotal:
		return u.TotalTokens
	}
	return nil
}

// Known reports whether at least one counter is known.
func (u *Usage) Known() bool {
	if u == nil {
		return false
	}
	for _, name := range TokenCounters {
		if u.Counter(name) != nil {
			return true
		}
	}
	return u.UnitUsage.known()
}

// Cost is a fixed-point monetary amount: Amount minor units at Scale decimal
// places of Currency. Amount is a decimal integer string so totals stay exact
// for every JSON consumer. With basis "unknown" no currency or amount is set.
type Cost struct {
	Basis    string `json:"basis"`
	Currency string `json:"currency,omitempty"`
	Amount   string `json:"amount,omitempty"`
	Scale    uint8  `json:"scale"`
}

// MarshalJSON omits the scale of an unknown cost so it cannot be read as zero.
// A known cost also carries amount_decimal, the same value written as a decimal
// string ("6" at scale 6 is "0.000006"); it is derived from amount and scale,
// never stored, and amount and scale stay authoritative.
func (c Cost) MarshalJSON() ([]byte, error) {
	if c.Basis == BasisUnknown || c.Basis == "" {
		return json.Marshal(struct {
			Basis string `json:"basis"`
		}{BasisUnknown})
	}
	type plain Cost
	decimal, _ := DecimalString(c.Amount, c.Scale)
	return json.Marshal(struct {
		plain
		AmountDecimal string `json:"amount_decimal,omitempty"`
	}{plain(c), decimal})
}

// DecimalString writes a decimal-integer string of minor units at scale decimal
// places exactly, without floating point and without grouping: ("6", 6) is
// "0.000006" and ("250", 2) is "2.50". ok is false when amount is not a
// decimal integer.
func DecimalString(amount string, scale uint8) (string, bool) {
	sign, digits := "", amount
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if digits == "" {
		return "", false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	n := int(scale)
	if n == 0 {
		if digits == "0" {
			sign = ""
		}
		return sign + digits, true
	}
	if len(digits) <= n {
		digits = strings.Repeat("0", n-len(digits)+1) + digits
	}
	whole, frac := digits[:len(digits)-n], digits[len(digits)-n:]
	if whole == "0" && strings.Trim(frac, "0") == "" {
		sign = ""
	}
	return sign + whole + "." + frac, true
}

// UnknownCost is the cost of an exchange no settlement or price establishes.
func UnknownCost() Cost { return Cost{Basis: BasisUnknown} }

// AmountInt parses the fixed-point amount.
func (c Cost) AmountInt() (*big.Int, bool) {
	if c.Amount == "" {
		return nil, false
	}
	value, ok := new(big.Int).SetString(c.Amount, 10)
	if !ok || value.Sign() < 0 || value.String() != c.Amount {
		return nil, false
	}
	return value, true
}

// Record is one managed or reported exchange.
type Record struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	LatencyMS    int64     `json:"latency_ms"`
	Plane        string    `json:"plane"`
	Provider     string    `json:"provider,omitempty"`
	ResourceID   string    `json:"resource_id,omitempty"`
	Operation    string    `json:"operation,omitempty"`
	ModelID      string    `json:"model_id,omitempty"`
	ModelVersion string    `json:"model_version,omitempty"`
	ToolName     string    `json:"tool_name,omitempty"`
	PrincipalID  string    `json:"principal_id,omitempty"`
	// PrincipalDisplay is a human label for the principal (verified email,
	// display name or subject) recorded by the server when the identity was
	// verified. It is for display only; PrincipalID stays the key.
	PrincipalDisplay string `json:"principal_display,omitempty"`
	ActorID          string `json:"actor_id,omitempty"`
	// ActorDisplay is the verified actor subject (an agent), for display only.
	ActorDisplay string `json:"actor_display,omitempty"`
	// ClientID is the verified token's authorized party. A public OAuth client
	// id is attribution, not a security boundary.
	ClientID string `json:"client_id,omitempty"`
	// AccessPointID and CallerKind are set only for exchanges that entered
	// through an access point; CallerKind is agent, user or client.
	AccessPointID string `json:"access_point_id,omitempty"`
	CallerKind    string `json:"caller_kind,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	// TraceID and SpanID are the W3C identifiers of the exchange's span (the
	// CLIENT span of a managed exchange, the SERVER span of the request that
	// reported a reported record). The trace continues the caller's
	// traceparent when it sent a valid one. They are generated whether or not
	// telemetry export is on, so logs, traces and records correlate.
	TraceID        string `json:"trace_id,omitempty"`
	SpanID         string `json:"span_id,omitempty"`
	QuotaAttemptID string `json:"quota_attempt_id,omitempty"`
	Streaming      bool   `json:"streaming"`
	// Session is true for a WebSocket session exchange: one record covers the
	// whole session and its usage is final only with a terminal observation.
	Session    bool   `json:"session,omitempty"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
	Source     string `json:"source"`
	Requests   int64  `json:"requests"`
	Usage      *Usage `json:"usage"`
	// UsageBound qualifies Usage when the exchange ended without final usage
	// evidence: "lower_bound" (the counters are what was observed before the
	// exchange ended), "ambiguous" (a resumed session whose counter
	// composition is unconfirmed) or "partial" (non-token units such as audio
	// seconds were reported but are not representable here). Empty means the
	// counters are the exchange's final reported usage (or unknown when nil).
	// Units the ledger holds (UnitUsage) no longer make a record partial.
	UsageBound string `json:"usage_bound,omitempty"`
	// Batch is true for an exchange that belongs to a provider batch job (its
	// creation, or the results fetch that settled it), so an operator
	// model-price with "batch": true can match it. No discount is built in.
	Batch bool `json:"batch,omitempty"`
	// ImageSize and ImageQuality are the billing dimensions the provider
	// reported for generated images (for example "1024x1024", "high").
	ImageSize    string `json:"image_size,omitempty"`
	ImageQuality string `json:"image_quality,omitempty"`
	Cost         Cost   `json:"cost"`
	// ProviderLimits is the provider rate-limit metadata observed on the
	// managed exchange's native response, when any was reported.
	ProviderLimits *providerlimits.Limits `json:"provider_limits,omitempty"`
	// Hooks summarizes request-hook decisions on the exchange (hooks.go).
	// It never holds content or matched text.
	Hooks *HookSummary `json:"hooks,omitempty"`
	// ToolPolicyRule is the identifier of the tool policy rule that refused
	// the call (error_class tool_policy_denied or tool_policy_error). Empty
	// for a default deny and when no rule decided. Never arguments.
	ToolPolicyRule string `json:"tool_policy_rule,omitempty"`
}

// NewID returns a random record identifier.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "ul_" + hex.EncodeToString(b[:])
}

func validText(v string, required bool) bool {
	if v == "" {
		return !required
	}
	if len(v) > MaxTextLength || !utf8.ValidString(v) {
		return false
	}
	for _, c := range v {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// ValidCurrency accepts three uppercase ASCII letters.
func ValidCurrency(v string) bool {
	if len(v) != 3 {
		return false
	}
	for _, c := range v {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Validate checks the record before it is stored.
func (r Record) Validate() error {
	for name, value := range map[string]string{"id": r.ID, "tenant_id": r.TenantID} {
		if !validText(value, true) {
			return invalid("%s required", name)
		}
	}
	for name, value := range map[string]string{"provider": r.Provider, "resource_id": r.ResourceID, "operation": r.Operation, "model_id": r.ModelID, "model_version": r.ModelVersion, "tool_name": r.ToolName, "principal_id": r.PrincipalID, "principal_display": r.PrincipalDisplay, "actor_id": r.ActorID, "actor_display": r.ActorDisplay, "client_id": r.ClientID, "access_point_id": r.AccessPointID, "request_id": r.RequestID, "trace_id": r.TraceID, "span_id": r.SpanID, "quota_attempt_id": r.QuotaAttemptID, "error_class": r.ErrorClass, "tool_policy_rule": r.ToolPolicyRule, "image_size": r.ImageSize, "image_quality": r.ImageQuality} {
		if !validText(value, false) {
			return invalid("%s is not bounded printable text", name)
		}
	}
	switch r.CallerKind {
	case "", "agent", "user", "client":
	default:
		return invalid("caller_kind must be agent, user or client")
	}
	switch r.UsageBound {
	case "", UsageLowerBound, UsageAmbiguous, UsagePartial:
	default:
		return invalid("usage_bound must be lower_bound, ambiguous or partial")
	}
	switch r.Plane {
	case PlaneLLM, PlaneMCP, PlaneA2A:
	default:
		return invalid("plane must be llmplex, mcplex or a2aplex")
	}
	switch r.Status {
	case StatusOK, StatusError, StatusDenied, StatusUnknown:
	default:
		return invalid("status must be ok, error, denied or unknown")
	}
	switch r.Source {
	case SourceManaged, SourceReported:
	default:
		return invalid("source must be managed or reported")
	}
	if r.StartedAt.IsZero() || r.FinishedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) {
		return invalid("started_at and finished_at required in order")
	}
	if r.LatencyMS < 0 || r.Requests < 0 || r.HTTPStatus < 0 || r.HTTPStatus > 999 {
		return invalid("latency, requests and http_status must be nonnegative")
	}
	if r.Usage != nil {
		for _, name := range TokenCounters {
			if v := r.Usage.Counter(name); v != nil && *v < 0 {
				return invalid("usage counter %s is negative", name)
			}
		}
		if err := r.Usage.UnitUsage.validate(); err != nil {
			return err
		}
	}
	if err := r.Hooks.validate(); err != nil {
		return err
	}
	return r.Cost.Validate()
}

// Validate checks the cost basis and fixed-point amount.
func (c Cost) Validate() error {
	switch c.Basis {
	case BasisUnknown:
		if c.Currency != "" || c.Amount != "" || c.Scale != 0 {
			return invalid("unknown cost cannot carry an amount")
		}
		return nil
	case BasisSettled, BasisEstimated:
		if !ValidCurrency(c.Currency) || c.Scale > MaxScale {
			return invalid("cost needs a three-letter currency and scale up to %d", MaxScale)
		}
		if _, ok := c.AmountInt(); !ok || len(c.Amount) > 64 {
			return invalid("cost amount must be a nonnegative decimal integer")
		}
		return nil
	}
	return invalid("cost basis must be settled, estimated or unknown")
}

// Clone returns a deep copy so stores never share counter pointers.
func (r Record) Clone() Record {
	if r.Usage != nil {
		u := Usage{}
		copyCounter := func(v *int64) *int64 {
			if v == nil {
				return nil
			}
			n := *v
			return &n
		}
		u.InputTokens = copyCounter(r.Usage.InputTokens)
		u.OutputTokens = copyCounter(r.Usage.OutputTokens)
		u.CacheReadTokens = copyCounter(r.Usage.CacheReadTokens)
		u.CacheWriteTokens = copyCounter(r.Usage.CacheWriteTokens)
		u.ReasoningTokens = copyCounter(r.Usage.ReasoningTokens)
		u.TotalTokens = copyCounter(r.Usage.TotalTokens)
		u.UnitUsage = r.Usage.UnitUsage.clone()
		r.Usage = &u
	}
	r.Hooks = cloneHooks(r.Hooks)
	return r
}

// Int64 returns a pointer to v, for building known counters.
func Int64(v int64) *int64 { return &v }
