package hooks

import (
	"bytes"
	"fmt"
)

// OpenAI Responses streams.
//
// The protocol repeats model output: a delta event per fragment, then the
// whole text in response.output_text.done, again in response.content_part.done
// and response.output_item.done, and once more in the output of
// response.completed. Every one of those copies is registered with the slot
// assembler (stream_slots.go), so a transform rewrites all of them.
//
// Every event type is listed here or the stream is refused, and each listed
// event type names the fields it may carry: a field that is not named is a
// place the assembler does not read, so it refuses rather than pass it.

// responsesUninspected are event types whose payload is not text the hooks
// inspect (reasoning, audio, code interpreter and MCP call payloads). The
// non-streamed extractor reports the same output items as uninspected parts,
// and the policy's non_text setting decides. The value is the part type
// reported.
var responsesUninspected = map[string]string{
	"response.reasoning_summary_part.added":        "reasoning",
	"response.reasoning_summary_part.done":         "reasoning",
	"response.reasoning_summary_text.delta":        "reasoning",
	"response.reasoning_summary_text.done":         "reasoning",
	"response.reasoning_text.delta":                "reasoning",
	"response.reasoning_text.done":                 "reasoning",
	"response.code_interpreter_call_code.delta":    "code_interpreter_call",
	"response.code_interpreter_call_code.done":     "code_interpreter_call",
	"response.mcp_call_arguments.delta":            "mcp_call",
	"response.mcp_call_arguments.done":             "mcp_call",
	"response.audio.delta":                         "audio",
	"response.audio.done":                          "audio",
	"response.audio.transcript.delta":              "audio",
	"response.audio.transcript.done":               "audio",
	"response.image_generation_call.partial_image": "image_generation_call",
}

// responsesStatus are event types that carry only a tool call's progress.
var responsesStatus = map[string]bool{
	"response.file_search_call.in_progress":       true,
	"response.file_search_call.searching":         true,
	"response.file_search_call.completed":         true,
	"response.web_search_call.in_progress":        true,
	"response.web_search_call.searching":          true,
	"response.web_search_call.completed":          true,
	"response.code_interpreter_call.in_progress":  true,
	"response.code_interpreter_call.interpreting": true,
	"response.code_interpreter_call.completed":    true,
	"response.image_generation_call.in_progress":  true,
	"response.image_generation_call.generating":   true,
	"response.image_generation_call.completed":    true,
	"response.mcp_call.in_progress":               true,
	"response.mcp_call.completed":                 true,
	"response.mcp_call.failed":                    true,
	"response.mcp_list_tools.in_progress":         true,
	"response.mcp_list_tools.completed":           true,
	"response.mcp_list_tools.failed":              true,
}

// responsesItemTypes are output item types reported as uninspected parts.
var responsesItemTypes = map[string]bool{
	"reasoning": true, "file_search_call": true, "web_search_call": true,
	"code_interpreter_call": true, "image_generation_call": true,
	"computer_call": true, "mcp_call": true, "mcp_list_tools": true,
	"mcp_approval_request": true, "local_shell_call": true,
}

type responsesStream struct {
	slots *slotSet
	skip  func(loc, typ string)
}

