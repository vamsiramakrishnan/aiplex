package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List resources",
	}

	cmd.AddCommand(listInstancesCmd(), listAgentsCmd(), listDelegationsCmd(), listDenialsCmd())
	return cmd
}

func listInstancesCmd() *cobra.Command {
	var plane string

	cmd := &cobra.Command{
		Use:     "instances",
		Aliases: []string{"inst", "i"},
		Short:   "List deployed instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			var opts *aiplex.ListInstancesOpts
			if plane != "" {
				opts = &aiplex.ListInstancesOpts{Plane: plane}
			}
			list, err := c.ListInstances(context.Background(), opts)
			if err != nil {
				return fmt.Errorf("list instances: %w", err)
			}

			if output == "json" {
				printJSON(list)
				return nil
			}

			headers := []string{"ID", "PLANE", "TEMPLATE", "STATUS", "OWNER"}
			var rows [][]string
			for _, inst := range list {
				rows = append(rows, []string{
					inst.ID, inst.Plane, inst.TemplateID, inst.Status, inst.Owner,
				})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d instance(s)\n", len(list))
			return nil
		},
	}
	cmd.Flags().StringVarP(&plane, "plane", "p", "", "Filter by plane")
	return cmd
}

func listAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "agents",
		Aliases: []string{"agent", "a"},
		Short:   "List registered agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			list, err := c.ListAgents(context.Background())
			if err != nil {
				return fmt.Errorf("list agents: %w", err)
			}

			if output == "json" {
				printJSON(list)
				return nil
			}

			headers := []string{"CLIENT_ID", "NAME", "AUTH", "STATUS", "SCOPES"}
			var rows [][]string
			for _, a := range list {
				scopes := strings.Join(a.AllowedScopes, ", ")
				if len(scopes) > 60 {
					scopes = scopes[:57] + "..."
				}
				rows = append(rows, []string{
					a.ClientID, a.DisplayName, a.AuthMethod, a.Status, scopes,
				})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d agent(s)\n", len(list))
			return nil
		},
	}
}

func listDelegationsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delegations",
		Aliases: []string{"del", "d"},
		Short:   "List A2A delegations",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			list, err := c.ListDelegations(context.Background())
			if err != nil {
				return fmt.Errorf("list delegations: %w", err)
			}

			if output == "json" {
				printJSON(list)
				return nil
			}

			headers := []string{"ID", "CALLER", "CALLEE", "TASK", "STATUS", "DURATION"}
			var rows [][]string
			for _, d := range list {
				dur := "-"
				if d.DurationMs > 0 {
					dur = fmt.Sprintf("%dms", d.DurationMs)
				}
				rows = append(rows, []string{
					d.ID, d.CallerAgentID, d.CalleeAgentID, d.TaskType, d.Status, dur,
				})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d delegation(s)\n", len(list))
			return nil
		},
	}
}

func listDenialsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "denials",
		Aliases: []string{"deny"},
		Short:   "List policy denials",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			list, err := c.ListPolicyDenials(context.Background())
			if err != nil {
				return fmt.Errorf("list denials: %w", err)
			}

			if output == "json" {
				printJSON(list)
				return nil
			}

			headers := []string{"ID", "PLANE", "AGENT", "ACTION", "REASON", "TIME"}
			var rows [][]string
			for _, d := range list {
				ts := d.Timestamp.Format("15:04:05")
				rows = append(rows, []string{
					d.ID, d.Plane, d.AgentID, d.Action, d.Reason, ts,
				})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d denial(s)\n", len(list))
			return nil
		},
	}
}
