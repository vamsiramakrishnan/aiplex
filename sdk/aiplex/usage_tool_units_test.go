package aiplex_test

import (
	"encoding/json"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// The SDK's record type is the ledger's (sdk/aiplex/usage_ledger.go), so a
// tool call's response size and duration are typed fields beside the other
// units; a value the record did not report stays nil. Summaries carry them in
// the units map like every other unit.
func TestUsageRecordCarriesToolUnits(t *testing.T) {
	var record aiplex.UsageLedgerRecord
	if err := json.Unmarshal([]byte(`{"usage":{"tool_response_bytes":2048,"tool_duration_milliseconds":15}}`), &record); err != nil {
		t.Fatal(err)
	}
	if v := record.Usage.ToolResponseBytes; v == nil || *v != 2048 {
		t.Fatalf("bytes: %v", v)
	}
	if v := record.Usage.UnitCounter(usageledger.CounterToolDurationMilliseconds); v == nil || *v != 15 {
		t.Fatalf("duration: %v", v)
	}
	var unreported aiplex.UsageLedgerRecord
	if err := json.Unmarshal([]byte(`{"usage":{"input_tokens":3}}`), &unreported); err != nil {
		t.Fatal(err)
	}
	if unreported.Usage.ToolResponseBytes != nil || unreported.Usage.ToolDurationMilliseconds != nil {
		t.Fatal("an unreported tool unit must stay nil")
	}
	var group aiplex.UsageSummaryGroup
	if err := json.Unmarshal([]byte(`{"units":{"tool_response_bytes":4096},"unit_records":{"tool_response_bytes":2}}`), &group); err != nil {
		t.Fatal(err)
	}
	if group.Units[usageledger.CounterToolResponseBytes] != 4096 || group.UnitRecords[usageledger.CounterToolResponseBytes] != 2 {
		t.Fatalf("%+v", group)
	}
}
