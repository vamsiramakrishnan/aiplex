package aiplex

import (
	"context"
	"net/http"
	"net/url"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
)

// RegisterToolConnection registers the connection's OAuth client with its
// upstream authorization server (POST /api/v1/mcp/connections/{id}/registration,
// operators only). The request has no body: the client profile and issuer come
// from the saved connection. The returned envelope is the stored
// "oauth-registration" resource, whose spec is the public registration (client
// identifier and a credential reference, never a secret).
func (c *Client) RegisterToolConnection(ctx context.Context, id string) (*resources.Record, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	var out resources.Record
	if err := c.do(ctx, http.MethodPost, "/api/v1/mcp/connections/"+url.PathEscape(id)+"/registration", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func toolAuthorizationPath(id string) (string, error) {
	if err := validateResourceID(id); err != nil {
		return "", err
	}
	return "/api/v1/mcp/connections/" + url.PathEscape(id) + "/authorization", nil
}
func (c *Client) StartToolAuthorization(ctx context.Context, id, redirect string) (*toolauth.AuthorizationChallenge, error) {
	path, e := toolAuthorizationPath(id)
	if e != nil {
		return nil, e
	}
	var out toolauth.AuthorizationChallenge
	e = c.do(ctx, http.MethodPost, path, struct {
		RedirectURI string `json:"redirect_uri"`
	}{redirect}, &out)
	return &out, e
}
func (c *Client) CompleteToolAuthorization(ctx context.Context, id string, response toolauth.AuthorizationResponse) (*toolauth.SessionInfo, error) {
	path, e := toolAuthorizationPath(id)
	if e != nil {
		return nil, e
	}
	var out toolauth.SessionInfo
	e = c.do(ctx, http.MethodPost, path+"/complete", response, &out)
	return &out, e
}
func (c *Client) GetToolAuthorization(ctx context.Context, id string) (*toolauth.SessionInfo, error) {
	path, e := toolAuthorizationPath(id)
	if e != nil {
		return nil, e
	}
	var out toolauth.SessionInfo
	e = c.do(ctx, http.MethodGet, path, nil, &out)
	return &out, e
}
func (c *Client) RevokeToolAuthorization(ctx context.Context, id string) error {
	path, e := toolAuthorizationPath(id)
	if e != nil {
		return e
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
