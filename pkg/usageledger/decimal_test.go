package usageledger

import (
	"encoding/json"
	"testing"
)

func TestDecimalStringIsExact(t *testing.T) {
	for _, tc := range []struct {
		amount string
		scale  uint8
		want   string
	}{
		{"6", 6, "0.000006"},
		{"1135", 6, "0.001135"},
		{"250", 2, "2.50"},
		{"0", 6, "0.000000"},
		{"123456789012345678901234567890", 6, "123456789012345678901234.567890"},
		{"42", 0, "42"},
		{"007", 2, "0.07"},
		{"-5", 3, "-0.005"},
		{"-0", 2, "0.00"},
	} {
		got, ok := DecimalString(tc.amount, tc.scale)
		if !ok || got != tc.want {
			t.Errorf("DecimalString(%q, %d) = %q, %t; want %q", tc.amount, tc.scale, got, ok, tc.want)
		}
	}
	for _, bad := range []string{"", "-", "1.5", "abc", "1e3"} {
		if got, ok := DecimalString(bad, 2); ok {
			t.Errorf("DecimalString(%q) = %q, want failure", bad, got)
		}
	}
}

// Cost objects carry amount_decimal next to amount and scale, which are unchanged;
// an unknown cost gains nothing.
func TestCostJSONCarriesAmountDecimal(t *testing.T) {
	raw, err := json.Marshal(Cost{Basis: BasisEstimated, Currency: "USD", Amount: "6", Scale: 6})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if got["amount"] != "6" || got["scale"] != float64(6) || got["amount_decimal"] != "0.000006" || got["currency"] != "USD" || got["basis"] != BasisEstimated {
		t.Fatalf("cost = %s", raw)
	}
	var back Cost
	if err := json.Unmarshal(raw, &back); err != nil || back.Amount != "6" || back.Scale != 6 {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	raw, _ = json.Marshal(UnknownCost())
	if string(raw) != `{"basis":"unknown"}` {
		t.Fatalf("unknown cost = %s", raw)
	}
	raw, _ = json.Marshal(CostTotal{Currency: "EUR", Basis: "settled", Amount: "250", Scale: 2, Records: 2})
	_ = json.Unmarshal(raw, &got)
	if got["amount"] != "250" || got["amount_decimal"] != "2.50" || got["records"] != float64(2) {
		t.Fatalf("cost total = %s", raw)
	}
}
