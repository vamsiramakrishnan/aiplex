package hooks

import (
	"fmt"
	"strings"
)

// Wire formats with an extractor. Anything else cannot be inspected.
const (
	FormatOpenAIChat        = "openai-chat"
	FormatOpenAIResponses   = "openai-responses"
	FormatAnthropicMessages = "anthropic-messages"
	FormatGemini            = "gemini"
	FormatBedrockConverse   = "bedrock-converse"
	FormatMCPToolCall       = "mcp-tools-call"
	// FormatOpenAIEmbeddings covers embeddings and moderations requests
	// ("input": a string or strings). Their responses carry no text.
	FormatOpenAIEmbeddings = "openai-embeddings"
)

// Inspectable reports whether a format has request and response extractors.
func Inspectable(format string) bool {
	switch format {
	case FormatOpenAIChat, FormatOpenAIResponses, FormatAnthropicMessages, FormatGemini, FormatBedrockConverse, FormatMCPToolCall, FormatOpenAIEmbeddings:
		return true
	}
	return false
}

// StreamBufferable reports whether a streamed response in this format can be
// buffered, inspected and re-emitted as a valid stream.
func StreamBufferable(format string) bool {
	return format == FormatOpenAIChat || format == FormatAnthropicMessages
}

// ExtractRequest returns the inspectable text of a native request body.
func ExtractRequest(format string, body []byte) (*Content, error) {
	root, err := parseJSON(body)
	if err != nil || root.kind != 'o' {
		return nil, uninspectable("request body is not one unambiguous JSON object")
	}
	content := &Content{Format: format, docs: [][]byte{body}}
	c := &collector{content: content}
	switch format {
	case FormatOpenAIChat:
		err = openAIChatRequest(c, root)
	case FormatOpenAIResponses:
		err = openAIResponsesRequest(c, root)
	case FormatAnthropicMessages:
		err = anthropicRequest(c, root)
	case FormatGemini:
		err = geminiRequest(c, root)
	case FormatBedrockConverse:
		err = bedrockConverseRequest(c, root)
	case FormatMCPToolCall:
		err = mcpRequest(c, root)
	case FormatOpenAIEmbeddings:
		err = embeddingsRequest(c, root)
	default:
		return nil, uninspectable("no extractor for format %q", format)
	}
	if err != nil {
		return nil, uninspectable("%v", err)
	}
	return content, nil
}

// ExtractResponse returns the inspectable text of a complete response body.
// mediaType selects JSON or a buffered server-sent event stream.
func ExtractResponse(format, mediaType string, body []byte) (*Content, error) {
	if mediaType == "text/event-stream" {
		return extractStream(format, body)
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return nil, uninspectable("response media type %q cannot be inspected", mediaType)
	}
	root, err := parseJSON(body)
	if err != nil {
		return nil, uninspectable("response body is not unambiguous JSON")
	}
	content := &Content{Format: format, docs: [][]byte{body}}
	c := &collector{content: content}
	switch format {
	case FormatOpenAIChat:
		err = openAIChatResponse(c, root)
	case FormatOpenAIResponses:
		err = openAIResponsesResponse(c, root)
	case FormatAnthropicMessages:
		err = anthropicResponse(c, root, "")
	case FormatGemini:
		if root.kind == 'a' {
			// streamGenerateContent without alt=sse answers a JSON array.
			for i, e := range root.elems {
				if err = geminiResponse(c, e, index("", i)); err != nil {
					break
				}
			}
		} else {
			err = geminiResponse(c, root, "")
		}
	case FormatBedrockConverse:
		err = bedrockConverseResponse(c, root)
	case FormatMCPToolCall:
		err = mcpResponse(c, root)
	case FormatOpenAIEmbeddings:
		// Vectors and category scores: no text to inspect.
	default:
		return nil, uninspectable("no extractor for format %q", format)
	}
	if err != nil {
		return nil, uninspectable("%v", err)
	}
	return content, nil
}

func errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }

func asObject(n *node) bool { return n != nil && n.kind == 'o' }

