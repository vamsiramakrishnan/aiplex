package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// Model surfaces, provider descriptors, capacity and batches. Helpers carry
// the llmx prefix so they cannot collide with other files in package main.

const llmxUnknown = "unknown"

// llmxPrint writes v as JSON, or as YAML with the JSON field names.
func llmxPrint(v any) {
	if output != "yaml" {
		printJSON(v)
		return
	}
	raw, err := json.Marshal(v)
	if err != nil {
		printJSON(v)
		return
	}
	var generic any
	if json.Unmarshal(raw, &generic) != nil {
		printJSON(v)
		return
	}
	printYAML(generic)
}

func llmxMachine() bool { return output == "json" || output == "yaml" }

// llmxReadSpec reads a surface file in JSON or YAML. It accepts the bare spec
// or the record `llm surfaces get -o json` prints ({"id","revision","spec"}).
func llmxReadSpec(r io.Reader) (aiplex.ModelSurfaceSpec, error) {
	var spec aiplex.ModelSurfaceSpec
	raw, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return spec, fmt.Errorf("surface file exceeds 1MiB")
	}
	var generic map[string]any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return spec, fmt.Errorf("surface file is neither JSON nor YAML: %w", err)
	}
	if inner, ok := generic["spec"].(map[string]any); ok && generic["surface"] == nil {
		generic = inner
	}
	if generic == nil {
		return spec, fmt.Errorf("surface file is empty")
	}
	encoded, err := json.Marshal(generic)
	if err != nil {
		return spec, fmt.Errorf("surface file is not representable as JSON: %w", err)
	}
	if err := json.Unmarshal(encoded, &spec); err != nil {
		return spec, fmt.Errorf("surface file: %w", err)
	}
	if spec.Surface.ID == "" {
		return spec, fmt.Errorf("surface.id is required")
	}
	return spec, nil
}

func llmxAuthCell(s aiplex.ModelSurfaceSpec) string {
	switch {
	case s.Auth != nil && s.Auth.SecretRef != "":
		return "secret-ref:" + s.Auth.SecretRef
	case s.Auth != nil:
		return "auth:" + s.Auth.Type
	case s.IdentityRef != "":
		return "identity-ref:" + s.IdentityRef
	}
	return "—"
}

func llmxProviderCell(s aiplex.ModelSurfaceSpec) string {
	if s.Surface.Provider != nil {
		return s.Surface.Provider.ID
	}
	return "google"
}

func llmxSurfaceRows(records []aiplex.ModelSurfaceRecord) ([]string, [][]string) {
	rows := make([][]string, 0, len(records))
	for _, r := range records {
		capacity := "off"
		if r.Spec.CapacityPolicy != nil {
			capacity = "on"
		}
		rows = append(rows, []string{r.ID, llmxProviderCell(r.Spec), r.Spec.Surface.Protocol, strconv.Itoa(len(r.Spec.Surface.Operations)), strconv.Itoa(len(r.Spec.Surface.Models)), llmxAuthCell(r.Spec), capacity, strconv.FormatUint(r.Revision, 10)})
	}
	return []string{"ID", "PROVIDER", "PROTOCOL", "OPERATIONS", "MODELS", "AUTH (reference)", "CAPACITY POLICY", "REVISION"}, rows
}

func llmxSurfacesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "surfaces",
		Short: "Manage model surfaces (provider endpoints, operations, auth references)",
		Long: `A model surface binds a provider (OpenAI, Anthropic, Azure OpenAI, Bedrock,
OpenAI-compatible, Gemini Developer) or a Google Cloud endpoint to the operations
and models callers may use. Credentials are references (a secret reference or an
identity reference), never values. Surfaces are revisioned: replace and delete
check the revision so concurrent edits are never silently overwritten.`,
	}
	cmd.AddCommand(llmxSurfacesListCmd(), llmxSurfacesGetCmd(), llmxSurfacesApplyCmd(), llmxSurfacesDeleteCmd())
	return cmd
}

func llmxSurfacesListCmd() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List model surfaces", Example: "  aiplex llm surfaces list", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			records, err := newClient().ListModelSurfaces(cmd.Context())
			if err != nil {
				return fmt.Errorf("list surfaces: %w", err)
			}
			if llmxMachine() {
				if records == nil {
					records = []aiplex.ModelSurfaceRecord{}
				}
				llmxPrint(llmxRecordsJSON(records))
				return nil
			}
			if len(records) == 0 {
				fmt.Println("No model surfaces. Create one with: aiplex llm surfaces apply -f surface.yaml")
				return nil
			}
			headers, rows := llmxSurfaceRows(records)
			printTable(headers, rows)
			return nil
		},
	}
}

type llmxRecordJSON struct {
	ID       string                  `json:"id"`
	Revision uint64                  `json:"revision"`
	Spec     aiplex.ModelSurfaceSpec `json:"spec"`
}

