package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
)

const providerSecret = "sk-operator-secret-value"

type capturedRequest struct {
	method, path, query, body string
	header                    http.Header
}

type providerUpstream struct {
	*httptest.Server
	mu       sync.Mutex
	requests []capturedRequest
}

func newProviderUpstream(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *providerUpstream {
	t.Helper()
	u := &providerUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.requests = append(u.requests, capturedRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(body), r.Header.Clone()})
		u.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *providerUpstream) calls() []capturedRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]capturedRequest(nil), u.requests...)
}

func providerSurface(t *testing.T, origin string, p Provider, protocol string, models []string, ids ...string) Surface {
	t.Helper()
	ops, err := SelectProviderOperations(p.ID, ids...)
	if err != nil {
		t.Fatal(err)
	}
	return Surface{ID: "native", Protocol: protocol, Origin: origin, Models: models, Operations: ops, Provider: &p}
}

func headerCredential(name, prefix string) Credential {
	return CredentialFunc(func(_ context.Context, r *http.Request) error {
		r.Header.Set(name, prefix+providerSecret)
		return nil
	})
}

func providerGateway(t *testing.T, s Surface, credential Credential, lease execution.Lease, mutate func(*Options)) *Gateway {
	t.Helper()
	opts := options(t, s.Origin, newOwnership())
	opts.LiveDialer = nil
	opts.TokenSource = nil
	opts.Credential = credential
	if lease != nil {
		opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	}
	if mutate != nil {
		mutate(&opts)
	}
	g, err := NewGateway(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func providerServe(g *Gateway, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer caller-aiplex-token")
	r.Header.Set("X-Api-Key", "caller-key")
	r.Header.Set("Api-Key", "caller-azure-key")
	r.Header.Set("OpenAI-Organization", "caller-org")
	r.Header.Set("X-Amz-Security-Token", "caller-aws")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func assertNoCallerCredentials(t *testing.T, h http.Header, allowed string) {
	t.Helper()
	for _, name := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Amz-Security-Token", "OpenAI-Organization"} {
		if name == allowed {
			continue
		}
		for _, v := range h.Values(name) {
			if strings.Contains(v, "caller") {
				t.Fatalf("caller credential header %s crossed upstream: %q", name, v)
			}
		}
	}
}

func singleTerminal(t *testing.T, lease *modelLease) execution.Observation {
	t.Helper()
	terminal := lease.terminalObservations()
	if len(terminal) != 1 {
		t.Fatalf("want one terminal observation, got %+v (results %+v)", lease.observations, lease.results)
	}
	return terminal[0]
}

func TestProviderSurfaceValidation(t *testing.T) {
	ok := func(p Provider, protocol string, models []string, ids ...string) Surface {
		ops, err := SelectProviderOperations(p.ID, ids...)
		if err != nil {
			t.Fatal(err)
		}
		return Surface{ID: "s", Protocol: protocol, Origin: "https://api.example.com", Models: models, Operations: ops, Provider: &p}
	}
	bedrock := ok(Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockConverse, []string{"anthropic.claude-3-5-sonnet-20240620-v1:0"}, "bedrock.converse", "bedrock.converse-stream")
	bedrock.Origin = "https://bedrock-runtime.us-east-1.amazonaws.com"
	azure := ok(Provider{ID: ProviderAzureOpenAI, APIVersion: "2024-10-21"}, ProtocolOpenAIChat, []string{"gpt-dep"}, "azure.v1.chat-completions", "azure.deployments.chat-completions")
	azure.Origin = "https://res.openai.azure.com"
	valid := []Surface{
		ok(Provider{ID: ProviderOpenAI, Organization: "org-1", Project: "proj_1"}, ProtocolOpenAIChat, []string{"gpt-x", "ft:gpt-x:org:custom:id"}, "openai.v1.chat-completions", "openai.v1.responses"),
		ok(Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01", AnthropicBetas: []string{"prompt-caching-2024-07-31"}}, ProtocolAnthropicMessages, []string{"claude-x"}, "anthropic.messages"),
		ok(Provider{ID: ProviderOpenAICompatible, BasePath: "/openai/v1", StreamUsage: StreamUsageInject}, ProtocolOpenAIChat, []string{"meta-llama/Llama-3.1-8B"}, "compat.chat-completions"),
		bedrock, azure,
	}
	for i, s := range valid {
		if err := s.Validate(); err != nil {
			t.Fatalf("valid surface %d rejected: %v", i, err)
		}
	}
	mutate := func(base Surface, f func(*Surface)) Surface {
		c := cloneSurface(base)
		f(&c)
		return c
	}
	invalid := map[string]Surface{
		"unknown provider":         mutate(valid[0], func(s *Surface) { s.Provider.ID = "nope" }),
		"protocol mismatch":        mutate(valid[0], func(s *Surface) { s.Protocol = ProtocolBedrockConverse }),
		"google fields":            mutate(valid[0], func(s *Surface) { s.Project = "p" }),
		"origin path":              mutate(valid[0], func(s *Surface) { s.Origin = "https://api.example.com/v1" }),
		"foreign operation":        mutate(valid[0], func(s *Surface) { s.Operations = append(s.Operations, AnthropicOperations()[0]) }),
		"altered operation":        mutate(valid[0], func(s *Surface) { s.Operations[0].Path = "/v1/other" }),
		"duplicate operation":      mutate(valid[0], func(s *Surface) { s.Operations = append(s.Operations, s.Operations[0]) }),
		"no models":                mutate(valid[0], func(s *Surface) { s.Models = nil }),
		"bad model":                mutate(valid[0], func(s *Surface) { s.Models = []string{"../x"} }),
		"bad org":                  mutate(valid[0], func(s *Surface) { s.Provider.Organization = "org\r\nx" }),
		"stream usage value":       mutate(valid[0], func(s *Surface) { s.Provider.StreamUsage = "maybe" }),
		"anthropic version":        mutate(valid[1], func(s *Surface) { s.Provider.AnthropicVersion = "" }),
		"anthropic stream usage":   mutate(valid[1], func(s *Surface) { s.Provider.StreamUsage = StreamUsageInject }),
		"base path elsewhere":      mutate(valid[0], func(s *Surface) { s.Provider.BasePath = "/v1" }),
		"bad base path":            mutate(valid[2], func(s *Surface) { s.Provider.BasePath = "/../x" }),
		"bedrock region":           mutate(bedrock, func(s *Surface) { s.Provider.Region = "" }),
		"bedrock origin region":    mutate(bedrock, func(s *Surface) { s.Origin = "https://bedrock-runtime.us-west-2.amazonaws.com" }),
		"bedrock slash model":      mutate(bedrock, func(s *Surface) { s.Models = []string{"arn:aws:bedrock:x/y"} }),
		"azure origin":             mutate(azure, func(s *Surface) { s.Origin = "https://example.com" }),
		"azure missing apiversion": mutate(azure, func(s *Surface) { s.Provider.APIVersion = "" }),
		"region elsewhere":         mutate(valid[0], func(s *Surface) { s.Provider.Region = "us-east-1" }),
	}
	for name, s := range invalid {
		if err := s.Validate(); err == nil {
			t.Fatalf("%s: invalid surface accepted", name)
		}
	}
}

func TestOpenAIChatJSONPassthroughCredentialAndUsage(t *testing.T) {
	const response = `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-x-2025","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":7,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":5}}}`
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Ratelimit-Remaining-Tokens", "149984")
		w.Header().Set("X-Request-Id", "req_1")
		fmt.Fprint(w, response)
	})
	lease := &modelLease{}
	var limits []providerlimits.Limits
	g := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderOpenAI, Organization: "org-pinned", Project: "proj_pinned"}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions"), headerCredential("Authorization", "Bearer "), lease, func(o *Options) {
		o.ObserveLimits = func(_ context.Context, _ Owner, l providerlimits.Limits) { limits = append(limits, l) }
	})
	body := `{"model":"gpt-x",  "messages":[{"role":"user","content":"hello"}],"max_completion_tokens":9,"extension":{"x":[1,2]}}`
	w := providerServe(g, "POST", "/v1/chat/completions?key=caller&foo=bar", body, nil)
	if w.Code != 200 || w.Body.String() != response {
		t.Fatalf("native response changed: %d %s", w.Code, w.Body.String())
	}
	calls := up.calls()
	if len(calls) != 1 || calls[0].path != "/v1/chat/completions" || calls[0].body != body || calls[0].query != "" {
		t.Fatalf("request not passed through unchanged: %+v", calls)
	}
	h := calls[0].header
	if h.Get("Authorization") != "Bearer "+providerSecret || h.Get("OpenAI-Organization") != "org-pinned" || h.Get("OpenAI-Project") != "proj_pinned" {
		t.Fatalf("server credential/pins not applied: %v", h)
	}
	assertNoCallerCredentials(t, h, "")
	if w.Header().Get("X-Ratelimit-Remaining-Tokens") != "" || w.Header().Get("X-Request-Id") != "req_1" {
		t.Fatal("shared-key limit headers leaked or request id dropped", w.Header())
	}
	if strings.Contains(w.Body.String()+fmt.Sprint(w.Header()), providerSecret) {
		t.Fatal("credential echoed to caller")
	}
	value := singleTerminal(t, lease)
	if value.ModelVersion != "gpt-x-2025" || value.Counters["uncached_input_tokens"] != 6 || value.Counters["cached_tokens"] != 4 || value.Counters["output_tokens"] != 7 || value.Counters["input_tokens"] != 10 {
		t.Fatalf("usage not normalized exactly: %+v", value)
	}
	if len(limits) != 1 || limits[0].Tokens == nil || *limits[0].Tokens.Remaining != 149984 {
		t.Fatalf("limit signal not observed: %+v", limits)
	}
}

