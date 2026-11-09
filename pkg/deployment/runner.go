package deployment

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// ExecRunner is a bounded argv runner. It is not a sandbox: install Anvil and
// its dependencies read-only and grant its workload only the intended authority.
type ExecRunner struct{}
type outputBudget struct {
	mu        sync.Mutex
	remaining int
	overflow  bool
	cancel    context.CancelFunc
}
type boundedOutput struct {
	budget *outputBudget
	buffer bytes.Buffer
}

func (w *boundedOutput) Write(b []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	n := len(b)
	keep := n
	if keep > w.budget.remaining {
		keep = w.budget.remaining
		w.budget.overflow = true
	}
	if keep > 0 {
		_, _ = w.buffer.Write(b[:keep])
		w.budget.remaining -= keep
	}
	if keep < n {
		w.budget.cancel()
	}
	return n, nil
}
func (ExecRunner) Run(ctx context.Context, in Command) (ProcessResult, error) {
	call, cancel := context.WithCancel(ctx)
	defer cancel()
	budget := &outputBudget{remaining: in.OutputLimit, cancel: cancel}
	if in.OutputLimit < 1 {
		return ProcessResult{}, fmt.Errorf("output limit required")
	}
	stdout := &boundedOutput{budget: budget}
	stderr := &boundedOutput{budget: budget}
	command := exec.CommandContext(call, in.Executable, in.Args...)
	command.Env = append([]string(nil), in.Env...)
	command.Dir = in.Directory
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = time.Second
	configureProcess(command)
	if e := command.Start(); e != nil {
		return ProcessResult{ExitCode: -1}, e
	}
	e := command.Wait()
	result := ProcessResult{Stdout: append([]byte(nil), stdout.buffer.Bytes()...), Stderr: append([]byte(nil), stderr.buffer.Bytes()...), ExitCode: 0, Started: true, Interrupted: call.Err() != nil || budget.overflow}
	if e != nil {
		result.ExitCode = -1
		if exit, ok := e.(*exec.ExitError); ok {
			result.ExitCode = exit.ExitCode()
		}
	}
	if budget.overflow {
		return result, fmt.Errorf("Anvil output limit exceeded")
	}
	if call.Err() != nil {
		return result, call.Err()
	}
	return result, e
}
