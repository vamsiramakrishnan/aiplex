package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func whoCmd() *cobra.Command {
	var (
		kind   string
		since  string
		limit  int
		cursor string
	)
	cmd := &cobra.Command{
		Use:   "who [agent|user|client] [id]",
		Short: "See every caller: how it authenticates, what it can reach and why, and its usage",
		Long: `With a kind and an id, shows one caller in detail: how it authenticates, the
models, tools and skills it can reach with the reason for each, quota coverage,
and recent usage by model and tool.

With --kind (or just a kind), lists callers of that kind with their identity,
access points, last activity and usage over --since (default 24h).

Unknown is shown as "unknown", never as zero. There is no user directory:
known users are those named by an access point or seen in the usage ledger.`,
		Example: "  aiplex who --kind agent\n  aiplex who agent tutor-agent\n  aiplex who user principal-ab12… --since 7d\n  aiplex who --kind client -o json",
		Args:    cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			switch len(args) {
			case 2:
				detail, err := c.GetOverviewSubject(cmd.Context(), args[0], args[1], since)
				if err != nil {
					return fmt.Errorf("who: %w", err)
				}
				if spStructured(cmd, detail) {
					return nil
				}
				spRenderSubject(cmd.OutOrStdout(), detail)
				return nil
			case 1:
				if kind != "" && kind != args[0] {
					return fmt.Errorf("conflicting kinds %q and --kind %q", args[0], kind)
				}
				kind = args[0]
			}
			if kind == "" {
				return fmt.Errorf("give a kind and an id (aiplex who agent <id>) or --kind agent|user|client to list")
			}
			page, err := c.ListOverviewSubjects(cmd.Context(), aiplex.OverviewSubjectsQuery{Kind: kind, Since: since, Limit: limit, Cursor: cursor})
			if err != nil {
				return fmt.Errorf("who: %w", err)
			}
			if spStructured(cmd, page) {
				return nil
			}
			spRenderSubjects(cmd.OutOrStdout(), page)
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "List callers of this kind: agent, user or client")
	cmd.Flags().StringVar(&since, "since", "", "Usage window: a duration like 24h or 7d, or an RFC3339 time (default 24h)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Page size when listing (default 50, max 200)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Continue a listing from a previous next-cursor")
	return cmd
}

func spLastSeen(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.UTC().Format("2006-01-02 15:04Z")
}

func spRenderSubjects(out io.Writer, p *aiplex.OverviewSubjectPage) {
	if len(p.Subjects) == 0 {
		fmt.Fprintf(out, "No %s callers known since %s.\n", p.Kind, p.Since.UTC().Format(time.RFC3339))
	} else {
		rows := make([][]string, 0, len(p.Subjects))
		for _, s := range p.Subjects {
			name := s.DisplayName
			if name == "" {
				name = "-"
			}
			requests := spUnknown
			if s.Usage != nil {
				requests = strconv.FormatInt(s.Usage.Requests, 10)
			}
			rows = append(rows, []string{s.ID, name, s.Identity.Source, spJoin(s.AccessPoints), spLastSeen(s.LastSeen), requests, spTokensCell(s.Usage, "input_tokens"), spTokensCell(s.Usage, "output_tokens"), spCostCell(s.Usage)})
		}
		spTable(out, []string{strings.ToUpper(p.Kind), "NAME", "AUTH", "ACCESS POINTS", "LAST SEEN", "REQUESTS", "IN", "OUT", "COST"}, rows)
	}
	if !p.UsageAvailable {
		fmt.Fprintln(out, "\nUsage is unavailable: the usage ledger is not configured.")
	}
	for _, l := range p.Limitations {
		fmt.Fprintf(out, "\nNote: %s", l)
	}
	if len(p.Limitations) > 0 {
		fmt.Fprintln(out)
	}
	if p.NextCursor != "" {
		fmt.Fprintf(out, "\nMore: aiplex who --kind %s --cursor %s\n", p.Kind, p.NextCursor)
	}
}

func spReasons(reasons []aiplex.OverviewReason) string {
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		label := r.Type
		if r.Ref != "" {
			label += ":" + r.Ref
		}
		if r.Conditional {
			label += " (conditional)"
		}
		parts = append(parts, label)
	}
	return spJoin(parts)
}

func spAccessRows(items []aiplex.OverviewAccessItem) [][]string {
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		rows = append(rows, []string{it.Name, it.AccessPointID, it.State, spReasons(it.Reasons)})
	}
	return rows
}

