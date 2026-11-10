package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// spUnknown marks a value the platform does not know. It is never shown as 0.
const spUnknown = "unknown"

// spStructured writes JSON or YAML when -o asks for it and reports whether it
// did. YAML is produced from the JSON form so both formats carry the same
// field names. Output goes to the command's writer.
func spStructured(cmd *cobra.Command, value any) bool {
	switch output {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		_ = enc.Encode(value)
		return true
	case "yaml":
		raw, err := json.Marshal(value)
		if err == nil {
			var generic any
			if err = json.Unmarshal(raw, &generic); err == nil {
				enc := yaml.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent(2)
				_ = enc.Encode(generic)
				_ = enc.Close()
				return true
			}
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Error: render yaml:", err)
		return true
	}
	return false
}

func spTable(out io.Writer, headers []string, rows [][]string) {
	tableFrom(headers, rows).render(out)
}

func spDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func spJoin(values []string) string { return spDash(strings.Join(values, ",")) }

// spReadSpec reads a JSON or YAML document from a file or stdin ("-").
func spReadSpec(path string, stdin io.Reader) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("-f <file> is required (use '-' for stdin)")
	}
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(io.LimitReader(stdin, 1<<20+1))
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("%s exceeds 1 MiB", path)
	}
	// YAML is a superset of JSON; normalise to JSON so decoding is strict and
	// uses the API's field names.
	var generic any
	if err = yaml.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	generic = spJSONCompatible(generic)
	out, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}

// spJSONCompatible converts yaml.v3 map[any]any values to map[string]any.
func spJSONCompatible(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = spJSONCompatible(e)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[fmt.Sprint(k)] = spJSONCompatible(e)
		}
		return m
	case []any:
		for i, e := range t {
			t[i] = spJSONCompatible(e)
		}
		return t
	}
	return v
}

func spStrictDecode(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected trailing content")
	}
	return nil
}

func accessPointsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "access-points",
		Aliases: []string{"access-point", "ap"},
		Short:   "Access points: one URL that enforces identity, access, QoS and quota",
		Long: `An access point is one stable URL (/ap/<id>/...) in front of models, tools and
skills. It decides which kinds of callers (agents, users, clients) may use it,
and carries a QoS class that is rendered into quota controls.

  aiplex access-points create ide --model gpt-4o-mini=openai --tool search --allow-groups eng
  aiplex access-points apply -f ide.yaml
  aiplex access-points quota-plan ide
  aiplex connect ide --client claude-code`,
		Example:     "  aiplex access-points list\n  aiplex access-points create team --model mock-gpt=local-models --tool local-tools",
		Annotations: map[string]string{"kind": "access point"},
	}
	cmd.AddCommand(spAPListCmd(), spAPGetCmd(), spAPCreateCmd(), spAPApplyCmd(), spAPDeleteCmd(), spAPQuotaPlanCmd())
	registerLister("access-points", func(ctx context.Context, c *aiplex.Client) ([]string, error) {
		list, err := c.ListAccessPoints(ctx)
		ids := make([]string, 0, len(list))
		for _, r := range list {
			ids = append(ids, r.ID)
		}
		return ids, err
	})
	return cmd
}

func spAPQoS(ap aiplex.AccessPoint) string {
	if ap.QoS == nil {
		return "-"
	}
	return ap.QoS.Class
}

func spAPListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List access points (administrators see all; others see the ones that admit them)",
		Example: "  aiplex access-points list\n  aiplex access-points list -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := newClient().ListAccessPoints(cmd.Context())
			if err != nil {
				return fmt.Errorf("list access points: %w", err)
			}
			if spStructured(cmd, list) {
				return nil
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No access points. Create one with: aiplex access-points create <id> --model <name=surface> --tool <connection>")
				return nil
			}
			filtered := true
			for _, r := range list {
				filtered = filtered && r.Revision == 0
			}
			if filtered {
				// A caller without an operator role sees only the access points
				// that admit it, without their configuration.
				base := resolveAPIURL()
				t := newTable(keyCol("ID"), col("URL"))
				for _, r := range list {
					t.add(r.ID, base+"/ap/"+r.ID)
				}
				t.render(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), "\nThese are the access points that admit you. Client configuration: aiplex connect <id>")
				return nil
			}
			rows := make([][]string, 0, len(list))
			for _, r := range list {
				s := r.Spec
				rows = append(rows, []string{r.ID, spDash(s.DisplayName), spJoin(s.CallerKinds), strconv.Itoa(len(s.Models)), strconv.Itoa(len(s.Tools)), strconv.Itoa(len(s.Skills)), spAPQoS(s), strconv.FormatUint(r.Revision, 10)})
			}
			spTable(cmd.OutOrStdout(), []string{"ID", "NAME", "CALLERS", "MODELS", "TOOLS", "SKILLS", "QOS", "REV"}, rows)
			return nil
		},
	}
}

func spAPGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Aliases: []string{"show", "describe"},
		Short:   "Show one access point",
		Example: "  aiplex access-points get local\n  aiplex access-points get local -o yaml > ap.yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newClient().GetAccessPoint(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get access point: %w", err)
			}
			if spStructured(cmd, r) {
				return nil
			}
			out, s := cmd.OutOrStdout(), r.Spec
			fmt.Fprintf(out, "Access point %s (revision %d)\n", r.ID, r.Revision)
			if s.DisplayName != "" {
				fmt.Fprintf(out, "  Name:      %s\n", s.DisplayName)
			}
			fmt.Fprintf(out, "  Callers:   %s\n", spJoin(s.CallerKinds))
			sub := s.Subjects
			if len(sub.Principals)+len(sub.Agents)+len(sub.Groups)+len(sub.ClientRegistrations) == 0 {
				fmt.Fprintln(out, "  Subjects:  every caller of an allowed kind")
			} else {
				fmt.Fprintf(out, "  Subjects:  %d principals, %d agents, %d groups, %d client registrations\n", len(sub.Principals), len(sub.Agents), len(sub.Groups), len(sub.ClientRegistrations))
			}
			for _, m := range s.Models {
				fmt.Fprintf(out, "  Model:     %s -> %s on %s\n", m.Name, m.NativeModel, m.SurfaceRef)
			}
			for _, t := range s.Tools {
				expose := "all permitted tools"
				if len(t.Expose) > 0 {
					expose = strings.Join(t.Expose, ",")
				}
				fmt.Fprintf(out, "  Tools:     %s (%s)\n", t.ConnectionRef, expose)
			}
			for _, k := range s.Skills {
				fmt.Fprintf(out, "  Skills:    %s\n", k.InstanceRef)
			}
			if s.QoS != nil {
				fmt.Fprintf(out, "  QoS:       class %s weight %d\n", s.QoS.Class, s.QoS.Weight)
			}
			fmt.Fprintf(out, "Next: aiplex connect %s    aiplex who --kind user\n", r.ID)
			return nil
		},
	}
}

func spAPApplyCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: "Create or replace an access point from a JSON or YAML file",
		Long: `Create the access point named by the file's id, or replace it when it already
exists (the current revision is read first, so a concurrent change is not
overwritten). The file may be the bare specification or the record printed by
'access-points get -o yaml'. Use '-f -' to read stdin.`,
		Example: "  aiplex access-points apply -f ide.yaml\n  aiplex access-points get local -o yaml | aiplex access-points apply -f -",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := spReadSpec(file, cmd.InOrStdin())
			if err != nil {
				return err
			}
			spec, err := spDecodeAccessPoint(raw)
			if err != nil {
				return fmt.Errorf("invalid access point: %w", err)
			}
			c := newClient()
			var expected uint64
			existing, err := c.GetAccessPoint(cmd.Context(), spec.ID)
			switch {
			case err == nil:
				expected = existing.Revision
			case spIsNotFound(err):
			default:
				return fmt.Errorf("read access point: %w", err)
			}
			saved, err := c.PutAccessPoint(cmd.Context(), spec, expected)
			if err != nil {
				return spExplain("apply access point", err)
			}
			if spStructured(cmd, saved) {
				return nil
			}
			verb := "Created"
			if expected > 0 {
				verb = "Updated"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s access point %s (revision %d)\n", verb, saved.ID, saved.Revision)
			if saved.Spec.QoS != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Next: aiplex access-points quota-plan %s\n", saved.ID)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON or YAML file, or - for stdin")
	return cmd
}

// spDecodeAccessPoint accepts a bare specification or a record with "spec".
func spDecodeAccessPoint(raw []byte) (aiplex.AccessPoint, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return aiplex.AccessPoint{}, err
	}
	if spec, ok := probe["spec"]; ok && probe["id"] != nil {
		var envelope struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &envelope)
		raw = spec
		var inner aiplex.AccessPoint
		if err := spStrictDecode(raw, &inner); err != nil {
			return aiplex.AccessPoint{}, err
		}
		if inner.ID != envelope.ID {
			return aiplex.AccessPoint{}, fmt.Errorf("record id %q differs from spec id %q", envelope.ID, inner.ID)
		}
		return inner, nil
	}
	var spec aiplex.AccessPoint
	if err := spStrictDecode(raw, &spec); err != nil {
		return aiplex.AccessPoint{}, err
	}
	if spec.ID == "" {
		return spec, fmt.Errorf("id is required")
	}
	return spec, nil
}

