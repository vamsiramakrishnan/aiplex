package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func TestOfficialMCPSource_Fetch(t *testing.T) {
	// Mock registry server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/servers" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"name": "github-mcp", "description": "GitHub tools", "repository": "ghcr.io/github/mcp-server"},
			{"name": "slack-mcp", "description": "Slack tools", "tags": []string{"communication"}},
		})
	}))
	defer srv.Close()

	source := NewOfficialMCPSource(srv.URL)
	templates, err := source.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("got %d templates, want 2", len(templates))
	}
	if templates[0].Name != "github-mcp" {
		t.Errorf("templates[0].Name = %q, want github-mcp", templates[0].Name)
	}
	if templates[0].Plane != models.PlaneMCPlex {
		t.Errorf("templates[0].Plane = %q, want %q", templates[0].Plane, models.PlaneMCPlex)
	}
	if templates[0].Source != "official-mcp-registry" {
		t.Errorf("templates[0].Source = %q, want official-mcp-registry", templates[0].Source)
	}
	if !templates[0].Verified {
		t.Errorf("templates[0].Verified = false, want true")
	}
	if templates[0].Repository != "ghcr.io/github/mcp-server" {
		t.Errorf("templates[0].Repository = %q, want ghcr.io/github/mcp-server", templates[0].Repository)
	}
}

func TestOfficialMCPSource_FetchError(t *testing.T) {
	source := NewOfficialMCPSource("http://localhost:1")
	_, err := source.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable registry")
	}
}

func TestOfficialMCPSource_FetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	source := NewOfficialMCPSource(srv.URL)
	_, err := source.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestOfficialMCPSource_DefaultURL(t *testing.T) {
	source := NewOfficialMCPSource("")
	if source.registryURL != "https://registry.modelcontextprotocol.io" {
		t.Errorf("registryURL = %q, want https://registry.modelcontextprotocol.io", source.registryURL)
	}
}

func TestOfficialMCPSource_Name(t *testing.T) {
	source := NewOfficialMCPSource("")
	if source.Name() != "official-mcp-registry" {
		t.Errorf("Name() = %q, want official-mcp-registry", source.Name())
	}
}

func TestOfficialMCPSource_Plane(t *testing.T) {
	source := NewOfficialMCPSource("")
	if source.Plane() != models.PlaneMCPlex {
		t.Errorf("Plane() = %q, want %q", source.Plane(), models.PlaneMCPlex)
	}
}
