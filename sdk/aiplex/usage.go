package aiplex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type UsageSummaryOpts struct{ ModelID, AgentID, Period string }
type ListUsageRecordsOpts struct {
	ModelID, AgentID string
	Since            time.Time
	Limit            int
}

func (c *Client) GetUsageSummaryWithOpts(ctx context.Context, opts *UsageSummaryOpts) (*UsageSummary, error) {
	query := url.Values{}
	period := "day"
	if opts != nil {
		if opts.Period != "" {
			period = opts.Period
		}
		if opts.ModelID != "" {
			query.Set("model_id", opts.ModelID)
		}
		if opts.AgentID != "" {
			query.Set("agent_id", opts.AgentID)
		}
	}
	if period != "day" && period != "week" && period != "month" {
		return nil, fmt.Errorf("usage period must be day, week, or month")
	}
	query.Set("period", period)
	var result UsageSummary
	err := c.do(ctx, http.MethodGet, "/api/v1/llm/usage/summary?"+query.Encode(), nil, &result)
	return &result, err
}
func (c *Client) ListUsageRecords(ctx context.Context, opts *ListUsageRecordsOpts) ([]UsageRecord, error) {
	query := url.Values{}
	if opts != nil {
		if opts.Limit < 0 || opts.Limit > 1000 {
			return nil, fmt.Errorf("usage record limit must be between1 and1000")
		}
		if opts.ModelID != "" {
			query.Set("model_id", opts.ModelID)
		}
		if opts.AgentID != "" {
			query.Set("agent_id", opts.AgentID)
		}
		if opts.Limit > 0 {
			query.Set("limit", strconv.Itoa(opts.Limit))
		}
		if !opts.Since.IsZero() {
			query.Set("since", opts.Since.Format(time.RFC3339Nano))
		}
	}
	path := "/api/v1/llm/usage"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var result []UsageRecord
	err := c.do(ctx, http.MethodGet, path, nil, &result)
	return result, err
}
