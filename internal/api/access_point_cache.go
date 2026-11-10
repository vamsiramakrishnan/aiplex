package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/routing"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// The exact-match response cache of an access-point model (off unless the
// model has a cache block). It sits between the request hooks and the
// upstream dispatch: the key is computed from the body the hooks returned,
// after identity, grants, capacity admission and the quota lease, so a hit
// still passes all of them; the response hooks run again on every hit.

// routeCacheState is the cache decision of one routed exchange.
type routeCacheState struct {
	config *routing.Cache
	key    string
	info   *usageledger.CacheInfo
}

// routeCache implements modelapi.ResponseCache for one attempt.
type routeCache struct {
	attempt *routeAttempt
	usage   *usageCapture
}

// cacheableWires are the client wires whose non-streamed answers may be
// kept: generation and embeddings in JSON. Responses API requests are
// listed but its operation is stateful (stored responses), so they bypass.
var cacheableWires = map[string]bool{WireOpenAIChat: true, WireOpenAIResponses: true, WireAnthropic: true, WireGenAI: true, WireBedrockConverse: true, WireOpenAIEmbeddings: true}

// cacheBypass returns why a request is not served from or kept in the
// cache, or "" when it is eligible. The rule is conservative: only
// non-streamed, stateless requests on the listed wires, without per-user
// identifiers the provider must receive and without server-side tools.
func (x *routeExchange) cacheBypass(op modelapi.Operation, metadata modelapi.Metadata, body []byte) string {
	for _, v := range x.header.Values("Cache-Control") {
		for _, d := range strings.Split(strings.ToLower(v), ",") {
			if strings.TrimSpace(d) == "no-store" {
				return "no_store"
			}
		}
	}
	switch {
	case !cacheableWires[x.in.protocol] || (x.in.protocol == WireGenAI && x.in.method != "generateContent"):
		return "operation"
	case x.stateful || op.Stateful:
		return "stateful"
	case op.Transport == modelapi.WebSocket || op.Transport == modelapi.EventStream || metadata.Transport == modelapi.WebSocket || strings.Contains(strings.ToLower(op.ID), "stream"):
		// A body with "stream": true is caught below; operations whose
		// transport is SSE by default answer JSON unless asked to stream.
		return "streaming"
	case len(body) > routing.MaxCacheEntryBytes:
		return "size"
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(body, &doc) != nil {
		return "operation"
	}
	if raw, ok := doc["stream"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("true")) {
		return "streaming"
	}
	// Identifiers that tie the request to an end user for the provider's
	// abuse and safety systems must reach the provider every time.
	for _, key := range []string{"user", "safety_identifier"} {
		if _, ok := doc[key]; ok {
			return "user_identifier"
		}
	}
	if raw, ok := doc["metadata"]; ok {
		var meta map[string]json.RawMessage
		if json.Unmarshal(raw, &meta) == nil {
			if _, ok := meta["user_id"]; ok {
				return "user_identifier"
			}
		}
	}
	for _, key := range []string{"tools", "toolConfig"} {
		if raw, ok := doc[key]; ok && !functionToolsOnly(raw) {
			return "server_tools"
		}
	}
	return ""
}

// functionToolsOnly reports whether every declared tool is a function the
// caller runs itself (no provider-executed tool such as web search or code
// execution, whose result depends on when it runs).
func functionToolsOnly(raw json.RawMessage) bool {
	var tools []map[string]json.RawMessage
	if json.Unmarshal(raw, &tools) != nil {
		var config map[string]json.RawMessage
		return json.Unmarshal(raw, &config) == nil // Bedrock toolConfig: checked below
	}
	for _, t := range tools {
		var kind string
		if v, ok := t["type"]; ok {
			_ = json.Unmarshal(v, &kind)
		}
		switch {
		case kind == "function" || kind == "custom":
		case kind == "" && (t["name"] != nil || t["functionDeclarations"] != nil || t["function_declarations"] != nil || t["toolSpec"] != nil):
			// Anthropic custom tools, Gemini function declarations,
			// Bedrock toolSpec entries.
			if len(t) > 0 {
				for key := range t {
					if key == "googleSearch" || key == "google_search" || key == "codeExecution" || key == "code_execution" || key == "urlContext" || key == "url_context" {
						return false
					}
				}
			}
		default:
			return false
		}
	}
	return true
}

// responseCacheable reports why a successful response is not kept ("" when
// it may be): only complete JSON answers without tool calls, within the
// entry bound.
func responseCacheable(protocol string, status int, header http.Header, body []byte, limit int64) string {
	media, _, _ := mime.ParseMediaType(header.Get("Content-Type"))
	switch {
	case status != http.StatusOK:
		return "status"
	case media != "application/json" && !strings.HasSuffix(media, "+json"):
		return "not_json"
	case int64(len(body)) > limit:
		return "size"
	}
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return "not_json"
	}
	if hasToolCall(doc) {
		return "tool_calls"
	}
	return ""
}

