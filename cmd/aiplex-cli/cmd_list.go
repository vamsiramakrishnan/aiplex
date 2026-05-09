package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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
	var kind string
	var sortBy string
	var watch bool

	cmd := &cobra.Command{
		Use:     "instances",
		Aliases: []string{"inst", "i"},
		Short:   "List deployed instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			for {
				c := newClient()
				var opts *aiplex.ListInstancesOpts
				if kind != "" {
					opts = &aiplex.ListInstancesOpts{Kind: kind}
				}
				list, err := c.ListInstances(context.Background(), opts)
				if err != nil {
					return fmt.Errorf("list instances: %w", err)
				}

				if sortBy != "" {
					sortInstances(list, sortBy)
				}

				if output == "json" {
					printJSON(list)
				} else if output == "yaml" {
					printYAML(list)
				} else {
					headers := []string{"ID", "KIND", "TEMPLATE", "STATUS", "OWNER"}
					var rows [][]string
					for _, inst := range list {
						rows = append(rows, []string{
							inst.ID, inst.Kind, inst.TemplateID, inst.Status, inst.Owner,
						})
					}
					printTable(headers, rows)
					fmt.Printf("\n%d instance(s)\n", len(list))
				}

				if !watch {
					return nil
				}
				fmt.Println("\n--- refreshing in 5s (ctrl-c to stop) ---")
				time.Sleep(5 * time.Second)
				fmt.Print("\033[H\033[2J")
			}
		},
	}
	cmd.Flags().StringVarP(&kind, "kind", "k", "", "Filter by capability kind")
	cmd.Flags().StringVar(&sortBy, "sort-by", "", "Sort results by field (id, kind, status, owner)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Watch for changes (refresh every 5s)")
	return cmd
}

func sortInstances(list []aiplex.Instance, field string) {
	sort.Slice(list, func(i, j int) bool {
		switch strings.ToLower(field) {
		case "kind":
			return list[i].Kind < list[j].Kind
		case "status":
			return list[i].Status < list[j].Status
		case "owner":
			return list[i].Owner < list[j].Owner
		default:
			return list[i].ID < list[j].ID
		}
	})
}

func listAgentsCmd() *cobra.Command {
	var sortBy string
	var watch bool

	cmd := &cobra.Command{
		Use:     "agents",
		Aliases: []string{"agent", "a"},
		Short:   "List registered agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			for {
				c := newClient()
				list, err := c.ListAgents(context.Background())
				if err != nil {
					return fmt.Errorf("list agents: %w", err)
				}

				if sortBy != "" {
					sortAgents(list, sortBy)
				}

				if output == "json" {
					printJSON(list)
				} else if output == "yaml" {
					printYAML(list)
				} else {
					headers := []string{"CLIENT_ID", "NAME", "AUTH", "STATUS", "CAPS"}
					var rows [][]string
					for _, a := range list {
						uris := make([]string, 0, len(a.AllowedCaps))
						for _, c := range a.AllowedCaps {
							uris = append(uris, c.URI)
						}
						caps := strings.Join(uris, ", ")
						if len(caps) > 60 {
							caps = caps[:57] + "..."
						}
						rows = append(rows, []string{
							a.ClientID, a.DisplayName, a.AuthMethod, a.Status, caps,
						})
					}
					printTable(headers, rows)
					fmt.Printf("\n%d agent(s)\n", len(list))
				}

				if !watch {
					return nil
				}
				fmt.Println("\n--- refreshing in 5s (ctrl-c to stop) ---")
				time.Sleep(5 * time.Second)
				fmt.Print("\033[H\033[2J")
			}
		},
	}
	cmd.Flags().StringVar(&sortBy, "sort-by", "", "Sort results by field (client_id, name, status)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Watch for changes (refresh every 5s)")
	return cmd
}