func spIsNotFound(err error) bool {
	var apiErr *aiplex.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == 404
}

func spExplain(action string, err error) error {
	var apiErr *aiplex.Error
	if errors.As(err, &apiErr) && (apiErr.Code == "REVISION_CONFLICT" || apiErr.StatusCode == 409) {
		return fmt.Errorf("%s: %w\n  The resource changed or already exists; re-run to pick up the latest revision.", action, err)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func spAPDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"remove", "rm"},
		Short:   "Delete an access point",
		Example: "  aiplex access-points delete team --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			current, err := c.GetAccessPoint(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("read access point: %w", err)
			}
			if err := confirm(cmd, yes, "Delete access point "+args[0]+"? Clients using /ap/"+args[0]+" stop working."); err != nil {
				return err
			}
			if err = c.DeleteAccessPoint(cmd.Context(), args[0], current.Revision); err != nil {
				return spExplain("delete access point", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted access point %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
	return cmd
}

func spAPQuotaPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "quota-plan <id>",
		Short:   "Render the access point's QoS block as quota controls for review",
		Long:    "Shows the quota controls and scheduler flow that the access point's qos block would add. Nothing is applied.",
		Example: "  aiplex access-points quota-plan ide",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := newClient().PlanAccessPointQuota(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("quota plan: %w", err)
			}
			if spStructured(cmd, plan) {
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Quota plan for access point %s (not applied)\n", plan.AccessPointID)
			state := "valid"
			if !plan.Validation.Valid {
				state = "INVALID: " + plan.Validation.Error
			}
			fmt.Fprintf(out, "  Validation: %s (%s)\n", state, plan.Validation.Validator)
			for _, r := range plan.Fragment.Rates {
				fmt.Fprintf(out, "  Rate:        %s meter=%s %d per %s burst %d\n", r.ID, r.MeterID, r.Rate, r.Interval, r.Burst)
			}
			for _, r := range plan.Fragment.Concurrency {
				fmt.Fprintf(out, "  Concurrency: %s limit %d\n", r.ID, r.Limit)
			}
			for _, b := range plan.Fragment.BudgetRefs {
				fmt.Fprintf(out, "  Budget:      %s (existing, unchanged)\n", b.ID)
			}
			if plan.Scheduler != nil {
				fmt.Fprintf(out, "  Scheduler:   class %s flow %s weight %d (%d routes)\n", plan.Scheduler.Class, plan.Scheduler.FlowID, plan.Scheduler.Weight, len(plan.Scheduler.Routes))
			}
			for _, w := range plan.Warnings {
				fmt.Fprintf(out, "  Warning: %s\n", w)
			}
			return nil
		},
	}
}

// ---- connect --------------------------------------------------------------

