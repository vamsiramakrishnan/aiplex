package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// runsCmd is the AIPlex ↔ Tape run timeline + operator action surface.
// Subcommands:
//
//	aiplex runs list      [--tenant] [--agent] [--has-unknown] [--has-obligations] [--limit]
//	aiplex runs inspect   <run_id>
//	aiplex runs events    <run_id> [--from-seq N] [--limit N]
//	aiplex runs effects   <run_id>
//	aiplex runs obligations <run_id>
//	aiplex runs budgets   <run_id>
//	aiplex runs health
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
		Short: "Inspect and operate agent runs",
		Long: `Browse, inspect, and operate on durable agent runs surfaced from
Tape's journal into AIPlex's audit projection. Operator actions
(redrive, reconcile, cancel, signal, compensate) require the
corresponding aiplex:runs:{action} scope.`,
	}
	cmd.AddCommand(
		runsListCmd(),
		runsInspectCmd(),
		runsEventsCmd(),
		runsEffectsCmd(),
		runsObligationsCmd(),
		runsBudgetsCmd(),
		runsHealthCmd(),
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
		tenant     string
		agent      string
		hasUnknown bool
		hasOblig   bool
		limit      int
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
					shorten(r.RunID, 16, truncEnd, false),
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
		Short: "Show the summary of one run",
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
	var fromSeq int64
	var limit int
	cmd := &cobra.Command{
		Use:   "events <run_id>",
		Short: "Show the ordered timeline for a run",
		Long: `Show a run's journal events in sequence order, one page at a time.
--from-seq is inclusive. When a page is full the command prints the exact
command for the next page.`,
		Example: `  aiplex runs events RUN_ID
  aiplex runs events RUN_ID --from-seq 1000 --limit 500`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromSeq < 0 {
				return fmt.Errorf("--from-seq must be 0 or more")
			}
			if limit < 1 {
				return fmt.Errorf("--limit must be 1 or more")
			}
			events, err := newClient().ListRunEvents(context.Background(), args[0],
				aiplex.ListRunEventsOpts{FromSeq: fromSeq, Limit: limit})
			if err != nil {
				return fmt.Errorf("list events: %w", err)
			}
			if events == nil {
				events = []aiplex.ExecutionEvent{}
			}
			if spStructured(cmd, events) {
				return nil
			}
			out := cmd.OutOrStdout()
			rows := make([][]string, 0, len(events))
			for _, e := range events {
				rows = append(rows, []string{
					fmt.Sprintf("%d", e.Seq),
					runsTime(e.Timestamp),
					orUnknown(e.Kind),
					dashIfEmpty(e.Tool),
					dashIfEmpty(e.Scope),
				})
			}
			spTable(out, []string{"SEQ", "TS", "KIND", "TOOL", "SCOPE"}, rows)
			fmt.Fprintf(out, "\n%d event(s)\n", len(events))
			if len(events) >= limit {
				next := events[len(events)-1].Seq + 1
				fmt.Fprintf(out, "\nMore: %s\n", runsEventsNextCommand(args[0], next, limit))
			}
			return nil
		},
	}
	cmd.Flags().Int64Var(&fromSeq, "from-seq", 0, "first sequence number to return (inclusive)")
	cmd.Flags().IntVar(&limit, "limit", 1000, "page size")
	return cmd
}