const openAIStream = "data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-x-2025\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}],\"usage\":null}\n\n" +
	"data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-x-2025\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":null}\n\n" +
	"data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-x-2025\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":7,\"total_tokens\":17,\"prompt_tokens_details\":{\"cached_tokens\":0},\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n" +
	"data: [DONE]\n\n"

func TestOpenAIChatStreamUsagePolicies(t *testing.T) {
	sse := func(stream string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, stream)
		}
	}
	plain := `{"model":"gpt-x","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	asked := `{"model":"gpt-x","stream":true,"stream_options":{"include_usage":true},"messages":[]}`
	t.Run("require rejects before dispatch", func(t *testing.T) {
		up := newProviderUpstream(t, sse(openAIStream))
		lease := &modelLease{}
		g := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderOpenAI}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions"), headerCredential("Authorization", "Bearer "), lease, nil)
		w := providerServe(g, "POST", "/v1/chat/completions", plain, nil)
		if w.Code != 400 || len(up.calls()) != 0 || len(lease.calls) != 0 {
			t.Fatal("require mode did not reject a streaming request without usage", w.Code, up.calls(), lease.calls)
		}
		w = providerServe(g, "POST", "/v1/chat/completions", asked, nil)
		if w.Code != 200 || w.Body.String() != openAIStream || up.calls()[0].body != asked {
			t.Fatal("explicit usage request changed", w.Code, w.Body.String())
		}
		value := singleTerminal(t, lease)
		if value.Counters["uncached_input_tokens"] != 10 || value.Counters["cached_tokens"] != 0 || value.Counters["output_tokens"] != 7 || !lease.isSettled() {
			t.Fatal("stream usage not normalized", value)
		}
	})
	t.Run("inject adds only include_usage", func(t *testing.T) {
		up := newProviderUpstream(t, sse(openAIStream))
		g := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderOpenAI, StreamUsage: StreamUsageInject}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions"), headerCredential("Authorization", "Bearer "), &modelLease{}, nil)
		for body, want := range map[string]string{
			plain: `{"stream_options":{"include_usage":true},"model":"gpt-x","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			`{"model":"gpt-x","stream":true,"stream_options":{"other":1}}`: `{"model":"gpt-x","stream":true,"stream_options":{"include_usage":true,"other":1}}`,
			`{"model":"gpt-x","stream":true,"stream_options":{}}`:          `{"model":"gpt-x","stream":true,"stream_options":{"include_usage":true}}`,
			asked: asked,
		} {
			before := len(up.calls())
			w := providerServe(g, "POST", "/v1/chat/completions", body, nil)
			calls := up.calls()
			if w.Code != 200 || len(calls) != before+1 || calls[len(calls)-1].body != want {
				t.Fatalf("inject produced %d %q want %q", w.Code, calls[len(calls)-1].body, want)
			}
		}
		for _, conflicting := range []string{`{"model":"gpt-x","stream":true,"stream_options":{"include_usage":false}}`, `{"model":"gpt-x","stream":true,"stream_options":null}`} {
			if w := providerServe(g, "POST", "/v1/chat/completions", conflicting, nil); w.Code != 400 {
				t.Fatal("explicit opt-out was overridden", w.Code)
			}
		}
		nonStream := `{"model":"gpt-x","messages":[]}`
		up2 := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"c","choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0}}}`)
		})
		g2 := providerGateway(t, providerSurface(t, up2.URL, Provider{ID: ProviderOpenAI, StreamUsage: StreamUsageInject}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions"), headerCredential("Authorization", "Bearer "), &modelLease{}, nil)
		if w := providerServe(g2, "POST", "/v1/chat/completions", nonStream, nil); w.Code != 200 || up2.calls()[0].body != nonStream {
			t.Fatal("non-streaming body mutated", w.Code)
		}
	})
	t.Run("off forwards unchanged and leaves usage unknown", func(t *testing.T) {
		stream := strings.Replace(openAIStream, "data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-x-2025\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":7,\"total_tokens\":17,\"prompt_tokens_details\":{\"cached_tokens\":0},\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n", "", 1)
		up := newProviderUpstream(t, sse(stream))
		lease := &modelLease{}
		g := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderOpenAI, StreamUsage: StreamUsageOff}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions"), headerCredential("Authorization", "Bearer "), lease, nil)
		w := providerServe(g, "POST", "/v1/chat/completions", plain, nil)
		if w.Code != 200 || up.calls()[0].body != plain || len(lease.terminalObservations()) != 0 || lease.isSettled() {
			t.Fatal("missing stream usage became known or settled", w.Code, lease.observations, lease.results)
		}
	})
	t.Run("missing DONE is incomplete", func(t *testing.T) {
		up := newProviderUpstream(t, sse(strings.TrimSuffix(openAIStream, "data: [DONE]\n\n")))
		lease := &modelLease{}
		g := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderOpenAI}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions"), headerCredential("Authorization", "Bearer "), lease, nil)
		providerServe(g, "POST", "/v1/chat/completions", asked, nil)
		if lease.isSettled() || len(lease.terminalObservations()) != 0 {
			t.Fatal("stream without terminal marker settled")
		}
	})
}

