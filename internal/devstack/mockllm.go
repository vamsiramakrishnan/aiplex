package devstack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MockModels is a small local model provider. One handler speaks three wire
// formats so one listener can back several surfaces:
//
//   - OpenAI:    POST /v1/chat/completions (JSON, or SSE with a final usage
//     chunk when stream_options.include_usage is true), POST /v1/responses
//     (JSON or SSE, usage on response.completed), POST /v1/embeddings,
//     GET /v1/models
//   - Anthropic: POST /v1/messages (JSON or SSE), POST /v1/messages/count_tokens
//   - Gemini:    POST /v1beta/models/{model}:generateContent,
//     :streamGenerateContent?alt=sse, :embedContent, :countTokens
//
// Model names select test behaviour: a name containing "ratelimit" answers
// the provider's own 429 shape with Retry-After, "hang" never answers until
// the client goes away, and "slow" streams one chunk every 50ms for 2s.
// A name containing "fault" injects failures (see MockFault).
// Replies are deterministic and include token usage computed from the input.
type MockModels struct {
	mu       sync.Mutex
	requests []MockRequest
	// HangLimit bounds how long a "hang" model holds a request (default 10m).
	HangLimit time.Duration
	// Name labels this mock in replies ("" for the default mock), so a test
	// can tell which of two mock backends answered.
	Name   string
	faults map[string]int
}

// ResetFaults forgets how many times each fault model has failed.
func (m *MockModels) ResetFaults() {
	m.mu.Lock()
	m.faults = nil
	m.mu.Unlock()
}

// MockFault is a failure injected by the model name. The name contains
// "fault" and dash-separated options:
//
//	s<status>   answer with this status (default 503)
//	n<count>    fail only the first count requests for this model name and
//	            path, then answer normally (default: always fail)
//	ra<sec>     send Retry-After: <sec>
//	d<ms>       wait before answering
//	dropbefore  close the connection without any response
//	dropafter   answer 200 text/event-stream, send one event, then close
//
// For example "gpt-fault-s503-n2-ra1" fails twice with 503 and Retry-After 1.
type MockFault struct {
	Status     int
	Count      int
	RetryAfter string
	Delay      time.Duration
	DropBefore bool
	DropAfter  bool
}

// ParseMockFault reads the fault options of a model name.
func ParseMockFault(model string) (MockFault, bool) {
	if !strings.Contains(model, "fault") {
		return MockFault{}, false
	}
	f := MockFault{Status: 503}
	for _, part := range strings.Split(model, "-") {
		switch {
		case part == "dropbefore":
			f.DropBefore = true
		case part == "dropafter":
			f.DropAfter = true
		case strings.HasPrefix(part, "ra") && len(part) > 2:
			f.RetryAfter = part[2:]
		case strings.HasPrefix(part, "s") && len(part) == 4:
			if n, err := strconv.Atoi(part[1:]); err == nil {
				f.Status = n
			}
		case strings.HasPrefix(part, "n") && len(part) > 1:
			if n, err := strconv.Atoi(part[1:]); err == nil {
				f.Count = n
			}
		case strings.HasPrefix(part, "d") && len(part) > 1:
			if n, err := strconv.Atoi(part[1:]); err == nil {
				f.Delay = time.Duration(n) * time.Millisecond
			}
		}
	}
	return f, true
}

// fault applies an injected failure; it returns true when it answered.
func (m *MockModels) fault(w http.ResponseWriter, r *http.Request, model string) bool {
	f, ok := ParseMockFault(model)
	if !ok {
		return false
	}
	key := r.URL.Path + "\x00" + model
	m.mu.Lock()
	if m.faults == nil {
		m.faults = map[string]int{}
	}
	m.faults[key]++
	seen := m.faults[key]
	m.mu.Unlock()
	if f.Count > 0 && seen > f.Count {
		return false
	}
	if f.Delay > 0 {
		select {
		case <-r.Context().Done():
			return true
		case <-time.After(f.Delay):
		}
	}
	switch {
	case f.DropBefore:
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return true
	case f.DropAfter:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "data: {\"id\":\"mock-partial\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial \"}}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return true
	}
	if f.RetryAfter != "" {
		w.Header().Set("Retry-After", f.RetryAfter)
	}
	body := map[string]any{"error": map[string]any{"message": fmt.Sprintf("mock fault %d", f.Status), "type": "mock_fault"}}
	if r.URL.Path == "/v1/messages" {
		body = map[string]any{"type": "error", "error": map[string]any{"type": "overloaded_error", "message": fmt.Sprintf("mock fault %d", f.Status)}}
	}
	writeJSON(w, f.Status, body)
	return true
}

