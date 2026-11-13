package deploy_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

type discoveryTripwireTransport func(*http.Request) (*http.Response, error)

func (f discoveryTripwireTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type discoveryOptInK8s struct{ deploy.NoOpK8sClient }

func (*discoveryOptInK8s) CapabilityDiscoveryEnabled() bool { return true }

func TestDeploymentDiscoveryRequiresAdapterOptIn(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	requests := 0
	http.DefaultTransport = discoveryTripwireTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("unexpected discovery request: %s", r.URL)
	})
	for _, adapter := range []struct {
		name   string
		client deploy.K8sClient
	}{
		{"noop", deploy.NewNoOpK8sClient()},
		{"unknown", &routeCountingK8s{}},
	} {
		for _, plane := range []models.Plane{models.PlaneMCPlex, models.PlaneA2APlex, models.PlaneSkillsPlex} {
			t.Run(adapter.name+"/"+string(plane), func(t *testing.T) {
				store := registry.NewMemoryStore()
				tmpl := &models.Template{ID: "test", Plane: plane, Tools: []models.ToolInfo{{Name: "template-tool"}}, TaskTypes: []string{"template-task"}, Skills: []models.SkillInfo{{Name: "template-skill"}}}
				if err := store.PutTemplate(context.Background(), tmpl); err != nil {
					t.Fatal(err)
				}
				engine := deploy.NewEngineWithK8s(store, adapter.client, "test.local", "gateway")
				inst, err := engine.Deploy(context.Background(), plane, tmpl.ID, nil, "owner", "")
				if err != nil {
					t.Fatal(err)
				}
				if len(inst.Scopes) != 1 || !strings.Contains(inst.Scopes[0], "template-") {
					t.Fatalf("lost template scopes: %v", inst.Scopes)
				}
			})
		}
	}
	if requests != 0 {
		t.Fatalf("local adapters performed %d network probes", requests)
	}
}

func TestOptedInAdapterDiscoversThroughFakeTransport(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	// A compliant MCP server (the SDK's own) behind the faked transport.
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "discovered-tool"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	backend := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil))
	t.Cleanup(backend.Close)
	backendURL, _ := url.Parse(backend.URL)
	requests := 0
	http.DefaultTransport = discoveryTripwireTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if !strings.HasSuffix(r.URL.Host, ".svc.cluster.local:8080") || r.URL.Path != "/mcp" {
			t.Fatalf("unexpected discovery target: %s", r.URL)
		}
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host, r.Host = backendURL.Scheme, backendURL.Host, backendURL.Host
		return original.RoundTrip(r)
	})
	store := registry.NewMemoryStore()
	tmpl := &models.Template{ID: "test", Plane: models.PlaneMCPlex, Tools: []models.ToolInfo{{Name: "template-tool"}}}
	if err := store.PutTemplate(context.Background(), tmpl); err != nil {
		t.Fatal(err)
	}
	engine := deploy.NewEngineWithK8s(store, &discoveryOptInK8s{}, "test.local", "gateway")
	inst, err := engine.Deploy(context.Background(), tmpl.Plane, tmpl.ID, nil, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if requests == 0 || len(inst.Scopes) != 1 || inst.Scopes[0] != "mcp:tools:discovered-tool" {
		t.Fatalf("discovery not used: requests=%d scopes=%v", requests, inst.Scopes)
	}
}

func TestLiveDiscoveryDisabledForDryRun(t *testing.T) {
	if deploy.NewLiveK8sClientConfigured(deploy.WithDryRun(true)).CapabilityDiscoveryEnabled() {
		t.Fatal("dry-run enables cluster probes")
	}
	if !deploy.NewLiveK8sClientConfigured(deploy.WithDryRun(false)).CapabilityDiscoveryEnabled() {
		t.Fatal("live apply disables explicit discovery")
	}
}
