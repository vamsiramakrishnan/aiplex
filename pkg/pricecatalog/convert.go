package pricecatalog

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Currency is the only currency the catalogue states prices in.
const Currency = "USD"

// Scale is the fixed-point scale of imported prices (millionths), the same
// default as `aiplex usage prices set`. A cost is rounded up to this scale per
// record; the exact value is kept beside it (usageledger.Cost.Exact), so
// summaries do not accumulate the rounding.
const Scale uint8 = 6

// Options controls a conversion.
type Options struct {
	// ReasoningIncluded declares reasoning_tokens as a free meter: the
	// operator states that the provider's output tokens already include
	// reasoning, so a record that reports reasoning tokens is still priced.
	// The catalogue never states this, so it is never assumed.
	ReasoningIncluded bool
}

// Meter pairs a price meter with the exact decimal rate written for it. Mtok
// is per million tokens, or per call when Unit is "call".
type Meter struct {
	Name string
	Mtok string
	Unit string
}

// PlannedTier is one long-context override of a price: the meters that apply
// to a whole request whose total input exceeds Above tokens.
type PlannedTier struct {
	Above  int64
	Meters []Meter
}

// Plan is what importing one catalogue entry would write.
type Plan struct {
	Entry  Entry
	Prices []usageledger.Price
	// Meters lists, per price in Prices, the rates written.
	Meters [][]Meter
	// Tiers lists, per price in Prices, its long-context overrides.
	Tiers [][]PlannedTier
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
	return ratePer(decimal, scale, big.NewRat(1_000_000, 1), "million tokens")
}

// RatePerCall converts an exact decimal price per call to the stored rational.
func RatePerCall(decimal string, scale uint8) (usageledger.Rational, error) {
	return ratePer(decimal, scale, big.NewRat(1, 1), "call")
}

func ratePer(decimal string, scale uint8, per *big.Rat, unit string) (usageledger.Rational, error) {
	decimal = strings.TrimSpace(decimal)
	if decimal == "" || strings.Trim(decimal, "0123456789.") != "" {
		return usageledger.Rational{}, fmt.Errorf("%q is not a plain decimal price", decimal)
	}
	r, ok := new(big.Rat).SetString(decimal)
	if !ok {
		return usageledger.Rational{}, fmt.Errorf("%q is not a plain decimal price", decimal)
	}
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)))
	r.Quo(r, per)
	if !r.Num().IsInt64() || !r.Denom().IsInt64() {
		return usageledger.Rational{}, fmt.Errorf("%s per %s does not fit the stored form at scale %d", decimal, unit, scale)
	}
	return usageledger.Rational{Numerator: r.Num().Int64(), Denominator: r.Denom().Int64()}, nil
}

// rateMeters pairs each stated scalar rate with its price meter, in a fixed
// order. Unstated rates are absent (unknown).
func rateMeters(r Rates) []struct{ name, value string } {
	all := []struct{ name, value string }{
		{usageledger.MeterInput, r.Input}, {usageledger.MeterOutput, r.Output},
		{usageledger.MeterCacheRead, r.CacheRead}, {usageledger.MeterCacheWrite, r.CacheWrite},
		{usageledger.MeterCacheWrite1h, r.CacheWrite1h},
		{usageledger.CounterInputAudioTokens, r.InputAudio}, {usageledger.CounterInputImageTokens, r.InputImage}, {usageledger.CounterInputVideoTokens, r.InputVideo},
		{usageledger.CounterOutputAudioTokens, r.OutputAudio}, {usageledger.CounterOutputImageTokens, r.OutputImage},
	}
	out := all[:0]
	for _, m := range all {
		if m.value != "" {
			out = append(out, m)
		}
	}
	return out
}