// MockRequest is what the mock recorded about one upstream request. Header
// values that carry credentials are recorded so tests can prove which
// credential reached the provider.
type MockRequest struct {
	Method, Path, Query string
	Authorization       string
	APIKey              string // x-api-key
	GoogAPIKey          string // x-goog-api-key
	Traceparent         string // W3C trace context AIPlex propagated
	IdempotencyKey      string // Idempotency-Key, if any reached the provider
	Model               string
	Stream              bool
	BodyBytes           int
	// Body is the request body as received (first 64 KiB), so tests can
	// prove what reached the provider (for example a masked prompt).
	Body string
}

// Requests returns a copy of the recorded requests.
func (m *MockModels) Requests() []MockRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MockRequest(nil), m.requests...)
}

func (m *MockModels) record(r *http.Request, model string, stream bool, body []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := body
	if len(kept) > 64<<10 {
		kept = kept[:64<<10]
	}
	m.requests = append(m.requests, MockRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Authorization: r.Header.Get("Authorization"), APIKey: r.Header.Get("X-Api-Key"), GoogAPIKey: r.Header.Get("X-Goog-Api-Key"), Traceparent: r.Header.Get("Traceparent"), IdempotencyKey: r.Header.Get("Idempotency-Key"), Model: model, Stream: stream, BodyBytes: len(body), Body: string(kept)})
}

// MockReply is the deterministic assistant text for a prompt.
func (m *MockModels) reply(prompt string) string {
	if m.Name != "" {
		return "[" + m.Name + "] " + MockReply(prompt)
	}
	return MockReply(prompt)
}

func MockReply(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if len(prompt) > 80 {
		prompt = prompt[:80] + "..."
	}
	if prompt == "" {
		return "Hello from the AIPlex mock model."
	}
	return "Hello from the AIPlex mock model. You said: " + prompt
}

func countTokens(s string) int {
	n := len(strings.Fields(s))
	if n == 0 {
		return 1
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *MockModels) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		http.Error(w, "read error", 400)
		return
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/v1/models":
		writeJSON(w, 200, map[string]any{"object": "list", "data": []map[string]any{{"id": "mock-gpt", "object": "model", "owned_by": "aiplex-mock"}}})
	case r.Method == http.MethodPost && path == "/v1/chat/completions":
		m.openAIChat(w, r, body)
	case r.Method == http.MethodPost && path == "/v1/responses":
		m.openAIResponses(w, r, body)
	case r.Method == http.MethodPost && path == "/v1/embeddings":
		m.openAIEmbeddings(w, r, body)
	case r.Method == http.MethodPost && path == "/v1/messages":
		m.anthropicMessages(w, r, body)
	case r.Method == http.MethodPost && path == "/v1/messages/count_tokens":
		var req anthropicRequest
		_ = json.Unmarshal(body, &req)
		m.record(r, req.Model, false, body)
		writeJSON(w, 200, map[string]any{"input_tokens": countTokens(req.text())})
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/v1beta/models/"):
		m.gemini(w, r, body)
	default:
		writeJSON(w, 404, map[string]any{"error": map[string]any{"message": "mock provider has no route " + r.Method + " " + path, "type": "invalid_request_error"}})
	}
}

// behaviour applies the name-selected test behaviours. It returns true when
// the response has been written.
func (m *MockModels) behaviour(w http.ResponseWriter, r *http.Request, model string, rateLimited func()) bool {
	if m.fault(w, r, model) {
		return true
	}
	switch {
	case strings.Contains(model, "ratelimit"):
		w.Header().Set("Retry-After", "7")
		rateLimited()
		return true
	case strings.Contains(model, "hang"):
		limit := m.HangLimit
		if limit == 0 {
			limit = 10 * time.Minute
		}
		select {
		case <-r.Context().Done():
		case <-time.After(limit):
			writeJSON(w, 504, map[string]any{"error": map[string]any{"message": "mock hang limit reached"}})
		}
		return true
	}
	return false
}

// ── OpenAI ─────────────────────────────────────────────────────────────

type openAIChatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Stream        bool `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

func (m *MockModels) openAIChat(w http.ResponseWriter, r *http.Request, body []byte) {
	var req openAIChatRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"message": "invalid request: model and messages are required", "type": "invalid_request_error"}})
		return
	}
	m.record(r, req.Model, req.Stream, body)
	if m.behaviour(w, r, req.Model, func() {
		writeJSON(w, 429, map[string]any{"error": map[string]any{"message": "Rate limit reached for mock model", "type": "rate_limit_exceeded", "code": "rate_limit_exceeded"}})
	}) {
		return
	}
	var prompt strings.Builder
	for _, msg := range req.Messages {
		prompt.WriteString(contentText(msg.Content))
		prompt.WriteString(" ")
	}
	reply := m.reply(lastLine(prompt.String()))
	in, out := countTokens(prompt.String()), countTokens(reply)
	usage := map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out}
	id := fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	if !req.Stream {
		writeJSON(w, 200, map[string]any{"id": id, "object": "chat.completion", "created": created, "model": req.Model,
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": "stop"}},
			"usage":   usage})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	send := func(v any) bool {
		b, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model, "choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	words := strings.SplitAfter(reply, " ")
	slow := strings.Contains(req.Model, "slow")
	if slow {
		words = make([]string, 40)
		for i := range words {
			words[i] = "tick "
		}
	}
	send(chunk(map[string]any{"role": "assistant", "content": ""}, nil))
	for _, word := range words {
		if slow {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
		if !send(chunk(map[string]any{"content": word}, nil)) {
			return
		}
	}
	send(chunk(map[string]any{}, "stop"))
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
		send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model, "choices": []any{}, "usage": usage})
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// ── OpenAI Responses ───────────────────────────────────────────────────

type openAIResponsesRequest struct {
	Model        string          `json:"model"`
	Input        json.RawMessage `json:"input"`
	Instructions string          `json:"instructions"`
	Stream       bool            `json:"stream"`
}

// text flattens the Responses input: a string, or a list of strings and
// message items whose content is a string or a list of typed parts.
func (q openAIResponsesRequest) text() string {
	var one string
	if json.Unmarshal(q.Input, &one) == nil {
		return q.Instructions + " " + one
	}
	var items []struct {
		Content json.RawMessage `json:"content"`
	}
	var b strings.Builder
	b.WriteString(q.Instructions)
	b.WriteString(" ")
	if json.Unmarshal(q.Input, &items) == nil {
		for _, it := range items {
			b.WriteString(contentText(it.Content))
			b.WriteString(" ")
		}
	}
	return b.String()
}

func (m *MockModels) openAIResponses(w http.ResponseWriter, r *http.Request, body []byte) {
	var req openAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" || len(req.Input) == 0 {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"message": "invalid request: model and input are required", "type": "invalid_request_error"}})
		return
	}
	m.record(r, req.Model, req.Stream, body)
	if m.behaviour(w, r, req.Model, func() {
		writeJSON(w, 429, map[string]any{"error": map[string]any{"message": "Rate limit reached for mock model", "type": "rate_limit_exceeded", "code": "rate_limit_exceeded"}})
	}) {
		return
	}
	prompt := req.text()
	reply := m.reply(strings.TrimSpace(prompt))
	in, out := countTokens(prompt), countTokens(reply)
	usage := map[string]any{"input_tokens": in, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens": out, "output_tokens_details": map[string]any{"reasoning_tokens": 0}, "total_tokens": in + out}
	now := time.Now()
	id := fmt.Sprintf("resp_mock_%d", now.UnixNano())
	msgID := fmt.Sprintf("msg_mock_%d", now.UnixNano())
	message := func(status string, text string) map[string]any {
		content := []any{}
		if status == "completed" {
			content = append(content, map[string]any{"type": "output_text", "text": text, "annotations": []any{}})
		}
		return map[string]any{"id": msgID, "type": "message", "role": "assistant", "status": status, "content": content}
	}
	response := func(status string, output []any, u any) map[string]any {
		return map[string]any{"id": id, "object": "response", "created_at": now.Unix(), "status": status, "error": nil, "incomplete_details": nil, "model": req.Model, "output": output, "usage": u}
	}
	if !req.Stream {
		writeJSON(w, 200, response("completed", []any{message("completed", reply)}, usage))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	seq := 0
	event := func(name string, v map[string]any) {
		v["type"] = name
		v["sequence_number"] = seq
		seq++
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	event("response.created", map[string]any{"response": response("in_progress", []any{}, nil)})
	event("response.in_progress", map[string]any{"response": response("in_progress", []any{}, nil)})
	event("response.output_item.added", map[string]any{"output_index": 0, "item": message("in_progress", "")})
	event("response.content_part.added", map[string]any{"item_id": msgID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
	for _, word := range strings.SplitAfter(reply, " ") {
		event("response.output_text.delta", map[string]any{"item_id": msgID, "output_index": 0, "content_index": 0, "delta": word})
	}
	event("response.output_text.done", map[string]any{"item_id": msgID, "output_index": 0, "content_index": 0, "text": reply})
	event("response.content_part.done", map[string]any{"item_id": msgID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": reply, "annotations": []any{}}})
	event("response.output_item.done", map[string]any{"output_index": 0, "item": message("completed", reply)})
	event("response.completed", map[string]any{"response": response("completed", []any{message("completed", reply)}, usage)})
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	return s
}

func (m *MockModels) openAIEmbeddings(w http.ResponseWriter, r *http.Request, body []byte) {
	var req struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"message": "invalid request", "type": "invalid_request_error"}})
		return
	}
	m.record(r, req.Model, false, body)
	var inputs []string
	var one string
	if json.Unmarshal(req.Input, &one) == nil {
		inputs = []string{one}
	} else {
		_ = json.Unmarshal(req.Input, &inputs)
	}
	data := []map[string]any{}
	tokens := 0
	for i, in := range inputs {
		tokens += countTokens(in)
		data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": []float64{0.1, 0.2, 0.3, float64(len(in)) / 100}})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data, "model": req.Model, "usage": map[string]any{"prompt_tokens": tokens, "total_tokens": tokens}})
}

// ── Anthropic ──────────────────────────────────────────────────────────

type anthropicRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Stream    bool   `json:"stream"`
	Messages  []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

func (a anthropicRequest) text() string {
	var b strings.Builder
	for _, msg := range a.Messages {
		b.WriteString(contentText(msg.Content))
		b.WriteString(" ")
	}
	return b.String()
}

func (m *MockModels) anthropicMessages(w http.ResponseWriter, r *http.Request, body []byte) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" || req.MaxTokens <= 0 {
		writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": "model, max_tokens and messages are required"}})
		return
	}
	m.record(r, req.Model, req.Stream, body)
	if m.behaviour(w, r, req.Model, func() {
		writeJSON(w, 429, map[string]any{"type": "error", "error": map[string]any{"type": "rate_limit_error", "message": "Rate limit reached for mock model"}})
	}) {
		return
	}
	prompt := req.text()
	reply := m.reply(prompt)
	in, out := countTokens(prompt), countTokens(reply)
	id := fmt.Sprintf("msg_mock_%d", time.Now().UnixNano())
	if !req.Stream {
		writeJSON(w, 200, map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model,
			"content":     []map[string]any{{"type": "text", "text": reply}},
			"stop_reason": "end_turn", "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": in, "output_tokens": out, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	event := func(name string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	event("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": in, "output_tokens": 1, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}}})
	event("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	for _, word := range strings.SplitAfter(reply, " ") {
		event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": word}})
	}
	event("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": out}})
	event("message_stop", map[string]any{"type": "message_stop"})
}

// ── Gemini (Developer API shape) ───────────────────────────────────────

func (m *MockModels) gemini(w http.ResponseWriter, r *http.Request, body []byte) {
	target := strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
	model, method, ok := strings.Cut(target, ":")
	if !ok {
		writeJSON(w, 404, map[string]any{"error": map[string]any{"code": 404, "message": "unknown method", "status": "NOT_FOUND"}})
		return
	}
	var req struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		Content *struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"code": 400, "message": "Invalid JSON payload", "status": "INVALID_ARGUMENT"}})
		return
	}
	stream := method == "streamGenerateContent"
	m.record(r, model, stream, body)
	if m.behaviour(w, r, model, func() {
		writeJSON(w, 429, map[string]any{"error": map[string]any{"code": 429, "message": "Resource has been exhausted (mock).", "status": "RESOURCE_EXHAUSTED"}})
	}) {
		return
	}
	var prompt strings.Builder
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			prompt.WriteString(p.Text)
			prompt.WriteString(" ")
		}
	}
	if req.Content != nil {
		for _, p := range req.Content.Parts {
			prompt.WriteString(p.Text)
		}
	}
	in := countTokens(prompt.String())
	switch method {
	case "countTokens":
		writeJSON(w, 200, map[string]any{"totalTokens": in})
		return
	case "embedContent":
		writeJSON(w, 200, map[string]any{"embedding": map[string]any{"values": []float64{0.1, 0.2, 0.3}}})
		return
	case "generateContent", "streamGenerateContent":
	default:
		writeJSON(w, 404, map[string]any{"error": map[string]any{"code": 404, "message": "method not implemented by mock", "status": "NOT_FOUND"}})
		return
	}
	reply := m.reply(prompt.String())
	out := countTokens(reply)
	candidate := func(text string, finish string) map[string]any {
		c := map[string]any{"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": text}}}, "index": 0}
		if finish != "" {
			c["finishReason"] = finish
		}
		return c
	}
	usage := map[string]any{"promptTokenCount": in, "candidatesTokenCount": out, "totalTokenCount": in + out}
	if !stream {
		writeJSON(w, 200, map[string]any{"candidates": []any{candidate(reply, "STOP")}, "usageMetadata": usage, "modelVersion": model})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	words := strings.SplitAfter(reply, " ")
	for i, word := range words {
		finish := ""
		chunk := map[string]any{"modelVersion": model}
		if i == len(words)-1 {
			finish = "STOP"
			chunk["usageMetadata"] = usage
		} else {
			chunk["usageMetadata"] = map[string]any{"promptTokenCount": in}
		}
		chunk["candidates"] = []any{candidate(word, finish)}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\r\n\r\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
}
