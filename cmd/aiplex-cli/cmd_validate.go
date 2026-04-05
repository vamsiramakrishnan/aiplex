package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file.yaml|file.json>",
		Short: "Validate an AIPlex manifest file",
		Long: `Validate a YAML or JSON manifest against the expected AIPlex schema.

The manifest must contain a top-level 'version' field and at least one of:
  instances, agents, routes

Example:
  aiplex validate deploy.yaml
  aiplex validate config.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			var manifest map[string]any

			ext := strings.ToLower(filepath.Ext(path))
			switch ext {
			case ".yaml", ".yml":
				if err := yaml.Unmarshal(data, &manifest); err != nil {
					return fmt.Errorf("invalid YAML: %w", err)
				}
			case ".json":
				if err := json.Unmarshal(data, &manifest); err != nil {
					return fmt.Errorf("invalid JSON: %w", err)
				}
			default:
				return fmt.Errorf("unsupported file extension %q (expected .yaml, .yml, or .json)", ext)
			}

			var errors []string

			// Check required 'version' field.
			ver, ok := manifest["version"]
			if !ok {
				errors = append(errors, "missing required field: version")
			} else if _, ok := ver.(string); !ok {
				errors = append(errors, "field 'version' must be a string")
			}

			// Check that at least one known resource section exists.
			knownSections := []string{"instances", "agents", "routes"}
			foundSection := false
			for _, s := range knownSections {
				if v, ok := manifest[s]; ok {
					foundSection = true
					// Each section should be a list.
					switch v.(type) {
					case []any:
						// ok
					default:
						errors = append(errors, fmt.Sprintf("field '%s' must be a list", s))
					}
				}
			}
			if !foundSection {
				errors = append(errors, fmt.Sprintf("manifest must contain at least one of: %s", strings.Join(knownSections, ", ")))
			}

			// Validate instances entries if present.
			if instances, ok := manifest["instances"]; ok {
				if items, ok := instances.([]any); ok {
					for i, item := range items {
						m, ok := item.(map[string]any)
						if !ok {
							errors = append(errors, fmt.Sprintf("instances[%d]: must be a mapping", i))
							continue
						}
						for _, required := range []string{"id", "plane"} {
							if _, ok := m[required]; !ok {
								errors = append(errors, fmt.Sprintf("instances[%d]: missing required field '%s'", i, required))
							}
						}
						if plane, ok := m["plane"].(string); ok {
							validPlanes := map[string]bool{"mcplex": true, "a2aplex": true, "llmplex": true}
							if !validPlanes[plane] {
								errors = append(errors, fmt.Sprintf("instances[%d]: invalid plane %q (expected mcplex, a2aplex, or llmplex)", i, plane))
							}
						}
					}
				}
			}

			// Validate agents entries if present.
			if agents, ok := manifest["agents"]; ok {
				if items, ok := agents.([]any); ok {
					for i, item := range items {
						m, ok := item.(map[string]any)
						if !ok {
							errors = append(errors, fmt.Sprintf("agents[%d]: must be a mapping", i))
							continue
						}
						if _, ok := m["client_id"]; !ok {
							errors = append(errors, fmt.Sprintf("agents[%d]: missing required field 'client_id'", i))
						}
					}
				}
			}

			// Validate routes entries if present.
			if routes, ok := manifest["routes"]; ok {
				if items, ok := routes.([]any); ok {
					for i, item := range items {
						m, ok := item.(map[string]any)
						if !ok {
							errors = append(errors, fmt.Sprintf("routes[%d]: must be a mapping", i))
							continue
						}
						if _, ok := m["name"]; !ok {
							errors = append(errors, fmt.Sprintf("routes[%d]: missing required field 'name'", i))
						}
					}
				}
			}

			if len(errors) > 0 {
				fmt.Fprintf(os.Stderr, "Validation failed for %s:\n", path)
				for _, e := range errors {
					fmt.Fprintf(os.Stderr, "  - %s\n", e)
				}
				return fmt.Errorf("%d validation error(s)", len(errors))
			}

			fmt.Printf("Manifest %s is valid.\n", path)
			return nil
		},
	}
}
