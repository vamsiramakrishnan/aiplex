package modelapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// LiveDialer is the functional WebSocket transport. Hosts should use
// NewLiveDialer with their endpoint-policy HTTP client, preserving dial-time DNS
// validation and TLS verification rather than constructing an ambient dialer.
type LiveDialer interface {
	DialContext(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}

type SafeNetworkDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

func NewLiveDialer(policy SafeNetworkDialer, tlsConfig *tls.Config) (*websocket.Dialer, error) {
	if policy == nil {
		return nil, fmt.Errorf("endpoint-policy dialer required")
	}
	if tlsConfig != nil && tlsConfig.InsecureSkipVerify {
		return nil, fmt.Errorf("TLS verification cannot be disabled")
	}
	var safeTLS *tls.Config
	if tlsConfig != nil {
		safeTLS = tlsConfig.Clone()
	}
	return &websocket.Dialer{NetDialContext: policy.DialContext, TLSClientConfig: safeTLS, HandshakeTimeout: 10 * time.Second, ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10}, nil
}

func websocketEndpoint(origin, path, rawQuery string) string {
	endpoint := strings.TrimSuffix(origin, "/") + path
	if strings.HasPrefix(endpoint, "https://") {
		endpoint = "wss://" + strings.TrimPrefix(endpoint, "https://")
	} else {
		endpoint = "ws://" + strings.TrimPrefix(endpoint, "http://")
	}
	if rawQuery != "" {
		endpoint += "?" + rawQuery
	}
	return endpoint
}

// serveLive relays a Gemini Live (BidiGenerateContent) session. Client
// authentication has already succeeded. Only the bounded initial setup is read
// before quota admission, server credentials or the upstream socket. A quota
// lease must use the request-count profile: one session is one admitted
// exchange, observed usage is advisory, and token-metered leases are refused
// before credentials are acquired.
func (g *Gateway) serveLive(w http.ResponseWriter, r *http.Request, trusted contracts.RequestContext, owner Owner, op Operation) {
	connection, e := (&websocket.Upgrader{HandshakeTimeout: 10 * time.Second}).Upgrade(w, r, nil)
	if e != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(g.options.MaxBodyBytes)
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	kind, setup, e := connection.ReadMessage()
	if e != nil || kind != websocket.TextMessage {
		return
	}
	metadata, resumed, e := g.validateLiveSetupResumption(r.Context(), owner, setup)
	if pinned := r.URL.Query()["model"]; e == nil && len(pinned) > 0 {
		// A routing selector (e.g. an access point's ?model=) pins the
		// session: the setup must name the same model.
		if len(pinned) != 1 || g.normalizeModel(pinned[0]) != g.normalizeModel(metadata.Model) {
			e = fmt.Errorf("setup model differs from the selected model")
		}
	}
	if e != nil {
		closeWebSocket(connection, websocket.ClosePolicyViolation, "setup denied")
		return
	}
	if e = g.options.Authorize(r.Context(), trusted, g.Surface(), op, metadata); e != nil {
		closeWebSocket(connection, websocket.ClosePolicyViolation, "operation denied")
		return
	}
	var gate *liveWindowGate
	var run *executionRun
	if g.surface.WebSocket.tokenMetering() && g.options.Execution != nil {
		// Renewable allowance: the first window is admitted and claimed
		// before credentials and the dial (see session_metering.go).
		if resumed {
			closeWebSocket(connection, websocket.ClosePolicyViolation, "token-metered sessions cannot resume")
			return
		}
		admit := func(n int) (*executionRun, error) {
			window, err := g.admitSessionExchange(r, trusted, op, metadata.Model, setup, n, g.normalizeModel(metadata.Model))
			if err == nil && window != nil && requestCountRun(window) {
				window.finish(execution.Result{Err: fmt.Errorf("request-count lease in token mode")})
				err = &execution.StatusError{Status: http.StatusNotImplemented, Code: "UNSUPPORTED_EXECUTION_TRANSPORT", Message: "token-metered sessions require a token profile binding"}
			}
			return window, err
		}
		first, err := admit(0)
		if err != nil {
			closeWebSocketForExecution(connection, err)
			return
		}
		gate = &liveWindowGate{gateway: g, admit: admit, allowance: g.surface.SessionAllowance(), current: first}
		defer func() {
			if gate.current != nil {
				gate.current.close()
			}
		}()
	} else {
		var err error
		if run, err = g.prepareSession(r, trusted, op, metadata, setup); err != nil {
			closeWebSocketForExecution(connection, err)
			return
		}
		if run != nil {
			defer run.close()
		}
		if err = run.beforeDispatch(r.Context()); err != nil {
			closeWebSocketForExecution(connection, err)
			return
		}
	}
	var sessionGate wsGate
	if gate != nil {
		sessionGate = gate
	}
	token, e := g.options.TokenSource.Token(r.Context())
	if e != nil || token == "" {
		_ = run.finish(execution.Result{Err: fmt.Errorf("upstream identity unavailable")})
		closeWebSocket(connection, websocket.CloseTryAgainLater, "upstream identity unavailable")
		return
	}
	headers := forwardRequestHeaders(r.Header)
	applyTrustedHeaders(headers, g.options.UpstreamHeaders)
	headers.Set("Authorization", "Bearer "+token)
	upstream, response, e := g.options.LiveDialer.DialContext(r.Context(), websocketEndpoint(g.surface.Origin, r.URL.EscapedPath(), ""), headers)
	if e != nil && response != nil && response.Body != nil {
		response.Body.Close()
	}
	if e != nil {
		_ = run.finish(execution.Result{Err: e})
		closeWebSocket(connection, websocket.CloseTryAgainLater, "upstream unavailable")
		return
	}
	defer upstream.Close()
	upstream.SetReadLimit(g.options.MaxBodyBytes)
	if e = upstream.WriteMessage(kind, setup); e != nil {
		_ = run.finish(execution.Result{Err: e})
		return
	}
	_ = connection.SetReadDeadline(time.Time{})
	g.pumpWebSocket(r.Context(), connection, upstream, owner, op, &liveAdapter{gateway: g, owner: owner, resumed: resumed}, run, sessionGate)
}

func (g *Gateway) validateLiveSetup(ctx context.Context, owner Owner, payload []byte) (Metadata, error) {
	metadata, _, err := g.validateLiveSetupResumption(ctx, owner, payload)
	return metadata, err
}

func (g *Gateway) validateLiveSetupResumption(ctx context.Context, owner Owner, payload []byte) (Metadata, bool, error) {
	if e := validateJSON(payload); e != nil {
		return Metadata{}, false, e
	}
	var doc struct {
		Setup struct {
			Model             string `json:"model"`
			SessionResumption struct {
				Handle string `json:"handle"`
			} `json:"sessionResumption"`
		} `json:"setup"`
	}
	if e := json.Unmarshal(payload, &doc); e != nil || doc.Setup.Model == "" || !g.allowedModel(doc.Setup.Model) {
		return Metadata{}, false, fmt.Errorf("Live setup model denied")
	}
	handle := doc.Setup.SessionResumption.Handle
	if handle != "" {
		if len(handle) > 8192 || g.options.Ownership == nil {
			return Metadata{}, false, fmt.Errorf("session ownership required")
		}
		if e := g.options.Ownership.Check(ctx, owner, Resource{"live-session", handle}); e != nil {
			return Metadata{}, false, e
		}
	}
	return Metadata{Model: doc.Setup.Model, Transport: WebSocket}, handle != "", nil
}

// liveAdapter observes Gemini Live server messages.
//
// usageMetadata is its own top-level server message. The Developer API names
// the output counter responseTokenCount; the Vertex v1 UsageMetadata names it
// candidatesTokenCount (no page shows the Vertex Live JSON), so both are
// accepted and a conflict is refused. Vertex documents the message as "tokens
// used by the session so far", so snapshots are cumulative: the last one
// replaces earlier ones and successive snapshots are never added. A snapshot
// that regresses disproves that reading for the session, which then stays
// ambiguous rather than being summed or replaced.
//
// The final snapshot is used as the session's usage only when the session
// closed cleanly and no model output, turn signal or client input followed
// it. Otherwise the last snapshot is a lower bound. Whether counters restart
// after session resumption is unconfirmed, so a resumed session's usage is
// labelled ambiguous instead of being presented as additive to the session it
// resumed (no double counting is claimed either way).
type liveAdapter struct {
	gateway    *Gateway
	owner      Owner
	resumed    bool
	cumulative execution.CumulativeUsage
	last       map[string]int64
	stale      bool // activity after the last usage snapshot
	ambiguous  bool
}

func (a *liveAdapter) clientMessage(context.Context, []byte, int) error {
	if a.last != nil {
		a.stale = true
	}
	return nil
}

func (a *liveAdapter) serverMessage(ctx context.Context, payload []byte) (wsEffects, error) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(payload, &doc) != nil {
		return wsEffects{}, nil
	}
	effects := wsEffects{}
	if raw := doc["sessionResumptionUpdate"]; raw != nil {
		var update struct {
			NewHandle string `json:"newHandle"`
		}
		if json.Unmarshal(raw, &update) != nil {
			return effects, fmt.Errorf("invalid session resumption update")
		}
		if update.NewHandle != "" {
			if len(update.NewHandle) > 8192 || a.gateway.options.Ownership == nil {
				return effects, fmt.Errorf("session ownership required")
			}
			if e := a.gateway.options.Ownership.Bind(ctx, a.owner, Resource{"live-session", update.NewHandle}); e != nil {
				return effects, e
			}
		}
	}
	for _, key := range []string{"serverContent", "toolCall", "outputTranscription", "inputTranscription"} {
		if doc[key] != nil && a.last != nil {
			a.stale = true
		}
	}
	raw := doc["usageMetadata"]
	if raw == nil {
		return effects, nil
	}
	counters, err := liveUsageCounters(raw)
	if err != nil || len(counters) == 0 {
		// Advisory telemetry: an unreadable snapshot is unknown, not fatal.
		return effects, nil
	}
	if a.cumulative.Observe(counters) != nil {
		a.ambiguous = true
		return effects, nil
	}
	a.last, a.stale = counters, false
	effects.observations = append(effects.observations, UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, Counters: cloneCounters(counters), Cumulative: true})
	return effects, nil
}

