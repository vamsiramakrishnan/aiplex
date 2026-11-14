package hooks

import "fmt"

// Gemini streamGenerateContent responses, as server-sent events (alt=sse) or
// as the JSON array the method answers without it. Both are a list of
// GenerateContentResponse chunks; the text of a candidate arrives in pieces
// across chunks.
//
// Consecutive text parts of one candidate (and of one kind: answer or
// thought) are one logical text and are inspected as one part, so a value
// split across chunks is still seen whole. Every object the assembler reads
// names the fields it may carry; a field that is not named, such as grounding
// metadata or log probabilities (which repeat the text), is refused.

var (
	geminiChunkKeys = []string{
		"candidates", "usageMetadata", "usage_metadata", "modelVersion", "model_version",
		"responseId", "response_id", "promptFeedback", "prompt_feedback", "createTime", "create_time",
	}
	geminiCandidateKeys = []string{
		"content", "index", "finishReason", "finish_reason", "finishMessage", "finish_message",
		"safetyRatings", "safety_ratings", "citationMetadata", "citation_metadata",
		"avgLogprobs", "avg_logprobs", "tokenCount", "token_count",
	}
	geminiPartKeys = []string{
		"text", "thought", "thoughtSignature", "thought_signature",
		"functionCall", "function_call", "executableCode", "executable_code",
		"codeExecutionResult", "code_execution_result",
		"inlineData", "inline_data", "fileData", "file_data", "videoMetadata", "video_metadata",
	}
)

type geminiStream struct {
	slots  *slotSet
	skip   func(loc, typ string)
	unique int
}

func newGeminiStream(slots *slotSet, skip func(loc, typ string)) *geminiStream {
	return &geminiStream{slots: slots, skip: skip}
}

// chunk handles one GenerateContentResponse.
func (g *geminiStream) chunk(doc int, data []byte, root *node) error {
	if !asObject(root) {
		return errorf("stream chunk must be an object")
	}
	if e := root.get("error"); e != nil {
		// A provider error mid-stream. Its message is inspected like any
		// other text; nothing else may ride along with it.
		if err := onlyKeys(root, "stream error", "error"); err != nil {
			return err
		}
		if err := onlyKeys(e, "stream error", "code", "message", "status"); err != nil {
			return err
		}
		g.unique++
		return g.slots.full(slotKey("e", g.unique), "error.message", "model", KindText, doc, e.get("message"))
	}
	if err := onlyKeys(root, "stream chunk", geminiChunkKeys...); err != nil {
		return err
	}
	if err := root.checkSpelling("candidates", "usageMetadata", "modelVersion", "responseId", "promptFeedback", "createTime"); err != nil {
		return err
	}
	candidates := root.get("candidates")
	switch {
	case candidates == nil || candidates.kind == 'z':
		return nil
	case candidates.kind != 'a':
		return errorf("candidates must be an array")
	}
	for position, candidate := range candidates.elems {
		if err := g.candidate(doc, data, position, candidate); err != nil {
			return err
		}
	}
	return nil
}

func (g *geminiStream) candidate(doc int, data []byte, position int, candidate *node) error {
	if !asObject(candidate) {
		return errorf("candidates[%d] must be an object", position)
	}
	if err := onlyKeys(candidate, "candidate", geminiCandidateKeys...); err != nil {
		return err
	}
	if err := candidate.checkSpelling("content", "finishReason", "finishMessage", "safetyRatings", "citationMetadata", "avgLogprobs", "tokenCount"); err != nil {
		return err
	}
	ci := position
	if n := candidate.get("index"); n != nil {
		v, err := indexOf(data, n, "candidate index")
		if err != nil {
			return err
		}
		ci = v
	}
	content := candidate.get("content")
	if content == nil || content.kind == 'z' {
		return nil
	}
	if err := onlyKeys(content, "candidate content", "parts", "role"); err != nil {
		return err
	}
	if role := content.get("role"); role != nil {
		if s, ok := role.stringValue(); !ok || s != "model" {
			return errorf("candidate content has a role other than model")
		}
	}
	parts := content.get("parts")
	switch {
	case parts == nil || parts.kind == 'z':
		return nil
	case parts.kind != 'a':
		return errorf("candidate parts must be an array")
	}
	for pi, part := range parts.elems {
		if err := g.part(doc, data, ci, pi, part); err != nil {
			return err
		}
	}
	return nil
}

