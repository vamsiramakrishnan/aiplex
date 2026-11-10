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
		Short: "Look up list prices in the bundled catalogues and import the ones you choose",
		Long: `The binary carries pinned snapshots of two third-party price lists: LiteLLM's
model_prices_and_context_window.json (the default) and the models.dev model
database (both MIT licence). Nothing is priced from them automatically: a model
stays unknown until you import its price, which creates an ordinary price
resource marked with its source and snapshot date. The cost basis stays
"estimated": these are list prices, not invoices.

When the two sources state different values for the same model and meter, search
and show flag it, and an import needs --source to say which one to believe.`,
		Example: `  aiplex usage prices catalog search claude --provider anthropic
  aiplex usage prices catalog show openai/gpt-4o-mini
  aiplex usage prices catalog import openai/gpt-4o-mini --dry-run
  aiplex usage prices catalog import mistral/open-mistral-nemo --source models.dev --dry-run`,
	}
	cmd.AddCommand(usageCatalogSearchCmd(), usageCatalogShowCmd(), usageCatalogImportCmd())
	return cmd
}

func loadCatalogue() (*pricecatalog.Catalog, error) {
	return loadCatalogueSource(pricecatalog.SourceLiteLLM)
}

func loadCatalogueSource(name string) (*pricecatalog.Catalog, error) {
	c, err := pricecatalog.EmbeddedSource(name)
	if err != nil {
		return nil, fmt.Errorf("price catalogue %s unavailable: %w", name, err)
	}
	return c, nil
}

// catalogueSet is the embedded snapshots of every source.
type catalogueSet struct {
	byName map[string]*pricecatalog.Catalog
}

func loadCatalogues() (*catalogueSet, error) {
	set := &catalogueSet{byName: map[string]*pricecatalog.Catalog{}}
	for _, name := range pricecatalog.SourceNames {
		c, err := loadCatalogueSource(name)
		if err != nil {
			return nil, err
		}
		set.byName[name] = c
	}
	return set, nil
}

// entries returns the entry of an id from every source that lists it.
func (s *catalogueSet) entries(id string) map[string]pricecatalog.Entry {
	out := map[string]pricecatalog.Entry{}
	for name, c := range s.byName {
		if e, ok := c.Get(id); ok {
			out[name] = e
		}
	}
	return out
}

// disagreements compares the two sources' entries for an id.
func disagreementsOf(found map[string]pricecatalog.Entry) []pricecatalog.Disagreement {
	a, okA := found[pricecatalog.SourceLiteLLM]
	b, okB := found[pricecatalog.SourceModelsDev]
	if !okA || !okB {
		return nil
	}
	return pricecatalog.Disagreements(a, b)
}

func formatDisagreement(d pricecatalog.Disagreement) string {
	meter := d.Meter
	if d.Above > 0 {
		meter += fmt.Sprintf(" (input over %d tokens)", d.Above)
	}
	return fmt.Sprintf("%s: %s says %s, %s says %s (USD per 1M tokens)", meter, pricecatalog.SourceLiteLLM, d.A, pricecatalog.SourceModelsDev, d.B)
}

func catalogueStamp(c *pricecatalog.Catalog) string {
	commit := c.Meta.Commit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	return fmt.Sprintf("%s snapshot %s (commit %s)", c.Meta.Catalog, c.Meta.RetrievedAt, commit)
}

