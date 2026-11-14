package toolexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

type TokenSource interface {
	Token(context.Context) (string, error)
}
type TokenSourceFunc func(context.Context) (string, error)

func (f TokenSourceFunc) Token(ctx context.Context) (string, error) {
	if f == nil {
		return "", fmt.Errorf("operator token source unavailable")
	}
	return f(ctx)
}

type Invocation struct {
	Context    contracts.RequestContext
	Connection Connection
	Request    Request
	Headers    http.Header
}

// Credential applies one upstream credential to an outbound request. It must
// refuse requests that do not target the origin it is bound to.
type Credential interface {
	Apply(*http.Request) error
}

// CredentialSource yields the credential for one dispatch. It is an
// alternative to TokenSource for connections with an explicit auth block.
type CredentialSource func(context.Context) (Credential, error)

type bearerToken string

func (b bearerToken) Apply(r *http.Request) error {
	r.Header.Set("Authorization", "Bearer "+string(b))
	return nil
}

type Options struct {
	HTTPClient  *http.Client
	TokenSource TokenSource
	Credentials CredentialSource
	// Unauthorized is called when the upstream answers HTTP 401 for the
	// credential it was sent, so cached tokens can be invalidated.
	Unauthorized func(context.Context, Credential)
	// List enables governed tools/list. Nil keeps tools/call only.
	List           *ListOptions
	Context        func(*http.Request) (contracts.RequestContext, error)
	Authorize      func(context.Context, contracts.RequestContext, Connection, Request) error
	Prepare        func(context.Context, Invocation) (execution.Lease, error)
	BeforeDispatch func(context.Context) error
	// InspectRequest runs once after authorization and before the quota
	// lease and dispatch. It returns the request body to send (the caller's
	// bytes unless a hook transformed them). A *Denial refuses the call.
	InspectRequest func(context.Context, Request) ([]byte, error)
	// InspectResponse runs once on a complete, valid successful response
	// before it is written. A *Denial refuses the call.
	InspectResponse func(ctx context.Context, request Request, mediaType string, body []byte) ([]byte, error)
	// Retry retries transport failures of tools/list and of tools/call on
	// tools the host marks repeatable (retry.go). Nil never retries.
	Retry *TransportRetry
	// ObserveUsage receives a call's usage (requests, tool_calls, response
	// bytes and duration; usage.go) when the call has no execution lease.
	// With a lease, the lease's terminal observation carries the same
	// counters instead, so usage is reported once.
	ObserveUsage      func(context.Context, map[string]int64)
	AllowHTTPForTests bool
	MaxBodyBytes      int64
	MaxResponseBytes  int64
	MaxDuration       time.Duration
}

type Gateway struct {
	connection Connection
	options    Options
	client     *http.Client
}

func NewGateway(connection Connection, options Options) (*Gateway, error) {
	if err := connection.Validate(); err != nil {
		return nil, err
	}
	u, _ := url.Parse(connection.Endpoint)
	if u.Scheme != "https" && !options.AllowHTTPForTests {
		return nil, fmt.Errorf("tool endpoint requires HTTPS")
	}
	if options.HTTPClient == nil || (!tokenSourcePresent(options.TokenSource) && options.Credentials == nil) || options.Context == nil || options.Authorize == nil {
		return nil, fmt.Errorf("safe HTTP, operator token, verified context and authorizer are required")
	}
	if options.MaxBodyBytes == 0 {
		options.MaxBodyBytes = 1 << 20
	}
	if options.MaxResponseBytes == 0 {
		options.MaxResponseBytes = 8 << 20
	}
	if options.MaxDuration == 0 {
		options.MaxDuration = 5 * time.Minute
	}
	// A connection's own bounds apply to its calls (usage.go); a quota
	// binding that meters bytes or duration reserves exactly these.
	options.MaxResponseBytes = connection.EffectiveMaxResponseBytes(options.MaxResponseBytes)
	options.MaxDuration = connection.EffectiveMaxDuration(options.MaxDuration)
	if options.MaxBodyBytes < 1 || options.MaxBodyBytes > 8<<20 || options.MaxResponseBytes < 1 || options.MaxResponseBytes > 64<<20 || options.MaxDuration < time.Second || options.MaxDuration > 30*time.Minute {
		return nil, fmt.Errorf("tool execution bounds are invalid")
	}
	client := *options.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	connection.AllowedTools = append([]string(nil), connection.AllowedTools...)
	return &Gateway{connection: connection, options: options, client: &client}, nil
}

func tokenSourcePresent(source TokenSource) bool {
	if source == nil {
		return false
	}
	value := reflect.ValueOf(source)
	switch value.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan:
		return !value.IsNil()
	}
	return true
}

