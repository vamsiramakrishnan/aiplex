package modelapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

// LiveDialer is the functional WebSocket transport. Hosts should use
// NewLiveDialer with their endpoint-policy HTTP client, preserving dial-time DNS
// validation and TLS verification rather than constructing an ambient dialer.
type LiveDialer interface {
	DialContext(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}

type SafeNetworkDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

func NewLiveDialer(policy SafeNetworkDialer, tlsConfig *tls.Config) (*websocket.Dialer, error) {
	if policy == nil {
		return nil, fmt.Errorf("endpoint-policy dialer required")
	}
	if tlsConfig != nil && tlsConfig.InsecureSkipVerify {
		return nil, fmt.Errorf("TLS verification cannot be disabled")
	}
	var safeTLS *tls.Config
	if tlsConfig != nil {
		safeTLS = tlsConfig.Clone()
	}
	return &websocket.Dialer{NetDialContext: policy.DialContext, TLSClientConfig: safeTLS, HandshakeTimeout: 10 * time.Second, ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10}, nil
}

func (g *Gateway) serveLive(w http.ResponseWriter, r *http.Request, trusted contracts.RequestContext, owner Owner, op Operation) {
	// Client authentication has already succeeded. Read only the bounded initial
	// setup before acquiring server credentials or opening the upstream socket.
	connection, e := (&websocket.Upgrader{HandshakeTimeout: 10 * time.Second}).Upgrade(w, r, nil)
	if e != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(g.options.MaxBodyBytes)
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	kind, setup, e := connection.ReadMessage()
	if e != nil || kind != websocket.TextMessage {
		return
	}
	metadata, e := g.validateLiveSetup(r.Context(), owner, setup)
	if e != nil {
		_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "setup denied"), time.Now().Add(time.Second))
		return
	}
	if e = g.options.Authorize(r.Context(), trusted, g.Surface(), op, metadata); e != nil {
		return
	}
	token, e := g.options.TokenSource.Token(r.Context())
	if e != nil || token == "" {
		return
	}
	endpoint := strings.TrimSuffix(g.surface.Origin, "/") + r.URL.EscapedPath()
	if strings.HasPrefix(endpoint, "https://") {
		endpoint = "wss://" + strings.TrimPrefix(endpoint, "https://")
	} else {
		endpoint = "ws://" + strings.TrimPrefix(endpoint, "http://")
	}
	headers := forwardRequestHeaders(r.Header)
	applyTrustedHeaders(headers, g.options.UpstreamHeaders)
	headers.Set("Authorization", "Bearer "+token)
	upstream, response, e := g.options.LiveDialer.DialContext(r.Context(), endpoint, headers)
	if e != nil && response != nil && response.Body != nil {
		response.Body.Close()
	}
	if e != nil {
		return
	}
	defer upstream.Close()
	upstream.SetReadLimit(g.options.MaxBodyBytes)
	if e = upstream.WriteMessage(kind, setup); e != nil {
		return
	}
	_ = connection.SetReadDeadline(time.Time{})
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-r.Context().Done():
			connection.Close()
			upstream.Close()
		case <-done:
		}
	}()
	failures := make(chan error, 2)
	go func() {
		for {
			kind, payload, e := connection.ReadMessage()
			if e != nil {
				failures <- e
				return
			}
			if kind == websocket.TextMessage {
				var doc map[string]json.RawMessage
				if json.Unmarshal(payload, &doc) == nil && doc["setup"] != nil {
					failures <- fmt.Errorf("repeated setup denied")
					return
				}
			}
			if e = upstream.WriteMessage(kind, payload); e != nil {
				failures <- e
				return
			}
		}
	}()
	go func() {
		for {
			kind, payload, e := upstream.ReadMessage()
			if e != nil {
				failures <- e
				return
			}
			if kind == websocket.TextMessage {
				if e = g.observeLive(r.Context(), owner, op, payload); e != nil {
					failures <- e
					return
				}
			}
			if e = connection.WriteMessage(kind, payload); e != nil {
				failures <- e
				return
			}
		}
	}()
	<-failures
}
func (g *Gateway) validateLiveSetup(ctx context.Context, owner Owner, payload []byte) (Metadata, error) {
	if e := validateJSON(payload); e != nil {
		return Metadata{}, e
	}
	var doc struct {
		Setup struct {
			Model             string `json:"model"`
			SessionResumption struct {
				Handle string `json:"handle"`
			} `json:"sessionResumption"`
		} `json:"setup"`
	}
	if e := json.Unmarshal(payload, &doc); e != nil || doc.Setup.Model == "" || !g.allowedModel(doc.Setup.Model) {
		return Metadata{}, fmt.Errorf("Live setup model denied")
	}
	if handle := doc.Setup.SessionResumption.Handle; handle != "" {
		if len(handle) > 8192 || g.options.Ownership == nil {
			return Metadata{}, fmt.Errorf("session ownership required")
		}
		if e := g.options.Ownership.Check(ctx, owner, Resource{"live-session", handle}); e != nil {
			return Metadata{}, e
		}
	}
	return Metadata{Model: doc.Setup.Model, Transport: WebSocket}, nil
}
func (g *Gateway) observeLive(ctx context.Context, owner Owner, op Operation, payload []byte) error {
	var doc struct {
		SessionResumptionUpdate struct {
			NewHandle string `json:"newHandle"`
		} `json:"sessionResumptionUpdate"`
	}
	if e := json.Unmarshal(payload, &doc); e != nil {
		return e
	}
	if handle := doc.SessionResumptionUpdate.NewHandle; handle != "" {
		if len(handle) > 8192 || g.options.Ownership == nil {
			return fmt.Errorf("session ownership required")
		}
		if e := g.options.Ownership.Bind(ctx, owner, Resource{"live-session", handle}); e != nil {
			return e
		}
	}
	return g.observeJSON(ctx, owner, op, payload, false)
}
