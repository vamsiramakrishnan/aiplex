package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// ContentHooks is the request-hook seam of the native gateway. It is called
// at exactly two points of one exchange:
//
//   - Request, once, after authorization and capacity admission and before
//     any quota lease, credential acquisition or upstream I/O. It returns the
//     body to dispatch (the caller's bytes unchanged unless a hook
//     transformed them) and whether the response must be inspected.
//   - Response, once, on the complete successful response (a buffered
//     stream included) before any byte of it reaches the caller.
//
// A *HookRefusal error refuses the exchange with its status and code. Any
// other error refuses it with 503 (fail closed).
type ContentHooks interface {
	Request(ctx context.Context, x HookExchange, body []byte) (out []byte, inspectResponse bool, err error)
	Response(ctx context.Context, x HookExchange, mediaType string, body []byte) ([]byte, error)
}

// HookExchange describes the exchange to the hooks without content.
type HookExchange struct {
	Surface   Surface
	Operation Operation
	Metadata  Metadata
}

// HookRefusal is a hook decision to refuse the exchange. Message is safe to
// show the caller; it never contains request or response content.
type HookRefusal struct {
	Status  int
	Code    string
	Message string
}

func (e *HookRefusal) Error() string { return e.Code + ": " + e.Message }

// writeHookRefusal answers in AIPlex's JSON error shape ({"code","message"}),
// which an access point re-renders in the caller's own protocol.
func writeHookRefusal(w http.ResponseWriter, err error) {
	refusal := &HookRefusal{Status: http.StatusServiceUnavailable, Code: "HOOK_UNAVAILABLE", Message: "a content policy check is unavailable; the request was refused"}
	var typed *HookRefusal
	if errors.As(err, &typed) && typed.Status >= 400 && typed.Status <= 599 {
		refusal = typed
	}
	body, _ := json.Marshal(map[string]string{"code": refusal.Code, "message": refusal.Message})
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(refusal.Status)
	_, _ = w.Write(body)
}

// writeDispatchError writes a refusal from prepareDispatch.
func writeDispatchError(w http.ResponseWriter, err error) {
	var refusal *HookRefusal
	if errors.As(err, &refusal) {
		writeHookRefusal(w, err)
		return
	}
	writeExecutionError(w, err)
}

// prepareDispatch is the request side of an exchange: capacity admission,
// then the request hooks, then the quota lease (on the body that will be
// sent). A hook refusal therefore never reserves quota.
func (g *Gateway) prepareDispatch(r *http.Request, trusted contracts.RequestContext, op Operation, metadata Metadata, body []byte) ([]byte, bool, execution.Lease, error) {
	if err := g.admit(r, trusted, op, metadata); err != nil {
		return body, false, nil, err
	}
	inspect := false
	if g.options.Hooks != nil {
		out, wantResponse, err := g.options.Hooks.Request(r.Context(), HookExchange{Surface: g.Surface(), Operation: op, Metadata: metadata}, body)
		if err != nil {
			return body, false, nil, err
		}
		body, inspect = out, wantResponse
	}
	lease, err := g.lease(r, trusted, op, metadata, body)
	return body, inspect, lease, err
}

// sessionHooks refuses a WebSocket session when hooks apply to it: client
// messages are not inspected one by one, so a session on a hooked surface
// is refused at connect, before the upgrade.
func (g *Gateway) sessionHooks(w http.ResponseWriter, r *http.Request, op Operation, metadata Metadata) bool {
	if g.options.Hooks == nil {
		return true
	}
	if _, _, err := g.options.Hooks.Request(r.Context(), HookExchange{Surface: g.Surface(), Operation: op, Metadata: metadata}, nil); err != nil {
		writeHookRefusal(w, err)
		return false
	}
	return true
}

// bufferWriter collects a response that must be inspected before delivery.
type bufferWriter struct {
	header   http.Header
	buf      bytes.Buffer
	limit    int64
	overflow bool
}

func (b *bufferWriter) Header() http.Header { return b.header }
func (b *bufferWriter) WriteHeader(int)     {}
func (b *bufferWriter) Flush()              {}
func (b *bufferWriter) Write(p []byte) (int, error) {
	if int64(b.buf.Len()+len(p)) > b.limit {
		b.overflow = true
		return 0, errors.New("response exceeds the inspection bound")
	}
	return b.buf.Write(p)
}

// serveInspected is the response side of an exchange whose response hooks
// apply: the successful response is read completely (a stream through the
// same observers as an unbuffered one, so metering is unchanged), handed
// to the response hooks, and only then written.
func (g *Gateway) serveInspected(w http.ResponseWriter, r *http.Request, response *http.Response, owner Owner, op Operation, metadata Metadata, run *executionRun, store CacheStore, copyHeaders func(http.Header)) {
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	jsonResponse := mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
	var data []byte
	switch {
	case mediaType == "text/event-stream" || (mediaType == "application/vnd.amazon.eventstream" && op.Transport == EventStream):
		buffer := &bufferWriter{header: http.Header{}, limit: g.options.MaxBodyBytes}
		switch {
		case mediaType != "text/event-stream":
			g.copyEventStream(buffer, r, response.Body, owner, op, run, response.StatusCode)
		case run != nil:
			g.copyControlledSSE(buffer, r, response.Body, owner, op, run, response.StatusCode)
		default:
			g.copySSE(buffer, r, response.Body, owner, op)
		}
		if buffer.overflow {
			http.Error(w, "upstream response exceeds the inspection bound", http.StatusBadGateway)
			return
		}
		data = buffer.buf.Bytes()
	default:
		read, err := io.ReadAll(io.LimitReader(response.Body, g.options.MaxBodyBytes+1))
		if err != nil || int64(len(read)) > g.options.MaxBodyBytes {
			if err == nil {
				err = errors.New("response observation bound exceeded")
			}
			_ = run.finish(execution.Result{StatusCode: response.StatusCode, Err: err})
			http.Error(w, "upstream response exceeds the inspection bound", http.StatusBadGateway)
			return
		}
		data = read
		if run != nil {
			terminal := false
			if op.Action == Create || jsonResponse {
				if terminal, err = g.observeControlledJSON(owner, op, data, run); err != nil {
					_ = run.finish(execution.Result{StatusCode: response.StatusCode, ResponseComplete: true, Err: err})
					http.Error(w, "upstream response could not be acknowledged", http.StatusBadGateway)
					return
				}
			}
			if err = run.finish(execution.Result{StatusCode: response.StatusCode, ResponseComplete: true, Terminal: terminal}); err != nil {
				writeExecutionError(w, err)
				return
			}
		} else if op.Action == Create || jsonResponse {
			if err = g.observeJSON(r.Context(), owner, op, data, true); err != nil {
				http.Error(w, "resource ownership could not be persisted", http.StatusBadGateway)
				return
			}
		}
	}
	out, err := g.options.Hooks.Response(r.Context(), HookExchange{Surface: g.Surface(), Operation: op, Metadata: metadata}, mediaType, data)
	if err != nil {
		writeHookRefusal(w, err)
		return
	}
	if store != nil && jsonResponse {
		// The provider's answer is kept as it came (the response hooks run
		// again on every hit); a denied answer is never kept.
		store(response.StatusCode, response.Header, data)
	}
	copyHeaders(w.Header())
	w.Header().Del("Content-Length")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(out)
}
