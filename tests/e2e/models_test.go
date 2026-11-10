//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// Upstream credentials of the mock providers. They are resolved by the API
// from AIPLEX_TOOL_SECRET_<REF> in development mode; the e2e checks that
// exactly these (and never the caller's AIPlex token) reach the provider.
const (
	openAIKey    = "sk-e2e-openai-upstream"
	anthropicKey = "sk-ant-e2e-upstream"
	geminiKey    = "gm-e2e-upstream"
)

func withProviderSecrets(o *devstack.Options) {
	o.Env = append(o.Env,
		"AIPLEX_TOOL_SECRET_OPENAI_KEY="+openAIKey,
		"AIPLEX_TOOL_SECRET_ANTHROPIC_KEY="+anthropicKey,
		"AIPLEX_TOOL_SECRET_GEMINI_KEY="+geminiKey,
	)
}

// modelEnv creates, with the CLI, one surface per provider type and an access
// point "models" that exposes them.
func modelEnv(t *testing.T) *env {
	t.Helper()
	e := start(t, withProviderSecrets)
	origin := e.stack.ModelsURL
	surfaces := map[string]string{
		"oai": `surface:
  id: oai
  protocol: openai-chat
  origin: ` + origin + `
  models: [gpt-e2e, gpt-ratelimit, gpt-hang, gpt-slow]
  operations: [openai.v1.chat-completions]
  provider: {id: openai}
auth: {type: bearer, secret_ref: openai-key}
`,
		"oai-embed": `surface:
  id: oai-embed
  protocol: openai-embeddings
  origin: ` + origin + `
  models: [embed-e2e]
  operations: [openai.v1.embeddings]
  provider: {id: openai}
auth: {type: bearer, secret_ref: openai-key}
`,
		"compat": `surface:
  id: compat
  protocol: openai-chat
  origin: ` + origin + `
  models: [compat-e2e]
  operations: [compat.chat-completions]
  provider: {id: openai-compatible, usage_semantics: openai, stream_usage: inject}
auth: {type: none}
`,
		"ant": `surface:
  id: ant
  protocol: anthropic-messages
  origin: ` + origin + `
  models: [claude-e2e, claude-ratelimit]
  operations: [anthropic.messages, anthropic.messages-count-tokens]
  provider: {id: anthropic, anthropic_version: "2023-06-01"}
auth: {type: api_key, in: header, name: x-api-key, secret_ref: anthropic-key}
`,
		"gem": `surface:
  id: gem
  protocol: google-genai
  origin: ` + origin + `
  models: [gemini-e2e, gemini-ratelimit]
  operations: [gemini.models.generateContent, gemini.models.streamGenerateContent, gemini.models.countTokens]
  provider: {id: gemini-developer}
auth: {type: api_key, in: header, name: x-goog-api-key, secret_ref: gemini-key}
`,
	}
	for _, id := range []string{"oai", "oai-embed", "compat", "ant", "gem"} {
		r := e.mustCLI("llm", "surfaces", "apply", "-f", writeFile(t, id+".yaml", surfaces[id]))
		contains(t, r.Out, "Applied model surface "+id, "surface apply")
	}
	models := []string{}
	for surface, names := range map[string][]string{"oai": {"gpt-e2e", "gpt-ratelimit", "gpt-hang", "gpt-slow"}, "oai-embed": {"embed-e2e"}, "compat": {"compat-e2e"}, "ant": {"claude-e2e", "claude-ratelimit"}, "gem": {"gemini-e2e", "gemini-ratelimit"}} {
		for _, n := range names {
			models = append(models, fmt.Sprintf("  - {name: %s, surface_ref: %s, native_model: %s}", n, surface, n))
		}
	}
	ap := "id: models\ndisplay_name: Model e2e\ncaller_kinds: [user, agent, client]\nmodels:\n" + strings.Join(models, "\n") + "\n"
	r := e.mustCLI("access-points", "apply", "-f", writeFile(t, "ap.yaml", ap))
	contains(t, r.Out, "models", "access point apply")
	return e
}

func lastUpstream(t *testing.T, e *env) devstack.MockRequest {
	t.Helper()
	reqs := e.stack.Models.Requests()
	if len(reqs) == 0 {
		t.Fatal("no upstream request recorded")
	}
	return reqs[len(reqs)-1]
}

