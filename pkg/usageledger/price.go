package usageledger

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"time"
)

// PriceKind is the tenant resource kind holding operator-configured prices.
// There is no built-in price list: without a matching price, cost is unknown.
const PriceKind = "model-price"

// Price meters. input_tokens is total input; when cache_read_tokens is also
// priced, input_tokens is charged only for the uncached remainder.
// reasoning_tokens is charged in addition to output_tokens, so configure it
// only for providers that report reasoning separately (a zero price declares
// it included). requests is one per record.
const (
	MeterInput      = "input_tokens"
	MeterOutput     = "output_tokens"
	MeterCacheRead  = "cache_read_tokens"
	MeterCacheWrite = "cache_write_tokens"
	MeterReasoning  = "reasoning_tokens"
	MeterRequests   = "requests"
	// MeterCacheWrite1h is the part of cache_write_tokens written with the
	// one-hour TTL; when priced, cache_write_tokens is charged only for the
	// remainder. MeterToolUsePrompt is the Gemini tool-use prompt tokens
	// (separate from input_tokens). Per-call server-tool meters are named
	// "server_tool.<kind>" (see server_tools.go).
	MeterCacheWrite1h  = CounterCacheWrite1h
	MeterToolUsePrompt = CounterToolUsePrompt
)

// Limits on tiers.
const MaxPriceTiers = 8

var priceMeters = func() map[string]bool {
	meters := map[string]bool{MeterInput: true, MeterOutput: true, MeterCacheRead: true, MeterCacheWrite: true, MeterReasoning: true, MeterRequests: true}
	// Modality token splits are subsets of input/output tokens: a priced split
	// is charged at its own rate and removed from its parent (see Estimate).
	// Unit meters (milliseconds, image counts) are independent quantities.
	for _, name := range heldCounters() {
		meters[name] = true
	}
	return meters
}()

// Rational is Numerator fixed-point money units (at the price's scale) per
// Denominator raw units, the same convention as QuotaPlex unit prices. A zero
// numerator is a declared free meter, distinct from an unpriced one.
type Rational struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

// PriceTier replaces the price's meters for a request whose input is long.
// It applies when the record's input_tokens (the ledger's total input,
// including cache reads and writes) is greater than AboveInputTokens, and then
// prices the WHOLE request at Meters, not only the tokens above the threshold.
// That is the rule of the providers whose long-context prices the catalogue
// imports (see docs/usage-ledger.md); a provider that charges only the excess
// cannot be expressed. Meters must name exactly the meters of the base price,
// so a meter never switches between priced and unpriced. When several tiers
// apply, the one with the highest threshold wins. A record whose input_tokens
// is unknown cannot select a tier, so its cost is unknown.
type PriceTier struct {
	AboveInputTokens int64               `json:"above_input_tokens"`
	Meters           map[string]Rational `json:"meters"`
}

