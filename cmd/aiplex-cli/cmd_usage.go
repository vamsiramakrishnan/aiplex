package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// usageAllRecordsCap bounds `usage records --all` so a runaway ledger cannot
// exhaust memory; the command warns when it stops early.
const usageAllRecordsCap = 100000

// usageUnknown is the explicit marker for usage or cost the platform could
// not establish. It is never rendered as zero.
const usageUnknown = "unknown"

type usageFilterFlags struct {
	plane, provider, model, tool, agent, user, client, status, source string
	accessPoint, callerKind                                           string
	since, until                                                      string
}

func (f *usageFilterFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.plane, "plane", "", "Plane: llmplex, mcplex or a2aplex")
	fl.StringVar(&f.provider, "provider", "", "Provider")
	fl.StringVar(&f.model, "model", "", "Model ID (model_id)")
	fl.StringVar(&f.tool, "tool", "", "Tool name (tool_name)")
	fl.StringVar(&f.agent, "agent", "", "Agent / actor ID (actor_id)")
	fl.StringVar(&f.user, "user", "", "User / principal ID (principal_id)")
	fl.StringVar(&f.client, "client", "", "Client ID (client_id)")
	fl.StringVar(&f.accessPoint, "access-point", "", "Access point ID (access_point_id)")
	fl.StringVar(&f.callerKind, "caller-kind", "", "Caller kind: agent, user or client (caller_kind)")
	fl.StringVar(&f.status, "status", "", "Status: ok, error, denied or unknown")
	fl.StringVar(&f.source, "source", "", "Source: managed or reported")
	fl.StringVar(&f.since, "since", "", "Start of window: duration like 24h, 7d, or an RFC3339 time")
	fl.StringVar(&f.until, "until", "", "End of window (exclusive): duration ago like 1h, or an RFC3339 time")
}

func (f *usageFilterFlags) filter(now time.Time) (aiplex.UsageFilter, error) {
	out := aiplex.UsageFilter{Plane: f.plane, Provider: f.provider, ModelID: f.model, ToolName: f.tool, ActorID: f.agent, PrincipalID: f.user, ClientID: f.client, AccessPointID: f.accessPoint, CallerKind: f.callerKind, Status: f.status, Source: f.source}
	var err error
	if out.Since, err = parseUsageTime(f.since, now); err != nil {
		return out, fmt.Errorf("--since: %w", err)
	}
	if out.Until, err = parseUsageTime(f.until, now); err != nil {
		return out, fmt.Errorf("--until: %w", err)
	}
	return out, nil
}

var usageDurationPattern = regexp.MustCompile(`^(\d+)([smhdw])$`)

// parseUsageTime accepts "" (unset), a relative duration meaning that long
// before now (30m, 24h, 7d, 2w, or any Go duration), or an RFC3339 timestamp.
func parseUsageTime(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if m := usageDurationPattern.FindStringSubmatch(value); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 32)
		if err != nil || n == 0 {
			return time.Time{}, fmt.Errorf("duration %q must be positive and fit in range", value)
		}
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return now.Add(-time.Duration(n) * unit).UTC(), nil
	}
	if d, err := time.ParseDuration(value); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("duration %q must be positive", value)
		}
		return now.Add(-d).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is neither a duration (24h, 7d) nor an RFC3339 timestamp", value)
	}
	return t.UTC(), nil
}

func usageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Usage ledger — what each request cost in requests, tokens and dollars",
		Long: `Query the usage ledger: one record per managed request on any plane, by any
client or agent. Unknown usage or cost is shown as "unknown", never as zero,
and cost always carries its basis (settled or estimated). Non-administrators
see only their own records.`,
	}
	cmd.AddCommand(usageRecordsCmd(), usageGetCmd(), usageSummaryCmd(), usagePricesCmd())
	return cmd
}

func usagePrint(v any) {
	if output == "yaml" {
		printYAML(v)
	} else {
		printJSON(v)
	}
}

func usageMachineOutput() bool { return output == "json" || output == "yaml" }