func TestOpenAIResponsesUsageOwnershipAndStream(t *testing.T) {
	const response = `{"id":"resp_1","object":"response","model":"gpt-x-2025","status":"completed","usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":2},"output_tokens":9,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":21}}`
	stream := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_2\",\"model\":\"gpt-x-2025\",\"status\":\"in_progress\",\"usage\":null}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_2\",\"model\":\"gpt-x-2025\",\"status\":\"completed\",\"usage\":{\"input_tokens\":12,\"input_tokens_details\":{\"cached_tokens\":2},\"output_tokens\":9,\"output_tokens_details\":{\"reasoning_tokens\":3},\"total_tokens\":21}}}\n\n"
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(strings.NewReader(""), 0))
		_ = body
		if strings.Contains(r.Header.Get("Accept"), "event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, stream)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	})
	s := providerSurface(t, up.URL, Provider{ID: ProviderOpenAI}, ProtocolOpenAIResponses, []string{"gpt-x"}, "openai.v1.responses")
	for _, streaming := range []bool{false, true} {
		lease := &modelLease{}
		g := providerGateway(t, s, headerCredential("Authorization", "Bearer "), lease, nil)
		headers := map[string]string{}
		body := `{"model":"gpt-x","input":"hello"}`
		if streaming {
			headers["Accept"] = "text/event-stream"
			body = `{"model":"gpt-x","input":"hello","stream":true}`
		}
		w := providerServe(g, "POST", "/v1/responses", body, headers)
		if w.Code != 200 || (streaming && w.Body.String() != stream) || (!streaming && w.Body.String() != response) {
			t.Fatalf("streaming=%t response changed: %d %s", streaming, w.Code, w.Body.String())
		}
		value := singleTerminal(t, lease)
		if value.Counters["uncached_input_tokens"] != 10 || value.Counters["cached_tokens"] != 2 || value.Counters["output_tokens"] != 9 || value.Counters["thought_tokens"] != 3 {
			t.Fatalf("streaming=%t usage %+v", streaming, value)
		}
	}
	// Response IDs are bound to their creator; a different principal cannot chain them.
	g := providerGateway(t, s, headerCredential("Authorization", "Bearer "), nil, nil)
	if w := providerServe(g, "POST", "/v1/responses", `{"model":"gpt-x","input":"x"}`, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	before := len(up.calls())
	if w := providerServe(g, "POST", "/v1/responses", `{"model":"gpt-x","input":"x","previous_response_id":"resp_1"}`, map[string]string{"Test-Principal": "mallory"}); w.Code != 403 || len(up.calls()) != before {
		t.Fatal("foreign previous_response_id reached upstream", w.Code)
	}
	if w := providerServe(g, "POST", "/v1/responses", `{"model":"gpt-x","input":"x","previous_response_id":"resp_1"}`, nil); w.Code != 200 {
		t.Fatal("owner could not continue its response", w.Code)
	}
	if w := providerServe(g, "POST", "/v1/responses", `{"model":"gpt-x","input":"x","conversation":"conv_1"}`, nil); w.Code != 403 {
		t.Fatal("unscoped conversation accepted", w.Code)
	}
}

func TestAnthropicDirectHeadersCredentialAndUsage(t *testing.T) {
	const response = `{"id":"msg_1","type":"message","model":"claude-x-20250101","stop_reason":"end_turn","usage":{"input_tokens":5,"cache_read_input_tokens":20,"cache_creation_input_tokens":0,"output_tokens":3}}`
	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_2\",\"model\":\"claude-x-20250101\",\"usage\":{\"input_tokens\":5,\"cache_read_input_tokens\":20,\"cache_creation_input_tokens\":0,\"output_tokens\":1}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Anthropic-Ratelimit-Requests-Remaining", "99")
		if strings.Contains(r.Header.Get("Accept"), "event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, stream)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	})
	s := providerSurface(t, up.URL, Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01", AnthropicBetas: []string{"beta-a"}}, ProtocolAnthropicMessages, []string{"claude-x"}, "anthropic.messages")
	for _, streaming := range []bool{false, true} {
		lease := &modelLease{}
		g := providerGateway(t, s, headerCredential("X-Api-Key", ""), lease, nil)
		headers := map[string]string{"Anthropic-Beta": "beta-a", "Anthropic-Version": "2023-06-01"}
		body := `{"model":"claude-x","max_tokens":9,"messages":[{"role":"user","content":"hi"}]}`
		if streaming {
			headers["Accept"] = "text/event-stream"
		}
		w := providerServe(g, "POST", "/v1/messages", body, headers)
		if w.Code != 200 || w.Header().Get("Anthropic-Ratelimit-Requests-Remaining") != "" {
			t.Fatal(w.Code, w.Body.String(), w.Header())
		}
		h := up.calls()[len(up.calls())-1].header
		if h.Get("X-Api-Key") != providerSecret || h.Get("Anthropic-Version") != "2023-06-01" || h.Get("Anthropic-Beta") != "beta-a" || h.Get("Authorization") != "" {
			t.Fatalf("anthropic headers wrong: %v", h)
		}
		value := singleTerminal(t, lease)
		// Anthropic input_tokens excludes cache reads: uncached is native input, total adds cache.
		if value.Counters["uncached_input_tokens"] != 5 || value.Counters["cached_tokens"] != 20 || value.Counters["input_tokens"] != 25 || value.Counters["output_tokens"] != 3 {
			t.Fatalf("streaming=%t usage %+v", streaming, value)
		}
	}
	g := providerGateway(t, s, headerCredential("X-Api-Key", ""), nil, nil)
	before := len(up.calls())
	for _, headers := range []map[string]string{{"Anthropic-Beta": "beta-a,beta-not-allowed"}, {"Anthropic-Version": "2099-01-01"}} {
		if w := providerServe(g, "POST", "/v1/messages", `{"model":"claude-x"}`, headers); w.Code != 400 || len(up.calls()) != before {
			t.Fatal("unapproved protocol header accepted", headers, w.Code)
		}
	}
	if w := providerServe(g, "POST", "/v1/messages", `{"model":"claude-other"}`, nil); w.Code != 403 || len(up.calls()) != before {
		t.Fatal("unlisted model reached upstream", w.Code)
	}
}

func TestAnthropicDirectMissingFinalUsageNeverSettles(t *testing.T) {
	start := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"m\",\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n"
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, start+"data: {\"type\":\"message_stop\"}\n\n")
	})
	lease := &modelLease{}
	g := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01"}, ProtocolAnthropicMessages, []string{"claude-x"}, "anthropic.messages"), headerCredential("X-Api-Key", ""), lease, nil)
	providerServe(g, "POST", "/v1/messages", `{"model":"claude-x","stream":true}`, nil)
	if lease.isSettled() || len(lease.terminalObservations()) != 0 {
		t.Fatal("stream without final usage settled")
	}
}

