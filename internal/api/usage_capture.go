package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// usageCapture observes one managed exchange for the usage ledger. It only
// wraps existing hooks: it never changes what the native handler writes, how
// the quota lease is driven, or whether the exchange succeeds. All methods are
// safe on a nil capture (ledger disabled).
type usageCapture struct {
	h       *ConnectedHandler
	r       *http.Request
	writer  *usageWriter
	started time.Time

	mu           sync.Mutex
	record       usageledger.Record
	usage        usageledger.Accumulator
	anthropicRaw bool
	attemptID    string
	errorCode    string
	incomplete   bool
	streaming    bool
	finished     bool
	// terminalSeen and bound come from the native adapter's observations. A
	// WebSocket session has no HTTP completion signal of its own, so its
	// usage is final only with a terminal observation.
	terminalSeen bool
	bound        string
}

// beginUsage starts capturing an exchange for a verified tenant. The returned
// writer must be used for the rest of the handler; finish must be deferred.
func (h *ConnectedHandler) beginUsage(w http.ResponseWriter, r *http.Request, tenant, plane, resourceID string) (http.ResponseWriter, *usageCapture) {
	if h == nil || h.Ledger == nil || tenant == "" {
		return w, nil
	}
	c := &usageCapture{h: h, r: r, started: time.Now().UTC()}
	c.writer = &usageWriter{ResponseWriter: w}
	c.record = usageledger.Record{TenantID: tenant, Plane: plane, ResourceID: resourceID, Source: usageledger.SourceManaged, Requests: 1, RequestID: GetRequestID(r.Context()), TraceID: traceIDFromHeader(r.Header.Get("traceparent"))}
	if identity := GetWIFIdentity(r.Context()); identity != nil {
		c.record.PrincipalID = identity.PrincipalID
		c.record.ActorID = identity.ActorID
		c.record.ClientID = identity.AuthorizedParty
	}
	if call := accessPointCallFrom(r.Context()); call != nil {
		c.record.AccessPointID = call.ID
		c.record.CallerKind = call.CallerKind
	}
	return c.writer, c
}

// traceIDFromHeader extracts a W3C trace ID for correlation only.
func traceIDFromHeader(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) < 4 || len(parts[1]) != 32 || !traceHex(parts[1], 32) || parts[1] == strings.Repeat("0", 32) {
		return ""
	}
	return parts[1]
}

func (c *usageCapture) set(fn func(*usageledger.Record)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.record)
}

// agent records the provider and logical operation of a native agent call.
func (c *usageCapture) agent(provider cloudagents.Provider, operation string) {
	c.set(func(r *usageledger.Record) {
		r.Provider = string(provider)
		r.Operation = operation
		if strings.Contains(strings.ToLower(operation), "stream") {
			c.streaming = true
		}
	})
}

// noteError keeps a safe, server-defined error code (never provider text).
func (c *usageCapture) noteError(err error) {
	if c == nil || err == nil {
		return
	}
	var status *execution.StatusError
	if errors.As(err, &status) && status.Code != "" {
		c.mu.Lock()
		if c.errorCode == "" {
			c.errorCode = strings.ToLower(status.Code)
		}
		c.mu.Unlock()
	}
}

func (c *usageCapture) observe(counters map[string]int64, cumulative bool, modelVersion string, terminal bool, bound string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.terminalSeen = c.terminalSeen || terminal
	if bound != "" {
		c.bound = bound
	}
	c.mu.Unlock()
	c.usage.Observe(counters, cumulative)
	if modelVersion != "" {
		c.set(func(r *usageledger.Record) { r.ModelVersion = modelVersion })
	}
}

