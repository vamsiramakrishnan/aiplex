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

// bedrockUsageCounters maps the Bedrock Converse TokenUsage object.
//
// Documented: inputTokens ("tokens sent in the request"), outputTokens,
// totalTokens ("input tokens and tokens generated"), optional
// cacheReadInputTokens and cacheWriteInputTokens. Whether inputTokens and
// totalTokens include cache-read/write tokens is not stated by the reference,
// so when any cache counter is positive the input/total composition is left
// unknown rather than guessed; cache counters are still reported. Bedrock
// reports no reasoning breakdown, so none is invented.
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
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &n) != nil || n < 0 {
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
	if values["cacheReadInputTokens"] == 0 && values["cacheWriteInputTokens"] == 0 {
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
