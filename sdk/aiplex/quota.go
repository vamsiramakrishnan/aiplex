package aiplex

import (
	"context"
	"net/http"
	"net/url"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// GetQuotaAttempt reads the authenticated caller's own attempt, or an attempt
// visible through the tenant administrator role. It grants no dispatch rights.
func (c *Client) GetQuotaAttempt(ctx context.Context, id string) (*quotaplex.AttemptInfo, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	var out quotaplex.AttemptInfo
	err := c.do(ctx, http.MethodGet, "/api/v1/quota/attempts/"+url.PathEscape(id), nil, &out)
	return &out, err
}
