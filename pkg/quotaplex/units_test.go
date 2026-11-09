package quotaplex

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestVectorArithmeticConservesWithoutAliasingOrWrapping(t *testing.T) {
	a := Vector{"input": math.MaxInt64 - 3, "calls": 5, "zero": 0}
	b := Vector{"input": 3, "calls": 2, "duration_ms": 8}
	sum, err := AddVectors(a, b)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := SubtractVectors(sum, b)
	if err != nil || restored["input"] != a["input"] || restored["calls"] != a["calls"] || restored["duration_ms"] != 0 {
		t.Fatalf("quantities failed conservation: restored=%v error=%v", restored, err)
	}
	if sum["input"] != math.MaxInt64 || sum["calls"] != 7 {
		t.Fatalf("exact boundary addition failed: %v", sum)
	}
	sum["calls"] = 99
	if a["calls"] != 5 || b["calls"] != 2 {
		t.Fatal("result aliases an input")
	}
	if _, err := AddVectors(a, Vector{"input": 4}); !errors.Is(err, ErrQuantityOverflow) {
		t.Fatalf("overflow was not classified: %v", err)
	}
	if !reflect.DeepEqual(a, Vector{"input": math.MaxInt64 - 3, "calls": 5, "zero": 0}) {
		t.Fatal("failed operation changed source quantities")
	}
}

func TestRawQuantitiesCannotMintCapacity(t *testing.T) {
	for _, invalid := range []Vector{{"calls": -1}, {"": 0}} {
		if err := ValidateVector(invalid); err == nil {
			t.Fatalf("invalid quantity accepted: %v", invalid)
		}
		if _, err := AddVectors(Vector{"calls": 4}, invalid); err == nil {
			t.Fatalf("negative/unnamed consumption accepted: %v", invalid)
		}
		if _, err := SubtractVectors(Vector{"calls": 4}, invalid); err == nil {
			t.Fatalf("negative release could mint capacity: %v", invalid)
		}
	}
	for _, release := range []Vector{{"calls": 5}, {"unknown": 1}} {
		if _, err := SubtractVectors(Vector{"calls": 4}, release); err == nil {
			t.Fatalf("underflow released unheld capacity: %v", release)
		}
	}
	if values, err := AddVectors(nil, nil); err != nil || values == nil || len(values) != 0 {
		t.Fatalf("empty composition should be usable: %v %v", values, err)
	}
}

func primitiveMeters() map[string]Meter {
	return map[string]Meter{
		"input":  {ID: "input", Unit: "tokens", Billable: true},
		"output": {ID: "output", Unit: "tokens", Billable: true},
		"calls":  {ID: "calls", Unit: "calls"},
		"usd":    {ID: "usd", Unit: "money", Currency: "USD", Scale: 9},
	}
}

func TestPricingRoundsOneCumulativeRationalSum(t *testing.T) {
	meters := primitiveMeters()
	prices := []UnitPrice{{MeterID: "input", MoneyMeterID: "usd", Numerator: 1, Denominator: 3}, {MeterID: "output", MoneyMeterID: "usd", Numerator: 1, Denominator: 3}}
	whole := Vector{"input": 1, "output": 1, "calls": 100}
	priced, err := PriceUsage(whole, prices, meters)
	if err != nil || priced["usd"] != 1 || len(priced) != 1 {
		t.Fatalf("terms rounded independently or raw meters leaked: %v %v", priced, err)
	}
	// A different arrival/chunk order produces the identical terminal vector.
	accumulated, err := AddVectors(Vector{"output": 1}, Vector{"input": 1, "calls": 100})
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := PriceUsage(accumulated, prices, meters)
	if err != nil || !reflect.DeepEqual(priced, reordered) {
		t.Fatalf("chunk order changed price: %v %v", reordered, err)
	}
	if whole["input"] != 1 || whole["output"] != 1 {
		t.Fatal("pricing changed provider counters")
	}
	// Exact arbitrary-precision intermediates are valid when the final amount
	// still fits; an overflow must be based on the final rounded result.
	large := []UnitPrice{{MeterID: "input", MoneyMeterID: "usd", Numerator: math.MaxInt64, Denominator: math.MaxInt64}, {MeterID: "output", MoneyMeterID: "usd", Numerator: 1, Denominator: 1}}
	priced, err = PriceUsage(Vector{"input": math.MaxInt64}, large, meters)
	if err != nil || priced["usd"] != math.MaxInt64 {
		t.Fatalf("safe final result rejected due to large intermediate: %v %v", priced, err)
	}
	if _, err := PriceUsage(Vector{"input": math.MaxInt64, "output": 1}, large, meters); !errors.Is(err, ErrQuantityOverflow) {
		t.Fatalf("monetary overflow lost: %v", err)
	}
}

func TestMissingPriceAndUnitAmbiguityAreExplicit(t *testing.T) {
	meters := primitiveMeters()
	prices := []UnitPrice{{MeterID: "input", MoneyMeterID: "usd", Numerator: 0, Denominator: 1}}
	if priced, err := PriceUsage(Vector{"input": 25}, prices, meters); err != nil || priced["usd"] != 0 {
		t.Fatalf("explicit free price rejected: %v %v", priced, err)
	}
	if _, err := PriceUsage(Vector{"output": 1}, prices, meters); !errors.Is(err, ErrUnknownPrice) {
		t.Fatalf("unpriced billable work fabricated zero cost: %v", err)
	}
	for _, usage := range []Vector{{"usd": 0}, {"unknown": 1}, {"input": -1}} {
		if _, err := PriceUsage(usage, prices, meters); err == nil {
			t.Fatalf("invalid/raw monetary usage accepted: %v", usage)
		}
	}
	for _, price := range []UnitPrice{
		{MeterID: "input", MoneyMeterID: "calls", Numerator: 1, Denominator: 1},
		{MeterID: "usd", MoneyMeterID: "usd", Numerator: 1, Denominator: 1},
		{MeterID: "input", MoneyMeterID: "usd", Numerator: -1, Denominator: 1},
		{MeterID: "input", MoneyMeterID: "usd", Numerator: 1, Denominator: 0},
	} {
		if err := ValidatePrices(meters, []UnitPrice{price}); err == nil {
			t.Fatalf("invalid price units accepted: %+v", price)
		}
	}
	if err := ValidatePrices(meters, append(prices, prices[0])); err == nil {
		t.Fatal("duplicate price could charge work twice")
	}
	for _, meter := range []Meter{
		{ID: "bad", Unit: "tokens", Currency: "USD"},
		{ID: "bad", Unit: "money", Currency: "usd", Scale: 9},
		{ID: "bad", Unit: "money", Currency: "USD", Scale: 19},
		{ID: "bad", Unit: "money", Currency: "USD", Billable: true},
	} {
		if err := ValidateMeter(meter); err == nil {
			t.Fatalf("ambiguous monetary unit accepted: %+v", meter)
		}
	}
}
