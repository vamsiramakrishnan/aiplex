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
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
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
	errorClass, bound, batch                                          string
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
	fl.StringVar(&f.errorClass, "error-class", "", "Error class (error_class)")
	fl.StringVar(&f.bound, "bound", "", "Usage bound: lower_bound, ambiguous, partial or none (usage_bound)")
	fl.StringVar(&f.batch, "batch", "", "Provider batch records only (true) or excluding them (false)")
	fl.Lookup("batch").NoOptDefVal = "true"
	fl.StringVar(&f.since, "since", "", "Start of window: duration like 24h, 7d, or an RFC3339 time")
	fl.StringVar(&f.until, "until", "", "End of window (exclusive): duration ago like 1h, or an RFC3339 time")
}

func (f *usageFilterFlags) filter(now time.Time) (aiplex.UsageFilter, error) {
	out := aiplex.UsageFilter{Plane: f.plane, Provider: f.provider, ModelID: f.model, ToolName: f.tool, ActorID: f.agent, PrincipalID: f.user, ClientID: f.client, AccessPointID: f.accessPoint, CallerKind: f.callerKind, Status: f.status, Source: f.source}
	out.ErrorClass, out.UsageBound = f.errorClass, f.bound
	switch f.bound {
	case "", usageledger.UsageLowerBound, usageledger.UsageAmbiguous, usageledger.UsagePartial, usageledger.UsageBoundNone:
	default:
		return out, fmt.Errorf("--bound must be lower_bound, ambiguous, partial or none")
	}
	switch f.batch {
	case "":
	case "true", "false":
		b := f.batch == "true"
		out.Batch = &b
	default:
		return out, fmt.Errorf("--batch must be true or false (use --batch=false to exclude batch records)")
	}
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
					usageRecordTable(records).render(os.Stdout)
					if !wideOutput() {
						fmt.Println("\nRecord IDs and every counter: -o wide. One record in full: aiplex usage get <id>.")
					}
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
access-point, caller-kind, status, source, error-class, bound (usage_bound)
or batch (aliases: model=model_id, agent=actor_id,
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
				usageSummaryTable(summary).render(os.Stdout)
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
	cmd.Flags().StringSliceVar(&by, "by", nil, "Group by (comma separated): plane,provider,model,tool,agent,user,client,access-point,caller-kind,status,source,error-class,bound,batch")
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

// usageRecordTable renders records under the table policy: by default the
// columns needed to answer "who used what, did it work, what did it cost";
// -o wide adds identifiers and every counter.
func usageRecordTable(records []usageledger.Record) *outTable {
	t := newTable(
		wideCol("ID").trunc(truncNever), col("TIME"), wideCol("PLANE"), col("MODEL/TOOL"), col("CALLER").trunc(truncMiddle),
		wideCol("AGENT"), wideCol("USER").trunc(truncNever), wideCol("CLIENT"), col("ACCESS POINT"), wideCol("SESSION"), wideCol("BATCH"),
		col("STATUS"), col("IN"), col("OUT"), wideCol("CACHE-W"), wideCol("MODALITY"), wideCol("UNITS"), col("COST").trunc(truncNever), wideCol("LATENCY"),
	)
	for _, r := range records {
		cost := usageCost(r.Cost)
		latency := humanDuration(r.LatencyMS)
		if wideOutput() {
			latency = fmt.Sprintf("%dms", r.LatencyMS)
		} else {
			cost = shortMoney(r.Cost.Currency, r.Cost.Amount, r.Cost.Scale, r.Cost.Basis)
		}
		t.add(r.ID, relTime(r.StartedAt), r.Plane, usageTarget(r), usageCaller(r), usageDash(usageAgentLabel(r)), usageDash(r.PrincipalID), usageDash(r.ClientID),
			usageDash(r.AccessPointID), usageSessionCell(r), usageBatchCell(r), usageStatusCell(r),
			usageRecordCounter(r, usageledger.CounterInput), usageRecordCounter(r, usageledger.CounterOutput), usageRecordCounter(r, usageledger.CounterCacheWrite),
			usageModalityCell(r), usageUnitsCell(r), cost, latency)
	}
	return t
}

// usageCaller is the human label of who made the request: the user's
// display label (email) when the server recorded one, else a short form of
// the principal ID; an agent is shown with the user it acts for.
func usageCaller(r usageledger.Record) string {
	user := r.PrincipalDisplay
	if user == "" && r.PrincipalID != "" {
		user = shortPrincipal(r.PrincipalID)
	}
	agent := usageAgentLabel(r)
	switch {
	case user != "" && agent != "":
		return agent + " (" + user + ")"
	case user != "":
		return user
	case agent != "":
		return agent
	}
	return "—"
}

func usageAgentLabel(r usageledger.Record) string {
	if r.ActorDisplay != "" {
		return r.ActorDisplay
	}
	return r.ActorID
}

func usageBatchCell(r usageledger.Record) string {
	if r.Batch {
		return "batch"
	}
	return "—"
}

// usageStatusCell appends the reason of a capacity rejection: the request was
// refused by the capacity policy before reaching the provider.
func usageStatusCell(r usageledger.Record) string {
	switch r.ErrorClass {
	case "capacity_cooldown", "capacity_shed":
		return r.Status + " (" + r.ErrorClass + ")"
	}
	return r.Status
}

// usageDuration renders a millisecond unit as a duration, keeping the exact
// milliseconds when they are not a whole second.
func usageDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if ms%1000 == 0 {
		return d.String()
	}
	return fmt.Sprintf("%s (%dms)", d.String(), ms)
}

// usageUnitLabel names a unit counter for tables. Units are never tokens.
var usageUnitLabel = map[string]string{
	usageledger.CounterAudioInputMilliseconds:   "audio-in",
	usageledger.CounterVoiceSessionMilliseconds: "voice-session",
	usageledger.CounterVideoOutputMilliseconds:  "video-out",
	usageledger.CounterGeneratedImages:          "images",
}

// usageUnitValue renders one reported unit quantity: milliseconds as a
// duration, images as a count with the record's size and quality when known.
func usageUnitValue(name string, v int64, size, quality string) string {
	if name == usageledger.CounterGeneratedImages {
		cell := fmt.Sprintf("%d images", v)
		if v == 1 {
			cell = "1 image"
		}
		if dims := strings.TrimSpace(size + " " + quality); dims != "" {
			cell += " (" + dims + ")"
		}
		return cell
	}
	return usageDuration(v)
}

// usageUnitsCell lists only the units the record reported. A unit that is not
// listed is unknown, never zero; a reported zero is shown as zero.
func usageUnitsCell(r usageledger.Record) string {
	parts := []string{}
	for _, name := range usageledger.UnitCounters {
		if v := r.Usage.UnitCounter(name); v != nil {
			value := usageUnitValue(name, *v, r.ImageSize, r.ImageQuality)
			if name != usageledger.CounterGeneratedImages {
				value = usageUnitLabel[name] + " " + value
			}
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

var usageModalityShort = strings.NewReplacer("input_", "", "output_", "", "_tokens", "")

func usageModalityParts(counters []string, get func(string) (int64, bool)) string {
	parts := []string{}
	for _, name := range counters {
		if v, ok := get(name); ok {
			parts = append(parts, usageModalityShort.Replace(name)+"="+strconv.FormatInt(v, 10))
		}
	}
	return strings.Join(parts, " ")
}

// usageModalityText renders modality token splits (subsets of IN / OUT):
// "in text=7 audio=0 | out image=4". Splits the provider did not report are
// omitted, never shown as zero.
func usageModalityText(get func(string) (int64, bool)) string {
	in := usageModalityParts(usageledger.InputModalityCounters, get)
	out := usageModalityParts(usageledger.OutputModalityCounters, get)
	switch {
	case in != "" && out != "":
		return "in " + in + " | out " + out
	case in != "":
		return "in " + in
	case out != "":
		return "out " + out
	}
	return "—"
}

func usageModalityCell(r usageledger.Record) string {
	return usageModalityText(func(name string) (int64, bool) {
		v := r.Usage.UnitCounter(name)
		if v == nil {
			return 0, false
		}
		return *v, true
	})
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
		{"principal_id", usageDash(r.PrincipalID)}, {"principal_display", usageDash(r.PrincipalDisplay)}, {"actor_id", usageDash(r.ActorID)}, {"actor_display", usageDash(r.ActorDisplay)}, {"client_id", usageDash(r.ClientID)}, {"request_id", usageDash(r.RequestID)},
		{"access_point_id", usageDash(r.AccessPointID)}, {"caller_kind", usageDash(r.CallerKind)},
		{"trace_id", usageDash(r.TraceID)}, {"quota_attempt_id", usageDash(r.QuotaAttemptID)}, {"streaming", strconv.FormatBool(r.Streaming)}, {"session", strconv.FormatBool(r.Session)},
		{"usage_bound", usageDash(r.UsageBound)},
		{"status", r.Status}, {"http_status", httpStatus}, {"error_class", usageDash(r.ErrorClass)}, {"source", r.Source},
		{"requests", strconv.FormatInt(r.Requests, 10)},
	}
	rows = append(rows, [2]string{"batch", strconv.FormatBool(r.Batch)}, [2]string{"image_size", usageDash(r.ImageSize)}, [2]string{"image_quality", usageDash(r.ImageQuality)})
	for _, name := range usageledger.TokenCounters {
		rows = append(rows, [2]string{name, usageRecordCounter(r, name)})
	}
	// Modality splits are subsets of input_tokens / output_tokens.
	for _, name := range usageledger.ModalityCounters {
		rows = append(rows, [2]string{name + " (subset)", usageUnitCounter(r, name)})
	}
	// Units are other quantities and are never added to tokens.
	for _, name := range usageledger.UnitCounters {
		rows = append(rows, [2]string{name + " (unit)", usageUnitCounter(r, name)})
	}
	rows = append(rows, [2]string{"cost", usageCost(r.Cost)})
	return append(rows, usageLimitRows(r.ProviderLimits)...)
}

// usageUnitCounter renders a modality split or unit; unreported is "unknown".
func usageUnitCounter(r usageledger.Record, name string) string {
	v := r.Usage.UnitCounter(name)
	if v == nil {
		return usageUnknown
	}
	cell := strconv.FormatInt(*v, 10)
	switch name {
	case usageledger.CounterGeneratedImages:
		if r.ImageSize != "" || r.ImageQuality != "" {
			cell += " (" + strings.TrimSpace(r.ImageSize+" "+r.ImageQuality) + ")"
		}
	case usageledger.CounterAudioInputMilliseconds, usageledger.CounterVoiceSessionMilliseconds, usageledger.CounterVideoOutputMilliseconds:
		cell += " ms (" + usageDuration(*v) + ")"
	}
	if r.UsageBound != "" {
		cell += " (" + r.UsageBound + ")"
	}
	return cell
}

func usageLimitInt(v *int64) string {
	if v == nil {
		return usageUnknown
	}
	return strconv.FormatInt(*v, 10)
}

func usageLimitWindow(w *providerlimits.Window) string {
	if w == nil {
		return "not reported"
	}
	cell := "remaining " + usageLimitInt(w.Remaining) + " of " + usageLimitInt(w.Limit)
	if w.ResetAt != nil {
		cell += ", resets " + w.ResetAt.UTC().Format(time.RFC3339)
	}
	return cell
}

// usageLimitRows renders the provider rate-limit metadata observed on the
// exchange. Fields the provider did not report are "unknown", never zero.
func usageLimitRows(l *providerlimits.Limits) [][2]string {
	if l == nil {
		return [][2]string{{"provider_limits", "none observed"}}
	}
	rows := [][2]string{{"provider_limits", l.Provider + " observed " + l.ObservedAt.UTC().Format(time.RFC3339)}}
	if l.HTTPStatus != 0 {
		rows = append(rows, [2]string{"  http_status", strconv.Itoa(l.HTTPStatus)})
	}
	rows = append(rows, [2]string{"  classification", usageDash(l.Classification)})
	retry := usageUnknown
	if l.RetryAfterMS != nil {
		retry = (time.Duration(*l.RetryAfterMS) * time.Millisecond).String()
	}
	rows = append(rows, [2]string{"  retry_after", retry},
		[2]string{"  requests", usageLimitWindow(l.Requests)}, [2]string{"  tokens", usageLimitWindow(l.Tokens)},
		[2]string{"  input_tokens", usageLimitWindow(l.InputTokens)}, [2]string{"  output_tokens", usageLimitWindow(l.OutputTokens)})
	return rows
}

var usageGroupHeader = map[string]string{
	"plane": "PLANE", "provider": "PROVIDER", "model_id": "MODEL", "tool_name": "TOOL", "actor_id": "AGENT",
	"principal_id": "USER", "client_id": "CLIENT", "access_point_id": "ACCESS POINT", "caller_kind": "CALLER KIND", "status": "STATUS",
	"source": "SOURCE", "error_class": "ERROR CLASS", "usage_bound": "BOUND", "batch": "BATCH",
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
		if wideOutput() {
			parts = append(parts, aiplex.FormatCostTotal(c))
		} else {
			parts = append(parts, shortMoney(c.Currency, c.Amount, c.Scale, c.Basis))
		}
	}
	return strings.Join(parts, "; ")
}

func usageUnknownCounts(g usageledger.SummaryGroup) string {
	if g.UnknownUsageRecords == 0 && g.UnknownCostRecords == 0 {
		return "—"
	}
	return fmt.Sprintf("usage:%d cost:%d", g.UnknownUsageRecords, g.UnknownCostRecords)
}

func usageGroupModality(g usageledger.SummaryGroup) string {
	return usageModalityText(func(name string) (int64, bool) { v, ok := g.ModalityTokens[name]; return v, ok })
}

// usageGroupUnits lists the units some record reported, with how many records
// reported each. Units no record reported are absent (unknown), never zero.
func usageGroupUnits(g usageledger.SummaryGroup) string {
	parts := []string{}
	for _, name := range usageledger.UnitCounters {
		v, ok := g.Units[name]
		if !ok {
			continue
		}
		value := usageUnitValue(name, v, "", "")
		if name != usageledger.CounterGeneratedImages {
			value = usageUnitLabel[name] + " " + value
		}
		parts = append(parts, fmt.Sprintf("%s (%d rec)", value, g.UnitRecords[name]))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

func usageGroupBatch(g usageledger.SummaryGroup) string {
	if g.BatchRecords == 0 {
		return "—"
	}
	return strconv.FormatInt(g.BatchRecords, 10)
}

// usageSummaryTable renders summary groups: requests, errors, tokens and
// cost by default; -o wide adds cache writes, modalities, units, batch and the
// unknown/bounded counts.
func usageSummaryTable(s *usageledger.Summary) *outTable {
	var cols []column
	for _, field := range s.GroupBy {
		name := usageGroupHeader[field]
		if name == "" {
			name = strings.ToUpper(field)
		}
		c := col(name)
		if field == "principal_id" || field == "actor_id" {
			c = c.trunc(truncMiddle)
		}
		cols = append(cols, c)
	}
	if s.Bucket != "" {
		cols = append(cols, col("BUCKET"))
	}
	cols = append(cols, col("REQUESTS"), col("ERRORS"), col("DENIED"), col("IN"), col("OUT"), wideCol("CACHE-W"), wideCol("MODALITY"), wideCol("UNITS"), wideCol("BATCH"), col("COST"), wideCol("UNKNOWN"), wideCol("BOUNDED"))
	t := newTable(cols...)
	for _, g := range s.Groups {
		var row []string
		for _, field := range s.GroupBy {
			v := g.Key[field]
			if label := g.Labels[field]; label != "" && !wideOutput() {
				v = label
			} else if field == "principal_id" && !wideOutput() {
				v = shortPrincipal(v)
			}
			row = append(row, usageDash(v))
		}
		if s.Bucket != "" {
			start := "—"
			if g.BucketStart != nil {
				start = g.BucketStart.UTC().Format("2006-01-02T15:04Z")
			}
			row = append(row, start)
		}
		row = append(row, strconv.FormatInt(g.Requests, 10), strconv.FormatInt(g.Errors, 10), strconv.FormatInt(g.Denied, 10),
			usageTokens(g, usageledger.CounterInput), usageTokens(g, usageledger.CounterOutput), usageTokens(g, usageledger.CounterCacheWrite),
			usageGroupModality(g), usageGroupUnits(g), usageGroupBatch(g), usageCostTotals(g), usageUnknownCounts(g), usageBoundedCounts(g))
		t.add(row...)
	}
	return t
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

// usagePriceSelectors lists the selectors that narrow a price.
func usagePriceSelectors(p usageledger.Price) string {
	parts := []string{}
	if p.Batch != nil {
		parts = append(parts, "batch="+strconv.FormatBool(*p.Batch))
	}
	if p.ImageSize != "" {
		parts = append(parts, "image_size="+p.ImageSize)
	}
	if p.ImageQuality != "" {
		parts = append(parts, "image_quality="+p.ImageQuality)
	}
	if len(parts) == 0 {
		return "—"
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
		Example: "  aiplex usage prices list\n  aiplex usage prices list -o wide",
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
				fmt.Println("Set one with: aiplex usage prices set <model> --input-per-mtok 3 --output-per-mtok 15")
				return nil
			}
			t := newTable(keyCol("ID"), col("MODEL"), wideCol("PROVIDER"), col("PRICE"), wideCol("CURRENCY"), wideCol("SCALE"), wideCol("METERS (money per denominator)"), col("SELECTORS"), col("EFFECTIVE"), wideCol("REVISION"))
			for _, p := range prices {
				t.add(p.ID, p.Price.ModelID, usageDash(p.Price.Provider), humanPriceMeters(p.Price), p.Price.Currency, strconv.Itoa(int(p.Price.Scale)), usagePriceMeters(p.Price), usagePriceSelectors(p.Price), usageEffective(p.Price), strconv.FormatUint(p.Revision, 10))
			}
			t.render(os.Stdout)
			return nil
		},
	}
}

func usagePricesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Short:   "Show one price and its current revision",
		Example: "  aiplex usage prices get mock-gpt\n  aiplex usage prices get mock-gpt -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			price, err := newClient().GetModelPrice(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get price: %w", err)
			}
			if usageMachineOutput() {
				usagePrint(price)
				return nil
			}
			p := price.Price
			fmt.Printf("Price %s (revision %d)\n", price.ID, price.Revision)
			fmt.Printf("  Model:      %s\n", p.ModelID)
			if p.Provider != "" {
				fmt.Printf("  Provider:   %s\n", p.Provider)
			}
			fmt.Printf("  Rates:      %s\n", humanPriceMeters(p))
			fmt.Printf("  Stored as:  %s %s, scale %d\n", p.Currency, usagePriceMeters(p), p.Scale)
			fmt.Printf("  Selectors:  %s\n", usagePriceSelectors(p))
			fmt.Printf("  Effective:  %s\n", usageEffective(p))
			return nil
		},
	}
}

