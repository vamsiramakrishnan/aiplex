package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
)

func cohesionRecord(t *testing.T, store resources.Store, kind, id string, spec any) resources.Record {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: kind, ID: id, Spec: raw}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
func cohesionHandler(t *testing.T, store resources.Store, origin string) *ConnectedHandler {
	t.Helper()
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{origin}, AllowLoopback: true}, Identities: []connections.Identity{{ID: "host", TenantID: "tenant", Type: "bearer-env", TokenEnv: "COHESION_HOST_CREDENTIAL", AllowedOrigins: []string{origin}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := NewConnectedHandler(store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	h.PublicURL = "https://gateway.example"
	return h
}
func cohesionSurface(t *testing.T) ModelConnection {
	t.Helper()
	ops, err := modelapi.SelectOperations("models.generateContent")
	if err != nil {
		t.Fatal(err)
	}
	return ModelConnection{Surface: modelapi.Surface{ID: "surface", Protocol: clientplex.GenAI, Origin: "https://models.example", APIVersion: "v1", Project: "project", Location: "global", Publisher: "google", Models: []string{"model"}, Operations: ops}, IdentityRef: "host"}
}

type cohesionReadFailure struct {
	resources.Store
	kind string
}

func (s cohesionReadFailure) Get(ctx context.Context, tenant, kind, id string) (resources.Record, error) {
	if kind == s.kind {
		return resources.Record{}, fmt.Errorf("temporary store failure")
	}
	return s.Store.Get(ctx, tenant, kind, id)
}

func TestClientDestinationTracksActualModuleAndInferenceCapability(t *testing.T) {
	store := resources.NewMemoryStore()
	surface := cohesionSurface(t)
	cohesionRecord(t, store, "model-surface", "surface", surface)
	h := cohesionHandler(t, store, surface.Surface.Origin)
	if destination, err := h.ResolveDestination(context.Background(), "tenant", "surface"); err != nil || destination.Provider != "aiplex" {
		t.Fatal("valid native surface could not be linked", err)
	}
	h.WithEnabledModules([]string{"clientplex"})
	if _, err := h.ResolveDestination(context.Background(), "tenant", "surface"); err == nil {
		t.Fatal("client plan advertised disabled LLMPlex facade")
	}
	h.WithEnabledModules([]string{"clientplex", "llmplex"})
	surface.Surface.Protocol = clientplex.Responses
	surface.Surface.Endpoint = "endpoint"
	surface.Surface.Operations, _ = modelapi.SelectOperations("models.countTokens")
	raw, _ := json.Marshal(surface)
	_, _ = store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "model-surface", ID: "surface", Spec: raw}, 1)
	if _, err := h.ResolveDestination(context.Background(), "tenant", "surface"); err == nil {
		t.Fatal("protocol label advertised an unimplemented Responses operation")
	}
}

func TestClientDestinationDoesNotRetargetAcrossResourceKindsOrStoreFailures(t *testing.T) {
	store := resources.NewMemoryStore()
	surface := cohesionSurface(t)
	cohesionRecord(t, store, "model-surface", "surface", surface)
	cohesionRecord(t, store, "client-destination", "surface", clientplex.Destination{ID: "surface", Endpoint: "https://external.example", Protocols: []string{clientplex.Responses}, Availability: "configured"})
	h := cohesionHandler(t, store, surface.Surface.Origin)
	if _, err := h.ResolveDestination(context.Background(), "tenant", "surface"); err == nil {
		t.Fatal("ambiguous destination silently selected one resource kind")
	}
	h.Store = cohesionReadFailure{Store: store, kind: "model-surface"}
	if _, err := h.ResolveDestination(context.Background(), "tenant", "surface"); err == nil {
		t.Fatal("model lookup failure silently fell back to an external destination")
	}
}

