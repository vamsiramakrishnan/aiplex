//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// The planted marker sits inside an email address (masked) and inside a
// private key block (refused). It must not reach the provider after
// masking, and must not appear in any error body, ledger output or API log.
const (
	e2eMarker     = "planted-e2e-91c4"
	e2eEmail      = e2eMarker + "@example.com"
	e2ePrivateKey = `-----BEGIN PRIVATE KEY-----\n` + e2eMarker + `\n-----END PRIVATE KEY-----`
)

const e2eRequestPolicy = `id: pii
scope: {kind: tenant}
hooks:
  - id: redact
    type: redact
    phases: [request]
    redact:
      rules:
        - {id: email, builtin: email, action: mask}
        - {id: private_key, builtin: private_key, action: deny}
`

func hookSidecar(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv, url, err := devstack.Listen(handler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return url
}

func setPolicy(t *testing.T, e *env, name, yaml string) {
	t.Helper()
	r := e.mustCLI("hooks", "policies", "set", "-f", writeFile(t, name+".yaml", yaml))
	contains(t, r.Out, "hook policy", "policy set")
}

func deletePolicy(t *testing.T, e *env, id string) {
	t.Helper()
	e.mustCLI("hooks", "policies", "delete", id, "--yes")
}

func upstreamCount(e *env) int { return len(e.stack.Models.Requests()) }

func TestRequestHooksOnModelWires(t *testing.T) {
	// A sidecar for the fail-closed check: reserved, then shut down.
	down, downURL, err := devstack.Listen(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	down.Close()
	e := modelEnv(t, func(o *devstack.Options) { o.ExtraOrigins = append(o.ExtraOrigins, downURL) })
	origin := e.stack.ModelsURL
	e.mustCLI("llm", "surfaces", "apply", "-f", writeFile(t, "resp.yaml", `surface:
  id: resp
  protocol: openai-responses
  origin: `+origin+`
  models: [resp-e2e]
  operations: [openai.v1.responses]
  provider: {id: openai}
auth: {type: bearer, secret_ref: openai-key}
`))
	ap := "id: hooked\ncaller_kinds: [user, agent, client]\nmodels:\n" +
		"  - {name: gpt-e2e, surface_ref: oai, native_model: gpt-e2e}\n" +
		"  - {name: resp-e2e, surface_ref: resp, native_model: resp-e2e}\n" +
		"  - {name: claude-e2e, surface_ref: ant, native_model: claude-e2e}\n" +
		"  - {name: gemini-e2e, surface_ref: gem, native_model: gemini-e2e}\n"
	e.mustCLI("access-points", "apply", "-f", writeFile(t, "hooked.yaml", ap))
	setPolicy(t, e, "pii", e2eRequestPolicy)
	anthropic := []string{"anthropic-version", "2023-06-01"}

	t.Run("masked prompt reaches the provider masked", func(t *testing.T) {
		r := e.post("/ap/hooked/v1/chat/completions", `{"model":"gpt-e2e","messages":[{"role":"user","content":"write to `+e2eEmail+`"}]}`)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		up := lastUpstream(t, e)
		contains(t, up.Body, `"content":"write to [EMAIL]"`, "upstream body")
		notContains(t, up.Body+r.Body, e2eMarker, "masked prompt")
		contains(t, r.Body, "You said: write to [EMAIL]", "mock echo of the masked prompt")
	})

	t.Run("deny is protocol-native on each wire", func(t *testing.T) {
		before := upstreamCount(e)
		cases := []struct {
			name, path, body string
			headers          []string
			check            func(map[string]any) bool
		}{
			{"openai chat", "/ap/hooked/v1/chat/completions", `{"model":"gpt-e2e","messages":[{"role":"user","content":"` + e2ePrivateKey + `"}]}`, nil,
				func(m map[string]any) bool {
					e, _ := m["error"].(map[string]any)
					return e["code"] == "hook_denied" && e["type"] == "permission_error"
				}},
			{"openai responses", "/ap/hooked/v1/responses", `{"model":"resp-e2e","input":"` + e2ePrivateKey + `"}`, nil,
				func(m map[string]any) bool { e, _ := m["error"].(map[string]any); return e["code"] == "hook_denied" }},
			{"anthropic messages", "/ap/hooked/v1/messages", `{"model":"claude-e2e","max_tokens":16,"messages":[{"role":"user","content":"` + e2ePrivateKey + `"}]}`, anthropic,
				func(m map[string]any) bool {
					e, _ := m["error"].(map[string]any)
					return m["type"] == "error" && e["type"] == "permission_error" && e["code"] == "hook_denied"
				}},
			{"gemini", "/ap/hooked/v1beta/models/gemini-e2e:generateContent", `{"contents":[{"role":"user","parts":[{"text":"` + e2ePrivateKey + `"}]}]}`, nil,
				func(m map[string]any) bool {
					e, _ := m["error"].(map[string]any)
					details, _ := e["details"].([]any)
					return e["status"] == "PERMISSION_DENIED" && len(details) == 1 && strings.Contains(r2s(details[0]), "HOOK_DENIED")
				}},
		}
		for _, c := range cases {
			r := e.post(c.path, c.body, c.headers...)
			if r.Status != 403 || !c.check(r.JSON(t)) || r.Header.Get("X-Aiplex-Error-Code") != "hook_denied" {
				t.Errorf("%s: %d %s", c.name, r.Status, r.Body)
			}
			notContains(t, r.Body, e2eMarker, c.name+" error body")
		}
		if after := upstreamCount(e); after != before {
			t.Fatalf("a denied request reached the provider (%d -> %d)", before, after)
		}
	})

	t.Run("streaming is refused by default when a response hook applies", func(t *testing.T) {
		setPolicy(t, e, "out", `id: out
scope: {kind: access_point, id: hooked}
hooks:
  - id: mask-output
    type: redact
    phases: [response]
    redact: {rules: [{id: email, builtin: email, action: mask}]}
`)
		before := upstreamCount(e)
		r := e.post("/ap/hooked/v1/chat/completions", `{"model":"gpt-e2e","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"x"}]}`)
		if r.Status != 403 || !strings.Contains(r.Body, "hook_denied") || upstreamCount(e) != before {
			t.Fatalf("stream under a response hook: %d %s", r.Status, r.Body)
		}
	})

	t.Run("buffered streams are inspected and re-emitted", func(t *testing.T) {
		// Only response hooks: the email reaches the mock, which echoes it;
		// the buffered stream reaches the caller masked.
		deletePolicy(t, e, "pii")
		setPolicy(t, e, "out", `id: out
scope: {kind: access_point, id: hooked}
streaming: buffer
hooks:
  - id: mask-output
    type: redact
    phases: [response]
    redact: {rules: [{id: email, builtin: email, action: mask}]}
`)
		setPolicy(t, e, "tenant-buffer", "id: tenant-buffer\nscope: {kind: tenant}\nstreaming: buffer\nhooks: [{id: size, type: size_limit, phases: [request], size_limit: {max_chars: 100000}}]\n")
		r := e.post("/ap/hooked/v1/chat/completions", `{"model":"gpt-e2e","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi `+e2eEmail+`"}]}`)
		if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("chat buffer: %d %s", r.Status, r.Body)
		}
		data := sseData(r.Body)
		if data[len(data)-1] != "[DONE]" || !strings.Contains(data[len(data)-2], `"usage"`) {
			t.Fatalf("chat stream not re-emitted intact: %v", data)
		}
		var text strings.Builder
		for _, d := range data[:len(data)-1] {
			var chunk struct {
				Choices []struct {
					Delta struct{ Content string } `json:"delta"`
				} `json:"choices"`
			}
			if json.Unmarshal([]byte(d), &chunk) != nil {
				t.Fatalf("invalid chunk %q", d)
			}
			for _, c := range chunk.Choices {
				text.WriteString(c.Delta.Content)
			}
		}
		contains(t, text.String(), "You said: hi [EMAIL]", "assembled masked stream")
		notContains(t, r.Body, e2eMarker, "chat stream")
		contains(t, lastUpstream(t, e).Body, e2eMarker, "upstream saw the unmasked prompt (no request hook)")
		// Metering is the same observer as an unbuffered stream: exact usage.
		var page struct {
			Records []struct {
				Streaming bool `json:"streaming"`
				Usage     *struct {
					InputTokens  *int64 `json:"input_tokens"`
					OutputTokens *int64 `json:"output_tokens"`
				} `json:"usage"`
				Hooks *struct{ Decision string } `json:"hooks"`
			} `json:"records"`
		}
		out := e.mustCLI("usage", "records", "--model", "gpt-e2e", "--limit", "1", "-o", "json")
		if json.Unmarshal([]byte(out.Out), &page) != nil || len(page.Records) != 1 {
			t.Fatalf("records: %s", out.Out)
		}
		rec := page.Records[0]
		if !rec.Streaming || rec.Usage == nil || rec.Usage.InputTokens == nil || rec.Usage.OutputTokens == nil || rec.Hooks == nil || rec.Hooks.Decision != "transform" {
			t.Fatalf("buffered stream record: %s", out.Out)
		}

		r = e.post("/ap/hooked/v1/messages", `{"model":"claude-e2e","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi `+e2eEmail+`"}]}`, anthropic...)
		if r.Status != 200 {
			t.Fatalf("anthropic buffer: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "event: message_stop", "anthropic stream end")
		contains(t, r.Body, "[EMAIL]", "anthropic masked text")
		notContains(t, r.Body, e2eMarker, "anthropic stream")

		// OpenAI Responses and Gemini streams repeat their text in later
		// events; the mask must reach every copy.
		r = e.post("/ap/hooked/v1/responses", `{"model":"resp-e2e","stream":true,"input":"hi `+e2eEmail+`"}`)
		if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("responses buffer: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "response.completed", "responses stream end")
		contains(t, r.Body, "[EMAIL]", "responses masked text")
		notContains(t, r.Body, e2eMarker, "responses stream")
		r = e.post("/ap/hooked/v1beta/models/gemini-e2e:streamGenerateContent?alt=sse", `{"contents":[{"role":"user","parts":[{"text":"hi `+e2eEmail+`"}]}]}`)
		if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("gemini buffer: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "usageMetadata", "gemini stream usage")
		contains(t, r.Body, "[EMAIL]", "gemini masked text")
		notContains(t, r.Body, e2eMarker, "gemini stream")
		deletePolicy(t, e, "tenant-buffer")
		deletePolicy(t, e, "out")
	})

	t.Run("an unreachable sidecar refuses the request", func(t *testing.T) {
		setPolicy(t, e, "side", "id: side\nscope: {kind: model_surface, id: oai}\nhooks: [{id: s, type: http, phases: [request], timeout_ms: 500, http: {endpoint: \""+downURL+"/\"}}]\n")
		before := upstreamCount(e)
		r := e.post("/ap/hooked/v1/chat/completions", `{"model":"gpt-e2e","messages":[{"role":"user","content":"hello"}]}`)
		if r.Status != 503 || !strings.Contains(r.Body, "hook_unavailable") || upstreamCount(e) != before {
			t.Fatalf("down sidecar: %d %s", r.Status, r.Body)
		}
		deletePolicy(t, e, "side")
	})

	t.Run("ledger records decisions without content", func(t *testing.T) {
		r := e.mustCLI("usage", "records", "--hook-decision", "deny", "-o", "json")
		var page struct {
			Records []struct {
				ErrorClass string `json:"error_class"`
				Hooks      struct {
					Decision string `json:"decision"`
					Results  []struct {
						Rules []struct{ ID string } `json:"rules"`
					} `json:"results"`
				} `json:"hooks"`
			} `json:"records"`
		}
		if err := json.Unmarshal([]byte(r.Out), &page); err != nil || len(page.Records) < 5 {
			t.Fatalf("deny records: %v %s", err, r.Out)
		}
		for _, rec := range page.Records {
			if rec.Hooks.Decision != "deny" || rec.ErrorClass != "hook_denied" {
				t.Fatalf("record %+v", rec)
			}
		}
		wide := e.mustCLI("usage", "records", "--since", "1h", "-o", "wide")
		contains(t, wide.Out, "HOOKS", "wide column")
		contains(t, wide.Out, "private_key", "denied rule in wide output")
		all := e.mustCLI("usage", "records", "--since", "1h", "-o", "json")
		notContains(t, all.Out, e2eMarker, "ledger")
		notContains(t, e.log.String(), e2eMarker, "API log")
	})
}

func r2s(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestRequestHooksOnToolCalls(t *testing.T) {
	sidecarSeen := make(chan string, 8)
	sidecarURL := hookSidecar(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sidecarSeen <- string(body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "forbidden-result") {
			_, _ = io.WriteString(w, `{"decision":"deny","reason":"result_rule"}`)
			return
		}
		_, _ = io.WriteString(w, `{"decision":"allow"}`)
	})
	e := start(t, func(o *devstack.Options) { o.ExtraOrigins = append(o.ExtraOrigins, sidecarURL) })
	spec := devstack.ToolConnectionSpec("open", e.stack.MCPURL, devstack.MockTools, nil, nil)
	e.mustCLI("resources", "put", "tool-connection", "open", writeFile(t, "open.json", spec), "0")
	e.mustCLI("access-points", "apply", "-f", writeFile(t, "ap.json", map[string]any{"id": "tools", "caller_kinds": []string{"user"}, "subjects": map[string]any{},
		"tools": []map[string]any{{"connection_ref": "open"}}}))
	setPolicy(t, e, "tools", `id: tools
scope: {kind: tool_connection, id: open}
hooks:
  - id: mask-args
    type: redact
    phases: [request]
    redact: {rules: [{id: email, builtin: email, action: mask}]}
  - id: results
    type: http
    phases: [response]
    http: {endpoint: "`+sidecarURL+`/hook"}
`)
	c := &mcpClient{e: e, path: "/ap/tools/mcp"}
	c.initialize("2025-06-18")

	got := callText(t, c.call("open__echo", map[string]any{"text": "contact " + e2eEmail}))
	if got != "contact [EMAIL]" {
		t.Fatalf("echo = %q", got)
	}
	bodies := e.stack.MCP.CallBodies()
	if len(bodies) == 0 || !strings.Contains(bodies[len(bodies)-1], "[EMAIL]") || strings.Contains(strings.Join(bodies, ""), e2eMarker) {
		t.Fatalf("tool server received %q", bodies)
	}
	if seen := <-sidecarSeen; !strings.Contains(seen, `"phase":"response"`) || !strings.Contains(seen, `"tool":"echo"`) || strings.Contains(seen, e2eMarker) {
		t.Fatalf("sidecar request %s", seen)
	}

	denied := c.call("open__echo", map[string]any{"text": "forbidden-result"})
	if denied.Error == nil || denied.Error.Code != -32001 || !strings.Contains(string(denied.Error.Data), "hook_denied") || strings.Contains(denied.Raw, "forbidden-result") {
		t.Fatalf("tool result deny: %s", denied.Raw)
	}
	all := e.mustCLI("usage", "records", "--plane", "mcplex", "-o", "json")
	notContains(t, all.Out, e2eMarker, "tool ledger")
	contains(t, all.Out, `"result_rule"`, "sidecar rule recorded")
	notContains(t, e.log.String(), e2eMarker, "API log")
}
