package workflow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// CapInvoker abstracts "make a cap call." The default implementation hits
// the AIPlex gateway over HTTP; tests inject a stub that records calls.
//
// The token threaded into ctx (via capability.WithBearer or the request
// headers) is forwarded so downstream caps see the original delegation
// chain — this is what makes a workflow's audit trail one connected
// receipt sequence rather than a forest of orphan invocations.
type CapInvoker interface {
	Invoke(ctx context.Context, token string, capURI, action string, input map[string]any) (map[string]any, error)
}

// Executor runs workflow specs. One per process; concurrent runs are safe.
type Executor struct {
	mu        sync.RWMutex
	specs     map[string]Spec // workflow URI → spec
	invoker   CapInvoker
	maxSteps  int

	runsMu sync.RWMutex
	runs   map[string]*Run
}

// NewExecutor creates an executor with the given cap invoker. If maxSteps is
// 0 it defaults to 50. Specs are registered via Register.
func NewExecutor(invoker CapInvoker, maxSteps int) *Executor {
	if maxSteps <= 0 {
		maxSteps = 50
	}
	return &Executor{
		specs:    make(map[string]Spec),
		invoker:  invoker,
		maxSteps: maxSteps,
		runs:     make(map[string]*Run),
	}
}

// Register binds a workflow URI to its spec. Called by the deploy hook.
func (e *Executor) Register(uri string, spec Spec) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.specs[uri] = spec
}

// Unregister removes a workflow.
func (e *Executor) Unregister(uri string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.specs, uri)
}

// HasSpec reports whether a workflow URI is registered (handy for tests).
func (e *Executor) HasSpec(uri string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.specs[uri]
	return ok
}

// GetRun returns a previously-recorded run, or nil if absent.
func (e *Executor) GetRun(id string) *Run {
	e.runsMu.RLock()
	defer e.runsMu.RUnlock()
	return e.runs[id]
}

// Run executes the workflow at uri with the given inputs and returns the
// completed Run record. The caller's bearer token (if any) is threaded
// through every step invocation via the CapInvoker.
func (e *Executor) Run(ctx context.Context, token, uri, caller string, inputs map[string]any) (*Run, error) {
	e.mu.RLock()
	spec, ok := e.specs[uri]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("workflow %q not registered", uri)
	}

	if err := validateInputs(spec.Inputs, inputs); err != nil {
		return nil, err
	}

	maxSteps := e.maxSteps
	if spec.MaxStepsPerRun > 0 && spec.MaxStepsPerRun < maxSteps {
		maxSteps = spec.MaxStepsPerRun
	}
	if len(spec.Steps) > maxSteps {
		return nil, fmt.Errorf("workflow has %d steps; maximum is %d",
			len(spec.Steps), maxSteps)
	}

	run := &Run{
		ID:          "wfrun-" + randHex(8),
		WorkflowURI: uri,
		Caller:      caller,
		StartedAt:   time.Now(),
		Status:      "running",
		Inputs:      inputs,
	}
	e.recordRun(run)

	tplCtx := map[string]any{
		"inputs": inputs,
		"steps":  map[string]any{},
	}

	for _, step := range spec.Steps {
		select {
		case <-ctx.Done():
			run.Status = "cancelled"
			run.Error = ctx.Err().Error()
			run.FinishedAt = time.Now()
			e.recordRun(run)
			return run, ctx.Err()
		default:
		}

		result := e.runStep(ctx, token, step, tplCtx)
		run.Steps = append(run.Steps, result)
		// Make the result available for subsequent step templates.
		tplCtx["steps"].(map[string]any)[step.ID] = map[string]any{
			"output": result.Output,
			"status": result.Status,
		}

		if result.Status == "failed" && step.OnError != "continue" {
			run.Status = "failed"
			run.Error = fmt.Sprintf("step %q failed: %s", step.ID, result.Error)
			run.FinishedAt = time.Now()
			e.recordRun(run)
			return run, nil // failure is recorded on the Run, not returned as Go error
		}
	}

	// Render workflow outputs.
	outputs := make(map[string]any, len(spec.Outputs))
	for name, tmpl := range spec.Outputs {
		rendered, missing := renderString(tmpl, tplCtx)
		if len(missing) > 0 {
			outputs[name] = nil
		} else {
			outputs[name] = rendered
		}
	}
	run.Outputs = outputs
	run.Status = "succeeded"
	run.FinishedAt = time.Now()
	e.recordRun(run)
	return run, nil
}

