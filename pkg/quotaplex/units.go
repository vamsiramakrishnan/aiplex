package quotaplex

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
)

// ErrQuantityOverflow identifies arithmetic that cannot be represented without
// losing accounting precision. Callers must retain the outstanding exposure.
var ErrQuantityOverflow = errors.New("quota quantity overflow")

var ErrUnknownPrice = errors.New("billable meter has no price")

// ValidateMeter keeps currency precision part of the meter's immutable unit.
// Raw work meters cannot accidentally inherit monetary metadata.
func ValidateMeter(meter Meter) error {
	if meter.ID == "" || meter.Unit == "" || strings.TrimSpace(meter.ID) != meter.ID || strings.TrimSpace(meter.Unit) != meter.Unit {
		return fmt.Errorf("meter ID and unit required")
	}
	if meter.Unit != "money" {
		if meter.Currency != "" || meter.Scale != 0 {
			return fmt.Errorf("nonmonetary meter cannot declare currency or scale")
		}
		return nil
	}
	if len(meter.Currency) != 3 || meter.Scale > 18 || meter.Billable {
		return fmt.Errorf("money meter needs currency, bounded scale and no raw billable flag")
	}
	for _, character := range meter.Currency {
		if character < 'A' || character > 'Z' {
			return fmt.Errorf("money currency must be three uppercase ASCII letters")
		}
	}
	return nil
}

// ValidatePrices validates units before any quantity multiplication. Each raw
// meter has at most one rational price into a particular monetary meter.
func ValidatePrices(meters map[string]Meter, prices []UnitPrice) error {
	for id, meter := range meters {
		if id != meter.ID {
			return fmt.Errorf("meter catalog key and identity disagree")
		}
		if err := ValidateMeter(meter); err != nil {
			return err
		}
	}
	seen := map[[2]string]bool{}
	for _, price := range prices {
		raw, rawOK := meters[price.MeterID]
		money, moneyOK := meters[price.MoneyMeterID]
		key := [2]string{price.MeterID, price.MoneyMeterID}
		if !rawOK || raw.Unit == "money" || !moneyOK || money.Unit != "money" || price.Numerator < 0 || price.Denominator <= 0 || seen[key] {
			return fmt.Errorf("price needs distinct known raw/money meters, nonnegative numerator and positive denominator")
		}
		seen[key] = true
	}
	return nil
}

// PriceUsage derives exact monetary quantities from cumulative raw work. It
// sums rational terms per monetary meter before rounding up once. Hosts must
// price cumulative terminal usage, never sum individually rounded stream chunks.
// No conversion or implicit currency interoperability occurs between meters.
func PriceUsage(usage Vector, prices []UnitPrice, meters map[string]Meter) (Vector, error) {
	if err := ValidateVector(usage); err != nil {
		return nil, err
	}
	if err := ValidatePrices(meters, prices); err != nil {
		return nil, err
	}
	for id := range usage {
		meter, ok := meters[id]
		if !ok || meter.Unit == "money" {
			return nil, fmt.Errorf("raw usage contains unknown or monetary meter %q", id)
		}
	}
	totals := map[string]*big.Rat{}
	covered := map[[2]string]bool{}
	for _, price := range prices {
		if totals[price.MoneyMeterID] == nil {
			totals[price.MoneyMeterID] = new(big.Rat)
		}
		covered[[2]string{price.MeterID, price.MoneyMeterID}] = true
		quantity := usage[price.MeterID]
		numerator := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(price.Numerator))
		term := new(big.Rat).SetFrac(numerator, big.NewInt(price.Denominator))
		totals[price.MoneyMeterID].Add(totals[price.MoneyMeterID], term)
	}
	for id, quantity := range usage {
		if quantity == 0 || !meters[id].Billable {
			continue
		}
		for moneyID := range totals {
			if !covered[[2]string{id, moneyID}] {
				return nil, fmt.Errorf("meter %s into %s: %w", id, moneyID, ErrUnknownPrice)
			}
		}
	}
	out := make(Vector, len(totals))
	for moneyID, total := range totals {
		whole, remainder := new(big.Int), new(big.Int)
		whole.QuoRem(total.Num(), total.Denom(), remainder)
		if remainder.Sign() > 0 {
			whole.Add(whole, big.NewInt(1))
		}
		if !whole.IsInt64() || whole.Sign() < 0 {
			return nil, fmt.Errorf("money meter %s: %w", moneyID, ErrQuantityOverflow)
		}
		out[moneyID] = whole.Int64()
	}
	return out, nil
}

// ValidateVector rejects quantities that cannot describe actual resource work.
// Signed ledger adjustments use separate transaction operations, not raw usage.
func ValidateVector(values Vector) error {
	for meter, value := range values {
		if meter == "" || value < 0 {
			return fmt.Errorf("nonempty meter and nonnegative quantity required: %q", meter)
		}
	}
	return nil
}

// CloneVector returns an independently owned copy, retaining explicit zeros.
func CloneVector(values Vector) Vector {
	if values == nil {
		return nil
	}
	copy := make(Vector, len(values))
	for meter, value := range values {
		copy[meter] = value
	}
	return copy
}

func checkedAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, fmt.Errorf("nonnegative quantities required")
	}
	if a > math.MaxInt64-b {
		return 0, ErrQuantityOverflow
	}
	return a + b, nil
}

// AddVectors combines resource quantities without modifying either input.
func AddVectors(a, b Vector) (Vector, error) {
	if err := ValidateVector(a); err != nil {
		return nil, err
	}
	if err := ValidateVector(b); err != nil {
		return nil, err
	}
	out := make(Vector, len(a)+len(b))
	for meter, quantity := range a {
		out[meter] = quantity
	}
	for meter, quantity := range b {
		sum, err := checkedAdd(out[meter], quantity)
		if err != nil {
			return nil, fmt.Errorf("meter %s: %w", meter, err)
		}
		out[meter] = sum
	}
	return out, nil
}

// SubtractVectors releases known unconsumed quantities. An underflow is an
// accounting error and cannot mint capacity by creating a negative hold.
func SubtractVectors(a, b Vector) (Vector, error) {
	if err := ValidateVector(a); err != nil {
		return nil, err
	}
	if err := ValidateVector(b); err != nil {
		return nil, err
	}
	out := CloneVector(a)
	if out == nil {
		out = Vector{}
	}
	for meter, quantity := range b {
		if out[meter] < quantity {
			return nil, fmt.Errorf("meter %s: quantity underflow", meter)
		}
		out[meter] -= quantity
	}
	return out, nil
}