func llmxRecordsJSON(records []aiplex.ModelSurfaceRecord) []llmxRecordJSON {
	out := make([]llmxRecordJSON, 0, len(records))
	for _, r := range records {
		out = append(out, llmxRecordJSON{r.ID, r.Revision, r.Spec})
	}
	return out
}

func llmxSurfacesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use: "get <id>", Short: "Show one surface and its revision", Example: "  aiplex llm surfaces get openai-prod -o yaml", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newClient().GetModelSurface(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get surface: %w", err)
			}
			if output == "yaml" {
				llmxPrint(llmxRecordJSON{r.ID, r.Revision, r.Spec})
			} else {
				printJSON(llmxRecordJSON{r.ID, r.Revision, r.Spec})
			}
			return nil
		},
	}
}

func llmxSurfacesApplyCmd() *cobra.Command {
	var file string
	var revision uint64
	var haveRevision bool
	cmd := &cobra.Command{
		Use:   "apply -f <surface.yaml|json>",
		Short: "Create a surface, or replace it with the file's content",
		Long: `Reads a surface specification (JSON or YAML) and writes it under surface.id.
When the surface exists it is replaced at the revision read just before the
write; pass --revision to require a specific revision instead. A stale revision
fails with a conflict and changes nothing. The server validates the whole
specification (provider block, operations, websocket and capacity_policy).
"-f -" reads standard input.`,
		Example: "  aiplex llm surfaces apply -f openai-prod.yaml\n  aiplex llm surfaces apply -f openai-prod.yaml --revision 3",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return fmt.Errorf("-f/--file is required")
			}
			var in io.Reader = os.Stdin
			if file != "-" {
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer f.Close()
				in = f
			}
			spec, err := llmxReadSpec(in)
			if err != nil {
				return err
			}
			haveRevision = cmd.Flags().Changed("revision")
			saved, err := llmxApply(cmd.Context(), newClient(), spec, revision, haveRevision)
			if err != nil {
				return err
			}
			if llmxMachine() {
				llmxPrint(llmxRecordJSON{saved.ID, saved.Revision, saved.Spec})
				return nil
			}
			fmt.Printf("Applied model surface %s (revision %d).\n", saved.ID, saved.Revision)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Surface specification file (JSON or YAML)")
	cmd.Flags().Uint64Var(&revision, "revision", 0, "Require this current revision (0 requires that the surface does not exist)")
	return cmd
}

func llmxIsNotFound(err error) bool {
	var apiErr *aiplex.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == 404
}

func llmxApply(ctx context.Context, c *aiplex.Client, spec aiplex.ModelSurfaceSpec, revision uint64, explicit bool) (*aiplex.ModelSurfaceRecord, error) {
	id := spec.Surface.ID
	if !explicit {
		current, err := c.GetModelSurface(ctx, id)
		switch {
		case err == nil:
			revision = current.Revision
		case llmxIsNotFound(err):
			revision = 0
		default:
			return nil, fmt.Errorf("apply surface: %w", err)
		}
	}
	saved, err := c.PutModelSurface(ctx, id, spec, revision)
	if err != nil {
		if aiplex.IsRevisionConflict(err) {
			return nil, fmt.Errorf("apply surface: %q changed (or already exists) since revision %d; run `aiplex llm surfaces get %s` and retry: %w", id, revision, id, err)
		}
		return nil, fmt.Errorf("apply surface: %w", err)
	}
	return saved, nil
}

func llmxSurfacesDeleteCmd() *cobra.Command {
	var revision uint64
	cmd := &cobra.Command{
		Use: "delete <id>", Short: "Delete a surface (at its current revision unless --revision is given)", Example: "  aiplex llm surfaces delete openai-prod --revision 4", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if revision == 0 {
				current, err := c.GetModelSurface(cmd.Context(), args[0])
				if err != nil {
					return fmt.Errorf("delete surface: %w", err)
				}
				revision = current.Revision
			}
			if err := c.DeleteModelSurface(cmd.Context(), args[0], revision); err != nil {
				if aiplex.IsRevisionConflict(err) {
					return fmt.Errorf("delete surface: %q changed since revision %d; run `aiplex llm surfaces get %s` and retry: %w", args[0], revision, args[0], err)
				}
				return fmt.Errorf("delete surface: %w", err)
			}
			fmt.Printf("Deleted model surface %s (revision %d).\n", args[0], revision)
			return nil
		},
	}
	cmd.Flags().Uint64Var(&revision, "revision", 0, "Expected revision")
	return cmd
}

// ---- provider descriptors ---------------------------------------------------