func usageRecordsCmd() *cobra.Command {
	var flags usageFilterFlags
	var limit int
	var cursor string
	var all bool
	cmd := &cobra.Command{
		Use:   "records",
		Short: "List usage records, newest first",
		Example: `  aiplex usage records --since 24h --model gemini-2.5-flash
  aiplex usage records --agent tutor-agent --status error --limit 50
  aiplex usage records --access-point ide-team --caller-kind agent --since 24h
  aiplex usage records --since 7d --all -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := flags.filter(time.Now())
			if err != nil {
				return err
			}
			if all && cursor != "" {
				return fmt.Errorf("--all cannot be combined with --cursor")
			}
			query := aiplex.UsageRecordsQuery{UsageFilter: filter, Limit: limit, Cursor: cursor}
			client := newClient()
			var records []usageledger.Record
			next := ""
			capped := false
			if all {
				if query.Limit == 0 {
					query.Limit = aiplex.UsageMaxPageSize
				}
				records, capped, err = client.CollectUsageRecords(cmd.Context(), query, usageAllRecordsCap)
				if err != nil {
					return fmt.Errorf("list usage records: %w", err)
				}
			} else {
				page, err := client.QueryUsageRecords(cmd.Context(), query)
				if err != nil {
					return fmt.Errorf("list usage records: %w", err)
				}
				records, next = page.Records, page.NextCursor
			}
			if records == nil {
				records = []usageledger.Record{}
			}
			if usageMachineOutput() {
				if all {
					usagePrint(map[string]any{"records": records, "truncated": capped})
				} else {
					usagePrint(usageledger.Page{Records: records, NextCursor: next})
				}
			} else {
				if len(records) == 0 {
					fmt.Println("No usage records match.")
				} else {
					headers, rows := usageRecordRows(records)
					printTable(headers, rows)
				}
				if next != "" {
					fmt.Printf("\nMore records available: --cursor %s\n", next)
				}
			}
			if capped {
				fmt.Fprintf(os.Stderr, "WARNING: stopped after %d records; narrow the window with --since/--until\n", usageAllRecordsCap)
			}
			return nil
		},
	}
	flags.bind(cmd)
	cmd.Flags().IntVar(&limit, "limit", 0, "Page size, 1 to 1000 (default 100)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor value")
	cmd.Flags().BoolVar(&all, "all", false, "Follow every page")
	return cmd
}

func usageGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Short:   "Show every field of one usage record",
		Example: "  aiplex usage get ul_0123456789abcdef",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			record, err := newClient().GetUsageLedgerRecord(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get usage record: %w", err)
			}
			if usageMachineOutput() {
				usagePrint(record)
				return nil
			}
			for _, row := range usageRecordDetail(*record) {
				fmt.Printf("%-20s %s\n", row[0]+":", row[1])
			}
			return nil
		},
	}
}

func usageSummaryCmd() *cobra.Command {
	var flags usageFilterFlags
	var by []string
	var bucket string
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Totals of requests, tokens and cost, optionally grouped",
		Long: `Aggregate usage. --by takes plane, provider, model, tool, agent, user, client,
access-point, caller-kind or status (aliases: model=model_id, agent=actor_id,
user=principal_id, client=client_id, tool=tool_name, access-point=access_point_id,
caller-kind=caller_kind). Records whose usage is bounded (lower_bound, ambiguous
or partial) are included in the sums and counted in the BOUNDED column; their
totals are not final. Costs are never summed across currencies
or bases. When the scan limit is reached the totals cover only the newest
records and the command says so.`,
		Example: `  aiplex usage summary --by model,agent --since 7d
  aiplex usage summary --by access-point,caller-kind --since 7d
  aiplex usage summary --by model --bucket day --since 30d -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := flags.filter(time.Now())
			if err != nil {
				return err
			}
			summary, err := newClient().SummarizeUsage(cmd.Context(), aiplex.UsageSummaryQuery{UsageFilter: filter, GroupBy: by, Bucket: bucket})
			if err != nil {
				return fmt.Errorf("usage summary: %w", err)
			}
			if usageMachineOutput() {
				usagePrint(summary)
				return nil
			}
			if len(summary.Groups) == 0 {
				fmt.Println("No usage records match.")
			} else {
				headers, rows := usageSummaryRows(summary)
				printTable(headers, rows)
			}
			fmt.Printf("\n%d records scanned.\n", summary.RecordsScanned)
			for _, g := range summary.Groups {
				if g.BoundedUsageRecords > 0 {
					fmt.Println("NOTE: some totals include bounded usage (lower_bound, ambiguous or partial); those counters are not final. See the BOUNDED column.")
					break
				}
			}
			if summary.Truncated {
				fmt.Println("WARNING: truncated - totals cover only the newest records scanned. Narrow the window with --since/--until.")
			}
			return nil
		},
	}
	flags.bind(cmd)
	cmd.Flags().StringSliceVar(&by, "by", nil, "Group by (comma separated): plane,provider,model,tool,agent,user,client,status")
	cmd.Flags().StringVar(&bucket, "bucket", "", "Time bucket: hour or day")
	return cmd
}