// runsEffectsCmd lists a run's effect events. The outcome comes from the
// event kind; an effect the backend reported as unknown stays "unknown".
func runsEffectsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "effects <run_id>",
		Example: "  aiplex runs effects RUN_ID\n  aiplex runs effects RUN_ID -o json",
		Short:   "Show each action a run took on a tool, and how it ended",
		Long: `Show the run's effect events: started, confirmed, failed, duplicate, or
unknown. An unknown outcome means the backend could not tell whether the
effect happened; it is neither success nor failure. Resolve it with
'aiplex runs reconcile'. The API reads at most the first 10000 events of
the run for this view.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			events, err := newClient().ListRunEffects(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("list effects: %w", err)
			}
			if events == nil {
				events = []aiplex.ExecutionEvent{}
			}
			if spStructured(cmd, events) {
				return nil
			}
			out := cmd.OutOrStdout()
			unknown := 0
			rows := make([][]string, 0, len(events))
			for _, e := range events {
				outcome := runsEffectOutcome(e.Kind)
				if outcome == spUnknown {
					unknown++
				}
				rows = append(rows, []string{fmt.Sprintf("%d", e.Seq), runsTime(e.Timestamp), outcome, dashIfEmpty(e.Tool), dashIfEmpty(e.Scope)})
			}
			spTable(out, []string{"SEQ", "TS", "OUTCOME", "TOOL", "SCOPE"}, rows)
			fmt.Fprintf(out, "\n%d effect(s)", len(events))
			if unknown > 0 {
				fmt.Fprintf(out, ", %d with unknown outcome (see '%s')", unknown, runsRunCommand("reconcile", args[0]))
			}
			fmt.Fprintln(out)
			return nil
		},
	}
}

func runsObligationsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "obligations <run_id>",
		Example: "  aiplex runs obligations RUN_ID",
		Short:   "Show the follow-up work a run owes, such as undoing an action",
		Long: `Show the run's obligation events: follow-up work the run owes, such as
an undo for an effect. Drain them with 'aiplex runs compensate'. The API
reads at most the first 10000 events of the run for this view.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			events, err := newClient().ListRunObligations(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("list obligations: %w", err)
			}
			if events == nil {
				events = []aiplex.ExecutionEvent{}
			}
			if spStructured(cmd, events) {
				return nil
			}
			out := cmd.OutOrStdout()
			rows := make([][]string, 0, len(events))
			for _, e := range events {
				rows = append(rows, []string{fmt.Sprintf("%d", e.Seq), runsTime(e.Timestamp), dashIfEmpty(e.Tool), dashIfEmpty(e.Scope), dashIfEmpty(e.PayloadJSON)})
			}
			spTable(out, []string{"SEQ", "TS", "TOOL", "SCOPE", "DETAIL"}, rows)
			fmt.Fprintf(out, "\n%d obligation(s)\n", len(events))
			return nil
		},
	}
}

func runsBudgetsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "budgets <run_id>",
		Example: "  aiplex runs budgets RUN_ID",
		Short:   "Show the budget charges recorded on a run",
		Long: `Show the run's budget charge events. A charge whose amount is missing
from the event is shown as unknown, and the total says how many it leaves
out. The API reads at most the first 10000 events of the run for this view.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			events, err := newClient().ListRunBudgets(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("list budgets: %w", err)
			}
			if events == nil {
				events = []aiplex.ExecutionEvent{}
			}
			if spStructured(cmd, events) {
				return nil
			}
			out := cmd.OutOrStdout()
			var total float64
			unknown := 0
			rows := make([][]string, 0, len(events))
			for _, e := range events {
				amount := spUnknown
				if usd, ok := runsChargedUSD(e.PayloadJSON); ok {
					total += usd
					amount = fmt.Sprintf("$%.2f", usd)
				} else {
					unknown++
				}
				rows = append(rows, []string{fmt.Sprintf("%d", e.Seq), runsTime(e.Timestamp), amount})
			}
			spTable(out, []string{"SEQ", "TS", "USD"}, rows)
			fmt.Fprintf(out, "\n%d charge(s)\n", len(events))
			switch {
			case len(events) == 0:
				fmt.Fprintln(out, "No budget charges recorded; no total.")
			case unknown == len(events):
				fmt.Fprintf(out, "Total charged: unknown (%d charge(s) with unknown amount)\n", unknown)
			case unknown > 0:
				fmt.Fprintf(out, "Total charged: $%.2f (excludes %d charge(s) with unknown amount)\n", total, unknown)
			default:
				fmt.Fprintf(out, "Total charged: $%.2f\n", total)
			}
			return nil
		},
	}
}

func runsHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "health",
		Example: "  aiplex runs health",
		Short:   "Check that run events are arriving, and when the last one came in",
		Long: `Show when the API last ingested a run event, whether any run is known,
