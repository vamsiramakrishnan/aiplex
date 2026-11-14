package modelapi

// WebSocket session relays (Gemini Live, OpenAI Realtime, OpenAI Responses
// WebSocket mode).
//
// Session metering contract. Today's execution model is one exchange = one
// admission + one terminal settlement. A WebSocket session maps onto it
// unchanged, with the smallest correct extension:
//
//   - Admission: one lease per session, prepared after the session's model is
//     known and before credentials or the upstream dial. Only the
//     request-count profile is accepted: a token-metered lease is aborted and
//     the session refused, because no session protocol here has a proven
//     exact, final usage report. One session is one admitted request. The
//     one exception is OpenAI Live voice on a surface with websocket.metering
//     "seconds", which also accepts a voice-seconds lease (live_voice.go).
//   - Observation: usage server events are forwarded to the lease and the
//     legacy observer as non-terminal advisory observations while the session
//     runs (cumulative snapshots for Live, per-response deltas for Realtime and
//     Responses).
//   - Settlement: exactly one terminal settlement at session end. With final
//     evidence (clean close, every started response finished with usage, no
//     activity after the last snapshot) a terminal observation carries the
//     session totals; otherwise a non-terminal observation labels the usage
//     (lower_bound / ambiguous / partial) and the lease finishes with an error.
//     Either way a request-count lease settles its one request and releases
//     its concurrency permit; it never retains exposure for a closed socket.
//   - Duration: the session is bounded by the gateway's MaxStreamDuration
//     (default 30 minutes, at most 24 hours); reaching it closes both sockets
//     and the usage is labelled a lower bound.
//
// Exposure bound: per session, one request against request budgets and rates,
// and one concurrency permit for at most MaxStreamDuration. Tokens are NOT
// bounded by quota on these paths (only by provider session limits and the
// duration bound); token usage reaches the ledger as advisory telemetry.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
)

type wsEffects struct {
	observations []UsageObservation
	limits       *providerlimits.Limits
}

// wsAdapter is a session protocol's observer. Calls are serialized.
type wsAdapter interface {
	// clientMessage validates one client text message before it is relayed.
	clientMessage(ctx context.Context, payload []byte, kind int) error
	// serverMessage observes one upstream text message before it is relayed.
	serverMessage(ctx context.Context, payload []byte) (wsEffects, error)
	// end returns the final session usage when final evidence exists,
	// otherwise nil and the bound label for what was observed.
	end(clean bool) (*UsageObservation, string)
}

var errSessionUsageBound = errors.New("session ended without final usage evidence")

func closeWebSocket(conn *websocket.Conn, code int, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
}

func closeWebSocketForExecution(conn *websocket.Conn, err error) {
	var status *execution.StatusError
	code, reason := websocket.CloseTryAgainLater, "execution control unavailable"
	if errors.As(err, &status) {
		reason = status.Code
		if status.Status != http.StatusTooManyRequests && status.Status != http.StatusServiceUnavailable {
			code = websocket.ClosePolicyViolation
		}
	}
	closeWebSocket(conn, code, reason)
}

// prepareSession admits one WebSocket session. Request-count leases are
// accepted, except for OpenAI Live voice on a surface that meters sessions by
// seconds, which accepts only a voice-seconds lease (live_voice.go); any
// other lease is aborted before credentials and the dial.
func (g *Gateway) prepareSession(r *http.Request, trusted contracts.RequestContext, op Operation, metadata Metadata, body []byte) (*executionRun, error) {
	lease, err := g.prepareExecution(r, trusted, op, metadata, body)
	if err != nil || lease == nil {
		return nil, err
	}
	run := &executionRun{lease: lease, ctx: r.Context()}
	refuse := func(message string) (*executionRun, error) {
		cleanup, cancel := executionContext(r.Context())
		defer cancel()
		run.closed = true
		if err := lease.Abort(cleanup); err != nil {
			return nil, err
		}
		return nil, &execution.StatusError{Status: http.StatusNotImplemented, Code: "UNSUPPORTED_EXECUTION_TRANSPORT", Message: message}
	}
	if g.secondsSession(op) {
		if !g.secondsRun(run, op) {
			return refuse("seconds-metered sessions require a voice-seconds profile binding")
		}
		return run, nil
	}
	if !requestCountRun(run) {
		return refuse("WebSocket sessions are metered by request count; token-metered profiles are not supported")
	}
	return run, nil
}