// ---- rendering ----------------------------------------------------------

func usageDash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

func usageCounter(u *usageledger.Usage, name string) string {
	v := u.Counter(name)
	if v == nil {
		return usageUnknown
	}
	return strconv.FormatInt(*v, 10)
}

// usageRecordCounter renders a record's counter. A bounded record's counters
// are never shown bare: the bound travels with the number.
func usageRecordCounter(r usageledger.Record, name string) string {
	v := usageCounter(r.Usage, name)
	if r.UsageBound != "" && v != usageUnknown {
		return v + " (" + r.UsageBound + ")"
	}
	return v
}

func usageSessionCell(r usageledger.Record) string {
	if r.Session {
		return "session"
	}
	return "—"
}

func usageCost(c usageledger.Cost) string {
	return aiplex.FormatCost(c)
}

func usageTarget(r usageledger.Record) string {
	switch {
	case r.ModelID != "":
		return r.ModelID
	case r.ToolName != "":
		return r.ToolName
	case r.ResourceID != "":
		return r.ResourceID
	}
	return "—"
}

func usageRecordRows(records []usageledger.Record) ([]string, [][]string) {
	headers := []string{"ID", "TIME", "PLANE", "MODEL/TOOL", "AGENT", "USER", "CLIENT", "SESSION", "STATUS", "IN", "OUT", "COST", "LATENCY"}
	rows := make([][]string, 0, len(records))
	for _, r := range records {
		rows = append(rows, []string{
			r.ID, r.StartedAt.UTC().Format("2006-01-02T15:04:05Z"), r.Plane, usageTarget(r), usageDash(r.ActorID), usageDash(r.PrincipalID), usageDash(r.ClientID), usageSessionCell(r), r.Status,
			usageRecordCounter(r, usageledger.CounterInput), usageRecordCounter(r, usageledger.CounterOutput), usageCost(r.Cost), fmt.Sprintf("%dms", r.LatencyMS),
		})
	}
	return headers, rows
}

func usageRecordDetail(r usageledger.Record) [][2]string {
	httpStatus := "—"
	if r.HTTPStatus != 0 {
		httpStatus = strconv.Itoa(r.HTTPStatus)
	}
	rows := [][2]string{
		{"id", r.ID}, {"started_at", r.StartedAt.UTC().Format(time.RFC3339Nano)}, {"finished_at", r.FinishedAt.UTC().Format(time.RFC3339Nano)},
		{"latency", fmt.Sprintf("%dms", r.LatencyMS)}, {"plane", r.Plane}, {"provider", usageDash(r.Provider)}, {"resource_id", usageDash(r.ResourceID)},
		{"operation", usageDash(r.Operation)}, {"model_id", usageDash(r.ModelID)}, {"model_version", usageDash(r.ModelVersion)}, {"tool_name", usageDash(r.ToolName)},
		{"principal_id", usageDash(r.PrincipalID)}, {"actor_id", usageDash(r.ActorID)}, {"client_id", usageDash(r.ClientID)}, {"request_id", usageDash(r.RequestID)},
		{"access_point_id", usageDash(r.AccessPointID)}, {"caller_kind", usageDash(r.CallerKind)},
		{"trace_id", usageDash(r.TraceID)}, {"quota_attempt_id", usageDash(r.QuotaAttemptID)}, {"streaming", strconv.FormatBool(r.Streaming)}, {"session", strconv.FormatBool(r.Session)},
		{"usage_bound", usageDash(r.UsageBound)},
		{"status", r.Status}, {"http_status", httpStatus}, {"error_class", usageDash(r.ErrorClass)}, {"source", r.Source},
		{"requests", strconv.FormatInt(r.Requests, 10)},
	}
	for _, name := range usageledger.TokenCounters {
		rows = append(rows, [2]string{name, usageRecordCounter(r, name)})
	}
	rows = append(rows, [2]string{"cost", usageCost(r.Cost)})
	return rows
}

