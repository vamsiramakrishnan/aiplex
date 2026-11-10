package api

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// An Azure OpenAI SDK pointed at an access point authenticates with api-key and
// uses the Azure path shapes. Authentication goes through the production
// middleware order (NativeKeyHeaders, then the WIF validator).
func TestAccessPointAzureSDKAuthenticatesWithAPIKeyHeader(t *testing.T) {
	f := newAPFixture(t)
	chain, upstream, logs := nativeKeyChain(t, f)
	const query = "?api-version=2024-10-21"
	chat := `{"model":"chat-model","messages":[{"role":"user","content":"hi"}]}`
	for _, c := range []struct{ name, path, body string }{
		{"v1", "/ap/ide/openai/v1/chat/completions" + query, chat},
		{"deployment with SDK body", "/ap/ide/openai/deployments/chat-model/chat/completions" + query, chat},
		{"deployment without body model", "/ap/ide/openai/deployments/chat-model/chat/completions" + query, `{"messages":[{"role":"user","content":"hi"}]}`},
		{"openai-shaped wire", "/ap/ide/v1/chat/completions", chat},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := len(f.upstreamCalls())
			w := nativeKeyDo(chain, "POST", c.path, c.body, "Content-Type", "application/json", "Api-Key", nativeKeyToken)
			if w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			calls := f.upstreamCalls()
			if len(calls) != before+1 || calls[before].Path != "/v1/projects/project/locations/global/endpoints/openapi/chat/completions" {
				t.Fatalf("upstream calls: %+v", calls[before:])
			}
			if !strings.Contains(calls[before].Body, `"model"`) || !strings.Contains(calls[before].Body, `"chat-model"`) {
				t.Fatalf("the native model must reach the surface in the body: %q", calls[before].Body)
			}
			if strings.Contains(calls[before].Query, "api-version") {
				t.Fatalf("api-version crossed to the upstream: %q", calls[before].Query)
			}
		})
	}
	for _, seen := range upstream.seen {
		if strings.Contains(seen, nativeKeyToken) || strings.Contains(strings.ToLower(seen), "api-key") || strings.Contains(seen, "api-version") {
			t.Fatalf("caller credential or api-version reached the upstream: %s", seen)
		}
	}
	if strings.Contains(logs.String(), nativeKeyToken) {
		t.Fatal("the token was logged")
	}

	path := "/ap/ide/openai/v1/chat/completions"
	// A wrong key, a conflicting Authorization, and a key in the URL are refused.
	for name, w := range map[string]int{
		"wrong key":       nativeKeyDo(chain, "POST", path, chat, "Api-Key", "not-a-valid-token").Code,
		"conflict":        nativeKeyDo(chain, "POST", path, chat, "Api-Key", nativeKeyToken, "Authorization", "Bearer other-token").Code,
		"url key":         nativeKeyDo(chain, "POST", path+"?api-key="+nativeKeyToken, chat).Code,
		"no credentials":  nativeKeyDo(chain, "POST", path, chat).Code,
		"anthropic style": nativeKeyDo(chain, "POST", path, chat, "X-Api-Key", nativeKeyToken).Code,
	} {
		if w != 401 {
			t.Errorf("%s: %d, want 401", name, w)
		}
	}
	// The same token in both headers is one credential.
	if w := nativeKeyDo(chain, "POST", path, chat, "Api-Key", nativeKeyToken, "Authorization", "Bearer "+nativeKeyToken); w.Code != 200 {
		t.Fatalf("same token in both headers: %d %s", w.Code, w.Body.String())
	}
	// The gateway path keeps its own behavior: Api-Key is not accepted on /ap/ non-OpenAI wires.
	if w := nativeKeyDo(chain, "POST", "/ap/ide/v1beta/models/gemini-pro:generateContent", nativeGenAIBody, "Api-Key", nativeKeyToken); w.Code != 401 {
		t.Fatalf("Api-Key on a GenAI wire: %d", w.Code)
	}
}

