package hooks

import (
	"bytes"
	"fmt"
	"strconv"
)

// sseStream is a complete, buffered server-sent event stream. Events whose
// data is unchanged by a transform are re-emitted byte for byte.
type sseStream struct {
	events []sseEvent
}

type sseEvent struct {
	raw   []byte // the event including its terminating blank line
	lines [][]byte
	eol   []byte
	doc   int // index into Content.docs of the event's JSON data, or -1
}

func parseSSE(body []byte) *sseStream {
	s := &sseStream{}
	start := 0
	var lines [][]byte
	eol := []byte("\n")
	at := 0
	for at < len(body) {
		end := bytes.IndexByte(body[at:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += at
		}
		line := body[at:end]
		if bytes.HasSuffix(line, []byte("\r")) {
			line = line[:len(line)-1]
			eol = []byte("\r\n")
		}
		next := end + 1
		if next > len(body) {
			next = len(body)
		}
		if len(line) == 0 {
			s.events = append(s.events, sseEvent{raw: body[start:next], lines: lines, eol: eol, doc: -1})
			lines, start = nil, next
		} else {
			lines = append(lines, line)
		}
		at = next
	}
	if start < len(body) {
		// An unterminated final event is kept verbatim and never inspected
		// as complete data.
		s.events = append(s.events, sseEvent{raw: body[start:], lines: lines, eol: eol, doc: -2})
	}
	return s
}

func (e sseEvent) data() []byte {
	var out []byte
	found := false
	for _, line := range e.lines {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		part := bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" "))
		if found {
			out = append(out, '\n')
		}
		out = append(out, part...)
		found = true
	}
	return out
}

// streamFragment is one text fragment of an assembled stream part.
type assembly struct {
	content *Content
	keys    map[string]int
}

func (a *assembly) add(key, loc, role, kind string, doc int, n *node) {
	s, ok := n.stringValue()
	if !ok {
		return
	}
	i, seen := a.keys[key]
	if !seen {
		i = len(a.content.Parts)
		a.keys[key] = i
		a.content.Parts = append(a.content.Parts, Part{Index: i, Locator: loc, Role: role, Kind: kind})
	}
	a.content.Parts[i].Text += s
	a.content.Parts[i].spans = append(a.content.Parts[i].spans, span{doc, n.start, n.end})
}

func intValue(doc []byte, n *node) (int, bool) {
	if n == nil || n.kind != 'n' {
		return 0, false
	}
	v, err := strconv.Atoi(string(doc[n.start:n.end]))
	return v, err == nil
}

func extractStream(format string, body []byte) (*Content, error) {
	if !StreamBufferable(format) && format != FormatMCPToolCall {
		return nil, uninspectable("streamed %s responses cannot be inspected", format)
	}
	stream := parseSSE(body)
	content := &Content{Format: format, stream: stream}
	a := &assembly{content: content, keys: map[string]int{}}
	skipped := map[string]bool{}
	skip := func(loc, typ string) {
		if !skipped[loc] {
			skipped[loc] = true
			content.Uninspected = append(content.Uninspected, Uninspected{Locator: loc, Type: typ})
		}
	}
	for ei := range stream.events {
		event := &stream.events[ei]
		if event.doc == -2 {
			continue
		}
		data := event.data()
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		root, err := parseJSON(data)
		if err != nil || root.kind != 'o' {
			return nil, uninspectable("stream event is not unambiguous JSON")
		}
		doc := len(content.docs)
		content.docs = append(content.docs, data)
		event.doc = doc
		switch format {
		case FormatMCPToolCall:
			// A streamable HTTP tools/call answer: the JSON-RPC response is
			// one event; notifications carry no tool output.
			if root.get("result") != nil {
				if err := mcpResponse(&collector{content: content, doc: doc}, root); err != nil {
					return nil, uninspectable("%v", err)
				}
			}
		case FormatOpenAIChat:
			choices := root.get("choices")
			if choices == nil || choices.kind != 'a' {
				continue
			}
			for _, choice := range choices.elems {
				ci, ok := intValue(data, choice.get("index"))
				if !ok {
					return nil, uninspectable("stream choice has no index")
				}
				delta := choice.get("delta")
				if err := delta.checkSpelling("content", "tool_calls", "refusal"); err != nil {
					return nil, uninspectable("%v", err)
				}
				base := fmt.Sprintf("choices[%d].delta", ci)
				if c := delta.get("content"); c != nil && c.kind == 'a' {
					skip(base+".content", "content_parts")
				}
				a.add(base+".content", base+".content", "assistant", KindText, doc, delta.get("content"))
				a.add(base+".refusal", base+".refusal", "assistant", KindText, doc, delta.get("refusal"))
				if calls := delta.get("tool_calls"); calls != nil && calls.kind == 'a' {
					for _, call := range calls.elems {
						ti, ok := intValue(data, call.get("index"))
						if !ok {
							return nil, uninspectable("stream tool call has no index")
						}
						loc := fmt.Sprintf("%s.tool_calls[%d].function.arguments", base, ti)
						a.add(loc, loc, "assistant", KindToolArguments, doc, call.get("function").get("arguments"))
					}
				}
			}
		case FormatAnthropicMessages:
			bi, _ := intValue(data, root.get("index"))
			loc := fmt.Sprintf("content[%d]", bi)
			switch typeOf(root) {
			case "content_block_start":
				block := root.get("content_block")
				switch t := typeOf(block); t {
				case "text":
					a.add(loc, loc+".text", "assistant", KindText, doc, block.get("text"))
				case "tool_use", "server_tool_use":
				default:
					skip(loc, t)
				}
			case "content_block_delta":
				delta := root.get("delta")
				switch t := typeOf(delta); t {
				case "text_delta":
					a.add(loc, loc+".text", "assistant", KindText, doc, delta.get("text"))
				case "input_json_delta":
					a.add(loc, loc+".input", "assistant", KindToolArguments, doc, delta.get("partial_json"))
				default:
					skip(loc, t)
				}
			}
		}
	}
	return content, nil
}

// apply re-emits the stream with replacement texts. Each replaced part's
// full text goes into its first fragment and the later fragments become
// empty strings, so the stream stays valid for the protocol and every
// non-text event (usage, finish reasons, ids) is unchanged.
func (s *sseStream) apply(c *Content, replacements map[int]string) []byte {
	perDoc := map[int][]edit{}
	for i, text := range replacements {
		if i < 0 || i >= len(c.Parts) || c.Parts[i].Text == text {
			continue
		}
		for j, sp := range c.Parts[i].spans {
			value := ""
			if j == 0 {
				value = text
			}
			perDoc[sp.doc] = append(perDoc[sp.doc], edit{sp.start, sp.end, value})
		}
	}
	var out bytes.Buffer
	for _, event := range s.events {
		edits, changed := perDoc[event.doc]
		if !changed || event.doc < 0 {
			out.Write(event.raw)
			continue
		}
		data := splice(c.docs[event.doc], edits)
		wroteData := false
		for _, line := range event.lines {
			if bytes.HasPrefix(line, []byte("data:")) {
				if !wroteData {
					for _, dl := range bytes.Split(data, []byte("\n")) {
						out.WriteString("data: ")
						out.Write(dl)
						out.Write(event.eol)
					}
					wroteData = true
				}
				continue
			}
			out.Write(line)
			out.Write(event.eol)
		}
		out.Write(event.eol)
	}
	return out.Bytes()
}
