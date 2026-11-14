package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/routing"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Reliability and routing for access-point models (docs/reliability-and-routing.md).
//
// A routed model (several backends, a routing block or a cache) is served by
// serveRoutedModel. Each attempt is one complete pass through the existing
// managed path for one backend (ClientGateway -> NativeModel -> gateway), so
// every attempt takes its own grants check, capacity admission, quota lease
// and credential. Between the two hook seams:
//
//   - the request hooks run once, on the first attempt that reaches them,
//     with the policies of every backend's surface; later attempts dispatch
//     the same hooked body with only the model name rewritten;
//   - an attempt's response is held back (never forwarded) while it is a
//     failure the policy may retry or fall over on; the decision is taken
//     when the gateway writes the status, before any byte reaches the caller,
//     so nothing is retried once a byte (of a stream or otherwise) was sent;
//   - the response hooks run once, on the response that is delivered.
//
// The usage ledger gets one record per exchange (the attempt that answered
// the caller) with the attempts summary; each attempt has its own CLIENT
// span under the exchange's INTERNAL "aiplex.route" span.

// ── state ─────────────────────────────────────────────────────────────────

type routeAttemptKey struct{}

func withRouteAttempt(ctx context.Context, a *routeAttempt) context.Context {
	return context.WithValue(ctx, routeAttemptKey{}, a)
}

func routeAttemptFrom(ctx context.Context) *routeAttempt {
	a, _ := ctx.Value(routeAttemptKey{}).(*routeAttempt)
	return a
}

// routeExchange is one routed exchange.
type routeExchange struct {
	h        *ConnectedHandler
	tenant   string
	ap       AccessPoint
	entry    AccessPointModel
	in       accessPointClientRequest
	policy   routing.Effective
	backends []routing.Backend
	surfaces []string
	started  time.Time
	deadline time.Time
	// stateful requests never fall over to a different backend and are
	// retried only when provably unsent.
	stateful bool
	// header is the caller's request header (cache-control and the
	// protocol headers that change output).
	header    http.Header
	principal string
	span      trace.Span

	mu        sync.Mutex
	attempts  []usageledger.Attempt
	delivered []*deliveredRecord
	hooks     *routeHooks
	cache     *routeCacheState
	// firstMissingGrant is the refusal explanation of the first backend the
	// caller was not granted.
	firstMissingGrant string
	upstreamAttempts  int
}

// routeAttempt is one attempt of a routed exchange.
type routeAttempt struct {
	route   *routeExchange
	backend routing.Backend
	reason  string
	number  int // attempt number on this backend (1 = first)
	breaker routing.Admission
	started time.Time

	mu           sync.Mutex
	dispatched   bool
	dispatch     modelapi.DispatchOutcome
	notGranted   bool
	errorCode    string
	bodyMismatch bool
	cacheHit     bool
	operation    modelapi.Operation
	outcome      string
	upstream     int
	retryAfter   *time.Duration
	record       *deliveredRecord
}

// deliveredRecord is an attempt's finished ledger record, held by the
// router until it knows which attempt answered the caller.
type deliveredRecord struct {
	record        usageledger.Record
	span          trace.Span
	genAIProvider string
	firstWrite    time.Time
	submit        func(usageledger.Record)
}

// routeHooks is the request-hook result shared by the attempts.
type routeHooks struct {
	in, out []byte
	inspect bool
	m       modelHooks
}

func (a *routeAttempt) set(fn func(*routeAttempt)) {
	a.mu.Lock()
	fn(a)
	a.mu.Unlock()
}

