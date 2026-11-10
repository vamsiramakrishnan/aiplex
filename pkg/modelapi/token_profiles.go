package modelapi

import "fmt"

func anthropicOperation(op Operation) bool {
	return op.ID == "anthropic.messages" || op.ID == "models.rawPredict" || op.ID == "models.streamRawPredict"
}

// serverToolCounters names the documented Anthropic server_tool_use request
// counters. They are kept as ledger telemetry only.
//
// Server-tool metering choice (explicit, per counter): web_search_requests,
// web_fetch_requests and code_execution_requests are billed per request (code
// execution additionally by container time, which no usage object reports).
// None of them is a token, the strict text profiles refuse tool requests, and
// no exposure bound exists for a request count chosen by the model, so any
// positive quantity keeps every token profile failing closed. Unknown
// server_tool_use keys also fail closed. Gemini tool-use prompt tokens
// (toolUsePromptTokenCount / total_tool_use_tokens) likewise fail closed.
var serverToolCounters = map[string]string{
	"web_search_requests":     "server_tool_web_search_requests",
	"web_fetch_requests":      "server_tool_web_fetch_requests",
	"code_execution_requests": "server_tool_code_execution_requests",
}

// cacheWriteFamily returns the per-TTL cache-write meters reported by an
// output-inclusive format: Anthropic Messages and Bedrock (5m/1h) or OpenAI
// Responses (30m/12h).
func cacheWriteFamily(anthropic bool) []string {
	if anthropic {
		return cacheWriteFamilies[0]
	}
	return cacheWriteFamilies[1]
}

// normalizeOutputInclusive settles the output-inclusive formats (OpenAI,
// Anthropic Messages, Bedrock Converse). Reasoning is already inside output
// and is diagnostic only. Input is decomposed into three disjoint meters:
//
//	uncached_input_tokens  fresh input billed at the input rate
//	cached_tokens          cache reads
//	cache_write_tokens     cache writes (cache creation), with the per-TTL
//	                       cache_write_<ttl>_tokens breakdown when reported
//
// OpenAI input is inclusive of reads and writes (uncached = input - read -
// write); Anthropic and Bedrock input is exclusive (uncached = input, total
// input = input + read + write). A reported zero scalar implies zero in each
// TTL bucket; a positive scalar without a breakdown leaves the buckets
// unknown, so a binding that prices per TTL fails closed there. Server-tool
// quantities always fail closed (see serverToolCounters).
func normalizeOutputInclusive(value *UsageObservation, anthropic bool) error {
	c := value.Counters
	input, inputOK := c["input_tokens"]
	output, outputOK := c["output_tokens"]
	if !inputOK || !outputOK || input < 0 || output < 0 {
		return fmt.Errorf("complete supported text usage is required")
	}
	if c["server_tool_calls"] != 0 {
		return fmt.Errorf("server-tool quantities are not metered by token profiles")
	}
	cached, cacheOK := c["cached_tokens"]
	if !anthropic && !cacheOK {
		return fmt.Errorf("complete cached-input usage required")
	}
	// Anthropic documents the cache counters as nullable; null or absent
	// means no cache activity was reported for this request. OpenAI documents
	// cache_write_tokens with default 0; models without separately billed
	// writes keep any written tokens inside ordinary input.
	write := c["cache_write_tokens"]
	if cached < 0 || write < 0 {
		return fmt.Errorf("invalid cache usage")
	}
	const maxInt = int64(^uint64(0) >> 1)
	family := cacheWriteFamily(anthropic)
	known, sum := 0, int64(0)
	for _, meter := range family {
		if n, ok := c[meter]; ok {
			known++
			sum += n
		}
	}
	switch {
	case known == 0 && write == 0:
		for _, meter := range family {
			c[meter] = 0
		}
	case known > 0 && (known != len(family) || sum != write):
		return fmt.Errorf("cache-write breakdown conflicts with its total")
	}
	if anthropic {
		if uncached, normalized := c["uncached_input_tokens"]; normalized {
			if uncached < 0 || cached > maxInt-write || uncached > maxInt-cached-write || input != uncached+cached+write {
				return fmt.Errorf("conflicting normalized input")
			}
		} else {
			if cached > maxInt-write || input > maxInt-cached-write {
				return fmt.Errorf("native token total overflow")
			}
			c["uncached_input_tokens"] = input
			c["input_tokens"] = input + cached + write
		}
	} else {
		if cached > input || write > input-cached {
			return fmt.Errorf("cached input exceeds total input")
		}
		if total, ok := c["total_tokens"]; ok && (total < input || total-input != output) {
			return fmt.Errorf("conflicting native token total")
		}
		if thought := c["thought_tokens"]; thought > output {
			return fmt.Errorf("reasoning exceeds output")
		}
		c["uncached_input_tokens"] = input - cached - write
	}
	c["cached_tokens"] = cached
	c["cache_write_tokens"] = write
	value.Cumulative = true
	return nil
}

// normalizeVertexOpenAIChat settles the Vertex OpenAI-compatible endpoint.
// It documents no cached-input breakdown and (for Gemini) completion_tokens
// that EXCLUDE reasoning. vertexCompletionComposition already folded reasoning
// into output only when the native total proved the composition (otherwise
// output is absent here). Settlement therefore requires input and output, and
// a reported total equal to their sum. The vertex-openai-text profile meters
// exactly total input (input_tokens) and billed output. When the endpoint does
// report cached_tokens, the openai-text decomposition is added as well.
func normalizeVertexOpenAIChat(value *UsageObservation) error {
	c := value.Counters
	input, inputOK := c["input_tokens"]
	output, outputOK := c["output_tokens"]
	if !inputOK || !outputOK || input < 0 || output < 0 {
		return fmt.Errorf("complete proven input and output usage is required")
	}
	if total, ok := c["total_tokens"]; ok && (total < input || total-input != output) {
		return fmt.Errorf("conflicting native token total")
	}
	if c["server_tool_calls"] != 0 {
		return fmt.Errorf("server-tool quantities are not metered by token profiles")
	}
	if _, cached := c["cached_tokens"]; cached {
		return normalizeOutputInclusive(value, false)
	}
	value.Cumulative = true
	return nil
}

func normalizeControlledFinal(op Operation, value *UsageObservation) error {
	switch {
	case geminiGeneration(op) || op.ID == "interaction.create":
		return normalizeGeminiFinal(value)
	case op.ID == "openai.chat-completions":
		return normalizeVertexOpenAIChat(value)
	case op.ID == "response.create" || providerOpenAITokenOperation(op):
		return normalizeOutputInclusive(value, false)
	case anthropicOperation(op) || bedrockOperation(op):
		// Bedrock Converse inputTokens is "only the non-cached input tokens"
		// (total input = inputTokens + cacheReadInputTokens +
		// cacheWriteInputTokens); Anthropic-shaped InvokeModel bodies follow
		// the Messages usage rules.
		return normalizeOutputInclusive(value, true)
	}
	return nil
}

// MeteringProfile is an optional lease contract. Request-count consumers must
// not acquire a token-accounting promise just because the native API has usage.
func normalizeForRun(op Operation, value *UsageObservation, run *executionRun) error {
	if requestCountRun(run) {
		return nil
	}
	return normalizeControlledFinal(op, value)
}

func requestCountRun(run *executionRun) bool {
	contract, ok := run.lease.(interface{ MeteringProfile() string })
	return ok && contract.MeteringProfile() == "request-count"
}
