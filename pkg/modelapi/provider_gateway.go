package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
)

// providerRequestError is a caller-correctable rejection raised before any
// credential acquisition or upstream I/O.
type providerRequestError struct {
	status  int
	message string
}

func (e *providerRequestError) Error() string { return e.message }

func badRequest(format string, args ...any) error {
	return &providerRequestError{status: http.StatusBadRequest, message: fmt.Sprintf(format, args...)}
}
func forbidden(message string) error {
	return &providerRequestError{status: http.StatusForbidden, message: message}
}

func writeProviderRequestError(w http.ResponseWriter, err error) {
	var typed *providerRequestError
	if errors.As(err, &typed) {
		http.Error(w, typed.message, typed.status)
		return
	}
	http.Error(w, "request denied", http.StatusForbidden)
}

// serveProvider is the provider-surface request path. It shares operation
// matching, authorization, execution leases, SSE observation and ownership
// with the Google path but owns header shaping, credentials and limits.
func (g *Gateway) serveProvider(w http.ResponseWriter, r *http.Request) {
	op, params, ok := g.match(r.Method, r.URL.Path)
	if !ok {
		http.Error(w, "operation is not allowed by this surface", http.StatusNotFound)
		return
	}
	trusted, e := g.options.Context(r)
	if e != nil || trusted.Validate() != nil {
		http.Error(w, "verified request context required", http.StatusUnauthorized)
		return
	}
	if trusted.ResourceID != g.surface.ID {
		http.Error(w, "request context surface mismatch", http.StatusForbidden)
		return
	}
	owner := Owner{trusted.TenantID, trusted.PrincipalID, g.surface.ID}
	if g.serveProviderExtended(w, r, trusted, owner, op, params) {
		return
	}
	if bedrockInvoke(op) {
		// The model id (path) selects the InvokeModel body family.
		r = r.WithContext(withInvokeModel(r.Context(), params["model"]))
	}
	if op.ID == geminiLiveOperation {
		// Gemini Live on the Developer API: the Vertex Live relay with the
		// surface credential. As on the Google Cloud path, the operation is
		// authorized before the upgrade and the model after the setup.
		metadata := Metadata{Resource: Resource{op.ResourceKind, ""}, Transport: WebSocket}
		if _, e = g.providerRequestHeaders(r.Header); e != nil {
			writeProviderRequestError(w, e)
			return
		}
		if e = g.options.Authorize(r.Context(), trusted, g.Surface(), op, metadata); e != nil {
			http.Error(w, "operation denied", http.StatusForbidden)
			return
		}
		if !g.sessionHooks(w, r, op, metadata) {
			return
		}
		g.serveLive(w, r, trusted, owner, op)
		return
	}
	if op.Transport == WebSocket {
		g.serveProviderWebSocket(w, r, trusted, owner, op)
		return
	}
	metadata := Metadata{Model: params["model"], Transport: op.Transport}
	if metadata.Model != "" && !g.allowedModel(metadata.Model) {
		http.Error(w, "model is not allowed", http.StatusForbidden)
		return
	}
	if e = g.preflightUtility(r.Context(), trusted, owner, op, params, &metadata); e != nil {
		writeProviderRequestError(w, e)
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, g.options.MaxBodyBytes+1))
	if e != nil || int64(len(body)) > g.options.MaxBodyBytes {
		http.Error(w, "invalid or oversized request body", http.StatusBadRequest)
		return
	}
	if body, e = g.validateProviderBody(r.Context(), owner, op, &metadata, body); e != nil {
		writeProviderRequestError(w, e)
		return
	}
	headers, e := g.providerRequestHeaders(r.Header)
	if e != nil {
		writeProviderRequestError(w, e)
		return
	}
	if e = g.options.Authorize(r.Context(), trusted, g.Surface(), op, metadata); e != nil {
		http.Error(w, "operation denied", http.StatusForbidden)
		return
	}
	body, inspect, lease, e := g.prepareDispatch(r, trusted, op, metadata, body)
	if e != nil {
		writeDispatchError(w, e)
		return
	}
	var run *executionRun
	if lease != nil {
		run = &executionRun{lease: lease, ctx: r.Context()}
		if bedrockConverse(op) || embeddingOperation(op) {
			run.dispatchedModel = metadata.Model
		}
		defer run.close()
		if receipt, ok := lease.(execution.Receipt); ok {
			id := receipt.AttemptID()
			if id != "" && len(id) <= 1024 && !strings.ContainsAny(id, "\r\n") {
				w.Header().Set(execution.AttemptHeader, id)
			}
		}
	}
	hit, store := g.cacheLookup(r, op, metadata, body)
	if hit != nil {
		g.serveCached(w, r, op, metadata, run, inspect, hit)
		return
	}
	upstream, e := url.Parse(g.upstreamOrigin() + r.URL.EscapedPath())
	if e != nil {
		http.Error(w, "invalid upstream request", http.StatusBadRequest)
		return
	}
	// Caller query parameters never cross: only operator-pinned values do.
	upstream.RawQuery = utilityQuery(op, r.URL.Query())
	if strings.Contains(op.Path, "/openai/deployments/") {
		upstream.RawQuery = url.Values{"api-version": {g.surface.Provider.APIVersion}}.Encode()
	}
	request, e := http.NewRequestWithContext(r.Context(), r.Method, upstream.String(), bytes.NewReader(body))
	if e != nil {
		http.Error(w, "invalid upstream request", http.StatusBadRequest)
		return
	}
	request.Header = headers
	// The credential is applied last so request signing covers final headers.
	if e = g.options.Credential.Authenticate(r.Context(), request); e != nil {
		http.Error(w, "upstream identity unavailable", http.StatusServiceUnavailable)
		return
	}
	if e = run.beforeDispatch(r.Context()); e != nil {
		writeExecutionError(w, e)
		return
	}
	response, notSent, e := g.do(request)
	if e != nil {
		if err := run.finish(transportResult(e, notSent)); err != nil {
			writeExecutionError(w, err)
			return
		}
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if g.options.ObserveLimits != nil {
		g.options.ObserveLimits(r.Context(), owner, providerlimits.Parse(g.surface.Provider.ID, response.StatusCode, response.Header, time.Now()))
	}
	if g.handleUpstreamAuth(w, r, run, response) {
		return
	}
	g.noteRejectionClass(r, response)
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		if err := run.finish(execution.Result{StatusCode: response.StatusCode, Err: errors.New("upstream redirect refused")}); err != nil {
			writeExecutionError(w, err)
			return
		}
		http.Error(w, "upstream redirects are not supported", http.StatusBadGateway)
		return
	}
	success := response.StatusCode >= 200 && response.StatusCode < 300
	if inspect && success {
		g.serveInspected(w, r, response, owner, op, metadata, run, store, func(out http.Header) { g.copyProviderResponseHeaders(out, response.Header, response.StatusCode) })
		return
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	jsonResponse := mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
	if success && mediaType == "text/event-stream" {
		g.copyProviderResponseHeaders(w.Header(), response.Header, response.StatusCode)
		w.WriteHeader(response.StatusCode)
		if run != nil {
			g.copyControlledSSE(w, r, response.Body, owner, op, run, response.StatusCode)
			return
		}
		g.copySSE(w, r, response.Body, owner, op)
		return
	}
	if success && mediaType == "application/vnd.amazon.eventstream" && op.Transport == EventStream {
		g.copyProviderResponseHeaders(w.Header(), response.Header, response.StatusCode)
		w.WriteHeader(response.StatusCode)
		g.copyEventStream(w, r, response.Body, owner, op, run, response.StatusCode)
		return
	}
	if run != nil {
		data, err := io.ReadAll(io.LimitReader(response.Body, g.options.MaxBodyBytes+1))
		if err != nil || int64(len(data)) > g.options.MaxBodyBytes {
			if err == nil {
				err = errors.New("response observation bound exceeded")
			}
			_ = run.finish(execution.Result{StatusCode: response.StatusCode, Err: err})
			http.Error(w, "upstream response exceeds observation bound", http.StatusBadGateway)
			return
		}
		terminal := false
		if success && (op.Action == Create || jsonResponse) {
			terminal, err = g.observeControlledJSON(owner, op, data, run)
			if err != nil {
				_ = run.finish(execution.Result{StatusCode: response.StatusCode, ResponseComplete: true, Err: err})
				http.Error(w, "upstream response could not be acknowledged", http.StatusBadGateway)
				return
			}
		}
		if err = run.finish(execution.Result{StatusCode: response.StatusCode, ResponseComplete: true, Terminal: terminal, Rejected: !success && g.rejectedBeforeProcessing(response.StatusCode)}); err != nil {
			writeExecutionError(w, err)
			return
		}
		if store != nil && success && jsonResponse {
			store(response.StatusCode, response.Header, data)
		}
		g.copyProviderResponseHeaders(w.Header(), response.Header, response.StatusCode)
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(data)
		return
	}
	if success && (op.Action == Create || jsonResponse) {
		data, e := io.ReadAll(io.LimitReader(response.Body, g.options.MaxBodyBytes+1))
		if e != nil || int64(len(data)) > g.options.MaxBodyBytes {
			http.Error(w, "upstream response exceeds observation bound", http.StatusBadGateway)
			return
		}
		if e = g.observeJSON(r.Context(), owner, op, data, true); e != nil {
			http.Error(w, "resource ownership could not be persisted", http.StatusBadGateway)
			return
		}
		if store != nil && jsonResponse {
			store(response.StatusCode, response.Header, data)
		}
		g.copyProviderResponseHeaders(w.Header(), response.Header, response.StatusCode)
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(data)
		return
	}
	g.copyProviderResponseHeaders(w.Header(), response.Header, response.StatusCode)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

// providerRequestHeaders forwards only protocol data headers. Caller
// credentials (Authorization, x-api-key, api-key, AWS signatures), billing,
// tier and organization selectors never cross; operator pins are applied.
func (g *Gateway) providerRequestHeaders(in http.Header) (http.Header, error) {
	out := http.Header{}
	for _, key := range []string{"Content-Type", "Accept", "User-Agent"} {
		if values := in.Values(key); len(values) > 0 {
			out[key] = append([]string(nil), values...)
		}
	}
	p := g.surface.Provider
	if err := utilityRequestHeaders(p.ID, in, out); err != nil {
		return nil, err
	}
	switch p.ID {
	case ProviderOpenAI:
		if p.Organization != "" {
			out.Set("OpenAI-Organization", p.Organization)
		}
		if p.Project != "" {
			out.Set("OpenAI-Project", p.Project)
		}
	case ProviderAnthropic:
		for _, version := range in.Values("Anthropic-Version") {
			if strings.TrimSpace(version) != p.AnthropicVersion {
				return nil, badRequest("anthropic-version is pinned to %s by this surface", p.AnthropicVersion)
			}
		}
		out.Set("Anthropic-Version", p.AnthropicVersion)
		var betas []string
		for _, value := range in.Values("Anthropic-Beta") {
			for _, beta := range strings.Split(value, ",") {
				beta = strings.TrimSpace(beta)
				if beta == "" {
					continue
				}
				allowed := false
				for _, candidate := range p.AnthropicBetas {
					allowed = allowed || candidate == beta
				}
				if !allowed {
					return nil, badRequest("anthropic-beta %q is not enabled on this surface", beta)
				}
				betas = append(betas, beta)
			}
		}
		if len(betas) > 0 {
			out.Set("Anthropic-Beta", strings.Join(betas, ","))
		}
	case ProviderBedrock:
		if !g.surface.BedrockMantle() {
			break
		}
		// Mantle Messages: the documented anthropic-version header, pinned.
		// Beta features are not enabled on this surface.
		for _, version := range in.Values("Anthropic-Version") {
			if strings.TrimSpace(version) != BedrockMantleAnthropicVersion {
				return nil, badRequest("anthropic-version is pinned to %s on bedrock-mantle", BedrockMantleAnthropicVersion)
			}
		}
		if len(in.Values("Anthropic-Beta")) > 0 {
			return nil, badRequest("anthropic-beta is not enabled on this surface")
		}
		out.Set("Anthropic-Version", BedrockMantleAnthropicVersion)
	}
	return out, nil
}

func (g *Gateway) copyProviderResponseHeaders(out, in http.Header, status int) {
	names := []string{"Content-Type", "Cache-Control", "X-Request-Id", "Request-Id", "Apim-Request-Id", "X-Amzn-Requestid", "X-Amzn-Errortype"}
	if status >= 400 {
		// A limited caller needs the backoff hint; it carries no counts.
		names = append(names, "Retry-After", "Retry-After-Ms")
	}
	if g.surface.Provider.LimitHeaderPassthrough {
		names = append(names, providerlimits.Headers(g.surface.Provider.ID)...)
	}
	for _, key := range names {
		if values := in.Values(key); len(values) > 0 {
			out[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
		}
	}
}

func (g *Gateway) validateProviderBody(ctx context.Context, owner Owner, op Operation, metadata *Metadata, body []byte) ([]byte, error) {
	if out, handled, err := g.utilityBody(ctx, owner, op, metadata, body); handled {
		return out, err
	}
	format := UsageFormat(g.surface.Provider.ID, op.ID)
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, badRequest("a native JSON request body is required")
	}
	if err := validateJSON(body); err != nil {
		return nil, badRequest("invalid native JSON")
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(body, &doc) != nil || doc == nil {
		return nil, badRequest("a native JSON object is required")
	}
	for key := range doc {
		for _, reserved := range []string{"model", "stream", "stream_options", "previous_response_id", "conversation", "modelId"} {
			if strings.EqualFold(key, reserved) && key != reserved {
				return nil, forbidden("ambiguous security field spelling")
			}
		}
	}
	if format == FormatBedrockConverse || format == FormatBedrockInvoke {
		// The model is the path segment; a body model selector is not part of
		// these contracts and is refused rather than silently ignored.
		if doc["modelId"] != nil {
			return nil, forbidden("model is selected by the path")
		}
		if metadata.Model == "" {
			return nil, forbidden("model is required")
		}
		return body, nil
	}
	if raw := doc["model"]; raw != nil {
		var model string
		if json.Unmarshal(raw, &model) != nil || !g.allowedModel(model) {
			return nil, forbidden("body model is not allowed")
		}
		if metadata.Model != "" && metadata.Model != model {
			return nil, forbidden("body model differs from path deployment")
		}
		metadata.Model = model
	}
	if metadata.Model == "" {
		return nil, forbidden("model is required")
	}
	streaming := false
	if raw := doc["stream"]; raw != nil {
		if json.Unmarshal(raw, &streaming) != nil {
			return nil, badRequest("stream must be a boolean")
		}
		if streaming {
			metadata.Transport = SSE
		}
	}
	switch format {
	case FormatOpenAIChat:
		if streaming {
			return g.applyStreamUsage(body, doc)
		}
	case FormatOpenAIResponses:
		if raw := doc["conversation"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, forbidden("provider conversations are not ownership-scoped by this surface")
		}
		if raw := doc["previous_response_id"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var id string
			if json.Unmarshal(raw, &id) != nil || !validSegment(id) {
				return nil, badRequest("invalid previous response")
			}
			if g.options.Ownership == nil {
				return nil, forbidden("ownership required")
			}
			metadata.References = append(metadata.References, Resource{"response", id})
			if g.options.Ownership.Check(ctx, owner, Resource{"response", id}) != nil {
				return nil, forbidden("previous response ownership denied")
			}
		}
	}
	return body, nil
}

// applyStreamUsage enforces the surface stream_usage policy for a streaming
// Chat Completions request. "inject" is the only body mutation in this path.
func (g *Gateway) applyStreamUsage(body []byte, doc map[string]json.RawMessage) ([]byte, error) {
	policy := g.surface.Provider.EffectiveStreamUsage()
	if policy == StreamUsageOff {
		return body, nil
	}
	include, present := false, false
	if raw := doc["stream_options"]; raw != nil && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		present = true
	} else if raw != nil {
		var options map[string]json.RawMessage
		if json.Unmarshal(raw, &options) != nil || options == nil {
			return nil, badRequest("stream_options must be an object")
		}
		if value := options["include_usage"]; value != nil {
			if json.Unmarshal(value, &include) != nil {
				return nil, badRequest("stream_options.include_usage must be a boolean")
			}
			present = true
		}
	}
	if include {
		return body, nil
	}
	if policy == StreamUsageRequire || present {
		return nil, badRequest("this surface requires stream_options.include_usage: true on streaming requests")
	}
	return injectIncludeUsage(body)
}

// injectIncludeUsage inserts include_usage into the existing top-level
// stream_options object (or adds that object) without re-encoding any other
// byte of the caller's payload.
func injectIncludeUsage(body []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, badRequest("a native JSON object is required")
	}
	open := bytes.IndexByte(body, '{')
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, badRequest("invalid native JSON")
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return nil, badRequest("invalid native JSON")
		}
		if key != "stream_options" {
			continue
		}
		end := int(d.InputOffset())
		start := end - len(raw)
		var members map[string]json.RawMessage
		if json.Unmarshal(raw, &members) != nil || start < 0 || body[start] != '{' {
			return nil, badRequest("stream_options must be an object")
		}
		insert := `"include_usage":true`
		if len(members) > 0 {
			insert += ","
		}
		out := append(append(append([]byte(nil), body[:start+1]...), insert...), body[start+1:]...)
		return out, validateJSON(out)
	}
	// The object is non-empty: a model member is always required before here.
	out := append(append(append([]byte(nil), body[:open+1]...), `"stream_options":{"include_usage":true},`...), body[open+1:]...)
	return out, validateJSON(out)
}
