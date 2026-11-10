package modelapi

import (
	"context"
	"fmt"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"net/http"
	"strings"
	"testing"
)

func geminiKey() Credential { return headerCredential("X-Goog-Api-Key", "") }

func daSurface(t *testing.T, origin string, ids ...string) Surface {
	return providerSurface(t, origin, Provider{ID: ProviderGeminiDeveloper}, ProtocolGoogleGenAI, []string{"gemini-x"}, ids...)
}

func TestGeminiDeveloperGenerateCredentialQueryAndUsage(t *testing.T) {
	const response = `{"candidates":[{"index":0,"finishReason":"STOP"}],"modelVersion":"gemini-x-001","usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":4,"candidatesTokenCount":5,"thoughtsTokenCount":2,"totalTokenCount":17}}`
	stream := "data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"a\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":1,\"totalTokenCount\":11}}\n\n" +
		"data: {\"candidates\":[{\"index\":0,\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":5,\"totalTokenCount\":15}}\n\n"
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") == "sse" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, stream)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	})
	s := daSurface(t, up.URL, "gemini.models.generateContent", "gemini.models.streamGenerateContent")
	body := `{"contents":[{"parts":[{"text":"hi"}]}]}`
	headers := map[string]string{"X-Goog-Api-Key": "caller-key"}

	lease := &modelLease{}
	g := providerGateway(t, s, geminiKey(), lease, nil)
	w := providerServe(g, "POST", "/v1beta/models/gemini-x:generateContent?key=caller-google-key&alt=json&foo=1", body, headers)
	if w.Code != 200 || w.Body.String() != response {
		t.Fatalf("response changed: %d %s", w.Code, w.Body.String())
	}
	call := up.calls()[0]
	if call.path != "/v1beta/models/gemini-x:generateContent" || call.query != "" || call.body != body {
		t.Fatalf("upstream request changed: %+v", call)
	}
	if call.header.Get("X-Goog-Api-Key") != providerSecret || strings.Contains(call.query, "caller") {
		t.Fatalf("operator credential must be the only key, in the header: %v", call.header)
	}
	assertNoCallerCredentials(t, call.header, "")
	value := singleTerminal(t, lease)
	if value.Counters["input_tokens"] != 10 || value.Counters["output_tokens"] != 5 || value.Counters["thought_tokens"] != 2 || value.Counters["cached_tokens"] != 4 {
		t.Fatalf("usage %+v", value.Counters)
	}

	lease = &modelLease{}
	g = providerGateway(t, s, geminiKey(), lease, nil)
	w = providerServe(g, "POST", "/v1beta/models/gemini-x:streamGenerateContent?alt=sse&key=caller-google-key", body, headers)
	if w.Code != 200 || w.Body.String() != stream {
		t.Fatalf("stream changed: %d %s", w.Code, w.Body.String())
	}
	if last := up.calls()[1]; last.query != "alt=sse" {
		t.Fatalf("alt=sse must be forwarded and key= dropped: %q", last.query)
	}
	if value = singleTerminal(t, lease); value.Counters["output_tokens"] != 5 || value.Counters["input_tokens"] != 10 {
		t.Fatalf("stream usage %+v", value.Counters)
	}

	// Model placement: unlisted path model and a differing body model never reach upstream.
	before := len(up.calls())
	if w = providerServe(g, "POST", "/v1beta/models/other:generateContent", body, nil); w.Code != 403 {
		t.Fatal("unlisted model accepted", w.Code)
	}
	g = providerGateway(t, s, geminiKey(), nil, nil)
	if w = providerServe(g, "POST", "/v1beta/models/gemini-x:generateContent", `{"model":"models/other","contents":[]}`, nil); w.Code != 403 {
		t.Fatal("body model differing from path accepted", w.Code)
	}
	if w = providerServe(g, "POST", "/v1beta/models/gemini-x:generateContent", `{"model":"models/gemini-x","contents":[]}`, nil); w.Code != 200 {
		t.Fatal("models/ prefixed body model refused", w.Code)
	}
	if len(up.calls()) != before+1 {
		t.Fatal("rejected requests reached upstream")
	}
}

