package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestBugbashClientPlanReachesProtectedNativeMessages(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "SSE"}[stream], func(t *testing.T) {
			body := `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3}`
			response := `{"id":"native-message","model":"model-v1","type":"message","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`
			if stream {
				body = strings.TrimSuffix(body, "}") + `,"stream":true}`
				response = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"native-message\",\"model\":\"model-v1\",\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			}
			f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
				native, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/v1/messages" || string(native) != body || r.Header.Get("Authorization") != "Bearer operator-owned-upstream-token" {
					t.Error("planned native request changed", r.URL.Path, string(native))
				}
				w.Header().Set("Content-Type", "application/json")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				io.WriteString(w, response)
			}, func(cfg *QuotaHostConfig) {
				cfg.Bindings = cfg.Bindings[:1]
				cfg.Bindings[0].OperationID, cfg.Bindings[0].Provider = "anthropic.messages", "anthropic"
				cfg.Tenants[0].Policy.Budgets[0].Limit = 1
			})
			connection := bugbashMessagesSurface(t, f, "anthropic.messages")
			f.handler.PublicURL = "https://gateway.example"
			plan, err := clientplex.BuildPlan(context.Background(), "tenant", clientplex.Registration{ID: "developer", ProfileID: "claude-code", Mode: "gateway", Protocol: clientplex.Messages, DestinationRef: connection.Surface.ID, PrincipalBinding: f.identity.PrincipalID, AuthMode: "gateway-key", CredentialEnv: "MODEL_TOKEN", CredentialRef: "credential-reference"}, f.handler)
			if err != nil || len(plan.Artifacts) == 0 || !strings.Contains(plan.Artifacts[0].Content, "/api/v1/llm/gateway/surface") {
				t.Fatal("registered destination did not render a managed facade", plan, err)
			}
			requestAccess := f.access()
			invoke := func(key string) *httptest.ResponseRecorder {
				r := f.request("generateContent", key, body, false)
				r = r.WithContext(context.WithValue(r.Context(), keyWIFAccess, requestAccess))
				r.URL.Path = "/api/v1/llm/gateway/surface/v1/messages"
				r.URL.RawQuery = ""
				r.Header.Set("Anthropic-Version", "2023-06-01")
				w := httptest.NewRecorder()
				f.handler.ClientGateway(w, r)
				return w
			}
			// A generated plan supplies no invocation grant.
			originalAccess := f.handler.ResolveAccess
			requestAccess = &models.ResolvedAccess{Identity: *f.identity, Scopes: []string{"llm:surface:surface:*"}}
			f.handler.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) {
				return &models.ResolvedAccess{Identity: *f.identity, Scopes: []string{"llm:surface:surface:*"}}, nil
			}
			if denied := invoke("without-model-grant"); denied.Code != 403 || f.calls.Load() != 0 || len(quotaHostTestAttempts(t, f.store)) != 0 {
				t.Fatal("client plan granted execution authority", denied.Code, denied.Body.String())
			}
			f.handler.ResolveAccess = originalAccess
			requestAccess = f.access()
			w := invoke("protected-client-call")
			if w.Code != 200 || w.Body.String() != response || f.calls.Load() != 1 {
				t.Fatal("planned protected client failed", w.Code, w.Body.String(), f.calls.Load())
			}
			info := quotaHostModelAttempt(t, f, w.Header().Get("X-Aiplex-Attempt-ID"))
			if info.Decision.State != "settled" || info.Actual["requests"] != 1 || len(info.Actual) != 1 {
				t.Fatal("request-count client invented token billing or failed settlement", info)
			}
			if replay := invoke("protected-client-call"); replay.Code != 409 || f.calls.Load() != 1 {
				t.Fatal("planned client replay bypassed admission", replay.Code)
			}
			if next := invoke("another-client-call"); next.Code != 429 || f.calls.Load() != 1 {
				t.Fatal("planned client bypassed shared budget", next.Code)
			}
		})
	}
}

func bugbashMessagesSurface(t *testing.T, f *quotaHostModelFixture, operation string) ModelConnection {
	t.Helper()
	record, err := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	if err != nil {
		t.Fatal(err)
	}
	var connection ModelConnection
	if err := json.Unmarshal(record.Spec, &connection); err != nil {
		t.Fatal(err)
	}
	connection.Surface.Publisher, connection.Surface.Protocol = "anthropic", clientplex.Messages
	connection.Surface.Operations, err = modelapi.SelectOperations(operation)
	if err != nil {
		t.Fatal(err)
	}
	record.Spec, err = json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.resources.Put(context.Background(), record, record.Revision); err != nil {
		t.Fatal(err)
	}
	return connection
}

func TestBugbashVertexFacadeAmbiguityCannotReserveQuota(t *testing.T) {
	f := quotaHostModelNew(t, false, func(http.ResponseWriter, *http.Request) { t.Error("ambiguous client request reached provider") }, func(cfg *QuotaHostConfig) {
		cfg.Bindings = cfg.Bindings[:1]
		cfg.Bindings[0].OperationID, cfg.Bindings[0].Provider = "models.rawPredict", "anthropic"
	})
	bugbashMessagesSurface(t, f, "models.rawPredict")
	t.Setenv("QUOTA_HOST_MODEL_TOKEN", "")
	for _, body := range []string{
		`{"model":"model-v1","model":"model-v1","messages":[],"max_tokens":3}`,
		`{"model":"model-v1","messages":[],"max_tokens":1000,"max_tokens":3}`,
		`{"model":"model-v1","messages":[],"max_tokens":3,"stream":"true"}`,
	} {
		r := f.request("generateContent", "ambiguous-client-call", body, false)
		r.URL.Path = "/api/v1/llm/gateway/surface/v1/messages"
		w := httptest.NewRecorder()
		f.handler.ClientGateway(w, r)
		if w.Code != 400 || len(quotaHostTestAttempts(t, f.store)) != 0 || f.calls.Load() != 0 {
			t.Fatal("translation admitted an ambiguous native payload", w.Code, w.Body.String())
		}
	}
}