func TestModelsThroughAccessPoints(t *testing.T) {
	e := modelEnv(t)
	tok := e.stack.Token

	t.Run("openai chat JSON", func(t *testing.T) {
		r := e.post("/ap/models/v1/chat/completions", `{"model":"gpt-e2e","messages":[{"role":"user","content":"hello e2e"}]}`)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "You said: hello e2e", "reply")
		up := lastUpstream(t, e)
		if up.Authorization != "Bearer "+openAIKey {
			t.Fatalf("upstream Authorization = %q, want the surface credential", up.Authorization)
		}
		notContains(t, up.Authorization, tok, "AIPlex token forwarded upstream")
	})

	t.Run("openai chat SSE requires include_usage by default", func(t *testing.T) {
		r := e.post("/ap/models/v1/chat/completions", `{"model":"gpt-e2e","stream":true,"messages":[{"role":"user","content":"x"}]}`)
		if r.Status != 400 {
			t.Fatalf("stream without include_usage on an openai surface: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "include_usage", "error explains the stream_usage policy")
		r = e.post("/ap/models/v1/chat/completions", `{"model":"gpt-e2e","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"stream me"}]}`)
		if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("%d %s %s", r.Status, r.Header.Get("Content-Type"), r.Body)
		}
		data := sseData(r.Body)
		if len(data) < 3 || data[len(data)-1] != "[DONE]" || !strings.Contains(data[len(data)-2], `"usage"`) {
			t.Fatalf("SSE stream incomplete: %v", data)
		}
	})

	t.Run("openai-compatible injects include_usage", func(t *testing.T) {
		r := e.post("/ap/models/v1/chat/completions", `{"model":"compat-e2e","stream":true,"messages":[{"role":"user","content":"x"}]}`)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		contains(t, r.Body, `"usage"`, "injected usage chunk")
		if up := lastUpstream(t, e); up.Authorization != "" {
			t.Fatalf("auth none surface sent Authorization %q", up.Authorization)
		}
	})

	t.Run("anthropic messages JSON with x-api-key", func(t *testing.T) {
		r := e.do("POST", "/ap/models/v1/messages", `{"model":"claude-e2e","max_tokens":32,"messages":[{"role":"user","content":"hi claude"}]}`, "x-api-key", tok, "anthropic-version", "2023-06-01")
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		contains(t, r.Body, `"type":"message"`, "anthropic shape")
		up := lastUpstream(t, e)
		if up.APIKey != anthropicKey || up.Authorization != "" {
			t.Fatalf("upstream x-api-key %q authorization %q", up.APIKey, up.Authorization)
		}
	})

	t.Run("anthropic messages SSE", func(t *testing.T) {
		r := e.post("/ap/models/v1/messages", `{"model":"claude-e2e","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"stream"}]}`, "anthropic-version", "2023-06-01")
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "event: message_stop", "anthropic stream end")
	})

	t.Run("gemini generateContent with x-goog-api-key", func(t *testing.T) {
		r := e.do("POST", "/ap/models/v1beta/models/gemini-e2e:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi gemini"}]}]}`, "x-goog-api-key", tok, "Content-Type", "application/json")
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "usageMetadata", "gemini shape")
		if up := lastUpstream(t, e); up.GoogAPIKey != geminiKey {
			t.Fatalf("upstream x-goog-api-key %q", up.GoogAPIKey)
		}
		r = e.post("/ap/models/v1beta/models/gemini-e2e:streamGenerateContent?alt=sse", `{"contents":[{"role":"user","parts":[{"text":"stream"}]}]}`)
		if r.Status != 200 || len(sseData(r.Body)) < 2 {
			t.Fatalf("gemini stream: %d %s", r.Status, r.Body)
		}
	})

	t.Run("embeddings", func(t *testing.T) {
		r := e.post("/ap/models/v1/embeddings", `{"model":"embed-e2e","input":["one two","three"]}`)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		contains(t, r.Body, `"embedding"`, "embeddings")
	})

	t.Run("per-surface gateway", func(t *testing.T) {
		r := e.post("/api/v1/llm/gateway/oai/v1/chat/completions", `{"model":"gpt-e2e","messages":[{"role":"user","content":"via gateway"}]}`)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "via gateway", "gateway reply")
		r = e.do("POST", "/api/v1/llm/gateway/ant/v1/messages", `{"model":"claude-e2e","max_tokens":8,"messages":[{"role":"user","content":"x"}]}`, "x-api-key", tok, "anthropic-version", "2023-06-01", "Content-Type", "application/json")
		if r.Status != 200 {
			t.Fatalf("gateway anthropic with x-api-key: %d %s", r.Status, r.Body)
		}
	})

	t.Run("models list", func(t *testing.T) {
		r := e.do("GET", "/ap/models/v1/models", "", e.auth()...)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		contains(t, r.Body, `"object":"list"`, "openai list shape")
		contains(t, r.Body, "gpt-e2e", "openai list")
		r = e.do("GET", "/ap/models/v1/models", "", "x-api-key", e.stack.Token, "anthropic-version", "2023-06-01")
		if r.Status != 200 {
			t.Fatalf("anthropic list with x-api-key: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, `"has_more"`, "anthropic list shape")
		r = e.do("GET", "/ap/models/v1beta/models", "", "x-goog-api-key", e.stack.Token)
		if r.Status != 200 {
			t.Fatalf("genai list: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "gemini-e2e", "genai list")
		notContains(t, r.Body, "gpt-e2e", "genai list is limited to the GenAI wire")
	})

	t.Run("native error shapes", func(t *testing.T) {
		// Unknown model, OpenAI shape.
		r := e.post("/ap/models/v1/chat/completions", `{"model":"nope","messages":[]}`)
		if r.Status != 404 {
			t.Fatalf("unknown model: %d %s", r.Status, r.Body)
		}
		body := r.JSON(t)
		if _, ok := body["error"].(map[string]any); !ok {
			t.Fatalf("unknown model is not an OpenAI error object: %s", r.Body)
		}
		// Unknown model, Anthropic shape.
		r = e.post("/ap/models/v1/messages", `{"model":"nope","max_tokens":1,"messages":[]}`)
		if r.Status != 404 || !strings.Contains(r.Body, `"type":"error"`) {
			t.Fatalf("anthropic unknown model: %d %s", r.Status, r.Body)
		}
		// Unknown model, Gemini shape.
		r = e.post("/ap/models/v1beta/models/nope:generateContent", `{"contents":[]}`)
		if r.Status != 404 || !strings.Contains(r.Body, `"status"`) {
			t.Fatalf("gemini unknown model: %d %s", r.Status, r.Body)
		}
		// Wrong protocol: an Anthropic model on the OpenAI wire.
		r = e.post("/ap/models/v1/chat/completions", `{"model":"claude-e2e","messages":[{"role":"user","content":"x"}]}`)
		if r.Status != 400 {
			t.Fatalf("protocol mismatch: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "/v1/messages", "protocol mismatch names the serving path")
		if _, ok := r.JSON(t)["error"].(map[string]any); !ok {
			t.Fatalf("protocol mismatch not in OpenAI shape: %s", r.Body)
		}
		// Unauthorized: no token, and a token from an untrusted issuer.
		r = e.do("POST", "/ap/models/v1/chat/completions", `{"model":"gpt-e2e","messages":[]}`)
		if r.Status != 401 {
			t.Fatalf("no token: %d %s", r.Status, r.Body)
		}
		if _, ok := r.JSON(t)["error"].(map[string]any); !ok {
			t.Fatalf("401 not in OpenAI shape: %s", r.Body)
		}
		other, err := devstack.NewIssuer("http://127.0.0.1:1", devstack.DevAudience)
		if err != nil {
			t.Fatal(err)
		}
		foreign, _ := other.Mint(map[string]any{"sub": "intruder"}, time.Hour)
		r = e.do("POST", "/ap/models/v1/messages", `{"model":"claude-e2e","max_tokens":1,"messages":[]}`, "x-api-key", foreign, "Content-Type", "application/json")
		if r.Status != 401 || !strings.Contains(r.Body, `"type":"error"`) {
			t.Fatalf("foreign token on anthropic wire: %d %s", r.Status, r.Body)
		}
		// Conflicting Authorization and provider header.
		r = e.do("POST", "/ap/models/v1/messages", `{"model":"claude-e2e","max_tokens":1,"messages":[]}`, "Authorization", "Bearer "+tok, "x-api-key", foreign, "Content-Type", "application/json")
		if r.Status != 401 {
			t.Fatalf("conflicting credentials: %d %s", r.Status, r.Body)
		}
		// Token in the query string is refused.
		r = e.do("POST", "/ap/models/v1beta/models/gemini-e2e:generateContent?key="+tok, `{"contents":[]}`)
		if r.Status != 401 {
			t.Fatalf("query token: %d %s", r.Status, r.Body)
		}
	})

	t.Run("provider 429 passes through", func(t *testing.T) {
		for _, c := range []struct{ path, body, shape string }{
			{"/ap/models/v1/chat/completions", `{"model":"gpt-ratelimit","messages":[{"role":"user","content":"x"}]}`, "rate_limit_exceeded"},
			{"/ap/models/v1/messages", `{"model":"claude-ratelimit","max_tokens":4,"messages":[{"role":"user","content":"x"}]}`, "rate_limit_error"},
			{"/ap/models/v1beta/models/gemini-ratelimit:generateContent", `{"contents":[{"parts":[{"text":"x"}]}]}`, "RESOURCE_EXHAUSTED"},
		} {
			r := e.post(c.path, c.body, "anthropic-version", "2023-06-01")
			if r.Status != 429 || r.Header.Get("Retry-After") != "7" {
				t.Fatalf("%s: %d retry-after %q %s", c.path, r.Status, r.Header.Get("Retry-After"), r.Body)
			}
			contains(t, r.Body, c.shape, "provider body")
		}
	})

	t.Run("usage records the access point", func(t *testing.T) {
		r := e.mustCLI("usage", "records", "--access-point", "models", "-o", "json")
		var page struct {
			Records []map[string]any `json:"records"`
		}
		err := json.Unmarshal([]byte(r.Out), &page)
		records := page.Records
		if err != nil {
			t.Fatalf("usage records -o json: %v\n%s", err, r.Out)
		}
		if len(records) < 8 {
			t.Fatalf("expected the exchanges above in the ledger, got %d", len(records))
		}
		seen := map[string]bool{}
		for _, rec := range records {
			if rec["access_point_id"] != "models" || rec["caller_kind"] != "user" {
				t.Fatalf("record without access point attribution: %v", rec)
			}
			seen[fmt.Sprint(rec["status"])] = true
		}
		if !seen["ok"] || !seen["denied"] {
			t.Fatalf("expected ok and denied (429) records, saw %v", seen)
		}
	})
}
