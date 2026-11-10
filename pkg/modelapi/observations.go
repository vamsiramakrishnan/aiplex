package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

type jsonObservation struct {
	usage            *UsageObservation
	resource         Resource
	nativeTerminal   bool
	gemini           bool
	candidateCount   int
	foreignCandidate bool
	modelVersion     string
	deferredVersion  bool
	eventType        string
}

func cumulativeUsage(op Operation) bool {
	switch op.ID {
	case "models.generateContent", "models.streamGenerateContent", "endpoints.generateContent", "endpoints.streamGenerateContent", "openai.chat-completions", "response.create", "response.get", "interaction.create", "interaction.get", "anthropic.messages",
		"openai.v1.chat-completions", "openai.v1.responses", "azure.v1.chat-completions", "azure.v1.responses", "azure.deployments.chat-completions", "compat.chat-completions", "bedrock.converse", "bedrock.converse-stream":
		return true
	default:
		return utilityCumulative(op)
	}
}

// anthropicWire reports a Messages-format body on this surface: the native
// Messages API, Vertex rawPredict on the anthropic publisher, or a Bedrock
// InvokeModel body whose model-native payload is the Messages shape.
func (g *Gateway) anthropicWire(op Operation) bool {
	return op.ID == "anthropic.messages" || (g.surface.Publisher == "anthropic" && anthropicOperation(op)) || bedrockInvoke(op)
}

