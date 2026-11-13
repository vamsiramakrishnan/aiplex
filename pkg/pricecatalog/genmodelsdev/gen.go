package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/vamsiramakrishnan/aiplex/pkg/pricecatalog"
)

// providerDirs maps a models.dev provider directory to the catalogue provider.
// Only providers whose usage records AIPlex can match are read.
var providerDirs = map[string]string{
	"openai":                  "openai",
	"anthropic":               "anthropic",
	"azure":                   "azure-openai",
	"amazon-bedrock":          "bedrock",
	"google":                  "gemini-developer",
	"google-vertex":           "google",
	"google-vertex-anthropic": "vertex-anthropic",
	"deepseek":                "deepseek",
	"groq":                    "groq",
	"mistral":                 "mistral",
	"xai":                     "xai",
}

var vendors = map[string]bool{"deepseek": true, "groq": true, "mistral": true, "xai": true}

// mapID maps a models.dev provider directory and model id (the file path under
// models/, without .toml) to the catalogue provider and the id the provider
// spells natively, with the same exact-id discipline as the LiteLLM source:
// region, commitment and routing variants are other products, and no id is
// ever guessed by fuzzy matching.
func mapID(dir, id string) (provider, model string, ok bool) {
	provider = providerDirs[dir]
	plain := id != "" && !strings.Contains(id, "/")
	switch dir {
	case "openai", "anthropic", "azure", "google":
		return provider, id, plain
	case "amazon-bedrock":
		return provider, id, plain && strings.Contains(id, ".")
	case "google-vertex":
		return provider, id, plain && strings.HasPrefix(id, "gemini-")
	case "google-vertex-anthropic":
		return provider, id, plain && strings.Contains(id, "@")
	}
	if vendors[dir] {
		return provider, id, id != ""
	}
	return "", "", false
}

// exactNumber renders a TOML number as an exact plain decimal. TOML floats are
// read as float64, whose shortest round-trip form is the literal as written
// for any literal of up to 15 significant digits; longer ones are refused.
func exactNumber(v any) (string, bool) {
	switch n := v.(type) {
	case int64:
		if n < 0 {
			return "", false
		}
		return strconv.FormatInt(n, 10), true
	case float64:
		if n < 0 || n != n {
			return "", false
		}
		s := strconv.FormatFloat(n, 'f', -1, 64)
		digits := strings.TrimLeft(strings.ReplaceAll(s, ".", ""), "0")
		if len(digits) > 15 {
			return "", false
		}
		return s, true
	}
	return "", false
}

