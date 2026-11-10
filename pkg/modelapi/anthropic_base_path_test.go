package modelapi

import (
	"fmt"
	"net/http"
	"testing"
)

// An Anthropic-compatible endpoint served under a path prefix (for example
// https://host/api/anthropic/v1/messages) is reached through base_path, while
// clients keep calling /v1/messages.
func TestAnthropicBasePathPrefixesUpstreamPaths(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"m","type":"message","model":"claude-x","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	p := Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01", BasePath: "/api/anthropic"}
	s := providerSurface(t, up.URL, p, ProtocolAnthropicMessages, []string{"claude-x"}, "anthropic.messages")
	g := providerGateway(t, s, headerCredential("X-Api-Key", ""), nil, nil)
	w := providerServe(g, "POST", "/v1/messages", `{"model":"claude-x","max_tokens":4,"messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls := up.calls(); len(calls) != 1 || calls[0].path != "/api/anthropic/v1/messages" {
		t.Fatalf("upstream path: %+v", calls)
	}
	// Without a base path the upstream path is the client path.
	plain := providerSurface(t, up.URL, Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01"}, ProtocolAnthropicMessages, []string{"claude-x"}, "anthropic.messages")
	if w := providerServe(providerGateway(t, plain, headerCredential("X-Api-Key", ""), nil, nil), "POST", "/v1/messages", `{"model":"claude-x","max_tokens":4,"messages":[]}`, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if calls := up.calls(); calls[len(calls)-1].path != "/v1/messages" {
		t.Fatalf("upstream path: %+v", calls)
	}
}

func TestAnthropicBasePathValidation(t *testing.T) {
	base := func(path string) error {
		ops, err := SelectProviderOperations(ProviderAnthropic, "anthropic.messages")
		if err != nil {
			t.Fatal(err)
		}
		p := Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01", BasePath: path}
		return Surface{ID: "s", Protocol: ProtocolAnthropicMessages, Origin: "https://api.example.com", Models: []string{"claude-x"}, Operations: ops, Provider: &p}.Validate()
	}
	for _, good := range []string{"", "/api/anthropic", "/anthropic", "/a/b/c"} {
		if err := base(good); err != nil {
			t.Errorf("%q rejected: %v", good, err)
		}
	}
	for _, bad := range []string{"api/anthropic", "/api/anthropic/", "/api/../x", "/./x", "/x?y=1", "/x#f", "//x", "/x y", "/a/b/c/d/e", "/x%2e%2e"} {
		if base(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// base_path stays closed for the other providers that never had it.
	ops, _ := SelectProviderOperations(ProviderOpenAI, "openai.v1.chat-completions")
	p := Provider{ID: ProviderOpenAI, BasePath: "/x"}
	if (Surface{ID: "s", Protocol: ProtocolOpenAIChat, Origin: "https://api.example.com", Models: []string{"m"}, Operations: ops, Provider: &p}).Validate() == nil {
		t.Error("openai base_path accepted")
	}
}
