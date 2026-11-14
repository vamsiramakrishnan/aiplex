package aiplex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// InputError is returned when the client refuses a request before sending it
// because the server would refuse it too (a malformed ID, a missing field, a
// value out of range). Nothing was sent.
type InputError struct{ Message string }

func (e *InputError) Error() string { return "aiplex: " + e.Message }

func inputErr(format string, args ...any) error {
	return &InputError{Message: fmt.Sprintf(format, args...)}
}

// a2aID checks an identifier that becomes a path segment. what names it in
// the message ("agent connection", "delegation").
func a2aID(what, id string) error {
	if err := validateResourceID(id); err != nil {
		return inputErr("invalid %s ID", what)
	}
	return nil
}

func delegationPath(id string) (string, error) {
	if err := a2aID("delegation", id); err != nil {
		return "", err
	}
	return "/api/v1/a2a/delegations/" + url.PathEscape(id), nil
}

// ListDelegationsOptions filters ListDelegationsWithOptions. The zero value
// asks for the server's default page of all agents.
type ListDelegationsOptions struct {
	// AgentID restricts the list to delegations where this agent is caller or callee.
	AgentID string
	// Limit is 1..1000; 0 leaves the server's default (50).
	Limit int
}

// ListDelegationsWithOptions lists recent delegations with the server-side
// agent and limit filters. ListDelegations is the unfiltered form.
func (c *Client) ListDelegationsWithOptions(ctx context.Context, opts ListDelegationsOptions) ([]Delegation, error) {
	q := url.Values{}
	if opts.AgentID != "" {
		if strings.TrimSpace(opts.AgentID) != opts.AgentID || len(opts.AgentID) > 512 || strings.ContainsAny(opts.AgentID, "\r\n\x00") {
			return nil, inputErr("invalid agent ID")
		}
		q.Set("agent_id", opts.AgentID)
	}
	if opts.Limit != 0 {
		if opts.Limit < 1 || opts.Limit > 1000 {
			return nil, inputErr("limit must be between 1 and 1000")
		}
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	path := "/api/v1/a2a/delegations"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var list []Delegation
	err := c.do(ctx, http.MethodGet, path, nil, &list)
	return list, err
}

// Validate mirrors the server's PATCH checks (internal/api/a2a.go).
func (r *UpdateDelegationRequest) Validate() error {
	if r == nil {
		return inputErr("an update is required")
	}
	switch r.Status {
	case "pending", "running", "completed", "failed":
	default:
		return inputErr("status must be pending, running, completed or failed")
	}
	if len(r.Error) > 8192 {
		return inputErr("error text is limited to 8192 bytes")
	}
	return nil
}

// InvokeAgentRequest is the body of a native agent invocation. Operation is
// provider-specific (for example "invoke"); the payload is sent as given.
type InvokeAgentRequest struct {
	Operation   string          `json:"operation"`
	SessionID   string          `json:"session_id,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ContentType string          `json:"content_type,omitempty"`
	Accept      string          `json:"accept,omitempty"`
	Qualifier   string          `json:"qualifier,omitempty"`
	Watermark   string          `json:"watermark,omitempty"`
}

// maxInvokeBody is the server's request limit (internal/api/connected.go).
const maxInvokeBody = 1 << 20

// AgentResponse is the provider's reply, relayed by the API as it arrives.
// The caller must close Body. It is not decoded: its format is the provider's
// (often server-sent events), named by Header "Content-Type".
type AgentResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// InvokeAgent invokes the cloud agent behind an agent connection
// (POST /api/v1/a2a/connections/{id}/invoke) and returns the reply without
// buffering it, so a streamed reply can be read as it arrives. The request is
// bounded by ctx, not by the client's HTTP timeout, because an agent may take
// minutes. A non-2xx answer is returned as *Error and no response.
func (c *Client) InvokeAgent(ctx context.Context, connectionID string, req InvokeAgentRequest) (*AgentResponse, error) {
	if err := a2aID("agent connection", connectionID); err != nil {
		return nil, err
	}
	if req.Operation == "" || strings.TrimSpace(req.Operation) != req.Operation {
		return nil, inputErr("an operation is required, with no leading or trailing space")
	}
	if len(req.Payload) > 0 && !json.Valid(req.Payload) {
		return nil, inputErr("the payload must be valid JSON")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, inputErr("the invocation cannot be encoded: %v", err)
	}
	if len(body) > maxInvokeBody {
		return nil, inputErr("the invocation is larger than the 1 MiB the API accepts")
	}
	path := "/api/v1/a2a/connections/" + url.PathEscape(connectionID) + "/invoke"
	if err = validateAPIPath(path); err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("aiplex: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}
	hc := http.Client{}
	if c.HTTPClient != nil {
		hc = *c.HTTPClient
	}
	hc.Timeout = 0
	resp, err := hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("aiplex: request failed: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		var apiErr Error
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiErr) != nil {
			return nil, &Error{Code: "unknown", Message: resp.Status, StatusCode: resp.StatusCode}
		}
		apiErr.StatusCode = resp.StatusCode
		return nil, &apiErr
	}
	return &AgentResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body}, nil
}