func (g *Gateway) inspectJSON(ctx context.Context, owner Owner, op Operation, data []byte, terminal, includeUsage bool) (jsonObservation, error) {
	if utilityNoUsage(op) {
		includeUsage = false // ops_utility.go: never a usage-bearing response
	}
	if result, handled, err := inspectStateless(g.surface.ID, op, data, includeUsage); handled {
		return result, err
	}
	if bedrockOperation(op) && !(bedrockInvoke(op) && anthropicShaped(data)) {
		return g.inspectBedrockJSON(op, data, includeUsage)
	}
	if geminiGeneration(op) && jsonArray(data) {
		return g.inspectGeminiArray(ctx, owner, op, data, terminal, includeUsage)
	}
	var result jsonObservation
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		if op.Action == Create {
			return result, fmt.Errorf("invalid resource creation response")
		}
		return result, nil
	}
	if includeUsage {
		if raw := root["modelVersion"]; raw != nil {
			if json.Unmarshal(raw, &result.modelVersion) != nil {
				return result, fmt.Errorf("invalid native model version")
			}
		}
	}
	_ = json.Unmarshal(root["type"], &result.eventType)
	object := root
	for _, key := range []string{"interaction", "response", "message"} {
		if raw := root[key]; raw != nil {
			if err := json.Unmarshal(raw, &object); err != nil || object == nil {
				return result, fmt.Errorf("invalid native response object")
			}
		}
	}
	if includeUsage && result.modelVersion == "" {
		if raw := object["model"]; raw != nil {
			if json.Unmarshal(raw, &result.modelVersion) != nil {
				return result, fmt.Errorf("invalid native model identity")
			}
		}
	}
	id := ""
	for _, key := range []string{"id", "name", "responseId"} {
		if raw := object[key]; raw != nil {
			if json.Unmarshal(raw, &id) != nil {
				if includeUsage || op.Action == Create {
					return result, fmt.Errorf("invalid native resource identity")
				}
				continue
			}
			if id != "" {
				break
			}
		}
	}
	if id != "" && op.ResourceKind != "" {
		normalized, err := g.resourceID(op.ResourceKind, id)
		if err != nil {
			return result, err
		}
		id = normalized
		if op.Action == Create {
			if err = g.options.Ownership.Bind(ctx, owner, Resource{op.ResourceKind, id}); err != nil {
				return result, err
			}
		}
	} else if op.Action == Create && terminal {
		return result, fmt.Errorf("creation response omitted resource identity")
	}
	if id != "" {
		kind := op.ResourceKind
		if kind == "" {
			kind = "response"
		}
		result.resource = Resource{kind, id}
	}
	if !includeUsage {
		return result, nil
	}
	usage := map[string]json.RawMessage{}
	for _, parent := range []map[string]json.RawMessage{object, root} {
		for _, key := range []string{"usage", "usageMetadata"} {
			if raw := parent[key]; raw != nil {
				var current map[string]json.RawMessage
				if json.Unmarshal(raw, &current) != nil {
					return result, fmt.Errorf("invalid provider usage object")
				}
				for field, value := range current {
					if prior, exists := usage[field]; exists && !bytes.Equal(prior, value) {
						return result, fmt.Errorf("conflicting native usage objects")
					}
					usage[field] = value
				}
				if key == "usageMetadata" {
					result.gemini = true
				}
			}
		}
	}
	result.nativeTerminal = nativeTerminal(root, object)
	if raw := root["candidates"]; raw != nil {
		var candidates []struct {
			Index int `json:"index"`
		}
		if json.Unmarshal(raw, &candidates) != nil {
			return result, fmt.Errorf("invalid provider candidates")
		}
		result.candidateCount = len(candidates)
		for _, candidate := range candidates {
			if candidate.Index != 0 {
				result.foreignCandidate = true
			}
		}
	}
	if result.eventType == "message_start" && g.anthropicWire(op) {
		// message_start.output_tokens is a placeholder (1-3 tokens), never a
		// usage report; the cumulative message_delta carries the real count.
		delete(usage, "output_tokens")
	}
	if raw := root["predictions"]; raw != nil && len(usage) == 0 && predictOperation(op) {
		return result, embeddingPredictUsage(&result, g.surface.ID, op, raw)
	}
	if len(usage) == 0 {
		return result, nil
	}
	counters := map[string]int64{}
	mappings := map[string]string{
		"input_tokens": "input_tokens", "total_input_tokens": "input_tokens", "totalInputTokens": "input_tokens", "promptTokenCount": "input_tokens", "prompt_tokens": "input_tokens",
		"output_tokens": "output_tokens", "total_output_tokens": "output_tokens", "totalOutputTokens": "output_tokens", "candidatesTokenCount": "output_tokens", "completion_tokens": "output_tokens",
		"cache_read_input_tokens": "cached_tokens", "cache_creation_input_tokens": "cache_write_tokens",
		"total_thought_tokens": "thought_tokens", "totalThoughtTokens": "thought_tokens", "thoughtsTokenCount": "thought_tokens",
		"total_cached_tokens": "cached_tokens", "totalCachedTokens": "cached_tokens", "cachedContentTokenCount": "cached_tokens",
		"total_tokens": "total_tokens", "totalTokens": "total_tokens", "totalTokenCount": "total_tokens", "total_tool_use_tokens": "tool_use_tokens", "totalToolUseTokens": "tool_use_tokens", "toolUsePromptTokenCount": "tool_use_tokens",
	}
	setCounter := func(meter string, raw json.RawMessage) error {
		var n int64
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &n) != nil || n < 0 {
			return fmt.Errorf("invalid provider usage counter")
		}
		if prior, exists := counters[meter]; exists && prior != n {
			return fmt.Errorf("conflicting provider usage counters")
		}
		counters[meter] = n
		return nil
	}
	for native, target := range mappings {
		// A documented-nullable counter (Anthropic message_delta cache and
		// input fields) that is null is unknown, never zero and never invalid.
		if raw := usage[native]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if err := setCounter(target, raw); err != nil {
				return result, err
			}
		}
	}
	for _, key := range []string{"input_tokens_details", "prompt_tokens_details"} {
		if raw := usage[key]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var detail map[string]json.RawMessage
			if json.Unmarshal(raw, &detail) != nil {
				return result, fmt.Errorf("invalid input usage details")
			}
			if n := detail["cache_write_tokens"]; n != nil && !bytes.Equal(bytes.TrimSpace(n), []byte("null")) {
				if err := setCounter("cache_write_tokens", n); err != nil {
					return result, err
				}
			}
			// Responses: cache_write_tokens_details.ttl_30m_tokens and
			// ttl_12h_tokens "do not overlap and sum to cache_write_tokens".
			if n := detail["cache_write_tokens_details"]; n != nil && !bytes.Equal(bytes.TrimSpace(n), []byte("null")) {
				buckets, err := cacheWriteBuckets(n, map[string]string{"ttl_30m_tokens": "cache_write_30m_tokens", "ttl_12h_tokens": "cache_write_12h_tokens"})
				if err != nil {
					return result, err
				}
				for meter, value := range buckets {
					counters[meter] = value
				}
			}
		}
	}
	if raw := usage["server_tool_use"]; raw != nil {
		var calls map[string]json.RawMessage
		if json.Unmarshal(raw, &calls) != nil {
			return result, fmt.Errorf("invalid native server-tool usage")
		}
		// Server-tool requests are billed per request, not per token. Each
		// documented counter is kept as ledger telemetry under its own name;
		// server_tool_calls marks any positive quantity so that every strict
		// token profile fails closed (see serverToolMetering).
		for key, quantity := range calls {
			if bytes.Equal(bytes.TrimSpace(quantity), []byte("null")) {
				continue
			}
			var n int64
			if json.Unmarshal(quantity, &n) != nil || n < 0 {
				return result, fmt.Errorf("invalid native server-tool quantity")
			}
			if meter, known := serverToolCounters[key]; known {
				counters[meter] = n
			}
			if n > 0 {
				counters["server_tool_calls"] = 1
			}
		}
	}
	for parent, meter := range map[string]string{"input_tokens_details": "cached_tokens", "prompt_tokens_details": "cached_tokens", "output_tokens_details": "thought_tokens", "completion_tokens_details": "thought_tokens"} {
		if raw := usage[parent]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var detail map[string]json.RawMessage
			if json.Unmarshal(raw, &detail) != nil {
				return result, fmt.Errorf("invalid provider usage detail")
			}
			fields := []string{"cached_tokens"}
			if meter == "thought_tokens" {
				// OpenAI reports reasoning_tokens, Anthropic thinking_tokens.
				// Both are subsets of output, never additive.
				fields = []string{"reasoning_tokens", "thinking_tokens"}
			}
			for _, field := range fields {
				if value := detail[field]; value != nil && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					if err := setCounter(meter, value); err != nil {
						return result, err
					}
				}
			}
		}
	}
	if g.anthropicWire(op) {
		// Per-TTL cache-write breakdown: ephemeral_5m/1h buckets do not overlap
		// and sum to cache_creation_input_tokens (each documented default 0).
		if raw := usage["cache_creation"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			buckets, err := cacheWriteBuckets(raw, map[string]string{"ephemeral_5m_input_tokens": "cache_write_5m_tokens", "ephemeral_1h_input_tokens": "cache_write_1h_tokens"})
			if err != nil {
				return result, err
			}
			for meter, value := range buckets {
				counters[meter] = value
			}
		}
	}
	if err := reconcileCacheWriteBuckets(counters); err != nil {
		return result, err
	}
	if op.ID == "openai.chat-completions" && g.surface.Provider == nil {
		vertexCompletionComposition(counters)
	}
	if len(counters) == 0 {
		return result, nil
	}
	if root["event_type"] != nil || root["type"] != nil {
		terminal = result.nativeTerminal
	}
	result.usage = &UsageObservation{Source: "upstream", SurfaceID: g.surface.ID, OperationID: op.ID, ResourceID: id, Terminal: terminal, Counters: counters, Cumulative: cumulativeUsage(op) || g.anthropicWire(op), ModelVersion: result.modelVersion}
	return result, nil
}

