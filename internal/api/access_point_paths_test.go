package api

import (
	"context"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

// TestAccessPointRoutesEveryAcceptedProviderWireToTheNativePath is the
// regression for access-point requests that passed validation but were refused
// by the provider client facade (Azure OpenAI, openai-compatible base paths).
// Every provider x wire that save-time validation accepts must reach the
// upstream with the surface's own native path.
func TestAccessPointRoutesEveryAcceptedProviderWireToTheNativePath(t *testing.T) {
	f := newAPFixture(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLEOPERATOR")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "operator-secret-access-key")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{f.upstream.URL}, AllowLoopback: true}, Identities: []connections.Identity{
		{ID: "host", TenantID: "tenant", Type: "bearer-env", TokenEnv: "COHESION_HOST_CREDENTIAL", AllowedOrigins: []string{f.upstream.URL}},
		{ID: "aws", TenantID: "tenant", Type: "aws-default", AllowedOrigins: []string{f.upstream.URL}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.h.Runtime = runtime
	installProviderCredentials(t, f.h)

	chatBody := func(m string) string { return `{"model":"` + m + `","messages":[{"role":"user","content":"hi"}]}` }
	respBody := func(m string) string { return `{"model":"` + m + `","input":"hi"}` }
	msgBody := func(m string) string {
		return `{"model":"` + m + `","max_tokens":5,"messages":[{"role":"user","content":"hi"}]}`
	}
	bedrockBody := func(string) string { return `{"messages":[{"role":"user","content":[{"text":"hi"}]}]}` }
	azure := func(settings modelapi.Provider) modelapi.Provider {
		settings.ID = modelapi.ProviderAzureOpenAI
		return settings
	}
	type row struct {
		name     string
		protocol string
		provider modelapi.Provider
		ops      []string
		identity string
		auth     *toolcredential.Auth
		// stateful rows are relayed upstream but the fixture's reply has no
		// response id to record, so the handler answers 502 after the call.
		stateful bool
		wirePath func(m string) string
		body     func(m string) string
		want     func(m string) string
	}
	same := func(p string) func(string) string { return func(string) string { return p } }
	rows := []row{
		{"openai chat", modelapi.ProtocolOpenAIChat, modelapi.Provider{ID: modelapi.ProviderOpenAI}, []string{"openai.v1.chat-completions"}, "host", nil, false, same("/v1/chat/completions"), chatBody, same("/v1/chat/completions")},
		{"openai responses", modelapi.ProtocolOpenAIResponses, modelapi.Provider{ID: modelapi.ProviderOpenAI}, []string{"openai.v1.responses"}, "host", nil, true, same("/v1/responses"), respBody, same("/v1/responses")},
		{"anthropic direct", modelapi.ProtocolAnthropicMessages, modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}, []string{"anthropic.messages"}, "host", anthropicRef, false, same("/v1/messages"), msgBody, same("/v1/messages")},
		{"azure v1 chat", modelapi.ProtocolOpenAIChat, azure(modelapi.Provider{}), []string{"azure.v1.chat-completions"}, "host", azureRef, false, same("/v1/chat/completions"), chatBody, same("/openai/v1/chat/completions")},
		{"azure v1 responses", modelapi.ProtocolOpenAIResponses, azure(modelapi.Provider{}), []string{"azure.v1.responses"}, "host", azureRef, true, same("/v1/responses"), respBody, same("/openai/v1/responses")},
		{"azure deployment-only chat", modelapi.ProtocolOpenAIChat, azure(modelapi.Provider{APIVersion: "2024-10-21"}), []string{"azure.deployments.chat-completions"}, "host", azureRef, false, same("/v1/chat/completions"), chatBody, func(m string) string { return "/openai/deployments/" + m + "/chat/completions" }},
		{"azure v1 and deployment chat prefers v1", modelapi.ProtocolOpenAIChat, azure(modelapi.Provider{APIVersion: "2024-10-21"}), []string{"azure.deployments.chat-completions", "azure.v1.chat-completions"}, "host", azureRef, false, same("/v1/chat/completions"), chatBody, same("/openai/v1/chat/completions")},
		{"compatible default base", modelapi.ProtocolOpenAIChat, modelapi.Provider{ID: modelapi.ProviderOpenAICompatible}, []string{"compat.chat-completions"}, "host", nil, false, same("/v1/chat/completions"), chatBody, same("/v1/chat/completions")},
		{"compatible custom base", modelapi.ProtocolOpenAIChat, modelapi.Provider{ID: modelapi.ProviderOpenAICompatible, BasePath: "/custom/v2"}, []string{"compat.chat-completions"}, "host", nil, false, same("/v1/chat/completions"), chatBody, same("/custom/v2/chat/completions")},
		{"bedrock converse", modelapi.ProtocolBedrockConverse, modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}, []string{"bedrock.converse"}, "aws", nil, false, func(m string) string { return "/model/" + m + "/converse" }, bedrockBody, func(m string) string { return "/model/" + m + "/converse" }},
		{"bedrock converse-stream", modelapi.ProtocolBedrockConverse, modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}, []string{"bedrock.converse-stream"}, "aws", nil, false, func(m string) string { return "/model/" + m + "/converse-stream" }, bedrockBody, func(m string) string { return "/model/" + m + "/converse-stream" }},
		{"bedrock invoke", modelapi.ProtocolBedrockInvoke, modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}, []string{"bedrock.invoke"}, "aws", nil, false, func(m string) string { return "/model/" + m + "/invoke" }, bedrockBody, func(m string) string { return "/model/" + m + "/invoke" }},
		{"bedrock invoke-stream", modelapi.ProtocolBedrockInvoke, modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}, []string{"bedrock.invoke-stream"}, "aws", nil, false, func(m string) string { return "/model/" + m + "/invoke-with-response-stream" }, bedrockBody, func(m string) string { return "/model/" + m + "/invoke-with-response-stream" }},
	}
	spec := AccessPoint{ID: "paths", DisplayName: "Paths", CallerKinds: []string{CallerUser}}
	scopes := append([]string(nil), apAllScopes...)
	for i, r := range rows {
		id, native := "surf"+string(rune('a'+i)), "m-"+string(rune('a'+i))
		provider := r.provider
		ops, err := modelapi.SelectProviderOperations(provider.ID, r.ops...)
		if err != nil {
			t.Fatal(err)
		}
		surface := modelapi.Surface{ID: id, Protocol: r.protocol, Origin: f.upstream.URL, Models: []string{native}, Operations: ops, Provider: &provider}
		f.put("model-surface", id, ModelConnection{Surface: surface, IdentityRef: r.identity, Auth: r.auth})
		spec.Models = append(spec.Models, AccessPointModel{Name: native, SurfaceRef: id, NativeModel: native})
		scopes = append(scopes, "llm:model:"+native)
		for _, op := range ops {
			scopes = append(scopes, "llm:surface:"+id+":"+op.ID)
		}
	}
	// Save-time validation accepts exactly these combinations.
	if err = f.h.validateAccessPointModels(context.Background(), "tenant", spec.Models); err != nil {
		t.Fatal(err)
	}
	f.put(AccessPointKind, "paths", spec)
	f.as(apUser("alice", "platform-eng"), scopes...)
	for i, r := range rows {
		native := "m-" + string(rune('a'+i))
		t.Run(r.name, func(t *testing.T) {
			before := len(f.upstreamCalls())
			w := f.do("POST", "/ap/paths"+r.wirePath(native), r.body(native), "Content-Type", "application/json")
			calls := f.upstreamCalls()
			if (w.Code != 200 && !(r.stateful && w.Code == 502)) || len(calls) != before+1 {
				t.Fatalf("status %d, upstream calls %d: %s", w.Code, len(calls)-before, w.Body.String())
			}
			if got := calls[len(calls)-1].Path; got != r.want(native) {
				t.Fatalf("upstream path = %q, want %q", got, r.want(native))
			}
		})
	}
}

