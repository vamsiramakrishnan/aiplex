package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/pricecatalog"
)

// Modes whose cost is stated per token. Every other LiteLLM mode (image, audio,
// video, rerank, search, OCR, realtime, ...) is priced in units an AIPlex price
// does not carry and is not imported.
var modes = map[string]bool{"chat": true, "completion": true, "responses": true, "embedding": true}

// Upstream fields that map to an AIPlex meter.
const (
	fInput      = "input_cost_per_token"
	fOutput     = "output_cost_per_token"
	fCacheRead  = "cache_read_input_token_cost"
	fCacheWrite = "cache_creation_input_token_cost"
)

var batchFields = map[string]string{
	fInput + "_batches": fInput, fOutput + "_batches": fOutput,
	fCacheRead + "_batches": fCacheRead, fCacheWrite + "_batches": fCacheWrite,
}

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

func isNumber(v any) (json.Number, bool) {
	n, ok := v.(json.Number)
	return n, ok
}

// classify decides what an upstream cost field means for an import.
func classify(field string) (blocking bool) {
	for _, suffix := range []string{"_priority", "_flex", "_ultrafast", "_balanced", "_decisions"} {
		if strings.HasSuffix(field, suffix) {
			return false // another service tier; the standard tier is unaffected
		}
	}
	if strings.Contains(field, "_above_1hr") || strings.Contains(field, "_above_12hr") {
		return false // longer cache lifetime, opt-in
	}
	if strings.Contains(field, "_above_") {
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
	rate := func(field string) string {
		v, present := raw[field]
		if !present || v == nil {
			return ""
		}
		n, ok := isNumber(v)
		if !ok {
			blocking["invalid:"+field] = true
			return ""
		}
		s, err := pricecatalog.MtokDecimal(n.String())
		if err != nil {
			blocking["invalid:"+field] = true
			return ""
		}
		return s
	}
	e.Rates = pricecatalog.Rates{Input: rate(fInput), Output: rate(fOutput), CacheRead: rate(fCacheRead), CacheWrite: rate(fCacheWrite)}
	batch := pricecatalog.Rates{}
	for field, base := range batchFields {
		s := rate(field)
		switch base {
		case fInput:
			batch.Input = s
		case fOutput:
			batch.Output = s
		case fCacheRead:
			batch.CacheRead = s
		case fCacheWrite:
			batch.CacheWrite = s
		}
	}
	if !batch.Empty() {
		e.Batch = &batch
	}
	if e.Rates.Input == "" && e.Rates.Output == "" {
		return e, false
	}
	for field := range raw {
		if !costLike(field) {
			continue
		}
		switch field {
		case fInput, fOutput, fCacheRead, fCacheWrite:
			continue
		}
		if _, handled := batchFields[field]; handled {
			continue
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
