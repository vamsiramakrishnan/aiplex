package modelapi

// Token-metered WebSocket sessions.
//
// A surface opts in with websocket.metering = "token". The default
// ("session") keeps the request-count session contract in websocket.go.
//
// OpenAI Realtime and Responses WebSocket mode (per-response admission):
// every client response.create is admitted as its own exchange before it is
// relayed: a lease is prepared (budget, rate, concurrency, trusted exposure
// bounds), its dispatch is claimed, and only then is the event forwarded. One
// response may be in flight at a time; a second response.create, a denied
// admission or an unbounded request is answered with the protocol's own error
// event and dropped, and the session stays open. response.created binds the
// in-flight lease; the terminal event (Realtime response.done with status
// completed, Responses response.completed) settles it on its exact usage
// before the event reaches the client. Cancelled, failed or incomplete
// responses, a disconnect, or a response without usage retain the hold as
// unknown, exactly like an incomplete HTTP stream. A response.create the
// provider rejects with a validation error before response.created releases
// it. Realtime turn detection can create responses without a client
// response.create, so a token-metered Realtime session must first send a
// session.update that disables automatic responses (turn_detection null or
// create_response false); a response the gateway did not admit closes the
// session. Exposure: each response is bounded by its own admitted hold;
// nothing is consumed outside an admitted hold.
//
// Gemini Live (renewable allowance): Live usageMetadata reports
// session-cumulative snapshots whose composition is not fully documented, so
// no per-turn bound exists. The session holds a window lease of
// websocket.token_allowance tokens per mapped meter at setup (before the
// upstream dial). When any mapped counter has consumed half of the current
// window, the current window settles exactly on the snapshot delta and the
// next window is admitted (windows never overlap: one concurrency permit per
// session). If renewal is denied the gateway sends a goAway and closes both
// sockets with a policy-violation close. A clean close with a final snapshot
// (no activity after it) settles the last window; an interrupted or stale
// session retains the open window as unknown.
// Worst-case exposure beyond admitted holds is one allowance per mapped meter:
// a snapshot jump larger than half an allowance settles as a recorded overrun
// (debt that blocks further admissions), and after a denied renewal the usage
// generated between that snapshot and the close (at most the remaining half
// window plus in-flight generation) has no hold; it reaches the ledger only
// as lower_bound session telemetry.
// Resumed sessions are refused in token mode: counter continuity across
// resumption is not documented.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// Session metering modes for WebSocketSessions.Metering.
const (
	SessionMeteringSession = "session"
	SessionMeteringToken   = "token"
	// Defaults and bounds of the per-surface session settings.
	DefaultMaxSessionSeconds = 1800
	MinMaxSessionSeconds     = 60
	MaxMaxSessionSeconds     = 86400
	DefaultTokenAllowance    = 32768
	MinTokenAllowance        = 1024
	MaxTokenAllowance        = 4 << 20
)

// WebSocketSessions is the operator's per-surface session configuration.
type WebSocketSessions struct {
	// Metering is "session" (default: one request-count admission per
	// session) or "token" (per-response admission for OpenAI Realtime and
	// Responses WebSocket mode; renewable allowance for Gemini Live).
	Metering string `json:"metering,omitempty"`
	// MaxSessionSeconds bounds every WebSocket session on the surface
	// (default: the gateway's maximum stream duration, 1800 seconds unless
	// configured; 60..86400).
	MaxSessionSeconds int64 `json:"max_session_seconds,omitempty"`
	// TokenAllowance is the Gemini Live allowance window, held per mapped
	// meter (token metering only; default 32768; 1024..4194304).
	TokenAllowance int64 `json:"token_allowance,omitempty"`
}