// Combinations the facade cannot serve are rejected when the access point is
// saved rather than failing every request.
func TestAccessPointRejectsUnservableSurfaceWiresAtSave(t *testing.T) {
	f := newAPFixture(t)
	azureOps, _ := modelapi.SelectProviderOperations(modelapi.ProviderAzureOpenAI, "azure.deployments.chat-completions")
	azure := modelapi.Surface{ID: "az", Protocol: modelapi.ProtocolOpenAIResponses, Origin: f.upstream.URL, Models: []string{"dep"}, Operations: azureOps, Provider: &modelapi.Provider{ID: modelapi.ProviderAzureOpenAI, APIVersion: "2024-10-21"}}
	f.put("model-surface", "az", ModelConnection{Surface: azure, IdentityRef: "host"})
	claudeOps, _ := modelapi.SelectOperations("models.rawPredict")
	notAnthropic := modelapi.Surface{ID: "na", Protocol: clientplex.Messages, Origin: f.upstream.URL, APIVersion: "v1", Project: "project", Location: "global", Publisher: "google", Models: []string{"x"}, Operations: claudeOps}
	f.put("model-surface", "na", ModelConnection{Surface: notAnthropic, IdentityRef: "host"})
	for _, ref := range []string{"az", "na"} {
		native := map[string]string{"az": "dep", "na": "x"}[ref]
		err := f.h.validateAccessPointModels(context.Background(), "tenant", []AccessPointModel{{Name: native, SurfaceRef: ref, NativeModel: native}})
		if err == nil || !strings.Contains(err.Error(), "cannot serve") {
			t.Fatalf("%s: unservable surface accepted: %v", ref, err)
		}
	}
}
