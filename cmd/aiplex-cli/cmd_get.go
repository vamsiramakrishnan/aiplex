package main

import (
	"context"
	"fmt"
	"os"
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
	var history bool
	cmd := &cobra.Command{
		Use:   "instance <id>",
		Short: "Get instance details",
		Long: `Get one instance's details. With --history, show its deploy history
(deploy, undeploy and config changes) instead. The API returns at most 50
history events for an instance.`,
		Example: "  aiplex get instance i-1\n  aiplex get instance i-1 --history\n  aiplex get instance i-1 --history -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if history {
				return getInstanceHistory(cmd, c, args[0])
			}
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
	cmd.Flags().BoolVar(&history, "history", false, "Show the instance's deploy history instead of its details")
	return cmd
}

// getInstanceHistory prints an instance's deploy history in the order the API
// returns it.
func getInstanceHistory(cmd *cobra.Command, c *aiplex.Client, id string) error {
	history, err := c.GetHistory(cmd.Context(), id)
	if err != nil {
		return fmt.Errorf("get instance history: %w", err)
	}
	if history == nil {
		history = []aiplex.DeployHistory{}
	}
	if spStructured(cmd, history) {
		return nil
	}

	out := cmd.OutOrStdout()
	if len(history) == 0 {
		fmt.Fprintf(out, "No history recorded for instance %s.\n", id)
		return nil
	}
	rows := make([][]string, 0, len(history))
	for _, h := range history {
		success := "no"
		if h.Success {
			success = "yes"
		}
		rows = append(rows, []string{runsTime(h.Timestamp), orUnknown(h.Action), success, orUnknown(h.PerformedBy), dashIfEmpty(h.Error)})
	}
	spTable(out, []string{"TIME", "ACTION", "SUCCESS", "BY", "ERROR"}, rows)
	fmt.Fprintf(out, "\n%d history event(s)\n", len(history))
	if len(history) >= 50 {
		fmt.Fprintln(out, "The API returns at most 50 events; older ones are not shown.")
	}
	return nil
}

func getAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent <client-id>",
		Short: "Show an agent's details and the scopes it may be granted",
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
				fmt.Printf("  Allowed scopes (the most this agent may request):\n")
				for _, s := range agent.AllowedScopes {
					fmt.Printf("    - %s\n", s)
				}
			}

			// Also fetch the permissions grouped by module
			perms, err := c.GetAgentPermissions(ctx, args[0])
			if err == nil && perms != nil {
				fmt.Println()
				fmt.Println("  Permissions by module:")
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
					return a2aErr("get chain", err)
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
				return a2aErr("get delegation", err)
			}

			if output == "json" {
				printJSON(d)
				return nil
			}

			writeDelegation(os.Stdout, d)

			return nil
		},
	}

	cmd.Flags().BoolVarP(&chain, "chain", "c", false, "Show full delegation chain")
	return cmd
}

func printDelegationChain(ch *aiplex.DelegationChain, depth int) {
	writeDelegationChain(os.Stdout, ch, depth)
}