// event handles one data event of the stream.
func (r *responsesStream) event(ev *sseEvent, doc int, data []byte, root *node) error {
	typ, ok := root.get("type").stringValue()
	if !ok || typ == "" {
		return errorf("stream event has no type")
	}
	// The event: line, when present, must name the event the data carries:
	// it is a second statement of the type and a place text could hide.
	names := 0
	for _, line := range ev.lines {
		if bytes.HasPrefix(line, []byte("event:")) {
			names++
			if string(bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("event:")), []byte(" "))) != typ {
				return errorf("event name does not match the event type %q", typ)
			}
		}
	}
	if names > 1 {
		return errorf("stream event names its type more than once")
	}
	if label, ok := responsesUninspected[typ]; ok {
		r.skip(typ, label)
		return nil
	}
	if responsesStatus[typ] {
		return onlyKeys(root, typ, "type", "sequence_number", "output_index", "item_id")
	}
	if err := r.slots.scrub(root.get("obfuscation"), doc, "obfuscation"); err != nil {
		return err
	}
	text := func(oi, ci int) (string, string) {
		return slotKey("t", oi, ".", ci), fmt.Sprintf("output[%d].content[%d].text", oi, ci)
	}
	refusal := func(oi, ci int) (string, string) {
		return slotKey("r", oi, ".", ci), fmt.Sprintf("output[%d].content[%d].refusal", oi, ci)
	}
	switch typ {
	case "response.created", "response.in_progress", "response.queued", "response.completed", "response.failed", "response.incomplete":
		if err := onlyKeys(root, typ, "type", "sequence_number", "response"); err != nil {
			return err
		}
		return r.response(doc, data, root.get("response"))

	case "response.output_item.added", "response.output_item.done":
		if err := onlyKeys(root, typ, "type", "sequence_number", "output_index", "item"); err != nil {
			return err
		}
		oi, err := indexOf(data, root.get("output_index"), "output_index")
		if err != nil {
			return err
		}
		return r.item(doc, data, oi, root.get("item"))

	case "response.content_part.added", "response.content_part.done":
		if err := onlyKeys(root, typ, "type", "sequence_number", "item_id", "output_index", "content_index", "part"); err != nil {
			return err
		}
		oi, ci, err := r.indexes(data, root)
		if err != nil {
			return err
		}
		return r.part(doc, data, oi, ci, root.get("part"))

	case "response.output_text.delta":
		if err := onlyKeys(root, typ, "type", "sequence_number", "item_id", "output_index", "content_index", "delta", "logprobs", "obfuscation"); err != nil {
			return err
		}
		if err := emptyList(root.get("logprobs"), typ+" logprobs"); err != nil {
			return err
		}
		oi, ci, err := r.indexes(data, root)
		if err != nil {
			return err
		}
		key, loc := text(oi, ci)
		return r.slots.fragment(key, loc, "assistant", KindText, doc, data, root.get("delta"))

	case "response.output_text.done":
		if err := onlyKeys(root, typ, "type", "sequence_number", "item_id", "output_index", "content_index", "text", "logprobs"); err != nil {
			return err
		}
		if err := emptyList(root.get("logprobs"), typ+" logprobs"); err != nil {
			return err
		}
		oi, ci, err := r.indexes(data, root)
		if err != nil {
			return err
		}
		key, loc := text(oi, ci)
		return r.slots.full(key, loc, "assistant", KindText, doc, root.get("text"))

	case "response.refusal.delta":
		if err := onlyKeys(root, typ, "type", "sequence_number", "item_id", "output_index", "content_index", "delta", "obfuscation"); err != nil {
			return err
		}
		oi, ci, err := r.indexes(data, root)
		if err != nil {
			return err
		}
		key, loc := refusal(oi, ci)
		return r.slots.fragment(key, loc, "assistant", KindText, doc, data, root.get("delta"))

	case "response.refusal.done":
		if err := onlyKeys(root, typ, "type", "sequence_number", "item_id", "output_index", "content_index", "refusal"); err != nil {
			return err
		}
		oi, ci, err := r.indexes(data, root)
		if err != nil {
			return err
		}
		key, loc := refusal(oi, ci)
		return r.slots.full(key, loc, "assistant", KindText, doc, root.get("refusal"))

	case "response.function_call_arguments.delta":
		if err := onlyKeys(root, typ, "type", "sequence_number", "item_id", "output_index", "delta", "obfuscation"); err != nil {
			return err
		}
		oi, err := indexOf(data, root.get("output_index"), "output_index")
		if err != nil {
			return err
		}
		return r.slots.fragment(slotKey("a", oi), fmt.Sprintf("output[%d].arguments", oi), "assistant", KindToolArguments, doc, data, root.get("delta"))

	case "response.function_call_arguments.done":
		if err := onlyKeys(root, typ, "type", "sequence_number", "item_id", "output_index", "arguments", "name"); err != nil {
			return err
		}
		oi, err := indexOf(data, root.get("output_index"), "output_index")
		if err != nil {
			return err
		}
		return r.slots.full(slotKey("a", oi), fmt.Sprintf("output[%d].arguments", oi), "assistant", KindToolArguments, doc, root.get("arguments"))

	case "response.custom_tool_call_input.delta":
		if err := onlyKeys(root, typ, "type", "sequence_number", "item_id", "output_index", "delta", "obfuscation"); err != nil {
			return err
		}
		oi, err := indexOf(data, root.get("output_index"), "output_index")
		if err != nil {
			return err
		}
		return r.slots.fragment(slotKey("i", oi), fmt.Sprintf("output[%d].input", oi), "assistant", KindToolArguments, doc, data, root.get("delta"))

	case "response.custom_tool_call_input.done":
		if err := onlyKeys(root, typ, "type", "sequence_number", "item_id", "output_index", "input"); err != nil {
			return err
		}
		oi, err := indexOf(data, root.get("output_index"), "output_index")
		if err != nil {
			return err
		}
		return r.slots.full(slotKey("i", oi), fmt.Sprintf("output[%d].input", oi), "assistant", KindToolArguments, doc, root.get("input"))

	case "error":
		// A provider error mid-stream. Its message is inspected like any
		// other text: it must not become a way to carry content around a hook.
		if err := onlyKeys(root, typ, "type", "sequence_number", "code", "message", "param"); err != nil {
			return err
		}
		return r.slots.full("error", "error.message", "assistant", KindText, doc, root.get("message"))
	}
	// Includes response.output_text.annotation.added: an annotation indexes
	// into the text, which a transform changes.
	return errorf("stream event type %q cannot be inspected", typ)
}

