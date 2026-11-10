package api

import (
	"encoding/json"
	"fmt"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

func quotaFalse(object map[string]json.RawMessage, key string, required bool) bool {
	raw, exists := object[key]
	if !exists {
		return !required
	}
	var value bool
	return json.Unmarshal(raw, &value) == nil && !value
}
func quotaTextValue(raw json.RawMessage) bool {
	var text string
	return raw != nil && json.Unmarshal(raw, &text) == nil
}
func quotaMessages(raw json.RawMessage) bool { return quotaMessagesWith(raw, false) }

// quotaMessagesWith accepts inline text messages. With cacheBlocks, Anthropic
// text content blocks carrying a cache_control breakpoint are accepted too.
func quotaMessagesWith(raw json.RawMessage, cacheBlocks bool) bool {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 || len(messages) > 1024 {
		return false
	}
	for _, m := range messages {
		if !quotaOnlyFields(m, "role", "content") {
			return false
		}
		var role string
		if json.Unmarshal(m["role"], &role) != nil || (role != "user" && role != "system" && role != "developer" && role != "assistant") {
			return false
		}
		if !quotaTextValue(m["content"]) && !(cacheBlocks && quotaAnthropicTextBlocks(m["content"])) {
			return false
		}
	}
	return true
}

// quotaAnthropicCacheControl accepts the documented ephemeral breakpoint with
// an optional 5m or 1h TTL.
func quotaAnthropicCacheControl(raw json.RawMessage) bool {
	var control map[string]json.RawMessage
	var kind, ttl string
	if json.Unmarshal(raw, &control) != nil || !quotaOnlyFields(control, "type", "ttl") || json.Unmarshal(control["type"], &kind) != nil || kind != "ephemeral" {
		return false
	}
	if control["ttl"] != nil && (json.Unmarshal(control["ttl"], &ttl) != nil || (ttl != "5m" && ttl != "1h")) {
		return false
	}
	return true
}

// quotaAnthropicTextBlocks accepts a bounded array of text blocks, each with
// an optional cache_control breakpoint. Media, documents and tool blocks are
// still refused.
func quotaAnthropicTextBlocks(raw json.RawMessage) bool {
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 || len(blocks) > 1024 {
		return false
	}
	for _, block := range blocks {
		var kind string
		if !quotaOnlyFields(block, "type", "text", "cache_control") || json.Unmarshal(block["type"], &kind) != nil || kind != "text" || !quotaTextValue(block["text"]) {
			return false
		}
		if block["cache_control"] != nil && !quotaAnthropicCacheControl(block["cache_control"]) {
			return false
		}
	}
	return true
}

// quotaConverseTextOutput bounds a Bedrock Converse request: inline text
// content blocks (and, when cache writes are metered, cachePoint blocks), an
// optional text system prompt and an explicit inferenceConfig.maxTokens.
// Tools, guardrails, documents, images and additional model fields are refused.
func quotaConverseTextOutput(body []byte, cache bool) (int64, error) {
	object, err := quotaJSONObject(body)
	if err != nil {
		return 0, err
	}
	if !quotaOnlyFields(object, "messages", "system", "inferenceConfig") {
		return 0, fmt.Errorf("unsupported Converse exposure feature")
	}
	blocks := func(raw json.RawMessage) bool {
		var items []map[string]json.RawMessage
		if json.Unmarshal(raw, &items) != nil || len(items) == 0 || len(items) > 1024 {
			return false
		}
		for _, item := range items {
			if len(item) != 1 {
				return false
			}
			if text := item["text"]; text != nil {
				if !quotaTextValue(text) {
					return false
				}
				continue
			}
			point := item["cachePoint"]
			var fields map[string]json.RawMessage
			var kind, ttl string
			if !cache || point == nil || json.Unmarshal(point, &fields) != nil || !quotaOnlyFields(fields, "type", "ttl") || json.Unmarshal(fields["type"], &kind) != nil || kind != "default" {
				return false
			}
			if fields["ttl"] != nil && (json.Unmarshal(fields["ttl"], &ttl) != nil || (ttl != "5m" && ttl != "1h")) {
				return false
			}
		}
		return true
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(object["messages"], &messages) != nil || len(messages) == 0 || len(messages) > 1024 {
		return 0, fmt.Errorf("inline text messages required")
	}
	for _, m := range messages {
		var role string
		if !quotaOnlyFields(m, "role", "content") || json.Unmarshal(m["role"], &role) != nil || (role != "user" && role != "assistant") || !blocks(m["content"]) {
			return 0, fmt.Errorf("inline text messages required")
		}
	}
	if raw := object["system"]; raw != nil && !blocks(raw) {
		return 0, fmt.Errorf("inline text system prompt required")
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(object["inferenceConfig"], &config) != nil || !quotaOnlyFields(config, "maxTokens", "temperature", "topP", "stopSequences") {
		return 0, fmt.Errorf("explicit inference configuration required")
	}
	return quotaPositiveInteger(config["maxTokens"])
}
func quotaProviderTextOutput(binding QuotaBinding, body []byte) (int64, error) {
	object, err := quotaJSONObject(body)
	if err != nil {
		return 0, err
	}
	if raw := object["model"]; raw != nil {
		var model string
		if json.Unmarshal(raw, &model) != nil || model != binding.Model {
			return 0, fmt.Errorf("model must match bound selection")
		}
	}
	if !quotaFalse(object, "background", false) {
		return 0, fmt.Errorf("background work is not bounded by this exchange")
	}
	if raw := object["stream"]; raw != nil {
		var stream bool
		if json.Unmarshal(raw, &stream) != nil {
			return 0, fmt.Errorf("invalid stream selection")
		}
	}
	writes, _ := quotaBindingCacheWrites(binding)
	cache := len(writes) > 0
	if binding.OperationID == "openai.v1.responses-websocket" || binding.OperationID == "openai.v1.realtime" {
		// A WebSocket exchange is one client response.create event.
		var kind string
		if json.Unmarshal(object["type"], &kind) != nil || kind != "response.create" {
			return 0, fmt.Errorf("one response.create event required")
		}
		delete(object, "type")
		delete(object, "event_id")
		if binding.Profile == quotaProfileRealtime {
			return quotaRealtimeResponseOutput(object)
		}
	}
	switch binding.Profile {
	case quotaProfileBedrockConverse:
		return quotaConverseTextOutput(body, cache)
	case quotaProfileOpenAI, quotaProfileVertexOpenAI:
		// Prompt-cache routing hints (prompt_cache_key, prompt_cache_options)
		// change cache placement, not exposure; they are accepted only when
		// cache writes are metered, because they can cause billed writes.
		if raw := object["prompt_cache_key"]; raw != nil && (!cache || !quotaTextValue(raw)) {
			return 0, fmt.Errorf("prompt cache key requires metered cache writes")
		}
		if raw := object["prompt_cache_options"]; raw != nil {
			var options map[string]json.RawMessage
			var mode, ttl string
			if !cache || json.Unmarshal(raw, &options) != nil || !quotaOnlyFields(options, "mode", "ttl") {
				return 0, fmt.Errorf("prompt cache options require metered cache writes")
			}
			if options["mode"] != nil && (json.Unmarshal(options["mode"], &mode) != nil || (mode != "implicit" && mode != "explicit")) {
				return 0, fmt.Errorf("unsupported prompt cache mode")
			}
			if options["ttl"] != nil && (json.Unmarshal(options["ttl"], &ttl) != nil || ttl == "") {
				return 0, fmt.Errorf("unsupported prompt cache TTL")
			}
		}
		delete(object, "prompt_cache_key")
		delete(object, "prompt_cache_options")
		if modelapi.ResponsesOperation(binding.OperationID) || binding.OperationID == "openai.v1.responses-websocket" {
			if !quotaOnlyFields(object, "model", "input", "instructions", "max_output_tokens", "stream", "store", "background", "temperature", "top_p", "reasoning", "text") || !quotaFalse(object, "store", true) {
				return 0, fmt.Errorf("stateless text responses required")
			}
			if !quotaTextValue(object["input"]) && !quotaMessages(object["input"]) {
				return 0, fmt.Errorf("inline text input required")
			}
			if raw := object["instructions"]; raw != nil && !quotaTextValue(raw) {
				return 0, fmt.Errorf("inline instructions required")
			}
			if raw := object["text"]; raw != nil {
				var format map[string]json.RawMessage
				if json.Unmarshal(raw, &format) != nil || !quotaOnlyFields(format, "format") {
					return 0, fmt.Errorf("text response format required")
				}
				var f map[string]json.RawMessage
				if json.Unmarshal(format["format"], &f) != nil || !quotaOnlyFields(f, "type") {
					return 0, fmt.Errorf("simple text format required")
				}
				var kind string
				if json.Unmarshal(f["type"], &kind) != nil || kind != "text" {
					return 0, fmt.Errorf("simple text format required")
				}
			}
			return quotaPositiveInteger(object["max_output_tokens"])
		}
		if !quotaOnlyFields(object, "model", "messages", "max_completion_tokens", "stream", "stream_options", "n", "temperature", "top_p", "seed", "stop", "reasoning_effort", "store") || !quotaMessages(object["messages"]) || !quotaFalse(object, "store", false) {
			return 0, fmt.Errorf("bounded inline text chat required")
		}
		if raw := object["n"]; raw != nil {
			n, e := quotaPositiveInteger(raw)
			if e != nil || n != 1 {
				return 0, fmt.Errorf("one completion required")
			}
		}
		var streaming bool
		_ = json.Unmarshal(object["stream"], &streaming)
		if streaming {
			var options map[string]json.RawMessage
			var usage bool
			if json.Unmarshal(object["stream_options"], &options) != nil || !quotaOnlyFields(options, "include_usage") || json.Unmarshal(options["include_usage"], &usage) != nil || !usage {
				return 0, fmt.Errorf("streaming chat requires final usage")
			}
		}
		return quotaPositiveInteger(object["max_completion_tokens"])
	case quotaProfileAnthropic:
		if !quotaOnlyFields(object, "model", "messages", "system", "max_tokens", "stream", "anthropic_version", "temperature", "top_p", "top_k", "stop_sequences", "thinking", "cache_control") || !quotaMessagesWith(object["messages"], cache) {
			return 0, fmt.Errorf("inline text messages required")
		}
		if raw := object["system"]; raw != nil && !quotaTextValue(raw) && !(cache && quotaAnthropicTextBlocks(raw)) {
			return 0, fmt.Errorf("inline text system prompt required")
		}
		// Top-level cache_control is automatic caching: Claude places the
		// breakpoint on the last cacheable block. Breakpoints only change
		// which input is written or read, never the input ceiling.
		if raw := object["cache_control"]; raw != nil && (!cache || !quotaAnthropicCacheControl(raw)) {
			return 0, fmt.Errorf("cache controls require metered cache writes")
		}
		maximum, e := quotaPositiveInteger(object["max_tokens"])
		if e != nil {
			return 0, e
		}
		if raw := object["thinking"]; raw != nil {
			var thinking map[string]json.RawMessage
			var kind string
			if json.Unmarshal(raw, &thinking) != nil || !quotaOnlyFields(thinking, "type", "budget_tokens") || json.Unmarshal(thinking["type"], &kind) != nil {
				return 0, fmt.Errorf("bounded thinking required")
			}
			if kind == "enabled" {
				n, e := quotaPositiveInteger(thinking["budget_tokens"])
				if e != nil || n > maximum {
					return 0, fmt.Errorf("thinking exceeds output bound")
				}
			} else if kind != "disabled" || thinking["budget_tokens"] != nil {
				return 0, fmt.Errorf("unsupported thinking mode")
			}
		}
		return maximum, nil
	case quotaProfileInteraction:
		if !quotaOnlyFields(object, "model", "input", "generation_config", "generationConfig", "system_instruction", "systemInstruction", "stream", "store", "background") || !quotaTextValue(object["input"]) || !quotaFalse(object, "store", true) {
			return 0, fmt.Errorf("stateless model-backed text interaction required")
		}
		for _, key := range []string{"system_instruction", "systemInstruction"} {
			if raw := object[key]; raw != nil && !quotaTextValue(raw) {
				return 0, fmt.Errorf("inline system instruction required")
			}
		}
		raw := object["generation_config"]
		if other := object["generationConfig"]; other != nil {
			if raw != nil {
				return 0, fmt.Errorf("ambiguous generation configuration")
			}
			raw = other
		}
		var generation map[string]json.RawMessage
		if json.Unmarshal(raw, &generation) != nil || !quotaOnlyFields(generation, "max_output_tokens", "maxOutputTokens", "temperature", "top_p", "topP", "seed", "thinking_level", "thinkingLevel") {
			return 0, fmt.Errorf("bounded interaction configuration required")
		}
		output := generation["max_output_tokens"]
		if other := generation["maxOutputTokens"]; other != nil {
			if output != nil {
				return 0, fmt.Errorf("ambiguous output bound")
			}
			output = other
		}
		return quotaPositiveInteger(output)
	}
	return 0, fmt.Errorf("unsupported text profile")
}

// quotaRealtimeResponseOutput bounds one Realtime response.create: an
// explicit integer response.max_output_tokens ("inf" is unbounded), output
// modalities limited to text and audio, inline instructions, and no
// out-of-band input, tools or conversation selection. Input is the
// server-side conversation, bounded by the attested model input ceiling.
func quotaRealtimeResponseOutput(object map[string]json.RawMessage) (int64, error) {
	if !quotaOnlyFields(object, "response") {
		return 0, fmt.Errorf("unsupported response.create field")
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(object["response"], &response) != nil || response == nil || !quotaOnlyFields(response, "max_output_tokens", "instructions", "output_modalities", "modalities", "voice", "audio", "temperature", "metadata") {
		return 0, fmt.Errorf("bounded text/audio response required")
	}
	if raw := response["instructions"]; raw != nil && !quotaTextValue(raw) {
		return 0, fmt.Errorf("inline instructions required")
	}
	for _, key := range []string{"output_modalities", "modalities"} {
		if raw := response[key]; raw != nil {
			var values []string
			if json.Unmarshal(raw, &values) != nil || len(values) == 0 || len(values) > 2 {
				return 0, fmt.Errorf("invalid output modalities")
			}
			for _, value := range values {
				if value != "text" && value != "audio" {
					return 0, fmt.Errorf("unsupported output modality")
				}
			}
		}
	}
	return quotaPositiveInteger(response["max_output_tokens"])
}
