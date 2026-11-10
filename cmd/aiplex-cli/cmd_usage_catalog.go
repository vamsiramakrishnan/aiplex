package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/pkg/pricecatalog"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// catalogDisclaimer is printed with every catalogue price a person is shown.
const catalogDisclaimer = "Catalogue prices are a third-party snapshot of public list prices: they can be stale or wrong, and discounts or commitments are not reflected. Cost from them is an estimate."

func usagePricesCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Look up list prices in the bundled catalogue and import the ones you choose",
		Long: `The binary carries a pinned snapshot of a third-party price list (LiteLLM's
model_prices_and_context_window.json, MIT licence). Nothing is priced from it
automatically: a model stays unknown until you import its price, which creates
an ordinary price resource marked with its source and snapshot date. The cost
basis stays "estimated": these are list prices, not invoices.`,
		Example: `  aiplex usage prices catalog search claude --provider anthropic
  aiplex usage prices catalog show openai/gpt-4o-mini
  aiplex usage prices catalog import openai/gpt-4o-mini --dry-run`,
	}
	cmd.AddCommand(usageCatalogSearchCmd(), usageCatalogShowCmd(), usageCatalogImportCmd())
	return cmd
}

func loadCatalogue() (*pricecatalog.Catalog, error) {
	c, err := pricecatalog.Embedded()
	if err != nil {
		return nil, fmt.Errorf("price catalogue unavailable: %w", err)
	}
	return c, nil
}

func catalogueStamp(c *pricecatalog.Catalog) string {
	commit := c.Meta.Commit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	return fmt.Sprintf("%s snapshot %s (commit %s)", c.Meta.Catalog, c.Meta.RetrievedAt, commit)
}

