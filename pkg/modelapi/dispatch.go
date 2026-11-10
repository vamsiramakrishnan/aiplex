package modelapi

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// DispatchOutcome describes one upstream dispatch for a routing layer that
// decides whether the exchange may be retried or moved to another backend.
// It carries no body.
type DispatchOutcome struct {
	// NotSent is true when the request provably never left: the transport
	// failed before a connection to the provider was established, so no
	// request byte was written.
	NotSent bool
	// Err is the transport error; nil when a response was read.
	Err error
	// Status and Header are the provider's response status and headers.
	Status int
	Header http.Header
}

// do sends one upstream request. A transport failure before any connection
// was obtained is reported as not sent; anything later (including a failure
// while writing the request or waiting for headers) may have reached the
// provider and is not.
func (g *Gateway) do(request *http.Request) (*http.Response, bool, error) {
	var connected atomic.Bool
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := g.client.Do(request)
	notSent := err != nil && !connected.Load()
	if observe := g.options.ObserveDispatch; observe != nil {
		outcome := DispatchOutcome{NotSent: notSent, Err: err}
		if err == nil {
			outcome.Status, outcome.Header = response.StatusCode, response.Header.Clone()
		}
		observe(request.Context(), outcome)
	}
	return response, notSent, err
}

// transportResult is the execution result of a failed upstream round trip.
func transportResult(err error, notSent bool) execution.Result {
	return execution.Result{Err: err, NotSent: notSent}
}

// CachedResponse is a stored provider answer served without upstream I/O.
type CachedResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// CacheStore keeps one successful, complete, non-streamed provider response
// (before response hooks). It is called only after the response was observed
// and settled and the response hooks allowed it.
type CacheStore func(status int, header http.Header, body []byte)

// ResponseCache is the exact-match response cache seam. Lookup runs after
// capacity admission, the request hooks and the quota lease, with the body
// that would be dispatched. It returns a hit to serve, or a store function
// for a miss that may be kept (nil when the exchange is not cacheable).
type ResponseCache interface {
	Lookup(ctx context.Context, x HookExchange, body []byte) (*CachedResponse, CacheStore)
}

// cacheLookup consults the cache, if any.
func (g *Gateway) cacheLookup(r *http.Request, op Operation, metadata Metadata, body []byte) (*CachedResponse, CacheStore) {
	if g.options.Cache == nil {
		return nil, nil
	}
	return g.options.Cache.Lookup(r.Context(), HookExchange{Surface: g.Surface(), Operation: op, Metadata: metadata}, body)
}

// serveCached answers from the cache. The quota lease (already admitted) is
// claimed and settled as one request with zero provider consumption, then the
// response hooks run on the stored answer as on any other response.
func (g *Gateway) serveCached(w http.ResponseWriter, r *http.Request, op Operation, metadata Metadata, run *executionRun, inspect bool, hit *CachedResponse) {
	if err := run.beforeDispatch(r.Context()); err != nil {
		writeExecutionError(w, err)
		return
	}
	if err := run.finish(execution.Result{StatusCode: hit.Status, ResponseComplete: true, Cached: true}); err != nil {
		writeExecutionError(w, err)
		return
	}
	data := hit.Body
	if inspect && g.options.Hooks != nil {
		mediaType, _, _ := strings.Cut(hit.Header.Get("Content-Type"), ";")
		mediaType = strings.TrimSpace(mediaType)
		out, err := g.options.Hooks.Response(r.Context(), HookExchange{Surface: g.Surface(), Operation: op, Metadata: metadata}, mediaType, data)
		if err != nil {
			writeHookRefusal(w, err)
			return
		}
		data = out
	}
	for _, key := range []string{"Content-Type"} {
		if v := hit.Header.Values(key); len(v) > 0 {
			w.Header()[key] = append([]string(nil), v...)
		}
	}
	w.Header().Del("Content-Length")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(hit.Status)
	_, _ = w.Write(data)
}
