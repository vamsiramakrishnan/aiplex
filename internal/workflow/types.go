// Package workflow implements the workflow plane: cap://workflow/* capabilities
// whose value is a declarative spec that chains other capability invocations.
//
// A workflow is itself a Capability, so it inherits everything caps get for
// free: catalog, deploy, OPA gate, signed receipts, surgical revocation,
// vendor portability. Its body is a list of steps, each of which calls
// another cap (a tool, model, memory operation, agent, or another workflow).
// Step outputs are interpolated into subsequent step inputs via templates.
//
// See design/24-agent-and-workflow-as-cap.md.
package workflow

import "time"

// Spec is the declarative body of a workflow capability. It is stored on the
// Template (and on the Instance once deployed), and executed by the Executor
// when a `cap://workflow/<name>@<v>` invocation arrives.
type Spec struct {
	Inputs  InputSchema  `json:"inputs,omitempty"`
	Steps   []Step       `json:"steps"`
	Outputs OutputSchema `json:"outputs,omitempty"`
	// MaxStepsPerRun bounds runaway workflows. 0 = use kind default (50).
	MaxStepsPerRun int `json:"max_steps_per_run,omitempty"`
}

// InputSchema declares the workflow's input contract. Validation is lightweight
// (presence check on Required); schemas can be JSON Schema for richer checks.
type InputSchema struct {
	Required   []string       `json:"required,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

// OutputSchema is a templated mapping from step outputs to workflow outputs.
type OutputSchema map[string]string // output name → template

// Step is one cap invocation within a workflow.
type Step struct {
	ID     string         `json:"id"`            // unique within the spec
	Cap    string         `json:"cap"`           // cap URI to invoke
	Action string         `json:"action,omitempty"` // defaults to the kind's default action
	Input  map[string]any `json:"input,omitempty"`  // values may contain {{ template }} substitutions

	// OnError controls what happens when this step fails:
	//   "fail" (default) — the run halts and reports the error.
	//   "continue"        — the run records the error and proceeds.
	//   "skip"            — the run records the step as skipped if a previous
	//                       step's output is missing for substitution.
	OnError string `json:"on_error,omitempty"`

	// Timeout per step. 0 = no timeout beyond the surrounding request deadline.
	Timeout time.Duration `json:"timeout,omitempty"`
}

// Run records one execution of a workflow. Persisted (in-memory for now) so
// callers can fetch results, and so receipts have a stable parent ID.
type Run struct {
	ID         string                 `json:"id"`         // wfrun-<hex>
	WorkflowURI string                `json:"workflow_uri"`
	Caller     string                 `json:"caller"`     // sub claim of the invoking token (for audit)
	StartedAt  time.Time              `json:"started_at"`
	FinishedAt time.Time              `json:"finished_at,omitempty"`
	Status     string                 `json:"status"` // running | succeeded | failed | cancelled
	Inputs     map[string]any         `json:"inputs,omitempty"`
	Steps      []StepResult           `json:"steps,omitempty"`
	Outputs    map[string]any         `json:"outputs,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

// StepResult records what one step did. The Output field carries the JSON
// the downstream cap returned, which may be referenced by later steps via
// "{{ steps.<id>.output... }}" templates.
type StepResult struct {
	StepID    string         `json:"step_id"`
	Cap       string         `json:"cap"`
	Action    string         `json:"action"`
	StartedAt time.Time      `json:"started_at"`
	DurationMs int64         `json:"duration_ms"`
	Status    string         `json:"status"` // succeeded | failed | skipped
	Output    map[string]any `json:"output,omitempty"`
	Error     string         `json:"error,omitempty"`
}
