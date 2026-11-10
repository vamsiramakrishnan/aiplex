package main

import (
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func TestPerMillionRateIsExact(t *testing.T) {
	cases := []struct {
		price string
		scale uint8
		num   int64
		den   int64
	}{
		{"3", 6, 3, 1},          // $3/Mtok = 3e6 micro-dollars per 1e6 tokens
		{"15", 6, 15, 1},        // $15/Mtok
		{"0.075", 6, 3, 40},     // 75000/1e6 = 3/40
		{"0.15", 6, 3, 20},      // 150000/1e6
		{"1.25", 2, 1, 8000},    // 125 cents per 1e6 tokens
		{"0", 6, 0, 1},          // a declared free meter
		{"0.000001", 6, 1, 1e6}, // smallest representable at scale 6
	}
	for _, c := range cases {
		r, err := perMillionRate(c.price, c.scale)
		if err != nil {
			t.Fatalf("%s: %v", c.price, err)
		}
		if r.Numerator != c.num || r.Denominator != c.den {
			t.Errorf("%s at scale %d = %d/%d, want %d/%d", c.price, c.scale, r.Numerator, r.Denominator, c.num, c.den)
		}
		// Round trip: the rational times 1e6 tokens is exactly price × 10^scale.
		got := new(big.Rat).SetFrac64(r.Numerator, r.Denominator)
		got.Mul(got, big.NewRat(1_000_000, 1))
		want, _ := new(big.Rat).SetString(c.price)
		want.Mul(want, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(c.scale)), nil)))
		if got.Cmp(want) != 0 {
			t.Errorf("%s: round trip %s != %s", c.price, got, want)
		}
		if back := rateAsPerMillion(r, c.scale, 1_000_000); back != c.price && !(c.price == "3" && back == "3") && !(c.price == "15" && back == "15") && !(c.price == "0" && back == "0") {
			t.Errorf("%s rendered back as %s", c.price, back)
		}
	}
	for _, bad := range []string{"-1", "abc", "1e3", "1/3", ""} {
		if _, err := perMillionRate(bad, 6); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if r, err := perUnitRate("0.002", 6); err != nil || r.Numerator != 2000 || r.Denominator != 1 {
		t.Errorf("per request 0.002 = %+v %v", r, err)
	}
}

func TestPriceEstimateMatchesDecimalArithmetic(t *testing.T) {
	in, _ := perMillionRate("3", 6)
	out, _ := perMillionRate("15", 6)
	p := usageledger.Price{ID: "m", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]usageledger.Rational{usageledger.MeterInput: in, usageledger.MeterOutput: out}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	// 1000 input + 2000 output tokens = 0.003 + 0.030 = $0.033 = 33000 micro-dollars.
	if got := humanPriceMeters(p); got != "input $3/Mtok, output $15/Mtok" {
		t.Errorf("human rates = %q", got)
	}
	cost := new(big.Rat).Add(new(big.Rat).Mul(big.NewRat(1000, 1), big.NewRat(in.Numerator, in.Denominator)), new(big.Rat).Mul(big.NewRat(2000, 1), big.NewRat(out.Numerator, out.Denominator)))
	if cost.Cmp(big.NewRat(33000, 1)) != 0 {
		t.Errorf("cost = %s micro-dollars, want 33000", cost)
	}
}

func TestPricesSetFromFlagsReadsModifiesWrites(t *testing.T) {
	var sent string
	var ifMatch string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"id":"gpt","revision":7,"spec":{"id":"gpt","model_id":"gpt","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":3,"denominator":20},"output_tokens":{"numerator":3,"denominator":5}}}}`))
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			sent, ifMatch = string(b), r.Header.Get("If-Match")
			w.Write([]byte(`{"id":"gpt","revision":8,"spec":` + sent + `}`))
		}
	}))
	defer server.Close()
	out, err := runUsage(t, server, "table", "prices", "set", "gpt", "--output-per-mtok", "0.5")
	if err != nil {
		t.Fatal(out, err)
	}
	if ifMatch != "7" {
		t.Errorf("revision not read for the user: If-Match %q", ifMatch)
	}
	if !strings.Contains(sent, `"input_tokens":{"numerator":3,"denominator":20}`) || !strings.Contains(sent, `"output_tokens":{"numerator":1,"denominator":2}`) {
		t.Errorf("merge wrong: %s", sent)
	}
	if !strings.Contains(out, "output $0.50/Mtok") || !strings.Contains(out, "input $0.15/Mtok") {
		t.Errorf("summary wrong: %s", out)
	}
	if _, err := runUsage(t, server, "table", "prices", "set", "gpt", "--scale", "4", "--output-per-mtok", "1"); err == nil {
		t.Error("scale change without every rate accepted")
	}
}