func roleOf(n *node) string {
	s, _ := n.get("role").stringValue()
	return s
}

func typeOf(n *node) string {
	s, _ := n.get("type").stringValue()
	return s
}

// ── OpenAI Chat Completions ───────────────────────────────────────────────

func openAIChatRequest(c *collector, root *node) error {
	if err := root.checkSpelling("messages"); err != nil {
		return err
	}
	messages := root.get("messages")
	if messages == nil {
		return nil
	}
	if messages.kind != 'a' {
		return errorf("messages must be an array")
	}
	for i, m := range messages.elems {
		if err := openAIChatMessage(c, m, index("messages", i)); err != nil {
			return err
		}
	}
	return nil
}

func openAIChatMessage(c *collector, m *node, loc string) error {
	if !asObject(m) {
		return errorf("%s must be an object", loc)
	}
	if err := m.checkSpelling("content", "tool_calls", "function_call", "refusal", "role"); err != nil {
		return err
	}
	role := roleOf(m)
	kind := KindText
	switch role {
	case "system", "developer":
		kind = KindSystem
	case "tool", "function":
		kind = KindToolResult
	}
	if err := openAIContent(c, m.get("content"), locator(loc, "content"), role, kind); err != nil {
		return err
	}
	c.text(m.get("refusal"), locator(loc, "refusal"), role, KindText)
	if calls := m.get("tool_calls"); calls != nil && calls.kind == 'a' {
		for j, call := range calls.elems {
			fn := call.get("function")
			if err := fn.checkSpelling("arguments"); err != nil {
				return err
			}
			c.text(fn.get("arguments"), locator(locator(index(locator(loc, "tool_calls"), j), "function"), "arguments"), role, KindToolArguments)
		}
	}
	if fn := m.get("function_call"); asObject(fn) {
		c.text(fn.get("arguments"), locator(locator(loc, "function_call"), "arguments"), role, KindToolArguments)
	}
	return nil
}

// openAIContent handles a string or an array of typed content parts.
func openAIContent(c *collector, content *node, loc, role, kind string) error {
	if content == nil {
		return nil
	}
	switch content.kind {
	case 's':
		c.text(content, loc, role, kind)
	case 'a':
		for i, p := range content.elems {
			ploc := index(loc, i)
			if !asObject(p) {
				return errorf("%s must be an object", ploc)
			}
			if err := p.checkSpelling("text", "type"); err != nil {
				return err
			}
			switch t := typeOf(p); t {
			case "text", "input_text", "output_text":
				c.text(p.get("text"), locator(ploc, "text"), role, kind)
			case "refusal":
				c.text(p.get("refusal"), locator(ploc, "refusal"), role, kind)
			default:
				if t == "" {
					t = "unknown"
				}
				c.skip(ploc, t)
			}
		}
	case 'z':
	default:
		return errorf("%s has an unexpected type", loc)
	}
	return nil
}

func openAIChatResponse(c *collector, root *node) error {
	choices := root.get("choices")
	if choices == nil || choices.kind != 'a' {
		return nil
	}
	for i, choice := range choices.elems {
		if m := choice.get("message"); m != nil {
			if err := openAIChatMessage(c, m, locator(index("choices", i), "message")); err != nil {
				return err
			}
		}
	}
	return nil
}

// ── OpenAI Responses ──────────────────────────────────────────────────────

func openAIResponsesRequest(c *collector, root *node) error {
	if err := root.checkSpelling("input", "instructions"); err != nil {
		return err
	}
	c.text(root.get("instructions"), "instructions", "system", KindSystem)
	input := root.get("input")
	if input == nil {
		return nil
	}
	if input.kind == 's' {
		c.text(input, "input", "user", KindText)
		return nil
	}
	if input.kind != 'a' {
		return errorf("input must be a string or an array")
	}
	return responsesItems(c, input, "input")
}

