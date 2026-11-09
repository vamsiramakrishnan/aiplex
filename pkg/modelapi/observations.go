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
}

func cumulativeUsage(op Operation) bool {
	switch op.ID {
	case "models.generateContent", "models.streamGenerateContent", "endpoints.generateContent", "endpoints.streamGenerateContent", "openai.chat-completions", "response.create", "response.get", "interaction.create", "interaction.get":
		return true
	default:
		return false
	}
}

func (g *Gateway) inspectJSON(ctx context.Context, owner Owner, op Operation, data []byte, terminal, includeUsage bool) (jsonObservation, error) {
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
	object := root
	for _, key := range []string{"interaction", "response"} {
		if raw := root[key]; raw != nil {
			if err := json.Unmarshal(raw, &object); err != nil || object == nil {
				return result, fmt.Errorf("invalid native response object")
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
	if len(usage) == 0 {
		return result, nil
	}
	counters := map[string]int64{}
	mappings := map[string]string{
		"input_tokens": "input_tokens", "total_input_tokens": "input_tokens", "totalInputTokens": "input_tokens", "promptTokenCount": "input_tokens", "prompt_tokens": "input_tokens",
		"output_tokens": "output_tokens", "total_output_tokens": "output_tokens", "totalOutputTokens": "output_tokens", "candidatesTokenCount": "output_tokens", "completion_tokens": "output_tokens",
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
		if raw := usage[native]; raw != nil {
			if err := setCounter(target, raw); err != nil {
				return result, err
			}
		}
	}
	for parent, meter := range map[string]string{"input_tokens_details": "cached_tokens", "prompt_tokens_details": "cached_tokens", "output_tokens_details": "thought_tokens", "completion_tokens_details": "thought_tokens"} {
		if raw := usage[parent]; raw != nil {
			var detail map[string]json.RawMessage
			if json.Unmarshal(raw, &detail) != nil {
				return result, fmt.Errorf("invalid provider usage detail")
			}
			field := "cached_tokens"
			if meter == "thought_tokens" {
				field = "reasoning_tokens"
			}
			if value := detail[field]; value != nil {
				if err := setCounter(meter, value); err != nil {
					return result, err
				}
			}
		}
	}
	if len(counters) == 0 {
		return result, nil
	}
	if root["event_type"] != nil || root["type"] != nil {
		terminal = result.nativeTerminal
	}
	result.usage = &UsageObservation{Source: "upstream", SurfaceID: g.surface.ID, OperationID: op.ID, ResourceID: id, Terminal: terminal, Counters: counters, Cumulative: cumulativeUsage(op), ModelVersion: result.modelVersion}
	return result, nil
}

func nativeTerminal(root, object map[string]json.RawMessage) bool {
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
	result, err := g.inspectJSON(ctx, owner, op, data, terminal, g.options.Observe != nil)
	if err == nil {
		g.emitLegacy(ctx, owner, result.usage)
	}
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
