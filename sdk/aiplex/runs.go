package aiplex

import (
	"context"
	"net/url"
	"time"
)

// RunsHealth is the answer of GET /api/v1/runs/_health. LastIngestAt is the
// zero time when the API has recorded no ingest (or could not read it); that
// is "none recorded", not a real timestamp.
type RunsHealth struct {
	LastIngestAt       time.Time `json:"last_ingest_at"`
	HasRuns            bool      `json:"has_runs"`
	TapeInstancesCount int       `json:"tape_instances_count"`
	Now                time.Time `json:"now"`
}

// GetRunsHealth reports whether the durable-run pipeline has delivered events.
func (c *Client) GetRunsHealth(ctx context.Context) (*RunsHealth, error) {
	var h RunsHealth
	if err := c.do(ctx, "GET", "/api/v1/runs/_health", nil, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// ListRunEffects returns the run's effect.* events (begin, confirmed, failed,
// unknown, duplicate), in order. An effect.unknown event means the outcome is
// not known; callers must not read it as success, failure or zero. The route
// has no paging: the server reads at most the first 10000 events of the run.
func (c *Client) ListRunEffects(ctx context.Context, runID string) ([]ExecutionEvent, error) {
	return c.listRunKind(ctx, runID, "effects")
}

// ListRunObligations returns the run's obligation.created events, in order,
// with the same 10000-event read limit as ListRunEffects.
func (c *Client) ListRunObligations(ctx context.Context, runID string) ([]ExecutionEvent, error) {
	return c.listRunKind(ctx, runID, "obligations")
}

// ListRunBudgets returns the run's budget.charged events, in order, with the
// same 10000-event read limit as ListRunEffects. The amount is in
// PayloadJSON ("usd_charged"); a payload without it is an unknown amount.
func (c *Client) ListRunBudgets(ctx context.Context, runID string) ([]ExecutionEvent, error) {
	return c.listRunKind(ctx, runID, "budgets")
}

func (c *Client) listRunKind(ctx context.Context, runID, kind string) ([]ExecutionEvent, error) {
	var resp map[string][]ExecutionEvent
	if err := c.do(ctx, "GET", "/api/v1/runs/"+url.PathEscape(runID)+"/"+kind, nil, &resp); err != nil {
		return nil, err
	}
	return resp[kind], nil
}
