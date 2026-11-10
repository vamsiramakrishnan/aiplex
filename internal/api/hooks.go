package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/vamsiramakrishnan/aiplex/pkg/hooks"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Request hooks (pkg/hooks) are wired into exactly two seams:
//
//   - models: modelapi.Options.Hooks (modelHooks below). Its Request method
//     runs after authorization and capacity admission and before the quota
//     lease, credentials and upstream dispatch; its Response method runs on
//     the complete response before it reaches the caller.
//   - tools: toolexec.Options.InspectRequest / InspectResponse
//     (installToolHooks below), at the same two points of a tools/call.
//
// A later retry, fallback or cache layer belongs between these two calls:
// it must dispatch the body Request returned and hand the final response to
// Response exactly once.

// hookPolicies loads the tenant's hook policies. A stored policy that no
// longer validates is an error: an unreadable policy may be a protective one,
// so the exchange is refused rather than run without it.
func (h *ConnectedHandler) hookPolicies(ctx context.Context, tenant string) ([]hooks.Policy, error) {
	if h.Store == nil {
		return nil, errors.New("hook policy store unavailable")
	}
	records, err := h.Store.List(ctx, tenant, hooks.Kind)
	if err != nil {
		return nil, err
	}
	out := make([]hooks.Policy, 0, len(records))
	for _, record := range records {
		policy, err := hooks.DecodePolicy(record.ID, record.Spec)
		if err != nil {
			return nil, errors.New("a stored hook policy is invalid")
		}
		out = append(out, policy)
	}
	return out, nil
}

// hookRuntime gives hooks the outbound-policy HTTP client (the same policy
// that approves tool token endpoints) and tenant secret resolution.
func (h *ConnectedHandler) hookRuntime(tenant string) hooks.Runtime {
	rt := hooks.Runtime{}
	if h.Runtime != nil {
		rt.Client = h.Runtime.HTTP
	}
	rt.Secret = func(ctx context.Context, ref string) (string, error) {
		provider, err := h.toolCredentials()
		if err != nil {
			return "", err
		}
		return provider.ResolveSecret(ctx, tenant, ref)
	}
	return rt
}

// hookExchange is the per-exchange hook state shared by the request and
// response phases.
type hookExchange struct {
	h         *ConnectedHandler
	tenant    string
	usage     *usageCapture
	resolved  hooks.Resolved
	meta      hooks.Meta
	format    string
	streaming bool
	loaded    bool
}

func (h *ConnectedHandler) newHookExchange(r *http.Request, tenant string, usage *usageCapture, plane string) *hookExchange {
	x := &hookExchange{h: h, tenant: tenant, usage: usage, meta: hooks.Meta{Plane: plane}}
	if identity := GetWIFIdentity(r.Context()); identity != nil {
		x.meta.PrincipalID = identity.PrincipalID
	}
	if call := accessPointCallFrom(r.Context()); call != nil {
		x.meta.AccessPointID = call.ID
		x.meta.CallerKind = call.CallerKind
	}
	return x
}

// load resolves the policies for this exchange once.
func (x *hookExchange) load(ctx context.Context, target hooks.Target) error {
	if x.loaded {
		return nil
	}
	policies, err := x.h.hookPolicies(ctx, x.tenant)
	if err != nil {
		x.usage.hookError(hooks.CodeUnavailableConfig)
		return err
	}
	target.AccessPointID = x.meta.AccessPointID
	x.resolved = hooks.Resolve(policies, target)
	x.loaded = true
	return nil
}

// record keeps results in the ledger summary and the metrics.
func (x *hookExchange) record(ctx context.Context, out hooks.Outcome) {
	for _, result := range out.Results {
		telemetry.Metrics().RecordHook(ctx, result.Type, result.Phase, result.Decision, float64(result.DurationMS)/1000)
	}
	x.usage.hookResults(out.Results, out.Uninspected)
	if out.Refused() {
		x.usage.hookError(out.Code)
	}
}

func refusal(out hooks.Outcome) *modelapi.HookRefusal {
	return &modelapi.HookRefusal{Status: out.Status, Code: out.Code, Message: out.Message}
}

