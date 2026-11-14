package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

func quotaPrint(v any) {
	if output == "yaml" {
		printYAML(v)
	} else {
		printJSON(v)
	}
}
func quotaReadFile(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return fmt.Errorf("quota JSON file exceeds 1MiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("one quota JSON document required")
	}
	return nil
}

// quotaUnknownMaxPages bounds one `aiplex quota unknown` scan (64 records a
// page). A longer inventory is reported as truncated, never as complete.
const quotaUnknownMaxPages = 1024

// quotaUnknownScan pages through the attempts inventory with the older_than
// filter and merges the per-page summaries.
func quotaUnknownScan(cmd *cobra.Command, olderThan time.Duration) (quotaplex.UnknownExposure, bool, error) {
	client := newClient()
	total := quotaplex.UnknownExposure{Exposure: []quotaplex.HeldExposure{}}
	after := ""
	for page := 0; page < quotaUnknownMaxPages; page++ {
		v, err := client.GetQuotaUnknownAttempts(cmd.Context(), olderThan, after, 64)
		if err != nil {
			return total, false, err
		}
		total.Merge(*v.Unknown)
		if v.Done {
			return total, false, nil
		}
		if v.NextAfterID == "" || v.NextAfterID == after {
			return total, true, nil
		}
		after = v.NextAfterID
	}
	return total, true, nil
}

// quotaUnknownReport is the json/yaml form of `aiplex quota unknown`.
// OldestAgeSeconds is null when there is no unknown attempt or its age is
// unknown; Complete is false when any figure is a lower bound.
type quotaUnknownReport struct {
	OlderThan        string                    `json:"older_than"`
	Complete         bool                      `json:"complete"`
	Truncated        bool                      `json:"truncated"`
	OldestAgeSeconds *float64                  `json:"oldest_age_seconds"`
	Unknown          quotaplex.UnknownExposure `json:"unknown"`
}

func printQuotaUnknown(cmd *cobra.Command, olderThan time.Duration, u quotaplex.UnknownExposure, truncated bool) error {
	age, ageKnown := u.OldestAge()
	if output == "json" || output == "yaml" {
		report := quotaUnknownReport{OlderThan: olderThan.String(), Complete: u.Complete() && !truncated, Truncated: truncated, Unknown: u}
		if ageKnown {
			seconds := age.Seconds()
			report.OldestAgeSeconds = &seconds
		}
		// YAML goes through the JSON form, so both carry the documented keys.
		spStructured(cmd, report)
		return nil
	}
	out := cmd.OutOrStdout()
	count := fmt.Sprint(u.Attempts)
	if truncated || u.Unreadable > 0 {
		count = fmt.Sprintf("at least %d", u.Attempts)
	}
	fmt.Fprintf(out, "Unknown attempts dispatched at least %s ago: %s\n", olderThan, count)
	switch {
	case u.Attempts == 0 && u.Unreadable == 0 && !truncated:
		fmt.Fprintln(out, "Oldest:   none")
	case ageKnown:
		fmt.Fprintf(out, "Oldest:   %s (dispatched %s)\n", age.Round(time.Second), u.OldestDispatchAt.UTC().Format(time.RFC3339))
	default:
		fmt.Fprintln(out, "Oldest:   unknown")
	}
	if u.UndatedAttempts > 0 {
		fmt.Fprintf(out, "Undated:  %d attempt(s) without a valid dispatch time\n", u.UndatedAttempts)
	}
	if u.Unreadable > 0 {
		fmt.Fprintf(out, "Unreadable: %d attempt record(s) could not be read; figures are lower bounds\n", u.Unreadable)
	}
	if truncated {
		fmt.Fprintf(out, "Truncated: the scan stopped before the end of the inventory (at most %d pages); figures are lower bounds\n", quotaUnknownMaxPages)
	}
	if len(u.Exposure) > 0 {
		fmt.Fprintln(out, "Exposure held:")
		for _, e := range u.Exposure {
			fmt.Fprintln(out, "  "+e.String())
		}
	}
	return nil
}