// instrumentModel wraps the gateway's existing authorization, legacy usage
// observer and execution preparer. Without QuotaPlex, the gateway's legacy
// observer reports normalized usage; with it, the lease's observations do.
func (c *usageCapture) instrumentModel(opts *modelapi.Options, connection ModelConnection) {
	if c == nil || opts == nil {
		return
	}
	c.set(func(r *usageledger.Record) { r.Provider = connection.Surface.Publisher })
	// Messages-format usage (Anthropic provider, the anthropic publisher on
	// Vertex, and Anthropic-shaped Bedrock InvokeModel bodies) counts cache
	// reads and writes outside input_tokens; the ledger's input_tokens is total
	// input. This holds with and without a quota lease (a request-count lease
	// forwards the native counters unnormalized).
	c.mu.Lock()
	c.anthropicRaw = connection.Surface.Publisher == "anthropic"
	if connection.Surface.Provider != nil {
		c.record.Provider = connection.Surface.Provider.ID
		c.anthropicRaw = connection.Surface.Provider.ID == modelapi.ProviderAnthropic || connection.Surface.Provider.ID == modelapi.ProviderBedrock
	}
	c.mu.Unlock()
	authorize := opts.Authorize
	opts.Authorize = func(ctx context.Context, request contracts.RequestContext, s modelapi.Surface, op modelapi.Operation, m modelapi.Metadata) error {
		c.set(func(r *usageledger.Record) {
			r.Operation = op.ID
			if m.Model != "" {
				if model, err := s.ModelID(m.Model); err == nil {
					r.ModelID = model
				}
			}
			if op.Transport == modelapi.WebSocket {
				c.streaming = true
			}
		})
		return authorize(ctx, request, s, op, m)
	}
	if opts.Execution != nil {
		inner := opts.Execution
		opts.Execution = modelapi.ExecutionPreparerFunc(func(ctx context.Context, request modelapi.ExecutionRequest) (execution.Lease, error) {
			lease, err := inner.Prepare(ctx, request)
			if err != nil || lease == nil {
				c.noteError(err)
				return lease, err
			}
			return c.wrapLease(lease), nil
		})
		return
	}
	opts.Observe = func(_ context.Context, _ modelapi.Owner, value modelapi.UsageObservation) {
		if value.Source == "upstream" {
			c.observe(value.Counters, value.Cumulative, value.ModelVersion, value.Terminal, value.Bound)
		}
	}
}

// limits attaches the latest provider limit observation to the record.
func (c *usageCapture) limits(value providerlimits.Limits) {
	if c == nil || value.Empty() {
		return
	}
	copy := value.Clone()
	c.set(func(r *usageledger.Record) { r.ProviderLimits = &copy })
}

// instrumentTool wraps the tool gateway's authorization, dispatch recheck and
// execution preparer.
func (c *usageCapture) instrumentTool(opts *toolexec.Options) {
	if c == nil || opts == nil {
		return
	}
	c.set(func(r *usageledger.Record) { r.Provider = "mcp"; r.Operation = "tools/call" })
	authorize := opts.Authorize
	opts.Authorize = func(ctx context.Context, request contracts.RequestContext, connection toolexec.Connection, call toolexec.Request) error {
		c.set(func(r *usageledger.Record) { r.ToolName = call.Name })
		return authorize(ctx, request, connection, call)
	}
	if before := opts.BeforeDispatch; before != nil {
		opts.BeforeDispatch = func(ctx context.Context) error {
			err := before(ctx)
			c.noteError(err)
			return err
		}
	}
	if prepare := opts.Prepare; prepare != nil {
		opts.Prepare = func(ctx context.Context, in toolexec.Invocation) (execution.Lease, error) {
			lease, err := prepare(ctx, in)
			if err != nil || lease == nil {
				c.noteError(err)
				return lease, err
			}
			return c.wrapLease(lease), nil
		}
	}
}