func cataloguesStamp(s *catalogueSet) string {
	var parts []string
	for _, name := range pricecatalog.SourceNames {
		parts = append(parts, catalogueStamp(s.byName[name]))
	}
	return strings.Join(parts, "; ")
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
text (case-insensitive substring match only), from both sources. Prices are USD
per million tokens, from LiteLLM when it lists the model, otherwise from
models.dev. A model both sources list with different values for the same meter
is flagged. Providers: ` + strings.Join(pricecatalog.ProviderNames(), ", ") + `.`,
		Example: `  aiplex usage prices catalog search gpt-4o
  aiplex usage prices catalog search claude --provider anthropic
  aiplex usage prices catalog search "gemini flash" -o json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if provider != "" && pricecatalog.LedgerProvider(provider) == "" {
				return usageError(fmt.Sprintf("unknown catalogue provider %q", provider), "Providers: "+strings.Join(pricecatalog.ProviderNames(), ", "))
			}
			set, err := loadCatalogues()
			if err != nil {
				return err
			}
			text := strings.Join(args, " ")
			ids := map[string]bool{}
			bySource := map[string][]pricecatalog.Entry{}
			for _, name := range pricecatalog.SourceNames {
				found := set.byName[name].Search(text, provider)
				bySource[name] = found
				for _, e := range found {
					ids[e.ID()] = true
				}
			}
			ordered := make([]string, 0, len(ids))
			for id := range ids {
				ordered = append(ordered, id)
			}
			sort.Strings(ordered)
			var all []pricecatalog.Disagreement
			for _, id := range ordered {
				all = append(all, disagreementsOf(set.entries(id))...)
			}
			if usageMachineOutput() {
				sources := map[string]any{}
				for _, name := range pricecatalog.SourceNames {
					sources[name] = map[string]any{"meta": set.byName[name].Meta, "entries": bySource[name]}
				}
				usagePrint(map[string]any{"meta": set.byName[pricecatalog.SourceLiteLLM].Meta, "entries": bySource[pricecatalog.SourceLiteLLM], "sources": sources, "disagreements": all})
				return nil
			}
			if len(ordered) == 0 {
				fmt.Println("No catalogue model matches. A model the catalogue does not list stays unknown; set its price with aiplex usage prices set.")
				return nil
			}
			shown := ordered
			if limit > 0 && len(shown) > limit {
				shown = shown[:limit]
			}
			t := newTable(keyCol("MODEL"), col("INPUT"), col("OUTPUT"), col("CACHE READ"), wideCol("CACHE WRITE"), wideCol("BATCH IN/OUT"), wideCol("CONTEXT"), col("SOURCES"), col("NOTE"))
			for _, id := range shown {
				found := set.entries(id)
				e, listed := found[pricecatalog.SourceLiteLLM]
				if !listed {
					e = found[pricecatalog.SourceModelsDev]
				}
				var notes []string
				if len(e.Blocking) > 0 {
					notes = append(notes, "partial: needs --accept-partial")
				}
				if d := disagreementsOf(found); len(d) > 0 {
					meters := []string{}
					for _, x := range d {
						meters = append(meters, x.Meter)
					}
					notes = append(notes, "sources disagree: "+strings.Join(meters, ","))
				}
				batch := "—"
				if e.Batch != nil {
					batch = catRate(e.Batch.Input) + " / " + catRate(e.Batch.Output)
				}
				ctx := "unknown"
				if e.ContextWindow > 0 {
					ctx = fmt.Sprint(e.ContextWindow)
				}
				var names []string
				for _, name := range pricecatalog.SourceNames {
					if _, ok := found[name]; ok {
						names = append(names, name)
					}
				}
				t.add(id, catRate(e.Rates.Input), catRate(e.Rates.Output), catRate(e.Rates.CacheRead), catRate(e.Rates.CacheWrite), batch, ctx, strings.Join(names, ", "), strings.Join(notes, "; "))
			}
			t.render(os.Stdout)
			if len(shown) < len(ordered) {
				fmt.Printf("\n%d more match; narrow the search or raise --limit.\n", len(ordered)-len(shown))
			}
			fmt.Printf("\nUSD per million tokens; unknown means the catalogue does not state it. %s\n%s\n", cataloguesStamp(set), catalogDisclaimer)
			fmt.Println("Details: aiplex usage prices catalog show <provider>/<model>. Import: aiplex usage prices catalog import <provider>/<model> [--source litellm|models.dev]")
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "Only this catalogue provider")
	cmd.Flags().IntVar(&limit, "limit", 50, "Most rows to show (0 for all)")
	return cmd
}

func usageCatalogShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <provider>/<model>",
		Short: "Show one catalogue model in each source and what importing it would write",
		Long: `Shows the model as each bundled source states it, what importing it would
write, and every meter on which the two sources disagree.`,
		Example: "  aiplex usage prices catalog show openai/gpt-4o-mini\n  aiplex usage prices catalog show bedrock/anthropic.claude-sonnet-4-6 -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			set, err := loadCatalogues()
			if err != nil {
				return err
			}
			found, err := lookupEntries(set, args[0])
			if err != nil {
				return err
			}
			plans := map[string]pricecatalog.Plan{}
			for name, entry := range found {
				plan, err := set.byName[name].Convert(entry, pricecatalog.Options{})
				if err != nil {
					if len(found) == 1 {
						return fmt.Errorf("%s: %w", entry.ID(), err)
					}
					continue
				}
				plans[name] = plan
			}
			d := disagreementsOf(found)
			if usageMachineOutput() {
				sources := map[string]any{}
				for name, plan := range plans {
					sources[name] = map[string]any{"meta": set.byName[name].Meta, "entry": found[name], "prices": plan.Prices}
				}
				primary := pricecatalog.SourceLiteLLM
				if _, ok := plans[primary]; !ok {
					primary = pricecatalog.SourceModelsDev
				}
				usagePrint(map[string]any{"meta": set.byName[primary].Meta, "entry": found[primary], "prices": plans[primary].Prices, "sources": sources, "disagreements": d})
				return nil
			}
			var first pricecatalog.Entry
			for _, name := range pricecatalog.SourceNames {
				plan, ok := plans[name]
				if !ok {
					continue
				}
				if first.Model == "" {
					first = found[name]
				}
				fmt.Printf("== %s ==\n", name)
				printPlan(os.Stdout, set.byName[name], plan, nil, false)
			}
			if len(found) < len(pricecatalog.SourceNames) {
				for _, name := range pricecatalog.SourceNames {
					if _, ok := found[name]; !ok {
						fmt.Printf("\n%s does not list this model.\n", name)
					}
				}
			}
			if len(d) > 0 {
				fmt.Println("\nSOURCES DISAGREE on a value both state:")
				for _, x := range d {
					fmt.Println("  " + formatDisagreement(x))
				}
				fmt.Printf("An import needs --source to choose one: aiplex usage prices catalog import %s --source litellm|models.dev\n", first.ID())
			}
			fmt.Printf("\nImport it with: aiplex usage prices catalog import %s", first.ID())
			if len(d) > 0 {
				fmt.Print(" --source <litellm|models.dev>")
			} else if _, ok := found[pricecatalog.SourceLiteLLM]; !ok {
				fmt.Print(" --source " + pricecatalog.SourceModelsDev)
			}
			if len(first.Blocking) > 0 {
				fmt.Print(" --accept-partial")
			}
			fmt.Println()
			return nil
		},
	}
}

// lookupEntries resolves an exact <provider>/<model> id in every source.
func lookupEntries(set *catalogueSet, id string) (map[string]pricecatalog.Entry, error) {
	provider, model, err := pricecatalog.SplitID(id)
	if err != nil {
		return nil, usageError(err.Error(), "Find the id with: aiplex usage prices catalog search <text>")
	}
	found := set.entries(provider + "/" + model)
	if len(found) == 0 {
		return nil, usageError(fmt.Sprintf("%s is not in the catalogue", id),
			"Matching is exact (no fuzzy matching). Search with: aiplex usage prices catalog search "+model,
			"A model the catalogue does not list stays unknown; set its price with: aiplex usage prices set")
	}
	return found, nil
}

