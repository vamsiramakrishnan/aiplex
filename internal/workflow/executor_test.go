package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// stubInvoker records calls and returns canned outputs keyed by cap URI.
type stubInvoker struct {
	mu      sync.Mutex
	calls   []invokeCall
	outputs map[string]map[string]any // capURI → output
	errs    map[string]error
}

type invokeCall struct {
	URI    string
	Action string
	Input  map[string]any
	Token  string
}

func (s *stubInvoker) Invoke(_ context.Context, token, capURI, action string, input map[string]any) (map[string]any, error) {
	s.mu.Lock()
	s.calls = append(s.calls, invokeCall{URI: capURI, Action: action, Input: input, Token: token})
	s.mu.Unlock()
	if err, ok := s.errs[capURI]; ok {
		return nil, err
	}
	if out, ok := s.outputs[capURI]; ok {
		return out, nil
	}
	return map[string]any{}, nil
}

func TestExecutor_SequentialChain(t *testing.T) {
	stub := &stubInvoker{
		outputs: map[string]map[string]any{
			"cap://tool/get_quiz@v1":            {"content": "What is 2+2?", "id": "q-1"},
			"cap://model/gemini-2.5-flash@v1":   {"text": "Score: 95"},
			"cap://memory/students/alice/grades@v1": {"key": "quiz-q-1"},
		},
	}

	exec := NewExecutor(stub, 0)
	exec.Register("cap://workflow/grade-quiz@v1", Spec{
		Inputs: InputSchema{Required: []string{"quiz_id", "student"}},
		Steps: []Step{
			{
				ID:  "fetch",
				Cap: "cap://tool/get_quiz@v1",
				Input: map[string]any{
					"id": "{{ inputs.quiz_id }}",
				},
			},
			{
				ID:  "grade",
				Cap: "cap://model/gemini-2.5-flash@v1",
				Input: map[string]any{
					"prompt": "Grade this quiz: {{ steps.fetch.output.content }}",
				},
			},
			{
				ID:  "store",
				Cap: "cap://memory/students/{{ inputs.student }}/grades@v1",
				Input: map[string]any{
					"key":   "quiz-{{ inputs.quiz_id }}",
					"value": map[string]any{"score": "{{ steps.grade.output.text }}"},
				},
			},
		},
		Outputs: OutputSchema{
			"grade":      "{{ steps.grade.output.text }}",
			"stored_key": "{{ steps.store.output.key }}",
		},
	})

	run, err := exec.Run(context.Background(),
		"test-token",
		"cap://workflow/grade-quiz@v1",
		"alice@school.edu",
		map[string]any{"quiz_id": "q-1", "student": "alice"},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "succeeded" {
		t.Fatalf("Status = %q, want succeeded; error=%s", run.Status, run.Error)
	}
	if len(run.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(run.Steps))
	}

	// Check the cap URI got templated.
	if got := stub.calls[2].URI; got != "cap://memory/students/alice/grades@v1" {
		t.Errorf("step 3 URI = %q, want template substituted", got)
	}

	// Check input templating: step 2's prompt should reference step 1's output.
	prompt, _ := stub.calls[1].Input["prompt"].(string)
	if prompt != "Grade this quiz: What is 2+2?" {
		t.Errorf("step 2 prompt = %q", prompt)
	}

	// Check token threading: every step gets the original token.
	for i, c := range stub.calls {
		if c.Token != "test-token" {
			t.Errorf("step %d Token = %q, want test-token", i, c.Token)
		}
	}

	if run.Outputs["grade"] != "Score: 95" {
		t.Errorf("Outputs[grade] = %v", run.Outputs["grade"])
	}
}

func TestExecutor_FailFast(t *testing.T) {
	stub := &stubInvoker{
		errs: map[string]error{
			"cap://tool/broken@v1": errors.New("backend exploded"),
		},
	}

	exec := NewExecutor(stub, 0)
	exec.Register("cap://workflow/wf@v1", Spec{
		Steps: []Step{
			{ID: "a", Cap: "cap://tool/broken@v1"},
			{ID: "b", Cap: "cap://tool/never_called@v1"},
		},
	})
	run, _ := exec.Run(context.Background(), "", "cap://workflow/wf@v1", "", nil)

	if run.Status != "failed" {
		t.Errorf("Status = %q, want failed", run.Status)
	}
	if len(run.Steps) != 1 {
		t.Errorf("expected only step a to run, got %d steps", len(run.Steps))
	}
	if !strings.Contains(run.Error, "step \"a\" failed") {
		t.Errorf("Error = %q", run.Error)
	}
	if len(stub.calls) != 1 {
		t.Errorf("expected 1 call (b should not be invoked), got %d", len(stub.calls))
	}
}