// instrument wires an attempt's signals into a managed gateway's options.
// It runs in NativeModel after every other option is installed.
func (a *routeAttempt) instrument(r *http.Request, opts *modelapi.Options, usage *usageCapture) {
	authorize := opts.Authorize
	opts.Authorize = func(ctx context.Context, request contracts.RequestContext, s modelapi.Surface, op modelapi.Operation, m modelapi.Metadata) error {
		a.set(func(a *routeAttempt) { a.operation = op })
		err := authorize(ctx, request, s, op, m)
		if err != nil {
			a.set(func(a *routeAttempt) { a.notGranted = true })
		}
		return err
	}
	if admit := opts.Admit; admit != nil {
		opts.Admit = func(ctx context.Context, request contracts.RequestContext, op modelapi.Operation, m modelapi.Metadata) error {
			err := admit(ctx, request, op, m)
			a.noteError(err)
			return err
		}
	}
	if prepare := opts.Execution; prepare != nil {
		opts.Execution = modelapi.ExecutionPreparerFunc(func(ctx context.Context, request modelapi.ExecutionRequest) (execution.Lease, error) {
			lease, err := prepare.Prepare(ctx, request)
			a.noteError(err)
			return lease, err
		})
	}
	observe := opts.ObserveDispatch
	opts.ObserveDispatch = func(ctx context.Context, outcome modelapi.DispatchOutcome) {
		a.set(func(a *routeAttempt) { a.dispatched, a.dispatch = true, outcome })
		a.route.mu.Lock()
		a.route.upstreamAttempts++
		a.route.mu.Unlock()
		if observe != nil {
			observe(ctx, outcome)
		}
	}
	if a.route.entry.Cache != nil {
		opts.Cache = &routeCache{attempt: a, usage: usage}
	}
}

func (a *routeAttempt) noteError(err error) {
	var status *execution.StatusError
	if errors.As(err, &status) {
		a.set(func(a *routeAttempt) {
			if a.errorCode == "" {
				a.errorCode = status.Code
			}
		})
	}
}

// classify decides the attempt's outcome from the status the gateway wrote
// and the signals it raised on the way.
func (a *routeAttempt) classify(status int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.outcome != "" {
		return
	}
	switch {
	case a.cacheHit:
		a.outcome = routing.OutcomeCacheHit
	case a.notGranted:
		a.outcome = routing.OutcomeNotGranted
	case a.bodyMismatch:
		a.outcome = routing.OutcomeBodyMismatch
	case a.dispatched && a.dispatch.Err != nil:
		a.outcome = routing.OutcomeTransportError
		if a.dispatch.NotSent {
			a.outcome = routing.OutcomeConnectError
		}
	case a.dispatched:
		a.upstream = a.dispatch.Status
		if status == a.dispatch.Status {
			a.outcome = routing.UpstreamStatusClass(status)
		} else if status < 400 {
			a.outcome = routing.OutcomeOK
		} else {
			// The provider answered, then AIPlex refused to deliver it
			// (observation bound, settlement, a response hook): final.
			a.outcome = routing.OutcomeRefused
		}
		if hint, ok := routing.RetryAfter(a.dispatch.Header, time.Now()); ok && status >= 400 {
			a.retryAfter = &hint
		}
	case strings.HasPrefix(a.errorCode, "CAPACITY_"):
		a.outcome = routing.OutcomeCapacity
	case strings.HasPrefix(a.errorCode, "QUOTA_") && (status == http.StatusTooManyRequests || status == http.StatusForbidden) && a.errorCode != "QUOTA_EXECUTION_REVOKED" && a.errorCode != "QUOTA_ACCESS_POINT_CHANGED":
		a.outcome = routing.OutcomeQuotaDenied
	case status < 400:
		a.outcome = routing.OutcomeOK
	default:
		a.outcome = routing.OutcomeRefused
	}
}

// continuable reports whether the classified attempt may be followed by
// another one (a retry here or a fallback), in which case its response is
// held back rather than delivered.
func (a *routeAttempt) continuable() bool {
	x := a.route
	a.mu.Lock()
	outcome := a.outcome
	if a.operation.Stateful || a.operation.Transport == modelapi.WebSocket {
		// The operation table marks it stateful (stored responses, batches,
		// files, cached contents, long-running operations, sessions).
		x.stateful = true
	}
	a.mu.Unlock()
	if x.policy.Retries(outcome) && a.number < x.policy.MaxAttempts && (!x.stateful || outcome == routing.OutcomeConnectError) {
		return true
	}
	if !x.policy.FallsOver(outcome) {
		return false
	}
	if x.stateful {
		// A stateful request may skip a backend it was never sent to, but
		// never falls over after one was asked.
		return outcome == routing.OutcomeNotGranted || outcome == routing.OutcomeBreakerOpen || outcome == routing.OutcomeBodyMismatch
	}
	return true
}

// ── the held-back response writer ─────────────────────────────────────────

