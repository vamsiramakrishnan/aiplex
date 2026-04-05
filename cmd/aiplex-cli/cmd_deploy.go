package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func deployCmd() *cobra.Command {
	var (
		plane       string
		displayName string
	)

	cmd := &cobra.Command{
		Use:   "deploy <template-id>",
		Short: "Deploy an instance from a catalog template",
		Long: `Deploy an MCP server, A2A agent, or LLM provider from the catalog.

Examples:
  aiplex deploy kb-search --plane mcplex
  aiplex deploy research-agent --plane a2aplex --name "My Research Agent"
  aiplex deploy gemini-2.5-flash --plane llmplex`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := context.Background()

			inst, err := c.Deploy(ctx, &aiplex.DeployRequest{
				Plane:       plane,
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
			fmt.Printf("  Plane:    %s\n", inst.Plane)
			fmt.Printf("  Template: %s\n", inst.TemplateID)
			fmt.Printf("  Status:   %s\n", inst.Status)
			if len(inst.Scopes) > 0 {
				fmt.Printf("  Scopes:   %v\n", inst.Scopes)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&plane, "plane", "p", "", "Target plane: mcplex, a2aplex, llmplex (required)")
	cmd.Flags().StringVarP(&displayName, "name", "n", "", "Display name for the instance")
	cmd.MarkFlagRequired("plane")

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
