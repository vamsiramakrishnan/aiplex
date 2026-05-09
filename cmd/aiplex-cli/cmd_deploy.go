package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func deployCmd() *cobra.Command {
	var (
		kind        string
		displayName string
	)

	cmd := &cobra.Command{
		Use:   "deploy <template-id>",
		Short: "Deploy an instance from a catalog template",
		Long: `Deploy a tool, task agent, model proxy, skill server, or memory namespace
from the catalog. The kind defaults from the template if not provided.

Examples:
  aiplex deploy kb-search --kind tool
  aiplex deploy research-agent --kind task --name "My Research Agent"
  aiplex deploy gemini-2.5-flash --kind model`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := context.Background()

			inst, err := c.Deploy(ctx, &aiplex.DeployRequest{
				Kind:        kind,
				TemplateID:  args[0],
				DisplayName: displayName,
			})
			if err != nil {
				return fmt.Errorf("deploy: %w", err)
			}

			if output == "json" {
				printJSON(inst)
				return nil
			}

			fmt.Printf("Deployed %s\n", inst.ID)
			fmt.Printf("  Kind:         %s\n", inst.Kind)
			fmt.Printf("  Template:     %s\n", inst.TemplateID)
			fmt.Printf("  Status:       %s\n", inst.Status)
			if len(inst.Capabilities) > 0 {
				fmt.Printf("  Capabilities:\n")
				for _, c := range inst.Capabilities {
					fmt.Printf("    - %s %v\n", c.URI, c.Actions)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&kind, "kind", "k", "", "Capability kind: tool, task, model, skill, memory (defaults to template's kind)")
	cmd.Flags().StringVarP(&displayName, "name", "n", "", "Display name for the instance")

	return cmd
}

func undeployCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undeploy <instance-id>",
		Short: "Terminate and remove a deployed instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if err := c.Undeploy(context.Background(), args[0]); err != nil {
				return fmt.Errorf("undeploy: %w", err)
			}
			fmt.Printf("Undeployed %s\n", args[0])
			return nil
		},
	}
}