func responsesItems(c *collector, items *node, loc string) error {
	for i, item := range items.elems {
		iloc := index(loc, i)
		if !asObject(item) {
			return errorf("%s must be an object", iloc)
		}
		if err := item.checkSpelling("content", "arguments", "output", "type"); err != nil {
			return err
		}
		role := roleOf(item)
		switch t := typeOf(item); t {
		case "", "message":
			kind := KindText
			if role == "system" || role == "developer" {
				kind = KindSystem
			}
			if err := openAIContent(c, item.get("content"), locator(iloc, "content"), role, kind); err != nil {
				return err
			}
		case "function_call", "custom_tool_call":
			c.text(item.get("arguments"), locator(iloc, "arguments"), "assistant", KindToolArguments)
			c.text(item.get("input"), locator(iloc, "input"), "assistant", KindToolArguments)
		case "function_call_output", "custom_tool_call_output":
			out := item.get("output")
			if out != nil && out.kind == 'a' {
				if err := openAIContent(c, out, locator(iloc, "output"), "tool", KindToolResult); err != nil {
					return err
				}
			} else {
				c.text(out, locator(iloc, "output"), "tool", KindToolResult)
			}
		default:
			c.skip(iloc, t)
		}
	}
	return nil
}

func openAIResponsesResponse(c *collector, root *node) error {
	output := root.get("output")
	if output == nil || output.kind != 'a' {
		return nil
	}
	return responsesItems(c, output, "output")
}

// ── Anthropic Messages ────────────────────────────────────────────────────

func anthropicRequest(c *collector, root *node) error {
	if err := root.checkSpelling("messages", "system"); err != nil {
		return err
	}
	if system := root.get("system"); system != nil {
		if system.kind == 's' {
			c.text(system, "system", "system", KindSystem)
		} else if err := anthropicBlocks(c, system, "system", "system", KindSystem); err != nil {
			return err
		}
	}
	messages := root.get("messages")
	if messages == nil {
		return nil
	}
	if messages.kind != 'a' {
		return errorf("messages must be an array")
	}
	for i, m := range messages.elems {
		loc := index("messages", i)
		if !asObject(m) {
			return errorf("%s must be an object", loc)
		}
		if err := m.checkSpelling("content", "role"); err != nil {
			return err
		}
		content := m.get("content")
		if content != nil && content.kind == 's' {
			c.text(content, locator(loc, "content"), roleOf(m), KindText)
			continue
		}
		if err := anthropicBlocks(c, content, locator(loc, "content"), roleOf(m), KindText); err != nil {
			return err
		}
	}
	return nil
}

func anthropicBlocks(c *collector, blocks *node, loc, role, kind string) error {
	if blocks == nil {
		return nil
	}
	if blocks.kind != 'a' {
		return errorf("%s must be an array of blocks", loc)
	}
	for i, b := range blocks.elems {
		bloc := index(loc, i)
		if !asObject(b) {
			return errorf("%s must be an object", bloc)
		}
		if err := b.checkSpelling("text", "input", "content", "type"); err != nil {
			return err
		}
		switch t := typeOf(b); t {
		case "text":
			c.text(b.get("text"), locator(bloc, "text"), role, kind)
		case "tool_use", "server_tool_use":
			c.leaves(b.get("input"), locator(bloc, "input"), role, KindToolArguments)
		case "tool_result":
			content := b.get("content")
			if content != nil && content.kind == 's' {
				c.text(content, locator(bloc, "content"), role, KindToolResult)
			} else if err := anthropicBlocks(c, content, locator(bloc, "content"), role, KindToolResult); err != nil {
				return err
			}
		default:
			if t == "" {
				t = "unknown"
			}
			c.skip(bloc, t)
		}
	}
	return nil
}

func anthropicResponse(c *collector, root *node, loc string) error {
	if err := root.checkSpelling("content"); err != nil {
		return err
	}
	return anthropicBlocks(c, root.get("content"), locator(loc, "content"), "assistant", KindText)
}

// ── Gemini generateContent ────────────────────────────────────────────────

