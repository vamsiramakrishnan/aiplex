package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// OfficialMCPSource fetches MCP server templates from the official registry.
type OfficialMCPSource struct {
	registryURL string
	client      *http.Client
}

// NewOfficialMCPSource creates a source that fetches from the official MCP registry.
func NewOfficialMCPSource(registryURL string) *OfficialMCPSource {
	if registryURL == "" {
		registryURL = "https://registry.modelcontextprotocol.io"
	}
	return &OfficialMCPSource{
		registryURL: registryURL,
		client:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (o *OfficialMCPSource) Name() string          { return "official-mcp-registry" }
func (o *OfficialMCPSource) Plane() models.Plane   { return models.PlaneMCPlex }

// registryEntry represents a single server from the MCP registry API.
type registryEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Repository  string   `json:"repository"`
	Homepage    string   `json:"homepage"`
	Tags        []string `json:"tags"`
}

func (o *OfficialMCPSource) Fetch(ctx context.Context) ([]models.Template, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", o.registryURL+"/api/servers", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch MCP registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP registry returned status %d", resp.StatusCode)
	}

	var entries []registryEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode MCP registry: %w", err)
	}

	templates := make([]models.Template, 0, len(entries))
	for _, entry := range entries {
		tmpl := models.Template{
			ID:          entry.Name,
			Name:        entry.Name,
			Description: entry.Description,
			Plane:       models.PlaneMCPlex,
			Source:      "official-mcp-registry",
			Category:    "mcp-server",
			Verified:    true, // Official registry entries are verified
		}
		if entry.Repository != "" {
			tmpl.Repository = entry.Repository
			tmpl.Image = entry.Repository // Use repository as image for now
		}
		if len(entry.Tags) > 0 {
			tmpl.Tags = entry.Tags
		}
		templates = append(templates, tmpl)
	}

	return templates, nil
}
