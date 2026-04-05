package deploy_test

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func TestGenerateManifests_MCPlex(t *testing.T) {
	inst := &models.Instance{
		ID:        "kb-search-abc123",
		Plane:     models.PlaneMCPlex,
		Namespace: "mcplex",
		Replicas:  2,
		Config:    map[string]any{"INDEX_URL": "https://example.com"},
	}
	tmpl := &models.Template{
		ID:    "kb-search",
		Image: "gcr.io/myproject/kb-search:v1.2",
	}

	manifests := deploy.GenerateManifests(inst, tmpl, "test.local")
	if len(manifests) != 4 {
		t.Fatalf("expected 4 manifests (SA, Deploy, Svc, NetPol), got %d", len(manifests))
	}

	kinds := map[string]bool{}
	for _, m := range manifests {
		kinds[m.Kind] = true
		if m.Namespace != "mcplex" {
			t.Errorf("%s: expected namespace mcplex, got %s", m.Kind, m.Namespace)
		}
	}
	for _, k := range []string{"ServiceAccount", "Deployment", "Service", "NetworkPolicy"} {
		if !kinds[k] {
			t.Errorf("missing %s manifest", k)
		}
	}

	// Check deployment has correct image and replicas
	for _, m := range manifests {
		if m.Kind == "Deployment" {
			if !strings.Contains(m.YAML, "gcr.io/myproject/kb-search:v1.2") {
				t.Error("deployment missing custom image")
			}
			if !strings.Contains(m.YAML, "replicas: 2") {
				t.Error("deployment missing replicas: 2")
			}
			if !strings.Contains(m.YAML, "INDEX_URL") {
				t.Error("deployment missing env var from config")
			}
		}
		if m.Kind == "ServiceAccount" {
			if !strings.Contains(m.YAML, "spiffe://test.local") {
				t.Error("SA missing SPIFFE annotation")
			}
		}
	}
}

func TestGenerateManifests_LLMPlex_ReturnsNil(t *testing.T) {
	inst := &models.Instance{
		ID:        "gemini-abc",
		Plane:     models.PlaneLLMPlex,
		Namespace: "llmplex",
	}
	tmpl := &models.Template{ID: "gemini-2.5-flash", ModelID: "gemini-2.5-flash"}

	manifests := deploy.GenerateManifests(inst, tmpl, "test.local")
	if manifests != nil {
		t.Errorf("LLMPlex should not generate K8s manifests, got %d", len(manifests))
	}
}

func TestGenerateRoute_MCPlex(t *testing.T) {
	inst := &models.Instance{
		ID:        "kb-search-abc",
		Plane:     models.PlaneMCPlex,
		Namespace: "mcplex",
	}
	tmpl := &models.Template{ID: "kb-search"}

	routes := deploy.GenerateRoute(inst, tmpl, "aiplex-gateway")
	if len(routes) != 1 {
		t.Fatalf("expected 1 MCPRoute, got %d", len(routes))
	}
	if routes[0].Kind != "MCPRoute" {
		t.Errorf("expected MCPRoute, got %s", routes[0].Kind)
	}
	if !strings.Contains(routes[0].YAML, "/mcp/kb-search-abc") {
		t.Error("MCPRoute missing path")
	}
}

func TestGenerateRoute_A2APlex(t *testing.T) {
	inst := &models.Instance{
		ID:        "research-abc",
		Plane:     models.PlaneA2APlex,
		Namespace: "a2aplex",
	}
	tmpl := &models.Template{ID: "research-agent"}

	routes := deploy.GenerateRoute(inst, tmpl, "aiplex-gateway")
	if len(routes) != 1 {
		t.Fatalf("expected 1 HTTPRoute, got %d", len(routes))
	}
	if routes[0].Kind != "HTTPRoute" {
		t.Errorf("expected HTTPRoute, got %s", routes[0].Kind)
	}
	if !strings.Contains(routes[0].YAML, "/a2a/research-abc") {
		t.Error("HTTPRoute missing path prefix")
	}
}

func TestGenerateRoute_LLMPlex(t *testing.T) {
	inst := &models.Instance{
		ID:        "gemini-abc",
		Plane:     models.PlaneLLMPlex,
		Namespace: "aiplex-system",
	}
	tmpl := &models.Template{
		ID:       "gemini-2.5-flash",
		ModelID:  "gemini-2.5-flash",
		Provider: "google",
	}

	routes := deploy.GenerateRoute(inst, tmpl, "aiplex-gateway")
	if len(routes) != 2 {
		t.Fatalf("expected 2 (LLMRoute + AIServiceBackend), got %d", len(routes))
	}
	if routes[0].Kind != "LLMRoute" {
		t.Errorf("expected LLMRoute, got %s", routes[0].Kind)
	}
	if routes[1].Kind != "AIServiceBackend" {
		t.Errorf("expected AIServiceBackend, got %s", routes[1].Kind)
	}
	if !strings.Contains(routes[1].YAML, "provider: google") {
		t.Error("AIServiceBackend missing provider")
	}
}
