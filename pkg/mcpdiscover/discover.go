// Package mcpdiscover lists the tools of a remote MCP server over the
// Streamable HTTP transport, using the official MCP Go SDK.
//
// It is an outbound client used only for discovery (the CLI's --probe and
// `mcp` registration, and the deploy engine's post-deploy scope discovery).
// The gateway forwards MCP traffic byte for byte and does not use this
// package.
//
// The caller supplies the *http.Client, so redirect policy, timeouts and
// transport settings stay the caller's decision. This package adds only what
// the SDK has no option for: per-request headers (sent to the endpoint's own
// origin only, never to a redirect target) and a cap on response bytes.
package mcpdiscover

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// DefaultMaxResponseBytes caps the bytes read from any one HTTP response.
	DefaultMaxResponseBytes = 4 << 20
	// MaxPages bounds tools/list pagination.
	MaxPages = 20
)

// ErrCredentialRequired means the server answered HTTP 401 or 403.
var ErrCredentialRequired = errors.New("the server requires a credential")

// ErrTooLarge means a response exceeded the byte cap.
var ErrTooLarge = errors.New("response too large")

// Tool is a discovered tool definition.
type Tool struct {
	Name        string
	Description string
	InputSchema any
	Annotations *mcp.ToolAnnotations
}

// Options tunes ListTools. The zero value is valid.
type Options struct {
	// Headers are sent on every request to the endpoint's origin (scheme and
	// host). They are never sent to a different origin after a redirect, and
	// their values never appear in returned errors.
	Headers http.Header
	// ProtocolVersion is offered in initialize. Empty means the SDK default.
	// The server may negotiate another version.
	ProtocolVersion string
	// MaxResponseBytes caps each HTTP response body. Zero means
	// DefaultMaxResponseBytes.
	MaxResponseBytes int64
}

// ListTools connects to endpoint (initialize handshake, session handling,
// JSON or SSE responses, all by the SDK), lists every page of tools and
// closes the session. Pass an httpClient with the timeout and redirect policy
// you need; nil means a client with no timeout and the default redirect policy.
func ListTools(ctx context.Context, httpClient *http.Client, endpoint string, opts Options) ([]Tool, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("endpoint is empty")
	}
	limit := opts.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	hc := &http.Client{}
	if httpClient != nil {
		c := *httpClient
		hc = &c
	}
	rt := &guardTransport{
		next:    hc.Transport,
		origin:  originOf(endpoint),
		headers: opts.Headers.Clone(),
		limit:   limit,
	}
	if rt.next == nil {
		rt.next = http.DefaultTransport
	}
	hc.Transport = rt

	client := mcp.NewClient(&mcp.Implementation{Name: "aiplex-discovery", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           hc,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
		MaxEventSize:         int(limit),
	}, &mcp.ClientSessionOptions{ProtocolVersion: opts.ProtocolVersion})
	if err != nil {
		return nil, rt.explain("initialize", err)
	}
	defer session.Close()

	var tools []Tool
	cursor := ""
	for page := 0; page < MaxPages; page++ {
		res, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, rt.explain("tools/list", err)
		}
		for _, t := range res.Tools {
			tools = append(tools, Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, Annotations: t.Annotations})
		}
		if res.NextCursor == "" {
			return tools, nil
		}
		cursor = res.NextCursor
	}
	return nil, fmt.Errorf("tools/list: more than %d pages", MaxPages)
}

// Names returns the tool names, in order.
func Names(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func originOf(endpoint string) string {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	return req.URL.Scheme + "://" + req.URL.Host
}

// guardTransport adds headers for the endpoint origin, records credential
// refusals and other HTTP failures, and caps response bodies.
type guardTransport struct {
	next    http.RoundTripper
	origin  string
	headers http.Header
	limit   int64
	denied  atomic.Int32 // last 401/403 status
	status  atomic.Int32 // last other status >= 300
	large   atomic.Bool
}

func (g *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(g.headers) > 0 && g.origin != "" && req.URL.Scheme+"://"+req.URL.Host == g.origin {
		req = req.Clone(req.Context())
		for k, vs := range g.headers {
			req.Header.Del(k)
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
	}
	resp, err := g.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		g.denied.Store(int32(resp.StatusCode))
	case resp.StatusCode >= 300:
		g.status.Store(int32(resp.StatusCode))
	}
	resp.Body = &cappedBody{rc: resp.Body, left: g.limit, flag: &g.large}
	return resp, nil
}

// explain maps a failure to a stable error that carries no header values.
// Neither the SDK nor net/http errors include request headers; the mapping
// keeps the wording stable and lets callers test with errors.Is.
func (g *guardTransport) explain(step string, err error) error {
	switch {
	case g.large.Load():
		return fmt.Errorf("%s: %w (over %d bytes)", step, ErrTooLarge, g.limit)
	case g.denied.Load() != 0:
		return fmt.Errorf("%w (HTTP %d)", ErrCredentialRequired, g.denied.Load())
	case g.status.Load() != 0:
		return fmt.Errorf("%s answered HTTP %d", step, g.status.Load())
	}
	return fmt.Errorf("%s: %w", step, err)
}

type cappedBody struct {
	rc   io.ReadCloser
	left int64
	flag *atomic.Bool
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.left <= 0 {
		// The cap is reached: more data is an overflow, EOF is fine.
		var one [1]byte
		n, err := b.rc.Read(one[:])
		if n > 0 {
			b.flag.Store(true)
			return 0, ErrTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }
