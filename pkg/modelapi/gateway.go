package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
)

type Owner struct{ TenantID, PrincipalID, SurfaceID string }
type Resource struct{ Kind, ID string }

// Ownership must bind provider resource IDs durably before they become usable.
// Unknown resources and principal/tenant mismatches must return errors.
type Ownership interface {
	Bind(context.Context, Owner, Resource) error
	Check(context.Context, Owner, Resource) error
}
type TokenSource interface {
	Token(context.Context) (string, error)
}
type TokenSourceFunc func(context.Context) (string, error)

func (f TokenSourceFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

type Metadata struct {
	References   []Resource
	DataURIs     []string
	Model, Agent string
	Resource     Resource
	Transport    Transport
}
type ContextResolver func(*http.Request) (contracts.RequestContext, error)
type Authorizer func(context.Context, contracts.RequestContext, Surface, Operation, Metadata) error
type SharedAuthorizer func(context.Context, contracts.RequestContext, Surface, Operation) bool

type UsageObservation struct {
	Source       string           `json:"source"`
	SurfaceID    string           `json:"surface_id"`
	OperationID  string           `json:"operation_id"`
	ResourceID   string           `json:"resource_id,omitempty"`
	Terminal     bool             `json:"terminal"`
	Counters     map[string]int64 `json:"counters"`
	Cumulative   bool             `json:"cumulative"`
	EventID      string           `json:"event_id,omitempty"`
	ModelVersion string           `json:"model_version,omitempty"`
	// Bound labels a session-ending observation without final usage
	// evidence (see execution.Observation.Bound).
	Bound string `json:"bound,omitempty"`
	// Dimensions carries provider-reported billing dimensions (see
	// execution.Observation.Dimensions).
	Dimensions map[string]string `json:"dimensions,omitempty"`
}
type Options struct {
	UpstreamHeaders http.Header
	HTTPClient      *http.Client
	TokenSource     TokenSource
	Context         ContextResolver
	Authorize       Authorizer
	AllowShared     SharedAuthorizer
	Ownership       Ownership
	Observe         func(context.Context, Owner, UsageObservation)
	Execution       ExecutionPreparer
	// LiveDialer is a separately configured transport adapter. Its network dialer
	// must enforce the same endpoint policy as HTTPClient.
	LiveDialer        LiveDialer
	AllowHTTPForTests bool
	MaxBodyBytes      int64
	MaxStreamDuration time.Duration
	// Credential presents the upstream credential on provider surfaces
	// (Surface.Provider != nil), which do not use TokenSource.
	Credential Credential
	// ObserveLimits receives each provider response's normalized rate-limit
	// metadata (provider surfaces only). It is advisory telemetry.
	ObserveLimits func(context.Context, Owner, providerlimits.Limits)
	// Batches durably tracks provider batches created through this gateway
	// and BatchSettler settles and records them (batch.go). Without a store,
	// batch operations are relayed and counted per exchange only.
	Batches      BatchStore
	BatchSettler BatchSettler
	// Clock stamps batch record created_at and settled_at; nil is time.Now.
	Clock func() time.Time
	// MaxUploadBytes bounds one spooled multipart upload (default
	// DefaultMaxUploadBytes, at most 1 GiB).
	MaxUploadBytes int64
	// Admit runs after authorization and before any quota lease, credential
	// acquisition or upstream I/O (HTTP requests and WebSocket session
	// admission alike). An *execution.StatusError becomes the response.
	Admit func(context.Context, contracts.RequestContext, Operation, Metadata) error
	// Hooks inspects request and response content (hooks.go). Nil leaves
	// every body untouched.
	Hooks ContentHooks
	// NoteError receives the error class of a failure the gateway attributes
	// itself (upstream_auth, upstream_quota_exhausted; upstream_errors.go).
	NoteError func(ctx context.Context, class string)
	// OperatorDetail reports whether the caller is an operator, who sees the
	// surface id in gateway-generated upstream errors. Nil means no one does.
	OperatorDetail func(ctx context.Context) bool
}
type compiledOperation struct {
	operation Operation
	pattern   *regexp.Regexp
	params    []string
}
type Gateway struct {
	surface    Surface
	options    Options
	client     *http.Client
	operations []compiledOperation
}

func NewGateway(s Surface, opts Options) (*Gateway, error) {
	if e := s.Validate(); e != nil {
		return nil, e
	}
	origin, _ := url.Parse(s.Origin)
	if origin.Scheme != "https" {
		ip := net.ParseIP(origin.Hostname())
		if !opts.AllowHTTPForTests || (origin.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, fmt.Errorf("model surface requires HTTPS")
		}
	}
	if s.Provider != nil {
		if opts.Credential == nil || len(opts.UpstreamHeaders) > 0 {
			return nil, fmt.Errorf("provider surface requires an upstream credential and no Google trusted headers")
		}
		opts.TokenSource = TokenSourceFunc(func(context.Context) (string, error) { return "", fmt.Errorf("provider surfaces use Credential") })
	}
	if opts.HTTPClient == nil || opts.Context == nil || opts.Authorize == nil || opts.TokenSource == nil {
		return nil, fmt.Errorf("native gateway requires safe HTTP client, trusted context, authorizer, and token source")
	}
	if opts.MaxBodyBytes == 0 {
		opts.MaxBodyBytes = 8 << 20
	}
	if opts.MaxBodyBytes < 1 || opts.MaxBodyBytes > 64<<20 {
		return nil, fmt.Errorf("invalid body bound")
	}
	if opts.MaxStreamDuration == 0 {
		opts.MaxStreamDuration = 30 * time.Minute
	}
	if opts.MaxStreamDuration < time.Second || opts.MaxStreamDuration > 24*time.Hour {
		return nil, fmt.Errorf("stream duration must be between one second and 24 hours")
	}
	trustedHeaders := http.Header{}
	for key, values := range opts.UpstreamHeaders {
		canonical := http.CanonicalHeaderKey(key)
		if canonical != "X-Goog-User-Project" && canonical != "X-Goog-Request-Params" {
			return nil, fmt.Errorf("unapproved trusted upstream header %s", key)
		}
		if len(values) != 1 || values[0] == "" || len(values[0]) > 8192 || strings.ContainsAny(values[0], "\r\n") || trustedHeaders.Get(canonical) != "" {
			return nil, fmt.Errorf("invalid trusted upstream header")
		}
		trustedHeaders[canonical] = append([]string(nil), values...)
	}
	opts.UpstreamHeaders = trustedHeaders
	client := *opts.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	g := &Gateway{surface: cloneSurface(s), options: opts, client: &client}
	for _, o := range s.Operations {
		if o.Stateful && opts.Ownership == nil {
			return nil, fmt.Errorf("stateful surface requires ownership store")
		}
		if o.Transport == WebSocket && opts.LiveDialer == nil {
			return nil, fmt.Errorf("Live operation requires WebSocket transport")
		}
		c, e := compileOperation(s, o)
		if e != nil {
			return nil, e
		}
		g.operations = append(g.operations, c)
	}
	return g, nil
}
func compileOperation(s Surface, o Operation) (compiledOperation, error) {
	fixed := map[string]string{"version": s.APIVersion, "project": s.Project, "location": s.Location, "publisher": s.Publisher, "endpoint": s.Endpoint}
	if s.Provider != nil {
		fixed = map[string]string{"base": s.Provider.BasePath}
		if fixed["base"] == "" {
			fixed["base"] = "/v1"
		}
	}
	pattern := "^"
	params := []string{}
	remaining := o.Path
	for {
		match := placeholderPattern.FindStringIndex(remaining)
		if match == nil {
			pattern += regexp.QuoteMeta(remaining)
			break
		}
		pattern += regexp.QuoteMeta(remaining[:match[0]])
		name := remaining[match[0]+1 : match[1]-1]
		if name == "id" || name == "model" {
			params = append(params, name)
			pattern += "([^/]+)"
		} else {
			pattern += regexp.QuoteMeta(fixed[name])
		}
		remaining = remaining[match[1]:]
	}
	pattern += "$"
	r, e := regexp.Compile(pattern)
	return compiledOperation{o, r, params}, e
}
func (g *Gateway) Surface() Surface { return cloneSurface(g.surface) }
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	limit := g.options.MaxStreamDuration
	if websocketUpgrade(r) {
		limit = g.surface.WebSocket.maxSession(limit)
	}
	bounded, cancel := context.WithTimeout(r.Context(), limit)
	defer cancel()
	r = r.WithContext(bounded)
	if g.surface.Provider != nil {
		g.serveProvider(w, r)
		return
	}
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
	metadata := Metadata{Model: params["model"], Resource: Resource{op.ResourceKind, params["id"]}, Transport: op.Transport}
	if metadata.Model != "" && !g.allowedModel(metadata.Model) {
		http.Error(w, "model is not allowed", http.StatusForbidden)
		return
	}
	if metadata.Resource.ID != "" && !validSegment(metadata.Resource.ID) {
		http.Error(w, "invalid resource identifier", http.StatusBadRequest)
		return
	}
	if op.Action == List && (g.options.AllowShared == nil || !g.options.AllowShared(r.Context(), trusted, g.Surface(), op)) {
		http.Error(w, "shared listing requires explicit authorization", http.StatusForbidden)
		return
	}
	if op.Stateful && metadata.Resource.ID != "" {
		if e = g.options.Ownership.Check(r.Context(), owner, metadata.Resource); e != nil {
			http.Error(w, "resource ownership denied", http.StatusForbidden)
			return
		}
	}
	if op.Transport == WebSocket {
		// Operation-level authorization before the upgrade; the model-level
		// decision and request-count admission follow the bounded setup.
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
	body, e := io.ReadAll(io.LimitReader(r.Body, g.options.MaxBodyBytes+1))
	if e != nil || int64(len(body)) > g.options.MaxBodyBytes {
		http.Error(w, "invalid or oversized request body", http.StatusBadRequest)
		return
	}
	opaque := (strings.HasSuffix(op.ID, "rawPredict") || strings.HasSuffix(op.ID, "streamRawPredict")) && !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json")
	if !opaque {
		e = g.validateBody(r.Context(), owner, op, &metadata, body)
	}
	if e != nil {
		http.Error(w, "request resource or model denied", http.StatusForbidden)
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
		if embeddingOperation(op) {
			// Embedding responses carry no model identifier.
			run.dispatchedModel = g.normalizeModel(metadata.Model)
		}
		defer run.close()
		if receipt, ok := lease.(execution.Receipt); ok {
			id := receipt.AttemptID()
			if id != "" && len(id) <= 1024 && !strings.ContainsAny(id, "\r\n") {
				w.Header().Set(execution.AttemptHeader, id)
			}
		}
	}
	token, e := g.options.TokenSource.Token(r.Context())
	if e != nil || strings.TrimSpace(token) == "" {
		http.Error(w, "upstream identity unavailable", http.StatusServiceUnavailable)
		return
	}
	upstream, e := url.Parse(strings.TrimSuffix(g.surface.Origin, "/") + r.URL.EscapedPath())
	if e != nil {
		http.Error(w, "invalid upstream request", 400)
		return
	}
	query := r.URL.Query()
	for _, key := range []string{"key", "access_token", "oauth_token"} {
		query.Del(key)
	}
	upstream.RawQuery = query.Encode()
	request, e := http.NewRequestWithContext(r.Context(), r.Method, upstream.String(), bytes.NewReader(body))
	if e != nil {
		http.Error(w, "invalid upstream request", 400)
		return
	}
	request.Header = forwardRequestHeaders(r.Header)
	applyTrustedHeaders(request.Header, g.options.UpstreamHeaders)
	request.Header.Set("Authorization", "Bearer "+token)
	if e = run.beforeDispatch(r.Context()); e != nil {
		writeExecutionError(w, e)
		return
	}
	response, e := g.client.Do(request)
	if e != nil {
		if err := run.finish(execution.Result{Err: e}); err != nil {
			writeExecutionError(w, err)
			return
		}
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if g.handleUpstreamAuth(w, r, run, response) {
		return
	}
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
		g.serveInspected(w, r, response, owner, op, metadata, run, func(out http.Header) { copyResponseHeaders(out, response.Header) })
		return
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	jsonResponse := mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
	if success && mediaType == "text/event-stream" {
		copyResponseHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		if run != nil {
			g.copyControlledSSE(w, r, response.Body, owner, op, run, response.StatusCode)
			return
		}
		g.copySSE(w, r, response.Body, owner, op)
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
		copyResponseHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(data)
		return
	}
	// Creation responses must be examined and durably bound before delivery.
	// Other potentially large bodies stream directly after headers are selected.
	if success && (op.Action == Create || jsonResponse) {
		data, e := io.ReadAll(io.LimitReader(response.Body, g.options.MaxBodyBytes+1))
		if e != nil || int64(len(data)) > g.options.MaxBodyBytes {
			http.Error(w, "upstream response exceeds observation bound", 502)
			return
		}
		if e = g.observeJSON(r.Context(), owner, op, data, true); e != nil {
			http.Error(w, "resource ownership could not be persisted", 502)
			return
		}
		copyResponseHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(data)
		return
	}
	copyResponseHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}
func (g *Gateway) match(method, path string) (Operation, map[string]string, bool) {
	if strings.ContainsAny(path, "%\\\r\n") || strings.Contains(path, "//") || strings.Contains(path, "/../") || strings.Contains(path, "/./") {
		return Operation{}, nil, false
	}
	for _, c := range g.operations {
		if c.operation.Method != method {
			continue
		}
		m := c.pattern.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		p := map[string]string{}
		for i, name := range c.params {
			p[name] = m[i+1]
		}
		return c.operation, p, true
	}
	return Operation{}, nil, false
}
func (g *Gateway) allowedModel(model string) bool {
	if g.surface.Provider != nil {
		for _, allowed := range g.surface.Models {
			if model == allowed {
				return true
			}
		}
		return false
	}
	prefix := "projects/" + g.surface.Project + "/locations/" + g.surface.Location + "/publishers/" + g.surface.Publisher + "/models/"
	relative := "publishers/" + g.surface.Publisher + "/models/"
	if strings.HasPrefix(model, "projects/") && !strings.HasPrefix(model, prefix) {
		return false
	}
	if strings.HasPrefix(model, "publishers/") && !strings.HasPrefix(model, relative) {
		return false
	}
	normalized := g.normalizeModel(model)
	for _, allowed := range g.surface.Models {
		if strings.HasPrefix(allowed, "projects/") && !strings.HasPrefix(allowed, prefix) {
			continue
		}
		if strings.HasPrefix(allowed, "publishers/") && !strings.HasPrefix(allowed, relative) {
			continue
		}
		if normalized == g.normalizeModel(allowed) {
			return true
		}
	}
	return false
}

func (g *Gateway) validateBody(ctx context.Context, owner Owner, op Operation, metadata *Metadata, body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		if op.ID == "interaction.create" || op.ID == "response.create" || op.ID == "openai.chat-completions" {
			return errors.New("model or agent is required")
		}
		return nil
	}
	if e := validateJSON(body); e != nil {
		return e
	}
	var doc map[string]json.RawMessage
	if e := json.Unmarshal(body, &doc); e != nil {
		return e
	}
	for key := range doc {
		for _, reserved := range []string{"model", "agent", "baseModel", "base_model", "previous_interaction_id", "previous_response_id", "cachedContent", "cached_content", "name"} {
			if strings.EqualFold(key, reserved) && key != reserved {
				return errors.New("ambiguous security field spelling")
			}
		}
	}
	if raw := doc["stream"]; raw != nil {
		var streaming bool
		if json.Unmarshal(raw, &streaming) != nil {
			return errors.New("invalid stream flag")
		}
		if streaming {
			metadata.Transport = SSE
		}
	}
	if e := g.validateDataURIs(body, metadata); e != nil {
		return e
	}
	modelPresent := false
	checkModel := func(raw json.RawMessage) error {
		if raw == nil {
			return nil
		}
		var model string
		if e := json.Unmarshal(raw, &model); e != nil || !g.allowedModel(model) {
			return errors.New("body model is not allowed")
		}
		if metadata.Model != "" && g.normalizeModel(metadata.Model) != g.normalizeModel(model) {
			return errors.New("body model differs from path model")
		}
		metadata.Model = model
		modelPresent = true
		return nil
	}
	for _, key := range []string{"model", "baseModel", "base_model"} {
		if e := checkModel(doc[key]); e != nil {
			return e
		}
	}
	if raw := doc["agent"]; raw != nil {
		var agent string
		if e := json.Unmarshal(raw, &agent); e != nil {
			return e
		}
		allowed := false
		for _, a := range g.surface.Agents {
			if a == agent {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("agent is not allowed")
		}
		metadata.Agent = agent
		modelPresent = true
	}
	if op.ID == "interaction.create" && metadata.Model != "" && metadata.Agent != "" {
		return errors.New("choose a model or an agent")
	}
	if op.ID == "interaction.create" || op.ID == "response.create" || op.ID == "openai.chat-completions" {
		if !modelPresent {
			return errors.New("model or agent is required")
		}
	}
	if raw := doc["previous_interaction_id"]; raw != nil {
		var id string
		if e := json.Unmarshal(raw, &id); e != nil || !validSegment(id) {
			return errors.New("invalid previous interaction")
		}
		if g.options.Ownership == nil {
			return errors.New("ownership required")
		}
		metadata.References = append(metadata.References, Resource{"interaction", id})
		if e := g.options.Ownership.Check(ctx, owner, Resource{"interaction", id}); e != nil {
			return e
		}
	}
	if op.ID == "models.fetchPredictOperation" {
		var name string
		raw := doc["operationName"]
		if raw == nil {
			raw = doc["operation_name"]
		}
		if raw == nil || json.Unmarshal(raw, &name) != nil || g.options.Ownership == nil {
			return errors.New("operation ownership required")
		}
		id, e := g.resourceID("prediction-operation", name)
		if e != nil {
			return e
		}
		metadata.Resource = Resource{"prediction-operation", id}
		metadata.References = append(metadata.References, metadata.Resource)
		if e = g.options.Ownership.Check(ctx, owner, metadata.Resource); e != nil {
			return e
		}
	}
	if raw := doc["previous_response_id"]; raw != nil {
		var id string
		if e := json.Unmarshal(raw, &id); e != nil || !validSegment(id) {
			return errors.New("invalid previous response")
		}
		if g.options.Ownership == nil {
			return errors.New("ownership required")
		}
		metadata.References = append(metadata.References, Resource{"response", id})
		if e := g.options.Ownership.Check(ctx, owner, Resource{"response", id}); e != nil {
			return e
		}
	}
	for _, key := range []string{"cachedContent", "cached_content"} {
		if raw := doc[key]; raw != nil {
			var name string
			if e := json.Unmarshal(raw, &name); e != nil {
				return e
			}
			id, e := g.resourceID("cache", name)
			if e != nil || g.options.Ownership == nil {
				return errors.New("cache ownership required")
			}
			metadata.References = append(metadata.References, Resource{"cache", id})
			if e = g.options.Ownership.Check(ctx, owner, Resource{"cache", id}); e != nil {
				return e
			}
		}
	}
	if raw := doc["name"]; raw != nil && op.Stateful {
		var name string
		if e := json.Unmarshal(raw, &name); e != nil {
			return e
		}
		id, e := g.resourceID(op.ResourceKind, name)
		if e != nil || metadata.Resource.ID == "" || id != metadata.Resource.ID {
			return errors.New("body resource differs from path")
		}
	}
	return nil
}
func (g *Gateway) resourceID(kind, name string) (string, error) {
	if validSegment(name) {
		return name, nil
	}
	if g.surface.Provider != nil {
		return g.providerResourceID(kind, name)
	}
	if kind == "prediction-operation" {
		prefix := "projects/" + g.surface.Project + "/locations/" + g.surface.Location + "/"
		if !strings.HasPrefix(name, prefix) {
			return "", errors.New("prediction operation outside configured scope")
		}
		relative := strings.TrimPrefix(name, prefix)
		parts := strings.Split(relative, "/")
		if len(parts) == 2 && parts[0] == "operations" && validSegment(parts[1]) {
			return name, nil
		}
		if len(parts) == 6 && parts[0] == "publishers" && parts[1] == g.surface.Publisher && parts[2] == "models" && g.allowedModel(parts[3]) && parts[4] == "operations" && validSegment(parts[5]) {
			return name, nil
		}
		return "", errors.New("invalid prediction operation resource")
	}
	if kind == "response" {
		prefix := "projects/" + g.surface.Project + "/locations/" + g.surface.Location + "/endpoints/" + g.surface.Endpoint + "/responses/"
		if !strings.HasPrefix(name, prefix) {
			return "", errors.New("response outside configured scope")
		}
		id := strings.TrimPrefix(name, prefix)
		if !validSegment(id) {
			return "", errors.New("invalid response ID")
		}
		return id, nil
	}
	collection := map[string]string{"interaction": "interactions", "cache": "cachedContents", "batch": "batchPredictionJobs", "tuning": "tuningJobs", "operation": "operations"}[kind]
	prefix := "projects/" + g.surface.Project + "/locations/" + g.surface.Location + "/" + collection + "/"
	if collection == "" || !strings.HasPrefix(name, prefix) {
		return "", errors.New("upstream resource outside configured scope")
	}
	id := strings.TrimPrefix(name, prefix)
	if !validSegment(id) {
		return "", errors.New("invalid upstream resource ID")
	}
	return id, nil
}
func forwardRequestHeaders(in http.Header) http.Header {
	out := http.Header{}
	// Only protocol data/attribution headers cross this boundary. Caller identity,
	// quota and billing claims are not copied as authoritative upstream facts.
	for _, key := range []string{"Content-Type", "Accept", "User-Agent", "X-Goog-Api-Client", "Anthropic-Version", "Anthropic-Beta", "Last-Event-ID"} {
		if values := in.Values(key); len(values) > 0 {
			out[key] = append([]string(nil), values...)
		}
	}
	return out
}
func copyResponseHeaders(out, in http.Header) {
	for _, key := range []string{"Content-Type", "Cache-Control", "Retry-After", "X-Request-Id", "X-Goog-Request-Id"} {
		if values := in.Values(key); len(values) > 0 {
			out[key] = append([]string(nil), values...)
		}
	}
}

// BoundedContext is useful for adapters finishing provider observations after a
// downstream disconnect. It never gives settlement an unbounded lifetime.
func BoundedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func (g *Gateway) normalizeModel(model string) string {
	if g.surface.Provider != nil {
		return model
	}
	prefix := "projects/" + g.surface.Project + "/locations/" + g.surface.Location + "/publishers/" + g.surface.Publisher + "/models/"
	model = strings.TrimPrefix(model, prefix)
	model = strings.TrimPrefix(model, "publishers/"+g.surface.Publisher+"/models/")
	model = strings.TrimPrefix(model, "models/")
	return strings.TrimPrefix(model, g.surface.Publisher+"/")
}

func normalizeDataURI(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "gs" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "\\\r\n") {
		return "", errors.New("invalid Cloud Storage URI")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return "", errors.New("ambiguous Cloud Storage path")
		}
	}
	return "gs://" + u.Host + u.Path, nil
}
func (g *Gateway) validateDataURIs(body []byte, metadata *Metadata) error {
	var root any
	if json.Unmarshal(body, &root) != nil {
		return errors.New("invalid native JSON")
	}
	var visit func(any) error
	visit = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			for _, child := range v {
				if e := visit(child); e != nil {
					return e
				}
			}
		case []any:
			for _, child := range v {
				if e := visit(child); e != nil {
					return e
				}
			}
		case string:
			if strings.HasPrefix(v, "gs://") {
				candidate, e := normalizeDataURI(v)
				if e != nil {
					return e
				}
				allowed := false
				for _, raw := range g.surface.DataPrefixes {
					prefix, _ := normalizeDataURI(raw)
					if candidate == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(candidate, strings.TrimSuffix(prefix, "/")+"/") {
						allowed = true
					}
				}
				if !allowed {
					return errors.New("Cloud Storage URI outside surface data prefixes")
				}
				metadata.DataURIs = append(metadata.DataURIs, candidate)
			}
		}
		return nil
	}
	return visit(root)
}

func applyTrustedHeaders(out, trusted http.Header) {
	for key, values := range trusted {
		out[key] = append([]string(nil), values...)
	}
}
