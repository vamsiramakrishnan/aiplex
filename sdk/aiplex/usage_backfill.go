package aiplex

import (
	"context"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// UsageBackfillResult reports one back-estimation call (see BackfillUsageCosts).
type UsageBackfillResult = usageledger.BackfillResult

// UsageBackfillRequest selects records whose unknown cost may now be computed.
// All fields are optional.
type UsageBackfillRequest struct {
	Since    *time.Time `json:"since,omitempty"`
	Until    *time.Time `json:"until,omitempty"`
	ModelID  string     `json:"model_id,omitempty"`
	Provider string     `json:"provider,omitempty"`
	DryRun   bool       `json:"dry_run,omitempty"`
	// Limit bounds the records one call scans (server default 500, at most
	// 1000). Call again with the result's NextCursor until it is empty.
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// BackfillUsageCosts prices records written before a price existed: records
// whose cost is unknown and whose usage is final get the cost of the price
// effective at their own start time, marked estimated with a backfilled_at and
// the price id. Settled and estimated costs are never changed and a second run
// changes nothing. It needs the admin role.
func (c *Client) BackfillUsageCosts(ctx context.Context, req UsageBackfillRequest) (*UsageBackfillResult, error) {
	var out UsageBackfillResult
	if err := c.do(ctx, http.MethodPost, "/api/v1/usage/backfill", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