func TestAzureDeploymentAndV1Paths(t *testing.T) {
	const response = `{"id":"c1","model":"gpt-4o-2024","choices":[{"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":1}}}`
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	})
	s := providerSurface(t, up.URL, Provider{ID: ProviderAzureOpenAI, APIVersion: "2024-10-21"}, ProtocolOpenAIChat, []string{"dep-a"}, "azure.v1.chat-completions", "azure.deployments.chat-completions")
	lease := &modelLease{}
	g := providerGateway(t, s, headerCredential("Api-Key", ""), lease, nil)
	w := providerServe(g, "POST", "/openai/deployments/dep-a/chat/completions?api-version=2099-01-01-preview", `{"messages":[]}`, nil)
	calls := up.calls()
	if w.Code != 200 || calls[0].path != "/openai/deployments/dep-a/chat/completions" || calls[0].query != "api-version=2024-10-21" || calls[0].header.Get("Api-Key") != providerSecret {
		t.Fatalf("deployment call not pinned: %d %+v", w.Code, calls)
	}
	assertNoCallerCredentials(t, calls[0].header, "")
	if value := singleTerminal(t, lease); value.Counters["uncached_input_tokens"] != 2 || value.Counters["cached_tokens"] != 1 || value.Counters["output_tokens"] != 2 {
		t.Fatal(value)
	}
	if w := providerServe(g, "POST", "/openai/deployments/dep-a/chat/completions", `{"model":"dep-b"}`, nil); w.Code != 403 {
		t.Fatal("body model mismatch accepted", w.Code)
	}
	if w := providerServe(g, "POST", "/openai/deployments/dep-b/chat/completions", `{}`, nil); w.Code != 403 {
		t.Fatal("unlisted deployment accepted", w.Code)
	}
	if w := providerServe(g, "POST", "/openai/v1/chat/completions", `{"model":"dep-a","messages":[]}`, nil); w.Code != 200 || up.calls()[len(up.calls())-1].query != "" {
		t.Fatal("v1 path failed", w.Code)
	}
}