func connectCmd() *cobra.Command {
	var client string
	var wf connectWriteFlags
	cmd := &cobra.Command{
		Use:   "connect <access-point>",
		Short: "Show, or write, the configuration that points a client at an access point",
		Long: `Shows the base URLs, MCP and skills URLs, how to obtain a token, and snippets
for a client. --client takes a client profile (for example claude-code, codex,
antigravity) or "generic" (the default) for environment variables and curl.

Snippets read the token from $AIPLEX_TOKEN; no token or secret is printed.

With --write, the configuration is merged into the client's own config file
instead of only being printed. Supported for claude-code (settings.json and the
MCP server list), codex (config.toml) and gemini-cli (settings.json). Each file
is shown as a diff and needs confirmation (--yes without a terminal). A copy of
the original goes to ~/.aiplex/connect-backups first, existing keys are never
replaced wholesale, and a file that is not valid JSON or TOML is refused rather
than overwritten. A token value is never written: files refer to $AIPLEX_TOKEN,
or, with --token-helper, to "aiplex --context NAME auth print-token" for the context in
effect (which must have a stored token, since the client runs the helper later). Parts a file cannot hold
(an environment variable for the shell) are printed as remaining steps.
antigravity has no documented config file location, so it stays print-only.

--undo restores the most recent backup for --client (or the most recent one).
--dry-run shows the diff and writes nothing. aiplex does not install the client.`,
		Example: `  aiplex connect ide
  aiplex connect ide --client claude-code
  aiplex connect local --client claude-code --write
  aiplex connect local --client codex --write --dry-run
  aiplex connect local --client gemini-cli --write --with-telemetry http://localhost:4318
  aiplex connect --undo --client claude-code
  aiplex connect ide -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wf.undo {
				return runConnectUndo(cmd, client, &wf)
			}
			if len(args) != 1 {
				return usageError("connect needs an access point ID", "List the ones you can use with: aiplex access-points list")
			}
			if wf.write || wf.dryRun {
				return runConnectWrite(cmd, args[0], client, &wf)
			}
			if err := wf.rejectWriteOnly(cmd); err != nil {
				return err
			}
			apiClient := client
			if client == "gemini-cli" {
				apiClient = "generic"
			}
			c, err := newClient().ConnectAccessPoint(cmd.Context(), args[0], apiClient)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			c.Client = client
			if spStructured(cmd, c) {
				return nil
			}
			spRenderConnect(cmd.OutOrStdout(), c)
			if client == "gemini-cli" {
				fmt.Fprintln(cmd.OutOrStdout(), "\nGemini CLI: use the genai base URL as GOOGLE_GEMINI_BASE_URL and $AIPLEX_TOKEN as GEMINI_API_KEY.")
			}
			if hint := connectWriteHint(args[0], client); hint != "" {
				fmt.Fprintln(cmd.OutOrStdout(), "\n"+hint)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&client, "client", "generic", "Client profile ID, gemini-cli, or generic")
	wf.bind(cmd)
	return cmd
}

func spRenderConnect(out io.Writer, c *aiplex.AccessPointConnect) {
	fmt.Fprintf(out, "Connect to access point %s as %s\n\n", c.AccessPointID, c.Client)
	rows := [][]string{}
	for _, family := range []string{"openai", "anthropic", "genai"} {
		if u, ok := c.BaseURLs[family]; ok {
			rows = append(rows, []string{family + " base URL", u})
		}
	}
	// Endpoints that do not fit a single base URL: Bedrock paths, embeddings,
	// moderations, images, audio and WebSocket sessions.
	for _, e := range c.Endpoints {
		label := e.Label
		if label == "" {
			label = e.Protocol
		}
		cell := e.URL
		if len(e.Query) > 0 {
			cell += " (requires ?" + strings.Join(e.Query, "&?") + "=…)"
		}
		rows = append(rows, []string{fmt.Sprintf("%s [%s, %s]", label, e.Protocol, e.Transport), cell})
	}
	if c.MCPURL != "" {
		rows = append(rows, []string{"MCP URL", c.MCPURL}, []string{"MCP catalog", c.MCPCatalogURL})
	}
	if c.SkillsURL != "" {
		rows = append(rows, []string{"Skills", c.SkillsURL})
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "This access point offers no model, tool or skill endpoint to connect to.")
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\n", r[0], r[1])
	}
	_ = w.Flush()
	fmt.Fprintf(out, "\nAuthentication (%s, token from $%s)\n", c.Auth.Scheme, c.Auth.TokenEnv)
	for i, s := range c.Auth.Steps {
		fmt.Fprintf(out, "  %d. %s\n", i+1, s)
	}
	for _, s := range c.Snippets {
		fmt.Fprintf(out, "\n%s [%s]\n", s.Title, s.Language)
		for _, line := range strings.Split(strings.TrimRight(s.Content, "\n"), "\n") {
			fmt.Fprintf(out, "    %s\n", line)
		}
	}
	if len(c.Notes) > 0 {
		fmt.Fprintln(out, "\nNotes")
		for _, n := range c.Notes {
			fmt.Fprintf(out, "  - %s\n", n)
		}
	}
}

// spCostCell renders cost totals; "unknown" when nothing priced the usage.
func spCostCell(u *aiplex.OverviewUsage) string {
	if u == nil {
		return spUnknown
	}
	parts := make([]string, 0, len(u.Cost)+1)
	for _, c := range u.Cost {
		parts = append(parts, aiplex.FormatCostTotal(usageledger.CostTotal{Currency: c.Currency, Basis: c.Basis, Amount: c.Amount, Scale: c.Scale}))
	}
	if u.UnknownCostRecords > 0 {
		if len(parts) == 0 {
			return spUnknown
		}
		parts = append(parts, fmt.Sprintf("+%d unknown", u.UnknownCostRecords))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "; ")
}

// spTokensCell renders one token counter: unknown when never reported, and
// annotated when only some records reported it.
func spTokensCell(u *aiplex.OverviewUsage, name string) string {
	if u == nil {
		return spUnknown
	}
	sum, reported := u.Tokens[name]
	missing := u.TokensUnknown[name]
	switch {
	case !reported && missing == 0 && u.Records == 0:
		return "-"
	case !reported:
		return spUnknown
	case missing > 0:
		return fmt.Sprintf("%d (+%d unknown)", sum, missing)
	}
	return strconv.FormatInt(sum, 10)
}