func llmxProviderDescriptorsCmd() *cobra.Command {
	var configured bool
	cmd := &cobra.Command{
		Use:   "providers [provider]",
		Short: "List model providers and their operations (from the server's catalog)",
		Long: `Shows each supported provider with its protocols, credential schemes and
operations, as the server defines them. Surfaces must list operations exactly as
this catalog does. With a provider id, lists that provider's operations.
--configured lists the legacy provider route configuration instead.`,
		Example: "  aiplex llm providers\n  aiplex llm providers bedrock",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if configured {
				return llmxConfiguredProviders(cmd, c)
			}
			ids := aiplex.ModelProviderIDs
			if len(args) == 1 {
				ids = []string{args[0]}
			}
			var descriptors []aiplex.ModelProviderDescriptor
			for _, id := range ids {
				d, err := c.ListModelOperations(cmd.Context(), id)
				if err != nil {
					return fmt.Errorf("provider %s: %w", id, err)
				}
				descriptors = append(descriptors, *d)
			}
			if llmxMachine() {
				if len(args) == 1 {
					llmxPrint(descriptors[0])
				} else {
					llmxPrint(descriptors)
				}
				return nil
			}
			if len(args) == 1 {
				d := descriptors[0]
				fmt.Printf("%s\n  protocols:           %s\n  credential schemes:  %s\n", d.ID, strings.Join(d.Protocols, ", "), llmxSchemes(d))
				if d.DefaultStreamUsage != "" {
					fmt.Printf("  default stream_usage: %s\n", d.DefaultStreamUsage)
				}
				fmt.Println()
				rows := make([][]string, 0, len(d.Operations))
				for _, o := range d.Operations {
					rows = append(rows, []string{o.ID, o.Method, o.Path, string(o.Transport), string(o.Action)})
				}
				printTable([]string{"OPERATION", "METHOD", "PATH", "TRANSPORT", "ACTION"}, rows)
				return nil
			}
			rows := make([][]string, 0, len(descriptors))
			for _, d := range descriptors {
				rows = append(rows, []string{d.ID, strings.Join(d.Protocols, ","), llmxSchemes(d), strconv.Itoa(len(d.Operations))})
			}
			printTable([]string{"PROVIDER", "PROTOCOLS", "CREDENTIAL SCHEMES", "OPERATIONS"}, rows)
			fmt.Println("\nList a provider's operations: aiplex llm providers <provider>")
			return nil
		},
	}
	cmd.Flags().BoolVar(&configured, "configured", false, "List the legacy provider route configuration instead of the catalog")
	return cmd
}

func llmxSchemes(d aiplex.ModelProviderDescriptor) string {
	parts := make([]string, 0, len(d.CredentialSchemes))
	for _, s := range d.CredentialSchemes {
		parts = append(parts, string(s))
	}
	if d.APIKeyHeader != "" {
		parts = append(parts, "header:"+d.APIKeyHeader)
	}
	return strings.Join(parts, ", ")
}

func llmxConfiguredProviders(cmd *cobra.Command, c *aiplex.Client) error {
	providers, err := c.ListProviders(cmd.Context())
	if err != nil {
		return fmt.Errorf("list providers: %w", err)
	}
	if output == "json" {
		printJSON(providers)
		return nil
	}
	var rows [][]string
	for _, p := range providers {
		enabled := "yes"
		if !p.Enabled {
			enabled = "no"
		}
		rows = append(rows, []string{p.Provider, p.DisplayName, enabled, p.Region})
	}
	printTable([]string{"PROVIDER", "NAME", "ENABLED", "REGION"}, rows)
	return nil
}

// ---- capacity ----------------------------------------------------------------

func llmxInt(v *int64) string {
	if v == nil {
		return llmxUnknown
	}
	return strconv.FormatInt(*v, 10)
}

func llmxWindow(w *aiplex.ModelLimitWindow) string {
	if w == nil {
		return "not reported"
	}
	reset := ""
	if w.ResetAt != nil {
		reset = " resets " + w.ResetAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("remaining %s of %s%s", llmxInt(w.Remaining), llmxInt(w.Limit), reset)
}

func llmxCapacityCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capacity <surface>",
		Short: "Show a surface's last observed provider limits and cooldown state (admin)",
		Long: `Shows the last rate-limit observation the server that answered made for this
surface, and the capacity policy and cooldown phase. The observation is advisory
and local to one server replica; the policy is enforced per surface and per
replica. Values the provider did not report are shown as "unknown".`,
		Example: "  aiplex llm capacity openai-prod",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cap, err := newClient().GetSurfaceCapacity(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("surface capacity: %w", err)
			}
			if llmxMachine() {
				llmxPrint(cap)
				return nil
			}
			for _, line := range llmxCapacityLines(cap) {
				fmt.Println(line)
			}
			return nil
		},
	}
}