and how many agent instances use the Tape runtime. Runs need the durable-run
backend (Tape); with none connected the last ingest reads "none recorded".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := newClient().GetRunsHealth(context.Background())
			if err != nil {
				return fmt.Errorf("runs health: %w", err)
			}
			if spStructured(cmd, h) {
				return nil
			}
			out := cmd.OutOrStdout()
			// An omitted value is unknown: the API could not read it, or it
			// describes the whole deployment and the caller is confined to
			// one tenant. It is never shown as zero.
			last, known, instances := "unknown", "unknown", "unknown"
			switch {
			case h.LastIngestAt == nil:
			case h.LastIngestAt.IsZero():
				last = "none recorded"
			default:
				last = h.LastIngestAt.UTC().Format(time.RFC3339)
			}
			if h.HasRuns != nil {
				known = map[bool]string{true: "yes", false: "no"}[*h.HasRuns]
			}
			if h.TapeInstancesCount != nil {
				instances = strconv.Itoa(*h.TapeInstancesCount)
			}
			fmt.Fprintf(out, "Last ingest:     %s\n", last)
			fmt.Fprintf(out, "Runs known:      %s\n", known)
			fmt.Fprintf(out, "Tape instances:  %s\n", instances)
			if !h.Now.IsZero() {
				fmt.Fprintf(out, "Server time:     %s\n", h.Now.UTC().Format(time.RFC3339))
			}
			return nil
		},
	}
}

// runsEffectOutcome names an effect event's outcome from its kind. A missing
// kind is shown as unknown and an unrecognised one as itself, never blank.
func runsEffectOutcome(kind string) string {
	switch kind {
	case "effect.begin":
		return "started"
	case "effect.confirmed":
		return "confirmed"
	case "effect.failed":
		return "failed"
	case "effect.duplicate":
		return "duplicate"
	case "effect.unknown", "":
		return spUnknown
	}
	return kind
}

// runsChargedUSD reads usd_charged from a budget event payload. ok is false
// when the payload is absent, malformed or carries no amount: the amount is
// unknown, not zero.
func runsChargedUSD(payload string) (float64, bool) {
	if payload == "" {
		return 0, false
	}
	var p struct {
		USDCharged *float64 `json:"usd_charged"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil || p.USDCharged == nil {
		return 0, false
	}
	return *p.USDCharged, true
}

func runsTime(t time.Time) string {
	if t.IsZero() {
		return spUnknown
	}
	return t.UTC().Format(time.RFC3339)
}

func orUnknown(s string) string {
	if s == "" {
		return spUnknown
	}
	return s
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

var runsPlainArg = regexp.MustCompile(`^[A-Za-z0-9._:/@=-]+$`)

// runsShellArg quotes a run id only when the shell would need it, so the
// "More:" hint can be pasted as written.
func runsShellArg(s string) string {
	if runsPlainArg.MatchString(s) {
		return s
	}
	return shellQuote(s)
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
		Short: "Re-drive a stuck or finished run: ask it to continue",
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
		Short: "Re-check the actions of a run whose outcome is unknown",
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
		Short: "Carry out the follow-up work a run still owes",
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
		Short: "Clear a settled run's stored payloads now, keeping its audit summary",
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

// runsRunCommand renders "aiplex runs <verb> <run_id>" so the shell hands the
// id to the command as a positional argument even when it starts with "-".
func runsRunCommand(verb, runID string) string {
	if strings.HasPrefix(runID, "-") {
		return "aiplex runs " + verb + " -- " + runsShellArg(runID)
	}
	return "aiplex runs " + verb + " " + runsShellArg(runID)
}

func runsEventsNextCommand(runID string, fromSeq int64, limit int) string {
	if strings.HasPrefix(runID, "-") {
		return fmt.Sprintf("aiplex runs events --from-seq %d --limit %d -- %s", fromSeq, limit, runsShellArg(runID))
	}
	return fmt.Sprintf("aiplex runs events %s --from-seq %d --limit %d", runsShellArg(runID), fromSeq, limit)
}
