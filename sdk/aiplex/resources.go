package aiplex

import (
	"context"
	"fmt"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"net/http"
	"net/url"
	"strconv"
)

type ResourceKind string

const (
	ModelSurfaces      ResourceKind = "model-surface"
	AgentConnections   ResourceKind = "agent-connection"
	ToolConnections    ResourceKind = "tool-connection"
	NetworkProfiles    ResourceKind = "network-profile"
	ClientDestinations ResourceKind = "client-destination"
	DeploymentIntents  ResourceKind = "deployment-intent"
)

func resourcePath(kind ResourceKind) (string, error) {
	switch kind {
	case ModelSurfaces:
		return "/api/v1/llm/surfaces", nil
	case AgentConnections:
		return "/api/v1/a2a/connections", nil
	case ToolConnections:
		return "/api/v1/mcp/connections", nil
	case NetworkProfiles:
		return "/api/v1/network/profiles", nil
	case ClientDestinations:
		return "/api/v1/clients/destinations", nil
	case DeploymentIntents:
		return "/api/v1/network/deployment-intents", nil
	}
	return "", fmt.Errorf("unknown module resource kind %q", kind)
}
func (c *Client) ListResources(ctx context.Context, kind ResourceKind) ([]resources.Record, error) {
	path, e := resourcePath(kind)
	if e != nil {
		return nil, e
	}
	var out []resources.Record
	e = c.do(ctx, http.MethodGet, path+"/", nil, &out)
	return out, e
}
func (c *Client) GetResource(ctx context.Context, kind ResourceKind, id string) (*resources.Record, error) {
	path, e := resourcePath(kind)
	if e != nil {
		return nil, e
	}
	var out resources.Record
	e = c.do(ctx, http.MethodGet, path+"/"+url.PathEscape(id), nil, &out)
	return &out, e
}
func (c *Client) PutResource(ctx context.Context, kind ResourceKind, id string, spec any, revision uint64) (*resources.Record, error) {
	path, e := resourcePath(kind)
	if e != nil {
		return nil, e
	}
	var out resources.Record
	headers := map[string]string{}
	headers["If-Match"] = strconv.FormatUint(revision, 10)
	e = c.doWithHeaders(ctx, http.MethodPut, path+"/"+url.PathEscape(id), spec, &out, headers)
	return &out, e
}
func (c *Client) DeleteResource(ctx context.Context, kind ResourceKind, id string, revision uint64) error {
	path, e := resourcePath(kind)
	if e != nil {
		return e
	}
	headers := map[string]string{}
	headers["If-Match"] = strconv.FormatUint(revision, 10)
	return c.doWithHeaders(ctx, http.MethodDelete, path+"/"+url.PathEscape(id), nil, nil, headers)
}
func (c *Client) ResourcePlan(ctx context.Context, kind ResourceKind, id string, idempotency string) (any, error) {
	path, e := resourcePath(kind)
	if e != nil {
		return nil, e
	}
	if kind != NetworkProfiles && kind != DeploymentIntents {
		return nil, fmt.Errorf("resource has no plan operation")
	}
	headers := map[string]string{}
	if idempotency != "" {
		headers["Idempotency-Key"] = idempotency
	}
	var out any
	e = c.doWithHeaders(ctx, http.MethodPost, path+"/"+url.PathEscape(id)+"/plan", nil, &out, headers)
	return out, e
}