// agentPreparer wraps the native agent execution preparer.
func (c *usageCapture) agentPreparer(inner cloudagents.ExecutionPreparer) cloudagents.ExecutionPreparer {
	if c == nil || inner == nil {
		return inner
	}
	return cloudagents.ExecutionPreparerFunc(func(ctx context.Context, in cloudagents.ExecutionRequest) (execution.Lease, error) {
		lease, err := inner.Prepare(ctx, in)
		if err != nil || lease == nil {
			c.noteError(err)
			return lease, err
		}
		return c.wrapLease(lease), nil
	})
}

// agentBody notes whether a native agent body reached a clean EOF.
func (c *usageCapture) agentBody(body io.Reader) io.Reader {
	if c == nil {
		return body
	}
	return &usageBody{Reader: body, capture: c}
}

type usageBody struct {
	io.Reader
	capture *usageCapture
	ended   bool
}

func (b *usageBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err != nil && !b.ended {
		b.ended = true
		if !errors.Is(err, io.EOF) {
			b.capture.mu.Lock()
			b.capture.incomplete = true
			b.capture.mu.Unlock()
		}
	}
	return n, err
}

// usageLease forwards every call to the quota lease unchanged and records
// what it sees. It forwards the optional Receipt and metering-profile
// contracts so gateways behave exactly as with the unwrapped lease.
type usageLease struct {
	inner   execution.Lease
	capture *usageCapture
}

func (c *usageCapture) wrapLease(inner execution.Lease) execution.Lease {
	if receipt, ok := inner.(execution.Receipt); ok {
		c.mu.Lock()
		c.attemptID = receipt.AttemptID()
		c.mu.Unlock()
	}
	return &usageLease{inner: inner, capture: c}
}

func (l *usageLease) BeforeDispatch(ctx context.Context) error {
	err := l.inner.BeforeDispatch(ctx)
	l.capture.noteError(err)
	return err
}

func (l *usageLease) Observe(ctx context.Context, observation execution.Observation) error {
	err := l.inner.Observe(ctx, observation)
	if observation.Source == "upstream" {
		l.capture.observe(observation.Counters, observation.Cumulative, observation.ModelVersion, observation.Terminal, observation.Bound)
	}
	l.capture.noteError(err)
	return err
}

func (l *usageLease) Resource(ctx context.Context, resource execution.Resource) error {
	return l.inner.Resource(ctx, resource)
}

func (l *usageLease) Finish(ctx context.Context, result execution.Result) error {
	err := l.inner.Finish(ctx, result)
	l.capture.mu.Lock()
	if result.Err != nil {
		l.capture.incomplete = true
	}
	l.capture.mu.Unlock()
	l.capture.noteError(err)
	return err
}

func (l *usageLease) Abort(ctx context.Context) error { return l.inner.Abort(ctx) }

func (l *usageLease) AttemptID() string {
	if receipt, ok := l.inner.(execution.Receipt); ok {
		return receipt.AttemptID()
	}
	return ""
}

func (l *usageLease) MeteringProfile() string {
	if profile, ok := l.inner.(interface{ MeteringProfile() string }); ok {
		return profile.MeteringProfile()
	}
	return ""
}

// errorClass derives a stable class from the status code.
func errorClass(status int) string {
	switch {
	case status == 401:
		return "unauthenticated"
	case status == 403:
		return "forbidden"
	case status == 404:
		return "not_found"
	case status == 409:
		return "conflict"
	case status == 429:
		return "rate_limited"
	case status == 400 || status == 413 || status == 415 || status == 422:
		return "invalid_request"
	case status >= 400 && status < 500:
		return "client_error"
	case status == 501:
		return "unsupported"
	case status == 502:
		return "upstream_error"
	case status == 503:
		return "unavailable"
	case status == 504:
		return "timeout"
	case status >= 500:
		return "server_error"
	}
	return ""
}