func geminiRequest(c *collector, root *node) error {
	if err := root.checkSpelling("contents", "systemInstruction", "cachedContent"); err != nil {
		return err
	}
	if system := root.first("systemInstruction", "system_instruction"); system != nil {
		if err := geminiContent(c, system, keyOf(root, system), "system", KindSystem); err != nil {
			return err
		}
	}
	if cached := root.first("cachedContent", "cached_content"); cached != nil {
		c.skip(keyOf(root, cached), "cached_content")
	}
	contents := root.get("contents")
	if contents == nil {
		return nil
	}
	if contents.kind != 'a' {
		return errorf("contents must be an array")
	}
	for i, content := range contents.elems {
		if err := geminiContent(c, content, index("contents", i), roleOf(content), KindText); err != nil {
			return err
		}
	}
	return nil
}

func keyOf(parent, child *node) string {
	for i, v := range parent.vals {
		if v == child {
			return parent.keys[i]
		}
	}
	return ""
}

func geminiContent(c *collector, content *node, loc, role, kind string) error {
	if !asObject(content) {
		return errorf("%s must be an object", loc)
	}
	if err := content.checkSpelling("parts", "role"); err != nil {
		return err
	}
	parts := content.get("parts")
	if parts == nil {
		return nil
	}
	if parts.kind != 'a' {
		return errorf("%s.parts must be an array", loc)
	}
	for i, p := range parts.elems {
		ploc := index(locator(loc, "parts"), i)
		if !asObject(p) {
			return errorf("%s must be an object", ploc)
		}
		if err := p.checkSpelling("text", "functionCall", "functionResponse", "inlineData", "fileData", "executableCode", "codeExecutionResult"); err != nil {
			return err
		}
		matched := false
		if t := p.get("text"); t != nil {
			c.text(t, locator(ploc, "text"), role, kind)
			matched = true
		}
		if fc := p.first("functionCall", "function_call"); fc != nil {
			c.leaves(fc.get("args"), locator(locator(ploc, keyOf(p, fc)), "args"), role, KindToolArguments)
			matched = true
		}
		if fr := p.first("functionResponse", "function_response"); fr != nil {
			c.leaves(fr.get("response"), locator(locator(ploc, keyOf(p, fr)), "response"), role, KindToolResult)
			matched = true
		}
		if code := p.first("executableCode", "executable_code"); code != nil {
			c.text(code.get("code"), locator(locator(ploc, keyOf(p, code)), "code"), role, kind)
			matched = true
		}
		if result := p.first("codeExecutionResult", "code_execution_result"); result != nil {
			c.text(result.get("output"), locator(locator(ploc, keyOf(p, result)), "output"), role, kind)
			matched = true
		}
		for _, key := range []string{"inlineData", "inline_data", "fileData", "file_data", "videoMetadata", "video_metadata"} {
			if p.get(key) != nil {
				c.skip(ploc, snake(key))
				matched = true
			}
		}
		if !matched {
			c.skip(ploc, "unknown")
		}
	}
	return nil
}

func geminiResponse(c *collector, root *node, loc string) error {
	candidates := root.get("candidates")
	if candidates == nil || candidates.kind != 'a' {
		return nil
	}
	for i, candidate := range candidates.elems {
		if content := candidate.get("content"); content != nil {
			if err := geminiContent(c, content, locator(index(locator(loc, "candidates"), i), "content"), "model", KindText); err != nil {
				return err
			}
		}
	}
	return nil
}

// ── Bedrock Converse ──────────────────────────────────────────────────────

func bedrockConverseRequest(c *collector, root *node) error {
	if err := root.checkSpelling("messages", "system"); err != nil {
		return err
	}
	if system := root.get("system"); system != nil {
		if err := bedrockBlocks(c, system, "system", "system", KindSystem); err != nil {
			return err
		}
	}
	messages := root.get("messages")
	if messages == nil {
		return nil
	}
	if messages.kind != 'a' {
		return errorf("messages must be an array")
	}
	for i, m := range messages.elems {
		loc := index("messages", i)
		if err := m.checkSpelling("content"); err != nil {
			return err
		}
		if err := bedrockBlocks(c, m.get("content"), locator(loc, "content"), roleOf(m), KindText); err != nil {
			return err
		}
	}
	return nil
}

