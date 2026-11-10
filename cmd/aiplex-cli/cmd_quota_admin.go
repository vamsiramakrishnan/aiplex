package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

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
	inventory := &cobra.Command{Use: "inventory <kind>", Args: cobra.ExactArgs(1), Short: "Read a bounded quota administration page (kinds: budgets, loans, attempts, reconciliations, audit, terminal-work)", RunE: func(cmd *cobra.Command, args []string) error {
		v, err := newClient().GetQuotaInventory(cmd.Context(), args[0], after, limit)
		if err == nil {
			quotaPrint(v)
		}
		return err
	}}
	inventory.Flags().StringVar(&after, "after", "", "Continue strictly after this record ID")
	inventory.Flags().IntVar(&limit, "limit", 20, "Page size, 1 to 64")
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
	return []*cobra.Command{policy, validate, preview, inventory, balance, loan, grant, ret, evidence, plan, inspect, apply}
}
