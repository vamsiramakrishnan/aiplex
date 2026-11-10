package main

import (
	"fmt"
	"os"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"

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

// llmPeriodWindow maps the --period names onto a ledger window.
func llmPeriodWindow(period string, now time.Time) (time.Time, error) {
	switch period {
	case "", "day":
		return now.Add(-24 * time.Hour), nil
	case "week":
		return now.Add(-7 * 24 * time.Hour), nil
	case "month":
		return now.Add(-30 * 24 * time.Hour), nil
	}
	return time.Time{}, usageError("--period must be day, week or month", "For another window use: aiplex usage summary --since 90d")
}

// llmCostsText renders the ledger totals of the model plane. Unknown is
// never shown as zero: a counter or cost no record reported is "unknown", and
// records that lacked a price or token usage are counted.
func llmCostsText(summary *aiplex.UsageLedgerSummary, label string) {
	var g usageledger.SummaryGroup
	for _, group := range summary.Groups {
		if g.Key == nil {
			g = group
		}
	}
	fmt.Printf("LLM Usage Summary (%s)\n", label)
	fmt.Println("════════════════════════════════════════")
	if len(summary.Groups) == 0 {
		fmt.Println("  No model usage records in this window.")
		fmt.Println("  Records: aiplex usage records --plane llm")
		return
	}
	// The total is only shown when every record reported both counters.
	total := usageUnknown
	in, haveIn := g.Tokens[usageledger.CounterInput]
	out, haveOut := g.Tokens[usageledger.CounterOutput]
	if haveIn && haveOut && g.TokensUnknown[usageledger.CounterInput] == 0 && g.TokensUnknown[usageledger.CounterOutput] == 0 {
		total = fmt.Sprint(in + out)
	} else if haveIn || haveOut {
		total = usageUnknown + " (not every record reported input and output tokens)"
	}
	fmt.Printf("  Total Cost:    %s\n", usageCostTotals(g))
	fmt.Printf("  Total Tokens:  %s\n", total)
	fmt.Printf("  Input Tokens:  %s\n", usageTokens(g, usageledger.CounterInput))
	fmt.Printf("  Output Tokens: %s\n", usageTokens(g, usageledger.CounterOutput))
	fmt.Printf("  Requests:      %d\n", g.Requests)
	if g.UnknownCostRecords > 0 {
		fmt.Printf("  NOTE: %d of %d records have an unknown cost (no price); the total excludes them.\n", g.UnknownCostRecords, g.Records)
	}
	if g.UnknownUsageRecords > 0 {
		fmt.Printf("  NOTE: %d of %d records reported no token usage; the token totals exclude them.\n", g.UnknownUsageRecords, g.Records)
	}
	if g.BoundedUsageRecords > 0 {
		fmt.Println("  NOTE: some totals include bounded usage (lower_bound, ambiguous or partial); those counters are not final.")
	}
	if summary.Truncated {
		fmt.Println("  WARNING: truncated - totals cover only the newest records scanned.")
	}
	fmt.Println("  Grouped detail: aiplex usage summary --plane llm --by model,agent")
}

func llmCostsCmd() *cobra.Command {
	var period, modelID, agentID string

	cmd := &cobra.Command{
		Use:   "costs",
		Short: "Show model usage and cost totals from the usage ledger",
		Long: `Totals of model requests, tokens and cost over a period, read from the usage
ledger (the same data as "aiplex usage summary --plane llm"). A cost or token
count the ledger does not have is shown as "unknown", never as zero, and the
output says how many records it excludes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			since, err := llmPeriodWindow(period, now)
			if err != nil {
				return err
			}
			filter := aiplex.UsageFilter{Plane: usageledger.PlaneLLM, ModelID: modelID, ActorID: agentID, Since: since}
			summary, err := newClient().SummarizeUsage(cmd.Context(), aiplex.UsageSummaryQuery{UsageFilter: filter})
			if err != nil {
				return fmt.Errorf("get costs: %w", err)
			}
			if usageMachineOutput() {
				usagePrint(summary)
				return nil
			}
			label := period
			if label == "" {
				label = "day"
			}
			llmCostsText(summary, "last "+label)
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
	cmd := &cobra.Command{Use: "usage", Short: "List recorded model usage from the usage ledger", Long: `Model requests from the usage ledger, newest first (the same data as
"aiplex usage records --plane llm"). Costs and token counts the ledger does not
have are shown as "unknown".`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		filter := aiplex.UsageFilter{Plane: usageledger.PlaneLLM, ModelID: modelID, ActorID: agentID}
		if since != "" {
			parsed, err := parseUsageTime(since, time.Now())
			if err != nil {
				return usageError("--since: " + err.Error())
			}
			filter.Since = parsed
		}
		page, err := newClient().QueryUsageRecords(cmd.Context(), aiplex.UsageRecordsQuery{UsageFilter: filter, Limit: limit})
		if err != nil {
			return fmt.Errorf("list model usage: %w", err)
		}
		records := page.Records
		if records == nil {
			records = []usageledger.Record{}
		}
		if usageMachineOutput() {
			usagePrint(records)
			return nil
		}
		if len(records) == 0 {
			fmt.Println("No model usage records match.")
			return nil
		}
		usageRecordTable(records).render(os.Stdout)
		if page.NextCursor != "" {
			fmt.Println("\nMore records available: aiplex usage records --plane llm --cursor " + page.NextCursor)
		}
		return nil
	}}
	cmd.Flags().StringVar(&modelID, "model", "", "Filter by model ID")
	cmd.Flags().StringVar(&agentID, "agent", "", "Filter by agent ID")
	cmd.Flags().StringVar(&since, "since", "", "Start of window: duration like 24h, 7d, or an RFC3339 time")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum records (1–1000)")
	return cmd
}