func bedrockBlocks(c *collector, blocks *node, loc, role, kind string) error {
	if blocks == nil {
		return nil
	}
	if blocks.kind != 'a' {
		return errorf("%s must be an array", loc)
	}
	for i, b := range blocks.elems {
		bloc := index(loc, i)
		if !asObject(b) {
			return errorf("%s must be an object", bloc)
		}
		if err := b.checkSpelling("text", "toolUse", "toolResult", "json"); err != nil {
			return err
		}
		switch {
		case b.get("text") != nil:
			c.text(b.get("text"), locator(bloc, "text"), role, kind)
		case b.get("toolUse") != nil:
			c.leaves(b.get("toolUse").get("input"), locator(locator(bloc, "toolUse"), "input"), role, KindToolArguments)
		case b.get("toolResult") != nil:
			if err := bedrockBlocks(c, b.get("toolResult").get("content"), locator(locator(bloc, "toolResult"), "content"), role, KindToolResult); err != nil {
				return err
			}
		case b.get("json") != nil:
			c.leaves(b.get("json"), locator(bloc, "json"), role, kind)
		default:
			t := "unknown"
			if len(b.keys) == 1 {
				t = snake(b.keys[0])
			}
			c.skip(bloc, t)
		}
	}
	return nil
}

func bedrockConverseResponse(c *collector, root *node) error {
	message := root.get("output").get("message")
	if message == nil {
		return nil
	}
	return bedrockBlocks(c, message.get("content"), "output.message.content", "assistant", KindText)
}

// ── OpenAI embeddings and moderations ─────────────────────────────────────

func embeddingsRequest(c *collector, root *node) error {
	if err := root.checkSpelling("input"); err != nil {
		return err
	}
	input := root.get("input")
	switch {
	case input == nil:
	case input.kind == 's':
		c.text(input, "input", "user", KindText)
	case input.kind == 'a':
		for i, e := range input.elems {
			switch e.kind {
			case 's':
				c.text(e, index("input", i), "user", KindText)
			case 'n', 'a':
				c.skip(index("input", i), "tokens")
			default:
				// Moderations multimodal input: {"type":"text","text":...}.
				if typeOf(e) == "text" {
					c.text(e.get("text"), locator(index("input", i), "text"), "user", KindText)
				} else {
					c.skip(index("input", i), typeOf(e))
				}
			}
		}
	default:
		return errorf("input must be a string or an array")
	}
	return nil
}

// ── MCP tools/call ────────────────────────────────────────────────────────

func mcpRequest(c *collector, root *node) error {
	params := root.get("params")
	if err := params.checkSpelling("arguments"); err != nil {
		return err
	}
	c.leaves(params.get("arguments"), "params.arguments", "user", KindToolArguments)
	return nil
}

func mcpResponse(c *collector, root *node) error {
	result := root.get("result")
	if result == nil {
		// A JSON-RPC error carries a server-defined message; it is not
		// tool output and is not inspected.
		return nil
	}
	if err := result.checkSpelling("content", "structuredContent"); err != nil {
		return err
	}
	if content := result.get("content"); content != nil && content.kind == 'a' {
		for i, item := range content.elems {
			loc := index("result.content", i)
			switch t := typeOf(item); t {
			case "text":
				c.text(item.get("text"), locator(loc, "text"), "tool", KindToolResult)
			case "resource":
				if text := item.get("resource").get("text"); text != nil {
					c.text(text, locator(locator(loc, "resource"), "text"), "tool", KindToolResult)
				} else {
					c.skip(loc, "resource")
				}
			default:
				if t == "" {
					t = "unknown"
				}
				c.skip(loc, t)
			}
		}
	}
	c.leaves(result.get("structuredContent"), "result.structuredContent", "tool", KindToolResult)
	return nil
}
