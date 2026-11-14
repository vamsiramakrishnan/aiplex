package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Bounds on the aggregate tools/list. The aggregate answers with one page, so
// it follows every connection's own pagination first, within these limits.
const (
	// aggregateMaxPagesPerConnection bounds upstream tools/list calls for one
	// connection (looping or very long cursors stop here).
	aggregateMaxPagesPerConnection = 20
	// aggregateMaxTools bounds the tools gathered across all connections. The
	// check runs between pages, so a final page may overshoot it; nothing
	// already fetched is discarded.
	aggregateMaxTools = 2000
	// aggregateListTimeout bounds the total time spent listing upstreams.
	aggregateListTimeout = 20 * time.Second
)

// aggregateTruncationMeta is the result _meta key that reports a bounded
// listing. MCP reserves Result._meta for implementation metadata (every
// protocol revision this endpoint implements: 2025-03-26, 2025-06-18 and
// 2025-11-25), and clients ignore keys they do not know. The key uses a
// reverse-DNS prefix as the _meta naming rules require; the standard protocol
// has no other truncation signal for a single-page tools/list.
const aggregateTruncationMeta = "io.aiplex/truncated"

type aggregateTruncation struct {
	Connection string `json:"connection"`
	Reason     string `json:"reason"`
}

// aggregateTools lists every configured connection, following cursors within
// the bounds above. A connection that fails (error, denial, unavailable) at
// any page is omitted entirely and its error is never relayed. Connections cut
// short by a bound keep everything fetched and are reported in the second
// return value.
func (h *ConnectedHandler) aggregateTools(r *http.Request, call *accessPointCall, ap AccessPoint) ([]json.RawMessage, []aggregateTruncation) {
	ctx, cancel := context.WithTimeout(r.Context(), aggregateListTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	tools := []json.RawMessage{}
	var truncated []aggregateTruncation
	for _, tool := range ap.Tools {
		if len(tools) >= aggregateMaxTools || ctx.Err() != nil {
			reason := "tool_limit"
			if ctx.Err() != nil {
				reason = "time_limit"
			}
			truncated = append(truncated, aggregateTruncation{tool.ConnectionRef, reason})
			continue
		}
		var gathered []json.RawMessage
		var cursor *string
		seen := map[string]bool{}
		reason := ""
		failed := false
		for page := 0; ; page++ {
			listed, _, _, err := h.managedList(r, call, tool.ConnectionRef, json.RawMessage(`"aiplex-aggregate"`), cursor)
			if err != nil {
				if ctx.Err() != nil {
					reason = "time_limit"
				} else {
					failed = true
				}
				break
			}
			for _, entry := range listed.Tools {
				if renamed, ok := renameTool(entry, tool.ConnectionRef, tool); ok {
					gathered = append(gathered, renamed)
				}
			}
			next := listed.NextCursor
			if next == "" {
				break
			}
			switch {
			case seen[next]:
				reason = "cursor_loop"
			case page+1 >= aggregateMaxPagesPerConnection:
				reason = "page_limit"
			case len(tools)+len(gathered) >= aggregateMaxTools:
				reason = "tool_limit"
			case ctx.Err() != nil:
				reason = "time_limit"
			}
			if reason != "" {
				break
			}
			seen[next] = true
			cursor = &next
		}
		if failed {
			continue
		}
		tools = append(tools, gathered...)
		if reason != "" {
			truncated = append(truncated, aggregateTruncation{tool.ConnectionRef, reason})
		}
	}
	if len(truncated) > 0 {
		slog.Warn("aggregate tools/list truncated", "access_point", ap.ID, "connections", truncated)
	}
	return tools, truncated
}