// vertexCompletionComposition gives the Vertex OpenAI-compatible endpoint's
// counters OpenAI semantics. Unlike OpenAI, Gemini on that endpoint reports
// completion_tokens EXCLUDING reasoning_tokens (prompt + completion +
// reasoning = total). Open models on the same endpoint may follow OpenAI's
// inclusive convention, so the native total decides: an exclusive composition
// folds reasoning into output; an inclusive one stays; anything else leaves
// output and total unknown rather than guessing.
func vertexCompletionComposition(counters map[string]int64) {
	thought, hasThought := counters["thought_tokens"]
	output, hasOutput := counters["output_tokens"]
	if !hasThought || thought == 0 || !hasOutput {
		return
	}
	input, hasInput := counters["input_tokens"]
	total, hasTotal := counters["total_tokens"]
	switch {
	case hasInput && hasTotal && total == input+output+thought:
		counters["output_tokens"] = output + thought
	case hasInput && hasTotal && total == input+output && thought <= output:
	default:
		delete(counters, "output_tokens")
		delete(counters, "total_tokens")
	}
}

// cacheWriteBuckets maps a per-TTL cache-write breakdown object. Every key
// must be a known bucket: an unrecognized bucket would leave the documented
// sum incomplete, so it is rejected rather than ignored. A missing known
// bucket is its documented default 0.
func cacheWriteBuckets(raw json.RawMessage, names map[string]string) (map[string]int64, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, fmt.Errorf("invalid cache-write breakdown")
	}
	out := map[string]int64{}
	for _, meter := range names {
		out[meter] = 0
	}
	for key, value := range fields {
		meter, known := names[key]
		if !known {
			return nil, fmt.Errorf("unrecognized cache-write bucket %q", key)
		}
		var n int64
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		if json.Unmarshal(value, &n) != nil || n < 0 || n > 1<<53 {
			return nil, fmt.Errorf("invalid cache-write bucket")
		}
		out[meter] = n
	}
	return out, nil
}

