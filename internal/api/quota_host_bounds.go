package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

func quotaJSONUnique(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			token, err = decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate JSON field")
			}
			seen[key] = true
			if err = quotaJSONUnique(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err = quotaJSONUnique(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	return nil
}

func quotaJSONObject(body []byte) (map[string]json.RawMessage, error) {
	if len(body) == 0 || len(body) > 8<<20 {
		return nil, fmt.Errorf("bounded JSON body required")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := quotaJSONUnique(decoder, 0); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("single JSON object required")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return nil, fmt.Errorf("JSON object required")
	}
	return object, nil
}

func quotaOnlyFields(object map[string]json.RawMessage, allowed ...string) bool {
	set := map[string]bool{}
	for _, key := range allowed {
		set[key] = true
	}
	for key := range object {
		if !set[key] {
			return false
		}
	}
	return true
}

func quotaTextParts(raw json.RawMessage, system bool) error {
	var content map[string]json.RawMessage
	if json.Unmarshal(raw, &content) != nil || content == nil || !quotaOnlyFields(content, "role", "parts") {
		return fmt.Errorf("text-only content required")
	}
	if roleRaw := content["role"]; roleRaw != nil {
		var role string
		if json.Unmarshal(roleRaw, &role) != nil || (!system && role != "user") || (system && role != "system" && role != "user") {
			return fmt.Errorf("unsupported conversation role")
		}
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(content["parts"], &parts) != nil || len(parts) == 0 || len(parts) > 1024 {
		return fmt.Errorf("bounded text parts required")
	}
	for _, part := range parts {
		var text string
		if len(part) != 1 || part["text"] == nil || json.Unmarshal(part["text"], &text) != nil {
			return fmt.Errorf("external, media and function parts are unsupported")
		}
	}
	return nil
}

func quotaPositiveInteger(raw json.RawMessage) (int64, error) {
	var value int64
	if raw == nil || json.Unmarshal(raw, &value) != nil || value <= 0 {
		return 0, fmt.Errorf("positive explicit integer required")
	}
	return value, nil
}

// quotaGeminiTextOutput rejects unbounded modalities, caches, tool work and
// alternative model routing. It does not estimate input tokens from bytes.
func quotaGeminiTextOutput(body []byte) (int64, error) {
	object, err := quotaJSONObject(body)
	if err != nil {
		return 0, err
	}
	if !quotaOnlyFields(object, "contents", "systemInstruction", "generationConfig", "safetySettings", "model") {
		return 0, fmt.Errorf("unsupported Gemini exposure feature")
	}
	var contents []json.RawMessage
	if json.Unmarshal(object["contents"], &contents) != nil || len(contents) != 1 {
		return 0, fmt.Errorf("one text-only user content is required")
	}
	if err = quotaTextParts(contents[0], false); err != nil {
		return 0, err
	}
	if raw := object["systemInstruction"]; raw != nil {
		if err = quotaTextParts(raw, true); err != nil {
			return 0, err
		}
	}
	var generation map[string]json.RawMessage
	if json.Unmarshal(object["generationConfig"], &generation) != nil || generation == nil || !quotaOnlyFields(generation, "maxOutputTokens", "candidateCount", "thinkingConfig", "temperature", "topP", "topK", "seed", "stopSequences", "responseMimeType", "responseModalities") {
		return 0, fmt.Errorf("explicit supported generation configuration required")
	}
	output, err := quotaPositiveInteger(generation["maxOutputTokens"])
	if err != nil {
		return 0, err
	}
	if raw := generation["candidateCount"]; raw != nil {
		value, e := quotaPositiveInteger(raw)
		if e != nil || value != 1 {
			return 0, fmt.Errorf("only one candidate is supported")
		}
	}
	if raw := generation["thinkingConfig"]; raw != nil {
		var thinking map[string]json.RawMessage
		if json.Unmarshal(raw, &thinking) != nil || thinking == nil || !quotaOnlyFields(thinking, "thinkingBudget", "includeThoughts") {
			return 0, fmt.Errorf("unsupported thinking configuration")
		}
		if raw := thinking["thinkingBudget"]; raw != nil {
			var value int64
			if json.Unmarshal(raw, &value) != nil || value < 0 || value > output {
				return 0, fmt.Errorf("thinking budget exceeds protected output exposure")
			}
		}
		if raw := thinking["includeThoughts"]; raw != nil {
			var value bool
			if json.Unmarshal(raw, &value) != nil {
				return 0, fmt.Errorf("invalid thought visibility")
			}
		}
	}
	for _, key := range []string{"temperature", "topP"} {
		if raw := generation[key]; raw != nil {
			var value float64
			maximum := 2.0
			if key == "topP" {
				maximum = 1
			}
			if json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > maximum {
				return 0, fmt.Errorf("invalid sampling configuration")
			}
		}
	}
	if raw := generation["topK"]; raw != nil {
		value, e := quotaPositiveInteger(raw)
		if e != nil || value > 1000 {
			return 0, fmt.Errorf("invalid topK")
		}
	}
	if raw := generation["seed"]; raw != nil {
		var value int32
		if json.Unmarshal(raw, &value) != nil {
			return 0, fmt.Errorf("invalid seed")
		}
	}
	if raw := generation["stopSequences"]; raw != nil {
		var values []string
		if json.Unmarshal(raw, &values) != nil || len(values) > 16 {
			return 0, fmt.Errorf("invalid stop sequences")
		}
		for _, value := range values {
			if len(value) > 1024 {
				return 0, fmt.Errorf("oversized stop sequence")
			}
		}
	}
	if raw := generation["responseMimeType"]; raw != nil {
		var value string
		if json.Unmarshal(raw, &value) != nil || (value != "text/plain" && value != "application/json") {
			return 0, fmt.Errorf("text response required")
		}
	}
	if raw := generation["responseModalities"]; raw != nil {
		var values []string
		if json.Unmarshal(raw, &values) != nil || len(values) != 1 || values[0] != "TEXT" {
			return 0, fmt.Errorf("text response modality required")
		}
	}
	if raw := object["safetySettings"]; raw != nil {
		var settings []map[string]json.RawMessage
		if json.Unmarshal(raw, &settings) != nil || len(settings) > 16 {
			return 0, fmt.Errorf("invalid safety settings")
		}
		for _, setting := range settings {
			if !quotaOnlyFields(setting, "category", "threshold") {
				return 0, fmt.Errorf("unsupported safety settings")
			}
			for _, value := range setting {
				var text string
				if json.Unmarshal(value, &text) != nil || !quotaText(text, true) {
					return 0, fmt.Errorf("invalid safety setting")
				}
			}
		}
	}
	return output, nil
}

func (h *QuotaHost) nativeBounds(ctx context.Context, binding QuotaBinding, target quotaplex.Target, in QuotaInvocation, now time.Time) (map[string]int64, *QuotaBoundsProof, error) {
	counts := map[string]int64{"requests": 1}
	if in.Plane == "mcplex" {
		counts["tool_calls"] = 1
	}
	if binding.Profile == quotaProfileRequests {
		return counts, nil, nil
	}
	if quotaExtendedProfile(binding.Profile) {
		return h.extendedBounds(ctx, binding, target, in, now)
	}
	if !quotaStrictProfile(binding.Profile) || in.Plane != "llmplex" || in.Model != binding.Model || !quotaProfileOperation(binding) {
		return nil, nil, quotaStatus(503, "QUOTA_PROFILE_UNSUPPORTED", "operation has no supported exposure contract")
	}
	if binding.Profile == quotaProfileLiveAllowance {
		// The allowance is the gateway-enforced exposure: the saved surface
		// configuration fixes it and the relay renews or closes before it is
		// exhausted. No provider bound authority is involved.
		if in.SessionAllowance <= 0 {
			return nil, nil, quotaStatus(503, "QUOTA_PROFILE_UNSUPPORTED", "the surface does not meter sessions by token allowance")
		}
		for native := range binding.Meters {
			if native != "requests" {
				counts[native] = in.SessionAllowance
			}
		}
		return counts, nil, nil
	}
	if binding.OperationID == "compat.chat-completions" && in.UsageSemantics != modelapi.UsageSemanticsOpenAI {
		return nil, nil, quotaStatus(503, "QUOTA_PROFILE_UNSUPPORTED", "the compatible server has no declared OpenAI usage semantics")
	}
	if len(in.Headers.Values("Anthropic-Beta")) > 0 || len(in.Headers.Values("Last-Event-ID")) > 0 {
		return nil, nil, quotaStatus(400, "QUOTA_PROTOCOL_UNSUPPORTED", "beta features and stream resumption require their own exposure profile")
	}
	query, queryErr := url.ParseQuery(in.RawQuery)
	if queryErr != nil {
		return nil, nil, quotaStatus(400, "QUOTA_QUERY_UNSUPPORTED", "request query is outside the protected profile")
	}
	for key, values := range query {
		if key == "model" && binding.OperationID == "openai.v1.realtime" && len(values) == 1 && values[0] == binding.Model {
			continue // the Realtime connect-time model selector
		}
		if key == "alt" && binding.OperationID == "gemini.interactions.create" && len(values) == 1 && values[0] == "sse" {
			continue // Developer API Interactions streaming selector
		}
		if key == "api-version" && binding.OperationID == "azure.deployments.embeddings" {
			continue // operator-pinned; caller values never cross upstream
		}
		if binding.Profile != quotaProfileGemini || key != "alt" || len(values) != 1 || (values[0] != "json" && values[0] != "sse") {
			return nil, nil, quotaStatus(400, "QUOTA_QUERY_UNSUPPORTED", "partial responses and unknown query options are unsupported")
		}
	}
	// streamGenerateContent is admitted with alt=sse (SSE frames) or without
	// it (one JSON array, buffered and observed whole before delivery).
	if (in.OperationID == "models.generateContent" || in.OperationID == "endpoints.generateContent" || in.OperationID == "gemini.models.generateContent") && query.Get("alt") == "sse" {
		return nil, nil, quotaStatus(400, "QUOTA_QUERY_UNSUPPORTED", "request transport does not match protected native response semantics")
	}
	var output int64
	var err error
	if binding.Profile == quotaProfileGemini {
		output, err = quotaGeminiTextOutput(in.Body)
	} else if binding.Profile == quotaProfileEmbedding {
		output, err = 0, quotaEmbeddingRequest(binding.OperationID, in.Body)
	} else {
		output, err = quotaProviderTextOutput(binding, in.Body)
	}
	if err != nil {
		return nil, nil, quotaStatus(400, "QUOTA_UNBOUNDED_REQUEST", "request is outside the protected text profile")
	}
	if !quotaInterfacePresent(h.bounds) {
		return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "trusted provider exposure authority required")
	}
	proof, err := h.bounds.Bounds(ctx, QuotaBoundsRequest{TenantID: in.Context.TenantID, BindingID: binding.ID, Target: target, Model: in.Model, Body: append([]byte(nil), in.Body...), RequestedOutput: output, At: now})
	if err != nil || !quotaText(proof.EvidenceRef, true) || !proof.ExpiresAt.After(now) || proof.ModelVersion != binding.ModelVersion || proof.ConfigurationDigest != target.ConfigurationDigest {
		return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "provider exposure authority is unavailable or stale")
	}
	copyBounds := map[string]int64{}
	for key, value := range proof.NativeBounds {
		if !quotaText(key, true) || value < 0 {
			return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "provider exposure proof is invalid")
		}
		copyBounds[key] = value
	}
	proof.NativeBounds = copyBounds
	// The proof attests one provider-enforced input ceiling (keyed by the
	// first input subset) and the requested output cap. Every disjoint input
	// subset (uncached, cache read, each mapped cache-write meter) reserves
	// that whole ceiling; every output subset reserves the output cap.
	inputs := quotaProfileInputCounters(binding)
	ceiling, attested := copyBounds["uncached_input_tokens"]
	if !attested {
		ceiling = copyBounds["input_tokens"]
	}
	if ceiling <= 0 || copyBounds["output_tokens"] != output || ((binding.Profile == quotaProfileGemini || binding.Profile == quotaProfileInteraction) && copyBounds["thought_tokens"] != output) {
		return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "provider exposure proof omits a conservative token bound")
	}
	for _, native := range inputs {
		if value, present := copyBounds[native]; present && value != ceiling {
			return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "provider exposure proof omits a conservative token bound")
		}
		copyBounds[native] = ceiling
	}
	for _, native := range quotaProfileOutputCounters(binding.Profile) {
		copyBounds[native] = output
	}
	proof.NativeBounds = copyBounds
	for native := range binding.Meters {
		if native != "requests" {
			counts[native] = copyBounds[native]
		}
	}
	return counts, &proof, nil
}