func spRenderSubject(out io.Writer, d *aiplex.OverviewSubjectDetail) {
	fmt.Fprintf(out, "%s %s", strings.ToUpper(d.Kind[:1])+d.Kind[1:], d.ID)
	if d.DisplayName != "" {
		fmt.Fprintf(out, " (%s)", d.DisplayName)
	}
	fmt.Fprintf(out, "\n  Authenticates: %s - %s\n  Last seen:     %s\n  Known from:    %s\n", d.Identity.Source, d.Identity.Detail, spLastSeen(d.LastSeen), spJoin(d.KnownFrom))
	for _, b := range d.Bindings {
		fmt.Fprintf(out, "  Binding:       %s preset=%s issuer=%s %s=%s\n", b.ID, b.Preset, b.Issuer, b.Claim, b.Subject)
	}
	if len(d.Providers) > 0 {
		ids := make([]string, 0, len(d.Providers))
		for _, p := range d.Providers {
			ids = append(ids, p.ID)
		}
		fmt.Fprintf(out, "  Identity providers in this tenant: %s\n", strings.Join(ids, ", "))
	}
	fmt.Fprintln(out, "\nAccess points")
	if len(d.AccessPoints) == 0 {
		fmt.Fprintln(out, "  none admit this caller")
	} else {
		rows := make([][]string, 0, len(d.AccessPoints))
		for _, a := range d.AccessPoints {
			rows = append(rows, []string{a.ID, a.Admits, a.Reason})
		}
		spTable(out, []string{"ID", "ADMITS", "WHY"}, rows)
	}
	headers := []string{"NAME", "ACCESS POINT", "STATE", "WHY"}
	for _, section := range []struct {
		title string
		items []aiplex.OverviewAccessItem
	}{{"Models", d.Access.Models}, {"Tools", d.Access.Tools}, {"Skills", d.Access.Skills}} {
		fmt.Fprintf(out, "\n%s\n", section.title)
		if len(section.items) == 0 {
			fmt.Fprintln(out, "  none")
			continue
		}
		spTable(out, headers, spAccessRows(section.items))
	}
	if d.Access.ScopesNote != "" {
		fmt.Fprintf(out, "\n  %s\n", d.Access.ScopesNote)
	}
	if d.Access.Truncated {
		fmt.Fprintln(out, "  (access list truncated)")
	}
	fmt.Fprintln(out, "\nQuota")
	if len(d.Quota.AccessPoints) == 0 {
		fmt.Fprintln(out, "  no admitting access point")
	}
	for _, q := range d.Quota.AccessPoints {
		class := ""
		if q.QoSClass != "" {
			class = " class " + q.QoSClass
		}
		fmt.Fprintf(out, "  %s%s: %s - %s\n", q.AccessPointID, class, q.Coverage.State, q.Coverage.Detail)
		for _, b := range q.Allocations.Budgets {
			fmt.Fprintf(out, "    budget %s meter=%s limit=%d\n", b.ID, b.MeterID, b.Limit)
		}
		for _, r := range q.Allocations.Rates {
			fmt.Fprintf(out, "    rate %s meter=%s %d per %s\n", r.ID, r.MeterID, r.Rate, r.Interval)
		}
		for _, r := range q.Allocations.Concurrency {
			fmt.Fprintf(out, "    concurrency %s limit=%d\n", r.ID, r.Limit)
		}
	}
	if !d.Quota.Balance.Available {
		fmt.Fprintf(out, "  Remaining balance: unavailable - %s\n", d.Quota.Balance.Reason)
	}
	fmt.Fprintf(out, "\nUsage since %s\n", d.Since.UTC().Format(time.RFC3339))
	if d.Usage == nil {
		fmt.Fprintln(out, "  unknown (usage ledger not configured)")
	} else {
		fmt.Fprintf(out, "  Requests %d   In %s   Out %s   Cost %s\n", d.Usage.Requests, spTokensCell(d.Usage, "input_tokens"), spTokensCell(d.Usage, "output_tokens"), spCostCell(d.Usage))
	}
	for _, g := range []struct {
		title, key string
		groups     []aiplex.OverviewUsageGroup
	}{{"By model", "model_id", d.ByModel}, {"By tool", "tool_name", d.ByTool}} {
		if len(g.groups) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s\n", g.title)
		rows := make([][]string, 0, len(g.groups))
		for _, grp := range g.groups {
			rows = append(rows, []string{grp.Key[g.key], strconv.FormatInt(grp.Usage.Requests, 10), spTokensCell(grp.Usage, "input_tokens"), spTokensCell(grp.Usage, "output_tokens"), spCostCell(grp.Usage), spLastSeen(grp.LastSeen)})
		}
		spTable(out, []string{"NAME", "REQUESTS", "IN", "OUT", "COST", "LAST SEEN"}, rows)
	}
	for _, n := range d.Notes {
		fmt.Fprintf(out, "\nNote: %s\n", n)
	}
}