// cacheWriteFamilies are the per-TTL cache-write meters of each provider.
var cacheWriteFamilies = [][]string{{"cache_write_5m_tokens", "cache_write_1h_tokens"}, {"cache_write_30m_tokens", "cache_write_12h_tokens"}}

// reconcileCacheWriteBuckets derives the scalar from a complete breakdown and
// rejects a breakdown that contradicts a reported scalar.
func reconcileCacheWriteBuckets(counters map[string]int64) error {
	for _, family := range cacheWriteFamilies {
		var sum int64
		present := false
		for _, meter := range family {
			if n, ok := counters[meter]; ok {
				present = true
				sum += n
			}
		}
		if !present {
			continue
		}
		if scalar, ok := counters["cache_write_tokens"]; ok && scalar != sum {
			return fmt.Errorf("cache-write breakdown conflicts with its total")
		}
		counters["cache_write_tokens"] = sum
	}
	return nil
}

func jsonArray(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] == '['
}

// anthropicShaped reports a decoded Messages-API object or stream event: the
// documented shape of an Anthropic model's InvokeModel body on Bedrock.
func anthropicShaped(data []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return false
	}
	switch probe.Type {
	case "message", "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "ping", "error":
		return true
	}
	return false
}

// inspectGeminiArray observes streamGenerateContent without alt=sse: one JSON
// array of GenerateContentResponse. usageMetadata is cumulative within the
// stream, so the last element is authoritative; earlier elements must not
// regress and are never added.
func (g *Gateway) inspectGeminiArray(ctx context.Context, owner Owner, op Operation, data []byte, terminal, includeUsage bool) (jsonObservation, error) {
	var elements []json.RawMessage
	if err := json.Unmarshal(data, &elements); err != nil {
		return jsonObservation{}, nil
	}
	var last jsonObservation
	var cumulative execution.CumulativeUsage
	version := ""
	for i, element := range elements {
		value, err := g.inspectJSON(ctx, owner, op, element, terminal && i == len(elements)-1, includeUsage)
		if err != nil {
			return value, err
		}
		if value.modelVersion != "" {
			if version != "" && version != value.modelVersion {
				return value, fmt.Errorf("native model version changed within stream")
			}
			version = value.modelVersion
		}
		if value.usage != nil {
			if err = cumulative.Observe(value.usage.Counters); err != nil {
				return value, err
			}
		}
		if i < len(elements)-1 && value.nativeTerminal {
			return value, fmt.Errorf("native generation continued after final marker")
		}
		if value.candidateCount > last.candidateCount {
			last.candidateCount = value.candidateCount
		}
		last.foreignCandidate = last.foreignCandidate || value.foreignCandidate
		last.usage, last.nativeTerminal, last.resource, last.gemini = value.usage, value.nativeTerminal, value.resource, value.gemini
	}
	last.modelVersion = version
	if last.usage != nil && last.usage.ModelVersion == "" {
		last.usage.ModelVersion = version
	}
	return last, nil
}

func predictOperation(op Operation) bool {
	return op.ID == "models.predict" || op.ID == "endpoints.predict"
}

// embeddingPredictUsage reads Vertex embedding :predict statistics
// (predictions[].embeddings.statistics.token_count, summed per instance).
// Other :predict models have model-defined bodies: usage stays unknown unless
// every prediction carries the embedding statistics.
func embeddingPredictUsage(result *jsonObservation, surfaceID string, op Operation, raw json.RawMessage) error {
	var predictions []struct {
		Embeddings *struct {
			Statistics *struct {
				TokenCount *json.Number `json:"token_count"`
			} `json:"statistics"`
		} `json:"embeddings"`
	}
	if json.Unmarshal(raw, &predictions) != nil || len(predictions) == 0 {
		return nil
	}
	var sum int64
	for _, p := range predictions {
		if p.Embeddings == nil || p.Embeddings.Statistics == nil || p.Embeddings.Statistics.TokenCount == nil {
			return nil
		}
		n, err := p.Embeddings.Statistics.TokenCount.Int64()
		if err != nil {
			// The statistic is documented as a number; accept whole values only.
			f, ferr := p.Embeddings.Statistics.TokenCount.Float64()
			if ferr != nil || f < 0 || f > 1<<53 || f != float64(int64(f)) {
				return nil
			}
			n = int64(f)
		}
		if n < 0 || n > 1<<53 || sum > 1<<53 {
			return nil
		}
		sum += n
	}
	result.nativeTerminal = true
	result.usage = &UsageObservation{Source: "upstream", SurfaceID: surfaceID, OperationID: op.ID, Terminal: true, Counters: map[string]int64{"input_tokens": sum}, Cumulative: true}
	return nil
}