func (w *WebSocketSessions) validate() error {
	if w == nil {
		return nil
	}
	switch w.Metering {
	case "", SessionMeteringSession, SessionMeteringToken:
	default:
		return fmt.Errorf("websocket.metering must be %q or %q", SessionMeteringSession, SessionMeteringToken)
	}
	if w.MaxSessionSeconds != 0 && (w.MaxSessionSeconds < MinMaxSessionSeconds || w.MaxSessionSeconds > MaxMaxSessionSeconds) {
		return fmt.Errorf("websocket.max_session_seconds must be between %d and %d", MinMaxSessionSeconds, MaxMaxSessionSeconds)
	}
	if w.TokenAllowance != 0 && (w.TokenAllowance < MinTokenAllowance || w.TokenAllowance > MaxTokenAllowance) {
		return fmt.Errorf("websocket.token_allowance must be between %d and %d", MinTokenAllowance, MaxTokenAllowance)
	}
	if w.TokenAllowance != 0 && w.Metering != SessionMeteringToken {
		return fmt.Errorf("websocket.token_allowance requires metering %q", SessionMeteringToken)
	}
	return nil
}

func (w *WebSocketSessions) maxSession(fallback time.Duration) time.Duration {
	if w == nil || w.MaxSessionSeconds == 0 {
		return fallback
	}
	return time.Duration(w.MaxSessionSeconds) * time.Second
}

func (w *WebSocketSessions) tokenMetering() bool {
	return w != nil && w.Metering == SessionMeteringToken
}

// SessionAllowance is the Live allowance window the quota host holds per
// mapped meter, or 0 when the surface does not meter sessions by token.
func (s Surface) SessionAllowance() int64 {
	if !s.WebSocket.tokenMetering() {
		return 0
	}
	if s.WebSocket.TokenAllowance == 0 {
		return DefaultTokenAllowance
	}
	return s.WebSocket.TokenAllowance
}

func websocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// wsGate enforces token metering on a relayed session. Calls are serialized
// with the adapter.
type wsGate interface {
	// client decides a client message before relay: forward it, or answer
	// the client with reply and drop it; an error closes the session.
	client(ctx context.Context, payload []byte, kind int) (reply []byte, forward bool, err error)
	// server runs on an upstream text message before it reaches the client;
	// settlement is acknowledged first. reply (with an error) is sent to the
	// client before the session closes.
	server(ctx context.Context, payload []byte) (reply []byte, err error)
	// end settles or retains whatever is still open.
	end(clean bool)
}

var errSessionPolicy = errors.New("session metering policy violated")

// sessionLeaseRequest derives a distinct, deterministic attempt key per
// admitted exchange from a caller-supplied session key.
func sessionLeaseRequest(r *http.Request, n int) *http.Request {
	clone := r.Clone(r.Context())
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		derived := key + "/ws/" + strconv.Itoa(n)
		if len(derived) > 256 {
			clone.Header.Del("Idempotency-Key")
		} else {
			clone.Header.Set("Idempotency-Key", derived)
		}
	}
	return clone
}

type sessionExchangeKey struct{}

// sessionExchange marks a lease prepared inside an admitted session (a
// Realtime/Responses response or a later Live allowance window). The
// capacity hook (Options.Admit) runs once, at session admission, not again
// for these.
func sessionExchange(ctx context.Context) bool {
	marked, _ := ctx.Value(sessionExchangeKey{}).(bool)
	return marked
}

func (g *Gateway) admitSessionExchange(r *http.Request, trusted contracts.RequestContext, op Operation, model string, body []byte, n int, dispatched string) (*executionRun, error) {
	request := sessionLeaseRequest(r, n)
	if n > 0 || op.ID != "live.connect" {
		request = request.WithContext(context.WithValue(request.Context(), sessionExchangeKey{}, true))
	}
	lease, err := g.prepareExecution(request, trusted, op, Metadata{Model: model, Transport: WebSocket}, body)
	if err != nil {
		return nil, err
	}
	if lease == nil {
		return nil, nil
	}
	run := &executionRun{lease: lease, ctx: r.Context(), dispatchedModel: dispatched}
	if err = run.beforeDispatch(r.Context()); err != nil {
		run.close()
		return nil, err
	}
	return run, nil
}

