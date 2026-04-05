package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func llmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "llm",
		Short: "LLMPlex — manage model routes, providers, and costs",
	}

	cmd.AddCommand(llmRoutesCmd(), llmProvidersCmd(), llmCostsCmd())
	return cmd
}

func llmRoutesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "routes",
		Short: "List LLM routing rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			routes, err := c.ListLLMRoutes(context.Background())
			if err != nil {
				return fmt.Errorf("list routes: %w", err)
			}

			if output == "json" {
				printJSON(routes)
				return nil
			}

			for _, rc := range routes {
				fmt.Printf("Model: %s\n", rc.ModelID)
				for _, b := range rc.Backends {
					status := "●"
					if !b.Enabled {
						status = "○"
					}
					fmt.Printf("  %s %s/%s  weight=%d%%\n", status, b.Provider, b.ModelID, b.Weight)
				}
				if len(rc.Fallbacks) > 0 {
					fmt.Printf("  Fallback: %v\n", rc.Fallbacks)
				}
				if rc.Budget != nil && rc.Budget.MaxDailyCostUSD > 0 {
					fmt.Printf("  Budget: $%.2f/day\n", rc.Budget.MaxDailyCostUSD)
				}
				fmt.Println()
			}
			if len(routes) == 0 {
				fmt.Println("No routing rules configured.")
			}
			return nil
		},
	}
}

func llmProvidersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List LLM providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			providers, err := c.ListProviders(context.Background())
			if err != nil {
				return fmt.Errorf("list providers: %w", err)
			}

			if output == "json" {
				printJSON(providers)
				return nil
			}

			headers := []string{"PROVIDER", "NAME", "ENABLED", "REGION"}
			var rows [][]string
			for _, p := range providers {
				enabled := "yes"
				if !p.Enabled {
					enabled = "no"
				}
				rows = append(rows, []string{
					p.Provider, p.DisplayName, enabled, p.Region,
				})
			}
			printTable(headers, rows)
			return nil
		},
	}
}

func llmCostsCmd() *cobra.Command {
	var period string

	cmd := &cobra.Command{
		Use:   "costs",
		Short: "Show LLM usage and cost summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			summary, err := c.GetUsageSummary(context.Background(), period)
			if err != nil {
				return fmt.Errorf("get costs: %w", err)
			}

			if output == "json" {
				printJSON(summary)
				return nil
			}

			fmt.Printf("LLM Usage Summary (%s)\n", summary.Period)
			fmt.Println("════════════════════════════════════════")
			fmt.Printf("  Total Cost:    $%.2f\n", summary.TotalCostUSD)
			fmt.Printf("  Total Tokens:  %d\n", summary.TotalTokens)
			fmt.Printf("  Input Tokens:  %d\n", summary.InputTokens)
			fmt.Printf("  Output Tokens: %d\n", summary.OutputTokens)
			fmt.Printf("  Requests:      %d\n", summary.RequestCount)
			fmt.Printf("  Cache Hits:    %d\n", summary.CacheHits)
			fmt.Printf("  Avg Latency:   %.0fms\n", summary.AvgLatencyMs)
			return nil
		},
	}

	cmd.Flags().StringVarP(&period, "period", "p", "day", "Time period: day, week, month")
	return cmd
}