func TestGeminiDeveloperCountTokensAndEmbedUsage(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, ":countTokens"):
			fmt.Fprint(w, `{"totalTokens":1316,"cachedContentTokenCount":1024,"usageMetadata":{"promptTokenCount":999}}`)
		case strings.HasSuffix(r.URL.Path, ":embedContent"):
			fmt.Fprint(w, `{"embedding":{"values":[0.1]},"usageMetadata":{"promptTokenCount":18}}`)
		default:
			fmt.Fprint(w, `{"embeddings":[{"values":[0.1]}],"usageMetadata":{"promptTokenCount":7}}`)
		}
	})
	s := daSurface(t, up.URL, "gemini.models.countTokens", "gemini.models.embedContent", "gemini.models.batchEmbedContents")

	lease := &modelLease{}
	g := providerGateway(t, s, geminiKey(), lease, nil)
	if w := providerServe(g, "POST", "/v1beta/models/gemini-x:countTokens", `{"contents":[]}`, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(lease.observations) != 0 {
		t.Fatalf("an estimate must never be recorded as usage: %+v", lease.observations)
	}
	if len(lease.results) != 1 || !lease.results[0].ResponseComplete {
		t.Fatalf("request-count completion expected: %+v", lease.results)
	}

	lease = &modelLease{}
	g = providerGateway(t, s, geminiKey(), lease, nil)
	if w := providerServe(g, "POST", "/v1beta/models/gemini-x:embedContent", `{"model":"models/gemini-x","content":{"parts":[{"text":"x"}]}}`, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	value := singleTerminal(t, lease)
	if len(value.Counters) != 1 || value.Counters["input_tokens"] != 18 {
		t.Fatalf("embedding usage must be input tokens only: %+v", value.Counters)
	}

	lease = &modelLease{}
	g = providerGateway(t, s, geminiKey(), lease, nil)
	if w := providerServe(g, "POST", "/v1beta/models/gemini-x:batchEmbedContents", `{"requests":[{"model":"models/gemini-x","content":{"parts":[{"text":"x"}]}}]}`, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if value = singleTerminal(t, lease); value.Counters["input_tokens"] != 7 {
		t.Fatalf("batch embedding usage %+v", value.Counters)
	}
	if w := providerServe(g, "POST", "/v1beta/models/gemini-x:batchEmbedContents", `{"requests":[{"model":"models/other","content":{}}]}`, nil); w.Code != 403 {
		t.Fatal("batch element with another model accepted", w.Code)
	}
}

// Live Gemini Developer API, 2026-11-10: gemini-embedding-001 reports usage
// only as a top-level tokenCount (an int64 string); gemini-embedding-2 also
// carries usageMetadata.
func TestGeminiDeveloperEmbedTopLevelTokenCount(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, ":embedContent") {
			fmt.Fprint(w, `{"embedding":{"values":[-0.015,0.0096]},"tokenCount":"2"}`)
			return
		}
		fmt.Fprint(w, `{"embeddings":[{"values":[-0.015]},{"values":[-0.010]}],"tokenCount":"4"}`)
	})
	s := daSurface(t, up.URL, "gemini.models.embedContent", "gemini.models.batchEmbedContents")
	for path, want := range map[string]int64{"embedContent": 2, "batchEmbedContents": 4} {
		lease := &modelLease{}
		g := providerGateway(t, s, geminiKey(), lease, nil)
		body := `{"model":"models/gemini-x","content":{"parts":[{"text":"x"}]}}`
		if path == "batchEmbedContents" {
			body = `{"requests":[{"model":"models/gemini-x","content":{"parts":[{"text":"x"}]}}]}`
		}
		if w := providerServe(g, "POST", "/v1beta/models/gemini-x:"+path, body, nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if value := singleTerminal(t, lease); len(value.Counters) != 1 || value.Counters["input_tokens"] != want {
			t.Fatalf("%s: %+v", path, value.Counters)
		}
	}
}

