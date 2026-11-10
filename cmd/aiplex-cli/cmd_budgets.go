package main

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func budgetsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "budgets",
		Aliases: []string{"budget"},
		Short:   "Budgets: team and project limits above per-caller quota",
		Long: `A budget caps cost, tokens or requests for a team, project, access point or
other set of callers, over a calendar month, week or day (or tumbling N-hour
windows). Budgets form a tree: a request draws from its own budget and every
parent, and is refused when any one is exhausted. QuotaPlex enforces them, so
QuotaPlex must be enabled (aiplex dev --quota does this locally).

  aiplex budgets create platform --name "Platform team" --unit cost --currency USD --amount 250.00 --window month --group platform-eng
  aiplex budgets status platform
  aiplex budgets preview --group platform-eng --access-point ide

Cost budgets need a price for every model they count. A model with no price is
refused by default (--on-unknown-cost deny); allow_and_flag admits it and counts
it as unknown cost instead.`,
		Example:     "  aiplex budgets list\n  aiplex budgets status platform",
		Annotations: map[string]string{"kind": "budget"},
	}
	cmd.AddCommand(budgetsListCmd(), budgetsGetCmd(), budgetsCreateCmd(), budgetsUpdateCmd(), budgetsDeleteCmd(), budgetsStatusCmd(), budgetsPreviewCmd())
	registerLister("budgets", func(ctx context.Context, c *aiplex.Client) ([]string, error) {
		list, err := c.ListBudgets(ctx)
		ids := make([]string, 0, len(list))
		for _, r := range list {
			ids = append(ids, r.ID)
		}
		return ids, err
	})
	return cmd
}

var budgetWindowHours = regexp.MustCompile(`^([1-9][0-9]{0,3})h$`)

// budgetLimitText renders "250.00 USD", "1000000 tokens" or "5000 requests".
func budgetLimitText(unit, currency, amount string) string {
	if unit == budgets.UnitCost {
		return amount + " " + currency
	}
	return amount + " " + unit
}

func budgetWindowText(w budgets.Window) string {
	if w.Kind == budgets.WindowTumbling {
		return fmt.Sprintf("every %dh", w.Hours)
	}
	return fmt.Sprintf("%s (%s)", w.Unit, w.TimeZone)
}

func budgetMemberCount(m budgets.Members) int {
	return len(m.Groups) + len(m.Principals) + len(m.Agents) + len(m.ClientRegistrations) + len(m.AccessPoints) + len(m.Projects)
}

func budgetsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List budgets (admin or deployer)",
		Example: "  aiplex budgets list\n  aiplex budgets list -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := newClient().ListBudgets(cmd.Context())
			if err != nil {
				return fmt.Errorf("list budgets: %w", err)
			}
			if spStructured(cmd, list) {
				return nil
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No budgets. Create one with: aiplex budgets create <id> --unit cost --currency USD --amount 100 --window month --group <group>")
				return nil
			}
			t := newTable(keyCol("ID"), col("NAME"), idCol("PARENT"), col("LIMIT"), col("WINDOW"), col("MEMBERS"), col("REV"))
			for _, r := range list {
				s := r.Spec
				t.add(r.ID, spDash(s.DisplayName), spDash(s.Parent), budgetLimitText(s.Limit.Unit, s.Limit.Currency, s.Limit.Amount), budgetWindowText(s.Window), strconv.Itoa(budgetMemberCount(s.Members)), strconv.FormatUint(r.Revision, 10))
			}
			t.render(cmd.OutOrStdout())
			return nil
		},
	}
}

func budgetsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Aliases: []string{"show", "describe"},
		Short:   "Show one budget and where it stands",
		Example: "  aiplex budgets get platform\n  aiplex budgets get platform -o yaml > budget.yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			r, err := c.GetBudget(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get budget: %w", err)
			}
			if spStructured(cmd, r) {
				return nil
			}
			out, s := cmd.OutOrStdout(), r.Spec
			fmt.Fprintf(out, "Budget %s (revision %d)\n", r.ID, r.Revision)
			if s.DisplayName != "" {
				fmt.Fprintf(out, "  Name:     %s\n", s.DisplayName)
			}
			if s.Parent != "" {
				fmt.Fprintf(out, "  Parent:   %s\n", s.Parent)
			}
			fmt.Fprintf(out, "  Limit:    %s per %s\n", budgetLimitText(s.Limit.Unit, s.Limit.Currency, s.Limit.Amount), budgetWindowText(s.Window))
			if s.Limit.Unit == budgets.UnitCost {
				fmt.Fprintf(out, "  Unknown cost: %s\n", s.EffectiveOnUnknownCost())
			}
			m := s.Members
			if budgetMemberCount(m) == 0 {
				fmt.Fprintln(out, "  Members:  none directly (draws only through child budgets)")
			}
			for _, g := range m.Groups {
				fmt.Fprintf(out, "  Member:   group %s (source %s)\n", g.ID, g.Source)
			}
			for _, kv := range []struct {
				kind   string
				values []string
			}{{"principal", m.Principals}, {"agent", m.Agents}, {"client registration", m.ClientRegistrations}, {"access point", m.AccessPoints}, {"project", m.Projects}} {
				for _, v := range kv.values {
					fmt.Fprintf(out, "  Member:   %s %s\n", kv.kind, v)
				}
			}
			fmt.Fprintf(out, "  Alerts:   %s %%\n", intsText(s.EffectiveThresholds()))
			if st, err := c.GetBudgetStatus(cmd.Context(), args[0]); err == nil {
				fmt.Fprintf(out, "\nThis window (%s to %s)\n", st.Window.Start.UTC().Format("2006-01-02 15:04Z"), st.Window.End.UTC().Format("2006-01-02 15:04Z"))
				fmt.Fprintf(out, "  Spent %s, reserved %s, remaining %s of %s\n", st.Spent, st.Reserved, st.Remaining, st.Limit)
			} else {
				fmt.Fprintf(out, "\nCounters are unavailable (%v).\n", err)
			}
			fmt.Fprintf(out, "Next: aiplex budgets status %s    aiplex budgets preview --access-point <id>\n", r.ID)
			return nil
		},
	}
}