func quotaAdminCommands() []*cobra.Command {
	policy := &cobra.Command{Use: "policy", Args: cobra.NoArgs, Short: "Inspect the active and configured quota policy", RunE: func(cmd *cobra.Command, _ []string) error {
		v, err := newClient().GetQuotaPolicy(cmd.Context())
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	var expectedDigest string
	validate := &cobra.Command{Use: "policy-validate <policy.json>", Args: cobra.ExactArgs(1), Short: "Validate a policy export without activating it", RunE: func(cmd *cobra.Command, args []string) error {
		var input quotaplex.Policy
		if err := quotaReadFile(args[0], &input); err != nil {
			return err
		}
		v, err := newClient().ValidateQuotaPolicy(cmd.Context(), input, expectedDigest)
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	validate.Flags().StringVar(&expectedDigest, "expected-digest", "", "Current active policy digest")
	preview := &cobra.Command{Use: "preview <demand.json>", Args: cobra.ExactArgs(1), Short: "Simulate admission without reserving or dispatching", RunE: func(cmd *cobra.Command, args []string) error {
		var input quotaplex.Demand
		if err := quotaReadFile(args[0], &input); err != nil {
			return err
		}
		v, err := newClient().PreviewQuota(cmd.Context(), input)
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	var after string
	var limit int
	var inventoryOlderThan time.Duration
	inventory := &cobra.Command{Use: "inventory <kind>", Args: cobra.ExactArgs(1), Short: "Read a bounded quota administration page (kinds: budgets, loans, attempts, reconciliations, audit, terminal-work)", RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("older-than") {
			if args[0] != "attempts" {
				return usageError("--older-than applies only to the attempts inventory")
			}
			if inventoryOlderThan < 0 {
				return usageError("--older-than must not be negative")
			}
			v, err := newClient().GetQuotaUnknownAttempts(cmd.Context(), inventoryOlderThan, after, limit)
			if err == nil && !spStructured(cmd, v) {
				// Quota commands print JSON by default.
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				err = enc.Encode(v)
			}
			return err
		}
		v, err := newClient().GetQuotaInventory(cmd.Context(), args[0], after, limit)
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	inventory.Flags().StringVar(&after, "after", "", "Continue strictly after this record ID")
	inventory.Flags().IntVar(&limit, "limit", 20, "Page size, 1 to 64")
	inventory.Flags().DurationVar(&inventoryOlderThan, "older-than", 0, "attempts only: list attempts in the unknown state dispatched at least this long ago (0s lists all of them)")
	var unknownOlderThan time.Duration
	unknown := &cobra.Command{Use: "unknown", Args: cobra.NoArgs, Short: "Summarize attempts in the unknown state: count, oldest age and the exposure they hold", RunE: func(cmd *cobra.Command, _ []string) error {
		if unknownOlderThan < 0 {
			return usageError("--older-than must not be negative")
		}
		summary, truncated, err := quotaUnknownScan(cmd, unknownOlderThan)
		if err != nil {
			return err
		}
		return printQuotaUnknown(cmd, unknownOlderThan, summary, truncated)
	}}
	unknown.Flags().DurationVar(&unknownOlderThan, "older-than", 0, "Only attempts dispatched at least this long ago, for example 1h (0s counts all of them)")
	balance := &cobra.Command{Use: "balance <key>", Args: cobra.ExactArgs(1), Short: "Read a tenant-authorized budget balance", RunE: func(cmd *cobra.Command, args []string) error {
		v, err := newClient().GetQuotaBalance(cmd.Context(), args[0])
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	loan := &cobra.Command{Use: "loan <id>", Args: cobra.ExactArgs(1), Short: "Inspect a quota allocation loan", RunE: func(cmd *cobra.Command, args []string) error {
		v, err := newClient().GetQuotaLoan(cmd.Context(), args[0])
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	var grantKey string
	grant := &cobra.Command{Use: "loan-grant <loan.json>", Args: cobra.ExactArgs(1), Short: "Grant a policy-authorized allocation loan", RunE: func(cmd *cobra.Command, args []string) error {
		var input quotaplex.LoanRequest
		if err := quotaReadFile(args[0], &input); err != nil {
			return err
		}
		v, err := newClient().GrantQuotaLoan(cmd.Context(), input, grantKey)
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	grant.Flags().StringVar(&grantKey, "idempotency-key", "", "Stable administration operation key")
	_ = grant.MarkFlagRequired("idempotency-key")
	var returnKey string
	ret := &cobra.Command{Use: "loan-return <id>", Args: cobra.ExactArgs(1), Short: "Return only currently unused borrowed allocation", RunE: func(cmd *cobra.Command, args []string) error {
		v, err := newClient().ReturnQuotaLoan(cmd.Context(), args[0], returnKey)
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	ret.Flags().StringVar(&returnKey, "idempotency-key", "", "Stable administration operation key")
	_ = ret.MarkFlagRequired("idempotency-key")
	evidence := &cobra.Command{Use: "evidence <event-id>", Args: cobra.ExactArgs(1), Short: "Read immutable native terminal facts and resolution state", RunE: func(cmd *cobra.Command, args []string) error {
		v, err := newClient().GetQuotaEvidence(cmd.Context(), args[0])
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	var planKey string
	plan := &cobra.Command{Use: "reconcile-plan <proposal.json>", Args: cobra.ExactArgs(1), Short: "Prepare an expiring terminal reconciliation for review", RunE: func(cmd *cobra.Command, args []string) error {
		var input quotaplex.ReconciliationRequest
		if err := quotaReadFile(args[0], &input); err != nil {
			return err
		}
		v, err := newClient().PlanQuotaReconciliation(cmd.Context(), input, planKey)
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	plan.Flags().StringVar(&planKey, "idempotency-key", "", "Stable administration operation key")
	_ = plan.MarkFlagRequired("idempotency-key")
	inspect := &cobra.Command{Use: "reconcile <plan-id>", Args: cobra.ExactArgs(1), Short: "Inspect a reconciliation plan and immutable receipt", RunE: func(cmd *cobra.Command, args []string) error {
		v, err := newClient().GetQuotaReconciliation(cmd.Context(), args[0])
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	var applyKey, digest string
	apply := &cobra.Command{Use: "reconcile-apply <plan-id>", Args: cobra.ExactArgs(1), Short: "Apply the exact reviewed terminal reconciliation", RunE: func(cmd *cobra.Command, args []string) error {
		v, err := newClient().ApplyQuotaReconciliation(cmd.Context(), args[0], digest, applyKey)
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	apply.Flags().StringVar(&applyKey, "idempotency-key", "", "Stable approval operation key")
	apply.Flags().StringVar(&digest, "digest", "", "Exact reviewed plan digest")
	_ = apply.MarkFlagRequired("idempotency-key")
	_ = apply.MarkFlagRequired("digest")
	return []*cobra.Command{policy, validate, preview, inventory, unknown, balance, loan, grant, ret, evidence, plan, inspect, apply}
}