// hasToolCall looks for a tool or function call anywhere in a response: the
// caller would act on a replayed call as if the model had just asked for it.
func hasToolCall(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for key, child := range t {
			switch key {
			case "tool_calls", "function_call", "functionCall", "toolUse":
				if child != nil {
					return true
				}
			case "type":
				if s, _ := child.(string); s == "tool_use" || s == "server_tool_use" || s == "function_call" || s == "web_search_call" || s == "code_interpreter_call" {
					return true
				}
			case "stop_reason":
				if s, _ := child.(string); s == "pause_turn" || s == "tool_use" {
					return true
				}
			}
			if hasToolCall(child) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if hasToolCall(child) {
				return true
			}
		}
	}
	return false
}

func (x *routeExchange) setCacheInfo(ctx context.Context, status, reason string) {
	x.mu.Lock()
	first := x.cache.info == nil
	x.cache.info = &usageledger.CacheInfo{Status: status, Reason: reason, Scope: x.cache.config.EffectiveScope()}
	x.mu.Unlock()
	if first {
		telemetry.Metrics().RecordCacheLookup(ctx, status)
	}
}

// Lookup decides the exchange's cache outcome on the first attempt that
// reaches it and serves a hit, or returns the store function of a miss.
func (c *routeCache) Lookup(ctx context.Context, hx modelapi.HookExchange, body []byte) (*modelapi.CachedResponse, modelapi.CacheStore) {
	x := c.attempt.route
	if x.cache == nil {
		return nil, nil
	}
	x.mu.Lock()
	decided, key := x.cache.info != nil, x.cache.key
	x.mu.Unlock()
	if !decided {
		if reason := x.cacheBypass(hx.Operation, hx.Metadata, body); reason != "" {
			x.setCacheInfo(ctx, usageledger.CacheBypass, reason)
			return nil, nil
		}
		canonical, err := routing.CanonicalJSON(body, x.entry.Name)
		if err != nil {
			x.setCacheInfo(ctx, usageledger.CacheBypass, "operation")
			return nil, nil
		}
		parts := routing.CacheKeyParts{TenantID: x.tenant, AccessPoint: x.ap.ID, Model: x.entry.Name, Wire: x.in.protocol, Path: x.in.local, Scope: x.cache.config.EffectiveScope(), Body: canonical}
		if parts.Scope == routing.CacheScopePrincipal {
			if x.principal == "" {
				x.setCacheInfo(ctx, usageledger.CacheBypass, "no_principal")
				return nil, nil
			}
			parts.Principal = x.principal
		}
		for _, name := range []string{"Anthropic-Version", "Anthropic-Beta"} {
			parts.Headers = append(parts.Headers, name+": "+strings.Join(x.header.Values(name), ","))
		}
		key = routing.CacheKey(parts)
		x.mu.Lock()
		x.cache.key = key
		x.mu.Unlock()
		refresh := false
		for _, v := range x.header.Values("Cache-Control") {
			refresh = refresh || strings.Contains(strings.ToLower(v), "no-cache")
		}
		if entry, ok := x.h.responseCache().Get(key, time.Now()); ok && !refresh {
			x.setCacheInfo(ctx, usageledger.CacheHit, "")
			c.attempt.set(func(a *routeAttempt) { a.cacheHit = true })
			if c.usage != nil {
				c.usage.mu.Lock()
				c.usage.cacheHit = &entry
				c.usage.mu.Unlock()
			}
			return &modelapi.CachedResponse{Status: entry.Status, Header: entry.Header, Body: entry.Body}, nil
		}
		reason := ""
		if refresh {
			reason = "no_cache"
		}
		x.setCacheInfo(ctx, usageledger.CacheMiss, reason)
	}
	x.mu.Lock()
	miss := x.cache.info != nil && x.cache.info.Status == usageledger.CacheMiss
	x.mu.Unlock()
	if !miss || key == "" {
		return nil, nil
	}
	backend := c.attempt.backend
	return nil, func(status int, header http.Header, data []byte) {
		if reason := responseCacheable(x.in.protocol, status, header, data, x.cache.config.EntryLimit()); reason != "" {
			x.mu.Lock()
			x.cache.info.Reason = "not_stored_" + reason
			x.mu.Unlock()
			return
		}
		counters, version := map[string]int64{}, ""
		if c.usage != nil {
			counters = c.usage.usage.Snapshot()
			c.usage.mu.Lock()
			anthropic := c.usage.anthropicRaw
			version = c.usage.record.ModelVersion
			c.usage.mu.Unlock()
			if anthropic {
				normalizeExclusiveInput(counters)
			}
		}
		now := time.Now()
		stored := http.Header{}
		if v := header.Get("Content-Type"); v != "" {
			stored.Set("Content-Type", v)
		}
		x.h.responseCache().Put(key, routing.CacheEntry{Status: status, Header: stored, Body: data, Counters: counters, ModelVersion: version, SurfaceRef: backend.SurfaceRef, NativeModel: backend.NativeModel, TenantID: x.tenant, AccessPoint: x.ap.ID, Model: x.entry.Name, StoredAt: now, Expires: now.Add(x.cache.config.TTLDuration())})
	}
}