func (g *geminiStream) part(doc int, data []byte, ci, pi int, part *node) error {
	loc := fmt.Sprintf("candidates[%d].content.parts[%d]", ci, pi)
	if !asObject(part) {
		return errorf("%s must be an object", loc)
	}
	if err := onlyKeys(part, loc, geminiPartKeys...); err != nil {
		return err
	}
	if err := part.checkSpelling("text", "thought", "thoughtSignature", "functionCall", "executableCode", "codeExecutionResult", "inlineData", "fileData", "videoMetadata"); err != nil {
		return err
	}
	thought := false
	if t := part.get("thought"); t != nil && t.kind != 'z' {
		if t.kind != 'b' {
			return errorf("%s.thought must be a boolean", loc)
		}
		thought = string(data[t.start:t.end]) == "true"
	}
	if text := part.get("text"); text != nil && text.kind != 'z' {
		// All text parts of a candidate are one text (answer and thought
		// apart), whatever sits between them: the SDKs' text accessors join
		// them, so a value split around a function call must be seen whole.
		key := slotKey("c", ci, ".", thought)
		if err := g.slots.fragment(key, fmt.Sprintf("candidates[%d].text[%t]", ci, thought), "model", KindText, doc, data, text); err != nil {
			return err
		}
	}
	one := func(key string, n *node, kind string) error {
		g.unique++
		return g.slots.full(slotKey("p", g.unique), loc+"."+key, "model", kind, doc, n)
	}
	if fc := part.first("functionCall", "function_call"); fc != nil {
		// Streamed argument fragments (partialArgs, willContinue) are fields
		// outside this list and are refused.
		if err := onlyKeys(fc, loc+".functionCall", "name", "args", "id"); err != nil {
			return err
		}
		if err := g.leaves(doc, loc+".functionCall.args", fc.get("args")); err != nil {
			return err
		}
	}
	if code := part.first("executableCode", "executable_code"); code != nil {
		if err := onlyKeys(code, loc+".executableCode", "language", "code"); err != nil {
			return err
		}
		if err := one("executableCode.code", code.get("code"), KindText); err != nil {
			return err
		}
	}
	if result := part.first("codeExecutionResult", "code_execution_result"); result != nil {
		if err := onlyKeys(result, loc+".codeExecutionResult", "outcome", "output"); err != nil {
			return err
		}
		if err := one("codeExecutionResult.output", result.get("output"), KindText); err != nil {
			return err
		}
	}
	for _, key := range []string{"inlineData", "inline_data", "fileData", "file_data", "videoMetadata", "video_metadata"} {
		if part.get(key) != nil {
			g.skip(loc, snake(key))
		}
	}
	return nil
}

// leaves registers every string leaf of a function call's arguments.
func (g *geminiStream) leaves(doc int, loc string, n *node) error {
	if n == nil {
		return nil
	}
	switch n.kind {
	case 's':
		g.unique++
		return g.slots.full(slotKey("p", g.unique), loc, "model", KindToolArguments, doc, n)
	case 'o':
		for i, k := range n.keys {
			if err := g.leaves(doc, locator(loc, k), n.vals[i]); err != nil {
				return err
			}
		}
	case 'a':
		for i, e := range n.elems {
			if err := g.leaves(doc, index(loc, i), e); err != nil {
				return err
			}
		}
	}
	return nil
}

// geminiChunkArray extracts the JSON-array form of streamGenerateContent.
// Every chunk is in the one document, so spans are offsets into body.
func geminiChunkArray(content *Content, body []byte, root *node) error {
	slots := newSlotSet(content)
	g := newGeminiStream(slots, uninspectedReporter(content))
	for _, chunk := range root.elems {
		if err := g.chunk(0, body, chunk); err != nil {
			return err
		}
	}
	return slots.finish()
}
