package modelapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// chatCompletionsOperation reports Chat Completions wire-format operations,
// whose streams end with a data: [DONE] marker after the final usage chunk.
func chatCompletionsOperation(op Operation) bool {
	switch op.ID {
	case "openai.chat-completions", "openai.v1.chat-completions", "azure.v1.chat-completions", "azure.deployments.chat-completions", "compat.chat-completions":
		return true
	}
	return false
}

// providerOpenAITokenOperation reports provider operations whose OpenAI usage
// composition (cached input inside prompt/input tokens, reasoning inside
// completion/output tokens) is documented and normalized by the
// output-inclusive profile. Generic OpenAI-compatible servers are excluded:
// their counter semantics are not established by a provider contract.
func providerOpenAITokenOperation(op Operation) bool {
	switch op.ID {
	case "openai.v1.chat-completions", "openai.v1.responses", "azure.v1.chat-completions", "azure.v1.responses", "azure.deployments.chat-completions":
		return true
	}
	return false
}

// ResponsesOperation reports operations speaking the Responses wire format.
func ResponsesOperation(id string) bool {
	return id == "response.create" || id == "openai.v1.responses" || id == "azure.v1.responses"
}

func bedrockOperation(op Operation) bool { return strings.HasPrefix(op.ID, "bedrock.") }

func bedrockConverse(op Operation) bool {
	return op.ID == "bedrock.converse" || op.ID == "bedrock.converse-stream"
}

func bedrockInvoke(op Operation) bool {
	return op.ID == "bedrock.invoke" || op.ID == "bedrock.invoke-stream"
}

// bedrockUsageCounters maps the Bedrock Converse TokenUsage object.
//
// AWS documents (Bedrock user guide, "Prompt caching for faster model
// inference", https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html):
// "When prompt caching is enabled, the inputTokens field represents only the
// non-cached input tokens (tokens that were not read from or written to the
// cache)" and "total input tokens = inputTokens + cacheReadInputTokens +
// cacheWriteInputTokens". inputTokens is therefore exclusive, like the
// Anthropic Messages counter, and is normalized to uncached_input_tokens with
// input_tokens = all input. cacheDetails is "Detailed breakdown of cache
// writes by TTL", ttl one of 5m | 1h (API_runtime_CacheDetail); it is mapped to
// cache_write_5m_tokens / cache_write_1h_tokens. Absent cache counters
// (Required: No) mean no cache activity was reported. Whether totalTokens
// includes cache tokens is still not stated, so it is kept only when it equals
// all input + output; otherwise it stays unknown (never used for settlement).
// Bedrock reports no reasoning breakdown, so none is invented.
func bedrockUsageCounters(raw json.RawMessage) (map[string]int64, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, fmt.Errorf("invalid Bedrock usage object")
	}
	read := func(name string) (int64, bool, error) {
		value := fields[name]
		if value == nil {
			return 0, false, nil
		}
		var n int64
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &n) != nil || n < 0 || n > 1<<53 {
			return 0, false, fmt.Errorf("invalid Bedrock usage counter")
		}
		return n, true, nil
	}
	counters := map[string]int64{}
	values := map[string]int64{}
	known := map[string]bool{}
	for _, name := range []string{"inputTokens", "outputTokens", "totalTokens", "cacheReadInputTokens", "cacheWriteInputTokens"} {
		n, ok, err := read(name)
		if err != nil {
			return nil, err
		}
		values[name], known[name] = n, ok
	}
	if known["outputTokens"] {
		counters["output_tokens"] = values["outputTokens"]
	}
	if known["cacheReadInputTokens"] {
		counters["cached_tokens"] = values["cacheReadInputTokens"]
	}
	if known["cacheWriteInputTokens"] {
		counters["cache_write_tokens"] = values["cacheWriteInputTokens"]
	}
	if raw := fields["cacheDetails"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var details []struct {
			InputTokens *int64 `json:"inputTokens"`
			TTL         string `json:"ttl"`
		}
		if json.Unmarshal(raw, &details) != nil {
			return nil, fmt.Errorf("invalid Bedrock cache details")
		}
		buckets := map[string]int64{"cache_write_5m_tokens": 0, "cache_write_1h_tokens": 0}
		var sum int64
		for _, d := range details {
			if d.InputTokens == nil || *d.InputTokens < 0 || *d.InputTokens > 1<<53 {
				return nil, fmt.Errorf("invalid Bedrock cache detail")
			}
			sum += *d.InputTokens
			switch d.TTL {
			case "5m":
				buckets["cache_write_5m_tokens"] += *d.InputTokens
			case "1h":
				buckets["cache_write_1h_tokens"] += *d.InputTokens
			default:
				return nil, fmt.Errorf("undocumented Bedrock cache TTL")
			}
		}
		if known["cacheWriteInputTokens"] && sum != values["cacheWriteInputTokens"] {
			return nil, fmt.Errorf("Bedrock cache details conflict with cache writes")
		}
		counters["cache_write_tokens"] = sum
		for meter, n := range buckets {
			counters[meter] = n
		}
	}
	cache := counters["cached_tokens"] + counters["cache_write_tokens"]
	if known["inputTokens"] {
		// Normalized vocabulary: input_tokens is total input including cache
		// reads and writes; uncached_input_tokens is the fresh remainder.
		counters["uncached_input_tokens"] = values["inputTokens"]
		counters["input_tokens"] = values["inputTokens"] + cache
		if known["totalTokens"] && known["outputTokens"] && values["totalTokens"] == counters["input_tokens"]+values["outputTokens"] {
			counters["total_tokens"] = values["totalTokens"]
		}
	}
	return counters, nil
}

// inspectBedrockJSON observes a Bedrock Runtime JSON response. Converse has a
// documented usage object and stopReason terminal; InvokeModel bodies are
// model-specific, so their usage stays unknown (request-count only).
func (g *Gateway) inspectBedrockJSON(op Operation, data []byte, includeUsage bool) (jsonObservation, error) {
	var result jsonObservation
	if !bedrockConverse(op) || !includeUsage {
		return result, nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return result, nil
	}
	var stop string
	if json.Unmarshal(root["stopReason"], &stop) == nil && stop != "" {
		result.nativeTerminal = true
	}
	raw := root["usage"]
	if raw == nil {
		return result, nil
	}
	counters, err := bedrockUsageCounters(raw)
	if err != nil {
		return result, err
	}
	if len(counters) == 0 {
		return result, nil
	}
	result.usage = &UsageObservation{Source: "upstream", SurfaceID: g.surface.ID, OperationID: op.ID, Terminal: result.nativeTerminal, Counters: counters, Cumulative: true}
	return result, nil
}
