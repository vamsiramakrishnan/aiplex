package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// fakeCompactor records the runs it was asked to compact so the test
// can assert that the reactor honoured per-instance retention policies
// (only the ones older than compact_after_days should land here).
type fakeCompactor struct {
	mu    sync.Mutex
	calls []string
	fail  bool
}

func (f *fakeCompactor) CompactRun(_ context.Context, runID string) (TapeCompactResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, runID)
	if f.fail {
		return TapeCompactResult{}, errCompactFail
	}
	return TapeCompactResult{DecisionsZeroed: 3, EffectsZeroed: 2, BytesSaved: 4096}, nil
}

func (f *fakeCompactor) called(runID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == runID {
			return true
		}
	}
	return false
}

var errCompactFail = &compactError{msg: "tape compact_run failed"}

type compactError struct{ msg string }

func (e *compactError) Error() string { return e.msg }

func newRetentionStore(t *testing.T) registry.Store {
	t.Helper()
	return registry.NewMemoryStore()
}

func seedInstance(t *testing.T, store registry.Store, id string, retention models.RuntimeRetention) {
	t.Helper()
	inst := &models.Instance{
		ID:         id,
		Plane:      models.PlaneA2APlex,
		TemplateID: "tmpl-" + id,
		Owner:      "owner@example.com",
		Namespace:  "a2aplex",
		Scopes:     []string{"a2a:task:research"},
		Status:     models.StatusRunning,
		Runtime: models.RuntimeConfig{
			Engine:    models.RuntimeEngineTape,
			Retention: retention,
		},
	}
	if err := store.PutInstance(context.Background(), inst); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
}

