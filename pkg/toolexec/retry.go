package toolexec

import (
	"context"
	"net/http"
	"time"
)

// TransportRetry retries an upstream tool request whose transport failed
// (no HTTP response was read: a refused or reset connection, a timeout
// waiting for headers). An HTTP status, a JSON-RPC error or an invalid
// response is never retried. It applies to tools/list, which only reads,
// and to a tools/call only when Call says the tool may be repeated.
type TransportRetry struct {
	// Attempts is the total number of tries (at most 3). 0 or 1 is off.
	Attempts int
	// Backoff is the wait before each retry.
	Backoff time.Duration
	// Call reports whether a tools/call may be sent again after a transport
	// failure. The host answers true only for tools whose gateway-observed
	// MCP annotation readOnlyHint is true; nil never retries a call.
	Call func(context.Context, Request) bool
}

func (t *TransportRetry) attempts() int {
	if t == nil || t.Attempts < 2 {
		return 1
	}
	if t.Attempts > 3 {
		return 3
	}
	return t.Attempts
}

// send performs the upstream request, retrying transport failures when
// allowed. Each try builds a fresh request (and acquires the credential).
func (g *Gateway) send(ctx context.Context, body []byte, retry bool) (Credential, *http.Response, error) {
	tries := 1
	if retry {
		tries = g.options.Retry.attempts()
	}
	var lastErr error
	for i := 0; i < tries; i++ {
		if i > 0 {
			timer := time.NewTimer(g.options.Retry.Backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, nil, lastErr
			case <-timer.C:
			}
		}
		credential, upstream, err := g.upstreamRequest(ctx, body)
		if err != nil {
			return nil, nil, errCredential{err}
		}
		response, err := g.client.Do(upstream)
		if err == nil {
			return credential, response, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, nil, lastErr
}

// errCredential marks a credential failure (never retried).
type errCredential struct{ error }
