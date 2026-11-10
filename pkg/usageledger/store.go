package usageledger

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"
)

const (
	DefaultLimit = 100
	MaxLimit     = 1000
	// DefaultRetention bounds how long records are kept by the bundled stores.
	DefaultRetention = 90 * 24 * time.Hour
)

// Filter selects records within one tenant. Empty fields match everything.
// Since is inclusive and Until exclusive, both on StartedAt. Visibility, when
// non-empty, restricts results to records whose principal_id or actor_id is
// one of its subjects; it is set by the server, never by a query parameter.
type Filter struct {
	Plane       string
	Provider    string
	ModelID     string
	ToolName    string
	ActorID     string
	PrincipalID string
	ClientID    string
	// AccessPointID and CallerKind select access-point exchanges.
	AccessPointID string
	CallerKind    string
	// BudgetID selects records that drew from that tenant budget.
	BudgetID string
	Status   string
	Source   string
	// ErrorClass selects records with exactly that error_class.
	ErrorClass string
	// UsageBound selects records by usage_bound: lower_bound, ambiguous or
	// partial, or UsageBoundNone for records whose counters are final (or
	// unknown). Empty matches everything.
	UsageBound string
	// Batch, when non-nil, selects records that are (true) or are not (false)
	// part of a provider batch job. Nil matches everything.
	Batch *bool
	// Harness selects reported records from one harness. Tags selects records
	// whose tags contain every pair.
	Harness string
	Tags    map[string]string
	// HookDecision selects records by hooks.decision (allow, deny,
	// transform, error, skipped), or HookDecisionNone for records no hook
	// ran on. Empty matches everything.
	HookDecision string
	Since        time.Time
	Until        time.Time
	Visibility   []string
}

// Matches applies every filter field, including visibility and the window.
func (f Filter) Matches(r Record) bool {
	for _, c := range [][2]string{{f.Plane, r.Plane}, {f.Provider, r.Provider}, {f.ModelID, r.ModelID}, {f.ToolName, r.ToolName}, {f.ActorID, r.ActorID}, {f.PrincipalID, r.PrincipalID}, {f.ClientID, r.ClientID}, {f.AccessPointID, r.AccessPointID}, {f.CallerKind, r.CallerKind}, {f.Status, r.Status}, {f.Source, r.Source}, {f.ErrorClass, r.ErrorClass}, {f.Harness, r.Harness}} {
		if c[0] != "" && c[0] != c[1] {
			return false
		}
	}
	if f.BudgetID != "" {
		drew := false
		for _, id := range r.BudgetIDs {
			if id == f.BudgetID {
				drew = true
			}
		}
		if !drew {
			return false
		}
	}
	switch f.UsageBound {
	case "":
	case UsageBoundNone:
		if r.UsageBound != "" {
			return false
		}
	default:
		if r.UsageBound != f.UsageBound {
			return false
		}
	}
	for k, v := range f.Tags {
		if got, ok := r.Tags[k]; !ok || got != v {
			return false
		}
	}
	if f.HookDecision != "" && hookDecision(r) != f.HookDecision {
		return false
	}
	if f.Batch != nil && *f.Batch != r.Batch {
		return false
	}
	if !f.Since.IsZero() && r.StartedAt.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !r.StartedAt.Before(f.Until) {
		return false
	}
	if len(f.Visibility) > 0 {
		visible := false
		for _, subject := range f.Visibility {
			if subject != "" && (r.PrincipalID == subject || r.ActorID == subject) {
				visible = true
				break
			}
		}
		if !visible {
			return false
		}
	}
	return true
}

// Query is a filtered page request. Records are ordered newest first by
// (started_at, id); Cursor continues after the last record of a prior page.
type Query struct {
	Filter Filter
	Limit  int
	Cursor string
}

// Page is one page of records. NextCursor is empty on the last page.
type Page struct {
	Records    []Record `json:"records"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// Store persists records. Implementations must scope every operation to the
// tenant argument and must never return another tenant's record.
type Store interface {
	Append(context.Context, Record) error
	Get(ctx context.Context, tenant, id string) (Record, error)
	List(ctx context.Context, tenant string, q Query) (Page, error)
}

// Position is a decoded page cursor.
type Position struct {
	StartedAt time.Time `json:"t"`
	ID        string    `json:"i"`
}

// After reports whether r sorts after the cursor in newest-first order.
func (p Position) After(r Record) bool {
	if r.StartedAt.Equal(p.StartedAt) {
		return r.ID < p.ID
	}
	return r.StartedAt.Before(p.StartedAt)
}

// EncodeCursor returns an opaque cursor for the record.
func EncodeCursor(r Record) string {
	raw, _ := json.Marshal(Position{StartedAt: r.StartedAt.UTC(), ID: r.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor validates an opaque cursor.
func DecodeCursor(cursor string) (Position, error) {
	var p Position
	if len(cursor) > 2048 {
		return p, invalid("cursor is too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || json.Unmarshal(raw, &p) != nil || p.StartedAt.IsZero() || !validText(p.ID, true) {
		return Position{}, invalid("cursor is malformed")
	}
	return p, nil
}

// NormalizeLimit applies the default and maximum page size.
func NormalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultLimit, nil
	}
	if limit < 1 || limit > MaxLimit {
		return 0, invalid("limit must be between 1 and %d", MaxLimit)
	}
	return limit, nil
}

// Less orders records newest first by (started_at, id).
func Less(a, b Record) bool {
	if a.StartedAt.Equal(b.StartedAt) {
		return a.ID > b.ID
	}
	return a.StartedAt.After(b.StartedAt)
}