func (e *Executor) recordRun(r *Run) {
	e.runsMu.Lock()
	defer e.runsMu.Unlock()
	e.runs[r.ID] = r
}

// runStep evaluates one step, calling the cap and recording timing/output.
func (e *Executor) runStep(ctx context.Context, token string, step Step, tplCtx map[string]any) StepResult {
	res := StepResult{
		StepID:    step.ID,
		Cap:       step.Cap,
		Action:    step.Action,
		StartedAt: time.Now(),
	}

	// Render the cap URI itself — it may contain template variables
	// (e.g. cap://memory/students/{{ inputs.student }}/grades@v1).
	capURI, missingURI := renderString(step.Cap, tplCtx)
	if len(missingURI) > 0 {
		res.Status = "skipped"
		res.Error = fmt.Sprintf("cap URI references missing values: %v", missingURI)
		res.DurationMs = time.Since(res.StartedAt).Milliseconds()
		return res
	}
	res.Cap = capURI

	// Default the action to the kind's default if not specified.
	if res.Action == "" {
		if u, err := capability.ParseURI(capURI); err == nil {
			if spec, ok := capability.Spec(u.Kind); ok {
				res.Action = spec.DefaultAction
			}
		}
	}

	// Render inputs.
	rendered, missingInputs := renderValue(step.Input, tplCtx)
	if len(missingInputs) > 0 && step.OnError != "continue" {
		res.Status = "failed"
		res.Error = fmt.Sprintf("input templates failed to resolve: %v", missingInputs)
		res.DurationMs = time.Since(res.StartedAt).Milliseconds()
		return res
	}
	inputMap, _ := rendered.(map[string]any)
	if inputMap == nil {
		inputMap = map[string]any{}
	}

	stepCtx := ctx
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, step.Timeout)
		defer cancel()
	}

	output, err := e.invoker.Invoke(stepCtx, token, capURI, res.Action, inputMap)
	res.DurationMs = time.Since(res.StartedAt).Milliseconds()
	if err != nil {
		res.Status = "failed"
		res.Error = err.Error()
		return res
	}
	res.Status = "succeeded"
	res.Output = output
	return res
}

// validateInputs is a lightweight presence check on Required keys. JSON Schema
// validation is a future addition.
func validateInputs(schema InputSchema, inputs map[string]any) error {
	for _, key := range schema.Required {
		if _, ok := inputs[key]; !ok {
			return fmt.Errorf("missing required input %q", key)
		}
	}
	return nil
}

// HTTPInvoker calls caps via the AIPlex gateway over HTTP. It posts to
// /cap/<kind>/<name>@<version>/_invoke with a JSON body containing the
// action and input. The gateway routes to the right backend; ext_authz
// validates the bearer; receipts get emitted along the way.
//
// The gateway URL is the in-cluster gateway service. For tests, point it
// at the test httptest.Server.
type HTTPInvoker struct {
	GatewayURL string
	HTTPClient *http.Client
}

// NewHTTPInvoker creates an invoker that calls the gateway at gatewayURL.
func NewHTTPInvoker(gatewayURL string) *HTTPInvoker {
	return &HTTPInvoker{
		GatewayURL: strings.TrimRight(gatewayURL, "/"),
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Invoke implements CapInvoker.
func (h *HTTPInvoker) Invoke(ctx context.Context, token, capURI, action string, input map[string]any) (map[string]any, error) {
	u, err := capability.ParseURI(capURI)
	if err != nil {
		return nil, fmt.Errorf("parse cap URI: %w", err)
	}

	body := map[string]any{"action": action, "input": input}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := h.GatewayURL + "/cap/" + u.PathSegment() + "/_invoke"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("invoke %s: %w", capURI, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var errBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		msg := fmt.Sprintf("status %d", resp.StatusCode)
		if m, ok := errBody["message"].(string); ok {
			msg = m
		}
		return nil, fmt.Errorf("invoke %s: %s", capURI, msg)
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		// Empty body or non-JSON; not fatal.
		return map[string]any{}, nil
	}
	return out, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