var usageGroupHeader = map[string]string{
	"plane": "PLANE", "provider": "PROVIDER", "model_id": "MODEL", "tool_name": "TOOL", "actor_id": "AGENT",
	"principal_id": "USER", "client_id": "CLIENT", "access_point_id": "ACCESS POINT", "caller_kind": "CALLER KIND", "status": "STATUS",
}

// usageTokens renders one summed token counter. A counter no record reported
// is "unknown"; a partially reported counter shows how many records lacked it.
func usageTokens(g usageledger.SummaryGroup, name string) string {
	sum, reported := g.Tokens[name]
	missing := g.TokensUnknown[name]
	cell := strconv.FormatInt(sum, 10)
	switch {
	case !reported:
		return usageUnknown
	case missing > 0:
		cell = fmt.Sprintf("%d (+%d unknown)", sum, missing)
	}
	if g.BoundedUsageRecords > 0 {
		// The sum includes counters that are not final.
		cell += fmt.Sprintf(" [incl. %d bounded]", g.BoundedUsageRecords)
	}
	return cell
}

func usageBoundedCounts(g usageledger.SummaryGroup) string {
	if g.BoundedUsageRecords == 0 {
		return "—"
	}
	parts := []string{}
	for _, p := range [][2]any{{usageledger.UsageLowerBound, g.LowerBoundRecords}, {usageledger.UsageAmbiguous, g.AmbiguousUsageRecords}, {usageledger.UsagePartial, g.PartialUsageRecords}} {
		if n := p[1].(int64); n > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", p[0], n))
		}
	}
	return fmt.Sprintf("%d (%s)", g.BoundedUsageRecords, strings.Join(parts, " "))
}

func usageCostTotals(g usageledger.SummaryGroup) string {
	if len(g.Cost) == 0 {
		return usageUnknown
	}
	parts := make([]string, 0, len(g.Cost))
	for _, c := range g.Cost {
		parts = append(parts, aiplex.FormatCostTotal(c))
	}
	return strings.Join(parts, "; ")
}

func usageUnknownCounts(g usageledger.SummaryGroup) string {
	if g.UnknownUsageRecords == 0 && g.UnknownCostRecords == 0 {
		return "—"
	}
	return fmt.Sprintf("usage:%d cost:%d", g.UnknownUsageRecords, g.UnknownCostRecords)
}

func usageSummaryRows(s *usageledger.Summary) ([]string, [][]string) {
	var headers []string
	for _, field := range s.GroupBy {
		name := usageGroupHeader[field]
		if name == "" {
			name = strings.ToUpper(field)
		}
		headers = append(headers, name)
	}
	if s.Bucket != "" {
		headers = append(headers, "BUCKET")
	}
	headers = append(headers, "REQUESTS", "ERRORS", "DENIED", "IN", "OUT", "COST", "UNKNOWN", "BOUNDED")
	rows := make([][]string, 0, len(s.Groups))
	for _, g := range s.Groups {
		var row []string
		for _, field := range s.GroupBy {
			row = append(row, usageDash(g.Key[field]))
		}
		if s.Bucket != "" {
			start := "—"
			if g.BucketStart != nil {
				start = g.BucketStart.UTC().Format("2006-01-02T15:04Z")
			}
			row = append(row, start)
		}
		row = append(row, strconv.FormatInt(g.Requests, 10), strconv.FormatInt(g.Errors, 10), strconv.FormatInt(g.Denied, 10),
			usageTokens(g, usageledger.CounterInput), usageTokens(g, usageledger.CounterOutput), usageCostTotals(g), usageUnknownCounts(g), usageBoundedCounts(g))
		rows = append(rows, row)
	}
	return headers, rows
}

// ---- prices --------------------------------------------------------------

func usagePricesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prices",
		Short: "Manage model prices used to estimate cost (writes need the admin role)",
		Long: `Without a configured price a record's cost is unknown unless quota settles it.
Prices are revisioned resources: replacing or deleting one needs its current
revision, so concurrent edits are never silently overwritten.`,
	}
	cmd.AddCommand(usagePricesListCmd(), usagePricesGetCmd(), usagePricesSetCmd(), usagePricesDeleteCmd())
	return cmd
}