// finish classifies the exchange and submits its record. It runs after the
// native handler has written its response and never writes to it.
func (c *usageCapture) finish() {
	if c == nil {
		return
	}
	finishedAt := time.Now().UTC()
	clientGone := c.r.Context().Err() != nil
	status, writeFailed, hijacked, contentType := c.writer.state()
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	record := c.record.Clone()
	counters := c.usage.Snapshot()
	anthropicRaw, attemptID, code, incomplete := c.anthropicRaw, c.attemptID, c.errorCode, c.incomplete
	streaming := c.streaming || hijacked
	bound, terminalSeen := c.bound, c.terminalSeen
	c.mu.Unlock()
	if hijacked && !terminalSeen {
		// A relayed session without a final usage observation: whatever was
		// observed is a lower bound (or labelled more specifically).
		incomplete = true
		if bound == "" {
			bound = usageledger.UsageLowerBound
		}
		if code == "" {
			code = "session_usage_" + bound
		}
	}

	if media, _, err := mime.ParseMediaType(contentType); err == nil {
		switch media {
		case "text/event-stream", "application/x-ndjson", "application/vnd.amazon.eventstream":
			streaming = true
		}
	}
	if status == 0 {
		status = http.StatusOK
		if hijacked {
			status = http.StatusSwitchingProtocols
		}
	}
	record.ID = usageledger.NewID()
	record.StartedAt, record.FinishedAt = c.started, finishedAt
	record.LatencyMS = finishedAt.Sub(c.started).Milliseconds()
	record.HTTPStatus = status
	record.Streaming = streaming
	record.QuotaAttemptID = attemptID
	switch {
	case status == 401 || status == 403 || status == 407 || status == 429:
		record.Status = usageledger.StatusDenied
		record.ErrorClass = errorClass(status)
	case status >= 400:
		record.Status = usageledger.StatusError
		record.ErrorClass = errorClass(status)
	case writeFailed || clientGone || incomplete:
		// The response began successfully but did not complete.
		record.Status = usageledger.StatusUnknown
		record.ErrorClass = "stream_aborted"
		if clientGone || writeFailed {
			record.ErrorClass = "client_disconnected"
		}
	default:
		record.Status = usageledger.StatusOK
	}
	if code != "" && record.Status != usageledger.StatusOK {
		record.ErrorClass = code
	}
	if anthropicRaw {
		normalizeExclusiveInput(counters)
	}
	record.Usage = usageledger.FromCounters(counters)
	if bound != "" {
		record.UsageBound = bound
	} else if nonTokenUsage(counters) {
		// Audio seconds and similar units were observed but have no ledger
		// field: the token counters do not describe the whole exchange.
		record.UsageBound = usageledger.UsagePartial
	}
	record.Cost = usageledger.UnknownCost()
	h := c.h
	h.Ledger.Submit(c.r.Context(), func(ctx context.Context) (usageledger.Record, error) {
		if attemptID != "" && h.Quota != nil {
			if cost, ok := h.Quota.settledCost(ctx, record.TenantID, attemptID); ok {
				record.Cost = cost
				return record, nil
			}
		}
		if record.ModelID != "" {
			prices, err := h.ModelPrices(ctx, record.TenantID)
			if err == nil {
				record.Cost = usageledger.Estimate(prices, record)
			}
		}
		return record, nil
	})
}

// nonTokenUsage reports observed meters the record cannot represent:
// non-token units (milliseconds of audio, counts of images) and separately
// attributed meters such as Realtime input transcription (billed to the
// transcription model, while a record carries one model).
func nonTokenUsage(counters map[string]int64) bool {
	for key := range counters {
		if strings.HasSuffix(key, "_milliseconds") || strings.HasSuffix(key, "_images") || strings.HasPrefix(key, "transcription_") {
			return true
		}
	}
	return false
}