// routeWriter sits between one attempt and the caller. It decides when the
// gateway writes the status: a success, or a failure that ends the exchange,
// is committed (headers copied, everything after streamed through); a
// failure that may be retried or fall over is held back in a bounded buffer
// and never reaches the caller unless it turns out to be the last answer.
type routeWriter struct {
	w         http.ResponseWriter
	attempt   *routeAttempt
	header    http.Header
	decided   bool
	committed bool
	status    int
	buf       bytes.Buffer
	overflow  bool
}

const routeHeldBodyLimit = 1 << 20

func (w *routeWriter) Header() http.Header {
	if w.committed {
		return w.w.Header()
	}
	return w.header
}

func (w *routeWriter) WriteHeader(status int) {
	if w.decided || status < 200 {
		return
	}
	w.decided, w.status = true, status
	w.attempt.classify(status)
	if status >= 400 && w.attempt.continuable() {
		return
	}
	w.commit()
}

func (w *routeWriter) commit() {
	if w.committed {
		return
	}
	w.committed = true
	out := w.w.Header()
	for k, v := range w.header {
		out[k] = append([]string(nil), v...)
	}
	w.attempt.route.decorate(out)
	w.w.WriteHeader(w.status)
}

func (w *routeWriter) Write(p []byte) (int, error) {
	if !w.decided {
		w.WriteHeader(http.StatusOK)
	}
	if w.committed {
		return w.w.Write(p)
	}
	if w.buf.Len()+len(p) > routeHeldBodyLimit {
		w.overflow = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *routeWriter) Flush() {
	if !w.committed {
		return
	}
	if f, ok := w.w.(http.Flusher); ok {
		f.Flush()
	}
}

// upgrade commits the attempt for a WebSocket upgrade: a session is
// stateful and is never retried or failed over once it is being upgraded.
func (w *routeWriter) upgrade() {
	if w.decided {
		return
	}
	w.decided, w.status = true, http.StatusSwitchingProtocols
	w.attempt.classify(http.StatusOK)
	w.committed = true
	out := w.w.Header()
	for k, v := range w.header {
		out[k] = append([]string(nil), v...)
	}
}

// Hijack lets a WebSocket upgrade reach the caller's connection.
func (w *routeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.w.(http.Hijacker)
	if !ok || (w.decided && !w.committed) {
		return nil, nil, fmt.Errorf("response does not support hijacking")
	}
	w.upgrade()
	return hijacker.Hijack()
}

// Unwrap exposes the caller's writer for an upgrade.
func (w *routeWriter) Unwrap() http.ResponseWriter {
	w.upgrade()
	return w.w
}

// replay delivers a held-back failure as the exchange's answer.
func (w *routeWriter) replay() {
	if w.committed {
		return
	}
	body := w.buf.Bytes()
	if w.overflow {
		// The held error body did not fit: answer with a protocol-native
		// error instead of a truncated provider body.
		for k := range w.header {
			delete(w.header, k)
		}
		w.header.Set("Content-Type", "text/plain; charset=utf-8")
		// AIPlex writes this body, so it is AIPlex's refusal.
		errorcode.Set(w.header, "UPSTREAM_RESPONSE_TOO_LARGE")
		body = []byte("upstream error response exceeds the retained bound")
	}
	w.commit()
	_, _ = w.w.Write(body)
}

// decorate adds the routing response headers: the number of upstream
// attempts and the cache outcome. Backend names are never sent to callers.
func (x *routeExchange) decorate(h http.Header) {
	x.mu.Lock()
	defer x.mu.Unlock()
	h.Set("X-Aiplex-Attempts", strconv.Itoa(x.upstreamAttempts))
	if x.cache != nil && x.cache.info != nil {
		h.Set("X-Aiplex-Cache", x.cache.info.Status)
	}
}

// ── the router ────────────────────────────────────────────────────────────

func (h *ConnectedHandler) routeBreakers() *routing.Breakers {
	routeStateMu.Lock()
	defer routeStateMu.Unlock()
	if h.Breakers == nil {
		h.Breakers = routing.NewBreakers(nil)
		h.Breakers.OnTransition = func(state string) {
			telemetry.Metrics().RecordBreakerTransition(context.Background(), state)
		}
		breakers := h.Breakers
		telemetry.Metrics().ObserveBreakers(breakers.Counts)
	}
	return h.Breakers
}

func (h *ConnectedHandler) responseCache() routing.CacheStore {
	routeStateMu.Lock()
	defer routeStateMu.Unlock()
	if h.ResponseCache == nil {
		h.ResponseCache = routing.NewMemoryCache(0)
	}
	return h.ResponseCache
}

var routeStateMu sync.Mutex

// statefulRequest reports a request whose state lives at one provider:
// WebSocket sessions, Responses requests that store or continue a response,
// and requests that reference provider-held state.
func statefulRequest(protocol string, body []byte) bool {
	if accessPointWebSocket(protocol) {
		return true
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(body, &probe) != nil {
		return false
	}
	for _, key := range []string{"previous_response_id", "conversation", "cachedContent", "cached_content", "container", "previous_interaction_id"} {
		if v, ok := probe[key]; ok && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return true
		}
	}
	if protocol == WireOpenAIResponses {
		// Responses are stored by default (store: true): the stored
		// response then lives at the provider that produced it.
		store, ok := probe["store"]
		return !ok || !bytes.Equal(bytes.TrimSpace(store), []byte("false"))
	}
	return false
}

// owningBackend finds the backend whose surface owns a response or cached
// content the request continues, so a stateful follow-up goes back to it.
func (x *routeExchange) owningBackend(ctx context.Context) (int, bool) {
	var probe map[string]json.RawMessage
	if json.Unmarshal(x.in.data, &probe) != nil {
		return 0, false
	}
	var ref modelapi.Resource
	for key, kind := range map[string]string{"previous_response_id": "response", "cachedContent": "cache", "cached_content": "cache"} {
		var id string
		if raw, ok := probe[key]; ok && json.Unmarshal(raw, &id) == nil && id != "" {
			ref = modelapi.Resource{Kind: kind, ID: id}
			if kind == "cache" {
				if i := strings.LastIndex(id, "/"); i >= 0 {
					ref.ID = id[i+1:]
				}
			}
		}
	}
	if ref.ID == "" {
		return 0, false
	}
	for i, b := range x.backends {
		record, err := x.h.Store.Get(ctx, x.tenant, "model-surface", b.SurfaceRef)
		if err != nil {
			continue
		}
		owner := resourceOwnership{store: x.h.Store, namespace: strconv.FormatUint(record.Revision, 10)}
		if owner.Check(ctx, modelapi.Owner{TenantID: x.tenant, PrincipalID: x.principal, SurfaceID: b.SurfaceRef}, ref) == nil {
			return i, true
		}
	}
	return 0, false
}

// prices computes each backend's comparable price for lowest_cost: the
// estimated cost of InputWeight million input and OutputWeight million output
// tokens under the tenant's model prices. An unmatched price is unknown,
// never cheap.
func (x *routeExchange) prices(ctx context.Context) []routing.Price {
	out := make([]routing.Price, len(x.backends))
	prices, err := x.h.ModelPrices(ctx, x.tenant)
	if err != nil {
		return out
	}
	in, outTokens := int64(x.policy.InputWeight)*1_000_000, int64(x.policy.OutputWeight)*1_000_000
	for i, b := range x.backends {
		connection, err := x.h.accessPointSurface(ctx, x.tenant, b.SurfaceRef)
		if err != nil {
			continue
		}
		modelID, err := connection.Surface.ModelID(b.NativeModel)
		if err != nil {
			continue
		}
		provider := connection.Surface.Publisher
		if connection.Surface.Provider != nil {
			provider = connection.Surface.Provider.ID
		}
		now := time.Now().UTC()
		rec := usageledger.Record{Plane: usageledger.PlaneLLM, Provider: provider, ModelID: modelID, ResourceID: b.SurfaceRef, StartedAt: now, FinishedAt: now, Requests: 1, Usage: &usageledger.Usage{InputTokens: &in, OutputTokens: &outTokens}}
		cost := usageledger.Estimate(prices, rec)
		amount, ok := cost.AmountInt()
		if cost.Basis != usageledger.BasisEstimated || !ok {
			continue
		}
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(cost.Scale)), nil)
		out[i] = routing.Price{Known: true, Currency: cost.Currency, Amount: new(big.Rat).SetFrac(amount, scale)}
	}
	return out
}

