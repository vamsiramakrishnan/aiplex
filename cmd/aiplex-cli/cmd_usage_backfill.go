package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// usageServerTools renders a record's server-tool request counts.
func usageServerTools(tools map[string]int64) string {
	if len(tools) == 0 {
		return "—"
	}
	kinds := make([]string, 0, len(tools))
	for kind := range tools {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%s=%d", kind, tools[kind]))
	}
	return strings.Join(parts, " ")
}

// backfillPass runs back-estimation page by page until the cursor ends and
// adds the pages up. Totals are summed exactly before rounding.
func backfillPass(cmd *cobra.Command, client *aiplex.Client, req aiplex.UsageBackfillRequest) (aiplex.UsageBackfillResult, error) {
	var total aiplex.UsageBackfillResult
	total.DryRun = req.DryRun
	var totals [][]usageledger.CostTotal
	for page := 0; ; page++ {
		res, err := client.BackfillUsageCosts(cmd.Context(), req)
		if err != nil {
			return total, err
		}
		total.Scanned += res.Scanned
		total.Eligible += res.Eligible
		total.Updated += res.Updated
		total.Skipped.UsageNotFinal += res.Skipped.UsageNotFinal
		total.Skipped.NoPrice += res.Skipped.NoPrice
		total.Skipped.Changed += res.Skipped.Changed
		totals = append(totals, res.Totals)
		if res.NextCursor == "" {
			break
		}
		req.Cursor = res.NextCursor
	}
	total.Totals = usageledger.SumCostTotals(totals...)
	return total, nil
}

func printBackfill(res aiplex.UsageBackfillResult) {
	verb := "updated"
	count := res.Updated
	if res.DryRun {
		verb, count = "would be updated", res.Eligible
	}
	fmt.Printf("Scanned %d records with unknown cost; %d %s.\n", res.Scanned, count, verb)
	if n := res.Skipped.NoPrice; n > 0 {
		fmt.Printf("  %d left unknown: no single price covers them at their own time, or a priced quantity is unknown.\n", n)
	}
	if n := res.Skipped.UsageNotFinal; n > 0 {
		fmt.Printf("  %d left unknown: usage is missing or not final (usage_bound).\n", n)
	}
	if n := res.Skipped.Changed; n > 0 {
		fmt.Printf("  %d skipped: another writer gave them a cost first.\n", n)
	}
	for _, t := range res.Totals {
		fmt.Printf("  %s %s\n", aiplex.FormatCostTotal(t), map[bool]string{true: "in total", false: "added to summaries"}[res.DryRun])
	}
	if count > 0 && !res.DryRun {
		fmt.Println("These costs have the basis estimated and a backfilled_at time; they were unknown when written. Summaries now include them.")
	}
}

func usagePricesBackfillCmd() *cobra.Command {
	var since, until, model, provider string
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "backfill [--since 7d] [--until 1h] [--model m] [--provider p] [--dry-run] [--yes]",
		Short: "Price records that were written before a price existed (admin)",
		Long: `Records written when no price covered them have an unknown cost. This computes it
now, for records whose cost is unknown and whose usage is final and fully known,
using the price effective at each record's own start time (a price created later
with an effective window covering the record applies; a price outside its window
never does).

The cost becomes "estimated" and carries backfilled_at (the record was unknown
when written) and the id of the price used. A settled or estimated cost is never
changed, nothing is overwritten, and running it twice changes nothing. The
original usage and every other field stay as they were. Records that no single
price can cost exactly stay unknown.

--dry-run reports counts and the total per currency without writing. Without it
the command shows the dry-run result first and asks before writing (or needs
--yes when there is no terminal). The work is done in bounded batches.`,
		Example: `  aiplex usage prices backfill --dry-run
  aiplex usage prices backfill --since 30d --model gemini-2.5-flash --yes
  aiplex usage prices backfill --provider anthropic --until 2026-11-01T00:00:00Z --dry-run -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now().UTC()
			req := aiplex.UsageBackfillRequest{ModelID: model, Provider: provider, DryRun: true}
			start, err := parseUsageTime(since, now)
			if err != nil {
				return usageError("--since: " + err.Error())
			}
			end, err := parseUsageTime(until, now)
			if err != nil {
				return usageError("--until: " + err.Error())
			}
			if !start.IsZero() {
				req.Since = &start
			}
			if !end.IsZero() {
				req.Until = &end
			}
			client := newClient()
			res, err := backfillPass(cmd, client, req)
			if err != nil {
				return fmt.Errorf("back-estimate costs: %w", err)
			}
			if dryRun || res.Eligible == 0 {
				if usageMachineOutput() {
					usagePrint(res)
				} else {
					printBackfill(res)
				}
				return nil
			}
			if !usageMachineOutput() {
				printBackfill(res)
			}
			if err := confirm(cmd, yes, fmt.Sprintf("Write the estimated cost of %d record(s)? Prices stay as they are; only unknown costs change.", res.Eligible)); err != nil {
				return err
			}
			req.DryRun = false
			done, err := backfillPass(cmd, client, req)
			if err != nil {
				return fmt.Errorf("back-estimate costs: %w", err)
			}
			if usageMachineOutput() {
				usagePrint(done)
			} else {
				fmt.Println()
				printBackfill(done)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&since, "since", "", "Start of window: duration like 24h, 7d, or an RFC3339 time")
	fl.StringVar(&until, "until", "", "End of window (exclusive): duration ago like 1h, or an RFC3339 time")
	fl.StringVar(&model, "model", "", "Only this model ID (model_id)")
	fl.StringVar(&provider, "provider", "", "Only this provider")
	fl.BoolVar(&dryRun, "dry-run", false, "Report counts and totals and write nothing")
	fl.BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
	return cmd
}
