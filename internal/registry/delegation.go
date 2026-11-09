package registry

import (
	"context"
	"errors"
	"sort"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

var ErrAlreadyExists = errors.New("resource already exists")
var ErrConflict = errors.New("resource state changed or transition is invalid")

func (m *MemoryStore) ListDelegationTrace(_ context.Context, traceID string, limit int) ([]models.Delegation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.Delegation, 0)
	for _, d := range m.delegations {
		if d.TraceID == traceID {
			out = append(out, *cloneDelegation(d))
		}
	}
	sortDelegations(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func cloneDelegation(d *models.Delegation) *models.Delegation {
	if d == nil {
		return nil
	}
	copy := *d
	if d.CompletedAt != nil {
		at := *d.CompletedAt
		copy.CompletedAt = &at
	}
	return &copy
}
func sortDelegations(v []models.Delegation) {
	sort.SliceStable(v, func(i, j int) bool {
		if v[i].StartedAt.Equal(v[j].StartedAt) {
			return v[i].ID < v[j].ID
		}
		return v[i].StartedAt.After(v[j].StartedAt)
	})
}
func sortSkillInvocations(v []models.SkillInvocation) {
	sort.SliceStable(v, func(i, j int) bool {
		if v[i].StartedAt.Equal(v[j].StartedAt) {
			return v[i].ID < v[j].ID
		}
		return v[i].StartedAt.After(v[j].StartedAt)
	})
}

// Check while holding the memory lock or a Firestore read/write transaction.
// A second completion must not overwrite the first completion's timestamp,
// result or duration. Reported identities and trace topology are immutable.
func delegationTransition(previous, next *models.Delegation) error {
	if previous.Status == "completed" || previous.Status == "failed" {
		return ErrConflict
	}
	if next.Status != "pending" && next.Status != "running" && next.Status != "completed" && next.Status != "failed" {
		return ErrConflict
	}
	if previous.Status == "running" && next.Status == "pending" {
		return ErrConflict
	}
	if previous.ID != next.ID || previous.CallerAgentID != next.CallerAgentID || previous.CalleeAgentID != next.CalleeAgentID || previous.CallerInstanceID != next.CallerInstanceID || previous.CalleeInstanceID != next.CalleeInstanceID || previous.TaskType != next.TaskType || previous.UserID != next.UserID || previous.ParentID != next.ParentID || previous.TraceID != next.TraceID || previous.SpanID != next.SpanID || previous.ParentSpanID != next.ParentSpanID || !previous.StartedAt.Equal(next.StartedAt) || previous.ReportedBy != next.ReportedBy || !previous.ReportedAt.Equal(next.ReportedAt) {
		return ErrConflict
	}
	return nil
}