// normalizeExclusiveInput converts native Messages-format counters, whose
// input_tokens excludes cache reads and cache writes, to the ledger
// vocabulary: input_tokens is total input (fresh + cache read + cache write)
// and uncached_input_tokens the fresh part. Counters an adapter already
// normalized (uncached_input_tokens present) are left alone. A missing cache
// counter adds nothing; it is never reported as zero.
func normalizeExclusiveInput(counters map[string]int64) {
	if _, normalized := counters["uncached_input_tokens"]; normalized {
		return
	}
	input, ok := counters["input_tokens"]
	if !ok {
		return
	}
	total := input
	for _, key := range []string{"cached_tokens", "cache_write_tokens"} {
		if n, known := counters[key]; known {
			if n < 0 || total > math.MaxInt64-n {
				delete(counters, "input_tokens")
				return
			}
			total += n
		}
	}
	counters["uncached_input_tokens"] = input
	counters["input_tokens"] = total
}

// ModelPrices returns the tenant's valid model-price resources. Invalid stored
// specs are skipped: an unreadable price never becomes a guessed price.
func (h *ConnectedHandler) ModelPrices(ctx context.Context, tenant string) ([]usageledger.Price, error) {
	return modelPricesFrom(ctx, h.Store, tenant)
}

func modelPricesFrom(ctx context.Context, store resources.Store, tenant string) ([]usageledger.Price, error) {
	if store == nil {
		return nil, fmt.Errorf("price store unavailable")
	}
	records, err := store.List(ctx, tenant, usageledger.PriceKind)
	if err != nil {
		return nil, err
	}
	prices := make([]usageledger.Price, 0, len(records))
	for _, record := range records {
		if price, err := usageledger.DecodePrice(record.ID, record.Spec); err == nil {
			prices = append(prices, price)
		}
	}
	return prices, nil
}

// settledCost reads the money a quota settlement charged for the attempt. It
// is a read of durable state: it never settles, retries or changes the quota
// outcome. More than one distinct monetary meter is ambiguous and ignored.
func (h *QuotaHost) settledCost(ctx context.Context, tenant, attempt string) (usageledger.Cost, bool) {
	value, ok := h.tenants[tenant]
	if !ok || value.engine == nil {
		return usageledger.Cost{}, false
	}
	info, err := value.engine.GetAttempt(ctx, tenant, attempt)
	if err != nil || info.Decision.State != "settled" {
		return usageledger.Cost{}, false
	}
	var found *usageledger.Cost
	for meterID, quantity := range info.Actual {
		meter, known := info.Plan.Meters[meterID]
		if !known || meter.Unit != "money" || quantity < 0 {
			continue
		}
		cost := usageledger.Cost{Basis: usageledger.BasisSettled, Currency: meter.Currency, Amount: strconv.FormatInt(quantity, 10), Scale: meter.Scale}
		if found != nil && *found != cost {
			return usageledger.Cost{}, false
		}
		found = &cost
	}
	if found == nil || found.Validate() != nil {
		return usageledger.Cost{}, false
	}
	return *found, true
}

// usageWriter records the status and completion of the native response
// without altering it. It forwards flushing, hijacking and unwrapping.
type usageWriter struct {
	http.ResponseWriter
	mu          sync.Mutex
	status      int
	wrote       bool
	writeFailed bool
	hijacked    bool
}

func (w *usageWriter) WriteHeader(status int) {
	w.mu.Lock()
	if w.status == 0 && status >= 200 {
		w.status = status
	}
	w.mu.Unlock()
	w.ResponseWriter.WriteHeader(status)
}

func (w *usageWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.wrote = true
	w.mu.Unlock()
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.mu.Lock()
		w.writeFailed = true
		w.mu.Unlock()
	}
	return n, err
}

func (w *usageWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *usageWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response does not support hijacking")
	}
	conn, rw, err := hijacker.Hijack()
	if err == nil {
		w.mu.Lock()
		w.hijacked = true
		w.mu.Unlock()
	}
	return conn, rw, err
}

func (w *usageWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *usageWriter) state() (status int, writeFailed, hijacked bool, contentType string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status, w.writeFailed, w.hijacked, w.ResponseWriter.Header().Get("Content-Type")
}