func catRate(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

func usageCatalogSearchCmd() *cobra.Command {
	var provider string
	var limit int
	cmd := &cobra.Command{
		Use:   "search <text> [--provider p]",
		Short: "Search catalogue models by id",
		Long: `Lists catalogue models whose <provider>/<model> contains every word of the
text (case-insensitive substring match only). Prices are USD per million tokens.
Providers: ` + strings.Join(pricecatalog.ProviderNames(), ", ") + `.`,
		Example: `  aiplex usage prices catalog search gpt-4o
  aiplex usage prices catalog search claude --provider anthropic
  aiplex usage prices catalog search "gemini flash" -o json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if provider != "" && pricecatalog.LedgerProvider(provider) == "" {
				return usageError(fmt.Sprintf("unknown catalogue provider %q", provider), "Providers: "+strings.Join(pricecatalog.ProviderNames(), ", "))
			}
			c, err := loadCatalogue()
			if err != nil {
				return err
			}
			found := c.Search(strings.Join(args, " "), provider)
			if usageMachineOutput() {
				usagePrint(map[string]any{"meta": c.Meta, "entries": found})
				return nil
			}
			if len(found) == 0 {
				fmt.Println("No catalogue model matches. A model the catalogue does not list stays unknown; set its price with aiplex usage prices set.")
				return nil
			}
			shown := found
			if limit > 0 && len(shown) > limit {
				shown = shown[:limit]
			}
			t := newTable(keyCol("MODEL"), col("INPUT"), col("OUTPUT"), col("CACHE READ"), wideCol("CACHE WRITE"), wideCol("BATCH IN/OUT"), wideCol("CONTEXT"), col("NOTE"))
			for _, e := range shown {
				note := ""
				if len(e.Blocking) > 0 {
					note = "partial: needs --accept-partial"
				}
				batch := "—"
				if e.Batch != nil {
					batch = catRate(e.Batch.Input) + " / " + catRate(e.Batch.Output)
				}
				ctx := "unknown"
				if e.ContextWindow > 0 {
					ctx = fmt.Sprint(e.ContextWindow)
				}
				t.add(e.ID(), catRate(e.Rates.Input), catRate(e.Rates.Output), catRate(e.Rates.CacheRead), catRate(e.Rates.CacheWrite), batch, ctx, note)
			}
			t.render(os.Stdout)
			if len(shown) < len(found) {
				fmt.Printf("\n%d more match; narrow the search or raise --limit.\n", len(found)-len(shown))
			}
			fmt.Printf("\nUSD per million tokens; unknown means the catalogue does not state it. %s\n%s\n", catalogueStamp(c), catalogDisclaimer)
			fmt.Println("Details: aiplex usage prices catalog show <provider>/<model>. Import: aiplex usage prices catalog import <provider>/<model>")
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "Only this catalogue provider")
	cmd.Flags().IntVar(&limit, "limit", 50, "Most rows to show (0 for all)")
	return cmd
}

func usageCatalogShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show <provider>/<model>",
		Short:   "Show one catalogue model and what importing it would write",
		Example: "  aiplex usage prices catalog show openai/gpt-4o-mini\n  aiplex usage prices catalog show bedrock/anthropic.claude-sonnet-4-6 -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCatalogue()
			if err != nil {
				return err
			}
			entry, err := lookupEntry(c, args[0])
			if err != nil {
				return err
			}
			plan, err := c.Convert(entry, pricecatalog.Options{})
			if err != nil {
				return fmt.Errorf("%s: %w", entry.ID(), err)
			}
			if usageMachineOutput() {
				usagePrint(map[string]any{"meta": c.Meta, "entry": entry, "prices": plan.Prices})
				return nil
			}
			printPlan(os.Stdout, c, plan, nil)
			fmt.Printf("\nImport it with: aiplex usage prices catalog import %s", entry.ID())
			if len(entry.Blocking) > 0 {
				fmt.Print(" --accept-partial")
			}
			fmt.Println()
			return nil
		},
	}
}

func lookupEntry(c *pricecatalog.Catalog, id string) (pricecatalog.Entry, error) {
	provider, model, err := pricecatalog.SplitID(id)
	if err != nil {
		return pricecatalog.Entry{}, usageError(err.Error(), "Find the id with: aiplex usage prices catalog search <text>")
	}
	entry, ok := c.Lookup(provider, model)
	if !ok {
		return pricecatalog.Entry{}, usageError(fmt.Sprintf("%s is not in the catalogue", id),
			"Matching is exact (no fuzzy matching). Search with: aiplex usage prices catalog search "+model,
			"A model the catalogue does not list stays unknown; set its price with: aiplex usage prices set")
	}
	return entry, nil
}

// printPlan shows exactly what an import writes: per price and per meter, the
// exact decimal, the stored rational, and everything that is not imported.
func printPlan(w *os.File, c *pricecatalog.Catalog, plan pricecatalog.Plan, labels []string) {
	e := plan.Entry
	fmt.Fprintf(w, "%s\n", e.ID())
	for i, p := range plan.Prices {
		kind := "standard"
		if p.Batch != nil && *p.Batch {
			kind = "batch"
		}
		fmt.Fprintf(w, "  price %q (%s)", p.ID, kind)
		if i < len(labels) && labels[i] != "" {
			fmt.Fprintf(w, " [%s]", labels[i])
		}
		fmt.Fprintf(w, ": matches records with provider %q and model_id %q", p.Provider, p.ModelID)
		if p.Batch != nil && *p.Batch {
			fmt.Fprint(w, ", batch exchanges only")
		}
		fmt.Fprintln(w)
		for _, m := range plan.Meters[i] {
			rate := p.Meters[m.Name]
			if strings.HasPrefix(m.Mtok, "0 (") {
				fmt.Fprintf(w, "    %-20s %s\n", m.Name, m.Mtok)
				continue
			}
			fmt.Fprintf(w, "    %-20s %s %s per 1M tokens   (stored %d/%d per token at scale %d)\n", m.Name, p.Currency, m.Mtok, rate.Numerator, rate.Denominator, p.Scale)
		}
		var unset []string
		for _, name := range []string{usageledger.MeterInput, usageledger.MeterOutput, usageledger.MeterCacheRead, usageledger.MeterCacheWrite} {
			if _, set := p.Meters[name]; !set {
				unset = append(unset, name)
			}
		}
		if len(unset) > 0 {
			fmt.Fprintf(w, "    unknown (not stated by the catalogue, left unset): %s\n", strings.Join(unset, ", "))
		}
	}
	if e.ContextWindow > 0 {
		fmt.Fprintf(w, "  context window: %d tokens\n", e.ContextWindow)
	}
	if len(plan.Blocking) > 0 {
		fmt.Fprintf(w, "  NOT EXPRESSIBLE, changes what a request costs (importing needs --accept-partial): %s\n", strings.Join(plan.Blocking, ", "))
		fmt.Fprintln(w, "    The rates above are only the base rates; requests the fields above apply to are mispriced.")
	}
	if len(plan.LeftOut) > 0 {
		fmt.Fprintf(w, "  not imported: %s\n", strings.Join(plan.LeftOut, ", "))
	}
	src := e.Source
	if src == "" {
		src = c.Meta.SourceURL
	}
	fmt.Fprintf(w, "  source: %s\n  catalogue: %s; retrieved from %s\n", src, catalogueStamp(c), c.Meta.SourceURL)
}

type catalogResult struct {
	ID       string `json:"id"`
	Catalog  string `json:"catalog_id"`
	Status   string `json:"status"`
	Revision uint64 `json:"revision,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type importItem struct {
	plan     pricecatalog.Plan
	statuses []string // per price: new, replace, exists, conflict
	revision []uint64 // per price: current revision when replacing
	detail   []string
}

func usageCatalogImportCmd() *cobra.Command {
	var allForAP, currency string
	var dryRun, yes, replace, acceptPartial, reasoningIncluded bool
	cmd := &cobra.Command{
		Use:   "import <provider>/<model>... [--all-for-access-point <ap>]",
		Short: "Create price resources from catalogue models (shows exactly what is written first)",
		Long: `Creates ordinary model-price resources from catalogue entries, marked with
their source (catalogue, commit, snapshot date, source URL). It shows every meter
as an exact decimal before writing anything, and never overwrites an existing
price unless --replace is given. Cost from these prices has the basis "estimated".

Meters the catalogue does not state stay unset, so cost for usage of that kind
stays unknown. Fields an AIPlex price cannot express are listed. When one of
them changes what a request costs (context-length tiers, audio or image token
rates), the import is refused unless you pass --accept-partial. Reasoning tokens
are never priced from the catalogue; add --reasoning-included only when you know
the provider's output tokens already include them (OpenAI, Anthropic), otherwise
records that report reasoning tokens stay unknown cost.

--all-for-access-point resolves each model on the access point to its provider
and native model and imports those the catalogue lists; the rest are listed.`,
		Example: `  aiplex usage prices catalog import openai/gpt-4o-mini --dry-run
  aiplex usage prices catalog import anthropic/claude-haiku-4-5 --reasoning-included --yes
  aiplex usage prices catalog import --all-for-access-point team --yes
  aiplex usage prices catalog import gemini-developer/gemini-2.5-flash --accept-partial --replace`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.ToUpper(currency) != pricecatalog.Currency {
				return usageError(fmt.Sprintf("catalogue prices are in %s; there is no currency conversion", pricecatalog.Currency),
					"Set a price in another currency yourself: aiplex usage prices set <model> --currency "+strings.ToUpper(currency)+" --input-per-mtok ...")
			}
			if len(args) == 0 && allForAP == "" {
				return usageError("name at least one <provider>/<model>, or use --all-for-access-point <ap>", "Find ids with: aiplex usage prices catalog search <text>")
			}
			c, err := loadCatalogue()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			client := newClient()
			opts := pricecatalog.Options{ReasoningIncluded: reasoningIncluded}
			var entries []pricecatalog.Entry
			seen := map[string]bool{}
			for _, id := range args {
				entry, err := lookupEntry(c, id)
				if err != nil {
					return err
				}
				if !seen[entry.ID()] {
					seen[entry.ID()] = true
					entries = append(entries, entry)
				}
			}
			var notFound []string
			if allForAP != "" {
				found, missing, err := catalogEntriesForAccessPoint(ctx, client, c, allForAP)
				if err != nil {
					return err
				}
				notFound = missing
				for _, entry := range found {
					if !seen[entry.ID()] {
						seen[entry.ID()] = true
						entries = append(entries, entry)
					}
				}
			}
			existing, err := client.ListModelPrices(ctx)
			if err != nil {
				return fmt.Errorf("read existing prices (needed so nothing is overwritten): %w", err)
			}
			var items []importItem
			var skipped []catalogResult
			for _, entry := range entries {
				plan, err := c.Convert(entry, opts)
				if err != nil {
					skipped = append(skipped, catalogResult{Catalog: entry.ID(), Status: "not-importable", Detail: err.Error()})
					continue
				}
				if len(plan.Blocking) > 0 && !acceptPartial {
					if allForAP == "" {
						printPlan(os.Stderr, c, plan, []string{"refused", "refused"})
						return usageError(fmt.Sprintf("%s has pricing an AIPlex price cannot express; importing only its base rates would misstate cost", entry.ID()),
							"Nothing was written.", "Review it with: aiplex usage prices catalog show "+entry.ID(), "Import the base rates anyway with --accept-partial")
					}
					skipped = append(skipped, catalogResult{Catalog: entry.ID(), Status: "needs-accept-partial", Detail: strings.Join(plan.Blocking, ", ")})
					continue
				}
				items = append(items, classifyImport(plan, existing, replace))
			}
			if usageMachineOutput() && dryRun {
				usagePrint(map[string]any{"dry_run": true, "meta": c.Meta, "items": importJSON(items), "skipped": skipped, "not_found": notFound})
				return nil
			}
			if !usageMachineOutput() {
				for _, it := range items {
					printPlan(os.Stdout, c, it.plan, importLabels(it))
				}
				for _, s := range skipped {
					fmt.Printf("%s  [skipped: %s] %s\n", s.Catalog, s.Status, s.Detail)
				}
				if len(notFound) > 0 {
					fmt.Println("Not in the catalogue (left unknown):")
					for _, m := range notFound {
						fmt.Println("  " + m)
					}
				}
				fmt.Println("\n" + catalogDisclaimer)
			}
			writes := 0
			for _, it := range items {
				for _, st := range it.statuses {
					if st == "new" || st == "replace" {
						writes++
					}
				}
			}
			if writes == 0 {
				if !usageMachineOutput() {
					fmt.Println("\nNothing to write.")
				} else {
					usagePrint(map[string]any{"results": []catalogResult{}, "skipped": skipped, "not_found": notFound})
				}
				return nil
			}
			if dryRun {
				fmt.Printf("\nDry run: nothing was written. %d price(s) would be written.\n", writes)
				return nil
			}
			if err := confirmImport(cmd, yes, fmt.Sprintf("Write %d price(s) from the catalogue?", writes)); err != nil {
				return err
			}
			var results []catalogResult
			failed := false
			for _, it := range items {
				for i, p := range it.plan.Prices {
					st := it.statuses[i]
					if st != "new" && st != "replace" {
						results = append(results, catalogResult{ID: p.ID, Catalog: it.plan.Entry.ID(), Status: st, Detail: it.detail[i]})
						continue
					}
					saved, err := client.PutModelPrice(ctx, p, it.revision[i])
					if err != nil {
						failed = true
						results = append(results, catalogResult{ID: p.ID, Catalog: it.plan.Entry.ID(), Status: "failed", Detail: err.Error()})
						continue
					}
					status := "created"
					if st == "replace" {
						status = "replaced"
					}
					results = append(results, catalogResult{ID: p.ID, Catalog: it.plan.Entry.ID(), Status: status, Revision: saved.Revision})
				}
			}
			if usageMachineOutput() {
				usagePrint(map[string]any{"results": results, "skipped": skipped, "not_found": notFound})
			} else {
				fmt.Println()
				for _, r := range results {
					line := fmt.Sprintf("%-9s price %s", r.Status, r.ID)
					if r.Revision > 0 {
						line += fmt.Sprintf(" (revision %d)", r.Revision)
					}
					if r.Detail != "" {
						line += ": " + r.Detail
					}
					fmt.Println(line)
				}
				fmt.Println("Cost for new records of these models is now estimated from the catalogue; existing records are not recalculated.")
			}
			if failed {
				return fmt.Errorf("some prices were not written")
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&allForAP, "all-for-access-point", "", "Import every model of this access point that the catalogue lists")
	fl.StringVar(&currency, "currency", pricecatalog.Currency, "Currency (the catalogue is USD only)")
	fl.BoolVar(&dryRun, "dry-run", false, "Show what would be written and write nothing")
	fl.BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
	fl.BoolVar(&replace, "replace", false, "Replace an existing price with the same id (never done without this flag)")
	fl.BoolVar(&acceptPartial, "accept-partial", false, "Import base rates even when the model has pricing a price cannot express")
	fl.BoolVar(&reasoningIncluded, "reasoning-included", false, "Declare reasoning tokens as included in output tokens (free meter); only when the provider does")
	return cmd
}

func importJSON(items []importItem) []map[string]any {
	out := []map[string]any{}
	for _, it := range items {
		for i, p := range it.plan.Prices {
			out = append(out, map[string]any{"catalog_id": it.plan.Entry.ID(), "status": it.statuses[i], "detail": it.detail[i], "price": p, "blocking": it.plan.Blocking, "left_out": it.plan.LeftOut})
		}
	}
	return out
}

func importLabels(it importItem) []string {
	labels := make([]string, len(it.statuses))
	for i, st := range it.statuses {
		labels[i] = map[string]string{"new": "will create", "replace": "will replace", "exists": "skipped: exists, use --replace", "conflict": "skipped: " + it.detail[i]}[st]
	}
	return labels
}

// classifyImport decides, per price, whether it is written. An existing price
// with the same id is replaced only with --replace. Another price that already
// covers the same provider, model and batch selector would tie with the new
// one (a tie is no match, so cost would become unknown), so it is never added
// beside it.
func classifyImport(plan pricecatalog.Plan, existing []aiplex.ModelPriceResource, replace bool) importItem {
	it := importItem{plan: plan}
	for _, p := range plan.Prices {
		status, revision, detail := "new", uint64(0), ""
		for _, ex := range existing {
			if ex.ID == p.ID {
				if replace {
					status, revision = "replace", ex.Revision
				} else {
					status = "exists"
				}
				break
			}
		}
		if status == "new" {
			for _, ex := range existing {
				q := ex.Price
				if q.ModelID == p.ModelID && q.Provider == p.Provider && sameBatch(q.Batch, p.Batch) && q.ImageSize == "" && q.ImageQuality == "" {
					status, detail = "conflict", fmt.Sprintf("price %q already covers %s/%s; delete or edit it instead", ex.ID, p.Provider, p.ModelID)
					break
				}
			}
		}
		it.statuses = append(it.statuses, status)
		it.revision = append(it.revision, revision)
		it.detail = append(it.detail, detail)
	}
	return it
}

func sameBatch(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func confirmImport(cmd *cobra.Command, yes bool, question string) error {
	if yes {
		return nil
	}
	if !stdinIsTerminal() || !stderrIsTerminal() {
		return usageError("refusing to write prices without confirmation: there is no terminal to ask. "+question, "Review with --dry-run, then add --yes (-y) to run it non-interactively")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	return newCLIError(exitError, "cancelled; nothing was written")
}

// catalogEntriesForAccessPoint resolves every model of an access point to the
// provider and native model its usage records carry, and looks each up exactly.
func catalogEntriesForAccessPoint(ctx context.Context, client *aiplex.Client, c *pricecatalog.Catalog, apID string) (found []pricecatalog.Entry, notFound []string, err error) {
	ap, err := client.GetAccessPoint(ctx, apID)
	if err != nil {
		return nil, nil, fmt.Errorf("read access point %s: %w", apID, err)
	}
	if len(ap.Spec.Models) == 0 {
		return nil, nil, usageError(fmt.Sprintf("access point %s has no models", apID))
	}
	surfaces := map[string]*aiplex.ModelSurfaceRecord{}
	seen := map[string]bool{}
	for _, m := range ap.Spec.Models {
		surface, ok := surfaces[m.SurfaceRef]
		if !ok {
			surface, err = client.GetModelSurface(ctx, m.SurfaceRef)
			if err != nil {
				notFound = append(notFound, fmt.Sprintf("%s (model %s): surface %s could not be read: %v", m.NativeModel, m.Name, m.SurfaceRef, err))
				surfaces[m.SurfaceRef] = nil
				continue
			}
			surfaces[m.SurfaceRef] = surface
		}
		if surface == nil {
			continue
		}
		provider, model, reason := surfaceCatalogIdentity(surface.Spec.Surface, m.NativeModel)
		label := fmt.Sprintf("%s (access point model %s, surface %s)", m.NativeModel, m.Name, m.SurfaceRef)
		if provider == "" {
			notFound = append(notFound, label+": "+reason)
			continue
		}
		entry, ok := c.Lookup(provider, model)
		if !ok {
			notFound = append(notFound, fmt.Sprintf("%s: %s/%s is not in the catalogue", label, provider, model))
			continue
		}
		if !seen[entry.ID()] {
			seen[entry.ID()] = true
			found = append(found, entry)
		}
	}
	sort.Strings(notFound)
	return found, notFound, nil
}

// surfaceCatalogIdentity gives the catalogue provider and the model id that a
// usage record for this surface and native model carries. The ledger records
// the provider block's id, or the publisher of a Google Cloud surface.
func surfaceCatalogIdentity(s aiplex.ModelSurface, native string) (provider, model, reason string) {
	if s.Provider != nil {
		switch id := s.Provider.ID; id {
		case "openai", "anthropic", "azure-openai", "bedrock", "gemini-developer":
			return id, native, ""
		case "openai-compatible":
			return "", "", "an OpenAI-compatible endpoint: the vendor is unknown, so import it by name, for example groq/<model>"
		default:
			return "", "", fmt.Sprintf("provider %s has no catalogue", id)
		}
	}
	model = native
	for _, prefix := range []string{"projects/" + s.Project + "/locations/" + s.Location + "/publishers/" + s.Publisher + "/models/", "publishers/" + s.Publisher + "/models/", "models/", s.Publisher + "/"} {
		model = strings.TrimPrefix(model, prefix)
	}
	switch s.Publisher {
	case "google":
		return "google", model, ""
	case "anthropic":
		return "vertex-anthropic", model, ""
	}
	return "", "", fmt.Sprintf("publisher %q has no catalogue", s.Publisher)
}

// ---- hints ---------------------------------------------------------------

// catalogueProvidersFor lists the catalogue providers a record's provider can
// come from. openai-compatible has none: the vendor is not recorded.
func catalogueProvidersFor(ledgerProvider string) []string {
	switch ledgerProvider {
	case "anthropic":
		return []string{"anthropic", "vertex-anthropic"}
	case "openai-compatible", "mcp", "":
		return nil
	}
	if pricecatalog.LedgerProvider(ledgerProvider) == ledgerProvider {
		return []string{ledgerProvider}
	}
	return nil
}

// catalogHintTarget is a model whose cost is unknown.
type catalogHintTarget struct{ provider, model string }

// usageCatalogHint prints at most one line on stderr when a model with unknown
// cost has a catalogue entry and no price of any kind is configured for the
// model. It prints nothing off a terminal, with -o json or yaml, or when the
// price list cannot be read. It never imports anything.
func usageCatalogHint(ctx context.Context, targets []catalogHintTarget) {
	if usageMachineOutput() || !stderrIsTerminal() || len(targets) == 0 {
		return
	}
	line := usageCatalogHintLine(ctx, newClient(), targets)
	if line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
}

func usageCatalogHintLine(ctx context.Context, client *aiplex.Client, targets []catalogHintTarget) string {
	c, err := pricecatalog.Embedded()
	if err != nil {
		return ""
	}
	prices, err := client.ListModelPrices(ctx)
	if err != nil {
		return ""
	}
	priced := map[string]bool{}
	for _, p := range prices {
		priced[p.Price.ModelID] = true
	}
	var ids []string
	seen := map[string]bool{}
	for _, t := range targets {
		if t.model == "" || priced[t.model] {
			continue
		}
		var matches []string
		if t.provider != "" {
			for _, cp := range catalogueProvidersFor(t.provider) {
				if _, ok := c.Lookup(cp, t.model); ok {
					matches = append(matches, cp+"/"+t.model)
				}
			}
		} else {
			for _, cp := range pricecatalog.ProviderNames() {
				if _, ok := c.Lookup(cp, t.model); ok {
					matches = append(matches, cp+"/"+t.model)
				}
			}
		}
		if len(matches) != 1 || seen[matches[0]] {
			continue
		}
		seen[matches[0]] = true
		ids = append(ids, matches[0])
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	more := ""
	if len(ids) > 1 {
		more = fmt.Sprintf(" (and %d more: see aiplex usage prices catalog search)", len(ids)-1)
	}
	_, model, _ := strings.Cut(ids[0], "/")
	return fmt.Sprintf("Cost is unknown for %s, which the price catalogue lists (third-party list price, %s). Review and import: aiplex usage prices catalog import %s%s",
		model, c.Meta.RetrievedAt, ids[0], more)
}

// devPriceHint is the dev summary's one-line pointer to the catalogue for the
// model a real provider was wired to. It prices nothing.
func devPriceHint(devProvider, model string) string {
	provider := map[string]string{"openai": "openai", "anthropic": "anthropic", "gemini": "gemini-developer"}[devProvider]
	c, err := pricecatalog.Embedded()
	if err != nil || provider == "" {
		return ""
	}
	if _, ok := c.Lookup(provider, model); !ok {
		return ""
	}
	return fmt.Sprintf("the catalogue lists %s (list price, third-party snapshot %s): aiplex usage prices catalog import %s/%s", model, c.Meta.RetrievedAt, provider, model)
}
