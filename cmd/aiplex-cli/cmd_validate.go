package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file.yaml|file.json>",
		Short: "Check a manifest file exactly as `aiplex apply` would, without applying it",
		Long: `Runs every check "aiplex apply -f" makes before it writes anything, and stops
there: the file must parse (unknown fields are errors), 'version' must be v1 or
omitted, at least one of instances, agents or routes must be present, and each
entry needs its required fields:

  instances  name, plane (mcplex, a2aplex, llmplex or skillsplex), template
  agents     client_id; authorization_code agents need redirect_uris
  routes     model_id and at least one backend

It does not contact a server, so it cannot know whether a template, secret or
fallback route exists.

Example:
  aiplex validate deploy.yaml
  aiplex validate config.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			manifest, err := loadManifest(path)
			if err != nil {
				return fmt.Errorf("load manifest: %w", err)
			}
			if problems := validateManifest(manifest); len(problems) > 0 {
				fmt.Fprintf(os.Stderr, "Validation failed for %s:\n", path)
				for _, p := range problems {
					fmt.Fprintf(os.Stderr, "  - %s\n", p)
				}
				return fmt.Errorf("%d validation error(s)", len(problems))
			}
			fmt.Printf("Manifest %s is valid.\n", path)
			return nil
		},
	}
}