func bedrockFrame(eventType, payload string) []byte {
	return encodeEventMessage([][2]string{{":event-type", eventType}, {":content-type", "application/json"}, {":message-type", "event"}}, []byte(payload))
}

func TestBedrockConverseUsageJSONAndEventStream(t *testing.T) {
	const response = `{"output":{"message":{"role":"assistant","content":[{"text":"hi"}]}},"stopReason":"end_turn","usage":{"inputTokens":13,"outputTokens":52,"totalTokens":65},"metrics":{"latencyMs":10}}`
	var stream []byte
	for _, f := range [][2]string{{"messageStart", `{"role":"assistant"}`}, {"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"hi"}}`}, {"contentBlockStop", `{"contentBlockIndex":0}`}, {"messageStop", `{"stopReason":"end_turn"}`}, {"metadata", `{"usage":{"inputTokens":13,"outputTokens":52,"totalTokens":65},"metrics":{"latencyMs":10}}`}} {
		stream = append(stream, bedrockFrame(f[0], f[1])...)
	}
	corrupt := append([]byte(nil), stream...)
	corrupt[len(corrupt)-1] ^= 0xff
	var payload []byte
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/converse-stream") {
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			_, _ = w.Write(payload)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	})
	model := "anthropic.claude-3-5-sonnet-20240620-v1:0"
	s := providerSurface(t, up.URL, Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockConverse, []string{model}, "bedrock.converse", "bedrock.converse-stream")
	signer := CredentialFunc(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=operator/"+providerSecret)
		return nil
	})
	lease := &modelLease{}
	g := providerGateway(t, s, signer, lease, nil)
	body := `{"messages":[{"role":"user","content":[{"text":"hi"}]}],"inferenceConfig":{"maxTokens":64}}`
	w := providerServe(g, "POST", "/model/"+model+"/converse", body, nil)
	if w.Code != 200 || w.Body.String() != response || up.calls()[0].body != body || up.calls()[0].path != "/model/"+model+"/converse" {
		t.Fatal(w.Code, w.Body.String(), up.calls())
	}
	if value := singleTerminal(t, lease); value.Counters["input_tokens"] != 13 || value.Counters["output_tokens"] != 52 || value.Counters["total_tokens"] != 65 {
		t.Fatal(value)
	}
	payload = stream
	lease = &modelLease{}
	g = providerGateway(t, s, signer, lease, nil)
	w = providerServe(g, "POST", "/model/"+model+"/converse-stream", body, nil)
	if w.Code != 200 || !bytesEqual(w.Body.Bytes(), stream) || w.Header().Get("Content-Type") != "application/vnd.amazon.eventstream" || !lease.isSettled() {
		t.Fatalf("event stream not forwarded and settled: %d settled=%t %+v", w.Code, lease.isSettled(), lease.results)
	}
	if value := singleTerminal(t, lease); value.Counters["output_tokens"] != 52 || value.Counters["input_tokens"] != 13 {
		t.Fatal(value)
	}
	// A corrupted final frame withholds the tail and never settles.
	payload = corrupt
	lease = &modelLease{}
	g = providerGateway(t, s, signer, lease, nil)
	w = providerServe(g, "POST", "/model/"+model+"/converse-stream", body, nil)
	if lease.isSettled() || len(lease.terminalObservations()) != 0 || len(w.Body.Bytes()) >= len(stream) {
		t.Fatal("corrupted stream settled or delivered tail")
	}
	// A stream truncated before messageStop/metadata is incomplete.
	payload = stream[:len(stream)-len(bedrockFrame("metadata", `{"usage":{"inputTokens":13,"outputTokens":52,"totalTokens":65},"metrics":{"latencyMs":10}}`))-len(bedrockFrame("messageStop", `{"stopReason":"end_turn"}`))]
	lease = &modelLease{}
	g = providerGateway(t, s, signer, lease, nil)
	providerServe(g, "POST", "/model/"+model+"/converse-stream", body, nil)
	if lease.isSettled() || len(lease.results) != 1 || lease.results[0].Err == nil {
		t.Fatal("truncated stream not incomplete", lease.results)
	}
	if w := providerServe(g, "POST", "/model/other.model-v1:0/converse", body, nil); w.Code != 403 {
		t.Fatal("unlisted model reached upstream", w.Code)
	}
	if w := providerServe(g, "POST", "/model/"+model+"/converse", `{"modelId":"x"}`, nil); w.Code != 403 {
		t.Fatal("body model selector accepted", w.Code)
	}
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

