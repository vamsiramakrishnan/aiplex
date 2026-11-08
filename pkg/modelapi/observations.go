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

func (g *Gateway) observeJSON(ctx context.Context, owner Owner, op Operation, data []byte, terminal bool) error {
	var root map[string]json.RawMessage
	if e := json.Unmarshal(data, &root); e != nil {
		if op.Action == Create {
			return fmt.Errorf("invalid resource creation response")
		}
		return nil
	}
	object := root
	for _, key := range []string{"interaction", "response"} {
		if raw := root[key]; raw != nil {
			_ = json.Unmarshal(raw, &object)
		}
	}
	id := ""
	for _, key := range []string{"id", "name"} {
		if raw := object[key]; raw != nil {
			_ = json.Unmarshal(raw, &id)
			if id != "" {
				break
			}
		}
	}
	if id != "" && op.ResourceKind != "" {
		normalized, e := g.resourceID(op.ResourceKind, id)
		if e != nil {
			return e
		}
		id = normalized
		if op.Action == Create {
			if e = g.options.Ownership.Bind(ctx, owner, Resource{op.ResourceKind, id}); e != nil {
				return e
			}
		}
	} else if op.Action == Create && terminal {
		return fmt.Errorf("creation response omitted resource identity")
	}
	if g.options.Observe == nil {
		return nil
	}
	usage := map[string]json.RawMessage{}
	for _, parent := range []map[string]json.RawMessage{object, root} {
		for _, key := range []string{"usage", "usageMetadata"} {
			if raw := parent[key]; raw != nil {
				_ = json.Unmarshal(raw, &usage)
			}
		}
	}
	if len(usage) == 0 {
		return nil
	}
	counters := map[string]int64{}
	mappings := map[string]string{
		"input_tokens": "input_tokens", "total_input_tokens": "input_tokens", "promptTokenCount": "input_tokens", "prompt_tokens": "input_tokens",
		"output_tokens": "output_tokens", "total_output_tokens": "output_tokens", "candidatesTokenCount": "output_tokens", "completion_tokens": "output_tokens",
		"total_thought_tokens": "thought_tokens", "thoughtsTokenCount": "thought_tokens",
		"total_cached_tokens": "cached_tokens", "cachedContentTokenCount": "cached_tokens",
		"total_tokens": "total_tokens", "totalTokenCount": "total_tokens", "total_tool_use_tokens": "tool_use_tokens",
	}
	for native, target := range mappings {
		if raw := usage[native]; raw != nil {
			var n int64
			if e := json.Unmarshal(raw, &n); e != nil || n < 0 {
				return fmt.Errorf("invalid provider usage counter")
			}
			if prior, ok := counters[target]; ok && prior != n {
				return fmt.Errorf("conflicting provider usage counters")
			}
			counters[target] = n
		}
	}
	for parent, meter := range map[string]string{"input_tokens_details": "cached_tokens", "output_tokens_details": "thought_tokens"} {
		if raw := usage[parent]; raw != nil {
			var detail map[string]json.RawMessage
			if json.Unmarshal(raw, &detail) == nil {
				field := "cached_tokens"
				if meter == "thought_tokens" {
					field = "reasoning_tokens"
				}
				if value := detail[field]; value != nil {
					var n int64
					if json.Unmarshal(value, &n) != nil || n < 0 {
						return fmt.Errorf("invalid provider usage detail")
					}
					counters[meter] = n
				}
			}
		}
	}
	if len(counters) == 0 {
		return nil
	}
	if raw := root["event_type"]; raw != nil {
		var event string
		_ = json.Unmarshal(raw, &event)
		terminal = event == "interaction.completed" || event == "interaction.cancelled" || event == "interaction.failed"
	}
	if raw := root["type"]; raw != nil {
		var event string
		_ = json.Unmarshal(raw, &event)
		terminal = event == "response.completed" || event == "response.cancelled" || event == "response.failed"
	}
	g.options.Observe(ctx, owner, UsageObservation{Source: "upstream", SurfaceID: g.surface.ID, OperationID: op.ID, ResourceID: id, Terminal: terminal, Counters: counters, Cumulative: op.Stateful})
	return nil
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