// skipResponse records the response hooks as skipped for a reason (a
// passthrough stream), so the ledger shows what was not inspected.
func (x *hookExchange) skipResponse(ctx context.Context, reason string) {
	out := hooks.Outcome{Action: hooks.Allow}
	for _, h := range x.resolved.Hooks {
		for _, phase := range h.Phases {
			if phase == hooks.PhaseResponse {
				out.Results = append(out.Results, usageledger.HookResult{ID: h.Name(), Type: h.Type, Phase: phase, Decision: usageledger.HookSkipped, Rules: []usageledger.HookRuleCount{{ID: reason, Count: 1}}})
			}
		}
	}
	x.record(ctx, out)
}

// refuseAll records a refusal decided before any hook ran (streaming,
// WebSocket sessions) against every applicable hook.
func (x *hookExchange) refuse(ctx context.Context, phase, rule, message string) *modelapi.HookRefusal {
	out := hooks.Outcome{Action: hooks.Deny, Status: http.StatusForbidden, Code: hooks.CodeDenied, Message: message}
	out.Results = []usageledger.HookResult{{ID: "policy/" + rule, Type: "policy", Phase: phase, Decision: usageledger.HookDeny, Rules: []usageledger.HookRuleCount{{ID: rule, Count: 1}}}}
	x.record(ctx, out)
	return refusal(out)
}

// ── models ────────────────────────────────────────────────────────────────

// modelHooks implements modelapi.ContentHooks for one exchange.
type modelHooks struct {
	*hookExchange
	surfaceID string
	// route is set for an attempt of a routed access-point model: the
	// request hooks then run once per exchange, with the policies of every
	// backend's surface (access_point_routing.go).
	route *routeExchange
}

func (h *ConnectedHandler) installModelHooks(r *http.Request, tenant, surfaceID string, opts *modelapi.Options, usage *usageCapture) {
	m := &modelHooks{hookExchange: h.newHookExchange(r, tenant, usage, hooks.PlaneModels), surfaceID: surfaceID}
	if attempt := routeAttemptFrom(r.Context()); attempt != nil {
		m.route = attempt.route
	}
	opts.Hooks = m
}

// target is the policy target of the exchange.
func (m *modelHooks) target() hooks.Target {
	t := hooks.Target{Plane: hooks.PlaneModels, ModelSurface: m.surfaceID}
	if m.route != nil {
		t.ModelSurfaces = m.route.surfaces
	}
	return t
}

// modelHookFormat maps a native operation to the hook wire format, or ""
// when no extractor covers it (the exchange is then uninspectable).
func modelHookFormat(s modelapi.Surface, op modelapi.Operation, body []byte) string {
	id := op.ID
	if s.Provider != nil {
		switch modelapi.UsageFormat(s.Provider.ID, id) {
		case modelapi.FormatOpenAIChat:
			return hooks.FormatOpenAIChat
		case modelapi.FormatOpenAIResponses:
			return hooks.FormatOpenAIResponses
		case modelapi.FormatAnthropicMessages, modelapi.FormatAnthropicCountTokens:
			return hooks.FormatAnthropicMessages
		case modelapi.FormatBedrockConverse:
			return hooks.FormatBedrockConverse
		case modelapi.FormatBedrockInvoke:
			if anthropicShapedBody(body) {
				return hooks.FormatAnthropicMessages
			}
		case modelapi.FormatGeminiDeveloper:
			if strings.HasSuffix(id, "generateContent") || strings.HasSuffix(id, "GenerateContent") || strings.HasSuffix(id, "countTokens") {
				return hooks.FormatGemini
			}
		case modelapi.FormatOpenAIEmbeddings, modelapi.FormatOpenAIModerations:
			return hooks.FormatOpenAIEmbeddings
		}
		return ""
	}
	switch {
	case strings.HasPrefix(id, "models.") || strings.HasPrefix(id, "endpoints."):
		method := id[strings.Index(id, ".")+1:]
		switch method {
		case "generateContent", "streamGenerateContent", "countTokens", "computeTokens":
			return hooks.FormatGemini
		case "rawPredict", "streamRawPredict":
			if s.Publisher == "anthropic" && anthropicShapedBody(body) {
				return hooks.FormatAnthropicMessages
			}
		}
	case id == "openai.chat-completions":
		return hooks.FormatOpenAIChat
	case strings.HasPrefix(id, "response."):
		return hooks.FormatOpenAIResponses
	case id == "anthropic.messages":
		return hooks.FormatAnthropicMessages
	}
	return ""
}

