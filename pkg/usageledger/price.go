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
)

var priceMeters = map[string]bool{MeterInput: true, MeterOutput: true, MeterCacheRead: true, MeterCacheWrite: true, MeterReasoning: true, MeterRequests: true}

// Rational is Numerator fixed-point money units (at the price's scale) per
// Denominator raw units, the same convention as QuotaPlex unit prices. A zero
// numerator is a declared free meter, distinct from an unpriced one.
type Rational struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
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
}

// Validate checks a price spec.
func (p Price) Validate() error {
	if !validText(p.ID, true) || !validText(p.ModelID, true) || !validText(p.Provider, false) {
		return invalid("price id and model_id required")
	}
	if !ValidCurrency(p.Currency) || p.Scale > MaxScale {
		return invalid("price needs a three-letter uppercase currency and scale up to %d", MaxScale)
	}
	if len(p.Meters) == 0 {
		return invalid("price needs at least one meter")
	}
	for name, rate := range p.Meters {
		if !priceMeters[name] {
			return invalid("unknown price meter %q", name)
		}
		if rate.Numerator < 0 || rate.Denominator <= 0 {
			return invalid("price meter %s needs a nonnegative numerator and positive denominator", name)
		}
	}
	if p.EffectiveFrom != nil && p.EffectiveUntil != nil && !p.EffectiveUntil.After(*p.EffectiveFrom) {
		return invalid("effective_until must be after effective_from")
	}
	return nil
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
	return true
}

// MatchPrice selects the one price for a record. A provider-specific price
// wins over a provider-agnostic one; any remaining ambiguity yields no match
// rather than a guess.
func MatchPrice(prices []Price, r Record) (Price, bool) {
	var specific, generic []Price
	for _, p := range prices {
		if p.Validate() != nil || !p.applies(r) {
			continue
		}
		if p.Provider != "" {
			specific = append(specific, p)
		} else {
			generic = append(generic, p)
		}
	}
	if len(specific) > 0 {
		generic = specific
	}
	if len(generic) != 1 {
		return Price{}, false
	}
	return generic[0], true
}

// Estimate prices a record's known usage exactly: it sums rational terms and
// rounds up once. It returns an unknown cost when no single price matches,
// when a priced meter's quantity is unknown, or when a known non-zero
// quantity has no price. Prices are never invented.
func Estimate(prices []Price, r Record) Cost {
	p, ok := MatchPrice(prices, r)
	if !ok {
		return UnknownCost()
	}
	quantities := map[string]*int64{MeterRequests: Int64(r.Requests)}
	if r.Usage != nil {
		quantities[MeterOutput] = r.Usage.OutputTokens
		quantities[MeterCacheRead] = r.Usage.CacheReadTokens
		quantities[MeterCacheWrite] = r.Usage.CacheWriteTokens
		quantities[MeterReasoning] = r.Usage.ReasoningTokens
		quantities[MeterInput] = r.Usage.InputTokens
		// input_tokens is total input: cache reads and cache writes are inside
		// it. A separately priced cache subset is removed from the input
		// quantity so it is charged once, at its own rate.
		for _, subset := range []struct {
			meter string
			value *int64
		}{{MeterCacheRead, r.Usage.CacheReadTokens}, {MeterCacheWrite, r.Usage.CacheWriteTokens}} {
			if _, priced := p.Meters[subset.meter]; !priced || quantities[MeterInput] == nil {
				continue
			}
			input := quantities[MeterInput]
			quantities[MeterInput] = nil
			if subset.value != nil && *subset.value <= *input {
				quantities[MeterInput] = Int64(*input - *subset.value)
			}
		}
	}
	// Every known, non-zero quantity must be declared. Cached input is part of
	// input_tokens unless separately priced, so it is covered either way.
	for _, meter := range []string{MeterInput, MeterOutput, MeterCacheWrite, MeterReasoning} {
		if q := quantities[meter]; q != nil && *q > 0 {
			if _, priced := p.Meters[meter]; !priced {
				return UnknownCost()
			}
		}
	}
	if q := quantities[MeterCacheRead]; q != nil && *q > 0 {
		_, cachePriced := p.Meters[MeterCacheRead]
		_, inputPriced := p.Meters[MeterInput]
		if !cachePriced && !inputPriced {
			return UnknownCost()
		}
	}
	total := new(big.Rat)
	for meter, rate := range p.Meters {
		q := quantities[meter]
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
	if remainder.Sign() > 0 {
		whole.Add(whole, big.NewInt(1))
	}
	return Cost{Basis: BasisEstimated, Currency: p.Currency, Amount: whole.String(), Scale: p.Scale}
}
