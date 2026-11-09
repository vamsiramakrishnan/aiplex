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
// Documented: inputTokens ("tokens sent in the request"), outputTokens,
// totalTokens ("input tokens and tokens generated"), optional
// cacheReadInputTokens, cacheWriteInputTokens and cacheDetails (per-TTL
// cache-write breakdown). Whether inputTokens and totalTokens include
// cache-read/write tokens is not stated by the reference. An inclusive count
// can never be smaller than the cache counters it would contain, so
// inputTokens < cacheRead + cacheWrite proves the exclusive (Messages-style)
// composition, and input_tokens is then reported with exclusive semantics,
// like the Anthropic Messages counter. Otherwise, with positive cache
// counters, input and total stay unknown rather than guessed. The reported
// totalTokens is kept only when no cache counter is positive. Bedrock reports
// no reasoning breakdown, so none is invented.
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
	} else if raw := fields["cacheDetails"]; raw != nil {
		var details []struct {
			InputTokens *int64 `json:"inputTokens"`
		}
		if json.Unmarshal(raw, &details) != nil {
			return nil, fmt.Errorf("invalid Bedrock cache details")
		}
		var sum int64
		for _, d := range details {
			if d.InputTokens == nil || *d.InputTokens < 0 || *d.InputTokens > 1<<53 {
				return nil, fmt.Errorf("invalid Bedrock cache detail")
			}
			sum += *d.InputTokens
		}
		counters["cache_write_tokens"] = sum
		values["cacheWriteInputTokens"] = sum
	}
	cache := values["cacheReadInputTokens"] + counters["cache_write_tokens"]
	if cache > 0 && known["inputTokens"] && values["inputTokens"] < cache {
		// Normalized vocabulary: input_tokens is total input including cache
		// reads and writes; uncached_input_tokens is the fresh remainder.
		counters["uncached_input_tokens"] = values["inputTokens"]
		counters["input_tokens"] = values["inputTokens"] + cache
	}
	if cache == 0 {
		if known["inputTokens"] {
			counters["input_tokens"] = values["inputTokens"]
		}
		if known["totalTokens"] && known["inputTokens"] && known["outputTokens"] && values["totalTokens"] == values["inputTokens"]+values["outputTokens"] {
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