func (a *liveAdapter) end(clean bool) (*UsageObservation, string) {
	switch {
	case a.resumed || a.ambiguous:
		return nil, execution.BoundAmbiguous
	case a.last == nil || !clean || a.stale:
		return nil, execution.BoundLowerBound
	}
	return &UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, Counters: cloneCounters(a.last), Cumulative: true}, ""
}

// liveUsageCounters maps a Live usageMetadata message.
func liveUsageCounters(raw json.RawMessage) (map[string]int64, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, fmt.Errorf("invalid Live usage")
	}
	counters := map[string]int64{}
	for native, target := range map[string]string{"promptTokenCount": "input_tokens", "cachedContentTokenCount": "cached_tokens", "responseTokenCount": "output_tokens", "candidatesTokenCount": "output_tokens", "thoughtsTokenCount": "thought_tokens", "toolUsePromptTokenCount": "tool_use_tokens", "totalTokenCount": "total_tokens"} {
		value := fields[native]
		if value == nil {
			continue
		}
		var n int64
		if json.Unmarshal(value, &n) != nil || n < 0 || n > 1<<53 {
			return nil, fmt.Errorf("invalid Live usage counter")
		}
		if prior, ok := counters[target]; ok && prior != n {
			return nil, fmt.Errorf("conflicting Live usage counters")
		}
		counters[target] = n
	}
	return counters, nil
}

func cloneCounters(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
