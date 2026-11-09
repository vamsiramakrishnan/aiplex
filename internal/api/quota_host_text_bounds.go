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
func quotaMessages(raw json.RawMessage) bool {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 || len(messages) > 1024 {
		return false
	}
	for _, m := range messages {
		if !quotaOnlyFields(m, "role", "content") {
			return false
		}
		var role string
		if json.Unmarshal(m["role"], &role) != nil || (role != "user" && role != "system" && role != "developer" && role != "assistant") || !quotaTextValue(m["content"]) {
			return false
		}
	}
	return true
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
	switch binding.Profile {
	case quotaProfileOpenAI:
		if modelapi.ResponsesOperation(binding.OperationID) {
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
		if !quotaOnlyFields(object, "model", "messages", "system", "max_tokens", "stream", "anthropic_version", "temperature", "top_p", "top_k", "stop_sequences", "thinking") || !quotaMessages(object["messages"]) {
			return 0, fmt.Errorf("inline text messages required")
		}
		if raw := object["system"]; raw != nil && !quotaTextValue(raw) {
			return 0, fmt.Errorf("inline text system prompt required")
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
