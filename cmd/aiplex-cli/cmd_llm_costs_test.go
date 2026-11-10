package main

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestLLMCostsShowsUnknownNotZero(t *testing.T) {
	// Two records, neither priced and neither with tokens.
	unknown := &aiplex.UsageLedgerSummary{Groups: []usageledger.SummaryGroup{{
		Key: map[string]string{}, Records: 2, Requests: 2, UnknownCostRecords: 2, UnknownUsageRecords: 2,
	}}}
	out, _ := captureStdout(t, func() error { llmCostsText(unknown, "last day"); return nil })
	if strings.Contains(out, "$0.00") {
		t.Fatalf("unknown shown as zero:\n%s", out)
	}
	for _, want := range []string{"Total Cost:    unknown", "Total Tokens:  unknown", "2 of 2 records have an unknown cost", "Requests:      2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in:\n%s", want, out)
		}
	}

	// One priced record and one unpriced: the total is shown and the gap is said.
	mixed := &aiplex.UsageLedgerSummary{Groups: []usageledger.SummaryGroup{{
		Key: map[string]string{}, Records: 2, Requests: 2, UnknownCostRecords: 1,
		Tokens:        map[string]int64{usageledger.CounterInput: 10, usageledger.CounterOutput: 20},
		TokensUnknown: map[string]int64{},
		Cost:          []usageledger.CostTotal{{Currency: "USD", Basis: "estimated", Amount: "150", Scale: 4, Records: 1}},
	}}}
	out, _ = captureStdout(t, func() error { llmCostsText(mixed, "last day"); return nil })
	for _, want := range []string{"USD 0.015", "Total Tokens:  30", "1 of 2 records have an unknown cost"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in:\n%s", want, out)
		}
	}
}
