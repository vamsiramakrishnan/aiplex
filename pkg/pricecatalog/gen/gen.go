package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/pricecatalog"
)

// Modes whose cost is stated per token. Every other LiteLLM mode (image, audio,
// video, rerank, search, OCR, realtime, ...) is priced in units an AIPlex price
// does not carry and is not imported.
var modes = map[string]bool{"chat": true, "completion": true, "responses": true, "embedding": true}

// vendors are OpenAI-compatible APIs whose native model ids the catalogue lists.
var vendors = map[string]bool{"deepseek": true, "groq": true, "mistral": true, "xai": true}

// mapKey maps a LiteLLM entry to the catalogue provider and the model id as
// that provider spells it natively. It strips a known routing prefix and
// refuses anything else with a slash: region, commitment and resolution
// variants are other products, and no id is ever guessed by fuzzy matching.
func mapKey(litellmProvider, key string) (provider, model string, ok bool) {
	plain := func(s string) bool { return s != "" && !strings.Contains(s, "/") }
	switch litellmProvider {
	case "openai", "text-completion-openai":
		// Fine-tune entries ("ft:...") are templates; real ids carry a suffix.
		if plain(key) && !strings.HasPrefix(key, "ft:") {
			return "openai", key, true
		}
	case "anthropic":
		if plain(key) {
			return "anthropic", key, true
		}
	case "azure", "azure_text":
		// Azure model names; the operator's deployment name may differ.
		if rest, found := strings.CutPrefix(key, "azure/"); found && plain(rest) {
			return "azure-openai", rest, true
		}
	case "bedrock", "bedrock_converse":
		// Native Bedrock ids ("anthropic.claude-...", and inference profiles
		// with a geography prefix such as "us.anthropic...") are kept verbatim.
		rest := strings.TrimPrefix(key, "bedrock/")
		if plain(rest) && strings.Contains(rest, ".") {
			return "bedrock", rest, true
		}
	case "gemini":
		if rest, found := strings.CutPrefix(key, "gemini/"); found && plain(rest) {
			return "gemini-developer", rest, true
		}
	case "vertex_ai-language-models", "vertex_ai":
		rest := strings.TrimPrefix(key, "vertex_ai/")
		if plain(rest) && strings.HasPrefix(rest, "gemini-") {
			return "google", rest, true
		}
	case "vertex_ai-anthropic_models":
		// Only Vertex's own versioned form: an unversioned id would collide with
		// the first-party Anthropic id, which is priced differently.
		rest := strings.TrimPrefix(key, "vertex_ai/")
		if plain(rest) && strings.Contains(rest, "@") {
			return "vertex-anthropic", rest, true
		}
	default:
		if vendors[litellmProvider] {
			rest := strings.TrimPrefix(key, litellmProvider+"/")
			if rest != "" {
				return litellmProvider, rest, true
			}
		}
	}
	return "", "", false
}

// Upstream cost fields that map to an AIPlex meter, by base name.
var scalarFields = map[string]func(*pricecatalog.Rates, string){
	"input_cost_per_token":                      func(r *pricecatalog.Rates, v string) { r.Input = v },
	"output_cost_per_token":                     func(r *pricecatalog.Rates, v string) { r.Output = v },
	"cache_read_input_token_cost":               func(r *pricecatalog.Rates, v string) { r.CacheRead = v },
	"cache_creation_input_token_cost":           func(r *pricecatalog.Rates, v string) { r.CacheWrite = v },
	"cache_creation_input_token_cost_above_1hr": func(r *pricecatalog.Rates, v string) { r.CacheWrite1h = v },
	"input_cost_per_audio_token":                func(r *pricecatalog.Rates, v string) { r.InputAudio = v },
	"input_cost_per_image_token":                func(r *pricecatalog.Rates, v string) { r.InputImage = v },
	"input_cost_per_video_token":                func(r *pricecatalog.Rates, v string) { r.InputVideo = v },
	"output_cost_per_audio_token":               func(r *pricecatalog.Rates, v string) { r.OutputAudio = v },
	"output_cost_per_image_token":               func(r *pricecatalog.Rates, v string) { r.OutputImage = v },
}

// modalityFields are the scalar fields that price a token modality; they are
// imported only for providers whose responses report that modality split.
var modalityFields = map[string]bool{
	"input_cost_per_audio_token": true, "input_cost_per_image_token": true, "input_cost_per_video_token": true,
	"output_cost_per_audio_token": true, "output_cost_per_image_token": true,
}