// printPlan shows exactly what an import writes: per price and per meter, the
// exact decimal, the stored rational, and everything that is not imported.
// accepted is true when the import goes ahead despite NOT EXPRESSIBLE fields
// because --accept-partial was given.
func printPlan(w *os.File, c *pricecatalog.Catalog, plan pricecatalog.Plan, labels []string, accepted bool) {
	e := plan.Entry
	fmt.Fprintf(w, "%s\n", e.ID())
	for i, p := range plan.Prices {
		kind := "standard"
		switch {
		case p.Batch != nil && *p.Batch:
			kind = "batch"
		case p.ServiceTier != "":
			kind = p.ServiceTier + " service tier"
		}
		fmt.Fprintf(w, "  price %q (%s)", p.ID, kind)
		if i < len(labels) && labels[i] != "" {
			fmt.Fprintf(w, " [%s]", labels[i])
		}
		fmt.Fprintf(w, ": matches records with provider %q and model_id %q", p.Provider, p.ModelID)
		if p.Batch != nil && *p.Batch {
			fmt.Fprint(w, ", batch exchanges only")
		}
		if p.ServiceTier != "" {
			fmt.Fprintf(w, ", service_tier %q only (without a service_tier selector a price covers standard-tier exchanges)", p.ServiceTier)
		}
		fmt.Fprintln(w)
		printMeters(w, p, plan.Meters[i], "    ")
		for _, tier := range plan.Tiers[i] {
			fmt.Fprintf(w, "    when a request's total input is over %d tokens, the whole request is priced at:\n", tier.Above)
			for _, m := range tier.Meters {
				rate := tierRate(p, tier.Above, m.Name)
				printMeter(w, p, m, rate, "      ")
			}
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
		if accepted {
			fmt.Fprintf(w, "  NOT EXPRESSIBLE, changes what a request costs (imported anyway: --accept-partial was given): %s\n", strings.Join(plan.Blocking, ", "))
		} else {
			fmt.Fprintf(w, "  NOT EXPRESSIBLE, changes what a request costs (importing needs --accept-partial): %s\n", strings.Join(plan.Blocking, ", "))
		}
		fmt.Fprintln(w, "    The rates above leave these out; requests the fields above apply to are mispriced.")
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

func tierRate(p usageledger.Price, above int64, name string) usageledger.Rational {
	for _, t := range p.Tiers {
		if t.AboveInputTokens == above {
			return t.Meters[name]
		}
	}
	return usageledger.Rational{}
}

func printMeters(w *os.File, p usageledger.Price, meters []pricecatalog.Meter, indent string) {
	for _, m := range meters {
		printMeter(w, p, m, p.Meters[m.Name], indent)
	}
}

func printMeter(w *os.File, p usageledger.Price, m pricecatalog.Meter, rate usageledger.Rational, indent string) {
	switch {
	case strings.HasPrefix(m.Mtok, "0 ("), m.Unit == "call" && !strings.ContainsAny(m.Mtok, "0123456789"):
		fmt.Fprintf(w, "%s%-24s %s\n", indent, m.Name, m.Mtok)
	case m.Unit == "call":
		fmt.Fprintf(w, "%s%-24s %s %s per call   (stored %d/%d per call at scale %d)\n", indent, m.Name, p.Currency, m.Mtok, rate.Numerator, rate.Denominator, p.Scale)
	default:
		fmt.Fprintf(w, "%s%-24s %s %s per 1M tokens   (stored %d/%d per token at scale %d)\n", indent, m.Name, p.Currency, m.Mtok, rate.Numerator, rate.Denominator, p.Scale)
	}
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
	cat      *pricecatalog.Catalog
	source   string
	statuses []string // per price: new, replace, exists, conflict
	revision []uint64 // per price: current revision when replacing
	detail   []string
}

func usageCatalogImportCmd() *cobra.Command {
	var allForAP, currency, source string
	var dryRun, yes, replace, acceptPartial, reasoningIncluded bool
	cmd := &cobra.Command{
		Use:   "import <provider>/<model>... [--all-for-access-point <ap>]",
		Short: "Create price resources from catalogue models (shows exactly what is written first)",
		Long: `Creates ordinary model-price resources from catalogue entries, marked with
their source (catalogue, commit, snapshot date, source URL). The default source
is litellm; --source models.dev imports from the models.dev snapshot instead.
When both sources list a model and state different values for the same meter,
the import is refused until --source names the one to use, and both values are
printed. It shows every meter
as an exact decimal before writing anything, and never overwrites an existing
price unless --replace is given. Cost from these prices has the basis "estimated".

Meters the catalogue does not state stay unset, so cost for usage of that kind
stays unknown. Fields an AIPlex price cannot express are listed. When one of
them changes what a request costs (context-length tiers whose billing rule is
not confirmed for the provider, token rates for modalities the ledger does not
hold), the import is refused unless you pass --accept-partial. Reasoning tokens
are never priced from the catalogue; add --reasoning-included only when you know
the provider's output tokens already include them (OpenAI, Anthropic), otherwise
records that report reasoning tokens stay unknown cost.

--all-for-access-point resolves each model on the access point to its provider
and native model and imports those the catalogue lists; the rest are listed.`,
		Example: `  aiplex usage prices catalog import openai/gpt-4o-mini --dry-run
  aiplex usage prices catalog import anthropic/claude-haiku-4-5 --reasoning-included --yes
  aiplex usage prices catalog import --all-for-access-point team --yes
  aiplex usage prices catalog import gemini-developer/gemini-2.5-flash --accept-partial --replace
  aiplex usage prices catalog import mistral/open-mistral-nemo --source models.dev --yes`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.ToUpper(currency) != pricecatalog.Currency {
				return usageError(fmt.Sprintf("catalogue prices are in %s; there is no currency conversion", pricecatalog.Currency),
					"Set a price in another currency yourself: aiplex usage prices set <model> --currency "+strings.ToUpper(currency)+" --input-per-mtok ...")
			}
			if len(args) == 0 && allForAP == "" {
				return usageError("name at least one <provider>/<model>, or use --all-for-access-point <ap>", "Find ids with: aiplex usage prices catalog search <text>")
			}
			set, err := loadCatalogues()
			if err != nil {
				return err
			}
			if _, ok := set.byName[source]; !ok {
				return usageError(fmt.Sprintf("unknown catalogue source %q", source), "Sources: "+strings.Join(pricecatalog.SourceNames, ", "))
			}
			explicit := cmd.Flags().Changed("source")
			ctx := cmd.Context()
			client := newClient()
			opts := pricecatalog.Options{ReasoningIncluded: reasoningIncluded}
			var ids []string
			seen := map[string]bool{}
			for _, id := range args {
				found, err := lookupEntries(set, id)
				if err != nil {
					return err
				}
				_ = found
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			var notFound []string
			if allForAP != "" {
				found, missing, err := catalogEntriesForAccessPoint(ctx, client, set, allForAP)
				if err != nil {
					return err
				}
				notFound = missing
				for _, id := range found {
					if !seen[id] {
						seen[id] = true
						ids = append(ids, id)
					}
				}
			}
			existing, err := client.ListModelPrices(ctx)
			if err != nil {
				return fmt.Errorf("read existing prices (needed so nothing is overwritten): %w", err)
			}
			var items []importItem
			var skipped []catalogResult
			for _, id := range ids {
				found := set.entries(id)
				entry, from, problem := chooseSource(found, source, explicit)
				if problem != nil {
					if allForAP == "" {
						return usageError(fmt.Sprintf("%s: %s", id, strings.Join(problem.detail, "\n  ")), problem.hints...)
					}
					skipped = append(skipped, catalogResult{Catalog: id, Status: problem.status, Detail: strings.Join(problem.detail, "; ")})
					continue
				}
				c := set.byName[from]
				plan, err := c.Convert(entry, opts)
				if err != nil {
					skipped = append(skipped, catalogResult{Catalog: entry.ID(), Status: "not-importable", Detail: err.Error()})
					continue
				}
				if len(plan.Blocking) > 0 && !acceptPartial {
					if allForAP == "" {
						printPlan(os.Stderr, c, plan, make([]string, len(plan.Prices)), false)
						return usageError(fmt.Sprintf("%s has pricing an AIPlex price cannot express; importing only its base rates would misstate cost", entry.ID()),
							"Nothing was written.", "Review it with: aiplex usage prices catalog show "+entry.ID(), "Import the base rates anyway with --accept-partial")
					}
					skipped = append(skipped, catalogResult{Catalog: entry.ID(), Status: "needs-accept-partial", Detail: strings.Join(plan.Blocking, ", ")})
					continue
				}
				item := classifyImport(plan, existing, replace)
				item.cat, item.source = c, from
				items = append(items, item)
			}
			if usageMachineOutput() && dryRun {
				usagePrint(map[string]any{"dry_run": true, "items": importJSON(items), "skipped": skipped, "not_found": notFound})
				return nil
			}
			if !usageMachineOutput() {
				for _, it := range items {
					printPlan(os.Stdout, it.cat, it.plan, importLabels(it), acceptPartial)
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
	fl.StringVar(&source, "source", pricecatalog.SourceLiteLLM, "Catalogue source: "+strings.Join(pricecatalog.SourceNames, ", ")+" (needed when the two disagree on a model)")
	fl.BoolVar(&dryRun, "dry-run", false, "Show what would be written and write nothing")
	fl.BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
	fl.BoolVar(&replace, "replace", false, "Replace an existing price with the same id (never done without this flag)")
	fl.BoolVar(&acceptPartial, "accept-partial", false, "Import base rates even when the model has pricing a price cannot express")
	fl.BoolVar(&reasoningIncluded, "reasoning-included", false, "Declare reasoning tokens as included in output tokens (free meter); only when the provider does")
	return cmd
}

// resolveProblem says why a model cannot be imported without more input.
type resolveProblem struct {
	status string
	detail []string // first line is the headline
	hints  []string
}

// chooseSource picks the entry an import uses. An explicit --source decides.
// Without one, the default source (litellm) is used, but never when the other
// source states a different value for the same meter, and never for a model
// only the other source lists: the person says which to believe.
func chooseSource(found map[string]pricecatalog.Entry, source string, explicit bool) (pricecatalog.Entry, string, *resolveProblem) {
	if explicit {
		if e, ok := found[source]; ok {
			return e, source, nil
		}
		var others []string
		for _, name := range pricecatalog.SourceNames {
			if _, ok := found[name]; ok {
				others = append(others, name)
			}
		}
		return pricecatalog.Entry{}, "", &resolveProblem{status: "not-in-source", detail: []string{fmt.Sprintf("it is not in the %s catalogue", source)}, hints: []string{"It is listed by: " + strings.Join(others, ", ")}}
	}
	if d := disagreementsOf(found); len(d) > 0 {
		lines := []string{"the two catalogue sources disagree, so choose which to import with --source litellm or --source models.dev"}
		for _, x := range d {
			lines = append(lines, formatDisagreement(x))
		}
		return pricecatalog.Entry{}, "", &resolveProblem{status: "sources-disagree", detail: lines, hints: []string{"Nothing was written.", "Import one explicitly: --source litellm or --source models.dev (review both with: aiplex usage prices catalog show <id>)"}}
	}
	if e, ok := found[pricecatalog.SourceLiteLLM]; ok {
		return e, pricecatalog.SourceLiteLLM, nil
	}
	return pricecatalog.Entry{}, "", &resolveProblem{status: "only-in-models.dev", detail: []string{"only the models.dev catalogue lists it (the default source is litellm)"}, hints: []string{"Import it from models.dev with: --source models.dev"}}
}

func importJSON(items []importItem) []map[string]any {
	out := []map[string]any{}
	for _, it := range items {
		for i, p := range it.plan.Prices {
			out = append(out, map[string]any{"catalog_id": it.plan.Entry.ID(), "source": it.source, "status": it.statuses[i], "detail": it.detail[i], "price": p, "blocking": it.plan.Blocking, "left_out": it.plan.LeftOut})
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
				if q.ModelID == p.ModelID && q.Provider == p.Provider && sameBatch(q.Batch, p.Batch) && q.ServiceTier == p.ServiceTier && q.ImageSize == "" && q.ImageQuality == "" {
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
func catalogEntriesForAccessPoint(ctx context.Context, client *aiplex.Client, set *catalogueSet, apID string) (found []string, notFound []string, err error) {
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
		id := provider + "/" + model
		if len(set.entries(id)) == 0 {
			notFound = append(notFound, fmt.Sprintf("%s: %s is not in the catalogue", label, id))
			continue
		}
		if !seen[id] {
			seen[id] = true
			found = append(found, id)
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
