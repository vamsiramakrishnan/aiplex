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

type Options struct {
	HTTPClient        *http.Client
	TokenSource       TokenSource
	Context           func(*http.Request) (contracts.RequestContext, error)
	Authorize         func(context.Context, contracts.RequestContext, Connection, Request) error
	Prepare           func(context.Context, Invocation) (execution.Lease, error)
	BeforeDispatch    func(context.Context) error
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
	if options.HTTPClient == nil || !tokenSourcePresent(options.TokenSource) || options.Context == nil || options.Authorize == nil {
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
	request, err := ParseRequest(body, g.connection.AllowedTools)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err = g.options.Authorize(r.Context(), copyContext(trusted), g.Connection(), copyRequest(request)); err != nil {
		http.Error(w, "tool invocation denied", http.StatusForbidden)
		return
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
	token, err := g.options.TokenSource.Token(r.Context())
	if err != nil || !validBearer(token) {
		http.Error(w, "operator tool credential unavailable", http.StatusServiceUnavailable)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, g.connection.Endpoint, bytes.NewReader(request.Body))
	if err != nil {
		http.Error(w, "tool request unavailable", http.StatusServiceUnavailable)
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json")
	if g.connection.Mode == StatelessStreamableHTTP {
		upstream.Header.Set("Accept", "application/json, text/event-stream")
	}
	upstream.Header.Set("MCP-Protocol-Version", g.connection.ProtocolVersion)
	upstream.Header.Set("Authorization", "Bearer "+token)
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
	response, err := g.client.Do(upstream)
	if err != nil {
		http.Error(w, "tool upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	uncertainStatus = response.StatusCode
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, g.options.MaxResponseBytes+1))
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	validResponse := media == "application/json" && ValidateResponse(responseBody, request.ID) == nil
	if media == "text/event-stream" && g.connection.Mode == StatelessStreamableHTTP {
		validResponse = validateStreamResponse(responseBody, request.ID) == nil
	}
	terminal := readErr == nil && int64(len(responseBody)) <= g.options.MaxResponseBytes && response.StatusCode >= 200 && response.StatusCode < 300 && mediaErr == nil && response.Header.Get("MCP-Session-Id") == "" && validResponse
	if !terminal {
		http.Error(w, "tool response did not establish terminal usage", http.StatusBadGateway)
		return
	}
	if lease != nil {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		err = lease.Observe(cleanup, execution.Observation{Source: "upstream", Counters: map[string]int64{"requests": 1, "tool_calls": 1}, Cumulative: true, Terminal: true})
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
