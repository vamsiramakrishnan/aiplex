package modelapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// OpenAI Live voice sessions (wss://api.openai.com/v1/live/sessions), read in
// https://developers.openai.com/api/reference/resources/live/primary-websocket
// and https://developers.openai.com/api/docs/guides/voice-latency-cost.
//
// The session's primary meter is voice duration: session.usage.updated
// carries usage.seconds as a cumulative snapshot ("Each update replaces the
// previous duration snapshot. Do not sum the snapshots."), and session.closed
// carries the final usage.seconds, recorded once. It is observed as
// voice_session_milliseconds. Delegated Responses backends report token usage
// on the nested response.event / response.completed (counted once per
// response id) and decisions on decision.event / decision.completed; those
// tokens are summed as advisory session tokens.
//
// Admission follows the existing session model unchanged: one request-count
// admission and one settlement per session; any other lease profile is
// refused before the dial (prepareSession). Without session.closed the
// session ends lower_bound: the docs say a closed connection "does not
// confirm successful finalization".
const openAILiveOperation = "openai.v1.live"

func liveVoiceCatalog() providerOp {
	return providerOp{Operation{ID: openAILiveOperation, Method: "GET", Path: "/v1/live/sessions", Transport: WebSocket, Action: Invoke, SourceURL: "https://developers.openai.com/api/reference/resources/live/primary-websocket"}, FormatOpenAIRealtime, ProtocolOpenAILive}

}

// ProtocolOpenAILive is the Live voice WebSocket protocol.
const ProtocolOpenAILive = "openai-live"

type liveVoiceAdapter struct {
	gateway     *Gateway
	model       string
	started     bool
	closed      bool
	seconds     int64
	hasSeconds  bool
	responses   map[string]bool
	totals      map[string]int64
	overflowed  bool
	finalVoice  int64
	finalClosed bool
}

func newLiveVoiceAdapter(g *Gateway, model string) *liveVoiceAdapter {
	return &liveVoiceAdapter{gateway: g, model: model, responses: map[string]bool{}}
}

// clientMessage pins the session model chosen at connect time (?model=).
func (a *liveVoiceAdapter) clientMessage(_ context.Context, payload []byte, kind int) error {
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
		return nil
	}
	switch event.Type {
	case "session.start":
		if a.started || event.Session == nil || event.Session.Model == nil || *event.Session.Model != a.model {
			return fmt.Errorf("session.start must name the connect-time model once")
		}
		a.started = true
	case "session.update":
		if event.Session != nil && event.Session.Model != nil && *event.Session.Model != a.model {
			return fmt.Errorf("the session model is pinned at connect time")
		}
	}
	return nil
}

func (a *liveVoiceAdapter) add(counters map[string]int64) {
	if a.totals == nil {
		a.totals = map[string]int64{}
	}
	for key, n := range counters {
		if a.totals[key] > math.MaxInt64-n {
			a.overflowed = true
			return
		}
		a.totals[key] += n
	}
}

func (a *liveVoiceAdapter) serverMessage(_ context.Context, payload []byte) (wsEffects, error) {
	var event struct {
		Type  string          `json:"type"`
		Usage json.RawMessage `json:"usage"`
		Event *struct {
			Type     string `json:"type"`
			Response *struct {
				ID    string          `json:"id"`
				Usage json.RawMessage `json:"usage"`
			} `json:"response"`
			Decision *struct {
				Usage json.RawMessage `json:"usage"`
			} `json:"decision"`
		} `json:"event"`
		EventID string `json:"event_id"`
	}
	effects := wsEffects{}
	if json.Unmarshal(payload, &event) != nil {
		return effects, nil
	}
	observe := func(counters map[string]int64, cumulative bool) {
		effects.observations = append(effects.observations, UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, Counters: counters, Cumulative: cumulative})
	}
	seconds := func() (int64, bool) {
		var usage struct {
			Seconds json.RawMessage `json:"seconds"`
		}
		if json.Unmarshal(event.Usage, &usage) != nil || usage.Seconds == nil {
			return 0, false
		}
		ms, err := secondsToMilliseconds(usage.Seconds)
		return ms, err == nil
	}
	switch event.Type {
	case "session.usage.updated":
		if ms, ok := seconds(); ok && ms >= a.seconds {
			a.seconds, a.hasSeconds = ms, true
			observe(map[string]int64{CounterVoiceSessionMilliseconds: ms}, true)
		}
	case "session.closed":
		a.closed = true
		if ms, ok := seconds(); ok && ms >= a.seconds {
			a.finalVoice, a.finalClosed = ms, true
			a.seconds, a.hasSeconds = ms, true
			observe(map[string]int64{CounterVoiceSessionMilliseconds: ms}, true)
		}
	case "response.event":
		if event.Event == nil || event.Event.Type != "response.completed" || event.Event.Response == nil || event.Event.Response.ID == "" || a.responses[event.Event.Response.ID] {
			return effects, nil
		}
		a.responses[event.Event.Response.ID] = true
		if counters, _, err := nativeBodyCounters(ProviderOpenAI, mustUsageEnvelope(event.Event.Response.Usage)); err == nil && len(counters) > 0 {
			a.add(counters)
			observe(counters, false)
		}
	case "decision.event":
		if event.Event == nil || event.Event.Type != "decision.completed" || event.Event.Decision == nil {
			return effects, nil
		}
		if counters, _, err := nativeBodyCounters(ProviderOpenAI, mustUsageEnvelope(event.Event.Decision.Usage)); err == nil && len(counters) > 0 {
			a.add(counters)
			observe(counters, false)
		}
	}
	return effects, nil
}

func mustUsageEnvelope(usage json.RawMessage) json.RawMessage {
	if usage == nil {
		return json.RawMessage(`{}`)
	}
	return append(append([]byte(`{"usage":`), usage...), '}')
}

// end is final only with a clean close after session.closed reported seconds.
func (a *liveVoiceAdapter) end(clean bool) (*UsageObservation, string) {
	if !clean || !a.finalClosed || a.overflowed {
		return nil, execution.BoundLowerBound
	}
	counters := cloneCounters(a.totals)
	if counters == nil {
		counters = map[string]int64{}
	}
	counters[CounterVoiceSessionMilliseconds] = a.finalVoice
	return &UsageObservation{Source: "upstream", SurfaceID: a.gateway.surface.ID, Counters: counters, Cumulative: true}, ""
}