// PriceSource records where a price came from when it was imported from a
// third-party catalogue rather than written by an operator. It is provenance
// only: it never changes how a price matches or how cost is computed.
type PriceSource struct {
	Catalog     string `json:"catalog"`
	Commit      string `json:"commit,omitempty"`
	RetrievedAt string `json:"retrieved_at,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
}

// Price is the spec of a model-price resource.
type Price struct {
	ID             string              `json:"id"`
	ModelID        string              `json:"model_id"`
	Provider       string              `json:"provider,omitempty"`
	Currency       string              `json:"currency"`
	Scale          uint8               `json:"scale"`
	Meters         map[string]Rational `json:"meters"`
	EffectiveFrom  *time.Time          `json:"effective_from,omitempty"`
	EffectiveUntil *time.Time          `json:"effective_until,omitempty"`
	// Optional selectors. Batch true matches only batch exchanges and false
	// only non-batch ones; ImageSize and ImageQuality match the record's
	// reported image dimensions. A price without a selector matches any value;
	// among matching prices the one with the most selectors wins.
	Batch        *bool  `json:"batch,omitempty"`
	ImageSize    string `json:"image_size,omitempty"`
	ImageQuality string `json:"image_quality,omitempty"`
	// ServiceTier matches the tier the provider reported ("flex", "priority").
	// A price without it matches only standard-tier exchanges: no tier
	// reported, or default, standard or batch (see StandardServiceTier).
	ServiceTier string `json:"service_tier,omitempty"`
	// Tiers are context-length overrides of Meters (see PriceTier).
	Tiers []PriceTier `json:"tiers,omitempty"`
	// Source is optional provenance for imported prices.
	Source *PriceSource `json:"source,omitempty"`
}

// Validate checks a price spec.
func (p Price) Validate() error {
	if !validText(p.ID, true) || !validText(p.ModelID, true) || !validText(p.Provider, false) || !validText(p.ImageSize, false) || !validText(p.ImageQuality, false) || !validText(p.ServiceTier, false) {
		return invalid("price id and model_id required")
	}
	if !ValidCurrency(p.Currency) || p.Scale > MaxScale {
		return invalid("price needs a three-letter uppercase currency and scale up to %d", MaxScale)
	}
	if p.Source != nil && (!validText(p.Source.Catalog, true) || !validText(p.Source.Commit, false) || !validText(p.Source.RetrievedAt, false) || !validText(p.Source.SourceURL, false)) {
		return invalid("price source needs a catalogue name and plain-text fields")
	}
	if len(p.Meters) == 0 {
		return invalid("price needs at least one meter")
	}
	if err := validateMeters(p.Meters); err != nil {
		return err
	}
	if _, priced := p.Meters[MeterCacheWrite1h]; priced {
		if _, parent := p.Meters[MeterCacheWrite]; !parent {
			return invalid("cache_write_1h_tokens needs cache_write_tokens to be priced too")
		}
	}
	if len(p.Tiers) > MaxPriceTiers {
		return invalid("a price takes at most %d tiers", MaxPriceTiers)
	}
	for i, tier := range p.Tiers {
		if tier.AboveInputTokens <= 0 || (i > 0 && tier.AboveInputTokens <= p.Tiers[i-1].AboveInputTokens) {
			return invalid("price tiers need positive, strictly increasing above_input_tokens")
		}
		if err := validateMeters(tier.Meters); err != nil {
			return err
		}
		if len(tier.Meters) != len(p.Meters) {
			return invalid("a price tier must price exactly the meters of the base price")
		}
		for name := range tier.Meters {
			if _, ok := p.Meters[name]; !ok {
				return invalid("a price tier must price exactly the meters of the base price")
			}
		}
	}
	if p.EffectiveFrom != nil && p.EffectiveUntil != nil && !p.EffectiveUntil.After(*p.EffectiveFrom) {
		return invalid("effective_until must be after effective_from")
	}
	return nil
}

func validateMeters(meters map[string]Rational) error {
	if len(meters) == 0 {
		return invalid("price needs at least one meter")
	}
	for name, rate := range meters {
		if _, tool := serverToolKindOfMeter(name); !priceMeters[name] && !tool {
			return invalid("unknown price meter %q", name)
		}
		if rate.Numerator < 0 || rate.Denominator <= 0 {
			return invalid("price meter %s needs a nonnegative numerator and positive denominator", name)
		}
	}
	return nil
}

// StandardServiceTier reports whether a reported service tier is the standard
// one a price without a service_tier selector covers.
func StandardServiceTier(tier string) bool {
	switch tier {
	case "", "default", "standard", "batch":
		return true
	}
	return false
}

// DecodePrice strictly decodes a stored or submitted spec for resource id.
func DecodePrice(id string, raw []byte) (Price, error) {
	var p Price
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return Price{}, invalid("price spec: %v", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Price{}, invalid("trailing price specification")
	}
	if p.ID != id {
		return Price{}, invalid("price id must match the resource id")
	}
	return p, p.Validate()
}

func (p Price) applies(r Record) bool {
	if p.ModelID != r.ModelID || r.ModelID == "" {
		return false
	}
	if p.Provider != "" && p.Provider != r.Provider {
		return false
	}
	if p.EffectiveFrom != nil && r.StartedAt.Before(*p.EffectiveFrom) {
		return false
	}
	if p.EffectiveUntil != nil && !r.StartedAt.Before(*p.EffectiveUntil) {
		return false
	}
	if p.Batch != nil && *p.Batch != r.Batch {
		return false
	}
	if (p.ImageSize != "" && p.ImageSize != r.ImageSize) || (p.ImageQuality != "" && p.ImageQuality != r.ImageQuality) {
		return false
	}
	if p.ServiceTier == "" && !StandardServiceTier(r.ServiceTier) {
		return false
	}
	if p.ServiceTier != "" && p.ServiceTier != r.ServiceTier {
		return false
	}
	return true
}

// specificity counts the selectors a price narrows by.
func (p Price) specificity() int {
	n := 0
	for _, set := range []bool{p.Provider != "", p.Batch != nil, p.ImageSize != "", p.ImageQuality != "", p.ServiceTier != ""} {
		if set {
			n++
		}
	}
	return n
}

// MatchPrice selects the one price for a record. The price with the most
// matching selectors (provider, batch, image dimensions) wins over a less
// specific one; any remaining ambiguity yields no match rather than a guess.
func MatchPrice(prices []Price, r Record) (Price, bool) {
	best, bestScore := []Price{}, -1
	for _, p := range prices {
		if p.Validate() != nil || !p.applies(r) {
			continue
		}
		switch score := p.specificity(); {
		case score > bestScore:
			best, bestScore = []Price{p}, score
		case score == bestScore:
			best = append(best, p)
		}
	}
	if len(best) != 1 {
		return Price{}, false
	}
	return best[0], true
}

// Estimate prices a record's known usage exactly: it sums rational terms and
// rounds up once, at the price's scale (the amount is the smallest integer of
// minor units not below the exact value; when it rounded, Cost.Exact keeps the
// exact fraction so summaries can add exact values before rounding). It returns
// an unknown cost when no single price matches, when a priced meter's quantity
// is unknown, or when a known non-zero quantity has no price. Prices are never
// invented.
func Estimate(prices []Price, r Record) Cost {
	p, ok := MatchPrice(prices, r)
	if !ok {
		return UnknownCost()
	}
	return EstimateWith(p, r)
}

// EstimateWith prices a record with one chosen price.
func EstimateWith(p Price, r Record) Cost {
	meters := p.Meters
	for _, tier := range p.Tiers {
		// Tiers are validated ascending: the last one passed wins. The
		// threshold counts the ledger's total input (cache reads and writes
		// included), and an unknown input cannot select a tier.
		if r.Usage == nil || r.Usage.InputTokens == nil {
			return UnknownCost()
		}
		if *r.Usage.InputTokens > tier.AboveInputTokens {
			meters = tier.Meters
		}
	}
	quantities := map[string]*int64{MeterRequests: Int64(r.Requests)}
	if r.Usage != nil {
		quantities[MeterOutput] = r.Usage.OutputTokens
		quantities[MeterCacheRead] = r.Usage.CacheReadTokens
		quantities[MeterCacheWrite] = r.Usage.CacheWriteTokens
		quantities[MeterReasoning] = r.Usage.ReasoningTokens
		quantities[MeterInput] = r.Usage.InputTokens
		for _, name := range append(append([]string{}, ModalityCounters...), UnitCounters...) {
			quantities[name] = r.Usage.UnitCounter(name)
		}
		quantities[MeterCacheWrite1h] = r.Usage.UnitCounter(CounterCacheWrite1h)
		// A zero cache-write total means every TTL bucket is zero.
		if w := r.Usage.CacheWriteTokens; w != nil && *w == 0 && quantities[MeterCacheWrite1h] == nil {
			quantities[MeterCacheWrite1h] = Int64(0)
		}
		// A price that states a non-text rate for one modality of a direction
		// bills the modalities separately, so every other non-text modality
		// the record reports must be priced too. A flat price (no modality
		// meter) covers all modalities at its parent rate.
		for _, group := range [][]string{InputModalityCounters, OutputModalityCounters} {
			aware := false
			for _, name := range group {
				if _, priced := meters[name]; priced && !textModality(name) {
					aware = true
				}
			}
			if !aware {
				continue
			}
			for _, name := range group {
				if _, priced := meters[name]; priced || textModality(name) {
					continue
				}
				if q := quantities[name]; q != nil && *q > 0 {
					return UnknownCost()
				}
			}
		}
		// Cache subsets and input modality splits may overlap (cached audio is
		// both). Pricing both kinds is only exact when the priced cache subsets
		// are known to be zero.
		inputModalityPriced := false
		for _, name := range InputModalityCounters {
			if _, priced := meters[name]; priced {
				inputModalityPriced = true
			}
		}
		if inputModalityPriced {
			for _, cache := range []string{MeterCacheRead, MeterCacheWrite} {
				if _, priced := meters[cache]; priced {
					if q := quantities[cache]; q == nil || *q != 0 {
						return UnknownCost()
					}
				}
			}
		}
		// input_tokens is total input: cache reads and cache writes are inside
		// it, as are input modality splits; output modality splits are inside
		// output_tokens, and one-hour cache writes inside cache_write_tokens.
		// A separately priced subset is removed from its parent so it is
		// charged once, at its own rate.
		subtract := func(parent string, subsets []string) {
			for _, subset := range subsets {
				if _, priced := meters[subset]; !priced || quantities[parent] == nil {
					continue
				}
				whole := quantities[parent]
				quantities[parent] = nil
				if value := quantities[subset]; value != nil && *value <= *whole {
					quantities[parent] = Int64(*whole - *value)
				}
			}
		}
		subtract(MeterInput, append([]string{MeterCacheRead, MeterCacheWrite}, InputModalityCounters...))
		subtract(MeterCacheWrite, []string{MeterCacheWrite1h})
		subtract(MeterOutput, OutputModalityCounters)
	}
	// Every known, non-zero quantity must be declared. Cached input and
	// modality splits are part of their parent unless separately priced, so
	// they are covered either way; units have no parent. One-hour cache writes
	// and tool-use prompt tokens cost differently from their parents, so they
	// must be priced when present.
	for _, meter := range append([]string{MeterInput, MeterOutput, MeterCacheWrite, MeterReasoning, MeterCacheWrite1h}, UnitCounters...) {
		if q := quantities[meter]; q != nil && *q > 0 {
			if _, priced := meters[meter]; !priced {
				return UnknownCost()
			}
		}
	}
	if q := quantities[MeterCacheRead]; q != nil && *q > 0 {
		_, cachePriced := meters[MeterCacheRead]
		_, inputPriced := meters[MeterInput]
		if !cachePriced && !inputPriced {
			return UnknownCost()
		}
	}
	// Server-tool requests are per-call quantities. A reported positive count
	// needs its meter; a priced meter with no reported count contributes
	// nothing, because providers report a tool only when it ran.
	for kind, n := range r.ServerTools {
		if n > 0 {
			if _, priced := meters[ServerToolMeter(kind)]; !priced {
				return UnknownCost()
			}
		}
	}
	total := new(big.Rat)
	for meter, rate := range meters {
		q := quantities[meter]
		if kind, tool := serverToolKindOfMeter(meter); tool {
			n := r.ServerTools[kind]
			q = &n
		}
		if q == nil {
			if rate.Numerator == 0 {
				continue
			}
			return UnknownCost()
		}
		term := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(*q), big.NewInt(rate.Numerator)), big.NewInt(rate.Denominator))
		total.Add(total, term)
	}
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(total.Num(), total.Denom(), remainder)
	cost := Cost{Basis: BasisEstimated, Currency: p.Currency, Scale: p.Scale, PriceID: p.ID}
	if remainder.Sign() > 0 {
		whole.Add(whole, big.NewInt(1))
		cost.Exact = total.String()
	}
	cost.Amount = whole.String()
	return cost
}

func textModality(name string) bool {
	return name == CounterInputTextTokens || name == CounterOutputTextTokens
}
