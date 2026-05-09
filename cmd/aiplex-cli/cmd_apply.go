package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// AiplexManifest is the declarative config format for `aiplex apply -f`.
type AiplexManifest struct {
	Version   string             `json:"version" yaml:"version"`
	Instances []ManifestInstance `json:"instances,omitempty" yaml:"instances,omitempty"`
	Agents    []ManifestAgent    `json:"agents,omitempty" yaml:"agents,omitempty"`
	Routes    []ManifestRoute    `json:"routes,omitempty" yaml:"routes,omitempty"`
}

type ManifestInstance struct {
	Name     string         `json:"name" yaml:"name"`
	Kind     string         `json:"kind" yaml:"kind"`
	Template string         `json:"template" yaml:"template"`
	Config   map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
}

type ManifestAgent struct {
	ClientID    string       `json:"client_id" yaml:"client_id"`
	DisplayName string       `json:"display_name" yaml:"display_name"`
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	AuthMethod  string       `json:"auth_method" yaml:"auth_method"`
	GrantTypes  []string     `json:"grant_types" yaml:"grant_types"`
	AllowedCaps []aiplex.Cap `json:"allowed_caps" yaml:"allowed_caps"`
}

type ManifestRoute struct {
	ModelID   string               `json:"model_id" yaml:"model_id"`
	Backends  []aiplex.LLMBackend  `json:"backends" yaml:"backends"`
	Fallbacks []string             `json:"fallbacks,omitempty" yaml:"fallbacks,omitempty"`
	Budget    *aiplex.UsageBudget  `json:"budget,omitempty" yaml:"budget,omitempty"`
}

func applyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: "Apply a declarative configuration file",
		Long: `Apply instances, agents, and routes from a YAML/JSON file.

Example aiplex.yaml:
  version: v1
  instances:
    - name: knowledge-base
      kind: tool
      template: kb-search-server
    - name: research-agent
      kind: task
      template: research-agent
  agents:
    - client_id: tutor-agent
      display_name: Tutor Agent
      auth_method: client_credentials
      grant_types: [client_credentials]
      allowed_caps:
        - {uri: cap://tool/search_curriculum@v1, actions: [call]}
        - {uri: cap://task/research@v1, actions: [invoke]}
        - {uri: cap://model/gemini-2.5-flash@v1, actions: [complete]}
  routes:
    - model_id: gemini-2.5-flash
      backends:
        - provider: google
          model_id: gemini-2.5-flash
          weight: 80
          enabled: true
        - provider: anthropic
          model_id: claude-sonnet-4-20250514
          weight: 20
          enabled: true
      budget:
        max_daily_cost_usd: 50

Usage:
  aiplex apply -f aiplex.yaml
  aiplex apply -f instances.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			file, _ := cmd.Flags().GetString("file")
			if file == "" {
				return fmt.Errorf("specify a file with -f")
			}

			manifest, err := loadManifest(file)
			if err != nil {
				return fmt.Errorf("load manifest: %w", err)
			}

			c := newClient()
			ctx := context.Background()
			var errors []string

			// Apply instances
			for _, inst := range manifest.Instances {
				fmt.Printf("Deploying %s (%s/%s)... ", inst.Name, inst.Kind, inst.Template)
				result, err := c.Deploy(ctx, &aiplex.DeployRequest{
					Kind:        inst.Kind,
					TemplateID:  inst.Template,
					DisplayName: inst.Name,
					Config:      inst.Config,
				})
				if err != nil {
					fmt.Printf("FAILED: %v\n", err)
					errors = append(errors, fmt.Sprintf("instance %s: %v", inst.Name, err))
				} else {
					fmt.Printf("OK (%s)\n", result.ID)
				}
			}

			// Apply agents
			for _, agent := range manifest.Agents {
				fmt.Printf("Registering agent %s... ", agent.ClientID)
				_, err := c.RegisterAgent(ctx, &aiplex.RegisterAgentRequest{
					ClientID:    agent.ClientID,
					DisplayName: agent.DisplayName,
					Description: agent.Description,
					AuthMethod:  agent.AuthMethod,
					GrantTypes:  agent.GrantTypes,
					AllowedCaps: agent.AllowedCaps,
				})
				if err != nil {
					fmt.Printf("FAILED: %v\n", err)
					errors = append(errors, fmt.Sprintf("agent %s: %v", agent.ClientID, err))
				} else {
					fmt.Println("OK")
				}
			}

			// Apply routes
			for _, route := range manifest.Routes {
				fmt.Printf("Configuring route %s... ", route.ModelID)
				_, err := c.PutLLMRoute(ctx, route.ModelID, &aiplex.LLMRouteConfig{
					ModelID:   route.ModelID,
					Backends:  route.Backends,
					Fallbacks: route.Fallbacks,
					Budget:    route.Budget,
				})
				if err != nil {
					fmt.Printf("FAILED: %v\n", err)
					errors = append(errors, fmt.Sprintf("route %s: %v", route.ModelID, err))
				} else {
					fmt.Println("OK")
				}
			}

			fmt.Println()
			total := len(manifest.Instances) + len(manifest.Agents) + len(manifest.Routes)
			if len(errors) > 0 {
				fmt.Printf("Applied %d/%d resources (%d failed)\n", total-len(errors), total, len(errors))
				for _, e := range errors {
					fmt.Printf("  - %s\n", e)
				}
				return fmt.Errorf("%d resource(s) failed", len(errors))
			}

			fmt.Printf("Applied %d resource(s) successfully.\n", total)
			return nil
		},
	}

	cmd.Flags().StringP("file", "f", "", "Path to manifest file (YAML or JSON)")
	cmd.MarkFlagRequired("file")
	return cmd
}

func loadManifest(path string) (*AiplexManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(path))
	var manifest AiplexManifest

	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("parse JSON: %w", err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("parse YAML: %w", err)
		}
	default:
		// Try YAML first (superset of JSON), then JSON
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			if err2 := json.Unmarshal(data, &manifest); err2 != nil {
				return nil, fmt.Errorf("unsupported format %s — use .json or .yaml", ext)
			}
		}
	}

	if manifest.Version == "" {
		manifest.Version = "v1"
	}
	return &manifest, nil
}