// retryKey derives the quota idempotency key of a retry on the same backend
// from the caller's key, so a retry is a new quota attempt while a client's
// replay of the whole exchange still maps onto the same attempts. The
// caller's key is never sent to a provider.
func retryKey(key string, backend routing.Backend, number int) string {
	sum := sha256.Sum256([]byte(key + "\x00" + backend.SurfaceRef + "\x00" + backend.NativeModel + "\x00" + strconv.Itoa(number)))
	return "aiplex-retry-" + hex.EncodeToString(sum[:20])
}

func (x *routeExchange) remaining() time.Duration { return time.Until(x.deadline) }

func (x *routeExchange) addAttempt(a usageledger.Attempt) {
	x.mu.Lock()
	if len(x.attempts) < usageledger.MaxAttempts {
		x.attempts = append(x.attempts, a)
	}
	x.mu.Unlock()
	telemetry.Metrics().RecordRoutingAttempt(context.Background(), a.Outcome)
}

// serveRoutedModel serves one exchange of a routed access-point model.
func (h *ConnectedHandler) serveRoutedModel(w http.ResponseWriter, r *http.Request, tenant string, ap AccessPoint, call *accessPointCall, entry AccessPointModel, in accessPointClientRequest) {
	fail := modelErrorWriter(in.protocol)
	x := &routeExchange{h: h, tenant: tenant, ap: ap, entry: entry, in: in, policy: entry.Routing.Effective(), backends: entry.allBackends(), started: time.Now(), header: r.Header.Clone()}
	if identity := GetWIFIdentity(r.Context()); identity != nil {
		x.principal = identity.PrincipalID
	}
	for _, b := range x.backends {
		x.surfaces = append(x.surfaces, b.SurfaceRef)
	}
	x.deadline = x.started.Add(x.policy.Deadline)
	if d, ok := r.Context().Deadline(); ok && d.Before(x.deadline) {
		x.deadline = d
	}
	x.stateful = statefulRequest(in.protocol, in.data)
	if entry.Cache != nil {
		x.cache = &routeCacheState{config: entry.Cache}
	}
	ctx, span := telemetry.Tracer().Start(r.Context(), "aiplex.route "+entry.Name, trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	x.span = span
	r = r.WithContext(ctx)
	breakers := h.routeBreakers()

	order, excluded := routing.Order(x.policy, x.backends, nil, func(n int) int { return rand.IntN(n) })
	if x.policy.Strategy == routing.StrategyLowestCost {
		order, excluded = routing.Order(x.policy, x.backends, x.prices(ctx), nil)
	}
	if x.stateful {
		if owner, ok := x.owningBackend(ctx); ok {
			// A follow-up goes only to the backend that holds its state.
			order = []routing.Choice{{Index: owner, Reason: "state_owner"}}
		}
	}
	for _, c := range excluded {
		b := x.backends[c.Index]
		x.addAttempt(usageledger.Attempt{SurfaceRef: b.SurfaceRef, NativeModel: b.NativeModel, Outcome: routing.OutcomeBackendMissing, Reason: c.Reason})
	}

	var final *routeAttempt
	var held *routeAttempt
	var heldWriter *routeWriter
	asked := false // a backend has been sent (or offered) the request
	idempotency := r.Header.Get("Idempotency-Key")
backends:
	for _, choice := range order {
		backend := x.backends[choice.Index]
		if x.stateful && asked {
			break
		}
		key := routing.BreakerKey(tenant, ap.ID, entry.Name, backend)
		for number := 1; number <= x.policy.MaxAttempts; number++ {
			if x.remaining() <= 0 || ctx.Err() != nil {
				x.addAttempt(usageledger.Attempt{SurfaceRef: backend.SurfaceRef, NativeModel: backend.NativeModel, Outcome: routing.OutcomeDeadline, Reason: choice.Reason})
				break backends
			}
			admission := breakers.Allow(key, x.policy)
			if !admission.Allowed {
				x.addAttempt(usageledger.Attempt{SurfaceRef: backend.SurfaceRef, NativeModel: backend.NativeModel, Outcome: routing.OutcomeBreakerOpen, BreakerState: admission.State, Reason: choice.Reason})
				telemetry.Metrics().RecordRoutingFallback(ctx, routing.OutcomeBreakerOpen)
				continue backends
			}
			attempt := &routeAttempt{route: x, backend: backend, reason: choice.Reason, number: number, breaker: admission, started: time.Now()}
			ar := r.WithContext(withRouteAttempt(ctx, attempt))
			if number > 1 && idempotency != "" {
				ar.Header = r.Header.Clone()
				ar.Header.Set("Idempotency-Key", retryKey(idempotency, backend, number))
			}
			inner, failure := h.accessPointBackendRequest(ar, tenant, call, backend, in)
			if failure != nil {
				breakers.Release(key, admission)
				x.addAttempt(usageledger.Attempt{SurfaceRef: backend.SurfaceRef, NativeModel: backend.NativeModel, Outcome: routing.OutcomeBackendMissing, BreakerState: admission.State, Reason: choice.Reason})
				if held == nil && final == nil && len(order) == 1 {
					fail(w, r, failure.status, failure.code, failure.message)
					h.routeLedgerWithoutAttempt(w, r, x, failure.status)
					return
				}
				continue backends
			}
			call.missingGrant = ""
			// A delivered AIPlex refusal is re-rendered in the caller's
			// protocol, exactly as for a single-backend model.
			native := &nativeErrorWriter{ResponseWriter: w, protocol: in.protocol, call: call}
			rw := &routeWriter{w: native, attempt: attempt, header: http.Header{}}
			h.ClientGateway(rw, inner)
			if !rw.decided {
				// The handler returned without a status: an empty 200.
				rw.WriteHeader(http.StatusOK)
			}
			if rw.committed {
				native.finish()
			}
			attempt.mu.Lock()
			outcome, upstream, hint, dispatched := attempt.outcome, attempt.upstream, attempt.retryAfter, attempt.dispatched
			attempt.mu.Unlock()
			if dispatched {
				asked = true
			}
			if outcome == routing.OutcomeNotGranted {
				x.mu.Lock()
				if x.firstMissingGrant == "" {
					x.firstMissingGrant = call.missingGrant
				}
				x.mu.Unlock()
				breakers.Release(key, admission)
			} else if outcome == routing.OutcomeBodyMismatch {
				breakers.Release(key, admission)
			} else {
				breakers.Record(key, x.policy, admission, outcome, upstream)
			}
			summary := usageledger.Attempt{SurfaceRef: backend.SurfaceRef, NativeModel: backend.NativeModel, Outcome: outcome, UpstreamStatus: upstream, DurationMS: time.Since(attempt.started).Milliseconds(), BreakerState: admission.State, Reason: choice.Reason}
			if hint != nil {
				ms := hint.Milliseconds()
				summary.RetryAfterMS = &ms
			}
			x.addAttempt(summary)
			if rw.committed {
				final = attempt
				break backends
			}
			held, heldWriter = attempt, rw
			retry := x.policy.Retries(outcome) && number < x.policy.MaxAttempts && (!x.stateful || outcome == routing.OutcomeConnectError)
			if retry {
				var hinted time.Duration
				if hint != nil {
					hinted = *hint
				}
				wait, ok := routing.Wait(routing.Backoff(number, x.policy.BaseBackoff, x.policy.MaxBackoff, rand.Float64), hinted, hint != nil, x.policy.MaxRetryAfter, x.remaining())
				if ok {
					telemetry.Metrics().RecordRoutingRetry(ctx, outcome)
					timer := time.NewTimer(wait)
					select {
					case <-timer.C:
					case <-ctx.Done():
						timer.Stop()
						break backends
					}
					continue
				}
			}
			if x.policy.FallsOver(outcome) {
				telemetry.Metrics().RecordRoutingFallback(ctx, outcome)
				continue backends
			}
			break backends
		}
	}
	if final == nil {
		if heldWriter != nil {
			// The exchange ends with the last answer that was held back:
			// the last upstream error, in the provider's own shape.
			if held.outcome == routing.OutcomeNotGranted || held.outcome == routing.OutcomeBodyMismatch {
				x.mu.Lock()
				call.missingGrant = x.firstMissingGrant
				x.mu.Unlock()
			}
			writer := &nativeErrorWriter{ResponseWriter: w, protocol: in.protocol, call: call}
			heldWriter.w = writer
			heldWriter.replay()
			writer.finish()
			final = held
		} else {
			// No backend could be asked (all circuit breakers open, the
			// deadline passed or no backend resolved).
			h.routeUnavailable(w, r, x)
			return
		}
	}
	x.finalize(final)
}

// routeUnavailable answers when no backend could be tried, and records it.
func (h *ConnectedHandler) routeUnavailable(w http.ResponseWriter, r *http.Request, x *routeExchange) {
	h.routeLedgerWithoutAttempt(w, r, x, http.StatusServiceUnavailable)
	x.decorate(w.Header())
	w.Header().Set("Retry-After", "1")
	refuseNative(w, x.in.protocol, http.StatusServiceUnavailable, "NO_BACKEND_AVAILABLE", fmt.Sprintf("model %q has no backend available right now; retry later", x.entry.Name))
}

// routeLedgerWithoutAttempt records an exchange that never reached a
// managed handler.
func (h *ConnectedHandler) routeLedgerWithoutAttempt(w http.ResponseWriter, r *http.Request, x *routeExchange, status int) {
	_, _, usage := h.beginUsage(&discardWriter{header: http.Header{}, status: status}, r, x.tenant, usageledger.PlaneLLM, x.backends[0].SurfaceRef)
	if usage == nil {
		x.span.SetStatus(codes.Error, "")
		return
	}
	usage.writer.WriteHeader(status)
	usage.set(func(rec *usageledger.Record) { rec.Provider = "" })
	attempt := &routeAttempt{route: x, backend: x.backends[0], outcome: routing.OutcomeBackendMissing}
	usage.route = attempt
	usage.finish()
	x.finalize(attempt)
}

// discardWriter records a status for a ledger capture that writes nothing.
type discardWriter struct {
	header http.Header
	status int
}

func (d *discardWriter) Header() http.Header         { return d.header }
func (d *discardWriter) WriteHeader(int)             {}
func (d *discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// deliver is called by an attempt's usage capture instead of submitting.
func (a *routeAttempt) deliver(d *deliveredRecord) {
	a.mu.Lock()
	a.record = d
	a.mu.Unlock()
	a.route.mu.Lock()
	a.route.delivered = append(a.route.delivered, d)
	a.route.mu.Unlock()
}

// finalize submits one ledger record for the exchange (the record of the
// attempt that answered the caller, with the routing summary) and ends the
// spans of the other attempts.
func (x *routeExchange) finalize(final *routeAttempt) {
	x.mu.Lock()
	delivered := append([]*deliveredRecord(nil), x.delivered...)
	attempts := append([]usageledger.Attempt(nil), x.attempts...)
	cache := x.cache
	x.mu.Unlock()
	final.mu.Lock()
	chosen := final.record
	final.mu.Unlock()
	var hooks *usageledger.HookSummary
	for _, d := range delivered {
		if d.record.Hooks != nil {
			if hooks == nil {
				hooks = &usageledger.HookSummary{Results: []usageledger.HookResult{}}
			}
			hooks.UninspectedParts += d.record.Hooks.UninspectedParts
			for _, res := range d.record.Hooks.Results {
				if len(hooks.Results) < usageledger.MaxHookResults {
					hooks.Results = append(hooks.Results, res)
				}
			}
		}
		if d != chosen {
			endAttemptSpan(d.span, d.record)
		}
	}
	if hooks != nil {
		hooks.Summarize()
	}
	x.span.SetAttributes(attribute.String(telemetry.AttrRoutingStrategy, x.policy.Strategy), attribute.Int(telemetry.AttrRoutingAttempts, len(attempts)))
	if final.backend.SurfaceRef != "" && final.outcome != routing.OutcomeBackendMissing {
		x.span.SetAttributes(attribute.String(telemetry.AttrRoutingBackend, final.backend.SurfaceRef))
	}
	if cache != nil && cache.info != nil {
		x.span.SetAttributes(attribute.String(telemetry.AttrCacheStatus, cache.info.Status))
	}
	if chosen == nil {
		return
	}
	record := chosen.record
	record.Attempts = attempts
	record.RoutingStrategy = x.policy.Strategy
	if final.outcome != routing.OutcomeBackendMissing {
		record.Backend = &usageledger.BackendRef{SurfaceRef: final.backend.SurfaceRef, NativeModel: final.backend.NativeModel}
	}
	if cache != nil && cache.info != nil {
		info := *cache.info
		record.Cache = &info
	}
	if hooks != nil {
		record.Hooks = hooks
	}
	// The record covers the whole exchange: every attempt and wait. Its
	// span is the exchange's "aiplex.route" span, the parent of every
	// attempt's CLIENT span.
	record.StartedAt = x.started.UTC()
	if record.FinishedAt.Before(record.StartedAt) {
		record.FinishedAt = record.StartedAt
	}
	record.LatencyMS = record.FinishedAt.Sub(record.StartedAt).Milliseconds()
	record.TraceID, record.SpanID = x.span.SpanContext().TraceID().String(), x.span.SpanContext().SpanID().String()
	if record.Status != usageledger.StatusOK {
		x.span.SetStatus(codes.Error, "")
	}
	chosen.submit(record)
}

// endAttemptSpan ends the CLIENT span of an attempt that did not answer the
// caller. Its metrics are the routing metrics; it adds no exchange metrics.
func endAttemptSpan(span trace.Span, record usageledger.Record) {
	if span == nil {
		return
	}
	span.SetName("attempt " + record.Operation)
	span.SetAttributes(attribute.String(telemetry.AttrRoutingBackend, record.ResourceID), attribute.Int("http.response.status_code", record.HTTPStatus))
	if record.Status != usageledger.StatusOK {
		span.SetStatus(codes.Error, "")
	}
	span.End(trace.WithTimestamp(record.FinishedAt))
}

// attemptSpanAttributes adds an attempt's routing attributes to its CLIENT
// span (called by the usage capture as the span ends).
func (a *routeAttempt) spanAttributes() []attribute.KeyValue {
	a.mu.Lock()
	defer a.mu.Unlock()
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.AttrRoutingBackend, a.backend.SurfaceRef),
		attribute.String(telemetry.AttrRoutingBreakerState, a.breaker.State),
		attribute.Int(telemetry.AttrRoutingAttempt, a.number),
	}
	if a.outcome != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrRoutingOutcome, a.outcome))
	}
	if a.retryAfter != nil {
		attrs = append(attrs, attribute.Int64(telemetry.AttrRoutingRetryAfterMS, a.retryAfter.Milliseconds()))
	}
	return attrs
}