func TestBedrockUsageCacheCompositionIsExclusive(t *testing.T) {
	// AWS: inputTokens is only the non-cached input; total input =
	// inputTokens + cacheReadInputTokens + cacheWriteInputTokens.
	counters, err := bedrockUsageCounters([]byte(`{"inputTokens":1013,"outputTokens":52,"totalTokens":1065,"cacheReadInputTokens":0,"cacheWriteInputTokens":1000}`))
	if err != nil {
		t.Fatal(err)
	}
	if counters["input_tokens"] != 2013 || counters["uncached_input_tokens"] != 1013 || counters["cache_write_tokens"] != 1000 || counters["cached_tokens"] != 0 || counters["output_tokens"] != 52 {
		t.Fatal("exclusive composition not normalized", counters)
	}
	if _, known := counters["total_tokens"]; known {
		t.Fatal("total of undocumented cache composition reported", counters)
	}
	counters, _ = bedrockUsageCounters([]byte(`{"inputTokens":13,"outputTokens":52,"totalTokens":1065,"cacheReadInputTokens":0,"cacheWriteInputTokens":1000}`))
	if counters["input_tokens"] != 1013 || counters["uncached_input_tokens"] != 13 || counters["cache_write_tokens"] != 1000 || counters["total_tokens"] != 1065 {
		t.Fatal("exclusive composition not normalized", counters)
	}
	if _, err := bedrockUsageCounters([]byte(`{"inputTokens":13,"outputTokens":52,"totalTokens":65,"cacheDetails":[{"inputTokens":600,"ttl":"2h"}]}`)); err == nil {
		t.Fatal("undocumented TTL accepted")
	}
	if _, err := bedrockUsageCounters([]byte(`{"inputTokens":13,"outputTokens":52,"cacheWriteInputTokens":5,"cacheDetails":[{"inputTokens":600,"ttl":"1h"}]}`)); err == nil {
		t.Fatal("conflicting cache details accepted")
	}
	counters, _ = bedrockUsageCounters([]byte(`{"inputTokens":13,"outputTokens":52,"totalTokens":65,"cacheDetails":[{"inputTokens":600,"ttl":"1h"},{"inputTokens":400,"ttl":"5m"}]}`))
	if counters["cache_write_tokens"] != 1000 || counters["input_tokens"] != 1013 {
		t.Fatal("per-TTL cache writes not summed", counters)
	}
	counters, _ = bedrockUsageCounters([]byte(`{"inputTokens":13,"outputTokens":52,"totalTokens":99}`))
	if _, known := counters["total_tokens"]; known || counters["input_tokens"] != 13 {
		t.Fatal("inconsistent total accepted", counters)
	}
	if _, err = bedrockUsageCounters([]byte(`{"inputTokens":-1}`)); err == nil {
		t.Fatal("negative usage accepted")
	}
	counters, _ = bedrockUsageCounters([]byte(`{}`))
	if len(counters) != 0 {
		t.Fatal("missing usage became zero", counters)
	}
}