// modalityProviders report per-modality token usage that the ledger holds
// (OpenAI and Azure *_tokens_details, Gemini promptTokensDetails).
var modalityProviders = map[string]bool{"openai": true, "azure-openai": true, "gemini-developer": true, "google": true}

// serviceTierProviders report the service tier a request was served on, so a
// priority or flex price can be selected by what actually happened (OpenAI
// service_tier; Gemini usageMetadata.serviceTier).
var serviceTierProviders = map[string]bool{"openai": true, "gemini-developer": true, "google": true}

// confirmedTiers lists, per catalogue provider, the long-context thresholds
// (input tokens) whose billing rule was confirmed from the provider's own
// documentation: the higher rates apply to the WHOLE request once the total
// input exceeds the threshold, and cached tokens count toward it.
//
//   - anthropic: "a request whose prompt is over 100,000 tokens pays higher
//     prices. A request's prompt length counts all of its input tokens,
//     including cache reads and cache writes. Each request is priced on its
//     own" (https://platform.claude.com/docs/en/about-claude/pricing#long-context-pricing,
//     Claude Haiku 5.5).
//   - openai: "prompts with >272K input tokens are priced at 2x input and 1.5x
//     output for the full session" (https://developers.openai.com/api/docs/models/gpt-5.5).
//     272K is read as 272,000 (the 400K window splits as 272K input + 128K
//     output).
//
// Not confirmed, so tier fields stay unimported for these (the entry then
// needs --accept-partial): Gemini (the pricing page labels "prompts <= 200k
// tokens" and "> 200k" but does not say whether the higher rate covers the
// whole prompt or whether cached tokens count), xAI ("reaches the listed
// threshold" against the catalogue's "above"), Azure OpenAI, Bedrock and
// Vertex-hosted Claude (their own pages were not confirmed).
var confirmedTiers = map[string][]int64{
	"anthropic": {100_000},
	"openai":    {272_000},
}

var (
	tierRE    = regexp.MustCompile(`^(.*)_above_([0-9]+)k_tokens(_batches|_priority|_flex)?$`)
	variantRE = regexp.MustCompile(`^(.*?)(_batches|_priority|_flex)?$`)
)

const (
	vStandard = ""
	vBatch    = "_batches"
	vPriority = "_priority"
	vFlex     = "_flex"
)

// parseField splits an upstream cost field into its base name, rate variant
// and long-context threshold (0 when untiered).
func parseField(field string) (base, variant string, above int64) {
	if m := tierRE.FindStringSubmatch(field); m != nil {
		var n int64
		fmt.Sscanf(m[2], "%d", &n)
		return m[1], m[3], n * 1000
	}
	m := variantRE.FindStringSubmatch(field)
	return m[1], m[2], 0
}

func isNumber(v any) (json.Number, bool) {
	n, ok := v.(json.Number)
	return n, ok
}

// classify decides what an upstream cost field that was not imported means for
// an import: blocking changes what a request costs and the price cannot say
// so.
func classify(field string) (blocking bool) {
	for _, suffix := range []string{"_priority", "_flex", "_ultrafast", "_balanced", "_decisions"} {
		if strings.HasSuffix(field, suffix) {
			return false // another service tier; the standard tier is unaffected
		}
	}
	if strings.Contains(field, "_above_1hr") || strings.Contains(field, "_above_12hr") {
		return false // longer cache lifetime, opt-in
	}
	if tierRE.MatchString(field) {
		return true // context-length tier: the base rate stops applying
	}
	for _, m := range []string{"audio_token", "image_token", "video_token", "cache_hit", "explicit_cache"} {
		if strings.Contains(field, m) {
			return true // token rates for a modality or cache kind the price does not carry
		}
	}
	return false
}

func costLike(field string) bool {
	return strings.Contains(field, "cost") || strings.Contains(field, "dbu")
}

func intField(v any) int64 {
	n, ok := isNumber(v)
	if !ok {
		return 0
	}
	i, err := n.Int64()
	if err != nil || i < 0 {
		return 0
	}
	return i
}

// exactDecimal renders a JSON number's text as an exact plain decimal.
func exactDecimal(text string) (string, bool) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(text))
	if !ok || r.Sign() <= 0 {
		return "", false
	}
	for digits := 0; digits <= 12; digits++ {
		str := r.FloatString(digits)
		if back, ok := new(big.Rat).SetString(str); ok && back.Cmp(r) == 0 {
			return str, true
		}
	}
	return "", false
}

