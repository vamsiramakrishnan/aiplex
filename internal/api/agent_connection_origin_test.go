package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// An agent connection may name an http loopback origin only when the endpoint
// policy has allow_loopback (which the API permits only in development mode:
// cmd/aiplex-api loadConnectedConfig). Without it the rule is the https rule.
func TestAgentConnectionHTTPEndpointFollowsAllowLoopback(t *testing.T) {
	spec := func(endpoint string) []byte {
		raw, err := json.Marshal(cloudagents.Connection{ID: "agent", TenantID: "tenant", Provider: cloudagents.GoogleRuntime, Endpoint: endpoint,
			ResourceID: "projects/p/locations/l/reasoningEngines/e", IdentityRef: "host", GovernanceMode: "invocation"})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	handler := func(allowLoopback bool, origins ...string) *ConnectedHandler {
		runtime, err := connections.New(connections.Config{
			Network:    endpointpolicy.Config{AllowedOrigins: origins, AllowLoopback: allowLoopback},
			Identities: []connections.Identity{{ID: "host", TenantID: "tenant", Type: "bearer-env", TokenEnv: "AGENT_ORIGIN_TEST", AllowedOrigins: origins}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return NewConnectedHandler(resources.NewMemoryStore(), runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, allowLoopback)
	}
	const local = "http://127.0.0.1:9000"
	dev := handler(true, local, "https://agents.example")
	if err := dev.validate(context.Background(), "tenant", "agent-connection", "agent", spec(local)); err != nil {
		t.Fatalf("development mode refused a loopback http agent: %v", err)
	}
	for _, endpoint := range []string{"http://127.0.0.1.nip.io:9000", "http://0.0.0.0:9000", "http://169.254.169.254", "http://u:p@127.0.0.1:9000", "http://agents.example"} {
		if err := dev.validate(context.Background(), "tenant", "agent-connection", "agent", spec(endpoint)); err == nil {
			t.Errorf("development mode accepted %s", endpoint)
		}
	}
	// Production: the same loopback endpoint is refused by the https rule, and an
	// https endpoint is unchanged.
	prod := handler(false, "https://agents.example")
	err := prod.validate(context.Background(), "tenant", "agent-connection", "agent", spec(local))
	if err == nil || !strings.Contains(err.Error(), "pinned HTTPS origin") {
		t.Fatalf("production accepted or mis-reported a loopback http agent: %v", err)
	}
	if err := prod.validate(context.Background(), "tenant", "agent-connection", "agent", spec("https://agents.example")); err != nil {
		t.Fatalf("production refused an https agent: %v", err)
	}
}

// The allow_loopback value is read through one helper that is false without a
// runtime, so no call site can dereference a missing one.
func TestAllowLoopbackHelperWithoutRuntime(t *testing.T) {
	if (&ConnectedHandler{}).allowLoopback() {
		t.Fatal("a handler with no runtime allowed loopback")
	}
	var h *ConnectedHandler
	if h.allowLoopback() {
		t.Fatal("a nil handler allowed loopback")
	}
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{"http://127.0.0.1:9"}, AllowLoopback: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !(&ConnectedHandler{Runtime: runtime}).allowLoopback() {
		t.Fatal("allow_loopback not read from the runtime")
	}
}