// ---- OpenAI Realtime and Responses WebSocket mode ----

type responseGate struct {
	gateway  *Gateway
	request  *http.Request
	trusted  contracts.RequestContext
	owner    Owner
	op       Operation
	realtime bool
	model    string
	armed    bool
	n        int
	pending  *executionRun
	active   *executionRun
	activeID string
}

func statusOf(err error) (int, string, string) {
	var status *execution.StatusError
	if errors.As(err, &status) {
		return status.Status, status.Code, status.Message
	}
	return http.StatusServiceUnavailable, "EXECUTION_CONTROL_UNAVAILABLE", "execution control unavailable"
}

// errorEvent renders the protocol's native error event.
func (a *responseGate) errorEvent(clientEventID string, status int, code, message string) []byte {
	kind := "invalid_request_error"
	if status == http.StatusTooManyRequests {
		kind = "rate_limit_error"
	}
	if a.realtime {
		a.n++
		event := map[string]any{"type": "error", "event_id": "aiplex_" + strconv.Itoa(a.n), "error": map[string]any{"type": kind, "code": code, "message": message, "param": nil, "event_id": nullable(clientEventID)}}
		data, _ := json.Marshal(event)
		return data
	}
	data, _ := json.Marshal(map[string]any{"type": "error", "status": status, "error": map[string]any{"type": kind, "code": code, "message": message}})
	return data
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// realtimeAutoResponses reports whether a Realtime session.update leaves
// automatic response creation on (enables), turns it off (disables), or does
// not mention turn detection. Both the GA (session.audio.input.turn_detection)
// and beta (session.turn_detection) locations are read.
func realtimeAutoResponses(payload []byte) (mentioned, enabled bool, err error) {
	var event struct {
		Session map[string]json.RawMessage `json:"session"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return false, false, fmt.Errorf("invalid session.update")
	}
	var locations []json.RawMessage
	if raw, ok := event.Session["turn_detection"]; ok {
		locations = append(locations, raw)
	}
	if raw := event.Session["audio"]; raw != nil {
		var audio struct {
			Input map[string]json.RawMessage `json:"input"`
		}
		if json.Unmarshal(raw, &audio) != nil {
			return false, false, fmt.Errorf("invalid session audio")
		}
		if detection, ok := audio.Input["turn_detection"]; ok {
			locations = append(locations, detection)
		}
	}
	for _, raw := range locations {
		mentioned = true
		if strings.TrimSpace(string(raw)) == "null" {
			continue
		}
		var detection struct {
			CreateResponse *bool `json:"create_response"`
		}
		if json.Unmarshal(raw, &detection) != nil {
			return true, true, nil
		}
		// create_response defaults to true when turn detection is on.
		if detection.CreateResponse == nil || *detection.CreateResponse {
			enabled = true
		}
	}
	return mentioned, enabled, nil
}

func (a *responseGate) client(ctx context.Context, payload []byte, kind int) ([]byte, bool, error) {
	if kind != websocket.TextMessage {
		if a.realtime && !a.armed {
			return a.errorEvent("", 400, "AUTOMATIC_RESPONSES_ENABLED", "token-metered sessions must disable automatic responses first"), false, nil
		}
		return nil, true, nil
	}
	var event struct {
		Type     string          `json:"type"`
		EventID  string          `json:"event_id"`
		Model    string          `json:"model"`
		StreamID json.RawMessage `json:"stream_id"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return a.errorEvent("", 400, "INVALID_EVENT", "client event must be a JSON object"), false, nil
	}
	if a.realtime && event.Type == "session.update" {
		mentioned, enabled, err := realtimeAutoResponses(payload)
		if err != nil || enabled {
			return a.errorEvent(event.EventID, 400, "AUTOMATIC_RESPONSES_ENABLED", "token-metered sessions admit each response.create; automatic responses (turn_detection create_response) must stay disabled"), false, nil
		}
		if mentioned {
			a.armed = true
		}
		return nil, true, nil
	}
	if a.realtime && !a.armed {
		return a.errorEvent(event.EventID, 400, "AUTOMATIC_RESPONSES_ENABLED", "send a session.update with turn_detection null or create_response false before any other event"), false, nil
	}
	if event.Type != "response.create" {
		return nil, true, nil
	}
	if !a.realtime && len(event.StreamID) > 0 && strings.TrimSpace(string(event.StreamID)) != "null" {
		return a.errorEvent(event.EventID, 400, "CONCURRENT_RESPONSES_UNSUPPORTED", "token-metered sessions run one response at a time; stream_id lanes are not admitted"), false, nil
	}
	if a.pending != nil || a.active != nil {
		return a.errorEvent(event.EventID, 409, "RESPONSE_IN_PROGRESS", "a response is already in progress; wait for its terminal event"), false, nil
	}
	model := a.model
	if !a.realtime {
		model = event.Model
	}
	a.n++
	run, err := a.gateway.admitSessionExchange(a.request, a.trusted, a.op, model, payload, a.n, a.model)
	if err != nil {
		status, code, message := statusOf(err)
		return a.errorEvent(event.EventID, status, code, message), false, nil
	}
	a.pending = run
	return nil, true, nil
}

func (a *responseGate) server(ctx context.Context, payload []byte) ([]byte, error) {
	var event struct {
		Type     string `json:"type"`
		Status   int    `json:"status"`
		Response *struct {
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Usage  json.RawMessage `json:"usage"`
		} `json:"response"`
		Error *struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return nil, nil
	}
	id := ""
	if event.Response != nil {
		id = event.Response.ID
	}
	switch event.Type {
	case "response.created":
		if a.pending == nil || a.active != nil {
			// Not admitted by the gateway (automatic response or a lane).
			return nil, fmt.Errorf("%w: unadmitted response %s", errSessionPolicy, id)
		}
		a.active, a.activeID, a.pending = a.pending, id, nil
		return nil, nil
	case "response.done", "response.completed", "response.failed", "response.incomplete", "response.cancelled":
		if (event.Type == "response.done") != a.realtime || a.active == nil || id != a.activeID {
			if a.active == nil && a.pending == nil {
				return nil, fmt.Errorf("%w: unadmitted response %s", errSessionPolicy, id)
			}
			return nil, nil
		}
		run := a.active
		a.active, a.activeID = nil, ""
		completed := event.Type == "response.completed" || (a.realtime && event.Response.Status == "completed")
		if !completed {
			run.finish(execution.Result{ResponseComplete: true, Err: fmt.Errorf("response ended %s without settled usage", event.Type)})
			return nil, nil
		}
		value, err := a.terminalUsage(ctx, payload, event.Response.Usage)
		if err != nil {
			run.finish(execution.Result{ResponseComplete: true, Err: err})
			return nil, err
		}
		run.modelIdentity(value)
		if err = run.observe(*value); err != nil {
			run.finish(execution.Result{ResponseComplete: true, Err: err})
			return nil, err
		}
		if err = run.finish(execution.Result{StatusCode: http.StatusOK, ResponseComplete: true, Terminal: true}); err != nil {
			return nil, err
		}
		return nil, nil
	case "error":
		if a.pending == nil {
			return nil, nil
		}
		// The provider refused the admitted response.create before creating
		// a response: a request validation error is not processed.
		run := a.pending
		a.pending = nil
		rejected := (a.realtime && event.Error != nil && event.Error.Type == "invalid_request_error") || (!a.realtime && event.Status >= 400 && event.Status < 500 && a.gateway.rejectedBeforeProcessing(event.Status))
		status := event.Status
		if status == 0 {
			status = http.StatusBadRequest
		}
		run.finish(execution.Result{StatusCode: status, ResponseComplete: true, Rejected: rejected, Err: fmt.Errorf("response.create rejected")})
	}
	return nil, nil
}

func (a *responseGate) terminalUsage(ctx context.Context, payload []byte, usage json.RawMessage) (*UsageObservation, error) {
	if a.realtime {
		counters, err := realtimeTokenCounters(usage)
		if err != nil {
			return nil, err
		}
		return &UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, OperationID: a.op.ID, Counters: counters, Cumulative: true, Terminal: true}, nil
	}
	value, err := a.gateway.inspectJSON(ctx, a.owner, Operation{ID: "openai.v1.responses"}, payload, true, true)
	if err != nil || value.usage == nil {
		return nil, fmt.Errorf("response usage unavailable")
	}
	value.usage.OperationID, value.usage.Terminal, value.usage.ResourceID = a.op.ID, true, ""
	if err = normalizeOutputInclusive(value.usage, false); err != nil {
		return nil, err
	}
	return value.usage, nil
}