func nativeTerminal(root, object map[string]json.RawMessage) bool {
	var nativeType string
	_ = json.Unmarshal(root["type"], &nativeType)
	if nativeType == "message_delta" {
		var delta map[string]json.RawMessage
		var reason string
		if json.Unmarshal(root["delta"], &delta) == nil && json.Unmarshal(delta["stop_reason"], &reason) == nil && reason != "" {
			return true
		}
	}
	if nativeType == "message_stop" {
		return true
	}
	if nativeType == "message" {
		var reason string
		if json.Unmarshal(object["stop_reason"], &reason) == nil && reason != "" {
			return true
		}
	}

	for _, key := range []string{"event_type", "type"} {
		var value string
		if json.Unmarshal(root[key], &value) == nil {
			switch value {
			case "interaction.completed", "interaction.cancelled", "interaction.failed", "response.completed", "response.cancelled", "response.failed", "response.incomplete":
				return true
			}
		}
	}
	var status string
	if json.Unmarshal(object["status"], &status) == nil {
		switch status {
		case "completed", "cancelled", "failed", "incomplete":
			return true
		}
	}
	if raw := root["candidates"]; raw != nil {
		var candidates []struct {
			FinishReason string `json:"finishReason"`
		}
		if json.Unmarshal(raw, &candidates) == nil && len(candidates) > 0 {
			for _, candidate := range candidates {
				if candidate.FinishReason == "" || candidate.FinishReason == "FINISH_REASON_UNSPECIFIED" {
					return false
				}
			}
			return true
		}
	}
	if raw := root["choices"]; raw != nil {
		var choices []struct {
			FinishReason *string `json:"finish_reason"`
		}
		if json.Unmarshal(raw, &choices) == nil && len(choices) > 0 {
			for _, choice := range choices {
				if choice.FinishReason == nil || *choice.FinishReason == "" {
					return false
				}
			}
			return true
		}
	}
	return false
}

func (g *Gateway) emitLegacy(ctx context.Context, owner Owner, value *UsageObservation) {
	if value != nil && g.options.Observe != nil {
		g.options.Observe(ctx, owner, *value)
	}
}
func (g *Gateway) observeJSON(ctx context.Context, owner Owner, op Operation, data []byte, terminal bool) error {
	if g.options.Observe == nil {
		_, err := g.inspectJSON(ctx, owner, op, data, terminal, false)
		return err
	}
	result, err := g.inspectJSON(ctx, owner, op, data, terminal, true)
	if err == nil {
		g.emitLegacy(ctx, owner, result.usage)
		return nil
	}
	// Legacy observation is advisory on uncontrolled paths: a response the
	// gateway would deliver without an observer is not rejected because its
	// usage object is malformed. Usage then stays unobserved (unknown).
	_, err = g.inspectJSON(ctx, owner, op, data, terminal, false)
	return err
}

// copySSE observes bounded event frames while preserving their native bytes,
// including comments, event IDs and multi-line data. It never fabricates usage
// when a stream ends without a terminal usage event.
func (g *Gateway) copySSE(w http.ResponseWriter, r *http.Request, reader io.Reader, owner Owner, op Operation) {
	input := bufio.NewReaderSize(reader, 32<<10)
	event := bytes.Buffer{}
	for {
		chunk, e := input.ReadSlice('\n')
		if event.Len()+len(chunk) > 1<<20 {
			return
		}
		_, _ = event.Write(chunk)
		complete := bytes.Equal(chunk, []byte("\n")) || bytes.Equal(chunk, []byte("\r\n"))
		if complete || e == io.EOF {
			if event.Len() > 0 {
				if err := g.observeSSE(r.Context(), owner, op, event.Bytes()); err != nil {
					return
				}
				if _, err := w.Write(event.Bytes()); err != nil {
					return
				}
				if flush, ok := w.(http.Flusher); ok {
					flush.Flush()
				}
				event.Reset()
			}
		}
		if e != nil && !errors.Is(e, bufio.ErrBufferFull) {
			return
		}
		if r.Context().Err() != nil {
			return
		}
	}
}
func (g *Gateway) observeSSE(ctx context.Context, owner Owner, op Operation, event []byte) error {
	data := []byte{}
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if bytes.HasPrefix(line, []byte("data:")) {
			part := bytes.TrimPrefix(line, []byte("data:"))
			part = bytes.TrimPrefix(part, []byte(" "))
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, part...)
		}
	}
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return nil
	}
	return g.observeJSON(ctx, owner, op, data, false)
}
