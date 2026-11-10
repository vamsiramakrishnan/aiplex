package api

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
)

// tracingLiveDialer adds the exchange's trace context to the upstream
// WebSocket handshake of a live model session.
type tracingLiveDialer struct{ inner modelapi.LiveDialer }

func (d tracingLiveDialer) DialContext(ctx context.Context, url string, header http.Header) (*websocket.Conn, *http.Response, error) {
	header = header.Clone()
	if header == nil {
		header = http.Header{}
	}
	telemetry.Inject(ctx, header)
	return d.inner.DialContext(ctx, url, header)
}