func intsText(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

// budgetSpecFlags are the flags that define a budget.
type budgetSpecFlags struct {
	name, parent, unit, currency, amount, window, timezone, onUnknown string
	groups, users, agents, clients, accessPoints, projects            []string
	thresholds                                                        []int
	update                                                            bool
}

func (f *budgetSpecFlags) bind(cmd *cobra.Command, forCreate bool) {
	fl := cmd.Flags()
	fl.StringVar(&f.name, "name", "", "Display name")
	fl.StringVar(&f.parent, "parent", "", "Parent budget ID (a request also draws from every ancestor)")
	fl.StringVar(&f.unit, "unit", "", "What the budget caps: cost, tokens or requests")
	fl.StringVar(&f.currency, "currency", "", "Currency for a cost budget (for example USD); must match a money meter of the quota policy")
	fl.StringVar(&f.amount, "amount", "", "Limit as an exact decimal, such as 250.00 (cost) or 1000000 (tokens, requests)")
	fl.StringVar(&f.window, "window", "", "month, week (starts Monday), day, or a tumbling window such as 6h")
	fl.StringVar(&f.timezone, "timezone", "", "IANA time zone of a calendar window (default UTC)")
	fl.StringVar(&f.onUnknown, "on-unknown-cost", "", "Cost budgets: deny (default) refuses a model with no price; allow_and_flag admits it and counts it as unknown cost")
	fl.IntSliceVar(&f.thresholds, "threshold", nil, "Alert threshold in percent, repeatable (default 50, 80, 100)")
	if forCreate {
		fl.StringSliceVar(&f.groups, "group", nil, "Member group name from your identity provider, repeatable")
		fl.StringSliceVar(&f.users, "user", nil, "Member user (principal id, or name@localhost in aiplex dev), repeatable")
		fl.StringSliceVar(&f.agents, "agent", nil, "Member agent ID, repeatable")
		fl.StringSliceVar(&f.clients, "client", nil, "Member client registration ID, repeatable")
		fl.StringSliceVar(&f.accessPoints, "access-point", nil, "Member access point ID, repeatable")
		fl.StringSliceVar(&f.projects, "project", nil, "Member project label (set on an access point with its project field), repeatable")
		fl.BoolVar(&f.update, "update", false, "Replace the budget if it already exists")
	}
}

func parseBudgetWindow(window, zone string) (budgets.Window, error) {
	switch window {
	case "day", "week", "month":
		if zone == "" {
			zone = "UTC"
		}
		return budgets.Window{Kind: budgets.WindowCalendar, Unit: window, TimeZone: zone}, nil
	}
	if m := budgetWindowHours.FindStringSubmatch(window); m != nil {
		if zone != "" {
			return budgets.Window{}, usageError("--timezone applies only to day, week and month windows")
		}
		hours, _ := strconv.Atoi(m[1])
		return budgets.Window{Kind: budgets.WindowTumbling, Hours: hours}, nil
	}
	if window == "rolling" || strings.HasPrefix(window, "rolling") {
		return budgets.Window{}, usageError("rolling windows are not supported", "Use a calendar window (month, week, day) or a tumbling window such as 6h")
	}
	return budgets.Window{}, usageError("--window must be month, week, day or a tumbling window such as 6h")
}

// membershipSource finds the group source of the signed-in identity provider.
func budgetGroupSource(ctx context.Context, c *aiplex.Client) (provider, pool string, err error) {
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	me, err := c.GetIdentity(lctx)
	if err != nil {
		return "", "", fmt.Errorf("resolve your identity provider for --group/--user: %w", err)
	}
	return me.Identity.Provider, me.Identity.PoolID, nil
}

func (f *budgetSpecFlags) addMembers(ctx context.Context, c *aiplex.Client, m *budgets.Members, groups, users, agents, clients, aps, projects []string) error {
	if len(groups)+len(users) > 0 {
		provider, pool, err := budgetGroupSource(ctx, c)
		if err != nil {
			return err
		}
		if len(groups) > 0 && provider == "" {
			return usageError("--group needs a signed-in identity to know which identity provider the groups belong to", "Sign in (aiplex login, or eval \"$(aiplex dev env)\") or use a file with members.groups")
		}
		source := auth.MembershipSource(provider, pool)
		for _, g := range nonEmpty(groups) {
			m.Groups = append(m.Groups, quotaplex.GroupRef{Source: source, ID: g})
		}
		for _, u := range nonEmpty(users) {
			p, err := resolveUserPrincipal(ctx, c, u, provider, pool)
			if err != nil {
				return err
			}
			m.Principals = append(m.Principals, p)
		}
	}
	m.Agents = append(m.Agents, nonEmpty(agents)...)
	m.ClientRegistrations = append(m.ClientRegistrations, nonEmpty(clients)...)
	m.AccessPoints = append(m.AccessPoints, nonEmpty(aps)...)
	m.Projects = append(m.Projects, nonEmpty(projects)...)
	return nil
}

func budgetsCreateCmd() *cobra.Command {
	var f budgetSpecFlags
	var file string
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create (or with --update replace) a budget",
		Long: `Creates a budget, or replaces it with --update. A budget needs a limit (--unit,
--amount, and --currency for cost), a window and, usually, members. Without
members a budget draws only through its child budgets.

Group names are qualified with the identity provider you are signed in to. Use
'aiplex budgets create -f budget.yaml' to give the full specification.`,
		Example: `  aiplex budgets create platform --name "Platform team" --unit cost --currency USD --amount 250.00 --window month --group platform-eng
  aiplex budgets create engineering --unit tokens --amount 50000000 --window month
  aiplex budgets create platform-chat --parent engineering --unit tokens --amount 10000000 --window week --access-point ide
  aiplex budgets create ci --unit requests --amount 5000 --window 6h --agent spiffe://example/ci`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			spec := budgets.Budget{ID: id}
			if file != "" {
				raw, err := spReadSpec(file, cmd.InOrStdin())
				if err != nil {
					return err
				}
				if err = spStrictDecode(raw, &spec); err != nil {
					return fmt.Errorf("invalid budget: %w", err)
				}
				if spec.ID != id {
					return usageError(fmt.Sprintf("the file's id %q differs from %q", spec.ID, id))
				}
			} else {
				if f.unit == "" || f.amount == "" || f.window == "" {
					return usageError("a budget needs --unit, --amount and --window", "Example: aiplex budgets create platform --unit cost --currency USD --amount 250.00 --window month --group platform-eng")
				}
				w, err := parseBudgetWindow(f.window, f.timezone)
				if err != nil {
					return err
				}
				spec = budgets.Budget{ID: id, DisplayName: f.name, Parent: f.parent, Limit: budgets.Limit{Unit: f.unit, Currency: f.currency, Amount: f.amount}, Window: w, OnUnknownCost: f.onUnknown, Thresholds: f.thresholds}
			}
			c := newClient()
			if file == "" {
				if err := f.addMembers(cmd.Context(), c, &spec.Members, f.groups, f.users, f.agents, f.clients, f.accessPoints, f.projects); err != nil {
					return err
				}
			}
			var expected uint64
			existing, err := c.GetBudget(cmd.Context(), id)
			switch {
			case err == nil:
				if !f.update {
					return newCLIError(exitConflict, fmt.Sprintf("budget %q already exists (revision %d)", id, existing.Revision),
						"Replace it with the same command and --update", "Or change it: aiplex budgets update "+id+" --amount ...")
				}
				expected = existing.Revision
			case spIsNotFound(err):
			default:
				return fmt.Errorf("read budget: %w", err)
			}
			saved, err := c.PutBudget(cmd.Context(), spec, expected)
			if err != nil {
				return spExplain("save budget", err)
			}
			if spStructured(cmd, saved) {
				return nil
			}
			verb := "Created"
			if expected > 0 {
				verb = "Updated"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s budget %s (revision %d): %s per %s\n", verb, saved.ID, saved.Revision, budgetLimitText(spec.Limit.Unit, spec.Limit.Currency, spec.Limit.Amount), budgetWindowText(spec.Window))
			fmt.Fprintf(cmd.OutOrStdout(), "Next: aiplex budgets preview --access-point <id>    aiplex budgets status %s\n", saved.ID)
			return nil
		},
	}
	f.bind(cmd, true)
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON or YAML specification, or - for stdin (instead of the flags)")
	return cmd
}

