package models

import "time"

// OperatorAudit records a runtime mutation triggered through AIPlex on a
// Tape-backed run. Distinct from ExecutionEvent: ExecutionEvent is Tape's
// own journal projected for the run timeline; OperatorAudit is AIPlex's
// trail of what an operator did (clicked redrive / cancel / signal / …).
//
// Keeping them in separate collections solves the muddle PR 10 left:
// projection rebuild from Tape's outbox doesn't lose operator history,
// and idempotent (RunID, Seq) semantics on ExecutionEvent stay clean.
//
// Stored append-only — the Console reads them as a parallel timeline.
type OperatorAudit struct {
	ID         string    `json:"id"`
	RunID      string    `json:"run_id"`
	Action     string    `json:"action"`         // redrive / reconcile / cancel / signal / compensate
	Actor      string    `json:"actor"`          // who clicked the button
	At         time.Time `json:"at"`
	Reason     string    `json:"reason,omitempty"`
	GateName   string    `json:"gate_name,omitempty"`
	Resolution string    `json:"resolution,omitempty"`
	Status     string    `json:"status"`         // "accepted" / "failed"
	Error      string    `json:"error,omitempty"`
}