func meters(r Rates, scale uint8) (map[string]usageledger.Rational, []Meter, error) {
	out := map[string]usageledger.Rational{}
	shown := []Meter{}
	for _, m := range rateMeters(r) {
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
// standard rates and their long-context tiers; when the catalogue states batch
// rates, a second price with the batch selector carries them, and priority and
// flex rates become prices with a service_tier selector. A meter the entry
// does not state is absent from the price; an entry with no expressible rate is
// an error.
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
	// derive adds the meters that follow from the provider's usage reporting
	// or from the operator's declaration, to a base set and to each tier alike.
	derive := func(m map[string]usageledger.Rational, shown []Meter) []Meter {
		if opts.ReasoningIncluded {
			m[usageledger.MeterReasoning] = usageledger.Rational{Numerator: 0, Denominator: 1}
			shown = append(shown, Meter{Name: usageledger.MeterReasoning, Mtok: "0 (included in output_tokens, declared by the operator)"})
		}
		if out, ok := m[usageledger.MeterOutput]; ok && !opts.ReasoningIncluded && ledger == "gemini-developer" {
			// The Gemini Developer API reports thoughtsTokenCount apart from
			// candidatesTokenCount and bills thinking tokens at the output
			// rate (pricing page: "Output price (including thinking
			// tokens)"). Without this meter a thinking response stays unknown.
			m[usageledger.MeterReasoning] = out
			for _, sm := range shown {
				if sm.Name == usageledger.MeterOutput {
					shown = append(shown, Meter{Name: usageledger.MeterReasoning, Mtok: sm.Mtok})
					break
				}
			}
		}
		return shown
	}
	build := func(id string, rates Rates, batch bool, tier string, serverTools bool) error {
		m, shown, err := meters(rates, Scale)
		if err != nil {
			return err
		}
		if len(m) == 0 {
			return fmt.Errorf("%s states no rate that can be imported", e.ID())
		}
		shown = derive(m, shown)
		if serverTools {
			kinds := make([]string, 0, len(e.ServerTools))
			for kind := range e.ServerTools {
				kinds = append(kinds, kind)
			}
			sort.Strings(kinds)
			for _, kind := range kinds {
				rate, err := RatePerCall(e.ServerTools[kind], Scale)
				if err != nil {
					return fmt.Errorf("server tool %s: %w", kind, err)
				}
				m[usageledger.ServerToolMeter(kind)] = rate
				shown = append(shown, Meter{Name: usageledger.ServerToolMeter(kind), Mtok: e.ServerTools[kind], Unit: "call"})
			}
		}
		p := usageledger.Price{ID: id, ModelID: e.Model, Provider: ledger, Currency: Currency, Scale: Scale, Meters: m, Source: source, ServiceTier: tier}
		var planned []PlannedTier
		for _, t := range rates.Tiers {
			tm, tshown, err := meters(t.Rates, Scale)
			if err != nil {
				return err
			}
			tshown = derive(tm, tshown)
			for name, rate := range m {
				if _, kind := strings.CutPrefix(name, usageledger.MeterServerToolPrefix); kind {
					tm[name] = rate // a per-call price does not depend on prompt length
					tshown = append(tshown, Meter{Name: name, Mtok: "same as the base price", Unit: "call"})
				}
			}
			p.Tiers = append(p.Tiers, usageledger.PriceTier{AboveInputTokens: t.Above, Meters: tm})
			planned = append(planned, PlannedTier{Above: t.Above, Meters: tshown})
		}
		if batch {
			t := true
			p.Batch = &t
		}
		if err := p.Validate(); err != nil {
			return err
		}
		plan.Prices = append(plan.Prices, p)
		plan.Meters = append(plan.Meters, shown)
		plan.Tiers = append(plan.Tiers, planned)
		return nil
	}
	if err := build(PriceID(e), e.Rates, false, "", len(e.ServerTools) > 0); err != nil {
		return Plan{}, err
	}
	if e.Batch != nil && !e.Batch.Empty() {
		if err := build(PriceID(e)+"-batch", *e.Batch, true, "", false); err != nil {
			return Plan{}, err
		}
	}
	for _, v := range []struct {
		rates *Rates
		tier  string
	}{{e.Priority, "priority"}, {e.Flex, "flex"}} {
		if v.rates != nil && !v.rates.Empty() {
			if err := build(PriceID(e)+"-"+v.tier, *v.rates, false, v.tier, false); err != nil {
				return Plan{}, err
			}
		}
	}
	return plan, nil
}
