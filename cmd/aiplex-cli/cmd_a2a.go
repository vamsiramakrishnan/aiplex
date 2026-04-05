package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func a2aCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "a2a",
		Short: "A2APlex — manage agent delegation and discovery",
	}

	cmd.AddCommand(a2aAgentsCmd(), a2aDelegateCmd(), a2aCardCmd())
	return cmd
}

func a2aAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "List running A2A agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			cards, err := c.ListAgentCards(context.Background())
			if err != nil {
				return fmt.Errorf("list a2a agents: %w", err)
			}

			if output == "json" {
				printJSON(cards)
				return nil
			}

			headers := []string{"INSTANCE", "NAME", "URL", "STATUS"}
			var rows [][]string
			for _, card := range cards {
				name := fmt.Sprintf("%v", card["name"])
				url := fmt.Sprintf("%v", card["url"])
				status := fmt.Sprintf("%v", card["status"])
				instID := fmt.Sprintf("%v", card["instance_id"])
				rows = append(rows, []string{instID, name, url, status})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d A2A agent(s)\n", len(cards))
			return nil
		},
	}
}

func a2aDelegateCmd() *cobra.Command {
	var (
		caller   string
		callee   string
		taskType string
		userID   string
	)

	cmd := &cobra.Command{
		Use:   "delegate",
		Short: "Record a new agent-to-agent delegation",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()

			d, err := c.RecordDelegation(context.Background(), &aiplex.RecordDelegationRequest{
				CallerAgentID: caller,
				CalleeAgentID: callee,
				TaskType:      taskType,
				UserID:        userID,
			})
			if err != nil {
				return fmt.Errorf("record delegation: %w", err)
			}

			if output == "json" {
				printJSON(d)
				return nil
			}

			fmt.Printf("Delegation: %s\n", d.ID)
			fmt.Printf("  %s → %s [%s]\n", d.CallerAgentID, d.CalleeAgentID, d.TaskType)
			fmt.Printf("  Status: %s\n", d.Status)
			return nil
		},
	}

	cmd.Flags().StringVar(&caller, "caller", "", "Caller agent ID (required)")
	cmd.Flags().StringVar(&callee, "callee", "", "Callee agent ID (required)")
	cmd.Flags().StringVarP(&taskType, "task", "t", "", "Task type (required)")
	cmd.Flags().StringVarP(&userID, "user", "u", "", "User ID")
	cmd.MarkFlagRequired("caller")
	cmd.MarkFlagRequired("callee")
	cmd.MarkFlagRequired("task")

	return cmd
}

func a2aCardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "card <instance-id>",
		Short: "Get the Agent Card for a deployed A2A agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			card, err := c.GetAgentCard(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("get agent card: %w", err)
			}

			if output == "json" {
				printJSON(card)
				return nil
			}

			fmt.Printf("Agent Card: %s\n", card.Name)
			fmt.Printf("  URL:     %s\n", card.URL)
			fmt.Printf("  Version: %s\n", card.Version)
			fmt.Println("  Task Types:")
			for _, tt := range card.TaskTypes {
				fmt.Printf("    - %s: %s\n", tt.Type, tt.Description)
			}
			if len(card.AuthSchemes) > 0 {
				fmt.Println("  Auth Schemes:")
				for _, as := range card.AuthSchemes {
					fmt.Printf("    - %s\n", as.Scheme)
				}
			}
			return nil
		},
	}
}