func TestBedrockInvokeStreamForwardsWithoutInventingUsage(t *testing.T) {
	stream := append(encodeEventMessage([][2]string{{":event-type", "chunk"}, {":message-type", "event"}}, []byte(`{"bytes":"eyJ0eXBlIjoibWVzc2FnZV9zdG9wIn0="}`)), encodeEventMessage([][2]string{{":event-type", "chunk"}, {":message-type", "event"}}, []byte(`{"bytes":"e30="}`))...)
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/invoke") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"msg","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(stream)
	})
	s := providerSurface(t, up.URL, Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockInvoke, []string{"m.model-v1:0"}, "bedrock.invoke", "bedrock.invoke-stream")
	for _, path := range []string{"/model/m.model-v1:0/invoke", "/model/m.model-v1:0/invoke-with-response-stream"} {
		lease := &requestCountModelLease{}
		g := providerGateway(t, s, NoCredential(), lease, nil)
		w := providerServe(g, "POST", path, `{"anthropic_version":"bedrock-2023-05-31","max_tokens":5,"messages":[]}`, nil)
		if w.Code != 200 || len(lease.observations) != 0 || len(lease.results) != 1 || !lease.results[0].ResponseComplete || lease.results[0].Err != nil {
			t.Fatalf("%s: model-specific usage must stay unknown with a complete exchange: %+v %+v", path, lease.observations, lease.results)
		}
	}
}