func llmxCapacityLines(c *aiplex.SurfaceCapacity) []string {
	lines := []string{"surface:   " + c.SurfaceID, "scope:     " + c.Scope}
	if !c.Observed || c.Limits == nil {
		lines = append(lines, "observed:  no provider limit observation on this replica yet")
	} else {
		l := c.Limits
		lines = append(lines, "observed:  "+l.ObservedAt.UTC().Format(time.RFC3339)+" (provider "+l.Provider+")")
		if l.Classification != "" {
			lines = append(lines, "class:     "+l.Classification)
		}
		if l.HTTPStatus != 0 {
			lines = append(lines, "status:    "+strconv.Itoa(l.HTTPStatus))
		}
		if l.RetryAfterMS != nil {
			lines = append(lines, "retry:     after "+(time.Duration(*l.RetryAfterMS)*time.Millisecond).String())
		}
		for _, w := range []struct {
			name string
			win  *aiplex.ModelLimitWindow
		}{{"requests", l.Requests}, {"tokens", l.Tokens}, {"input", l.InputTokens}, {"output", l.OutputTokens}} {
			lines = append(lines, fmt.Sprintf("%-10s %s", w.name+":", llmxWindow(w.win)))
		}
	}
	p := c.CapacityPolicy
	if !p.Enabled || p.Policy == nil {
		return append(lines, "policy:    off (limits are observed but nothing is enforced)")
	}
	pol := p.Policy
	lines = append(lines, fmt.Sprintf("policy:    cooldown=%t max_cooldown=%ds headroom=%g min_weight=%d default_weight=%d signal_max_age=%ds", pol.CooldownEnabled, pol.MaxCooldownSeconds, pol.HeadroomFraction, pol.MinWeight, pol.DefaultWeight, pol.SignalMaxAgeSeconds))
	if p.Cooldown != nil {
		phase := "cooldown: " + p.Cooldown.Phase
		if p.Cooldown.Until != nil {
			phase += " until " + p.Cooldown.Until.UTC().Format(time.RFC3339)
		}
		if p.Cooldown.RetryAfterMS > 0 {
			phase += " (retry after " + (time.Duration(p.Cooldown.RetryAfterMS) * time.Millisecond).String() + ")"
		}
		lines = append(lines, "state:     "+phase)
	}
	return lines
}

// ---- batches -----------------------------------------------------------------

func llmxBatchesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "batches", Short: "Provider batch jobs created through the gateway"}
	var state, surface, cursor string
	var limit int
	var all bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List batches (admin)",
		Long: `Lists provider batches the gateway created. An OPEN batch holds its admitted
quota exposure and a concurrency slot until its results are fetched through
AIPlex; its usage stays unknown until then.`,
		Example: "  aiplex llm batches list --state open",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := newClient()
			var batches []aiplex.ModelBatch
			next := cursor
			for {
				page, err := c.ListModelBatches(cmd.Context(), aiplex.ModelBatchQuery{State: state, SurfaceID: surface, Limit: limit, Cursor: next})
				if err != nil {
					return fmt.Errorf("list batches: %w", err)
				}
				batches = append(batches, page.Batches...)
				next = page.NextCursor
				if !all || next == "" {
					break
				}
			}
			if batches == nil {
				batches = []aiplex.ModelBatch{}
			}
			if llmxMachine() {
				llmxPrint(aiplex.ModelBatchPage{Batches: batches, NextCursor: next})
				return nil
			}
			if len(batches) == 0 {
				fmt.Println("No batches match.")
				return nil
			}
			printTable(llmxBatchRows(batches))
			if next != "" {
				fmt.Printf("\nMore batches: --cursor %s\n", next)
			}
			if state != "settled" {
				fmt.Println("\nAn open batch holds quota exposure and a concurrency slot until its results are fetched through AIPlex.")
			}
			return nil
		},
	}
	list.Flags().StringVar(&state, "state", "", "open or settled (default all)")
	list.Flags().StringVar(&surface, "surface", "", "Only this surface")
	list.Flags().IntVar(&limit, "limit", 0, "Page size, 1 to 500 (default 100)")
	list.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor value")
	list.Flags().BoolVar(&all, "all", false, "Follow every page")
	cmd.AddCommand(list)
	return cmd
}

func llmxBatchRows(batches []aiplex.ModelBatch) ([]string, [][]string) {
	rows := make([][]string, 0, len(batches))
	for _, b := range batches {
		expected := llmxUnknown
		if b.Expected != nil {
			expected = strconv.FormatInt(*b.Expected, 10)
		}
		status := b.ProviderStatus
		if status == "" {
			status = llmxUnknown
		}
		rows = append(rows, []string{b.SurfaceID, b.Provider, b.BatchID, usageDash(b.AttemptID), b.State, status, expected})
	}
	return []string{"SURFACE", "PROVIDER", "BATCH", "ATTEMPT", "STATE", "PROVIDER STATUS", "ITEMS"}, rows
}