func (a *responseGate) end(bool) {
	a.pending.close()
	a.active.close()
}

// realtimeTokenCounters maps a Realtime response.usage to disjoint modality
// meters. input_tokens includes cached_tokens; input_token_details splits
// input by modality and cached_tokens_details splits cached input; output
// is text + audio. Every split must add up, else usage is not settled.
func realtimeTokenCounters(raw json.RawMessage) (map[string]int64, error) {
	type modalities struct {
		Text  *int64 `json:"text_tokens"`
		Audio *int64 `json:"audio_tokens"`
		Image *int64 `json:"image_tokens"`
	}
	var usage struct {
		Input   *int64 `json:"input_tokens"`
		Output  *int64 `json:"output_tokens"`
		Total   *int64 `json:"total_tokens"`
		Details *struct {
			modalities
			Cached        *int64      `json:"cached_tokens"`
			CachedDetails *modalities `json:"cached_tokens_details"`
		} `json:"input_token_details"`
		OutputDetails *modalities `json:"output_token_details"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &usage) != nil || usage.Input == nil || usage.Output == nil || usage.Details == nil || usage.OutputDetails == nil {
		return nil, fmt.Errorf("complete Realtime usage required")
	}
	value := func(n *int64) int64 {
		if n == nil {
			return 0 // documented detail fields default to 0
		}
		return *n
	}
	in := [3]int64{value(usage.Details.Text), value(usage.Details.Audio), value(usage.Details.Image)}
	var cached [3]int64
	if usage.Details.CachedDetails != nil {
		cached = [3]int64{value(usage.Details.CachedDetails.Text), value(usage.Details.CachedDetails.Audio), value(usage.Details.CachedDetails.Image)}
	}
	out := [2]int64{value(usage.OutputDetails.Text), value(usage.OutputDetails.Audio)}
	cachedTotal := value(usage.Details.Cached)
	for _, n := range append(append(in[:], cached[:]...), out[0], out[1], *usage.Input, *usage.Output, cachedTotal) {
		if n < 0 || n > 1<<53 {
			return nil, fmt.Errorf("invalid Realtime usage counter")
		}
	}
	if in[0]+in[1]+in[2] != *usage.Input || cached[0]+cached[1]+cached[2] != cachedTotal || out[0]+out[1] != *usage.Output || (usage.Total != nil && *usage.Total != *usage.Input+*usage.Output) {
		return nil, fmt.Errorf("Realtime usage breakdown does not add up")
	}
	counters := map[string]int64{"input_tokens": *usage.Input, "cached_tokens": cachedTotal, "output_tokens": *usage.Output, "text_output_tokens": out[0], "audio_output_tokens": out[1]}
	for i, name := range []string{"text", "audio", "image"} {
		if cached[i] > in[i] {
			return nil, fmt.Errorf("Realtime cached %s exceeds input", name)
		}
		counters["uncached_"+name+"_input_tokens"] = in[i] - cached[i]
		counters["cached_"+name+"_input_tokens"] = cached[i]
	}
	if usage.Total != nil {
		counters["total_tokens"] = *usage.Total
	}
	return counters, nil
}

// ---- Gemini Live renewable allowance ----

// liveCounterNames are the Live usage counters a window may settle. A plain
// proto3 int32 field omitted from a snapshot is 0 (see normalizeGeminiFinal).
var liveCounterNames = []string{"input_tokens", "cached_tokens", "output_tokens", "thought_tokens", "tool_use_tokens", "total_tokens"}

type liveWindowGate struct {
	gateway    *Gateway
	admit      func(n int) (*executionRun, error)
	allowance  int64
	current    *executionRun
	windows    int
	baseline   map[string]int64
	last       map[string]int64
	cumulative execution.CumulativeUsage
	stale      bool
	broken     bool
}

func liveGoAway() []byte { return []byte(`{"goAway":{"timeLeft":"0s"}}`) }

func (a *liveWindowGate) client(context.Context, []byte, int) ([]byte, bool, error) {
	if a.last != nil {
		a.stale = true
	}
	return nil, true, nil
}

func (a *liveWindowGate) delta() map[string]int64 {
	out := map[string]int64{}
	for _, name := range liveCounterNames {
		out[name] = a.last[name] - a.baseline[name]
	}
	return out
}

func (a *liveWindowGate) settle(run *executionRun, counters map[string]int64) error {
	value := UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, OperationID: "live.connect", Counters: counters, Cumulative: true, Terminal: true}
	run.modelIdentity(&value)
	if err := run.observe(value); err != nil {
		run.finish(execution.Result{ResponseComplete: true, Err: err})
		return err
	}
	return run.finish(execution.Result{StatusCode: http.StatusSwitchingProtocols, ResponseComplete: true, Terminal: true})
}

func (a *liveWindowGate) server(_ context.Context, payload []byte) ([]byte, error) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(payload, &doc) != nil {
		return nil, nil
	}
	raw := doc["usageMetadata"]
	if raw == nil {
		for _, key := range []string{"serverContent", "toolCall", "outputTranscription", "inputTranscription"} {
			if doc[key] != nil && a.last != nil {
				a.stale = true
			}
		}
		return nil, nil
	}
	counters, err := liveUsageCounters(raw)
	if err == nil {
		err = a.cumulative.Observe(counters)
	}
	if err != nil {
		a.broken = true
		return liveGoAway(), fmt.Errorf("%w: unusable Live usage snapshot", errSessionPolicy)
	}
	snapshot := map[string]int64{}
	for _, name := range liveCounterNames {
		snapshot[name] = counters[name]
	}
	a.last, a.stale = snapshot, false
	used := int64(0)
	for _, name := range liveCounterNames {
		if d := a.last[name] - a.baseline[name]; d > used {
			used = d
		}
	}
	if used*2 < a.allowance {
		return nil, nil
	}
	// Settle the current window on the exact snapshot delta first (this
	// releases its concurrency permit), then admit the next window. Windows
	// never overlap, so a session needs one permit, not two.
	previous, slice := a.current, a.delta()
	a.current, a.baseline = nil, a.last
	if err = a.settle(previous, slice); err != nil {
		a.broken = true
		return liveGoAway(), err
	}
	a.windows++
	next, err := a.admit(a.windows)
	if err != nil {
		// Renewal denied: goAway and close. Usage reported after this
		// snapshot until the close has no hold (the stated overrun bound).
		return liveGoAway(), fmt.Errorf("%w: allowance renewal denied: %v", errSessionPolicy, err)
	}
	a.current = next
	return nil, nil
}

func (a *liveWindowGate) end(clean bool) {
	if a.current == nil {
		return
	}
	if clean && !a.stale && !a.broken && a.last != nil {
		_ = a.settle(a.current, a.delta())
		return
	}
	a.current.close()
}
