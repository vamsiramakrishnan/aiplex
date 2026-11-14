package execution

import (
	"context"
	"net/http/httptrace"
	"sync/atomic"
)

// The never-started rule shared by every transport (model HTTP exchanges,
// WebSocket session dials and managed tool calls):
//
//   - A dispatch that failed before a connection to the upstream was obtained
//     wrote no request byte: Result.NotSent (settles at zero).
//   - A complete response whose status RejectedBeforeProcessing reports is a
//     documented refusal before any work: Result.Rejected (settles the request
//     and nothing else).
//   - Anything else (a connection was obtained and the exchange failed, or
//     another status) is uncertain and keeps its hold.

// ConnectionTrace returns ctx carrying a client trace that records whether a
// connection to the upstream was obtained, and a function that reports it.
// net/http and gorilla/websocket both report GotConn through the context's
// trace. One trace may span several tries (retries): the request counts as
// possibly sent once any try obtained a connection.
func ConnectionTrace(ctx context.Context) (context.Context, func() bool) {
	var connected atomic.Bool
	traced := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }})
	return traced, connected.Load
}

// RejectedBeforeProcessing reports the HTTP statuses every supported upstream
// documents as a refusal before processing: request validation (400),
// authentication (401), permission (403), not found (404) and rate limiting
// (429). Transports may add provider-documented statuses (modelapi
// rejections.go); 5xx, 408 and anything else stay uncertain.
func RejectedBeforeProcessing(status int) bool {
	switch status {
	case 400, 401, 403, 404, 429:
		return true
	}
	return false
}
