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
	return false
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
func (c Cost) MarshalJSON() ([]byte, error) {
	if c.Basis == BasisUnknown || c.Basis == "" {
		return json.Marshal(struct {
			Basis string `json:"basis"`
		}{BasisUnknown})
	}
	type plain Cost
	return json.Marshal(plain(c))
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
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	LatencyMS      int64     `json:"latency_ms"`
	Plane          string    `json:"plane"`
	Provider       string    `json:"provider,omitempty"`
	ResourceID     string    `json:"resource_id,omitempty"`
	Operation      string    `json:"operation,omitempty"`
	ModelID        string    `json:"model_id,omitempty"`
	ModelVersion   string    `json:"model_version,omitempty"`
	ToolName       string    `json:"tool_name,omitempty"`
	PrincipalID    string    `json:"principal_id,omitempty"`
	ActorID        string    `json:"actor_id,omitempty"`
	ClientID       string    `json:"client_id,omitempty"`
	RequestID      string    `json:"request_id,omitempty"`
	TraceID        string    `json:"trace_id,omitempty"`
	QuotaAttemptID string    `json:"quota_attempt_id,omitempty"`
	Streaming      bool      `json:"streaming"`
	Status         string    `json:"status"`
	HTTPStatus     int       `json:"http_status,omitempty"`
	ErrorClass     string    `json:"error_class,omitempty"`
	Source         string    `json:"source"`
	Requests       int64     `json:"requests"`
	Usage          *Usage    `json:"usage"`
	Cost           Cost      `json:"cost"`
	// ProviderLimits is the provider rate-limit metadata observed on the
	// managed exchange's native response, when any was reported.
	ProviderLimits *providerlimits.Limits `json:"provider_limits,omitempty"`
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
	for name, value := range map[string]string{"provider": r.Provider, "resource_id": r.ResourceID, "operation": r.Operation, "model_id": r.ModelID, "model_version": r.ModelVersion, "tool_name": r.ToolName, "principal_id": r.PrincipalID, "actor_id": r.ActorID, "client_id": r.ClientID, "request_id": r.RequestID, "trace_id": r.TraceID, "quota_attempt_id": r.QuotaAttemptID, "error_class": r.ErrorClass} {
		if !validText(value, false) {
			return invalid("%s is not bounded printable text", name)
		}
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
		r.Usage = &u
	}
	return r
}

// Int64 returns a pointer to v, for building known counters.
func Int64(v int64) *int64 { return &v }