// validateRoutedModel checks a model with backends, a routing block or a
// cache, and resolves every backend in the tenant.
func (h *ConnectedHandler) validateRoutedModel(ctx context.Context, tenant string, i int, m AccessPointModel) error {
	at := func(field string) string { return fmt.Sprintf("models/%d/%s", i, field) }
	wrap := func(err error) error {
		var field *routing.FieldError
		if errors.As(err, &field) {
			return withField(at(field.Field), fmt.Errorf("model %q: %v", m.Name, field.Err))
		}
		return withField(at("routing"), err)
	}
	if !accessPointModelPattern.MatchString(m.Name) || strings.Contains(m.Name, "..") {
		return withField(at("name"), fmt.Errorf("models entries need a name ([A-Za-z0-9._@/:-], no '..')"))
	}
	if len(m.Backends) > 0 && !m.derived && (m.SurfaceRef != "" || m.NativeModel != "") {
		return withField(at("backends"), fmt.Errorf("model %q: use either surface_ref and native_model or backends, not both", m.Name))
	}
	backends := m.allBackends()
	if len(m.Backends) > 0 {
		if err := routing.ValidateBackends(m.Backends); err != nil {
			return wrap(err)
		}
	}
	if err := m.Routing.Validate(len(backends)); err != nil {
		return wrap(err)
	}
	if err := m.Cache.Validate(); err != nil {
		return wrap(err)
	}
	if len(m.Backends) == 0 {
		// One backend: the surface_ref/native_model checks follow.
		return nil
	}
	var firstWires []string
	for j, b := range m.Backends {
		field := func(name string) string { return fmt.Sprintf("backends/%d/%s", j, name) }
		if !accessPointModelPattern.MatchString(b.NativeModel) || strings.Contains(b.NativeModel, "..") || b.SurfaceRef == "" {
			return withField(at(field("native_model")), fmt.Errorf("model %q backend %d needs surface_ref and native_model ([A-Za-z0-9._@/:-], no '..')", m.Name, j+1))
		}
		connection, err := h.accessPointSurface(ctx, tenant, b.SurfaceRef)
		if err != nil {
			return withField(at(field("surface_ref")), err)
		}
		if err = accessPointSurfaceServes(connection.Surface, b.NativeModel); err != nil {
			return withField(at(field("native_model")), fmt.Errorf("model surface %q cannot serve native model %q: %w", b.SurfaceRef, b.NativeModel, err))
		}
		if _, err = connection.Surface.ModelID(b.NativeModel); err != nil {
			return withField(at(field("native_model")), fmt.Errorf("model surface %q does not list native model %q", b.SurfaceRef, b.NativeModel))
		}
		if m.Name != b.NativeModel && !accessPointAliasRewritable(connection.Surface) {
			return withField(at(field("native_model")), fmt.Errorf("model %q backend %d: surface %q relays the request body verbatim, so its native_model must equal the model name", m.Name, j+1, b.SurfaceRef))
		}
		wires := accessPointServedWires(connection.Surface, b.NativeModel)
		if j == 0 {
			firstWires = wires
			continue
		}
		if strings.Join(wires, ",") != strings.Join(firstWires, ",") {
			return withField(at(field("surface_ref")), fmt.Errorf("model %q backend %d (%s/%s) serves %s, but backend 1 (%s/%s) serves %s: every backend of a model must speak the same client wire protocols natively (AIPlex does not translate between protocols)", m.Name, j+1, b.SurfaceRef, b.NativeModel, strings.Join(wires, ", "), m.Backends[0].SurfaceRef, m.Backends[0].NativeModel, strings.Join(firstWires, ", ")))
		}
	}
	return nil
}