func TestGeminiDeveloperCachedContentOwnership(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/v1beta/cachedContents" {
			fmt.Fprint(w, `{"name":"cachedContents/abc","model":"models/gemini-x","usageMetadata":{"totalTokenCount":4096}}`)
			return
		}
		fmt.Fprint(w, `{"name":"cachedContents/abc","usageMetadata":{"totalTokenCount":4096}}`)
	})
	s := daSurface(t, up.URL, "gemini.cachedContents.create", "gemini.cachedContents.get", "gemini.cachedContents.list", "gemini.cachedContents.update", "gemini.cachedContents.delete", "gemini.models.generateContent")
	lease := &modelLease{}
	own := newOwnership()
	g := providerGateway(t, s, geminiKey(), lease, func(o *Options) { o.Ownership = own })
	if w := providerServe(g, "POST", "/v1beta/cachedContents", `{"model":"models/gemini-x","contents":[]}`, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, o := range lease.observations {
		if len(o.Counters) != 0 {
			t.Fatalf("cache size must not be recorded as usage: %+v", o)
		}
	}
	for _, call := range []struct{ method, path, body string }{
		{"GET", "/v1beta/cachedContents/abc", ""},
		{"PATCH", "/v1beta/cachedContents/abc?updateMask=ttl&key=caller", `{"ttl":"600s"}`},
		{"DELETE", "/v1beta/cachedContents/abc", ""},
	} {
		if w := providerServe(g, call.method, call.path, call.body, nil); w.Code != 200 {
			t.Fatalf("owner %s %s: %d", call.method, call.path, w.Code)
		}
		before := len(up.calls())
		if w := providerServe(g, call.method, call.path, call.body, map[string]string{"Test-Principal": "mallory"}); w.Code != 403 || len(up.calls()) != before {
			t.Fatalf("foreign %s %s reached upstream: %d", call.method, call.path, w.Code)
		}
	}
	if patch := up.calls()[2]; patch.query != "updateMask=ttl" {
		t.Fatalf("updateMask only: %q", patch.query)
	}
	// Listing is a shared view of the upstream key's project: explicit authorization only.
	before := len(up.calls())
	if w := providerServe(g, "GET", "/v1beta/cachedContents?pageSize=5", "", nil); w.Code != 403 || len(up.calls()) != before {
		t.Fatal("shared listing served without authorization", w.Code)
	}
	g = providerGateway(t, s, geminiKey(), nil, func(o *Options) {
		o.Ownership = own
		o.AllowShared = func(_ context.Context, _ contracts.RequestContext, _ Surface, _ Operation) bool { return true }
	})
	if w := providerServe(g, "GET", "/v1beta/cachedContents?pageSize=5&pageToken=t1&key=caller", "", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if last := up.calls()[len(up.calls())-1]; last.query != "pageSize=5&pageToken=t1" {
		t.Fatalf("list query %q", last.query)
	}
	// A cache reference is ownership-checked before generation.
	before = len(up.calls())
	if w := providerServe(g, "POST", "/v1beta/models/gemini-x:generateContent", `{"cachedContent":"cachedContents/abc","contents":[]}`, map[string]string{"Test-Principal": "mallory"}); w.Code != 403 || len(up.calls()) != before {
		t.Fatal("foreign cachedContent reference reached upstream", w.Code)
	}
	if w := providerServe(g, "POST", "/v1beta/models/gemini-x:generateContent", `{"cachedContent":"cachedContents/abc","contents":[]}`, nil); w.Code != 200 {
		t.Fatal("owner cache reference refused", w.Code, w.Body.String())
	}
}

func TestGeminiDeveloperInteractionsOwnershipAndRevision(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"int_1","object":"interaction","model":"gemini-x","status":"completed","usage":{"total_input_tokens":10,"total_output_tokens":4,"total_tokens":14}}`)
	})
	s := daSurface(t, up.URL, "gemini.interactions.create", "gemini.interactions.get", "gemini.interactions.cancel", "gemini.interactions.delete")
	create, get := &modelLease{}, &modelLease{}
	own := newOwnership()
	g := providerGateway(t, s, geminiKey(), create, func(o *Options) { o.Ownership = own })
	if w := providerServe(g, "POST", "/v1beta/interactions", `{"model":"gemini-x","input":"hi"}`, map[string]string{"Api-Revision": "2026-05-20"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if up.calls()[0].header.Get("Api-Revision") != "2026-05-20" {
		t.Fatal("revision pin not forwarded")
	}
	if v := singleTerminal(t, create); v.Counters["input_tokens"] != 10 || v.Counters["output_tokens"] != 4 {
		t.Fatalf("create usage %+v", v.Counters)
	}
	if w := providerServe(g, "POST", "/v1beta/interactions", `{"model":"gemini-x","input":"hi"}`, map[string]string{"Api-Revision": "latest"}); w.Code != 400 {
		t.Fatal("malformed revision forwarded", w.Code)
	}
	if w := providerServe(g, "POST", "/v1beta/interactions", `{"agent":"deep-research","input":"hi"}`, nil); w.Code != 403 {
		t.Fatal("agent accepted", w.Code)
	}
	g = providerGateway(t, s, geminiKey(), get, func(o *Options) { o.Ownership = own })
	if w := providerServe(g, "GET", "/v1beta/interactions/int_1", "", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, o := range get.observations {
		if len(o.Counters) != 0 {
			t.Fatalf("retrieval recorded usage a second time: %+v", o)
		}
	}
	before := len(up.calls())
	for _, call := range [][2]string{{"GET", "/v1beta/interactions/int_1"}, {"POST", "/v1beta/interactions/int_1/cancel"}, {"DELETE", "/v1beta/interactions/int_1"}} {
		if w := providerServe(g, call[0], call[1], "", map[string]string{"Test-Principal": "mallory"}); w.Code != 403 {
			t.Fatalf("foreign %s %s: %d", call[0], call[1], w.Code)
		}
	}
	if len(up.calls()) != before {
		t.Fatal("foreign interaction access reached upstream")
	}
}

func TestOpenAIEmbeddingsModerationsAndResponseLifecycle(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/embeddings":
			fmt.Fprint(w, `{"object":"list","data":[],"model":"emb","usage":{"prompt_tokens":8,"total_tokens":8}}`)
		case r.URL.Path == "/v1/moderations":
			fmt.Fprint(w, `{"id":"modr-1","model":"omni-moderation-latest","results":[]}`)
		case r.URL.Path == "/v1/responses" && r.Method == "POST":
			fmt.Fprint(w, `{"id":"resp_1","object":"response","model":"gpt-x","status":"completed","usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":2},"output_tokens":9,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":21}}`)
		default:
			// Retrieval of the same, completed, response carries the same usage.
			fmt.Fprint(w, `{"id":"resp_1","object":"response","model":"gpt-x","status":"completed","usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":2},"output_tokens":9,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":21}}`)
		}
	})
	p := Provider{ID: ProviderOpenAI}
	models := []string{"gpt-x", "emb", DefaultModerationModel}
	creds := headerCredential("Authorization", "Bearer ")

	// Embeddings: exact input tokens, no output counter.
	lease := &modelLease{}
	g := providerGateway(t, providerSurface(t, up.URL, p, ProtocolOpenAIEmbeddings, models, "openai.v1.embeddings"), creds, lease, nil)
	if w := providerServe(g, "POST", "/v1/embeddings", `{"model":"emb","input":"x"}`, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if v := singleTerminal(t, lease); v.Counters["input_tokens"] != 8 || v.Counters["total_tokens"] != 8 || len(v.Counters) != 2 {
		t.Fatalf("embedding usage %+v", v.Counters)
	}

	// Moderations: default model is authorized, nothing is recorded, no resource is pinned.
	lease = &modelLease{}
	g = providerGateway(t, providerSurface(t, up.URL, p, ProtocolOpenAIModerations, models, "openai.v1.moderations"), creds, lease, nil)
	if w := providerServe(g, "POST", "/v1/moderations", `{"input":"x"}`, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(lease.observations) != 0 || strings.Contains(strings.Join(lease.calls, ","), "resource:") {
		t.Fatalf("moderation recorded usage or a resource: %+v", lease.calls)
	}
	g = providerGateway(t, providerSurface(t, up.URL, p, ProtocolOpenAIModerations, []string{"emb"}, "openai.v1.moderations"), creds, nil, nil)
	if w := providerServe(g, "POST", "/v1/moderations", `{"input":"x"}`, nil); w.Code != 403 {
		t.Fatal("default moderation model bypassed the allowlist", w.Code)
	}

	// Responses: create settles once; retrieval, cancel and delete never record usage.
	s := providerSurface(t, up.URL, p, ProtocolOpenAIResponses, models, "openai.v1.responses", "openai.v1.responses-retrieve", "openai.v1.responses-cancel", "openai.v1.responses-delete")
	own := newOwnership()
	var ledger []UsageObservation
	mutate := func(o *Options) {
		o.Ownership = own
		o.Observe = func(_ context.Context, _ Owner, v UsageObservation) { ledger = append(ledger, v) }
	}
	g = providerGateway(t, s, creds, nil, mutate)
	if w := providerServe(g, "POST", "/v1/responses", `{"model":"gpt-x","input":"x"}`, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	created := len(ledger)
	if created == 0 {
		t.Fatal("create recorded no usage")
	}
	for _, call := range [][2]string{{"GET", "/v1/responses/resp_1?stream=false&starting_after=3&key=caller"}, {"POST", "/v1/responses/resp_1/cancel"}, {"DELETE", "/v1/responses/resp_1"}} {
		if w := providerServe(g, call[0], call[1], "", nil); w.Code != 200 {
			t.Fatalf("owner %s: %d", call[1], w.Code)
		}
	}
	if len(ledger) != created {
		t.Fatalf("a retrieved response was counted again: %+v", ledger[created:])
	}
	if got := up.calls()[len(up.calls())-3]; got.query != "starting_after=3&stream=false" {
		t.Fatalf("retrieve query %q", got.query)
	}
	// ...and under a lease the retrieved usage is not offered to the quota host either.
	lease = &modelLease{}
	g = providerGateway(t, s, creds, lease, mutate)
	if w := providerServe(g, "GET", "/v1/responses/resp_1", "", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, o := range lease.observations {
		if len(o.Counters) != 0 {
			t.Fatalf("lease received usage from a retrieval: %+v", o)
		}
	}
	if !lease.results[0].ResponseComplete || lease.results[0].Err != nil {
		t.Fatalf("request-count completion expected: %+v", lease.results)
	}
	before := len(up.calls())
	for _, call := range [][2]string{{"GET", "/v1/responses/resp_1"}, {"POST", "/v1/responses/resp_1/cancel"}, {"DELETE", "/v1/responses/resp_1"}, {"GET", "/v1/responses/resp_unknown"}} {
		principal := "mallory"
		if strings.HasSuffix(call[1], "unknown") {
			principal = ""
		}
		if w := providerServe(g, call[0], call[1], "", map[string]string{"Test-Principal": principal}); w.Code != 403 {
			t.Fatalf("%s %s: %d", call[0], call[1], w.Code)
		}
	}
	if len(up.calls()) != before {
		t.Fatal("unowned response access reached upstream")
	}
	if w := providerServe(g, "POST", "/v1/responses/resp_1/cancel", `{"x":1}`, nil); w.Code != 400 {
		t.Fatal("cancel body accepted", w.Code)
	}
}

func TestAnthropicCountTokensIsNeverUsage(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"input_tokens":403}`)
	})
	p := Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01"}
	s := providerSurface(t, up.URL, p, ProtocolAnthropicMessages, []string{"claude-x"}, "anthropic.messages-count-tokens")
	lease := &modelLease{}
	g := providerGateway(t, s, headerCredential("x-api-key", ""), lease, nil)
	w := providerServe(g, "POST", "/v1/messages/count_tokens", `{"model":"claude-x","messages":[]}`, map[string]string{"Anthropic-Version": "2023-06-01"})
	if w.Code != 200 || w.Body.String() != `{"input_tokens":403}` {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(lease.observations) != 0 {
		t.Fatalf("an estimate was recorded as usage: %+v", lease.observations)
	}
	call := up.calls()[0]
	if call.header.Get("X-Api-Key") != providerSecret || call.header.Get("Anthropic-Version") != "2023-06-01" {
		t.Fatalf("credential or version: %v", call.header)
	}
	if w = providerServe(g, "POST", "/v1/messages/count_tokens", `{"model":"other","messages":[]}`, nil); w.Code != 403 {
		t.Fatal("unlisted model counted", w.Code)
	}
}

func TestAzureEmbeddingsPaths(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","usage":{"prompt_tokens":3,"total_tokens":3}}`)
	})
	p := Provider{ID: ProviderAzureOpenAI, APIVersion: "2024-10-21"}
	s := providerSurface(t, up.URL, p, ProtocolOpenAIEmbeddings, []string{"emb"}, "azure.v1.embeddings", "azure.deployments.embeddings")
	lease := &modelLease{}
	g := providerGateway(t, s, headerCredential("api-key", ""), lease, nil)
	if w := providerServe(g, "POST", "/openai/v1/embeddings", `{"model":"emb","input":"x"}`, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := providerServe(g, "POST", "/openai/deployments/emb/embeddings?api-version=caller", `{"input":"x"}`, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if calls := up.calls(); calls[0].query != "" || calls[1].query != "api-version=2024-10-21" {
		t.Fatalf("api-version must be operator pinned: %+v", calls)
	}
	if len(lease.terminalObservations()) != 0 {
		// each request used the same lease object; both exchanges report input tokens only
		for _, o := range lease.terminalObservations() {
			if o.Counters["input_tokens"] != 3 {
				t.Fatalf("usage %+v", o)
			}
		}
	}
}

func TestUtilitySurfaceValidation(t *testing.T) {
	s := Surface{ID: "da", Protocol: ProtocolGoogleGenAI, Origin: "https://example.com", Models: []string{"gemini-x"}, Provider: &Provider{ID: ProviderGeminiDeveloper}}
	ops, _ := SelectProviderOperations(ProviderGeminiDeveloper, "gemini.models.generateContent")
	s.Operations = ops
	if s.Validate() == nil {
		t.Fatal("foreign origin accepted for the Developer API")
	}
	s.Origin = "https://generativelanguage.googleapis.com"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	d, _ := ProviderByID(ProviderGeminiDeveloper)
	if d.APIKeyHeader != "x-goog-api-key" || len(d.CredentialSchemes) != 1 || d.CredentialSchemes[0] != SchemeHeader {
		t.Fatalf("credential contract %+v", d)
	}
	for _, id := range []string{"gemini.models.generateContent", "openai.v1.embeddings", "anthropic.messages-count-tokens", "azure.v1.responses-delete"} {
		var found bool
		for _, p := range []string{ProviderGeminiDeveloper, ProviderOpenAI, ProviderAnthropic, ProviderAzureOpenAI} {
			for _, o := range ProviderOperations(p) {
				found = found || o.ID == id
			}
		}
		want := map[string]string{"gemini.models.generateContent": "gemini-text", "openai.v1.embeddings": "embedding-input"}[id]
		if !found || !Coverage(Operation{ID: id}).RequestCount || Coverage(Operation{ID: id}).TokenProfile != want {
			t.Fatalf("%s coverage: want request-count with token profile %q", id, want)
		}
	}
}