type priceFlags struct {
	file, currency, provider                             string
	input, output, cacheRead, cacheWrite, reasoning, req string
	scale                                                uint8
	revision                                             uint64
}

var priceMeterFlags = []struct{ flag, meter string }{
	{"input-per-mtok", usageledger.MeterInput}, {"output-per-mtok", usageledger.MeterOutput},
	{"cache-read-per-mtok", usageledger.MeterCacheRead}, {"cache-write-per-mtok", usageledger.MeterCacheWrite},
	{"reasoning-per-mtok", usageledger.MeterReasoning}, {"per-request", usageledger.MeterRequests},
}

func usagePricesSetCmd() *cobra.Command {
	var f priceFlags
	cmd := &cobra.Command{
		Use:   "set <model> --input-per-mtok <price> --output-per-mtok <price> [--currency USD]",
		Short: "Set a model's price per million tokens (or -f for a full spec)",
		Long: `Sets the price used to estimate cost for one model. Prices are given per
million tokens (or per request) as decimals and converted to the exact rational
form the API stores: no floating point, no rounding. With aiplex dev the mock
model already has a placeholder price.

When the price exists, only the rates you pass change (the revision is read for
you; a concurrent change fails with a conflict). Changing --scale or
--currency of an existing price needs every rate it should keep.

-f reads a full specification (id, model_id, currency, scale, meters, ...),
creating it or replacing the existing one. --revision pins the revision you
expect instead of reading it.`,
		Example: `  aiplex usage prices set gpt-4o-mini --input-per-mtok 0.15 --output-per-mtok 0.60
  aiplex usage prices set claude-sonnet-4 --input-per-mtok 3 --output-per-mtok 15 --cache-read-per-mtok 0.30 --currency USD
  aiplex usage prices set gpt-4o-mini --output-per-mtok 0.50
  aiplex usage prices set -f gemini-flash.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := cmd.Context()
			if f.file != "" {
				if len(args) > 0 {
					return usageError("give either <model> with rate flags or -f <file>, not both")
				}
				var price aiplex.ModelPrice
				if err := quotaReadFile(f.file, &price); err != nil {
					return fmt.Errorf("read price: %w", err)
				}
				revision := f.revision
				if !cmd.Flags().Changed("revision") {
					if current, err := c.GetModelPrice(ctx, price.ID); err == nil {
						revision = current.Revision
					} else if !spIsNotFound(err) {
						return fmt.Errorf("read price: %w", err)
					}
				}
				saved, err := c.PutModelPrice(ctx, price, revision)
				if err != nil {
					return usagePriceError("set price", price.ID, err)
				}
				return printSavedPrice(saved, revision > 0)
			}
			if len(args) == 0 {
				return usageError("missing <model>", "Example: aiplex usage prices set gpt-4o-mini --input-per-mtok 0.15 --output-per-mtok 0.60", "Or a full specification: aiplex usage prices set -f price.json")
			}
			model := args[0]
			given := false
			for _, m := range priceMeterFlags {
				given = given || cmd.Flags().Changed(m.flag)
			}
			if !given {
				return usageError("give at least one rate, e.g. --input-per-mtok 3 --output-per-mtok 15", "Rates: --input-per-mtok --output-per-mtok --cache-read-per-mtok --cache-write-per-mtok --reasoning-per-mtok --per-request")
			}
			var price aiplex.ModelPrice
			var revision uint64
			current, err := c.GetModelPrice(ctx, model)
			switch {
			case err == nil:
				price, revision = current.Price, current.Revision
				if (cmd.Flags().Changed("scale") && f.scale != price.Scale) || (cmd.Flags().Changed("currency") && f.currency != price.Currency) {
					for _, m := range priceMeterFlags {
						if _, kept := price.Meters[m.meter]; kept && !cmd.Flags().Changed(m.flag) {
							return usageError(fmt.Sprintf("changing --scale or --currency of price %s needs every rate it should keep; --%s is missing", model, m.flag))
						}
					}
					price.Meters = map[string]usageledger.Rational{}
				}
			case spIsNotFound(err):
				price = aiplex.ModelPrice{ID: model, ModelID: model, Currency: "USD", Scale: 6, Meters: map[string]usageledger.Rational{}}
			default:
				return fmt.Errorf("read price: %w", err)
			}
			if cmd.Flags().Changed("currency") {
				price.Currency = strings.ToUpper(f.currency)
			}
			if cmd.Flags().Changed("scale") {
				price.Scale = f.scale
			}
			if cmd.Flags().Changed("provider") {
				price.Provider = f.provider
			}
			if cmd.Flags().Changed("revision") {
				revision = f.revision
			}
			values := map[string]string{"input-per-mtok": f.input, "output-per-mtok": f.output, "cache-read-per-mtok": f.cacheRead, "cache-write-per-mtok": f.cacheWrite, "reasoning-per-mtok": f.reasoning, "per-request": f.req}
			if price.Meters == nil {
				price.Meters = map[string]usageledger.Rational{}
			}
			for _, m := range priceMeterFlags {
				if !cmd.Flags().Changed(m.flag) {
					continue
				}
				var rate usageledger.Rational
				var e error
				if m.meter == usageledger.MeterRequests {
					rate, e = perUnitRate(values[m.flag], price.Scale)
				} else {
					rate, e = perMillionRate(values[m.flag], price.Scale)
				}
				if e != nil {
					return usageError("--" + m.flag + ": " + e.Error())
				}
				price.Meters[m.meter] = rate
			}
			saved, err := c.PutModelPrice(ctx, price, revision)
			if err != nil {
				return usagePriceError("set price", model, err)
			}
			return printSavedPrice(saved, revision > 0)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&f.file, "file", "f", "", "Full price specification (JSON or YAML)")
	fl.StringVar(&f.input, "input-per-mtok", "", "Price per million input tokens, e.g. 3 or 0.075")
	fl.StringVar(&f.output, "output-per-mtok", "", "Price per million output tokens")
	fl.StringVar(&f.cacheRead, "cache-read-per-mtok", "", "Price per million cached input tokens read")
	fl.StringVar(&f.cacheWrite, "cache-write-per-mtok", "", "Price per million cache-write tokens")
	fl.StringVar(&f.reasoning, "reasoning-per-mtok", "", "Price per million reasoning tokens, charged on top of output (only for providers that report them separately)")
	fl.StringVar(&f.req, "per-request", "", "Price per request")
	fl.StringVar(&f.currency, "currency", "USD", "Three-letter currency code")
	fl.Uint8Var(&f.scale, "scale", 6, "Decimal places costs are computed to (6 = millionths)")
	fl.StringVar(&f.provider, "provider", "", "Only apply to records from this provider")
	fl.Uint64Var(&f.revision, "revision", 0, "Expected current revision (default: read it for you)")
	return cmd
}

func printSavedPrice(saved *aiplex.ModelPriceResource, updated bool) error {
	if usageMachineOutput() {
		usagePrint(saved)
		return nil
	}
	verb := "Created"
	if updated {
		verb = "Updated"
	}
	fmt.Printf("%s price %s (revision %d): %s\n", verb, saved.ID, saved.Revision, humanPriceMeters(saved.Price))
	return nil
}

func usagePricesDeleteCmd() *cobra.Command {
	var revision uint64
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a price (uses the current revision unless --revision is given)",
		Example: "  aiplex usage prices delete gemini-flash --yes",
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
			if err := confirm(cmd, yes, "Delete price "+args[0]+"? Cost of new records for its model becomes unknown."); err != nil {
				return err
			}
			if err := client.DeleteModelPrice(cmd.Context(), args[0], revision); err != nil {
				return usagePriceError("delete price", args[0], err)
			}
			fmt.Printf("Deleted price %s (revision %d).\n", args[0], revision)
			return nil
		},
	}
	cmd.Flags().Uint64Var(&revision, "revision", 0, "Expected revision")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
	return cmd
}

func usagePriceError(action, id string, err error) error {
	if aiplex.IsRevisionConflict(err) {
		return &cliError{msg: fmt.Sprintf("%s: price %q was changed by someone else since it was read", action, id), code: exitConflict, err: err,
			hints: []string{"Nothing was changed.", "See the current price with: aiplex usage prices get " + id, "Run the command again to apply your change on top of it."}}
	}
	return fmt.Errorf("%s: %w", action, err)
}