func (r *responsesStream) indexes(data []byte, root *node) (int, int, error) {
	oi, err := indexOf(data, root.get("output_index"), "output_index")
	if err != nil {
		return 0, 0, err
	}
	ci, err := indexOf(data, root.get("content_index"), "content_index")
	return oi, ci, err
}

// response handles the response object of a lifecycle event: the output
// items it holds are complete copies of everything streamed so far.
func (r *responsesStream) response(doc int, data []byte, resp *node) error {
	if !asObject(resp) {
		return errorf("response must be an object")
	}
	if err := r.envelope(doc, resp); err != nil {
		return err
	}
	output := resp.get("output")
	switch {
	case output == nil || output.kind == 'z':
		return nil
	case output.kind != 'a':
		return errorf("response output must be an array")
	}
	for i, item := range output.elems {
		if err := r.item(doc, data, i, item); err != nil {
			return err
		}
	}
	return nil
}

// item handles an output item, streamed or complete.
func (r *responsesStream) item(doc int, data []byte, oi int, item *node) error {
	if !asObject(item) {
		return errorf("output[%d] must be an object", oi)
	}
	typ, _ := item.get("type").stringValue()
	switch {
	case typ == "message":
		if err := onlyKeys(item, "message item", "id", "type", "role", "status", "content", "phase"); err != nil {
			return err
		}
		if role, ok := item.get("role").stringValue(); item.get("role") != nil && (!ok || role != "assistant") {
			return errorf("output[%d] is a message from a role other than assistant", oi)
		}
		content := item.get("content")
		switch {
		case content == nil || content.kind == 'z':
			return nil
		case content.kind != 'a':
			return errorf("output[%d].content must be an array", oi)
		}
		for ci, part := range content.elems {
			if err := r.part(doc, data, oi, ci, part); err != nil {
				return err
			}
		}
		return nil
	case typ == "function_call":
		if err := onlyKeys(item, "function_call item", "id", "type", "call_id", "name", "arguments", "status", "namespace"); err != nil {
			return err
		}
		return r.slots.full(slotKey("a", oi), fmt.Sprintf("output[%d].arguments", oi), "assistant", KindToolArguments, doc, item.get("arguments"))
	case typ == "custom_tool_call":
		if err := onlyKeys(item, "custom_tool_call item", "id", "type", "call_id", "name", "input", "status", "namespace"); err != nil {
			return err
		}
		return r.slots.full(slotKey("i", oi), fmt.Sprintf("output[%d].input", oi), "assistant", KindToolArguments, doc, item.get("input"))
	case responsesItemTypes[typ]:
		r.skip(fmt.Sprintf("output[%d]", oi), typ)
		return nil
	}
	return errorf("output item type %q cannot be inspected", typ)
}

