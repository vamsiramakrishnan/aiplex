package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// runsCmd is the AIPlex ↔ Tape run timeline + operator action surface
// (PR 11 item 12). Subcommands:
//
//	aiplex runs list      [--tenant] [--agent] [--has-unknown] [--has-obligations] [--limit]
//	aiplex runs inspect   <run_id>
//	aiplex runs events    <run_id>
//	aiplex runs audit     <run_id>
//	aiplex runs redrive   <run_id>
//	aiplex runs reconcile <run_id>
//	aiplex runs cancel    <run_id> --reason "..."
//	aiplex runs signal    <run_id> --gate "..." --resolution "..."
//	aiplex runs compensate <run_id>
//	aiplex runs compact   <run_id>
func runsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Inspect + operate on Tape-backed runs",
		Long: `Browse, inspect, and operate on durable agent runs surfaced from
Tape's journal into AIPlex's audit projection. Operator actions
(redrive, reconcile, cancel, signal, compensate) require the
corresponding aiplex:runs:{action} scope.`,
	}
	cmd.AddCommand(
		runsListCmd(),
		runsInspectCmd(),
		runsEventsCmd(),
		runsAuditCmd(),
		runsRedriveCmd(),
		runsReconcileCmd(),
		runsCancelCmd(),
		runsSignalCmd(),
		runsCompensateCmd(),
		runsCompactCmd(),
	)
	return cmd
}

func runsListCmd() *cobra.Command {
	var (
		tenant       string
		agent        string
		hasUnknown   bool
		hasOblig     bool
		limit        int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the most recent runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			runs, err := c.ListRuns(context.Background(), aiplex.ListRunsOpts{
				TenantID:          tenant,
				AgentID:           agent,
				HasUnknownEffects: hasUnknown,
				HasObligations:    hasOblig,
				Limit:             limit,
			})
			if err != nil {
				return fmt.Errorf("list runs: %w", err)
			}
			if output == "json" {
				printJSON(runs)
				return nil
			}
			headers := []string{"RUN_ID", "STATUS", "TENANT", "AGENT", "DECISIONS", "EFFECTS", "UNKNOWN", "OBLIG"}
			rows := make([][]string, 0, len(runs))
			for _, r := range runs {
				rows = append(rows, []string{
					truncate(r.RunID, 16),
					r.Status,
					r.TenantID,
					r.AgentID,
					fmt.Sprintf("%d", r.DecisionsCount),
					fmt.Sprintf("%d", r.EffectsCount),
					fmt.Sprintf("%d", r.UnknownEffects),
					fmt.Sprintf("%d", r.Obligations),
				})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d run(s)\n", len(runs))
			return nil
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "filter by tenant_id")
	cmd.Flags().StringVar(&agent, "agent", "", "filter by agent_id")
	cmd.Flags().BoolVar(&hasUnknown, "has-unknown", false, "only runs with UNKNOWN effects")
	cmd.Flags().BoolVar(&hasOblig, "has-obligations", false, "only runs with obligations")
	cmd.Flags().IntVar(&limit, "limit", 50, "result cap")
	return cmd
}

func runsInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <run_id>",
		Short: "Show the projected summary for one run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := newClient().GetRun(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("inspect run: %w", err)
			}
			if output == "json" {
				printJSON(run)
				return nil
			}
			fmt.Printf("Run %s\n", run.RunID)
			fmt.Printf("  Status:           %s\n", run.Status)
			fmt.Printf("  Tenant / Agent:   %s / %s\n", run.TenantID, run.AgentID)
			fmt.Printf("  Actor / Subject:  %s / %s\n", run.Actor, run.Subject)
			fmt.Printf("  Plane:            %s\n", run.Plane)
			fmt.Printf("  Started:          %s\n", run.StartedAt.Format(time.RFC3339))
			if run.EndedAt != nil {
				fmt.Printf("  Ended:            %s\n", run.EndedAt.Format(time.RFC3339))
			}
			fmt.Printf("\n  Decisions:        %d\n", run.DecisionsCount)
			fmt.Printf("  Effects:          %d  (UNKNOWN: %d)\n", run.EffectsCount, run.UnknownEffects)
			fmt.Printf("  Obligations:      %d\n", run.Obligations)
			fmt.Printf("  Policy denials:   %d\n", run.PolicyViolations)
			if run.BudgetUSDCharged > 0 {
				fmt.Printf("  Budget spent:     $%.2f\n", run.BudgetUSDCharged)
			}
			return nil
		},
	}
}

func runsEventsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "events <run_id>",
		Short: "Show the ordered timeline for a run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			events, err := newClient().ListRunEvents(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("list events: %w", err)
			}
			if output == "json" {
				printJSON(events)
				return nil
			}
			headers := []string{"SEQ", "TS", "KIND", "TOOL", "SCOPE"}
			rows := make([][]string, 0, len(events))
			for _, e := range events {
				rows = append(rows, []string{
					fmt.Sprintf("%d", e.Seq),
					e.Timestamp.Format("15:04:05"),
					e.Kind,
					e.Tool,
					e.Scope,
				})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d event(s)\n", len(events))
			return nil
		},
	}
}

func runsAuditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "audit <run_id>",
		Short: "Show the operator-action audit trail for a run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rows, err := newClient().ListRunOperatorAudit(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("audit: %w", err)
			}
			if output == "json" {
				printJSON(rows)
				return nil
			}
			headers := []string{"AT", "ACTION", "ACTOR", "STATUS", "DETAIL"}
			tableRows := make([][]string, 0, len(rows))
			for _, r := range rows {
				detail := r.Reason
				if r.GateName != "" {
					detail = "gate=" + r.GateName
				}
				if r.Error != "" {
					detail = "error: " + r.Error
				}
				tableRows = append(tableRows, []string{
					r.At.Format("15:04:05"),
					r.Action,
					r.Actor,
					r.Status,
					detail,
				})
			}
			printTable(headers, tableRows)
			fmt.Printf("\n%d audit row(s)\n", len(rows))
			return nil
		},
	}
}

func runsRedriveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "redrive <run_id>",
		Short: "Re-drive a stuck or terminal run via the Tape admin RPC",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().RedriveRun(context.Background(), args[0]); err != nil {
				return fmt.Errorf("redrive: %w", err)
			}
			fmt.Printf("✓ redrive accepted for %s\n", args[0])
			return nil
		},
	}
}

func runsReconcileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile <run_id>",
		Short: "Kick the reconciler to resolve UNKNOWN effects on this run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().ReconcileRun(context.Background(), args[0]); err != nil {
				return fmt.Errorf("reconcile: %w", err)
			}
			fmt.Printf("✓ reconcile accepted for %s\n", args[0])
			return nil
		},
	}
}

func runsCancelCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "cancel <run_id>",
		Short: "Cancel a run (cooperative — agent checks at next boundary)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().CancelRun(context.Background(), args[0], reason); err != nil {
				return fmt.Errorf("cancel: %w", err)
			}
			fmt.Printf("✓ cancel accepted for %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "human-readable cancellation reason")
	return cmd
}

func runsSignalCmd() *cobra.Command {
	var gateName, resolution string
	cmd := &cobra.Command{
		Use:   "signal <run_id>",
		Short: "Send a signal to a parked run (releases the named gate)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if gateName == "" {
				return fmt.Errorf("--gate-name is required")
			}
			if err := newClient().SignalRun(context.Background(), args[0], gateName, resolution); err != nil {
				return fmt.Errorf("signal: %w", err)
			}
			fmt.Printf("✓ signal accepted for %s on gate %s\n", args[0], gateName)
			return nil
		},
	}
	cmd.Flags().StringVar(&gateName, "gate-name", "", "name of the gate to signal (required)")
	cmd.Flags().StringVar(&resolution, "resolution", "", "JSON-encoded resolution payload")
	return cmd
}

func runsCompensateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compensate <run_id>",
		Short: "Kick the compensator to drain pending obligations on this run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().CompensateRun(context.Background(), args[0]); err != nil {
				return fmt.Errorf("compensate: %w", err)
			}
			fmt.Printf("✓ compensate accepted for %s\n", args[0])
			return nil
		},
	}
}

func runsCompactCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compact <run_id>",
		Short: "Compact this run now — zero bulky payloads, keep the audit envelope",
		Long: `compact triggers Tape's CompactRun out of band. The retention reactor
handles the scheduled, policy-driven path; this command is the manual
override for ops who want to reclaim payload bytes before the window
expires. Tape will refuse if the run isn't yet settled (open obligations
or UNKNOWN effects).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().CompactRun(context.Background(), args[0]); err != nil {
				return fmt.Errorf("compact: %w", err)
			}
			fmt.Printf("✓ compact accepted for %s\n", args[0])
			return nil
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
