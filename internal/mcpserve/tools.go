package mcpserve

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

const (
	maxRecordsPage = 100
	defaultRecords = 20
	maxAccessPoint = 200
)

type tool struct {
	name, title, description string
	schema                   map[string]any
	run                      func(ctx context.Context, s *Server, args json.RawMessage) (any, error)
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func objectSchema(required []string, props map[string]any) map[string]any {
	m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

var sinceDesc = "Start of the window: a duration such as 24h or 7d, or an RFC 3339 time. Default 7d."

var tools = []tool{
	{
		name: "whoami", title: "Who am I",
		description: "Show the identity AIPlex sees for this session: principal, email, provider, groups, roles and scopes.",
		schema:      objectSchema(nil, map[string]any{}),
		run: func(ctx context.Context, s *Server, _ json.RawMessage) (any, error) {
			id, err := s.Client.GetIdentity(ctx)
			if err != nil {
				return nil, err
			}
			return id, nil
		},
	},
	{
		name: "list_access_points", title: "List access points",
		description: "List the access points visible to the caller, with model, tool and skill counts.",
		schema:      objectSchema(nil, map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxAccessPoint, "description": "Page size (default 50)."}}),
		run: func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			var a struct {
				Limit int `json:"limit"`
			}
			if err := decodeArgs(args, &a); err != nil {
				return nil, err
			}
			if a.Limit < 0 || a.Limit > maxAccessPoint {
				return nil, fmt.Errorf("limit must be between 1 and %d", maxAccessPoint)
			}
			return s.Client.ListOverviewAccessPoints(ctx, a.Limit, "")
		},
	},
	{
		name: "get_connect_config", title: "Get client configuration",
		description: "Return what `aiplex connect` prints for an access point and client profile: base URLs, MCP URL, authentication steps, snippets and notes. No token is included.",
		schema: objectSchema([]string{"access_point"}, map[string]any{
			"access_point": str("Access point ID."),
			"client":       str("Client profile (claude-code, codex, antigravity, antigravity-acp) or generic (default)."),
		}),
		run: func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			var a struct{ AccessPoint, Client string }
			if err := decodeArgsNamed(args, map[string]*string{"access_point": &a.AccessPoint, "client": &a.Client}); err != nil {
				return nil, err
			}
			if a.AccessPoint == "" {
				return nil, fmt.Errorf("access_point is required")
			}
			return s.Client.ConnectAccessPoint(ctx, a.AccessPoint, a.Client)
		},
	},
	{
		name: "list_models", title: "List models of an access point",
		description: "List the models an access point offers, with the wire protocols each speaks and the access point's base URLs.",
		schema:      objectSchema([]string{"access_point"}, map[string]any{"access_point": str("Access point ID.")}),
		run: func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			var a struct{ AccessPoint string }
			if err := decodeArgsNamed(args, map[string]*string{"access_point": &a.AccessPoint}); err != nil {
				return nil, err
			}
			if a.AccessPoint == "" {
				return nil, fmt.Errorf("access_point is required")
			}
			c, err := s.Client.ConnectAccessPoint(ctx, a.AccessPoint, "")
			if err != nil {
				return nil, err
			}
			return map[string]any{"access_point": c.AccessPointID, "models": c.Models, "base_urls": c.BaseURLs, "endpoints": c.Endpoints}, nil
		},
	},
	{
		name: "usage_summary", title: "Summarize my usage",
		description: "Totals of requests, tokens and cost for the caller's own usage, optionally grouped. Unknown counts and costs are reported as unknown, never as zero; groups say when they include bounded usage.",
		schema: objectSchema(nil, map[string]any{
			"since":    str(sinceDesc),
			"group_by": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Dimensions: model, provider, tool, access-point, status, error-class, caller-kind, source, bound."},
			"bucket":   str("Time bucket: hour or day."),
		}),
		run: func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			var a struct {
				Since   string   `json:"since"`
				GroupBy []string `json:"group_by"`
				Bucket  string   `json:"bucket"`
			}
			if err := decodeArgs(args, &a); err != nil {
				return nil, err
			}
			f, err := s.ownFilter(ctx, a.Since)
			if err != nil {
				return nil, err
			}
			return s.Client.SummarizeUsage(ctx, aiplex.UsageSummaryQuery{UsageFilter: f, GroupBy: a.GroupBy, Bucket: a.Bucket})
		},
	},
	{
		name: "usage_records", title: "List my usage records",
		description: "List the caller's own usage ledger records, newest first, one bounded page at a time. Useful for finding why a request failed or was denied: filter by status denied or error and read error_class.",
		schema: objectSchema(nil, map[string]any{
			"since":        str(sinceDesc),
			"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": maxRecordsPage, "description": fmt.Sprintf("Page size (default %d, at most %d).", defaultRecords, maxRecordsPage)},
			"cursor":       str("next_cursor from a previous page."),
			"access_point": str("Only this access point."),
			"model":        str("Only this model ID."),
			"status":       str("Only this status: ok, error, denied or unknown."),
			"error_class":  str("Only this error class."),
		}),
		run: func(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
			var a struct {
				Since       string `json:"since"`
				Limit       int    `json:"limit"`
				Cursor      string `json:"cursor"`
				AccessPoint string `json:"access_point"`
				Model       string `json:"model"`
				Status      string `json:"status"`
				ErrorClass  string `json:"error_class"`
			}
			if err := decodeArgs(args, &a); err != nil {
				return nil, err
			}
			if a.Limit < 0 || a.Limit > maxRecordsPage {
				return nil, fmt.Errorf("limit must be between 1 and %d", maxRecordsPage)
			}
			if a.Limit == 0 {
				a.Limit = defaultRecords
			}
			f, err := s.ownFilter(ctx, a.Since)
			if err != nil {
				return nil, err
			}
			f.AccessPointID, f.ModelID, f.Status, f.ErrorClass = a.AccessPoint, a.Model, a.Status, a.ErrorClass
			return s.Client.QueryUsageRecords(ctx, aiplex.UsageRecordsQuery{UsageFilter: f, Limit: a.Limit, Cursor: a.Cursor})
		},
	},
}