type variantRates struct {
	base  pricecatalog.Rates
	tiers map[int64]*pricecatalog.Rates
	// fields lists the upstream fields this variant read, for the notes.
	fields map[string][]string // "scalar" and "tier" lists
}

func scalarKeys(r pricecatalog.Rates) string { return r.Signature() }

// buildEntry converts one upstream record. ok is false when the record is not
// importable at all (no token rate).
func buildEntry(provider, model, key string, raw map[string]any) (pricecatalog.Entry, bool) {
	mode, _ := raw["mode"].(string)
	e := pricecatalog.Entry{Provider: provider, Model: model, Mode: mode, Upstream: key}
	e.ContextWindow = intField(raw["max_input_tokens"])
	e.MaxOutputTokens = intField(raw["max_output_tokens"])
	if s, ok := raw["source"].(string); ok && strings.HasPrefix(s, "https://") && !strings.ContainsAny(s, " \r\n") {
		e.Source = s
	}
	blocking, leftOut := map[string]bool{}, map[string]bool{}
	variants := map[string]*variantRates{}
	get := func(v string) *variantRates {
		if variants[v] == nil {
			variants[v] = &variantRates{tiers: map[int64]*pricecatalog.Rates{}, fields: map[string][]string{}}
		}
		return variants[v]
	}
	fieldNames := make([]string, 0, len(raw))
	for field := range raw {
		fieldNames = append(fieldNames, field)
	}
	sort.Strings(fieldNames)
	var unconsumed []string // cost-like fields not read into a rate
	consumed := map[string]string{}
	for _, field := range fieldNames {
		if !costLike(field) {
			continue
		}
		base, variant, above := parseField(field)
		setter, scalar := scalarFields[base]
		if !scalar || (modalityFields[base] && !modalityProviders[provider]) {
			unconsumed = append(unconsumed, field)
			continue
		}
		v, present := raw[field]
		if !present || v == nil {
			continue
		}
		n, ok := isNumber(v)
		if !ok {
			blocking["invalid:"+field] = true
			continue
		}
		dec, err := pricecatalog.MtokDecimal(n.String())
		if err != nil {
			blocking["invalid:"+field] = true
			continue
		}
		vr := get(variant)
		if above == 0 {
			setter(&vr.base, dec)
		} else {
			t := vr.tiers[above]
			if t == nil {
				t = &pricecatalog.Rates{}
				vr.tiers[above] = t
			}
			setter(t, dec)
		}
		consumed[field] = variant
	}
	std := variants[vStandard]
	if std == nil || (std.base.Input == "" && std.base.Output == "") {
		return e, false
	}
	// attach reads a variant's tiers into its Rates when every threshold is
	// confirmed for the provider and every tier prices exactly the scalar
	// meters of the base; otherwise it leaves the tiers out and reports false.
	attach := func(vr *variantRates) bool {
		if len(vr.tiers) == 0 {
			return true
		}
		thresholds := make([]int64, 0, len(vr.tiers))
		for above := range vr.tiers {
			thresholds = append(thresholds, above)
		}
		sort.Slice(thresholds, func(i, j int) bool { return thresholds[i] < thresholds[j] })
		confirmed := map[int64]bool{}
		for _, above := range confirmedTiers[provider] {
			confirmed[above] = true
		}
		for _, above := range thresholds {
			if !confirmed[above] || scalarKeys(*vr.tiers[above]) != scalarKeys(vr.base) {
				return false
			}
		}
		for _, above := range thresholds {
			vr.base.Tiers = append(vr.base.Tiers, pricecatalog.Tier{Above: above, Rates: *vr.tiers[above]})
		}
		return true
	}
	tiersOK := map[string]bool{}
	for name, vr := range variants {
		tiersOK[name] = attach(vr)
	}
	// Priority and flex variants are dropped whole unless they can be imported
	// exactly: their fields are left out, and a request on that tier finds no
	// price (unknown). Standard and batch rates are always imported; their
	// tier fields are carried only when the provider's rule is confirmed.
	usableTier := func(name string) bool {
		vr := variants[name]
		if vr == nil || vr.base.Empty() || !tiersOK[name] || !serviceTierProviders[provider] || scalarKeys(vr.base) != scalarKeys(std.base) {
			return false
		}
		if len(std.tiers) > 0 && !tiersOK[vStandard] {
			return false
		}
		if len(vr.base.Tiers) != len(std.base.Tiers) {
			return false
		}
		for i, t := range vr.base.Tiers {
			if t.Above != std.base.Tiers[i].Above {
				return false
			}
		}
		return true
	}
	e.Rates = std.base
	if b := variants[vBatch]; b != nil && !b.base.Empty() {
		batch := b.base
		e.Batch = &batch
	}
	if usableTier(vPriority) {
		b := variants[vPriority].base
		e.Priority = &b
	}
	if usableTier(vFlex) {
		b := variants[vFlex].base
		e.Flex = &b
	}
	// Fields that were read but not carried into the entry.
	for field, variant := range consumed {
		_, _, above := parseField(field)
		carried := false
		switch variant {
		case vStandard, vBatch:
			carried = above == 0 || tiersOK[variant]
		default:
			carried = usableTier(variant)
		}
		if !carried {
			unconsumed = append(unconsumed, field)
		}
	}
	for _, field := range unconsumed {
		if field == "search_context_cost_per_query" {
			if tool, ok := serverToolPrice(provider, raw[field]); ok {
				e.ServerTools = map[string]string{"web_search": tool}
				continue
			}
		}
		if classify(field) {
			blocking[field] = true
		} else {
			leftOut[field] = true
		}
	}
	e.Blocking, e.LeftOut = sortedKeys(blocking), sortedKeys(leftOut)
	return e, true
}