func TestEventStreamFraming(t *testing.T) {
	frame := encodeEventMessage([][2]string{{":event-type", "metadata"}}, []byte(`{}`))
	if binary.BigEndian.Uint32(frame[:4]) != uint32(len(frame)) {
		t.Fatal("length prefix")
	}
	bad := append([]byte(nil), frame...)
	bad[9] ^= 1
	for _, input := range [][]byte{bad, frame[:len(frame)-2]} {
		if _, err := readEventMessage(newBufReader(input)); err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	message, err := readEventMessage(newBufReader(frame))
	if err != nil || message.headers[":event-type"] != "metadata" || string(message.payload) != "{}" {
		t.Fatal(err, message)
	}
}

func TestCompatibleBasePathNoCredentialAndAdvisoryUsage(t *testing.T) {
	const response = `{"id":"c","model":"llama3.1:8b","choices":[{"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	})
	var observed []UsageObservation
	g := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderOpenAICompatible, BasePath: "/openai/v1"}, ProtocolOpenAIChat, []string{"llama3.1:8b"}, "compat.chat-completions"), NoCredential(), nil, func(o *Options) {
		o.Observe = func(_ context.Context, _ Owner, u UsageObservation) { observed = append(observed, u) }
	})
	w := providerServe(g, "POST", "/openai/v1/chat/completions", `{"model":"llama3.1:8b","stream":true,"messages":[]}`, nil)
	if w.Code != 200 || up.calls()[0].path != "/openai/v1/chat/completions" || up.calls()[0].body != `{"model":"llama3.1:8b","stream":true,"messages":[]}` {
		t.Fatal(w.Code, up.calls())
	}
	assertNoCallerCredentials(t, up.calls()[0].header, "")
	if up.calls()[0].header.Get("Authorization") != "" {
		t.Fatal("caller credential forwarded to a no-credential origin")
	}
	if len(observed) != 1 || observed[0].Counters["input_tokens"] != 4 || observed[0].Counters["output_tokens"] != 2 {
		t.Fatal(observed)
	}
	if w := providerServe(g, "POST", "/v1/chat/completions", `{"model":"llama3.1:8b"}`, nil); w.Code != 404 {
		t.Fatal("path outside base accepted", w.Code)
	}
	if Coverage(g.Surface().Operations[0]).TokenProfile != "" {
		t.Fatal("generic compatible server claims token coverage")
	}
}

func TestProviderRedirectNeverCarriesCredentialElsewhere(t *testing.T) {
	other := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
	})
	g := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderOpenAI}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions"), headerCredential("Authorization", "Bearer "), nil, nil)
	w := providerServe(g, "POST", "/v1/chat/completions", `{"model":"gpt-x"}`, nil)
	if w.Code != 502 || len(other.calls()) != 0 || strings.Contains(w.Body.String(), providerSecret) {
		t.Fatal("redirect followed or credential exposed", w.Code, other.calls())
	}
	failing := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderOpenAI}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions"), CredentialFunc(func(context.Context, *http.Request) error {
		return fmt.Errorf("secret %s unavailable", providerSecret)
	}), nil, nil)
	before := len(up.calls())
	w = providerServe(failing, "POST", "/v1/chat/completions", `{"model":"gpt-x"}`, nil)
	if w.Code != 503 || strings.Contains(w.Body.String(), providerSecret) || len(up.calls()) != before {
		t.Fatal("credential failure leaked or dispatched", w.Code, w.Body.String())
	}
}

func TestProviderLimitHeadersPassthroughAndRetryAfter(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.Header().Set("X-Ratelimit-Remaining-Requests", "0")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"type":"rate_limit"}}`)
	})
	for _, passthrough := range []bool{false, true} {
		var seen providerlimits.Limits
		g := providerGateway(t, providerSurface(t, up.URL, Provider{ID: ProviderOpenAI, LimitHeaderPassthrough: passthrough}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions"), headerCredential("Authorization", "Bearer "), nil, func(o *Options) {
			o.ObserveLimits = func(_ context.Context, _ Owner, l providerlimits.Limits) { seen = l }
		})
		w := providerServe(g, "POST", "/v1/chat/completions", `{"model":"gpt-x"}`, nil)
		if w.Code != 429 || w.Body.String() != `{"error":{"type":"rate_limit"}}` || w.Header().Get("Retry-After") != "7" {
			t.Fatal(w.Code, w.Header())
		}
		if (w.Header().Get("X-Ratelimit-Remaining-Requests") != "") != passthrough {
			t.Fatalf("passthrough=%t but header forwarding differed", passthrough)
		}
		if seen.Classification != providerlimits.RateLimited || seen.RetryAfterMS == nil || *seen.RetryAfterMS != 7000 || seen.Requests == nil || *seen.Requests.Remaining != 0 {
			t.Fatalf("limits not parsed: %+v", seen)
		}
	}
}

func TestProviderOptionsRequireCredential(t *testing.T) {
	s := providerSurface(t, "http://127.0.0.1:1", Provider{ID: ProviderOpenAI}, ProtocolOpenAIChat, []string{"gpt-x"}, "openai.v1.chat-completions")
	opts := options(t, s.Origin, nil)
	opts.LiveDialer = nil
	if _, err := NewGateway(s, opts); err == nil {
		t.Fatal("provider gateway accepted a Google token source without a provider credential")
	}
	opts.Credential = NoCredential()
	opts.UpstreamHeaders = http.Header{"X-Goog-User-Project": {"p"}}
	if _, err := NewGateway(s, opts); err == nil {
		t.Fatal("provider gateway accepted Google trusted headers")
	}
}

func newBufReader(b []byte) *bufio.Reader { return bufio.NewReader(bytes.NewReader(b)) }