func cleanClose(err error) bool {
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway)
}

type wsEnd struct {
	clean    bool
	upstream bool
	err      error
	code     int
	reason   string
}

// pumpWebSocket relays both directions, feeds the adapter, and settles the
// session exactly once after both directions have stopped.
func (g *Gateway) pumpWebSocket(ctx context.Context, client, upstream *websocket.Conn, owner Owner, op Operation, adapter wsAdapter, run *executionRun, gate wsGate) {
	var mu, writeMu sync.Mutex
	writeClient := func(kind int, payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return client.WriteMessage(kind, payload)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			client.Close()
			upstream.Close()
		case <-done:
		}
	}()
	ends := make(chan wsEnd, 2)
	emit := func(value UsageObservation) error {
		value.OperationID = op.ID
		if run != nil {
			if err := run.observe(value); err != nil {
				return err
			}
			g.emitLegacy(run.ctx, owner, &value)
			return nil
		}
		g.emitLegacy(ctx, owner, &value)
		return nil
	}
	closeEnd := func(err error, upstreamSide bool) wsEnd {
		end := wsEnd{clean: cleanClose(err), upstream: upstreamSide, err: err, code: websocket.CloseAbnormalClosure}
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) {
			end.code, end.reason = closeErr.Code, closeErr.Text
		}
		return end
	}
	go func() {
		for {
			kind, payload, err := client.ReadMessage()
			if err != nil {
				ends <- closeEnd(err, false)
				return
			}
			mu.Lock()
			err = adapter.clientMessage(ctx, payload, kind)
			var reply []byte
			forward := true
			if err == nil && gate != nil {
				reply, forward, err = gate.client(ctx, payload, kind)
			}
			mu.Unlock()
			if err != nil {
				closeWebSocket(client, websocket.ClosePolicyViolation, "message denied")
				ends <- wsEnd{err: err, code: websocket.ClosePolicyViolation, reason: "message denied"}
				return
			}
			if reply != nil {
				if err = writeClient(websocket.TextMessage, reply); err != nil {
					ends <- wsEnd{err: err, code: websocket.CloseAbnormalClosure}
					return
				}
			}
			if !forward {
				continue
			}
			if err = upstream.WriteMessage(kind, payload); err != nil {
				ends <- wsEnd{err: err, code: websocket.CloseAbnormalClosure}
				return
			}
		}
	}()
	go func() {
		for {
			kind, payload, err := upstream.ReadMessage()
			if err != nil {
				ends <- closeEnd(err, true)
				return
			}
			if kind == websocket.TextMessage {
				mu.Lock()
				effects, err := adapter.serverMessage(ctx, payload)
				if err == nil {
					for _, value := range effects.observations {
						if err = emit(value); err != nil {
							break
						}
					}
				}
				var reply []byte
				if err == nil && gate != nil {
					reply, err = gate.server(ctx, payload)
				}
				mu.Unlock()
				if err != nil {
					code, reason := websocket.CloseInternalServerErr, "session observation failed"
					if errors.Is(err, errSessionPolicy) {
						code, reason = websocket.ClosePolicyViolation, "session token allowance or admission denied"
					}
					if reply != nil {
						_ = writeClient(websocket.TextMessage, reply)
					}
					ends <- wsEnd{err: err, upstream: true, code: code, reason: reason}
					return
				}
				if effects.limits != nil && g.options.ObserveLimits != nil {
					g.options.ObserveLimits(ctx, owner, *effects.limits)
				}
			}
			if err = writeClient(kind, payload); err != nil {
				ends <- wsEnd{err: err, upstream: true, code: websocket.CloseAbnormalClosure}
				return
			}
		}
	}()
	first := <-ends
	// Propagate the ending to the other side, then stop both directions.
	code, reason := first.code, first.reason
	if code == websocket.CloseAbnormalClosure || code == websocket.CloseNoStatusReceived || code == 0 {
		code, reason = websocket.CloseGoingAway, ""
	}
	if first.upstream {
		closeWebSocket(client, code, reason)
	} else {
		closeWebSocket(upstream, code, reason)
	}
	client.Close()
	upstream.Close()
	<-ends
	close(done)
	clean := first.clean && ctx.Err() == nil
	mu.Lock()
	final, bound := adapter.end(clean)
	if gate != nil {
		gate.end(clean)
	}
	mu.Unlock()
	if final != nil {
		final.Terminal = true
		final.OperationID = op.ID
		if run != nil {
			if err := run.observe(*final); err != nil {
				_ = run.finish(execution.Result{StatusCode: http.StatusSwitchingProtocols, ResponseComplete: true, Err: err})
				return
			}
			_ = run.finish(execution.Result{StatusCode: http.StatusSwitchingProtocols, ResponseComplete: true, Terminal: true})
			g.emitLegacy(run.ctx, owner, final)
			return
		}
		g.emitLegacy(ctx, owner, final)
		return
	}
	label := UsageObservation{Source: "upstream", SurfaceID: g.surface.ID, OperationID: op.ID, Counters: map[string]int64{}, Cumulative: true, Bound: bound}
	if run != nil {
		_ = run.observe(label)
		// The session (one request) happened; its token usage is unproven.
		_ = run.finish(execution.Result{StatusCode: http.StatusSwitchingProtocols, ResponseComplete: true, Err: errSessionUsageBound})
		g.emitLegacy(run.ctx, owner, &label)
		return
	}
	g.emitLegacy(BackgroundContext(ctx), owner, &label)
}

