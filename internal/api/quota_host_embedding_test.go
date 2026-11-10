package api

import (
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

// Gemini Developer API generation/Interactions settle under the Gemini token
// profiles; embeddings (OpenAI, Gemini Developer API, Vertex predict) settle
// under the input-only embedding-input profile.
func TestManagedDeveloperAPIAndEmbeddingProfilesSettle(t *testing.T) {
	daKey := &toolcredential.Auth{Type: toolcredential.APIKey, In: toolcredential.InHeader, Name: "x-goog-api-key", SecretRef: "provider-key"}
	da := modelapi.Provider{ID: modelapi.ProviderGeminiDeveloper}
	gemini := map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3, "thought_tokens": 3}
	embedding := map[string]int64{"input_tokens": 2}
	tests := []tokenProfileCase{
		{name: "da-generate", profile: quotaProfileGemini, operation: "gemini.models.generateContent", provider: modelapi.ProviderGeminiDeveloper, path: "/v1beta/models/model-v1:generateContent", body: quotaHostModelBody, response: quotaHostModelTerminal, settings: da, protocol: modelapi.ProtocolGoogleGenAI, auth: daKey, meters: gemini, want: map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 2, "thought_tokens": 1, "usd": 14}},
		{name: "da-interaction", profile: quotaProfileInteraction, operation: "gemini.interactions.create", provider: modelapi.ProviderGeminiDeveloper, path: "/v1beta/interactions", body: `{"model":"model-v1","input":"hello","generation_config":{"max_output_tokens":3},"store":false}`, response: `{"id":"interaction-1","model":"model-v1","status":"completed","usage":{"total_input_tokens":7,"total_cached_tokens":2,"total_output_tokens":2,"total_thought_tokens":1,"total_tokens":10}}`, settings: da, protocol: modelapi.ProtocolGoogleGenAI, auth: daKey, meters: gemini, want: map[string]int64{"uncached_input_tokens": 5, "cached_tokens": 2, "output_tokens": 2, "thought_tokens": 1, "usd": 10 + 2 + 6 + 3}},
		{name: "openai-embeddings", profile: quotaProfileEmbedding, operation: "openai.v1.embeddings", provider: modelapi.ProviderOpenAI, path: "/v1/embeddings", body: `{"model":"model-v1","input":["a","b"]}`, response: `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1]}],"model":"model-v1","usage":{"prompt_tokens":8,"total_tokens":8}}`, settings: modelapi.Provider{ID: modelapi.ProviderOpenAI}, protocol: modelapi.ProtocolOpenAIEmbeddings, auth: bearerRef, meters: embedding, want: map[string]int64{"input_tokens": 8, "usd": 16}},
		{name: "da-embed", profile: quotaProfileEmbedding, operation: "gemini.models.embedContent", provider: modelapi.ProviderGeminiDeveloper, path: "/v1beta/models/model-v1:embedContent", body: `{"model":"models/model-v1","content":{"parts":[{"text":"x"}]}}`, response: `{"embedding":{"values":[0.1]},"usageMetadata":{"promptTokenCount":5}}`, settings: da, protocol: modelapi.ProtocolGoogleGenAI, auth: daKey, meters: embedding, want: map[string]int64{"input_tokens": 5, "usd": 10}},
		{name: "vertex-embed-predict", google: true, profile: quotaProfileEmbedding, operation: "models.predict", provider: "google", path: "/v1/projects/project/locations/us-central1/publishers/google/models/model-v1:predict", body: `{"instances":[{"content":"a"},{"content":"b"}]}`, response: `{"predictions":[{"embeddings":{"statistics":{"token_count":3,"truncated":false},"values":[0.1]}},{"embeddings":{"statistics":{"token_count":4,"truncated":false},"values":[0.2]}}]}`, meters: embedding, want: map[string]int64{"input_tokens": 7, "usd": 14}},
		// Embedding output counters do not exist: an unbounded request (non-text input) is refused before dispatch.
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { runTokenProfileCase(t, test) })
	}
	for _, body := range []string{`{"model":"model-v1","input":[[1,2,3]]}`, `{"model":"model-v1","input":"x","extra":true}`} {
		if quotaEmbeddingRequest("openai.v1.embeddings", []byte(body)) == nil {
			t.Fatal("unbounded embedding request accepted", body)
		}
	}
}
