package telemetry

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// propagator is W3C Trace Context only. Baggage is neither read nor sent:
// it can carry caller data.
var propagator = propagation.TraceContext{}

const (
	headerTraceparent = "Traceparent"
	headerTracestate  = "Tracestate"
)

// Middleware starts one SERVER span per request. It continues a valid
// inbound W3C traceparent and otherwise starts a new trace. The span is named
// "{method} {route}" from the mounted route pattern that route returns after
// the handler ran ("{method}" when nothing matched); the raw path and query
// string, which can carry keys, are never recorded.
func Middleware(route func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			method := normalizeMethod(r.Method)
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := Tracer().Start(ctx, method, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attribute.String(AttrHTTPRequestMethod, method)))
			sw := &statusWriter{ResponseWriter: w}
			r = r.WithContext(ctx)
			finished := false
			finish := func(status int) {
				if finished {
					return
				}
				finished = true
				pattern := ""
				if route != nil {
					pattern = route(r)
				}
				if pattern != "" {
					span.SetName(method + " " + pattern)
					span.SetAttributes(attribute.String(AttrHTTPRoute, pattern))
				}
				span.SetAttributes(attribute.Int(AttrHTTPResponseStatusCode, status))
				if status >= 500 {
					span.SetAttributes(attribute.String(AttrErrorType, statusText(status)))
					span.SetStatus(codes.Error, "")
				}
				span.End()
				Metrics().RecordHTTP(ctx, method, pattern, status, time.Since(started).Seconds())
			}
			defer func() {
				if p := recover(); p != nil {
					finish(http.StatusInternalServerError)
					panic(p)
				}
				finish(sw.Status())
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

// TraceID returns the 32-hex trace identifier of the span in ctx, or "".
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.TraceID().IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// SpanID returns the 16-hex span identifier of the span in ctx, or "".
func SpanID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.SpanID().IsValid() {
		return ""
	}
	return sc.SpanID().String()
}

// Inject sets traceparent (and tracestate) for the span in ctx on an
// upstream request header, replacing any value already there. With upstream
// propagation off it removes them instead.
func Inject(ctx context.Context, header http.Header) {
	if header == nil {
		return
	}
	header.Del(headerTraceparent)
	header.Del(headerTracestate)
	if !PropagateUpstream() {
		return
	}
	propagator.Inject(ctx, propagation.HeaderCarrier(header))
}

// Client returns a copy of c whose requests carry the trace context of
// their own context (see Inject). Nil stays nil.
func Client(c *http.Client) *http.Client {
	if c == nil {
		return nil
	}
	if _, wrapped := c.Transport.(injectingTransport); wrapped {
		return c
	}
	copied := *c
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copied.Transport = injectingTransport{base: base}
	return &copied
}

type injectingTransport struct{ base http.RoundTripper }

func (t injectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// A RoundTripper must not modify the caller's request.
	out := new(http.Request)
	*out = *req
	out.Header = req.Header.Clone()
	if out.Header == nil {
		out.Header = http.Header{}
	}
	Inject(req.Context(), out.Header)
	return t.base.RoundTrip(out)
}

// statusWriter records the response status without changing the response.
// It forwards flushing, hijacking and unwrapping.
type statusWriter struct {
	http.ResponseWriter
	mu       sync.Mutex
	status   int
	hijacked bool
}

func (w *statusWriter) WriteHeader(status int) {
	w.mu.Lock()
	if w.status == 0 && status >= 200 {
		w.status = status
	}
	w.mu.Unlock()
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.mu.Unlock()
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response does not support hijacking")
	}
	conn, rw, err := h.Hijack()
	if err == nil {
		w.mu.Lock()
		w.hijacked = true
		w.mu.Unlock()
	}
	return conn, rw, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Status is the response status (101 for a hijacked connection, 200 when
// nothing was written).
func (w *statusWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case w.status != 0:
		return w.status
	case w.hijacked:
		return http.StatusSwitchingProtocols
	}
	return http.StatusOK
}

func normalizeMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return m
	}
	return "_OTHER"
}

func statusText(status int) string { return strconv.Itoa(status) }