func (g *Gateway) Connection() Connection {
	c := g.connection
	c.AllowedTools = append([]string(nil), c.AllowedTools...)
	return c
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("MCP-Session-Id") != "" || r.Header.Get("Last-Event-ID") != "" {
		http.Error(w, "session and replay transports are unsupported by this connection", http.StatusBadRequest)
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && v != g.connection.ProtocolVersion {
		http.Error(w, "MCP protocol version does not match the connection", http.StatusBadRequest)
		return
	}
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		http.Error(w, "JSON content type required", http.StatusUnsupportedMediaType)
		return
	}
	bounded, cancel := context.WithTimeout(r.Context(), g.options.MaxDuration)
	defer cancel()
	r = r.WithContext(bounded)
	trusted, err := g.options.Context(r)
	if err != nil || trusted.Validate() != nil {
		http.Error(w, "verified request context required", http.StatusUnauthorized)
		return
	}
	if trusted.ResourceID != g.connection.ID {
		http.Error(w, "request context connection mismatch", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, g.options.MaxBodyBytes+1))
	if err != nil || int64(len(body)) > g.options.MaxBodyBytes {
		http.Error(w, "invalid or oversized tool request", http.StatusBadRequest)
		return
	}
	if g.options.List != nil && isListRequest(body) {
		g.serveList(w, r, trusted, body)
		return
	}
	request, err := ParseRequest(body, g.connection.AllowedTools)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err = g.options.Authorize(r.Context(), copyContext(trusted), g.Connection(), copyRequest(request)); err != nil {
		rpcDenied(w, request.ID, err)
		return
	}
	if g.options.InspectRequest != nil {
		inspected, err := g.options.InspectRequest(r.Context(), copyRequest(request))
		if err != nil {
			rpcDenied(w, request.ID, err)
			return
		}
		if !bytes.Equal(inspected, request.Body) {
			// The transformed body is parsed again so the quota lease, the
			// digests and the dispatch all describe the bytes sent.
			id := request.ID
			if request, err = ParseRequest(inspected, g.connection.AllowedTools); err != nil {
				rpcDenied(w, id, &Denial{Status: http.StatusServiceUnavailable, Code: RPCForbidden, Reason: "hook_unavailable", Message: "a content policy check returned an invalid request"})
				return
			}
		}
	}
	var lease execution.Lease
	if g.options.Prepare != nil {
		headers := http.Header{}
		for _, key := range []string{"Idempotency-Key", "X-Aiplex-Funding-Account", "X-Aiplex-Operation-Id"} {
			if values := r.Header.Values(key); len(values) > 0 {
				headers[key] = append([]string(nil), values...)
			}
		}
		lease, err = g.options.Prepare(r.Context(), Invocation{Context: copyContext(trusted), Connection: g.Connection(), Request: copyRequest(request), Headers: headers})
		if err != nil {
			executionError(w, err, "tool admission unavailable")
			return
		}
		if lease == nil {
			executionError(w, errors.New("nil execution lease"), "tool admission unavailable")
			return
		}
		if receipt, ok := lease.(execution.Receipt); ok {
			if attempt := receipt.AttemptID(); validIdentifier(attempt) {
				w.Header().Set("X-Aiplex-Attempt-ID", attempt)
			}
		}
	}
	claimed, finished := false, false
	uncertainStatus := 0
	defer func() {
		if lease == nil || finished {
			return
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer stop()
		if !claimed {
			_ = lease.Abort(cleanup)
		} else {
			_ = lease.Finish(cleanup, execution.Result{StatusCode: uncertainStatus, Err: errors.New("tool execution did not establish terminal usage")})
		}
	}()
	// The upstream sees the exchange's span context in params._meta as well as
	// in the HTTP headers; the rest of the caller's request is relayed
	// byte for byte. A transport retry (retry.go) re-sends exactly this
	// patched body: policy rules and hooks have already run and do not run
	// again.
	upstreamBody, err := withTraceMeta(r.Context(), request.Body)
	if err != nil {
		http.Error(w, "invalid or oversized tool request", http.StatusBadRequest)
		return
	}
	// The credential is checked before the dispatch claim; send acquires it
	// again for each try.
	if _, _, err = g.upstreamRequest(r.Context(), upstreamBody); err != nil {
		http.Error(w, "operator tool credential unavailable", http.StatusServiceUnavailable)
		return
	}
	if g.options.BeforeDispatch != nil {
		if err = g.options.BeforeDispatch(r.Context()); err != nil {
			executionError(w, err, "tool authorization changed")
			return
		}
	}
	if lease != nil {
		if err = lease.BeforeDispatch(r.Context()); err != nil {
			executionError(w, err, "tool dispatch denied")
			return
		}
	}
	claimed = true
	// Only a usage-metering lease receives never-started results (the rule
	// shared with model exchanges, pkg/execution dispatch.go); a
	// request-count lease keeps its contract unchanged.
	metered := lease != nil && usageProfile(lease)
	finishEarly := func(result execution.Result) {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer stop()
		finished = true
		_ = lease.Finish(cleanup, result)
	}
	repeatable := g.options.Retry != nil && g.options.Retry.Call != nil && g.options.Retry.Call(r.Context(), copyRequest(request))
	sendContext, connected := execution.ConnectionTrace(r.Context())
	// The duration runs on the monotonic clock from the first dispatch to
	// the end of the response body (time.Since uses the monotonic reading).
	dispatchedAt := time.Now()
	credential, response, err := g.send(sendContext, upstreamBody, repeatable)
	if err != nil {
		// No try obtained a connection (a dial refused, an endpoint policy
		// refusal, or a credential that could not be resolved for the try):
		// nothing was sent.
		if metered && !connected() {
			finishEarly(execution.Result{NotSent: true, Err: err})
		}
		if _, ok := err.(errCredential); ok {
			http.Error(w, "operator tool credential unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "tool upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	uncertainStatus = response.StatusCode
	g.unauthorized(r.Context(), response, credential)
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, g.options.MaxResponseBytes+1))
	elapsed := time.Since(dispatchedAt)
	complete := readErr == nil && int64(len(responseBody)) <= g.options.MaxResponseBytes
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	validResponse := media == "application/json" && ValidateResponse(responseBody, request.ID) == nil
	if media == "text/event-stream" && g.connection.Mode == StatelessStreamableHTTP {
		validResponse = validateStreamResponse(responseBody, request.ID) == nil
	}
	terminal := complete && response.StatusCode >= 200 && response.StatusCode < 300 && mediaErr == nil && response.Header.Get("MCP-Session-Id") == "" && validResponse
	if !terminal {
		if metered && complete && execution.RejectedBeforeProcessing(response.StatusCode) {
			finishEarly(execution.Result{StatusCode: response.StatusCode, ResponseComplete: true, Rejected: true, Err: errors.New("tool request refused before processing")})
		}
		http.Error(w, "tool response did not establish terminal usage", http.StatusBadGateway)
		return
	}
	usage := map[string]int64{"requests": 1, "tool_calls": 1, CounterToolResponseBytes: int64(len(responseBody)), CounterToolDurationMilliseconds: durationMilliseconds(elapsed, g.options.MaxDuration)}
	if lease == nil && g.options.ObserveUsage != nil {
		g.options.ObserveUsage(r.Context(), usage)
	}
	if lease != nil {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		err = lease.Observe(cleanup, execution.Observation{Source: "upstream", Counters: usage, Cumulative: true, Terminal: true})
		if err == nil {
			// A failed acknowledgement is retried through durable host evidence;
			// do not replace a terminal Finish with a contradictory unknown one.
			finished = true
			err = lease.Finish(cleanup, execution.Result{StatusCode: response.StatusCode, ResponseComplete: true, Terminal: true})
		}
		stop()
		if err != nil {
			executionError(w, err, "tool accounting acknowledgement unavailable")
			return
		}
	}
	finished = true
	if g.options.InspectResponse != nil {
		inspected, err := g.options.InspectResponse(r.Context(), copyRequest(request), media, responseBody)
		if err != nil {
			rpcDenied(w, request.ID, err)
			return
		}
		responseBody = inspected
	}
	for _, name := range []string{"Content-Type", "Cache-Control", "X-Request-ID"} {
		if value := response.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
}

func copyContext(value contracts.RequestContext) contracts.RequestContext {
	if value.MembershipSnapshotRef != nil {
		ref := *value.MembershipSnapshotRef
		value.MembershipSnapshotRef = &ref
	}
	return value
}

func validBearer(token string) bool {
	if token == "" {
		return false
	}
	for _, character := range token {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func executionError(w http.ResponseWriter, err error, fallback string) {
	var status *execution.StatusError
	if errors.As(err, &status) && status.Status >= 400 && status.Status <= 599 {
		if status.RetryAfter > 0 && (status.Status == 429 || status.Status == 503) {
			seconds := int64(status.RetryAfter / time.Second)
			if status.RetryAfter%time.Second != 0 {
				seconds++
			}
			w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		}
		http.Error(w, status.Message, status.Status)
		return
	}
	http.Error(w, fallback, http.StatusServiceUnavailable)
}