// ── request hooks across attempts ─────────────────────────────────────────

// errRouteBodyMismatch refuses an attempt whose request differs from the
// first attempt's in more than the model name, so the hooked body cannot be
// reused without running the hooks again.
var errRouteBodyMismatch = &modelapi.HookRefusal{Status: http.StatusServiceUnavailable, Code: "ROUTE_BODY_MISMATCH", Message: "this backend needs a different request body; it was skipped"}

// withoutModel blanks the top-level model of a JSON body, when present.
func withoutModel(body []byte) ([]byte, string) {
	model, err := bodyModel(body)
	if err != nil {
		return body, ""
	}
	out, err := rewriteBodyModel(body, "")
	if err != nil {
		return body, ""
	}
	return out, model
}

// routedRequest runs the request hooks of a routed exchange once: the first
// attempt runs them (with the union of every backend's surface policies) and
// keeps the result; a later attempt reuses it, rewriting only the model.
func (m *modelHooks) routedRequest(ctx context.Context, x modelapi.HookExchange, body []byte, run func() ([]byte, bool, error)) ([]byte, bool, error) {
	rx := m.route
	rx.mu.Lock()
	prior := rx.hooks
	rx.mu.Unlock()
	if prior == nil {
		out, inspect, err := run()
		if err == nil && len(m.resolved.Hooks) > 0 {
			rx.mu.Lock()
			if rx.hooks == nil {
				rx.hooks = &routeHooks{in: append([]byte(nil), body...), out: append([]byte(nil), out...), inspect: inspect, m: *m}
			}
			rx.mu.Unlock()
		}
		return out, inspect, err
	}
	m.resolved, m.format, m.streaming, m.meta = prior.m.resolved, prior.m.format, prior.m.streaming, prior.m.meta
	m.loaded = true
	current, model := withoutModel(body)
	first, _ := withoutModel(prior.in)
	if !bytes.Equal(current, first) {
		if a := routeAttemptFrom(ctx); a != nil {
			a.set(func(a *routeAttempt) { a.bodyMismatch = true })
		}
		return nil, false, errRouteBodyMismatch
	}
	out := prior.out
	if model != "" {
		rewritten, err := rewriteBodyModel(out, model)
		if err != nil {
			return nil, false, errRouteBodyMismatch
		}
		out = rewritten
	}
	return append([]byte(nil), out...), prior.inspect, nil
}
