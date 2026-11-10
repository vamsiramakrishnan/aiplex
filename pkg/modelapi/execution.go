package modelapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// ExecutionRequest contains an authorized, validated native request. Its body
// and metadata are private snapshots. Headers contain only caller protocol/selection and
// idempotency fields; they are requests to validate, never identity evidence.
type ExecutionRequest struct {
	Context   contracts.RequestContext
	Surface   Surface
	Operation Operation
	Metadata  Metadata
	Body      []byte
	Headers   http.Header
	Method    string
	Path      string
	RawQuery  string
}

type ExecutionPreparer interface {
	Prepare(context.Context, ExecutionRequest) (execution.Lease, error)
}
type ExecutionPreparerFunc func(context.Context, ExecutionRequest) (execution.Lease, error)

func (f ExecutionPreparerFunc) Prepare(ctx context.Context, request ExecutionRequest) (execution.Lease, error) {
	return f(ctx, request)
}

func (g *Gateway) prepareExecution(r *http.Request, trusted contracts.RequestContext, op Operation, metadata Metadata, body []byte) (execution.Lease, error) {
	if g.options.Admit != nil && !sessionExchange(r.Context()) {
		if err := g.options.Admit(r.Context(), trusted, op, metadata); err != nil {
			return nil, err
		}
	}
	if g.options.Execution == nil {
		return nil, nil
	}
	headers := http.Header{}
	for _, key := range []string{"Idempotency-Key", "X-Aiplex-Funding-Account", "X-Aiplex-Operation-Id", "Anthropic-Version", "Anthropic-Beta", "Last-Event-ID"} {
		if values := r.Header.Values(key); len(values) > 0 {
			headers[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
		}
	}
	query := r.URL.Query()
	for _, key := range []string{"key", "access_token", "oauth_token"} {
		query.Del(key)
	}
	metadata.References = append([]Resource(nil), metadata.References...)
	metadata.DataURIs = append([]string(nil), metadata.DataURIs...)
	if trusted.MembershipSnapshotRef != nil {
		ref := *trusted.MembershipSnapshotRef
		trusted.MembershipSnapshotRef = &ref
	}
	lease, err := g.options.Execution.Prepare(r.Context(), ExecutionRequest{Context: trusted, Surface: g.Surface(), Operation: op, Metadata: metadata, Body: append([]byte(nil), body...), Headers: headers, Method: r.Method, Path: r.URL.EscapedPath(), RawQuery: query.Encode()})
	if err == nil && lease == nil {
		return nil, errors.New("execution preparer returned no lease")
	}
	return lease, err
}

func executionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

func writeExecutionError(w http.ResponseWriter, err error) {
	var status *execution.StatusError
	if errors.As(err, &status) && status.Status >= 400 && status.Status <= 599 {
		if status.RetryAfter > 0 {
			seconds := int64(status.RetryAfter / time.Second)
			if status.RetryAfter%time.Second != 0 {
				seconds++
			}
			w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		}
		http.Error(w, status.Code+": "+status.Message, status.Status)
		return
	}
	http.Error(w, "execution control unavailable", http.StatusServiceUnavailable)
}

type executionRun struct {
	lease   execution.Lease
	ctx     context.Context
	claimed bool
	closed  bool
	// dispatchedModel is the exact model the request was dispatched to, for
	// protocols whose responses carry no model identifier (Bedrock Converse:
	// the versioned modelId is the URL path selector and is not echoed).
	dispatchedModel string
}

// modelIdentity fills an observation's model version from the dispatched
// model when the native response reports none.
func (r *executionRun) modelIdentity(value *UsageObservation) {
	if r != nil && value != nil && value.ModelVersion == "" && r.dispatchedModel != "" && !requestCountRun(r) {
		value.ModelVersion = r.dispatchedModel
	}
}

func (r *executionRun) beforeDispatch(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if err := r.lease.BeforeDispatch(ctx); err != nil {
		return err
	}
	r.claimed = true
	return nil
}
func (r *executionRun) resource(value Resource) error {
	if r == nil || value.ID == "" {
		return nil
	}
	ctx, cancel := executionContext(r.ctx)
	defer cancel()
	return r.lease.Resource(ctx, execution.Resource{Kind: value.Kind, ID: value.ID})
}
func (r *executionRun) observe(value UsageObservation) error {
	if r == nil {
		return nil
	}
	ctx, cancel := executionContext(r.ctx)
	defer cancel()
	copy := make(map[string]int64, len(value.Counters))
	for key, n := range value.Counters {
		copy[key] = n
	}
	return r.lease.Observe(ctx, execution.Observation{Source: value.Source, EventID: value.EventID, ResourceID: value.ResourceID, ModelVersion: value.ModelVersion, Counters: copy, Cumulative: value.Cumulative, Terminal: value.Terminal, Bound: value.Bound, Dimensions: cloneDimensions(value.Dimensions)})
}
func (r *executionRun) finish(result execution.Result) error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	ctx, cancel := executionContext(r.ctx)
	defer cancel()
	return r.lease.Finish(ctx, result)
}
func (r *executionRun) close() {
	if r == nil || r.closed {
		return
	}
	r.closed = true
	ctx, cancel := executionContext(r.ctx)
	defer cancel()
	if !r.claimed {
		_ = r.lease.Abort(ctx)
		return
	}
	_ = r.lease.Finish(ctx, execution.Result{Err: errors.New("execution response incomplete")})
}
