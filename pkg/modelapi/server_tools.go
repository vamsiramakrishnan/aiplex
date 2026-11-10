package modelapi

import (
	"bytes"
	"encoding/json"
)

// Server-tool and service-tier observation.
//
// Providers run some tools themselves and bill them per request. The ledger
// holds the counts the provider reports, under the normalized counter name
// "server_tool_<kind>_requests" (usageledger.ServerToolsFromCounters):
//
//   - Anthropic: usage.server_tool_use.<kind>_requests (web_search_requests,
//     web_fetch_requests, code_execution_requests, and any other documented
//     key, kept under its own name).
//   - OpenAI Responses: one request per built-in tool call item in the final
//     response's output array: web_search_call, file_search_call,
//     code_interpreter_call, image_generation_call. Function calls and
//     computer or MCP calls are executed by the caller or priced otherwise and
//     are not counted.
//   - Gemini: the number of grounding web search queries
//     (candidates[].groundingMetadata.webSearchQueries) reported on the chunk
//     that carries usageMetadata. Gemini bills grounding per query on current
//     models and per grounded prompt on older ones; the count is the queries
//     reported, so price it only for models billed per query.
//
// A count is only recorded when the provider reports it; a response without
// the field leaves the kind absent, never zero.

// responsesToolItems maps Responses output item types to tool kinds.
var responsesToolItems = map[string]string{
	"web_search_call":       "web_search",
	"file_search_call":      "file_search",
	"code_interpreter_call": "code_interpreter",
	"image_generation_call": "image_generation",
}

// serverToolCounter returns the normalized counter name for a tool kind, or
// false when the kind is not a lowercase identifier the ledger can hold.
func serverToolCounter(kind string) (string, bool) {
	if kind == "" || len(kind) > 40 || kind[0] < 'a' || kind[0] > 'z' {
		return "", false
	}
	for _, c := range kind {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return "", false
		}
	}
	return "server_tool_" + kind + "_requests", true
}

// addServerToolCalls adds n to a kind's counter and marks the exchange as
// using server tools, so strict token profiles keep failing closed.
func addServerToolCalls(counters map[string]int64, kind string, n int64) {
	meter, ok := serverToolCounter(kind)
	if !ok || n <= 0 {
		return
	}
	counters[meter] += n
	counters["server_tool_calls"] = 1
}

// countResponsesServerTools counts built-in tool call items in a Responses
// output array. A body without an output array reports nothing.
func countResponsesServerTools(raw json.RawMessage, counters map[string]int64) {
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return
	}
	var items []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return
	}
	for _, item := range items {
		if kind, ok := responsesToolItems[item.Type]; ok {
			addServerToolCalls(counters, kind, 1)
		}
	}
}

// countGroundingQueries counts Gemini grounding web search queries.
func countGroundingQueries(raw json.RawMessage, counters map[string]int64) {
	if raw == nil {
		return
	}
	var candidates []struct {
		Grounding *struct {
			Queries []string `json:"webSearchQueries"`
		} `json:"groundingMetadata"`
	}
	if json.Unmarshal(raw, &candidates) != nil {
		return
	}
	var n int64
	for _, candidate := range candidates {
		if candidate.Grounding != nil {
			n += int64(len(candidate.Grounding.Queries))
		}
	}
	addServerToolCalls(counters, "google_search", n)
}

// serviceTier reads the processing tier the provider reported: the response
// object's service_tier (OpenAI) or usage.service_tier (Anthropic). Only a
// short lowercase token is kept; anything else is dropped (unknown).
func serviceTier(object, usage map[string]json.RawMessage) string {
	for _, raw := range []json.RawMessage{object["service_tier"], usage["service_tier"], usage["serviceTier"]} {
		var tier string
		if raw == nil || json.Unmarshal(raw, &tier) != nil || tier == "" || tier == "unspecified" || len(tier) > 32 {
			continue
		}
		valid := true
		for _, c := range tier {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
				valid = false
			}
		}
		if valid {
			return tier
		}
	}
	return ""
}