func TestClientDestinationRechecksRemovedNetworkReference(t *testing.T) {
	store := resources.NewMemoryStore()
	surface := cohesionSurface(t)
	surface.NetworkProfileRef = "network"
	cohesionRecord(t, store, "network-profile", "network", map[string]any{})
	cohesionRecord(t, store, "model-surface", "surface", surface)
	h := cohesionHandler(t, store, surface.Surface.Origin)
	if _, err := h.ResolveDestination(context.Background(), "tenant", "surface"); err != nil {
		t.Fatal(err)
	}
	_ = store.Delete(context.Background(), "tenant", "network-profile", "network", 1)
	if _, err := h.ResolveDestination(context.Background(), "tenant", "surface"); err == nil {
		t.Fatal("plan retained a removed native network prerequisite")
	}
}

func TestToolRegistrationKeepsDeveloperAndOAuthIdentitiesDistinct(t *testing.T) {
	store := resources.NewMemoryStore()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer upstream.Close()
	h := cohesionHandler(t, store, upstream.URL)
	network := h.Runtime.Config.Network
	service, err := toolauth.NewService(toolauth.Config{Network: network, Credentials: toolauth.NewMemoryCredentials()})
	if err != nil {
		t.Fatal(err)
	}
	h.Tools = service
	cohesionRecord(t, store, clientplex.Kind, "developer-client", clientplex.Registration{ID: "developer-client"})
	cohesionRecord(t, store, "network-profile", "network", map[string]any{})
	tool := ToolConnection{ID: "surface", ClientRegistrationRef: "developer-client", NetworkProfileRef: "network", ResourceURL: upstream.URL + "/mcp", Issuer: upstream.URL, Client: toolauth.ClientProfile{ID: "oauth-profile", Name: "Developer tools", ApplicationType: "web", RedirectURIs: []string{"https://client.example/callback"}}}
	raw, _ := json.Marshal(tool)
	if err = h.validate(context.Background(), "tenant", "tool-connection", "surface", raw); err != nil {
		t.Fatal("unrelated developer registration and OAuth profile IDs were conflated", err)
	}
	cohesionRecord(t, store, "tool-connection", "surface", tool)
	_ = store.Delete(context.Background(), "tenant", "network-profile", "network", 1)
	w := httptest.NewRecorder()
	h.RegisterTool(w, trustedConnectedRequest("/api/v1/mcp/connections/surface/registration", "", nil))
	if w.Code != 409 || calls.Load() != 0 {
		t.Fatalf("OAuth work began before validating live network reference: status=%d calls=%d", w.Code, calls.Load())
	}
	cohesionRecord(t, store, "network-profile", "network", map[string]any{})
	h.Store = cohesionReadFailure{Store: store, kind: "oauth-registration"}
	w = httptest.NewRecorder()
	h.RegisterTool(w, trustedConnectedRequest("/api/v1/mcp/connections/surface/registration", "", nil))
	if w.Code != 500 || calls.Load() != 0 {
		t.Fatalf("OAuth side effects began despite unavailable result store: status=%d calls=%d", w.Code, calls.Load())
	}
}

func TestNativeAgentGovernanceReferenceIsCheckedForBearerProviders(t *testing.T) {
	for _, provider := range []cloudagents.Provider{cloudagents.GoogleRuntime, cloudagents.FoundryAgent, cloudagents.CopilotStudio, cloudagents.AgentCoreRuntime} {
		t.Run(string(provider), func(t *testing.T) {
			store := resources.NewMemoryStore()
			connection := cloudagents.Connection{ID: "surface", TenantID: "tenant", Provider: provider, Endpoint: "https://agents.example", ResourceID: "native-runtime", IdentityRef: "host", GovernanceBindingRef: "removed-binding", GovernanceMode: "ingress"}
			cohesionRecord(t, store, "agent-connection", "surface", connection)
			h := cohesionHandler(t, store, connection.Endpoint)
			request := trustedConnectedRequest("/api/v1/a2a/connections/surface/invoke", `{"operation":"query","payload":{}}`, []string{"a2a:agent:surface:query"})
			w := httptest.NewRecorder()
			h.InvokeAgent(w, request)
			if w.Code != 503 || !strings.Contains(w.Body.String(), "GOVERNANCE_BINDING_UNAVAILABLE") {
				t.Fatalf("bearer provider bypassed removed governance binding: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
