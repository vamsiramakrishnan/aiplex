package modelapi

import "fmt"

func anthropicOperation(op Operation) bool {
	return op.ID == "anthropic.messages" || op.ID == "models.rawPredict" || op.ID == "models.streamRawPredict"
}

// Output-inclusive providers already bill reasoning inside output_tokens. It is
// diagnostic metadata, not another additive token meter. Explicit cache writes
// and server tools need separate exposure/pricing profiles and fail closed.
func normalizeOutputInclusive(value *UsageObservation, anthropic bool) error {
	input, inputOK := value.Counters["input_tokens"]
	output, outputOK := value.Counters["output_tokens"]
	if !inputOK || !outputOK || input < 0 || output < 0 || value.Counters["cache_write_tokens"] != 0 || value.Counters["server_tool_calls"] != 0 {
		return fmt.Errorf("complete supported text usage is required")
	}
	cached, cacheOK := value.Counters["cached_tokens"]
	if !anthropic && !cacheOK {
		return fmt.Errorf("complete cached-input usage required")
	}
	if cached < 0 {
		return fmt.Errorf("invalid cached input")
	}
	if anthropic {
		if uncached, normalized := value.Counters["uncached_input_tokens"]; normalized {
			if uncached < 0 || uncached > int64(^uint64(0)>>1)-cached || input != uncached+cached {
				return fmt.Errorf("conflicting normalized input")
			}
			value.Cumulative = true
			return nil
		}
		if input > int64(^uint64(0)>>1)-cached {
			return fmt.Errorf("native token total overflow")
		}
		value.Counters["uncached_input_tokens"] = input
		value.Counters["input_tokens"] = input + cached
	} else {
		if cached > input {
			return fmt.Errorf("cached input exceeds total input")
		}
		if total, ok := value.Counters["total_tokens"]; ok && (total < input || total-input != output) {
			return fmt.Errorf("conflicting native token total")
		}
		if thought := value.Counters["thought_tokens"]; thought > output {
			return fmt.Errorf("reasoning exceeds output")
		}
		value.Counters["uncached_input_tokens"] = input - cached
	}
	value.Counters["cached_tokens"] = cached
	value.Cumulative = true
	return nil
}
func normalizeControlledFinal(op Operation, value *UsageObservation) error {
	switch {
	case geminiGeneration(op) || op.ID == "interaction.create":
		return normalizeGeminiFinal(value)
	case op.ID == "openai.chat-completions" || op.ID == "response.create":
		return normalizeOutputInclusive(value, false)
	case anthropicOperation(op):
		return normalizeOutputInclusive(value, true)
	}
	return nil
}

// MeteringProfile is an optional lease contract. Request-count consumers must
// not acquire a token-accounting promise just because the native API has usage.
func normalizeForRun(op Operation, value *UsageObservation, run *executionRun) error {
	if contract, ok := run.lease.(interface{ MeteringProfile() string }); ok && contract.MeteringProfile() == "request-count" {
		return nil
	}
	return normalizeControlledFinal(op, value)
}
