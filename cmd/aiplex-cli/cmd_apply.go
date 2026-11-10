package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
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
	Plane    string         `json:"plane" yaml:"plane"`
	Template string         `json:"template" yaml:"template"`
	Config   map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
}

type ManifestAgent struct {
	ClientID      string   `json:"client_id" yaml:"client_id"`
	DisplayName   string   `json:"display_name" yaml:"display_name"`
	Description   string   `json:"description,omitempty" yaml:"description,omitempty"`
	AuthMethod    string   `json:"auth_method" yaml:"auth_method"`
	GrantTypes    []string `json:"grant_types" yaml:"grant_types"`
	AllowedScopes []string `json:"allowed_scopes" yaml:"allowed_scopes"`
	WIFPrincipal  string   `json:"wif_principal,omitempty" yaml:"wif_principal,omitempty"`
	// RedirectURIs are required for auth_method authorization_code (an IDE or
	// other PKCE client) and rejected for any other method.
	RedirectURIs []string `json:"redirect_uris,omitempty" yaml:"redirect_uris,omitempty"`
}

type ManifestRoute struct {
	Revision  int64               `json:"revision,omitempty" yaml:"revision,omitempty"`
	ModelID   string              `json:"model_id" yaml:"model_id"`
	Backends  []aiplex.LLMBackend `json:"backends" yaml:"backends"`
	Fallbacks []string            `json:"fallbacks,omitempty" yaml:"fallbacks,omitempty"`
	Budget    *aiplex.UsageBudget `json:"budget,omitempty" yaml:"budget,omitempty"`
}

func applyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: "Apply a declarative configuration file",
		Long: `Apply instances, agents, and routes from a YAML/JSON file. The file is checked
first with the same checks as "aiplex validate"; nothing is applied if it fails.

Apply converges: running it again with the same file changes nothing. Each
object is reported as created (it was absent), updated (it differed and could
be replaced), unchanged (it already matched) or skipped, then a summary. The
exit status is non-zero only when an object failed. --dry-run prints the same
plan and writes nothing.

  routes     Replaced at the revision just read (If-Match), keeping a saved
             cache TTL and rate limit the file does not mention. A revision
             written in the file is a precondition: apply fails if the saved
             route is at a different revision.
  agents     Created when absent. The API has no update for an agent (its OAuth
             client is issued once), so an agent that differs fails and names
             the field; delete it and apply again.
  instances  Matched by plane and name. A different template or configuration
             fails the same way; undeploy it first.

Routes are applied in file order, so a route named as a fallback must come
before the route that lists it. Under "aiplex dev", instances are skipped
(they need Kubernetes and a template catalog).

Example aiplex.yaml:
  version: v1
  instances:
    - name: knowledge-base
      plane: mcplex
      template: kb-search-server
    - name: research-agent
      plane: a2aplex
      template: research-agent
  agents:
    - client_id: tutor-agent
      display_name: Tutor Agent
      auth_method: client_credentials
      grant_types: [client_credentials]
      allowed_scopes:
        - mcp:tools:search_curriculum
        - a2a:task:research
        - llm:model:gemini-2.5-flash
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

			// The same checks `aiplex validate` runs, before anything is written.
			if problems := validateManifest(manifest); len(problems) > 0 {
				fmt.Fprintf(os.Stderr, "Nothing was applied: %s is not valid:\n", file)
				for _, p := range problems {
					fmt.Fprintf(os.Stderr, "  - %s\n", p)
				}
				return fmt.Errorf("%d validation error(s)", len(problems))
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			// Instances deploy onto Kubernetes from the platform's template
			// catalog. aiplex dev has neither, so say so instead of failing
			// with "template not found".
			skipReason := ""
			if len(manifest.Instances) > 0 && localDevAPI() {
				skipReason = "aiplex dev has no Kubernetes cluster or template catalog; apply instances against a cloud deployment"
			}

			results := applyManifest(context.Background(), newClient(), manifest, dryRun, skipReason)
			failed := printApplyResults(cmd.OutOrStdout(), results, dryRun)
			if failed > 0 {
				for _, r := range results {
					if r.Action == actFailed {
						fmt.Fprintf(os.Stderr, "%s %s: %s\n", r.Kind, r.Name, r.Detail)
					}
				}
				return fmt.Errorf("%d resource(s) failed", failed)
			}
			return nil
		},
	}

	cmd.Flags().Bool("dry-run", false, "Show what would be created, updated, or left unchanged, and write nothing")
	cmd.Flags().StringP("file", "f", "", "Path to manifest file (YAML or JSON)")
	cmd.MarkFlagRequired("file")
	return cmd
}

// localDevAPI reports whether the current API is the one `aiplex dev` started.
func localDevAPI() bool {
	path, err := devStatePath()
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var st devState
	if json.Unmarshal(raw, &st) != nil || st.URL == "" {
		return false
	}
	return strings.TrimRight(st.URL, "/") == strings.TrimRight(resolveAPIURL(), "/")
}