func table(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

// cacheRates reads {read, write} where write is a number (the default TTL) or a
// table of TTL to number ("5m", "1h").
func cacheRates(v any, r *pricecatalog.Rates, unread map[string]bool, where string) {
	c := table(v)
	for key, value := range c {
		switch key {
		case "read":
			if s, ok := exactNumber(value); ok {
				r.CacheRead = s
				continue
			}
		case "write":
			if s, ok := exactNumber(value); ok {
				r.CacheWrite = s
				continue
			}
			if ttls := table(value); ttls != nil {
				allRead := true
				for ttl, price := range ttls {
					s, ok := exactNumber(price)
					switch {
					case ok && ttl == "5m":
						r.CacheWrite = s
					case ok && ttl == "1h":
						r.CacheWrite1h = s
					default:
						allRead = false
						unread[where+".cache.write."+ttl] = true
					}
				}
				if allRead {
					continue
				}
				continue
			}
		}
		// storage, implicit, explicit and unreadable values carry no rate.
		if key == "read" || key == "write" {
			unread[where+".cache."+key] = true
		}
	}
}

type contextOverride struct {
	above int64
	rates pricecatalog.Rates
}

// overrides reads [[...overrides]] entries whose condition is exactly
// {context = {gt = N}}; every other condition is returned as unread, named by
// its keys.
func overrides(v any, apply func(*pricecatalog.Rates, map[string]any), where string, tiers map[int64]*pricecatalog.Rates, unread map[string]bool) {
	for _, item := range list(v) {
		o := table(item)
		when := table(o["when"])
		ctx := table(when["context"])
		gt, isInt := ctx["gt"].(int64)
		if len(when) == 1 && len(ctx) == 1 && isInt && gt > 0 {
			t := tiers[gt]
			if t == nil {
				t = &pricecatalog.Rates{}
				tiers[gt] = t
			}
			apply(t, o)
			continue
		}
		keys := make([]string, 0, len(when))
		for k := range when {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		unread[where+".overrides[when:"+strings.Join(keys, ",")+"]"] = true
	}
}

func scalarKeys(r pricecatalog.Rates) string { return r.Signature() }

// convert reads one provider model file.
func convert(provider, model, rel, sourceURL string, doc map[string]any) (pricecatalog.Entry, bool) {
	e := pricecatalog.Entry{Provider: provider, Model: model, Mode: "chat", Upstream: rel, Source: sourceURL}
	if limit := table(doc["limit"]); limit != nil {
		if n, ok := limit["context"].(int64); ok && n > 0 {
			e.ContextWindow = n
		}
		if n, ok := limit["output"].(int64); ok && n > 0 {
			e.MaxOutputTokens = n
		}
	}
	cost := table(doc["cost"])
	text := table(cost["text"])
	in, out := table(text["input"]), table(text["output"])
	if in == nil && out == nil {
		return e, false
	}
	unread := map[string]bool{}
	if s, ok := exactNumber(in["price"]); ok {
		e.Rates.Input = s
	}
	if s, ok := exactNumber(out["price"]); ok {
		e.Rates.Output = s
	}
	cacheRates(in["cache"], &e.Rates, unread, "cost.text.input")
	if e.Rates.Input == "" && e.Rates.Output == "" {
		return e, false
	}
	tiers := map[int64]*pricecatalog.Rates{}
	overrides(in["overrides"], func(r *pricecatalog.Rates, o map[string]any) {
		if s, ok := exactNumber(o["price"]); ok {
			r.Input = s
		}
		cacheRates(o["cache"], r, unread, "cost.text.input.overrides")
	}, "cost.text.input", tiers, unread)
	overrides(out["overrides"], func(r *pricecatalog.Rates, o map[string]any) {
		if s, ok := exactNumber(o["price"]); ok {
			r.Output = s
		}
	}, "cost.text.output", tiers, unread)
	// Tiers are carried only where the provider's rule is confirmed and each
	// tier prices exactly the meters of the base (see pricecatalog.ConfirmedTiers).
	thresholds := make([]int64, 0, len(tiers))
	for above := range tiers {
		thresholds = append(thresholds, above)
	}
	sort.Slice(thresholds, func(i, j int) bool { return thresholds[i] < thresholds[j] })
	carry := len(thresholds) > 0
	for _, above := range thresholds {
		if !pricecatalog.TierConfirmed(provider, above) || scalarKeys(*tiers[above]) != scalarKeys(e.Rates) {
			carry = false
		}
	}
	blocking, leftOut := map[string]bool{}, map[string]bool{}
	if carry {
		for _, above := range thresholds {
			e.Rates.Tiers = append(e.Rates.Tiers, pricecatalog.Tier{Above: above, Rates: *tiers[above]})
		}
	} else {
		for _, above := range thresholds {
			blocking["cost.text.overrides[context>"+strconv.FormatInt(above, 10)+"]"] = true
		}
	}
	for field := range unread {
		if strings.Contains(field, "when:context") {
			blocking[field] = true // a context condition other than "greater than N"
		} else {
			leftOut[field] = true
		}
	}
	// Everything else the file prices that this import does not carry.
	for name, v := range cost {
		switch name {
		case "text":
			for k := range text {
				if k != "input" && k != "output" {
					leftOut["cost.text."+k] = true
				}
			}
		case "web_search":
			ws := table(v)
			price, ok := exactNumber(ws["price"])
			if ok && pricecatalog.WebSearchPerCallProviders[provider] && len(list(ws["overrides"])) == 0 {
				if per, ok := perThousand(price); ok {
					e.ServerTools = map[string]string{"web_search": per}
					continue
				}
			}
			leftOut["cost.web_search"] = true
		case "multipliers":
			for _, m := range list(v) {
				keys := []string{}
				for k := range table(table(m)["when"]) {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				leftOut["cost.multipliers[when:"+strings.Join(keys, ",")+"]"] = true
			}
		case "audio", "image", "video":
			blocking["cost."+name] = true // token or unit rates for a modality
		default:
			leftOut["cost."+name] = true
		}
	}
	e.Blocking, e.LeftOut = slices.Sorted(maps.Keys(blocking)), slices.Sorted(maps.Keys(leftOut))
	return e, true
}

// perThousand converts a price per 1,000 calls to an exact price per call.
func perThousand(price string) (string, bool) {
	r, ok := new(big.Rat).SetString(price)
	if !ok || r.Sign() <= 0 {
		return "", false
	}
	r.Quo(r, big.NewRat(1000, 1))
	for digits := 0; digits <= 12; digits++ {
		s := r.FloatString(digits)
		if back, ok := new(big.Rat).SetString(s); ok && back.Cmp(r) == 0 {
			return s, true
		}
	}
	return "", false
}

// skipped lists ids left out because two files stated different prices.
var skipped []string

// build converts the provider model files (path to contents).
func build(files map[string][]byte, meta pricecatalog.Meta) (pricecatalog.Snapshot, error) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	byID := map[string]pricecatalog.Entry{}
	var conflicts []string
	for _, rel := range paths {
		parts := strings.Split(rel, "/")
		if len(parts) < 4 || parts[0] != "providers" || parts[2] != "models" || !strings.HasSuffix(rel, ".toml") {
			continue
		}
		dir := parts[1]
		id := strings.TrimSuffix(strings.Join(parts[3:], "/"), ".toml")
		provider, model, ok := mapID(dir, id)
		if !ok {
			continue
		}
		var doc map[string]any
		if err := toml.Unmarshal(files[rel], &doc); err != nil {
			return pricecatalog.Snapshot{}, fmt.Errorf("%s: %w", rel, err)
		}
		source := strings.TrimSuffix(meta.SourceURL, "/providers") + "/" + rel
		source = strings.Replace(source, "/tree/", "/blob/", 1)
		e, ok := convert(provider, model, rel, source, doc)
		if !ok {
			continue
		}
		if prior, dup := byID[e.ID()]; dup {
			a, b := prior, e
			a.Upstream, b.Upstream, a.Source, b.Source = "", "", "", ""
			if fmt.Sprintf("%+v", a) != fmt.Sprintf("%+v", b) {
				conflicts = append(conflicts, fmt.Sprintf("%s (%s vs %s)", e.ID(), prior.Upstream, rel))
			}
			continue
		}
		byID[e.ID()] = e
	}
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

// inputDigest is a stable digest of the files the snapshot was built from:
// every provider model file's path and content hash, sorted.
func inputDigest(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b bytes.Buffer
	for _, p := range paths {
		fmt.Fprintf(&b, "%s %s\n", p, sha256Hex(files[p]))
	}
	return sha256Hex(b.Bytes())
}