// part handles a content part of a message item.
func (r *responsesStream) part(doc int, data []byte, oi, ci int, part *node) error {
	if !asObject(part) {
		return errorf("output[%d].content[%d] must be an object", oi, ci)
	}
	typ, _ := part.get("type").stringValue()
	switch typ {
	case "output_text":
		if err := onlyKeys(part, "output_text part", "type", "text", "annotations", "logprobs"); err != nil {
			return err
		}
		loc := fmt.Sprintf("output[%d].content[%d]", oi, ci)
		if err := emptyList(part.get("annotations"), loc+".annotations"); err != nil {
			return err
		}
		if err := emptyList(part.get("logprobs"), loc+".logprobs"); err != nil {
			return err
		}
		return r.slots.full(slotKey("t", oi, ".", ci), loc+".text", "assistant", KindText, doc, part.get("text"))
	case "refusal":
		if err := onlyKeys(part, "refusal part", "type", "refusal"); err != nil {
			return err
		}
		return r.slots.full(slotKey("r", oi, ".", ci), fmt.Sprintf("output[%d].content[%d].refusal", oi, ci), "assistant", KindText, doc, part.get("refusal"))
	case "reasoning_text", "output_audio":
		r.skip(fmt.Sprintf("output[%d].content[%d]", oi, ci), typ)
		return nil
	}
	return errorf("content part type %q cannot be inspected", typ)
}

// responsesEchoed are the response fields that repeat the caller's own request
// (instructions, tool and format configuration, metadata, identifiers the
// caller sent). The non-streamed extractor does not read them either: they
// are relayed as sent, not inspected as model output and not reported as
// uninspected. Request hooks already cover them.
var responsesEchoed = map[string]bool{
	"instructions": true, "tools": true, "tool_choice": true, "text": true,
	"reasoning": true, "prompt": true, "conversation": true, "metadata": true,
	"user": true, "safety_identifier": true, "prompt_cache_key": true,
	"billing": true, "input": true,
}

// responsesScalars are provider-defined identifiers, enumerations and
// numbers, with the JSON type each has. Such a field is relayed as sent when
// it has that type (or is null); with any other type it is treated like an
// unknown field. No list of values is kept: a provider adds values.
var responsesScalars = map[string]byte{
	"id": 's', "object": 's', "status": 's', "model": 's', "previous_response_id": 's',
	"service_tier": 's', "truncation": 's',
	"created_at": 'n', "completed_at": 'n', "max_output_tokens": 'n', "max_tool_calls": 'n',
	"temperature": 'n', "top_p": 'n', "top_logprobs": 'n',
	"parallel_tool_calls": 'b', "store": 'b', "background": 'b',
}

// Bounds on the walk of a field the assembler has no schema for.
const (
	maxWalkDepth  = 8
	maxWalkLeaves = 2000
)

// envelope handles every field of a response object except the output (the
// caller does that). What the provider or model produces is inspected:
// error and incomplete_details, and any field not listed here, whose string
// leaves become parts that a transform rewrites in place. Numbers, booleans
// and null pass. A walk beyond the bounds is refused.
func (r *responsesStream) envelope(doc int, resp *node) error {
	for i, key := range resp.keys {
		v := resp.vals[i]
		switch {
		case key == "output", responsesEchoed[key]:
		case responsesScalars[key] != 0 && (v.kind == 'z' || v.kind == responsesScalars[key]):
			// A scalar of the expected type. Strings here are provider
			// identifiers and enumerations.
		default:
			leaves := 0
			if err := r.walk(doc, "response."+key, v, 0, &leaves); err != nil {
				return err
			}
		}
	}
	return nil
}

// walk registers every string leaf under n as a text copy keyed by its path,
// so the same field repeated in later events is inspected once.
func (r *responsesStream) walk(doc int, loc string, n *node, depth int, leaves *int) error {
	if depth > maxWalkDepth {
		return errorf("%s is nested too deeply to inspect", loc)
	}
	switch n.kind {
	case 's':
		if *leaves++; *leaves > maxWalkLeaves {
			return errorf("%s holds too many strings to inspect", loc)
		}
		return r.slots.full("leaf:"+loc, loc, "assistant", KindText, doc, n)
	case 'o':
		for i, k := range n.keys {
			if err := r.walk(doc, locator(loc, k), n.vals[i], depth+1, leaves); err != nil {
				return err
			}
		}
	case 'a':
		for i, e := range n.elems {
			if err := r.walk(doc, index(loc, i), e, depth+1, leaves); err != nil {
				return err
			}
		}
	}
	return nil
}