func TestBugbashClientCapabilityChecksExecutionContract(t *testing.T) {
	for _, mutate := range []func(*modelapi.Operation){
		func(op *modelapi.Operation) { op.Transport = modelapi.WebSocket },
		func(op *modelapi.Operation) { op.Action = modelapi.Read },
		func(op *modelapi.Operation) { op.Stateful = true },
		func(op *modelapi.Operation) { op.ResourceKind = "foreign-resource" },
	} {
		surface := cohesionSurface(t).Surface
		mutate(&surface.Operations[0])
		if clientSurfaceReady(surface) {
			t.Fatal("client plan advertised a different execution contract", surface.Operations[0])
		}
	}
}

func TestBugbashMessagesFacadeUsesInstalledNativeMessagesOperation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "SSE"}[stream], func(t *testing.T) {
			body := "{\n  \"model\":\"model\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}],\"max_tokens\":3}"
			response := `{"id":"native-message","model":"model","type":"message","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
			if stream {
				body = strings.TrimSuffix(body, "}") + `,"stream":true}`
				response = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"native-message\",\"model\":\"model\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				native, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/v1/messages" || string(native) != body || r.Header.Get("Anthropic-Version") != "2023-06-01" || r.Header.Get("Authorization") != "Bearer host-token" {
					t.Error("native Messages protocol was rewritten", r.URL.Path, string(native), r.Header)
				}
				w.Header().Set("Content-Type", "application/json")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				io.WriteString(w, response)
			}))
			defer upstream.Close()
			t.Setenv("COHESION_HOST_CREDENTIAL", "host-token")
			store := resources.NewMemoryStore()
			h := cohesionHandler(t, store, upstream.URL)
			connection := cohesionSurface(t)
			connection.Surface.Origin, connection.Surface.Publisher, connection.Surface.Protocol = upstream.URL, "anthropic", clientplex.Messages
			connection.Surface.Operations, _ = modelapi.SelectOperations("anthropic.messages")
			cohesionRecord(t, store, "model-surface", "surface", connection)
			if _, err := h.ResolveDestination(context.Background(), "tenant", "surface"); err != nil {
				t.Error("ClientPlex rejected an installed native Messages operation", err)
			}
			request := trustedConnectedRequest("/api/v1/llm/gateway/surface/v1/messages", body, []string{"llm:surface:surface:anthropic.messages", "llm:model:model"})
			request.Header.Set("Anthropic-Version", "2023-06-01")
			w := httptest.NewRecorder()
			h.ClientGateway(w, request)
			if w.Code != 200 || w.Body.String() != response || calls.Load() != 1 {
				t.Fatalf("native client facade failed: status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body.String())
			}
		})
	}
}

func TestBugbashVertexFacadeRejectsAmbiguityBeforeReencoding(t *testing.T) {
	surface := modelapi.Surface{Protocol: clientplex.Messages, APIVersion: "v1", Project: "project", Location: "global", Publisher: "anthropic"}
	surface.Operations, _ = modelapi.SelectOperations("models.rawPredict", "models.streamRawPredict")
	for _, test := range []struct{ name, body string }{
		{"model", `{"model":"model","model":"model","messages":[],"max_tokens":3}`},
		{"escaped model alias", `{"model":"model","mo\u0064el":"model","messages":[],"max_tokens":3}`},
		{"output bound", `{"model":"model","messages":[],"max_tokens":1000,"max_tokens":3}`},
		{"nested role", `{"model":"model","messages":[{"role":"user","role":"assistant","content":"hello"}],"max_tokens":3}`},
		{"stream type", `{"model":"model","messages":[],"max_tokens":3,"stream":"true"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(test.body))
			if _, _, err := clientNativePath(surface, request, "/v1/messages"); err == nil {
				t.Fatal("facade erased ambiguous or invalid native fields before validation", test.body)
			}
		})
	}
}

func TestBugbashManagedModelAcceptsCaseInsensitiveNativeMediaTypes(t *testing.T) {
	for _, test := range []struct{ name, media, operation, body string }{
		{"JSON", "Application/JSON; Charset=UTF-8", "generateContent", quotaHostModelTerminal},
		{"SSE", "Text/Event-Stream; Charset=UTF-8", "streamGenerateContent", quotaHostModelSSEFirst + quotaHostModelSSEFinal},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := quotaHostModelNew(t, true, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.media)
				io.WriteString(w, test.body)
			}, nil)
			w := httptest.NewRecorder()
			f.handler.NativeModel(w, f.request(test.operation, "native-media", quotaHostModelBody, false))
			if w.Code != 200 || w.Body.String() != test.body || f.calls.Load() != 1 {
				t.Fatal("valid native media type lost protocol or usage", w.Code, w.Body.String(), f.calls.Load())
			}
			quotaHostModelAssertSettled(t, f, w.Header().Get("X-Aiplex-Attempt-ID"))
		})
	}
}