// anthropicShapedBody recognises a Messages body inside a model-specific
// wrapper (Bedrock InvokeModel, Vertex rawPredict).
func anthropicShapedBody(body []byte) bool {
	var probe struct {
		Version  *string         `json:"anthropic_version"`
		Messages json.RawMessage `json:"messages"`
	}
	return json.Unmarshal(body, &probe) == nil && probe.Version != nil && len(probe.Messages) > 0
}

func streamingRequest(op modelapi.Operation, body []byte) bool {
	if op.Transport == modelapi.EventStream || strings.Contains(strings.ToLower(op.ID), "stream") {
		return true
	}
	var probe struct {
		Stream *bool `json:"stream"`
	}
	return json.Unmarshal(body, &probe) == nil && probe.Stream != nil && *probe.Stream
}

func (m *modelHooks) Request(ctx context.Context, x modelapi.HookExchange, body []byte) ([]byte, bool, error) {
	if m.route != nil && len(body) > 0 && x.Operation.Transport != modelapi.WebSocket && x.Metadata.Transport != modelapi.WebSocket {
		return m.routedRequest(ctx, x, body, func() ([]byte, bool, error) { return m.request(ctx, x, body) })
	}
	return m.request(ctx, x, body)
}

func (m *modelHooks) request(ctx context.Context, x modelapi.HookExchange, body []byte) ([]byte, bool, error) {
	if err := m.load(ctx, m.target()); err != nil {
		return nil, false, &modelapi.HookRefusal{Status: http.StatusServiceUnavailable, Code: hooks.CodeUnavailableConfig, Message: "hook policies are unavailable; the request was refused"}
	}
	if len(m.resolved.Hooks) == 0 {
		return body, false, nil
	}
	m.meta.Operation, m.meta.Provider, m.meta.Model = x.Operation.ID, x.Surface.ProviderID(), x.Metadata.Model
	if x.Metadata.Transport == modelapi.WebSocket || x.Operation.Transport == modelapi.WebSocket {
		return nil, false, m.refuse(ctx, hooks.PhaseRequest, "websocket_session", hooks.MessageSession)
	}
	m.format = modelHookFormat(x.Surface, x.Operation, body)
	m.streaming = streamingRequest(x.Operation, body)
	inspectResponse := false
	if m.resolved.HasPhase(hooks.PhaseResponse) {
		switch {
		case m.streaming && m.resolved.Streaming == hooks.StreamingPassthrough:
			m.skipResponse(ctx, "streaming_passthrough")
		case m.streaming && m.resolved.Streaming == hooks.StreamingBuffer && hooks.StreamBufferable(m.format):
			inspectResponse = true
		case m.streaming && m.resolved.Streaming == hooks.StreamingBuffer:
			return nil, false, m.refuse(ctx, hooks.PhaseResponse, "streaming_unsupported", hooks.MessageStreaming)
		case m.streaming:
			return nil, false, m.refuse(ctx, hooks.PhaseResponse, "streaming", hooks.MessageStreaming)
		case hooks.Inspectable(m.format):
			inspectResponse = true
		default:
			// No response extractor: decide now, before any upstream cost.
			out := hooks.Run(ctx, m.h.hookRuntime(m.tenant), m.resolved, m.meta, hooks.PhaseResponse, nil, hooks.ErrUninspectable)
			m.record(ctx, out)
			if out.Refused() {
				return nil, false, refusal(out)
			}
		}
	}
	if !m.resolved.HasPhase(hooks.PhaseRequest) || len(body) == 0 {
		return body, inspectResponse, nil
	}
	var content *hooks.Content
	var err error
	if m.format != "" {
		content, err = hooks.ExtractRequest(m.format, body)
	} else {
		err = hooks.ErrUninspectable
	}
	out := hooks.Run(ctx, m.h.hookRuntime(m.tenant), m.resolved, m.meta, hooks.PhaseRequest, content, err)
	m.record(ctx, out)
	if out.Refused() {
		return nil, false, refusal(out)
	}
	if out.Action == hooks.Transform {
		return out.Body, inspectResponse, nil
	}
	return body, inspectResponse, nil
}

func (m *modelHooks) Response(ctx context.Context, x modelapi.HookExchange, mediaType string, body []byte) ([]byte, error) {
	content, err := hooks.ExtractResponse(m.format, mediaType, body)
	if err != nil {
		content = nil
	}
	out := hooks.Run(ctx, m.h.hookRuntime(m.tenant), m.resolved, m.meta, hooks.PhaseResponse, content, err)
	m.record(ctx, out)
	if out.Refused() {
		return nil, refusal(out)
	}
	if out.Action == hooks.Transform {
		return out.Body, nil
	}
	return body, nil
}