func TestAccessPointAzureDeploymentRoutesResolveTheModelLikeOtherWires(t *testing.T) {
	f := newAPFixture(t)
	before := len(f.upstreamCalls())
	chat := `{"messages":[]}`
	// Unknown deployment: the same model-not-found as any wire.
	if w := f.do("POST", "/ap/ide/openai/deployments/nope/chat/completions", chat, "Content-Type", "application/json"); w.Code != 404 || !strings.Contains(w.Body.String(), "MODEL_NOT_FOUND") && !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("unknown deployment: %d %s", w.Code, w.Body.String())
	}
	// A body model that differs from the deployment is ambiguous.
	if w := f.do("POST", "/ap/ide/openai/deployments/chat-model/chat/completions", `{"model":"claude","messages":[]}`, "Content-Type", "application/json"); w.Code != 400 {
		t.Fatalf("deployment/body model mismatch: %d %s", w.Code, w.Body.String())
	}
	// Duplicate model keys and non-objects are refused as elsewhere.
	for _, body := range []string{`{"model":"chat-model","model":"chat-model","messages":[]}`, `[]`, `{"model":5}`} {
		if w := f.do("POST", "/ap/ide/openai/deployments/chat-model/chat/completions", body, "Content-Type", "application/json"); w.Code != 400 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	// Azure has no deployment route for other operations or models of other wires.
	for _, path := range []string{"/ap/ide/openai/deployments/chat-model/completions", "/ap/ide/openai/deployments/chat-model/responses", "/ap/ide/openai/v1/images/generations"} {
		if w := f.do("POST", path, chat, "Content-Type", "application/json"); w.Code != 404 && w.Code != 405 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	// A model served only through another protocol is a protocol mismatch, as on /v1.
	if w := f.do("POST", "/ap/ide/openai/deployments/claude/chat/completions", chat, "Content-Type", "application/json"); w.Code != 400 || !strings.Contains(w.Body.String(), "anthropic-messages") {
		t.Fatalf("deployment on the wrong protocol: %d %s", w.Code, w.Body.String())
	}
	if len(f.upstreamCalls()) != before {
		t.Fatal("a refused request reached the upstream")
	}
}

// Azure surfaces: the deployment and v1 shapes reach the surface's own native
// path, aliases are rewritten, and the caller's api-version never crosses.
func TestAccessPointAzureShapesRouteToAzureSurfaces(t *testing.T) {
	azure := modelapi.Provider{ID: modelapi.ProviderAzureOpenAI}
	pinned := modelapi.Provider{ID: modelapi.ProviderAzureOpenAI, APIVersion: "2024-10-21"}
	f := newDirectAPFixture(t, "az", []directSurface{
		{"az-v1-chat", "gpt-friendly", "gpt-native", modelapi.ProtocolOpenAIChat, azure, []string{"azure.v1.chat-completions"}, azureRef},
		{"az-dep-chat", "dep-friendly", "dep-native", modelapi.ProtocolOpenAIChat, pinned, []string{"azure.deployments.chat-completions"}, azureRef},
		{"az-v1-emb", "emb-friendly", "emb-native", modelapi.ProtocolOpenAIEmbeddings, azure, []string{"azure.v1.embeddings"}, azureRef},
		{"az-dep-emb", "emb2-friendly", "emb2-native", modelapi.ProtocolOpenAIEmbeddings, pinned, []string{"azure.deployments.embeddings"}, azureRef},
	})
	const query = "?api-version=1999-01-01-ignored"
	for _, c := range []struct {
		name, path, body string
		wantPath         string
		wantQuery        string
		wantBody         string
	}{
		{"v1 chat by body model", "/ap/az/openai/v1/chat/completions" + query, `{"model":"gpt-friendly","messages":[]}`, "/openai/v1/chat/completions", "", `{"model":"gpt-native","messages":[]}`},
		{"v1 chat on the plain wire", "/ap/az/v1/chat/completions", `{"model":"gpt-friendly","messages":[]}`, "/openai/v1/chat/completions", "", `{"model":"gpt-native","messages":[]}`},
		{"deployment chat with SDK body", "/ap/az/openai/deployments/dep-friendly/chat/completions" + query, `{"messages":[],"model":"dep-friendly"}`, "/openai/deployments/dep-native/chat/completions", "api-version=2024-10-21", `{"messages":[],"model":"dep-native"}`},
		{"deployment chat without body model", "/ap/az/openai/deployments/dep-friendly/chat/completions" + query, "{\n  \"messages\": []\n}", "/openai/deployments/dep-native/chat/completions", "api-version=2024-10-21", "{\"model\":\"dep-native\",\n  \"messages\": []\n}"},
		{"deployment chat, empty object", "/ap/az/openai/deployments/dep-friendly/chat/completions", `{}`, "/openai/deployments/dep-native/chat/completions", "api-version=2024-10-21", `{"model":"dep-native"}`},
		{"v1 embeddings", "/ap/az/openai/v1/embeddings" + query, `{"input":"x","model":"emb-friendly"}`, "/openai/v1/embeddings", "", `{"input":"x","model":"emb-native"}`},
		{"deployment embeddings", "/ap/az/openai/deployments/emb2-friendly/embeddings" + query, `{"input":"x"}`, "/openai/deployments/emb2-native/embeddings", "api-version=2024-10-21", `{"model":"emb2-native","input":"x"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := f.do("POST", c.path, c.body, "Content-Type", "application/json")
			if w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			last := f.lastUpstream()
			if last.Path != c.wantPath || last.Query != c.wantQuery || last.Body != c.wantBody {
				t.Fatalf("upstream: path %q query %q body %q\nwant   path %q query %q body %q", last.Path, last.Query, last.Body, c.wantPath, c.wantQuery, c.wantBody)
			}
			if last.Authorization != "" && strings.Contains(last.Authorization, "1999") {
				t.Fatal("caller api-version leaked")
			}
		})
	}
}

func TestInsertBodyModelKeepsTheRestOfTheBody(t *testing.T) {
	for in, want := range map[string]string{
		`{}`:                 `{"model":"m"}`,
		"  { }":              `  {"model":"m" }`,
		`{"a":1}`:            `{"model":"m","a":1}`,
		"{\n\t\"a\": [1]\n}": "{\"model\":\"m\",\n\t\"a\": [1]\n}",
	} {
		got, err := insertBodyModel([]byte(in), "m")
		if err != nil || string(got) != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := insertBodyModel([]byte(`[1]`), "m"); err == nil {
		t.Error("array accepted")
	}
}
