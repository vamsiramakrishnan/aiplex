package main

import (
	"fmt"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
	"time"

	"github.com/spf13/cobra"
)

func llmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "llm",
		Short: "LLMPlex — manage model routes, providers, and costs",
	}

	cmd.AddCommand(llmRoutesCmd(), llmxProviderDescriptorsCmd(), llmxSurfacesCmd(), llmxCapacityCmd(), llmxBatchesCmd(), llmCostsCmd(), llmUsageCmd())
	return cmd
}

func llmRoutesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "routes",
		Short: "List LLM routing rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			routes, err := c.ListLLMRoutes(cmd.Context())
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

func llmCostsCmd() *cobra.Command {
	var period, modelID, agentID string

	cmd := &cobra.Command{
		Use:   "costs",
		Short: "Show LLM usage and cost summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			summary, err := c.GetUsageSummaryWithOpts(cmd.Context(), &aiplex.UsageSummaryOpts{Period: period, ModelID: modelID, AgentID: agentID})
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
	cmd.Flags().StringVar(&modelID, "model", "", "Filter by model ID")
	cmd.Flags().StringVar(&agentID, "agent", "", "Filter by agent ID")
	return cmd
}

func llmUsageCmd() *cobra.Command {
	var modelID, agentID, since string
	var limit int
	cmd := &cobra.Command{Use: "usage", Short: "List recorded model usage", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		opts := &aiplex.ListUsageRecordsOpts{ModelID: modelID, AgentID: agentID, Limit: limit}
		if since != "" {
			parsed, err := time.Parse(time.RFC3339Nano, since)
			if err != nil {
				return fmt.Errorf("--since must be RFC3339")
			}
			opts.Since = parsed
		}
		rows, err := newClient().ListUsageRecords(cmd.Context(), opts)
		if err != nil {
			return err
		}
		if output == "yaml" {
			printYAML(rows)
		} else {
			printJSON(rows)
		}
		return nil
	}}
	cmd.Flags().StringVar(&modelID, "model", "", "Filter by model ID")
	cmd.Flags().StringVar(&agentID, "agent", "", "Filter by agent ID")
	cmd.Flags().StringVar(&since, "since", "", "RFC3339 start timestamp")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum records (1–1000)")
	return cmd
}