// serverToolPrice returns the exact per-call price of web search for the
// providers whose response reports the call count and whose per-call price
// does not depend on the context size: Anthropic, whose pricing page states
// "$10 USD per 1,000 searches" and whose usage reports server_tool_use
// .web_search_requests. Other providers are not imported: OpenAI chat search
// models do not report calls, and Gemini grounding has monthly free requests
// and per-prompt billing on older models.
func serverToolPrice(provider string, v any) (string, bool) {
	if provider != "anthropic" {
		return "", false
	}
	sizes, ok := v.(map[string]any)
	if !ok || len(sizes) == 0 {
		return "", false
	}
	price := ""
	for _, size := range sizes {
		n, ok := isNumber(size)
		if !ok {
			return "", false
		}
		dec, ok := exactDecimal(n.String())
		if !ok || (price != "" && dec != price) {
			return "", false
		}
		price = dec
	}
	return price, price != ""
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// skipped lists the ids left out of the last build because upstream stated two
// different prices for them.
var skipped []string

// build converts the upstream document into a snapshot.
func build(upstream []byte, meta pricecatalog.Meta) (pricecatalog.Snapshot, error) {
	dec := json.NewDecoder(bytes.NewReader(upstream))
	dec.UseNumber()
	var doc map[string]json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		return pricecatalog.Snapshot{}, fmt.Errorf("decode upstream: %w", err)
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	byID := map[string]pricecatalog.Entry{}
	var conflicts []string
	for _, key := range keys {
		var raw map[string]any
		d := json.NewDecoder(bytes.NewReader(doc[key]))
		d.UseNumber()
		if d.Decode(&raw) != nil {
			continue
		}
		lp, _ := raw["litellm_provider"].(string)
		mode, _ := raw["mode"].(string)
		if lp == "" || !modes[mode] {
			continue
		}
		provider, model, ok := mapKey(lp, key)
		if !ok {
			continue
		}
		e, ok := buildEntry(provider, model, key, raw)
		if !ok {
			continue
		}
		if prior, dup := byID[e.ID()]; dup {
			a, b := prior, e
			a.Upstream, b.Upstream = "", ""
			ja, _ := json.Marshal(a)
			jb, _ := json.Marshal(b)
			if !bytes.Equal(ja, jb) {
				conflicts = append(conflicts, fmt.Sprintf("%s (%s vs %s)", e.ID(), prior.Upstream, key))
			}
			continue // identical duplicate: keep the first key in sorted order
		}
		byID[e.ID()] = e
	}
	// Upstream rows that map to one id but state different prices are
	// ambiguous: the id is left out of the snapshot (unknown, not a guess).
	for _, c := range conflicts {
		id, _, _ := strings.Cut(c, " (")
		delete(byID, id)
	}
	skipped = conflicts
	ids := map[string]string{}
	entries := make([]pricecatalog.Entry, 0, len(byID))
	for _, e := range byID {
		if prior, dup := ids[pricecatalog.PriceID(e)]; dup {
			return pricecatalog.Snapshot{}, fmt.Errorf("%s and %s map to one price id %s", prior, e.ID(), pricecatalog.PriceID(e))
		}
		ids[pricecatalog.PriceID(e)] = e.ID()
		entries = append(entries, e)
	}
	return pricecatalog.Snapshot{Meta: meta, Entries: entries}, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