// BackgroundContext keeps values but not cancellation, for session-end
// observations after the request context ended.
func BackgroundContext(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

// serveProviderWebSocket relays a provider WebSocket operation (OpenAI
// Realtime, Responses WebSocket mode). The model (when the protocol selects it
// at connect time), authorization and request-count admission are resolved
// before the upgrade, so refusals are ordinary HTTP errors.
func (g *Gateway) serveProviderWebSocket(w http.ResponseWriter, r *http.Request, trusted contracts.RequestContext, owner Owner, op Operation) {
	query := r.URL.Query()
	upstreamQuery := url.Values{}
	metadata := Metadata{Transport: WebSocket}
	for key := range query {
		if key != "model" {
			// call_id (SIP sideband) and any other selector are not
			// ownership-scoped by this surface.
			errorcode.Refuse(w, http.StatusBadRequest, "INVALID_REQUEST", "query parameter "+key+" is not supported by this surface")
			return
		}
	}
	if op.ID == "openai.v1.realtime" || op.ID == openAILiveOperation {
		if len(query["model"]) != 1 || !g.allowedModel(query.Get("model")) {
			errorcode.Refuse(w, http.StatusForbidden, "MODEL_NOT_ALLOWED", "model is not allowed")
			return
		}
		metadata.Model = query.Get("model")
		if op.ID == "openai.v1.realtime" {
			upstreamQuery.Set("model", metadata.Model)
		}
	} else if len(query["model"]) > 0 {
		errorcode.Refuse(w, http.StatusBadRequest, "INVALID_REQUEST", "the model is selected per response.create event")
		return
	}
	headers, e := g.providerRequestHeaders(r.Header)
	if e != nil {
		writeProviderRequestError(w, e)
		return
	}
	headers.Del("Content-Type")
	if e = g.options.Authorize(r.Context(), trusted, g.Surface(), op, metadata); e != nil {
		errorcode.Refuse(w, http.StatusForbidden, "ACTION_DENIED", "operation denied")
		return
	}
	if !g.sessionHooks(w, r, op, metadata) {
		return
	}
	var gate *responseGate
	var run *executionRun
	if g.surface.WebSocket.tokenMetering() && g.options.Execution != nil && op.ID != openAILiveOperation {
		// Per-response admission: no session lease; each response.create is
		// admitted (see session_metering.go).
		gate = &responseGate{gateway: g, request: r, trusted: trusted, owner: owner, op: op, realtime: op.ID == "openai.v1.realtime", model: metadata.Model}
		// The capacity hook runs once, at session admission (before the
		// upgrade); per-response admissions skip it.
		if g.options.Admit != nil {
			if e = g.options.Admit(r.Context(), trusted, op, metadata); e != nil {
				writeExecutionError(w, e)
				return
			}
		}
	} else if run, e = g.prepareSession(r, trusted, op, metadata, nil); e != nil {
		writeExecutionError(w, e)
		return
	}
	if run != nil {
		defer run.close()
		if receipt, ok := run.lease.(execution.Receipt); ok {
			id := receipt.AttemptID()
			if id != "" && len(id) <= 1024 && !strings.ContainsAny(id, "\r\n") {
				w.Header().Set(execution.AttemptHeader, id)
			}
		}
	}
	// The credential is applied to a request for the HTTPS form of the
	// upstream URL, and only its resulting headers are used for the dial.
	httpsURL := strings.TrimSuffix(g.surface.Origin, "/") + r.URL.EscapedPath()
	if encoded := upstreamQuery.Encode(); encoded != "" {
		httpsURL += "?" + encoded
	}
	signing, e := http.NewRequestWithContext(r.Context(), http.MethodGet, httpsURL, nil)
	if e != nil {
		errorcode.Refuse(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid upstream request")
		return
	}
	signing.Header = headers
	connection, e := (&websocket.Upgrader{HandshakeTimeout: 10 * time.Second, Error: handshakeRefusal}).Upgrade(w, r, upgradeResponseHeader(w))
	if e != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(g.options.MaxBodyBytes)
	if e = run.beforeDispatch(r.Context()); e != nil {
		closeWebSocketForExecution(connection, e)
		return
	}
	if e = g.options.Credential.Authenticate(r.Context(), signing); e != nil {
		_ = run.finish(execution.Result{Err: e})
		closeWebSocket(connection, websocket.CloseTryAgainLater, "upstream identity unavailable")
		return
	}
	dialContext := r.Context()
	seconds := g.secondsRun(run, op)
	connected := func() bool { return true }
	if seconds {
		// As for an HTTP exchange (dispatch.go do): a connection obtained
		// means the handshake may have been sent.
		dialContext, connected = execution.ConnectionTrace(dialContext)
	}
	upstream, response, e := g.options.LiveDialer.DialContext(dialContext, websocketEndpoint(g.surface.Origin, r.URL.EscapedPath(), upstreamQuery.Encode()), signing.Header)
	if response != nil {
		if g.options.ObserveLimits != nil {
			g.options.ObserveLimits(r.Context(), owner, providerlimits.Parse(g.surface.Provider.ID, response.StatusCode, response.Header, time.Now()))
		}
		if e != nil && response.Body != nil {
			response.Body.Close()
		}
	}
	if e != nil {
		result := execution.Result{Err: e}
		if seconds {
			result = g.secondsDialResult(e, connected(), response)
		}
		_ = run.finish(result)
		closeWebSocket(connection, websocket.CloseTryAgainLater, "upstream unavailable")
		return
	}
	defer upstream.Close()
	upstream.SetReadLimit(g.options.MaxBodyBytes)
	var adapter wsAdapter
	var sessionGate wsGate
	if gate != nil {
		sessionGate = gate
	}
	switch op.ID {
	case "openai.v1.realtime":
		adapter = &realtimeAdapter{gateway: g, model: metadata.Model, pending: map[string]bool{}, done: map[string]bool{}, now: time.Now}
	case openAILiveOperation:
		// Live voice: the model is pinned by ?model= and checked against
		// session.start (live_voice.go); admission is unchanged.
		adapter = newLiveVoiceAdapter(g, metadata.Model)
	default:
		adapter = &responsesSocketAdapter{gateway: g, owner: owner, trusted: trusted, op: op, pending: map[string]bool{}, done: map[string]bool{}}
	}
	g.pumpWebSocket(r.Context(), connection, upstream, owner, op, adapter, run, sessionGate)
}

// ---- OpenAI Realtime ----

// realtimeAdapter observes OpenAI Realtime server events.
//
// response.done (always emitted, whatever the final status) carries the
// response's usage: per response, not cumulative, so the session total is the
// sum over distinct response ids. input_tokens includes cached_tokens.
// conversation.item.input_audio_transcription.completed carries a separate
// meter billed at the transcription model's rate: it is observed under
// distinct transcription_* counters and never merged into the session's
// token counters; a duration-billed transcription reports seconds, kept as
// transcription_audio_milliseconds, which the token ledger cannot hold (the
// session is then labelled partial). rate_limits.updated feeds the provider
// limit observation and is not usage.
type realtimeAdapter struct {
	gateway      *Gateway
	model        string
	pending      map[string]bool
	done         map[string]bool
	totals       map[string]int64
	missingUsage bool
	nonToken     bool
	now          func() time.Time
}

func (a *realtimeAdapter) clientMessage(_ context.Context, payload []byte, kind int) error {
	if kind != websocket.TextMessage {
		return nil
	}
	var event struct {
		Type    string `json:"type"`
		Session *struct {
			Model *string `json:"model"`
		} `json:"session"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return nil // the provider rejects malformed events in-band
	}
	if event.Type == "session.update" && event.Session != nil && event.Session.Model != nil && *event.Session.Model != a.model {
		return fmt.Errorf("the session model is pinned at connect time")
	}
	return nil
}

func (a *realtimeAdapter) serverMessage(_ context.Context, payload []byte) (wsEffects, error) {
	var event struct {
		Type     string `json:"type"`
		Response *struct {
			ID    string          `json:"id"`
			Usage json.RawMessage `json:"usage"`
		} `json:"response"`
		Usage json.RawMessage `json:"usage"`
	}
	effects := wsEffects{}
	if json.Unmarshal(payload, &event) != nil {
		return effects, nil
	}
	switch event.Type {
	case "rate_limits.updated":
		if limits, ok := providerlimits.ParseRealtimeEvent(providerlimits.OpenAI, payload, a.now()); ok {
			effects.limits = &limits
		}
	case "response.created":
		if event.Response != nil && event.Response.ID != "" && !a.done[event.Response.ID] {
			a.pending[event.Response.ID] = true
		}
	case "response.done":
		if event.Response == nil || event.Response.ID == "" || a.done[event.Response.ID] {
			return effects, nil
		}
		delete(a.pending, event.Response.ID)
		a.done[event.Response.ID] = true
		counters := openAIRealtimeUsage(event.Response.Usage)
		if counters == nil {
			a.missingUsage = true
			return effects, nil
		}
		if a.totals == nil {
			a.totals = map[string]int64{}
		}
		for key, n := range counters {
			if a.totals[key] > math.MaxInt64-n {
				a.missingUsage = true
				return effects, nil
			}
			a.totals[key] += n
		}
		// One session carries many responses: per-response ids are not the
		// session's provider resource, so they are not attached.
		effects.observations = append(effects.observations, UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, Counters: counters})
	case "conversation.item.input_audio_transcription.completed":
		counters, nonToken := transcriptionUsage(event.Usage)
		if counters == nil {
			return effects, nil
		}
		a.nonToken = a.nonToken || nonToken
		if a.totals == nil {
			a.totals = map[string]int64{}
		}
		for key, n := range counters {
			if a.totals[key] > math.MaxInt64-n {
				return effects, nil
			}
			a.totals[key] += n
		}
		effects.observations = append(effects.observations, UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, Counters: counters})
	}
	return effects, nil
}

func (a *realtimeAdapter) end(clean bool) (*UsageObservation, string) {
	if !clean || len(a.pending) > 0 || a.missingUsage {
		return nil, execution.BoundLowerBound
	}
	if a.nonToken {
		return nil, execution.BoundPartial
	}
	totals := a.totals
	if totals == nil {
		totals = map[string]int64{}
	}
	return &UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, Counters: cloneCounters(totals), Cumulative: true}, ""
}

func whole(raw json.RawMessage) (int64, bool) {
	var n int64
	if raw == nil || json.Unmarshal(raw, &n) != nil || n < 0 || n > 1<<53 {
		return 0, false
	}
	return n, true
}

// openAIRealtimeUsage maps a Realtime response.usage object; nil when absent.
func openAIRealtimeUsage(raw json.RawMessage) map[string]int64 {
	var usage map[string]json.RawMessage
	if raw == nil || json.Unmarshal(raw, &usage) != nil || usage == nil {
		return nil
	}
	counters := map[string]int64{}
	for native, target := range map[string]string{"input_tokens": "input_tokens", "output_tokens": "output_tokens", "total_tokens": "total_tokens"} {
		if n, ok := whole(usage[native]); ok {
			counters[target] = n
		}
	}
	var details struct {
		CachedTokens json.RawMessage `json:"cached_tokens"`
	}
	if usage["input_token_details"] != nil && json.Unmarshal(usage["input_token_details"], &details) == nil {
		if n, ok := whole(details.CachedTokens); ok {
			counters["cached_tokens"] = n
		}
	}
	if len(counters) == 0 {
		return nil
	}
	return counters
}

// transcriptionUsage maps a transcription usage union: {type:"tokens"} or
// {type:"duration", seconds}. nonToken reports a seconds meter.
func transcriptionUsage(raw json.RawMessage) (map[string]int64, bool) {
	var usage struct {
		Type         string          `json:"type"`
		InputTokens  json.RawMessage `json:"input_tokens"`
		OutputTokens json.RawMessage `json:"output_tokens"`
		TotalTokens  json.RawMessage `json:"total_tokens"`
		Seconds      *float64        `json:"seconds"`
	}
	if raw == nil || json.Unmarshal(raw, &usage) != nil {
		return nil, false
	}
	switch usage.Type {
	case "tokens":
		counters := map[string]int64{}
		for key, value := range map[string]json.RawMessage{"transcription_input_tokens": usage.InputTokens, "transcription_output_tokens": usage.OutputTokens, "transcription_total_tokens": usage.TotalTokens} {
			if n, ok := whole(value); ok {
				counters[key] = n
			}
		}
		if len(counters) == 0 {
			return nil, false
		}
		return counters, false
	case "duration":
		if usage.Seconds == nil || *usage.Seconds < 0 || *usage.Seconds > 1<<40 {
			return nil, false
		}
		// Milliseconds, rounded up: never under-reports the billed duration.
		return map[string]int64{"transcription_audio_milliseconds": int64(math.Ceil(*usage.Seconds * 1000))}, true
	}
	return nil, false
}

// ---- OpenAI Responses WebSocket mode ----

// responsesSocketAdapter observes Responses WebSocket mode. Server events are
// the Responses streaming events: usage is on the terminal response.completed
// (also read from failed/incomplete when present), one report per response id,
// summed over the responses of the connection. Each client response.create is
// authorized like a POST /v1/responses: its model must be allowed and
// authorized, a previous_response_id must be owned by the caller and provider
// conversations are refused. Response ids are bound to the caller as they are
// created.
type responsesSocketAdapter struct {
	gateway      *Gateway
	owner        Owner
	trusted      contracts.RequestContext
	op           Operation
	pending      map[string]bool
	done         map[string]bool
	totals       map[string]int64
	missingUsage bool
}

func (a *responsesSocketAdapter) clientMessage(ctx context.Context, payload []byte, kind int) error {
	if kind != websocket.TextMessage {
		return fmt.Errorf("binary frames are not part of this protocol")
	}
	if err := validateJSON(payload); err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(payload, &doc) != nil {
		return fmt.Errorf("invalid client event")
	}
	var eventType string
	_ = json.Unmarshal(doc["type"], &eventType)
	if eventType != "response.create" {
		return nil
	}
	for key := range doc {
		for _, reserved := range []string{"model", "previous_response_id", "conversation", "type"} {
			if strings.EqualFold(key, reserved) && key != reserved {
				return fmt.Errorf("ambiguous security field spelling")
			}
		}
	}
	var model string
	if json.Unmarshal(doc["model"], &model) != nil || !a.gateway.allowedModel(model) {
		return fmt.Errorf("model is not allowed")
	}
	if raw := doc["conversation"]; raw != nil && strings.TrimSpace(string(raw)) != "null" {
		return fmt.Errorf("provider conversations are not ownership-scoped by this surface")
	}
	metadata := Metadata{Model: model, Transport: WebSocket}
	if raw := doc["previous_response_id"]; raw != nil && strings.TrimSpace(string(raw)) != "null" {
		var id string
		if json.Unmarshal(raw, &id) != nil || !validSegment(id) || a.gateway.options.Ownership == nil {
			return fmt.Errorf("invalid previous response")
		}
		if a.gateway.options.Ownership.Check(ctx, a.owner, Resource{"response", id}) != nil {
			return fmt.Errorf("previous response ownership denied")
		}
		metadata.References = append(metadata.References, Resource{"response", id})
	}
	return a.gateway.options.Authorize(ctx, a.trusted, a.gateway.Surface(), a.op, metadata)
}

func (a *responsesSocketAdapter) serverMessage(ctx context.Context, payload []byte) (wsEffects, error) {
	var event struct {
		Type     string `json:"type"`
		Response *struct {
			ID    string          `json:"id"`
			Usage json.RawMessage `json:"usage"`
		} `json:"response"`
	}
	effects := wsEffects{}
	if json.Unmarshal(payload, &event) != nil || event.Response == nil || event.Response.ID == "" {
		return effects, nil
	}
	id := event.Response.ID
	switch event.Type {
	case "response.created":
		if !validSegment(id) || a.gateway.options.Ownership == nil {
			return effects, fmt.Errorf("invalid response identity")
		}
		if err := a.gateway.options.Ownership.Bind(ctx, a.owner, Resource{"response", id}); err != nil {
			return effects, err
		}
		if !a.done[id] {
			a.pending[id] = true
		}
	case "response.completed", "response.failed", "response.incomplete", "response.cancelled":
		if a.done[id] {
			return effects, nil
		}
		delete(a.pending, id)
		a.done[id] = true
		value, err := a.gateway.inspectJSON(ctx, a.owner, Operation{ID: "openai.v1.responses"}, payload, true, true)
		if err != nil || value.usage == nil {
			a.missingUsage = true
			return effects, nil
		}
		counters := value.usage.Counters
		if a.totals == nil {
			a.totals = map[string]int64{}
		}
		for key, n := range counters {
			if a.totals[key] > math.MaxInt64-n {
				a.missingUsage = true
				return effects, nil
			}
			a.totals[key] += n
		}
		// Responses on one connection may use different models and ids, so
		// neither is attached to the session-level observation.
		effects.observations = append(effects.observations, UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, Counters: cloneCounters(counters)})
	}
	return effects, nil
}

func (a *responsesSocketAdapter) end(clean bool) (*UsageObservation, string) {
	if !clean || len(a.pending) > 0 || a.missingUsage {
		return nil, execution.BoundLowerBound
	}
	totals := a.totals
	if totals == nil {
		totals = map[string]int64{}
	}
	return &UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, Counters: cloneCounters(totals), Cumulative: true}, ""
}

// handshakeRefusal answers a failed WebSocket handshake as the upgrader does
// by default (its status text, Sec-Websocket-Version: 13), and marks it as
// AIPlex's own refusal.
func handshakeRefusal(w http.ResponseWriter, _ *http.Request, status int, _ error) {
	w.Header().Set("Sec-Websocket-Version", "13")
	errorcode.Refuse(w, status, "WEBSOCKET_HANDSHAKE_REFUSED", http.StatusText(status))
}

// upgradeResponseHeader holds the request ids already set on w. The upgrader
// writes the 101 itself, so headers set on w before it are not sent unless they
// are passed to Upgrade.
func upgradeResponseHeader(w http.ResponseWriter) http.Header {
	out := http.Header{}
	for _, name := range []string{"X-Request-Id", "X-Aiplex-Request-Id"} {
		if v := w.Header().Get(name); v != "" {
			out.Set(name, v)
		}
	}
	return out
}
