package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func getCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get details of a resource",
	}

	cmd.AddCommand(getInstanceCmd(), getAgentCmd(), getDelegationCmd())
	return cmd
}

func getInstanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "instance <id>",
		Short: "Get instance details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			inst, err := c.GetInstance(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("get instance: %w", err)
			}

			if output == "json" {
				printJSON(inst)
				return nil
			}

			fmt.Printf("Instance: %s\n", inst.ID)
			fmt.Printf("  Plane:       %s\n", inst.Plane)
			fmt.Printf("  Template:    %s\n", inst.TemplateID)
			fmt.Printf("  Status:      %s\n", inst.Status)
			fmt.Printf("  Owner:       %s\n", inst.Owner)
			fmt.Printf("  Namespace:   %s\n", inst.Namespace)
			if inst.SpiffeID != "" {
				fmt.Printf("  SPIFFE ID:   %s\n", inst.SpiffeID)
			}
			if inst.DisplayName != "" {
				fmt.Printf("  Name:        %s\n", inst.DisplayName)
			}
			if len(inst.Scopes) > 0 {
				fmt.Printf("  Scopes:\n")
				for _, s := range inst.Scopes {
					fmt.Printf("    - %s\n", s)
				}
			}
			fmt.Printf("  Deployed:    %s\n", inst.DeployedAt.Format("2006-01-02 15:04:05"))
			return nil
		},
	}
}

func getAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent <client-id>",
		Short: "Get agent details and cross-plane permissions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := context.Background()

			agent, err := c.GetAgent(ctx, args[0])
			if err != nil {
				return fmt.Errorf("get agent: %w", err)
			}

			if output == "json" {
				printJSON(agent)
				return nil
			}

			fmt.Printf("Agent: %s\n", agent.ClientID)
			fmt.Printf("  Name:        %s\n", agent.DisplayName)
			fmt.Printf("  Status:      %s\n", agent.Status)
			fmt.Printf("  Auth:        %s\n", agent.AuthMethod)
			fmt.Printf("  Grants:      %s\n", strings.Join(agent.GrantTypes, ", "))
			if agent.SpiffeID != "" {
				fmt.Printf("  SPIFFE ID:   %s\n", agent.SpiffeID)
			}
			if len(agent.AllowedScopes) > 0 {
				fmt.Printf("  Allowed Scopes (Dimension A):\n")
				for _, s := range agent.AllowedScopes {
					fmt.Printf("    - %s\n", s)
				}
			}

			// Also fetch cross-plane permissions
			perms, err := c.GetAgentPermissions(ctx, args[0])
			if err == nil && perms != nil {
				fmt.Println()
				fmt.Println("  Cross-Plane Permissions:")
				for plane, scopes := range perms.Ceiling {
					fmt.Printf("    %s:\n", plane)
					for _, s := range scopes {
						fmt.Printf("      - %s\n", s.Scope)
					}
				}
			}

			return nil
		},
	}
}

func getDelegationCmd() *cobra.Command {
	var chain bool

	cmd := &cobra.Command{
		Use:   "delegation <id>",
		Short: "Get delegation details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := context.Background()

			if chain {
				ch, err := c.GetDelegationChain(ctx, args[0])
				if err != nil {
					return fmt.Errorf("get chain: %w", err)
				}
				if output == "json" {
					printJSON(ch)
					return nil
				}
				printDelegationChain(ch, 0)
				return nil
			}

			d, err := c.GetDelegation(ctx, args[0])
			if err != nil {
				return fmt.Errorf("get delegation: %w", err)
			}

			if output == "json" {
				printJSON(d)
				return nil
			}

			fmt.Printf("Delegation: %s\n", d.ID)
			fmt.Printf("  Caller:  %s\n", d.CallerAgentID)
			fmt.Printf("  Callee:  %s\n", d.CalleeAgentID)
			fmt.Printf("  Task:    %s\n", d.TaskType)
			fmt.Printf("  Status:  %s\n", d.Status)
			fmt.Printf("  User:    %s\n", d.UserID)
			fmt.Printf("  Started: %s\n", d.StartedAt.Format("2006-01-02 15:04:05"))
			if d.CompletedAt != nil {
				fmt.Printf("  Ended:   %s\n", d.CompletedAt.Format("2006-01-02 15:04:05"))
			}
			if d.DurationMs > 0 {
				fmt.Printf("  Duration: %dms\n", d.DurationMs)
			}
			if d.Error != "" {
				fmt.Printf("  Error:   %s\n", d.Error)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&chain, "chain", "c", false, "Show full delegation chain")
	return cmd
}

func printDelegationChain(ch *aiplex.DelegationChain, depth int) {
	indent := strings.Repeat("  ", depth)
	root := ch.RootDelegation
	fmt.Printf("%s%s → %s [%s] (%s)\n", indent, root.CallerAgentID, root.CalleeAgentID, root.TaskType, root.Status)
	for _, child := range ch.Children {
		fmt.Printf("%s  └─ %s → %s [%s] (%s)\n", indent, child.CallerAgentID, child.CalleeAgentID, child.TaskType, child.Status)
	}
	if ch.TotalDurationMs > 0 {
		fmt.Printf("%sTotal: %dms (depth: %d)\n", indent, ch.TotalDurationMs, ch.Depth)
	}
}
