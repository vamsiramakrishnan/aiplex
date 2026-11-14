package api

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

func TestRewriteBodyModelPreservesEveryOtherByte(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"compact", `{"model":"friendly","a":1}`, `{"model":"native","a":1}`},
		{"key order and whitespace",
			"{ \"z\" : [1, 2.50 , {\"model\":\"nested\"}],\n \"model\" :   \"friendly\" ,\n\"a\":\"<&>\\u00e9\" }",
			"{ \"z\" : [1, 2.50 , {\"model\":\"nested\"}],\n \"model\" :   \"native\" ,\n\"a\":\"<&>\\u00e9\" }"},
		{"model first", `{"model":"friendly","stream":true,"vendor":{"x":[1,2,3]}}`, `{"model":"native","stream":true,"vendor":{"x":[1,2,3]}}`},
		{"model last, escaped key", `{"input":["a","b"],"model":"friendly"}`, `{"input":["a","b"],"model":"native"}`},
		{"escaped value", `{"model":"fri\"endlyé","n":1e400}`, `{"model":"native","n":1e400}`},
		{"unknown and duplicate-looking nested keys", `{"extra":{"model":"x","other":{"model":"y"}},"model":"friendly"}`, `{"extra":{"model":"x","other":{"model":"y"}},"model":"native"}`},
		{"trailing whitespace", "{\"model\":\"friendly\"}\n\n", "{\"model\":\"native\"}\n\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := rewriteBodyModel([]byte(c.in), "native")
			if err != nil || string(got) != c.want {
				t.Fatalf("got %q, %v\nwant %q", got, err, c.want)
			}
		})
	}
	for name, in := range map[string]string{
		"duplicate":              `{"model":"a","model":"b"}`,
		"duplicate escaped":      `{"model":"a","model":"b"}`,
		"duplicate with others":  `{"model":"a","x":1,"model":"a"}`,
		"nested duplicate":       `{"model":"a","x":{"k":1,"k":2}}`,
		"missing":                `{"input":"x"}`,
		"number":                 `{"model":5}`,
		"null":                   `{"model":null}`,
		"array":                  `[{"model":"a"}]`,
		"trailing data":          `{"model":"a"}{"model":"b"}`,
		"truncated":              `{"model":"a"`,
		"empty":                  ``,
		"object value for model": `{"model":{"id":"a"}}`,
	} {
		if got, err := rewriteBodyModel([]byte(in), "native"); err == nil {
			t.Errorf("%s: accepted, produced %q", name, got)
		}
	}
}

func TestRewriteBodyModelLargeBodyKeepsEverythingElse(t *testing.T) {
	filler := `"input":[` + strings.Repeat(`"0123456789abcdef0123456789abcdef",`, 200000) + `"end"]`
	for _, in := range []string{
		`{"model":"friendly",` + filler + `}`,
		`{` + filler + `,"model":"friendly"}`,
	} {
		if len(in) < 6<<20 {
			t.Fatalf("test body is only %d bytes", len(in))
		}
		got, err := rewriteBodyModel([]byte(in), "native-model")
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.Replace(in, `"model":"friendly"`, `"model":"native-model"`, 1); string(got) != want {
			t.Fatal("large body changed outside the model value")
		}
	}
}

func aliasRows() []directSurface {
	return []directSurface{
		{"s-chat", "chat-friendly", "gpt-native", modelapi.ProtocolOpenAIChat, modelapi.Provider{ID: modelapi.ProviderOpenAI}, []string{"openai.v1.chat-completions"}, nil},
		{"s-resp", "resp-friendly", "resp-native", modelapi.ProtocolOpenAIResponses, modelapi.Provider{ID: modelapi.ProviderOpenAI}, []string{"openai.v1.responses"}, nil},
		{"s-emb", "emb-friendly", "emb-native", modelapi.ProtocolOpenAIEmbeddings, modelapi.Provider{ID: modelapi.ProviderOpenAI}, []string{"openai.v1.embeddings"}, nil},
		{"s-msg", "claude-friendly", "claude-native", modelapi.ProtocolAnthropicMessages, modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}, []string{"anthropic.messages"}, anthropicRef},
	}
}