func budgetsUpdateCmd() *cobra.Command {
	var f budgetSpecFlags
	var adds, removes []string
	var file string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Change a budget's limit, window, parent or members",
		Long: `Reads the budget, applies the flags you give and saves it with the revision it
read, so a concurrent change is not overwritten. Members are changed with
--add KIND=VALUE and --remove KIND=VALUE, where KIND is group, user, agent,
client, access-point or project. Changing the unit, currency or window starts a
new counter; changing only the amount keeps the spend so far.`,
		Example: `  aiplex budgets update platform --amount 400.00
  aiplex budgets update platform --add group=data-eng --remove project=old
  aiplex budgets update platform -f budget.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			cur, err := c.GetBudget(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("read budget: %w", err)
			}
			spec := cur.Spec
			if file != "" {
				raw, err := spReadSpec(file, cmd.InOrStdin())
				if err != nil {
					return err
				}
				spec = budgets.Budget{}
				if err = spStrictDecode(raw, &spec); err != nil {
					return fmt.Errorf("invalid budget: %w", err)
				}
				if spec.ID != args[0] {
					return usageError(fmt.Sprintf("the file's id %q differs from %q", spec.ID, args[0]))
				}
			}
			fl := cmd.Flags()
			if fl.Changed("name") {
				spec.DisplayName = f.name
			}
			if fl.Changed("parent") {
				spec.Parent = f.parent
			}
			if fl.Changed("unit") {
				spec.Limit.Unit = f.unit
			}
			if fl.Changed("currency") {
				spec.Limit.Currency = f.currency
			}
			if fl.Changed("amount") {
				spec.Limit.Amount = f.amount
			}
			if fl.Changed("on-unknown-cost") {
				spec.OnUnknownCost = f.onUnknown
			}
			if fl.Changed("threshold") {
				spec.Thresholds = f.thresholds
			}
			if fl.Changed("window") || fl.Changed("timezone") {
				window := f.window
				if window == "" {
					window = spec.Window.Unit
					if spec.Window.Kind == budgets.WindowTumbling {
						window = fmt.Sprintf("%dh", spec.Window.Hours)
					}
				}
				zone := f.timezone
				if zone == "" && spec.Window.Kind == budgets.WindowCalendar {
					zone = spec.Window.TimeZone
				}
				if spec.Window, err = parseBudgetWindow(window, zone); err != nil {
					return err
				}
			}
			if err = budgetApplyMembers(cmd.Context(), c, f.addMembers, &spec.Members, adds, removes); err != nil {
				return err
			}
			saved, err := c.PutBudget(cmd.Context(), spec, cur.Revision)
			if err != nil {
				return spExplain("update budget", err)
			}
			if spStructured(cmd, saved) {
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated budget %s (revision %d): %s per %s\n", saved.ID, saved.Revision, budgetLimitText(spec.Limit.Unit, spec.Limit.Currency, spec.Limit.Amount), budgetWindowText(spec.Window))
			return nil
		},
	}
	f.bind(cmd, false)
	cmd.Flags().StringArrayVar(&adds, "add", nil, "Add a member: KIND=VALUE (group, user, agent, client, access-point, project), repeatable")
	cmd.Flags().StringArrayVar(&removes, "remove", nil, "Remove a member: KIND=VALUE, repeatable")
	cmd.Flags().StringVarP(&file, "file", "f", "", "Replace the specification from a JSON or YAML file, or - for stdin")
	return cmd
}

func budgetApplyMembers(ctx context.Context, c *aiplex.Client, add func(context.Context, *aiplex.Client, *budgets.Members, []string, []string, []string, []string, []string, []string) error, m *budgets.Members, adds, removes []string) error {
	split := func(kv string) (string, string, error) {
		kind, value, ok := strings.Cut(kv, "=")
		if !ok || kind == "" || value == "" {
			return "", "", usageError(fmt.Sprintf("%q is not KIND=VALUE", kv), "KIND is group, user, agent, client, access-point or project")
		}
		return kind, value, nil
	}
	var groups, users, agents, clients, aps, projects []string
	for _, kv := range adds {
		kind, value, err := split(kv)
		if err != nil {
			return err
		}
		switch kind {
		case "group":
			groups = append(groups, value)
		case "user":
			users = append(users, value)
		case "agent":
			agents = append(agents, value)
		case "client":
			clients = append(clients, value)
		case "access-point":
			aps = append(aps, value)
		case "project":
			projects = append(projects, value)
		default:
			return usageError(fmt.Sprintf("unknown member kind %q", kind), "KIND is group, user, agent, client, access-point or project")
		}
	}
	if len(removes) > 0 {
		var provider, pool string
		for _, kv := range removes {
			kind, value, err := split(kv)
			if err != nil {
				return err
			}
			switch kind {
			case "group", "user":
				if provider == "" {
					var gerr error
					if provider, pool, gerr = budgetGroupSource(ctx, c); gerr != nil {
						return gerr
					}
				}
			}
			switch kind {
			case "group":
				source := auth.MembershipSource(provider, pool)
				m.Groups = removeGroup(m.Groups, quotaplex.GroupRef{Source: source, ID: value})
			case "user":
				p, err := resolveUserPrincipal(ctx, c, value, provider, pool)
				if err != nil {
					return err
				}
				m.Principals = removeString(m.Principals, p)
			case "agent":
				m.Agents = removeString(m.Agents, value)
			case "client":
				m.ClientRegistrations = removeString(m.ClientRegistrations, value)
			case "access-point":
				m.AccessPoints = removeString(m.AccessPoints, value)
			case "project":
				m.Projects = removeString(m.Projects, value)
			default:
				return usageError(fmt.Sprintf("unknown member kind %q", kind), "KIND is group, user, agent, client, access-point or project")
			}
		}
	}
	return add(ctx, c, m, groups, users, agents, clients, aps, projects)
}

func removeString(list []string, v string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func removeGroup(list []quotaplex.GroupRef, v quotaplex.GroupRef) []quotaplex.GroupRef {
	out := list[:0:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func budgetsDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"remove", "rm"},
		Short:   "Delete a budget",
		Long:    "Stops the budget from being enforced. Its counters stay in the quota store until their windows end, so recreating it with the same unit and window resumes them. A budget with children cannot be deleted.",
		Example: "  aiplex budgets delete platform --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			cur, err := c.GetBudget(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("read budget: %w", err)
			}
			if err := confirm(cmd, yes, "Delete budget "+args[0]+"? Its limit stops applying to its members."); err != nil {
				return err
			}
			if err = c.DeleteBudget(cmd.Context(), args[0], cur.Revision); err != nil {
				return spExplain("delete budget", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted budget %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
	return cmd
}

func budgetsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <id>",
		Short: "Show spent, reserved and remaining for the current window",
		Long: `Members of a budget see its spent, reserved and remaining amounts. Operators also
see unknown-cost requests, threshold events, each child budget and, with the
admin role, the top spenders by principal from the usage ledger.`,
		Example: "  aiplex budgets status platform\n  aiplex budgets status platform -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := newClient().GetBudgetStatus(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("budget status: %w", err)
			}
			if spStructured(cmd, st) {
				return nil
			}
			renderBudgetStatus(cmd.OutOrStdout(), st)
			return nil
		},
	}
}

func renderBudgetStatus(out io.Writer, st *aiplex.BudgetStatus) {
	name := st.ID
	if st.DisplayName != "" {
		name = fmt.Sprintf("%s (%s)", st.DisplayName, st.ID)
	}
	unit := st.Unit
	if st.Currency != "" {
		unit = st.Currency
	}
	fmt.Fprintf(out, "Budget %s\n  Window:    %s to %s\n", name, st.Window.Start.UTC().Format("2006-01-02 15:04Z"), st.Window.End.UTC().Format("2006-01-02 15:04Z"))
	fmt.Fprintf(out, "  Limit:     %s %s\n  Spent:     %s\n  Reserved:  %s (held by requests in flight)\n  Remaining: %s\n", st.Limit, unit, st.Spent, st.Reserved, st.Remaining)
	if st.OverLimit {
		fmt.Fprintln(out, "  Over limit: a settled request used more than its reservation; new requests are refused")
	}
	if st.UnknownCostRequests != nil {
		fmt.Fprintf(out, "  Unknown cost: %d request(s)", *st.UnknownCostRequests)
		if st.OnUnknownCost != "" {
			fmt.Fprintf(out, " (on_unknown_cost %s)", st.OnUnknownCost)
		}
		fmt.Fprintln(out)
	}
	if len(st.ThresholdEvents) > 0 {
		fmt.Fprintln(out, "\nThresholds crossed")
		rows := [][]string{}
		for _, e := range st.ThresholdEvents {
			rows = append(rows, []string{strconv.Itoa(e.Percent) + "%", e.At.UTC().Format("2006-01-02 15:04Z"), e.Consumed})
		}
		spTable(out, []string{"THRESHOLD", "WHEN", "CONSUMED"}, rows)
	}
	if len(st.Children) > 0 {
		fmt.Fprintln(out, "\nChild budgets")
		rows := [][]string{}
		for _, ch := range st.Children {
			rows = append(rows, []string{ch.ID, spDash(ch.DisplayName), ch.Limit, ch.Spent, ch.Remaining})
		}
		spTable(out, []string{"ID", "NAME", "LIMIT", "SPENT", "REMAINING"}, rows)
	}
	if len(st.TopSpenders) > 0 {
		fmt.Fprintln(out, "\nTop spenders (usage ledger)")
		rows := [][]string{}
		for _, s := range st.TopSpenders {
			who := s.PrincipalID
			if s.Display != "" {
				who = s.Display
			}
			cost := "-"
			if len(s.Cost) > 0 {
				parts := []string{}
				for _, c := range s.Cost {
					parts = append(parts, aiplex.FormatCostTotal(c))
				}
				cost = strings.Join(parts, "; ")
			}
			if s.UnknownCostRecords > 0 {
				cost += fmt.Sprintf(" +%d unknown", s.UnknownCostRecords)
			}
			rows = append(rows, []string{who, strconv.FormatInt(s.Requests, 10), cost})
		}
		spTable(out, []string{"PRINCIPAL", "REQUESTS", "COST"}, rows)
	}
	for _, n := range st.Notes {
		fmt.Fprintf(out, "\nNote: %s", n)
	}
	if len(st.Notes) > 0 {
		fmt.Fprintln(out)
	}
}

func budgetsPreviewCmd() *cobra.Command {
	var (
		groups, agents []string
		user, client   string
		accessPoint    string
		plane          string
		draft          string
	)
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Which budgets would a request draw from, and why (admin; changes nothing)",
		Long: `Answers, for a hypothetical caller and access point, which budgets a request would
draw from, whether each matches the caller directly or only through a child, and
which selector matched. With -f a draft budget is validated as if saved and
included, so you can review a change before applying it with create or update.

The caller is hypothetical: nothing in it is verified. A real request uses the
groups of its verified token.`,
		Example: "  aiplex budgets preview --group platform-eng --access-point ide\n  aiplex budgets preview --user alice@localhost --plane mcplex\n  aiplex budgets preview --group platform-eng -f new-budget.yaml",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			req := aiplex.BudgetPreviewRequest{AccessPoint: accessPoint, Plane: plane}
			var members budgets.Members
			var userList []string
			if user != "" {
				userList = []string{user}
			}
			if err := (&budgetSpecFlags{}).addMembers(cmd.Context(), c, &members, groups, userList, nil, nil, nil, nil); err != nil {
				return err
			}
			req.Subject.Groups = members.Groups
			if len(members.Principals) > 0 {
				req.Subject.Principal = members.Principals[0]
			}
			if len(agents) > 0 {
				req.Subject.Agent = agents[0]
			}
			req.Subject.Client = client
			if draft != "" {
				raw, err := spReadSpec(draft, cmd.InOrStdin())
				if err != nil {
					return err
				}
				var b budgets.Budget
				if err = spStrictDecode(raw, &b); err != nil {
					return fmt.Errorf("invalid draft budget: %w", err)
				}
				req.Draft = &b
			}
			p, err := c.PreviewBudgets(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("budget preview: %w", err)
			}
			if spStructured(cmd, p) {
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Preview for plane %s", p.Plane)
			if p.AccessPoint != "" {
				fmt.Fprintf(out, ", access point %s", p.AccessPoint)
				if p.Project != "" {
					fmt.Fprintf(out, " (project %s)", p.Project)
				}
			}
			fmt.Fprintln(out)
			if p.Draft != nil {
				if p.Draft.Valid {
					fmt.Fprintln(out, "Draft: valid")
				} else {
					fmt.Fprintf(out, "Draft: INVALID (%s): %s\n", spDash(p.Draft.Field), p.Draft.Error)
				}
			}
			if len(p.Draws) == 0 {
				fmt.Fprintln(out, "\nThis request would draw from no budget.")
			} else {
				fmt.Fprintln(out, "\nWould draw from")
				t := newTable(keyCol("BUDGET"), col("NAME"), col("LIMIT"), col("WINDOW"), col("MATCH"), col("WHY"))
				for _, d := range p.Draws {
					match, why := "direct", strings.Join(d.Reasons, ",")
					if !d.Direct {
						match, why = "parent", "via "+d.ViaChild
					}
					id := d.ID
					if d.Draft {
						id += " (draft)"
					}
					t.add(id, spDash(d.DisplayName), budgetLimitText(d.Unit, d.Currency, d.Limit), budgetWindowText(d.Window), match, spDash(why))
				}
				t.render(out)
			}
			for _, s := range p.Skipped {
				fmt.Fprintf(out, "\nSkipped %s: %s\n", s.ID, s.Reason)
			}
			for _, n := range p.Notes {
				fmt.Fprintf(out, "\nNote: %s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&groups, "group", nil, "Caller's group name, repeatable")
	cmd.Flags().StringVar(&user, "user", "", "Caller's user (principal id or name@localhost)")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Caller's agent ID")
	cmd.Flags().StringVar(&client, "client", "", "Caller's client registration ID")
	cmd.Flags().StringVar(&accessPoint, "access-point", "", "Access point the request enters through (its project label applies)")
	cmd.Flags().StringVar(&plane, "plane", "", "llmplex (default), mcplex or a2aplex")
	cmd.Flags().StringVarP(&draft, "file", "f", "", "A draft budget (JSON or YAML) to validate and include; not saved")
	return cmd
}

// spRenderBudgets prints the budgets block of a subject or access point.
func spRenderBudgets(out io.Writer, b aiplex.OverviewBudgets) {
	fmt.Fprintln(out, "\nBudgets")
	if len(b.Applicable) == 0 {
		fmt.Fprintln(out, "  none apply")
	} else {
		t := newTable(keyCol("BUDGET"), col("LIMIT"), col("SPENT"), col("REMAINING"), col("WHY"))
		for _, item := range b.Applicable {
			spent, remaining := spUnknown, spUnknown
			if item.Counters == "available" {
				spent, remaining = item.Spent, item.Remaining
			}
			why := strings.Join(item.MatchedBy, ",")
			if len(item.ViaAccessPoints) > 0 {
				why = strings.TrimPrefix(why+",access point "+strings.Join(item.ViaAccessPoints, "+"), ",")
			}
			if !item.Direct && item.ViaChild != "" {
				why = "parent of " + item.ViaChild
			}
			t.add(item.ID, budgetLimitText(item.Unit, item.Currency, item.Limit), spent, remaining, spDash(why))
		}
		t.render(out)
	}
	if b.Note != "" {
		fmt.Fprintf(out, "  Note: %s\n", b.Note)
	}
}