// ── tools ─────────────────────────────────────────────────────────────────

func toolDenial(out hooks.Outcome) *toolexec.Denial {
	return &toolexec.Denial{Status: out.Status, Code: toolexec.RPCForbidden, Reason: strings.ToLower(out.Code), Message: out.Message}
}

// installToolHooks wires the hook chain into a managed tools/call.
func (h *ConnectedHandler) installToolHooks(r *http.Request, tenant, connectionID string, opts *toolexec.Options, usage *usageCapture) {
	x := h.newHookExchange(r, tenant, usage, hooks.PlaneTools)
	x.meta.Provider, x.meta.Operation, x.format = "mcp", "tools/call", hooks.FormatMCPToolCall
	inspectResponse := false
	opts.InspectRequest = func(ctx context.Context, request toolexec.Request) ([]byte, error) {
		if err := x.load(ctx, hooks.Target{Plane: hooks.PlaneTools, ToolConnection: connectionID}); err != nil {
			return nil, &toolexec.Denial{Status: http.StatusServiceUnavailable, Code: toolexec.RPCForbidden, Reason: "hook_policy_unavailable", Message: "hook policies are unavailable; the call was refused"}
		}
		if len(x.resolved.Hooks) == 0 {
			return request.Body, nil
		}
		x.meta.Tool = request.Name
		inspectResponse = x.resolved.HasPhase(hooks.PhaseResponse)
		if !x.resolved.HasPhase(hooks.PhaseRequest) {
			return request.Body, nil
		}
		content, err := hooks.ExtractRequest(hooks.FormatMCPToolCall, request.Body)
		out := hooks.Run(ctx, h.hookRuntime(tenant), x.resolved, x.meta, hooks.PhaseRequest, content, err)
		x.record(ctx, out)
		if out.Refused() {
			return nil, toolDenial(out)
		}
		if out.Action == hooks.Transform {
			return out.Body, nil
		}
		return request.Body, nil
	}
	opts.InspectResponse = func(ctx context.Context, request toolexec.Request, mediaType string, body []byte) ([]byte, error) {
		if !inspectResponse {
			return body, nil
		}
		content, err := hooks.ExtractResponse(hooks.FormatMCPToolCall, mediaType, body)
		if err != nil {
			content = nil
		}
		out := hooks.Run(ctx, h.hookRuntime(tenant), x.resolved, x.meta, hooks.PhaseResponse, content, err)
		x.record(ctx, out)
		if out.Refused() {
			return nil, toolDenial(out)
		}
		if out.Action == hooks.Transform {
			return out.Body, nil
		}
		return body, nil
	}
}

// ── usage capture ─────────────────────────────────────────────────────────

// hookResults adds hook results to the exchange's ledger summary.
func (c *usageCapture) hookResults(results []usageledger.HookResult, uninspected int) {
	if c == nil || (len(results) == 0 && uninspected == 0) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		c.hooks = &usageledger.HookSummary{Results: []usageledger.HookResult{}}
	}
	c.hooks.UninspectedParts += uninspected
	for _, r := range results {
		if len(c.hooks.Results) < usageledger.MaxHookResults {
			c.hooks.Results = append(c.hooks.Results, r)
		}
	}
	c.hooks.Summarize()
}

// hookError records the stable error class of a hook refusal.
func (c *usageCapture) hookError(code string) {
	if c == nil || code == "" {
		return
	}
	c.mu.Lock()
	c.errorCode = strings.ToLower(code)
	c.mu.Unlock()
}

// hookSpanAttributes adds hook attributes to the exchange span: decision,
// run count, the denying rule identifier and fail-open use. No content.
func hookSpanAttributes(record usageledger.Record) []attribute.KeyValue {
	s := record.Hooks
	if s == nil {
		return nil
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.AttrHooksDecision, s.Decision),
		attribute.Int(telemetry.AttrHooksCount, len(s.Results)),
	}
	for _, r := range s.Results {
		if r.Decision == usageledger.HookDeny && len(r.Rules) > 0 {
			attrs = append(attrs, attribute.String(telemetry.AttrHooksDeniedRule, r.Rules[0].ID))
			break
		}
	}
	if s.FailOpenUsed {
		attrs = append(attrs, attribute.Bool(telemetry.AttrHooksFailOpen, true))
	}
	return attrs
}
