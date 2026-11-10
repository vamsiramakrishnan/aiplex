package main

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// perMillionRate converts a decimal price per million units ("3", "0.075",
// "1.25") into the exact rational the API stores: Numerator money units at
// scale per Denominator raw units. The conversion is exact (no floating
// point): rate = price × 10^scale / 10^6, reduced.
func perMillionRate(price string, scale uint8) (usageledger.Rational, error) {
	return decimalRate(price, scale, 1_000_000)
}

// perUnitRate converts a decimal price per single unit (for requests).
func perUnitRate(price string, scale uint8) (usageledger.Rational, error) {
	return decimalRate(price, scale, 1)
}

func decimalRate(price string, scale uint8, per int64) (usageledger.Rational, error) {
	price = strings.TrimSpace(price)
	if price == "" || strings.ContainsAny(price, "eE/") {
		return usageledger.Rational{}, fmt.Errorf("%q is not a decimal price (write it like 3 or 0.075)", price)
	}
	r, ok := new(big.Rat).SetString(price)
	if !ok {
		return usageledger.Rational{}, fmt.Errorf("%q is not a decimal price (write it like 3 or 0.075)", price)
	}
	if r.Sign() < 0 {
		return usageledger.Rational{}, fmt.Errorf("price %q is negative", price)
	}
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)))
	r.Quo(r, new(big.Rat).SetInt64(per))
	if !r.Num().IsInt64() || !r.Denom().IsInt64() {
		return usageledger.Rational{}, fmt.Errorf("price %q at scale %d does not fit the stored form; use a smaller --scale or fewer decimals", price, scale)
	}
	return usageledger.Rational{Numerator: r.Num().Int64(), Denominator: r.Denom().Int64()}, nil
}

// rateAsPerMillion renders a stored rational back as a decimal price per
// million units, exactly when it terminates, else with 6 significant decimals
// and a "≈".
func rateAsPerMillion(rate usageledger.Rational, scale uint8, per int64) string {
	if rate.Denominator <= 0 {
		return "invalid"
	}
	r := new(big.Rat).SetFrac64(rate.Numerator, rate.Denominator)
	r.Mul(r, new(big.Rat).SetInt64(per))
	r.Quo(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)))
	for digits := 0; digits <= 18; digits++ {
		s := r.FloatString(digits)
		back, _ := new(big.Rat).SetString(s)
		if back.Cmp(r) == 0 {
			if digits < 2 && !r.IsInt() {
				return r.FloatString(2)
			}
			return s
		}
	}
	return "≈" + r.FloatString(6)
}

// meterLabels are the short names used in tables and flags.
var meterLabels = map[string]string{
	usageledger.MeterInput:      "input",
	usageledger.MeterOutput:     "output",
	usageledger.MeterCacheRead:  "cache-read",
	usageledger.MeterCacheWrite: "cache-write",
	usageledger.MeterReasoning:  "reasoning",
	usageledger.MeterRequests:   "request",
}

// humanPriceMeters renders "input USD 3/Mtok, output USD 15/Mtok".
func humanPriceMeters(p usageledger.Price) string {
	names := make([]string, 0, len(p.Meters))
	for name := range p.Meters {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return meterOrder(names[i]) < meterOrder(names[j]) })
	parts := make([]string, 0, len(names))
	sym := p.Currency + " "
	for _, name := range names {
		rate := p.Meters[name]
		label := meterLabels[name]
		switch {
		case name == usageledger.MeterRequests:
			parts = append(parts, label+" "+sym+rateAsPerMillion(rate, p.Scale, 1))
		case label != "":
			parts = append(parts, label+" "+sym+rateAsPerMillion(rate, p.Scale, 1_000_000)+"/Mtok")
		default:
			parts = append(parts, name+" "+sym+rateAsPerMillion(rate, p.Scale, 1)+"/unit")
		}
	}
	return strings.Join(parts, ", ")
}

func meterOrder(name string) int {
	for i, m := range []string{usageledger.MeterInput, usageledger.MeterOutput, usageledger.MeterCacheRead, usageledger.MeterCacheWrite, usageledger.MeterReasoning, usageledger.MeterRequests} {
		if m == name {
			return i
		}
	}
	return 100
}
