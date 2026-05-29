package models

import "time"

// ExecutionEventKind classifies a Tape journal entry surfaced into
// AIPlex audit. Tape writes a small set of `kind` values
// (run/decision/effect/obligation/gate/value/policy) on its journal;
// AIPlex ingests them verbatim and uses this typed enum so the
// run-timeline projection and the Console can switch on a constant
// rather than a string match.
type ExecutionEventKind string

const (
	ExecutionEventRunStarted        ExecutionEventKind = "run.started"
	ExecutionEventRunCompleted      ExecutionEventKind = "run.completed"
	ExecutionEventRunFailed         ExecutionEventKind = "run.failed"
	ExecutionEventDecisionRecorded  ExecutionEventKind = "decision.recorded"
	ExecutionEventEffectBegin       ExecutionEventKind = "effect.begin"
	ExecutionEventEffectConfirmed   ExecutionEventKind = "effect.confirmed"
	ExecutionEventEffectFailed      ExecutionEventKind = "effect.failed"
	ExecutionEventEffectUnknown     ExecutionEventKind = "effect.unknown"
	ExecutionEventEffectDuplicate   ExecutionEventKind = "effect.duplicate"
	ExecutionEventObligationCreated ExecutionEventKind = "obligation.created"
	ExecutionEventGateWaiting       ExecutionEventKind = "gate.waiting"
	ExecutionEventTimerScheduled    ExecutionEventKind = "timer.scheduled"
	ExecutionEventBudgetCharged     ExecutionEventKind = "budget.charged"
	ExecutionEventPolicyViolation   ExecutionEventKind = "policy.violation"
	// PR 13. Tape's compactor reactor emits a kind="run" journal entry
	// with `compacted_at_ms` in the payload when it zeroes the bulky
	// payloads on a settled run. The AIPlexSink (or the run-projection
	// loop) re-stamps it as `run.compacted` so the typed enum below
	// drives projection logic + Console rendering.
	ExecutionEventRunCompacted      ExecutionEventKind = "run.compacted"
)

// ExecutionEvent is one row of Tape's journal projected into AIPlex
// audit storage. Idempotent on (RunID, Seq) — duplicate posts are a
// no-op, out-of-order posts land and the projection catches up.
//
// The shape is what `/internal/tape/events` accepts (the wire contract
// between Tape's outbox relay and AIPlex's ingestion endpoint). See
// docs/integration/aiplex-tape-survey.md §"PR 6" for the rationale.
type ExecutionEvent struct {
	// (RunID, Seq) is the idempotency key for ingestion.
	RunID string `json:"run_id"`
	Seq   int64  `json:"seq"`

	// Identity context, mirrored from RunState. Indexed so the timeline
	// can filter by tenant / agent / actor without a per-row JOIN.
	TenantID         string `json:"tenant_id"`
	AgentID          string `json:"agent_id"`
	Plane            string `json:"plane"`
	Actor            string `json:"actor"`
	Subject          string `json:"subject"`
	AIPlexInstanceID string `json:"aiplex_instance_id,omitempty"`

	// What happened.
	Kind      ExecutionEventKind `json:"kind"`
	Scope     string             `json:"scope,omitempty"`     // populated on effect.* + policy.*
	Tool      string             `json:"tool,omitempty"`      // populated on effect.* + policy.*
	Timestamp time.Time          `json:"timestamp"`

	// Free-form payload — the raw Tape journal payload JSON. Stored as a
	// string so we don't lose precision (Firestore's typed map flattens
	// nested arrays) and so the Console can render it verbatim.
	PayloadJSON string `json:"payload_json,omitempty"`
}

// ExecutionRunStatus mirrors Tape's RunStatus enum (kept as strings here
// because the Console renders them).
type ExecutionRunStatus string

const (
	ExecutionRunRunnable     ExecutionRunStatus = "runnable"
	ExecutionRunRunning      ExecutionRunStatus = "running"
	ExecutionRunWaiting      ExecutionRunStatus = "waiting"
	ExecutionRunTerminal     ExecutionRunStatus = "terminal"
	ExecutionRunFailed       ExecutionRunStatus = "failed"
	ExecutionRunCompensating ExecutionRunStatus = "compensating"
	ExecutionRunStuck        ExecutionRunStatus = "stuck"
	ExecutionRunCancelled    ExecutionRunStatus = "cancelled"
)

// ExecutionRun is the per-run projection AIPlex computes from the
// ingested events. The Console reads from this; the events table is
// the auditable source of truth.
type ExecutionRun struct {
	RunID            string             `json:"run_id"`
	TenantID         string             `json:"tenant_id"`
	AgentID          string             `json:"agent_id"`
	Plane            string             `json:"plane"`
	Actor            string             `json:"actor"`
	Subject          string             `json:"subject"`
	AIPlexInstanceID string             `json:"aiplex_instance_id,omitempty"`
	Status           ExecutionRunStatus `json:"status"`
	StartedAt        time.Time          `json:"started_at"`
	EndedAt          *time.Time         `json:"ended_at,omitempty"`

	// Counters projected from the events list — cheap to compute, expensive
	// to query without (otherwise every list of runs is O(events)).
	DecisionsCount    int64 `json:"decisions_count"`
	EffectsCount      int64 `json:"effects_count"`
	UnknownEffects    int64 `json:"unknown_effects"`
	Obligations       int64 `json:"obligations"`
	PolicyViolations  int64 `json:"policy_violations"`
	BudgetUSDCharged  float64 `json:"budget_usd_charged"`

	// Compaction (PR 13). Tape's compactor reactor zeroes the bulky
	// JSON payloads on settled, retention-aged runs; AIPlex sees the
	// state change via a kind="run" journal entry carrying
	// compacted_at_ms in the payload. The projection mirrors that
	// here so the Console can render compacted runs with a "details
	// archived" badge and disable the live-timeline / operator-action
	// buttons that don't make sense on a compacted run.
	Compacted     bool       `json:"compacted"`
	CompactedAt   *time.Time `json:"compacted_at,omitempty"`
	RetainedUntil *time.Time `json:"retained_until,omitempty"`
}

// QuarantinedExecutionEvent is the kind of row we write when an event
// arrives for an instance AIPlex doesn't know about — typically a Tape
// running before AIPlex caught up, or a misconfigured outbox sink
// pointing at the wrong cluster. Quarantined rows aren't projected;
// operators triage them via the Console.
type QuarantinedExecutionEvent struct {
	ReceivedAt time.Time      `json:"received_at"`
	Reason     string         `json:"reason"`
	Event      ExecutionEvent `json:"event"`
}
