package devstack

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// QuotaModules is the module list of a stack started with Options.Quota: the
// API's default modules plus QuotaPlex.
const QuotaModules = "mcplex,a2aplex,llmplex,skillsplex,clientplex,networking,quotaplex"

// quotaFile builds the operator quota deployment file for a local stack that
// has QuotaPlex on. It covers the seeded mock surfaces and the seeded tool
// connection with request-count bindings, so tenant budgets that count
// requests work locally without a hand-written policy.
//
// It deliberately has no token or money meters. Token and cost budgets need
// operator-attested provider limits and price books for the models they
// count; see examples/budgets for an operator-written file that has them.
//
// The membership age is the 24 hour maximum so a long-lived developer token
// keeps its group view. The store is the API's in-memory one: counters reset
// when the local API restarts.
func quotaFile(tenant string, noTools, tariff bool) (map[string]any, error) {
	bindings := []map[string]any{}
	add := func(plane, resource, operation, model, provider string, tool bool) {
		meters := map[string]string{"requests": "requests"}
		if tool {
			meters["tool_calls"] = "tool_calls"
		}
		b := map[string]any{
			"id":        fmt.Sprintf("dev-%s-%s-%s", resource, strings.NewReplacer("/", "-", ":", "-", ".", "-").Replace(operation), model),
			"tenant_id": tenant, "plane": plane, "resource_id": resource, "operation_id": operation,
			"profile": "request-count", "funding_account_id": "dev", "provider": provider, "meters": meters,
		}
		if model != "" {
			b["model"] = model
		}
		bindings = append(bindings, b)
	}
	surfaceOps := func(spec map[string]any) (string, []string, error) {
		raw, err := json.Marshal(spec)
		if err != nil {
			return "", nil, err
		}
		var parsed struct {
			Surface struct {
				Provider struct {
					ID string `json:"id"`
				} `json:"provider"`
				Operations []struct {
					ID string `json:"id"`
				} `json:"operations"`
			} `json:"surface"`
		}
		if err = json.Unmarshal(raw, &parsed); err != nil {
			return "", nil, err
		}
		ids := make([]string, 0, len(parsed.Surface.Operations))
		for _, op := range parsed.Surface.Operations {
			ids = append(ids, op.ID)
		}
		return parsed.Surface.Provider.ID, ids, nil
	}
	primary, _, err := SurfaceSpec(SeedSurface, "mock", "http://127.0.0.1", MockModel)
	if err != nil {
		return nil, err
	}
	provider, ops, err := surfaceOps(primary)
	if err != nil {
		return nil, err
	}
	for _, op := range ops {
		add("llmplex", SeedSurface, op, MockModel, provider, false)
		add("llmplex", SeedSurfaceB, op, MockModel, provider, false)
	}
	for _, m := range MockSurfaces {
		spec, err := MockSurfaceSpec(m, "http://127.0.0.1")
		if err != nil {
			return nil, err
		}
		provider, ops, err := surfaceOps(spec)
		if err != nil {
			return nil, err
		}
		for _, op := range ops {
			add("llmplex", m.Surface, op, m.Model, provider, false)
		}
	}
	if !noTools {
		for _, tool := range MockTools {
			add("mcplex", SeedConnection, "tools/call:"+tool, "", "mcp", true)
		}
	}
	policy := map[string]any{
		"version": "v1", "tenant_id": tenant, "id": "dev-budgets", "revision": 1, "effective_at": "2026-01-01T00:00:00Z",
		"meters": map[string]any{
			"requests":   map[string]any{"id": "requests", "unit": "requests"},
			"tool_calls": map[string]any{"id": "tool_calls", "unit": "calls"},
		},
		"accounts":    []any{map[string]any{"id": "dev", "priority": 1, "tenant_wide": true}},
		"concurrency": []any{map[string]any{"id": "dev-active", "scope": map[string]any{"kind": "tenant"}, "match": map[string]any{}, "limit": 64}},
	}
	if tariff {
		// A placeholder per-request tariff on the primary surface only, so a
		// cost budget has a price to count there and the other surfaces have
		// none, and a per-call tariff on the seeded tool connection, so a cost
		// budget that sets include_tool_calls has a tool price to count. It is
		// not any provider's price.
		policy["meters"].(map[string]any)["requests"] = map[string]any{"id": "requests", "unit": "requests", "billable": true}
		policy["meters"].(map[string]any)["usd"] = map[string]any{"id": "usd", "unit": "money", "currency": "USD", "scale": 6}
		books := []any{map[string]any{"id": "dev-tariff", "revision": 1, "match": map[string]any{"resource_id": SeedSurface},
			"prices": []any{map[string]any{"meter_id": "requests", "money_meter_id": "usd", "numerator": 1000, "denominator": 1}}}}
		if !noTools {
			books = append(books, map[string]any{"id": "dev-tool-tariff", "revision": 1, "match": map[string]any{"plane": "mcplex", "resource_id": SeedConnection},
				"prices": []any{map[string]any{"meter_id": "requests", "money_meter_id": "usd", "numerator": 2000, "denominator": 1}}})
		}
		policy["price_books"] = books
	}
	return map[string]any{
		"version": "v1",
		"host": map[string]any{
			"max_membership_age": int64(24 * 60 * 60 * 1e9),
			"tenants":            []any{map[string]any{"policy": policy, "activate": true}},
			"bindings":           bindings,
		},
	}, nil
}

// writeQuotaFile writes the local quota deployment file into dir.
func writeQuotaFile(dir, tenant string, noTools, tariff bool) (string, error) {
	doc, err := quotaFile(tenant, noTools, tariff)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "quota.json")
	return path, writeJSONFile(path, doc)
}