func usagePriceMeters(p usageledger.Price) string {
	names := make([]string, 0, len(p.Meters))
	for name := range p.Meters {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		rate := p.Meters[name]
		parts = append(parts, fmt.Sprintf("%s=%d/%d", name, rate.Numerator, rate.Denominator))
	}
	return strings.Join(parts, " ")
}

func usageEffective(p usageledger.Price) string {
	if p.EffectiveFrom == nil && p.EffectiveUntil == nil {
		return "always"
	}
	from, until := "…", "…"
	if p.EffectiveFrom != nil {
		from = p.EffectiveFrom.UTC().Format("2006-01-02")
	}
	if p.EffectiveUntil != nil {
		until = p.EffectiveUntil.UTC().Format("2006-01-02")
	}
	return from + " → " + until
}

func usagePricesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List configured model prices",
		Example: "  aiplex usage prices list",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			prices, err := newClient().ListModelPrices(cmd.Context())
			if err != nil {
				return fmt.Errorf("list prices: %w", err)
			}
			if prices == nil {
				prices = []aiplex.ModelPriceResource{}
			}
			if usageMachineOutput() {
				usagePrint(prices)
				return nil
			}
			if len(prices) == 0 {
				fmt.Println("No prices configured. Cost is unknown until a price is set or quota settles it.")
				return nil
			}
			rows := make([][]string, 0, len(prices))
			for _, p := range prices {
				rows = append(rows, []string{p.ID, p.Price.ModelID, usageDash(p.Price.Provider), p.Price.Currency, strconv.Itoa(int(p.Price.Scale)), usagePriceMeters(p.Price), usageEffective(p.Price), strconv.FormatUint(p.Revision, 10)})
			}
			printTable([]string{"ID", "MODEL", "PROVIDER", "CURRENCY", "SCALE", "METERS (units per denominator)", "EFFECTIVE", "REVISION"}, rows)
			return nil
		},
	}
}

func usagePricesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Short:   "Show one price and its current revision",
		Example: "  aiplex usage prices get gemini-flash",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			price, err := newClient().GetModelPrice(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get price: %w", err)
			}
			usagePrint(price)
			return nil
		},
	}
}

func usagePricesSetCmd() *cobra.Command {
	var file string
	var revision uint64
	cmd := &cobra.Command{
		Use:   "set -f <price.json>",
		Short: "Create a price, or replace one with --revision",
		Long: `Reads a price specification (id, model_id, provider?, currency, scale, meters,
effective_from?, effective_until?) and writes it. Without --revision the
price must not exist yet; to replace one, pass the revision shown by
"usage prices get". A stale revision fails with a conflict and changes nothing.`,
		Example: `  aiplex usage prices set -f gemini-flash.json
  aiplex usage prices set -f gemini-flash.json --revision 3`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return fmt.Errorf("-f/--file is required")
			}
			var price aiplex.ModelPrice
			if err := quotaReadFile(file, &price); err != nil {
				return fmt.Errorf("read price: %w", err)
			}
			saved, err := newClient().PutModelPrice(cmd.Context(), price, revision)
			if err != nil {
				return usagePriceError("set price", price.ID, err)
			}
			usagePrint(saved)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Price specification JSON file")
	cmd.Flags().Uint64Var(&revision, "revision", 0, "Current revision to replace (0 creates)")
	return cmd
}

func usagePricesDeleteCmd() *cobra.Command {
	var revision uint64
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a price (uses the current revision unless --revision is given)",
		Example: "  aiplex usage prices delete gemini-flash --revision 4",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			if revision == 0 {
				current, err := client.GetModelPrice(cmd.Context(), args[0])
				if err != nil {
					return fmt.Errorf("delete price: %w", err)
				}
				revision = current.Revision
			}
			if err := client.DeleteModelPrice(cmd.Context(), args[0], revision); err != nil {
				return usagePriceError("delete price", args[0], err)
			}
			fmt.Printf("Deleted price %s (revision %d).\n", args[0], revision)
			return nil
		},
	}
	cmd.Flags().Uint64Var(&revision, "revision", 0, "Expected revision")
	return cmd
}

func usagePriceError(action, id string, err error) error {
	if aiplex.IsRevisionConflict(err) {
		return fmt.Errorf("%s: %q was changed by someone else or already exists; run `aiplex usage prices get %s` and retry with its --revision: %w", action, id, id, err)
	}
	return fmt.Errorf("%s: %w", action, err)
}
