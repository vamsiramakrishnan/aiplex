package aiplex

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
)

// ModuleDescriptor is the versioned, language-neutral discovery contract.
type ModuleDescriptor = modules.Descriptor

// ListModules returns the server's enabled modules, including extensions this
// version of the SDK does not know. Discovery failure never implies enabled.
func (c *Client) ListModules(ctx context.Context) ([]ModuleDescriptor, error) {
	var out []ModuleDescriptor
	if err := c.do(ctx, http.MethodGet, "/api/v1/modules", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetModule finds an enabled module using discovery, without constructing a URL
// from an untrusted module name.
func (c *Client) GetModule(ctx context.Context, name string) (*ModuleDescriptor, error) {
	if name == "" {
		return nil, fmt.Errorf("aiplex: module name is required")
	}
	all, err := c.ListModules(ctx)
	if err != nil {
		return nil, err
	}
	for _, descriptor := range all {
		if descriptor.Name == name {
			return &descriptor, nil
		}
	}
	return nil, &Error{Code: "MODULE_NOT_ENABLED", Message: "module is not enabled: " + name, StatusCode: http.StatusNotFound}
}