// Aliases on direct provider surfaces rewrite only the request's model value;
// the response is the provider's and keeps the native name.
func TestAccessPointAliasRewritesOnlyTheModelValueOnDirectWires(t *testing.T) {
	f := newDirectAPFixture(t, "alias", aliasRows())
	for _, row := range []struct {
		wire, path, body, want string
	}{
		{"chat", "/ap/alias/v1/chat/completions",
			"{\n  \"vendor\": {\"x\": 1.50},\n  \"model\" : \"chat-friendly\",\n  \"messages\": [{\"role\":\"user\",\"content\":\"<hi>\"}],\n  \"stream\": false\n}",
			"{\n  \"vendor\": {\"x\": 1.50},\n  \"model\" : \"gpt-native\",\n  \"messages\": [{\"role\":\"user\",\"content\":\"<hi>\"}],\n  \"stream\": false\n}"},
		{"chat streaming", "/ap/alias/v1/chat/completions",
			`{"stream":true,"stream_options":{"include_usage":true},"model":"chat-friendly","messages":[]}`,
			`{"stream":true,"stream_options":{"include_usage":true},"model":"gpt-native","messages":[]}`},
		{"responses", "/ap/alias/v1/responses",
			`{"input":"hi","model":"resp-friendly","future_field":{"model":"chat-friendly"}}`,
			`{"input":"hi","model":"resp-native","future_field":{"model":"chat-friendly"}}`},
		{"embeddings", "/ap/alias/v1/embeddings",
			`{"input":["a","b"],"model":"emb-friendly","dimensions":8}`,
			`{"input":["a","b"],"model":"emb-native","dimensions":8}`},
		{"messages", "/ap/alias/v1/messages",
			`{"max_tokens":5,"model":"claude-friendly","messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":5,"model":"claude-native","messages":[{"role":"user","content":"hi"}]}`},
	} {
		t.Run(row.wire, func(t *testing.T) {
			w := f.do("POST", row.path, row.body, "Content-Type", "application/json")
			// Responses are stateful: the fixture's reply has no response id to
			// record, so the handler answers 502 after relaying the request.
			if w.Code != 200 && !(row.wire == "responses" && w.Code == 502) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if got := f.lastUpstream().Body; got != row.want {
				t.Fatalf("upstream body\n got %q\nwant %q", got, row.want)
			}
			if strings.Contains(w.Body.String(), "friendly") {
				t.Fatalf("alias leaked into the response: %s", w.Body.String())
			}
		})
	}
}

func TestAccessPointAliasStillRejectsAmbiguousOrUnnamedBodies(t *testing.T) {
	f := newDirectAPFixture(t, "alias", aliasRows())
	before := len(f.upstreamCalls())
	for name, body := range map[string]string{
		"duplicate model":         `{"model":"chat-friendly","model":"gpt-native","messages":[]}`,
		"escaped duplicate model": `{"model":"chat-friendly","model":"gpt-native","messages":[]}`,
		"no model":                `{"messages":[]}`,
	} {
		w := f.do("POST", "/ap/alias/v1/chat/completions", body, "Content-Type", "application/json")
		if w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// The native name keeps working and is relayed untouched.
	native := `{"model":"gpt-native","messages":[]}`
	// (native names are not offered: only the access point's names resolve)
	if w := f.do("POST", "/ap/alias/v1/chat/completions", native, "Content-Type", "application/json"); w.Code != 404 {
		t.Errorf("native name resolved although the access point names the model differently: %d", w.Code)
	}
	if len(f.upstreamCalls()) != before {
		t.Fatal("a refused request reached the upstream")
	}
}
