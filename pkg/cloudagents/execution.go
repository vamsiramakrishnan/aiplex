package cloudagents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// ExecutionRequest is a private snapshot after native request validation and
// authorization, before credential acquisition. It describes the managed HTTP
// exchange, never the opaque agent's internal model/tool work.
type ExecutionRequest struct {
	Context                contracts.RequestContext
	Connection             Connection
	Invocation             Invocation
	Method, Path, RawQuery string
	Body                   []byte
	Headers                http.Header
}
type ExecutionPreparer interface {
	Prepare(context.Context, ExecutionRequest) (execution.Lease, error)
}
type ExecutionPreparerFunc func(context.Context, ExecutionRequest) (execution.Lease, error)

func (f ExecutionPreparerFunc) Prepare(ctx context.Context, in ExecutionRequest) (execution.Lease, error) {
	return f(ctx, in)
}
func WithExecutionPreparer(prepare ExecutionPreparer) Option {
	return func(v *Invoker) { v.execution = prepare }
}
func cloneExecutionContext(in contracts.RequestContext) contracts.RequestContext {
	if in.MembershipSnapshotRef != nil {
		ref := *in.MembershipSnapshotRef
		in.MembershipSnapshotRef = &ref
	}
	return in
}
func executionDurabilityContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

type agentExecution struct {
	ctx               context.Context
	lease             execution.Lease
	claimed, finished bool
	status            int
}

func (r *agentExecution) finish(complete bool, cause error) error {
	if r == nil || r.finished {
		return nil
	}
	r.finished = true
	ctx, cancel := executionDurabilityContext(r.ctx)
	defer cancel()
	if !r.claimed {
		return r.lease.Abort(ctx)
	}
	if complete {
		if e := r.lease.Observe(ctx, execution.Observation{Source: "upstream", Counters: map[string]int64{"requests": 1}, Cumulative: true, Terminal: true}); e != nil {
			return errors.Join(e, r.lease.Finish(ctx, execution.Result{StatusCode: r.status, ResponseComplete: true, Err: e}))
		}
	}
	return r.lease.Finish(ctx, execution.Result{StatusCode: r.status, ResponseComplete: complete, Terminal: complete, Err: cause})
}
func (r *agentExecution) resource(id string) error {
	if r == nil {
		return nil
	}
	ctx, cancel := executionDurabilityContext(r.ctx)
	defer cancel()
	return r.lease.Resource(ctx, execution.Resource{Kind: "agent-session", ID: id})
}

// acknowledgedAgentBody retains one final byte until a clean HTTP EOF has been
// acknowledged. Earlier native bytes stream unchanged, including AWS event
// streams. EOF is HTTP-RPC finality only, not internal agent-job finality.
type acknowledgedAgentBody struct {
	mu               sync.Mutex
	source           io.ReadCloser
	run              *agentExecution
	pending          []byte
	buffer           [32 * 1024]byte
	complete, closed bool
	failed           error
	read, expected   int64
	closeOnce        sync.Once
	closeErr         error
}

func (b *acknowledgedAgentBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if b.failed != nil {
		return 0, b.failed
	}
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	for {
		available := len(b.pending)
		if !b.complete {
			available--
		}
		if available > 0 {
			if available > len(p) {
				available = len(p)
			}
			n := copy(p, b.pending[:available])
			b.pending = b.pending[n:]
			return n, nil
		}
		if b.complete {
			return 0, io.EOF
		}
		n, e := b.source.Read(b.buffer[:])
		if n > 0 {
			b.pending = append(b.pending, b.buffer[:n]...)
			b.read += int64(n)
		}
		if e == nil {
			if n == 0 {
				return 0, nil
			}
			continue
		}
		if !errors.Is(e, io.EOF) {
			b.failed = errors.Join(e, b.run.finish(false, e))
			b.pending = nil
			return 0, b.failed
		}
		if b.expected >= 0 && b.read != b.expected {
			b.failed = errors.Join(io.ErrUnexpectedEOF, b.run.finish(false, io.ErrUnexpectedEOF))
			b.pending = nil
			return 0, b.failed
		}
		if e = b.run.ctx.Err(); e != nil {
			b.failed = errors.Join(e, b.run.finish(false, e))
			b.pending = nil
			return 0, b.failed
		}
		if e = b.run.finish(true, nil); e != nil {
			b.failed = e
			b.pending = nil
			return 0, e
		}
		b.complete = true
	}
}
func (b *acknowledgedAgentBody) Close() error {
	// Closing the native body must also unblock an outstanding read.
	b.closeOnce.Do(func() { b.closeErr = b.source.Close() })
	closeErr := b.closeErr
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return closeErr
	}
	b.closed = true
	if !b.complete && b.failed == nil {
		b.failed = errors.Join(io.ErrUnexpectedEOF, b.run.finish(false, io.ErrUnexpectedEOF))
	}
	return errors.Join(closeErr, b.failed)
}
