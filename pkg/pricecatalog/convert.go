package pricecatalog

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Currency is the only currency the catalogue states prices in.
const Currency = "USD"

// Scale is the fixed-point scale of imported prices (millionths), the same
// default as `aiplex usage prices set`.
const Scale uint8 = 6

// Options controls a conversion.
type Options struct {
	// ReasoningIncluded declares reasoning_tokens as a free meter: the
	// operator states that the provider's output tokens already include
	// reasoning, so a record that reports reasoning tokens is still priced.
	// The catalogue never states this, so it is never assumed.
	ReasoningIncluded bool
}

// Meter pairs a price meter with the exact decimal per-million-token rate
// written for it.
type Meter struct {
	Name string
	Mtok string
}

// Plan is what importing one catalogue entry would write.
type Plan struct {
	Entry  Entry
	Prices []usageledger.Price
	// Meters lists, per price in Prices, the rates written (per million tokens).
	Meters [][]Meter
	// Blocking and LeftOut repeat the entry's notes; Blocking needs
	// acceptance before the import is allowed.
	Blocking []string
	LeftOut  []string
}

// PriceID is the resource id of an entry's price: the catalogue identity with
// every character outside [a-z0-9._-] replaced by a dash.
func PriceID(e Entry) string {
	var b strings.Builder
	for _, c := range strings.ToLower(e.Provider + "-" + e.Model) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
			b.WriteRune(c)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// RatePerMtok converts an exact decimal price per million units to the stored
// rational at the given scale, with no floating point and no rounding. It
// fails when the exact value does not fit the stored form.
func RatePerMtok(decimal string, scale uint8) (usageledger.Rational, error) {
	decimal = strings.TrimSpace(decimal)
	if decimal == "" || strings.Trim(decimal, "0123456789.") != "" {
		return usageledger.Rational{}, fmt.Errorf("%q is not a plain decimal price", decimal)
	}
	r, ok := new(big.Rat).SetString(decimal)
	if !ok {
		return usageledger.Rational{}, fmt.Errorf("%q is not a plain decimal price", decimal)
	}
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)))
	r.Quo(r, big.NewRat(1_000_000, 1))
	if !r.Num().IsInt64() || !r.Denom().IsInt64() {
		return usageledger.Rational{}, fmt.Errorf("%s per million tokens does not fit the stored form at scale %d", decimal, scale)
	}
	return usageledger.Rational{Numerator: r.Num().Int64(), Denominator: r.Denom().Int64()}, nil
}

func meters(r Rates, scale uint8) (map[string]usageledger.Rational, []Meter, error) {
	out := map[string]usageledger.Rational{}
	shown := []Meter{}
	for _, m := range []struct{ name, value string }{
		{usageledger.MeterInput, r.Input}, {usageledger.MeterOutput, r.Output},
		{usageledger.MeterCacheRead, r.CacheRead}, {usageledger.MeterCacheWrite, r.CacheWrite},
	} {
		if m.value == "" {
			continue // not stated: stays unknown
		}
		rate, err := RatePerMtok(m.value, scale)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", m.name, err)
		}
		out[m.name] = rate
		shown = append(shown, Meter{Name: m.name, Mtok: m.value})
	}
	return out, shown, nil
}

// Convert builds the price resources for one entry. The base price carries the
// standard rates; when the catalogue states batch rates, a second price with
// the batch selector carries them. A meter the entry does not state is absent
// from the price; an entry with no expressible rate is an error.
func (c *Catalog) Convert(e Entry, opts Options) (Plan, error) {
	ledger := LedgerProvider(e.Provider)
	if ledger == "" {
		return Plan{}, fmt.Errorf("unknown catalogue provider %q", e.Provider)
	}
	source := &usageledger.PriceSource{Catalog: c.Meta.Catalog, Commit: c.Meta.Commit, RetrievedAt: c.Meta.RetrievedAt, SourceURL: c.Meta.SourceURL}
	if e.Source != "" {
		source.SourceURL = e.Source
	}
	plan := Plan{Entry: e, Blocking: e.Blocking, LeftOut: e.LeftOut}
	build := func(id string, rates Rates, batch bool) error {
		m, shown, err := meters(rates, Scale)
		if err != nil {
			return err
		}
		if len(m) == 0 {
			return fmt.Errorf("%s states no rate that can be imported", e.ID())
		}
		if opts.ReasoningIncluded {
			m[usageledger.MeterReasoning] = usageledger.Rational{Numerator: 0, Denominator: 1}
			shown = append(shown, Meter{Name: usageledger.MeterReasoning, Mtok: "0 (included in output_tokens, declared by the operator)"})
		}
		p := usageledger.Price{ID: id, ModelID: e.Model, Provider: ledger, Currency: Currency, Scale: Scale, Meters: m, Source: source}
		if batch {
			t := true
			p.Batch = &t
		}
		if err := p.Validate(); err != nil {
			return err
		}
		plan.Prices = append(plan.Prices, p)
		plan.Meters = append(plan.Meters, shown)
		return nil
	}
	if err := build(PriceID(e), e.Rates, false); err != nil {
		return Plan{}, err
	}
	if e.Batch != nil && !e.Batch.Empty() {
		if err := build(PriceID(e)+"-batch", *e.Batch, true); err != nil {
			return Plan{}, err
		}
	}
	return plan, nil
}