func toolByName(name string) (tool, bool) {
	for _, t := range tools {
		if t.name == name {
			return t, true
		}
	}
	return tool{}, false
}

func toolDefinitions() []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":        t.name,
			"title":       t.title,
			"description": t.description,
			"inputSchema": t.schema,
			"annotations": map[string]any{
				"title":           t.title,
				"readOnlyHint":    true,
				"destructiveHint": false,
				"idempotentHint":  true,
				"openWorldHint":   false,
			},
		})
	}
	return out
}

// ownFilter scopes a usage query to the caller's own principal and a window.
func (s *Server) ownFilter(ctx context.Context, since string) (aiplex.UsageFilter, error) {
	start, err := parseSince(since, time.Now())
	if err != nil {
		return aiplex.UsageFilter{}, err
	}
	id, err := s.Client.GetIdentity(ctx)
	if err != nil {
		return aiplex.UsageFilter{}, err
	}
	if id.Identity.PrincipalID == "" {
		return aiplex.UsageFilter{}, fmt.Errorf("the API did not return a principal ID for this caller, so usage cannot be limited to it")
	}
	return aiplex.UsageFilter{PrincipalID: id.Identity.PrincipalID, Since: start}, nil
}

// parseSince accepts a Go duration, an Nd day count, or an RFC 3339 time.
func parseSince(v string, now time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		v = "7d"
	}
	if n, ok := strings.CutSuffix(v, "d"); ok {
		if days, err := strconv.Atoi(n); err == nil && days > 0 {
			return now.Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("since %q is not a duration like 24h or 7d, or an RFC 3339 time", v)
}

func decodeArgs(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}

// decodeArgsNamed fills string fields by JSON name and rejects other keys.
func decodeArgsNamed(raw json.RawMessage, fields map[string]*string) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	for k, v := range m {
		dst, ok := fields[k]
		if !ok {
			return fmt.Errorf("invalid arguments: unknown field %q", k)
		}
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("invalid arguments: %q must be a string", k)
		}
		*dst = s
	}
	return nil
}