func TestExecutor_OnErrorContinue(t *testing.T) {
	stub := &stubInvoker{
		errs: map[string]error{
			"cap://tool/broken@v1": errors.New("transient"),
		},
		outputs: map[string]map[string]any{
			"cap://tool/ok@v1": {"x": 1},
		},
	}

	exec := NewExecutor(stub, 0)
	exec.Register("cap://workflow/wf@v1", Spec{
		Steps: []Step{
			{ID: "a", Cap: "cap://tool/broken@v1", OnError: "continue"},
			{ID: "b", Cap: "cap://tool/ok@v1"},
		},
	})
	run, _ := exec.Run(context.Background(), "", "cap://workflow/wf@v1", "", nil)

	if run.Status != "succeeded" {
		t.Errorf("Status = %q, want succeeded (a continues on error)", run.Status)
	}
	if len(run.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(run.Steps))
	}
	if run.Steps[0].Status != "failed" {
		t.Errorf("step a Status = %q", run.Steps[0].Status)
	}
	if run.Steps[1].Status != "succeeded" {
		t.Errorf("step b Status = %q", run.Steps[1].Status)
	}
}

func TestExecutor_MissingInput(t *testing.T) {
	exec := NewExecutor(&stubInvoker{}, 0)
	exec.Register("cap://workflow/wf@v1", Spec{
		Inputs: InputSchema{Required: []string{"required_thing"}},
		Steps:  []Step{{ID: "x", Cap: "cap://tool/x@v1"}},
	})
	_, err := exec.Run(context.Background(), "", "cap://workflow/wf@v1", "", nil)
	if err == nil || !strings.Contains(err.Error(), "required_thing") {
		t.Errorf("expected required-input error, got %v", err)
	}
}

func TestExecutor_StepLimit(t *testing.T) {
	exec := NewExecutor(&stubInvoker{}, 2)
	exec.Register("cap://workflow/big@v1", Spec{
		Steps: []Step{
			{ID: "a", Cap: "cap://tool/a@v1"},
			{ID: "b", Cap: "cap://tool/b@v1"},
			{ID: "c", Cap: "cap://tool/c@v1"},
		},
	})
	_, err := exec.Run(context.Background(), "", "cap://workflow/big@v1", "", nil)
	if err == nil || !strings.Contains(err.Error(), "maximum is 2") {
		t.Errorf("expected step-limit error, got %v", err)
	}
}

func TestRenderString(t *testing.T) {
	ctx := map[string]any{
		"inputs": map[string]any{
			"name": "alice",
			"id":   42,
		},
		"steps": map[string]any{
			"fetch": map[string]any{
				"output": map[string]any{
					"items": []any{
						map[string]any{"label": "first"},
						map[string]any{"label": "second"},
					},
				},
			},
		},
	}

	cases := []struct {
		in     string
		want   string
		missing int
	}{
		{"hello {{ inputs.name }}", "hello alice", 0},
		{"id={{inputs.id}}", "id=42", 0},
		{"{{ steps.fetch.output.items[1].label }}", "second", 0},
		{"{{ inputs.unknown }}", "", 1},
	}
	for _, c := range cases {
		got, missing := renderString(c.in, ctx)
		if got != c.want {
			t.Errorf("renderString(%q) = %q, want %q", c.in, got, c.want)
		}
		if len(missing) != c.missing {
			t.Errorf("renderString(%q) missing = %v, want count %d", c.in, missing, c.missing)
		}
	}
}

func TestRenderValue_TypePreservation(t *testing.T) {
	ctx := map[string]any{
		"inputs": map[string]any{"obj": map[string]any{"k": 1}},
	}
	out, missing := renderValue("{{ inputs.obj }}", ctx)
	if len(missing) != 0 {
		t.Fatalf("unexpected missing: %v", missing)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map preserved, got %T (%v)", out, out)
	}
	if m["k"] != 1 {
		t.Errorf("preserved value = %v", m)
	}
}
