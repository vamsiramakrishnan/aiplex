// Package nativeapi contains the public contracts and bounded HTTP transport
// shared by native governance provider adapters. It imports no Aiplex internals.
package nativeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const MaxBodyBytes = 1 << 20

type Authenticator interface {
	Authenticate(context.Context, *http.Request) error
}

type AuthFunc func(context.Context, *http.Request) error

func (f AuthFunc) Authenticate(ctx context.Context, req *http.Request) error {
	if f == nil {
		return errors.New("native API authentication is unavailable")
	}
	return f(ctx, req)
}

type Operation struct {
	Name     string          `json:"name"`
	Resource string          `json:"resource"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	ETag     string          `json:"etag,omitempty"`
}

type OperationInfo struct {
	Name        string `json:"name"`
	Mutating    bool   `json:"mutating"`
	Credential  bool   `json:"credential"`
	Description string `json:"description"`
}

type Result struct {
	Body       json.RawMessage `json:"body"`
	ETag       string          `json:"etag,omitempty"`
	StatusCode int             `json:"status_code"`
}

type Driver interface {
	Operations() []OperationInfo
	Execute(context.Context, Operation) (Result, error)
}

// StatusError exposes an upstream HTTP status without response bodies or URLs.
// Callers can classify a definite refusal separately from transport uncertainty.
type StatusError struct{ StatusCode int }

func (e *StatusError) Error() string { return fmt.Sprintf("native API returned HTTP %d", e.StatusCode) }

// Client pins requests to a configured origin. The supplied HTTP client's
// transport owns any deployment-specific DNS/network endpoint policy.
// Redirects and automatic application-level retries are never performed.
type Client struct {
	base *url.URL
	http *http.Client
	auth Authenticator
}

func NewClient(baseURL string, client *http.Client, auth Authenticator) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base == nil || base.Host == "" || base.Opaque != "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid native API endpoint")
	}
	if base.Scheme != "https" && !(base.Scheme == "http" && loopback(base.Hostname())) {
		return nil, errors.New("native API endpoint requires HTTPS")
	}
	if auth == nil {
		return nil, errors.New("native API authenticator is required")
	}
	if err := validateRelativePath(strings.TrimPrefix(base.EscapedPath(), "/")); err != nil {
		return nil, errors.New("invalid native API endpoint path")
	}
	if client == nil {
		return nil, errors.New("native API requires a configured HTTP client")
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 30 * time.Second
	}
	return &Client{base: base, http: &copyClient, auth: auth}, nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateRelativePath(path string) error {
	if strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\\\x00\r\n") {
		return errors.New("native API path must be relative")
	}
	value := path
	for i := 0; i < 8; i++ {
		parsed, err := url.Parse(value)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#\\\x00\r\n") {
			return errors.New("invalid native API path")
		}
		for _, segment := range strings.Split(value, "/") {
			if segment == "." || segment == ".." {
				return errors.New("native API path traversal is forbidden")
			}
		}
		decoded, err := url.PathUnescape(value)
		if err != nil {
			return errors.New("invalid native API path encoding")
		}
		if decoded == value {
			return nil
		}
		// Encoded separators can change resource boundaries after proxy decoding.
		if strings.Count(decoded, "/") != strings.Count(value, "/") || strings.Contains(decoded, "\\") {
			return errors.New("encoded native API path separators are forbidden")
		}
		value = decoded
	}
	return errors.New("excessive native API path encoding")
}

// Do makes one request. Paths are relative to the configured endpoint path;
// callers pass query parameters separately and cannot supply arbitrary headers.
// Error strings deliberately exclude endpoint URLs, payloads and response data.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, payload any) (Result, error) {
	if c == nil || c.base == nil || c.http == nil || c.auth == nil {
		return Result{}, errors.New("native API client is not configured")
	}
	if err := validateRelativePath(path); err != nil {
		return Result{}, err
	}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return Result{}, errors.New("native API payload is not JSON encodable")
		}
		if len(body) > MaxBodyBytes {
			return Result{}, errors.New("native API payload exceeds limit")
		}
	}
	target := *c.base
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		return Result{}, errors.New("invalid native API path encoding")
	}
	target.Path = strings.TrimRight(c.base.Path, "/") + "/" + decodedPath
	target.RawPath = ""
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return Result{}, errors.New("invalid native API request")
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	pinnedURL, pinnedHost, pinnedMethod := *req.URL, req.Host, req.Method
	if err := c.auth.Authenticate(ctx, req); err != nil {
		return Result{}, errors.New("native API authentication failed")
	}
	// Auth adapters may add credentials but cannot redirect the request.
	if req.URL == nil || *req.URL != pinnedURL || req.Host != pinnedHost || req.Method != pinnedMethod || req.RequestURI != "" {
		return Result{}, errors.New("native API authenticator changed the request target")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, errors.New("native API request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{StatusCode: resp.StatusCode}, &StatusError{StatusCode: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return Result{}, errors.New("native API response could not be read")
	}
	if len(data) > MaxBodyBytes {
		return Result{}, errors.New("native API response exceeds limit")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("null")
	}
	if !json.Valid(data) {
		return Result{}, errors.New("native API response is not valid JSON")
	}
	return Result{Body: json.RawMessage(data), ETag: resp.Header.Get("ETag"), StatusCode: resp.StatusCode}, nil
}