func sortAgents(list []aiplex.Agent, field string) {
	sort.Slice(list, func(i, j int) bool {
		switch strings.ToLower(field) {
		case "name":
			return list[i].DisplayName < list[j].DisplayName
		case "status":
			return list[i].Status < list[j].Status
		default: // "client_id" or unknown
			return list[i].ClientID < list[j].ClientID
		}
	})
}

func listDelegationsCmd() *cobra.Command {
	var sortBy string
	var watch bool

	cmd := &cobra.Command{
		Use:     "delegations",
		Aliases: []string{"del", "d"},
		Short:   "List A2A delegations",
		RunE: func(cmd *cobra.Command, args []string) error {
			for {
				c := newClient()
				list, err := c.ListDelegations(context.Background())
				if err != nil {
					return fmt.Errorf("list delegations: %w", err)
				}

				if sortBy != "" {
					sortDelegations(list, sortBy)
				}

				if output == "json" {
					printJSON(list)
				} else if output == "yaml" {
					printYAML(list)
				} else {
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
				}

				if !watch {
					return nil
				}
				fmt.Println("\n--- refreshing in 5s (ctrl-c to stop) ---")
				time.Sleep(5 * time.Second)
				fmt.Print("\033[H\033[2J")
			}
		},
	}
	cmd.Flags().StringVar(&sortBy, "sort-by", "", "Sort results by field (id, caller, callee, status, task)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Watch for changes (refresh every 5s)")
	return cmd
}

func sortDelegations(list []aiplex.Delegation, field string) {
	sort.Slice(list, func(i, j int) bool {
		switch strings.ToLower(field) {
		case "caller":
			return list[i].CallerAgentID < list[j].CallerAgentID
		case "callee":
			return list[i].CalleeAgentID < list[j].CalleeAgentID
		case "status":
			return list[i].Status < list[j].Status
		case "task":
			return list[i].TaskType < list[j].TaskType
		default: // "id" or unknown
			return list[i].ID < list[j].ID
		}
	})
}

func listDenialsCmd() *cobra.Command {
	var sortBy string
	var watch bool

	cmd := &cobra.Command{
		Use:     "denials",
		Aliases: []string{"deny"},
		Short:   "List policy denials",
		RunE: func(cmd *cobra.Command, args []string) error {
			for {
				c := newClient()
				list, err := c.ListPolicyDenials(context.Background())
				if err != nil {
					return fmt.Errorf("list denials: %w", err)
				}

				if sortBy != "" {
					sortDenials(list, sortBy)
				}

				if output == "json" {
					printJSON(list)
				} else if output == "yaml" {
					printYAML(list)
				} else {
					headers := []string{"ID", "KIND", "AGENT", "CAP", "ACTION", "REASON", "TIME"}
					var rows [][]string
					for _, d := range list {
						ts := d.Timestamp.Format("15:04:05")
						rows = append(rows, []string{
							d.ID, d.Kind, d.AgentID, d.CapURI, d.Action, d.Reason, ts,
						})
					}
					printTable(headers, rows)
					fmt.Printf("\n%d denial(s)\n", len(list))
				}

				if !watch {
					return nil
				}
				fmt.Println("\n--- refreshing in 5s (ctrl-c to stop) ---")
				time.Sleep(5 * time.Second)
				fmt.Print("\033[H\033[2J")
			}
		},
	}
	cmd.Flags().StringVar(&sortBy, "sort-by", "", "Sort results by field (id, kind, agent, action, time)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Watch for changes (refresh every 5s)")
	return cmd
}

func sortDenials(list []aiplex.PolicyDenial, field string) {
	sort.Slice(list, func(i, j int) bool {
		switch strings.ToLower(field) {
		case "kind":
			return list[i].Kind < list[j].Kind
		case "agent":
			return list[i].AgentID < list[j].AgentID
		case "action":
			return list[i].Action < list[j].Action
		case "time":
			return list[i].Timestamp.Before(list[j].Timestamp)
		default:
			return list[i].ID < list[j].ID
		}
	})
}
