package main

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Tool response size and duration render as bytes and a duration; an
// unreported counter is unknown, never 0. tool_use_prompt_tokens renders as
// tokens (it was formatted as a duration before it had a label).
func TestUsageToolUnitsRender(t *testing.T) {
	record := usageledger.Record{Usage: &usageledger.Usage{}}
	record.Usage.ToolResponseBytes = usageledger.Int64(2048)
	record.Usage.ToolDurationMilliseconds = usageledger.Int64(1500)
	record.Usage.ToolUsePromptTokens = usageledger.Int64(12)
	cell := usageUnitsCell(record)
	for _, want := range []string{"tool-response 2048 bytes", "tool-time 1.5s", "tool-use-prompt 12 tokens"} {
		if !strings.Contains(cell, want) {
			t.Fatalf("units cell %q lacks %q", cell, want)
		}
	}
	if strings.Contains(cell, "12ms") {
		t.Fatalf("tool_use_prompt_tokens rendered as a duration: %q", cell)
	}
	if got := usageUnitCounter(record, usageledger.CounterToolResponseBytes); got != "2048 bytes" {
		t.Fatalf("bytes detail: %q", got)
	}
	if got := usageUnitCounter(record, usageledger.CounterToolDurationMilliseconds); !strings.HasPrefix(got, "1500 ms") {
		t.Fatalf("duration detail: %q", got)
	}
	empty := usageledger.Record{Usage: &usageledger.Usage{}}
	if got := usageUnitCounter(empty, usageledger.CounterToolResponseBytes); got != usageUnknown {
		t.Fatalf("an unreported size must be unknown: %q", got)
	}
	if cell := usageUnitsCell(empty); cell != "—" {
		t.Fatalf("no units: %q", cell)
	}
}