func seedRun(t *testing.T, store registry.Store, runID, instanceID string, status models.ExecutionRunStatus, endedAgo time.Duration) {
	t.Helper()
	ended := time.Now().Add(-endedAgo)
	run := &models.ExecutionRun{
		RunID:            runID,
		TenantID:         "t-1",
		AgentID:          "agent-1",
		Plane:            "a2aplex",
		AIPlexInstanceID: instanceID,
		Status:           status,
		StartedAt:        ended.Add(-time.Minute),
		EndedAt:          &ended,
	}
	if err := store.UpsertExecutionRun(context.Background(), run); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

func TestRetentionReactor_CompactsOnlySettledRunsOlderThanPolicy(t *testing.T) {
	store := newRetentionStore(t)
	seedInstance(t, store, "inst-strict", models.RuntimeRetention{
		HotDays:          1,
		CompactAfterDays: 7,
		DeleteAfterDays:  30,
		DeleteProjection: false,
	})
	seedInstance(t, store, "inst-loose", models.RuntimeRetention{
		HotDays:          7,
		CompactAfterDays: 90,
		DeleteAfterDays:  365,
		DeleteProjection: false,
	})

	// strict: terminal, 10 days old → compact
	seedRun(t, store, "run-strict-old", "inst-strict", models.ExecutionRunTerminal, 10*24*time.Hour)
	// strict: terminal, 1 day old → too young
	seedRun(t, store, "run-strict-fresh", "inst-strict", models.ExecutionRunTerminal, 24*time.Hour)
	// strict: still running, 30 days old → skip (non-terminal)
	seedRun(t, store, "run-strict-active", "inst-strict", models.ExecutionRunRunning, 30*24*time.Hour)
	// loose: terminal, 10 days old → too young (policy says 90)
	seedRun(t, store, "run-loose-mid", "inst-loose", models.ExecutionRunTerminal, 10*24*time.Hour)
	// loose: terminal, 200 days old → compact
	seedRun(t, store, "run-loose-old", "inst-loose", models.ExecutionRunFailed, 200*24*time.Hour)

	compactor := &fakeCompactor{}
	reactor := NewRetentionReactor(store, compactor, time.Hour)
	reactor.compactSettledRuns(context.Background())

	if !compactor.called("run-strict-old") {
		t.Errorf("expected run-strict-old to be compacted")
	}
	if !compactor.called("run-loose-old") {
		t.Errorf("expected run-loose-old to be compacted")
	}
	if compactor.called("run-strict-fresh") {
		t.Errorf("run-strict-fresh is younger than policy, should not be compacted")
	}
	if compactor.called("run-strict-active") {
		t.Errorf("run-strict-active is non-terminal, should not be compacted")
	}
	if compactor.called("run-loose-mid") {
		t.Errorf("run-loose-mid is younger than loose policy, should not be compacted")
	}

	// Projection should be stamped.
	got, err := store.GetExecutionRun(context.Background(), "run-strict-old")
	if err != nil {
		t.Fatalf("get run-strict-old: %v", err)
	}
	if !got.Compacted {
		t.Errorf("run-strict-old projection not marked Compacted")
	}
	if got.CompactedAt == nil {
		t.Errorf("run-strict-old projection missing CompactedAt timestamp")
	}
}

func TestRetentionReactor_SkipsAlreadyCompacted(t *testing.T) {
	store := newRetentionStore(t)
	seedInstance(t, store, "inst-1", models.RuntimeRetention{
		HotDays:          1,
		CompactAfterDays: 3,
		DeleteAfterDays:  30,
		DeleteProjection: false,
	})
	// Settled, old, but already compacted.
	now := time.Now()
	stamp := now.Add(-2 * 24 * time.Hour)
	ended := now.Add(-10 * 24 * time.Hour)
	run := &models.ExecutionRun{
		RunID:            "run-done",
		AIPlexInstanceID: "inst-1",
		Status:           models.ExecutionRunTerminal,
		EndedAt:          &ended,
		Compacted:        true,
		CompactedAt:      &stamp,
	}
	if err := store.UpsertExecutionRun(context.Background(), run); err != nil {
		t.Fatalf("seed: %v", err)
	}

	compactor := &fakeCompactor{}
	reactor := NewRetentionReactor(store, compactor, time.Hour)
	reactor.compactSettledRuns(context.Background())

	if compactor.called("run-done") {
		t.Errorf("already-compacted run should be skipped")
	}
}

func TestRetentionReactor_CompactorErrorDoesntStampProjection(t *testing.T) {
	store := newRetentionStore(t)
	seedInstance(t, store, "inst-1", models.RuntimeRetention{
		HotDays:          1,
		CompactAfterDays: 3,
		DeleteAfterDays:  30,
		DeleteProjection: false,
	})
	seedRun(t, store, "run-broken", "inst-1", models.ExecutionRunTerminal, 10*24*time.Hour)

	compactor := &fakeCompactor{fail: true}
	reactor := NewRetentionReactor(store, compactor, time.Hour)
	reactor.compactSettledRuns(context.Background())

	got, err := store.GetExecutionRun(context.Background(), "run-broken")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Compacted {
		t.Errorf("compactor failed, projection should not be stamped")
	}
}

func TestRetentionReactor_PurgeStampsRetainedUntilByDefault(t *testing.T) {
	store := newRetentionStore(t)
	seedInstance(t, store, "inst-1", models.RuntimeRetention{
		HotDays:          1,
		CompactAfterDays: 3,
		DeleteAfterDays:  30,
		DeleteProjection: false,
	})
	seedRun(t, store, "run-purge", "inst-1", models.ExecutionRunTerminal, 60*24*time.Hour)
	// Need at least one event for the purge path to flag RetainedUntil.
	if _, err := store.AppendExecutionEvent(context.Background(), &models.ExecutionEvent{
		RunID:            "run-purge",
		Seq:              1,
		AIPlexInstanceID: "inst-1",
		Kind:             models.ExecutionEventRunStarted,
		Timestamp:        time.Now().Add(-60 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	reactor := NewRetentionReactor(store, &fakeCompactor{}, time.Hour)
	reactor.purgeExpiredEvents(context.Background())

	got, err := store.GetExecutionRun(context.Background(), "run-purge")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RetainedUntil == nil {
		t.Errorf("expected RetainedUntil to be stamped on purged run")
	}
}

func TestRetentionReactor_PurgeDeletesProjectionWhenDeleteRequested(t *testing.T) {
	store := newRetentionStore(t)
	seedInstance(t, store, "inst-strict", models.RuntimeRetention{
		HotDays:          1,
		CompactAfterDays: 3,
		DeleteAfterDays:  30,
		DeleteProjection: true,
	})
	seedRun(t, store, "run-erase", "inst-strict", models.ExecutionRunTerminal, 60*24*time.Hour)
	if _, err := store.AppendExecutionEvent(context.Background(), &models.ExecutionEvent{
		RunID:            "run-erase",
		Seq:              1,
		AIPlexInstanceID: "inst-strict",
		Kind:             models.ExecutionEventRunStarted,
		Timestamp:        time.Now().Add(-60 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	reactor := NewRetentionReactor(store, &fakeCompactor{}, time.Hour)
	reactor.purgeExpiredEvents(context.Background())

	if _, err := store.GetExecutionRun(context.Background(), "run-erase"); err == nil {
		t.Errorf("expected projection to be deleted when DeleteProjection=true")
	}
}

func TestIsTerminal(t *testing.T) {
	terminal := []models.ExecutionRunStatus{
		models.ExecutionRunTerminal,
		models.ExecutionRunFailed,
		models.ExecutionRunCancelled,
		models.ExecutionRunStuck,
	}
	for _, s := range terminal {
		if !isTerminal(s) {
			t.Errorf("expected %s to be terminal", s)
		}
	}
	notTerminal := []models.ExecutionRunStatus{
		models.ExecutionRunRunnable,
		models.ExecutionRunRunning,
		models.ExecutionRunWaiting,
		models.ExecutionRunCompensating,
	}
	for _, s := range notTerminal {
		if isTerminal(s) {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}
