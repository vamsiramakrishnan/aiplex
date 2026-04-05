package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show dashboard summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := context.Background()

			stats, err := c.GetDashboardStats(ctx)
			if err != nil {
				return fmt.Errorf("get stats: %w", err)
			}

			if output == "json" {
				printJSON(stats)
				return nil
			}

			fmt.Println("AIPlex Dashboard")
			fmt.Println("════════════════════════════════════════")
			fmt.Printf("  Instances: %d total, %d running\n", stats.TotalInstances, stats.RunningInstances)
			fmt.Printf("  Agents:    %d registered\n", stats.RegisteredAgents)
			fmt.Printf("  Planes:    %d active\n", stats.ActivePlanes)
			fmt.Println()
			fmt.Println("Per Plane:")
			fmt.Printf("  MCPlex:    %d instances\n", stats.MCPlexInstances)
			fmt.Printf("  A2APlex:   %d instances\n", stats.A2APlexInstances)
			fmt.Printf("  LLMPlex:   %d instances\n", stats.LLMPlexInstances)
			fmt.Println()
			fmt.Println("Last 24h:")
			fmt.Printf("  LLM Cost:       $%.2f\n", stats.DailyCostUSD)
			fmt.Printf("  LLM Tokens:     %d\n", stats.DailyTokens)
			fmt.Printf("  LLM Requests:   %d\n", stats.DailyRequests)
			fmt.Printf("  A2A Delegations: %d\n", stats.A2ADelegations)
			fmt.Printf("  Policy Denials:  %d\n", stats.PolicyDenials)

			return nil
		},
	}
}
