package api

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// A relayed WebSocket session has no HTTP completion of its own: its usage is
// final only with a terminal observation; otherwise the record is unknown and
// its counters are labelled a lower bound (or the adapter's label), never
// presented as final. Non-token meters mark the token usage partial.
func TestLedgerSessionUsageBoundLabels(t *testing.T) {
	for _, test := range []struct {
		name       string
		terminal   bool
		bound      string
		counters   map[string]int64
		wantStatus string
		wantBound  string
	}{
		{"final", true, "", map[string]int64{"input_tokens": 10, "output_tokens": 5}, usageledger.StatusOK, ""},
		{"interrupted", false, "", map[string]int64{"input_tokens": 10, "output_tokens": 5}, usageledger.StatusUnknown, usageledger.UsageLowerBound},
		{"resumed", false, usageledger.UsageAmbiguous, map[string]int64{"input_tokens": 10}, usageledger.StatusUnknown, usageledger.UsageAmbiguous},
		{"seconds", true, "", map[string]int64{"input_tokens": 1, "output_tokens": 1, "transcription_audio_milliseconds": 2500}, usageledger.StatusOK, usageledger.UsagePartial},
		{"transcription tokens", true, "", map[string]int64{"input_tokens": 1, "output_tokens": 1, "transcription_input_tokens": 13}, usageledger.StatusOK, usageledger.UsagePartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := &ConnectedHandler{}
			records := attachLedger(t, h)
			w := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
			_, capture := h.beginUsage(w, httptest.NewRequest("GET", "/ws", nil), "tenant", usageledger.PlaneLLM, "surface")
			capture.writer.hijacked = true
			capture.observe(test.counters, true, "", test.terminal, test.bound)
			capture.finish()
			r := onlyRecord(t, records())
			if r.Status != test.wantStatus || r.UsageBound != test.wantBound || r.Usage == nil || *r.Usage.InputTokens != test.counters["input_tokens"] {
				t.Fatalf("%+v usage=%+v", r, r.Usage)
			}
		})
	}
}

type hijackableRecorder struct{ *httptest.ResponseRecorder }

// Messages-format counters exclude cache reads and writes from input_tokens;
// the ledger's input_tokens is total input. Unknown cache counters add
// nothing and already-normalized counters are left alone.
func TestLedgerNormalizesExclusiveInputIncludingCacheWrites(t *testing.T) {
	for _, test := range []struct{ in, want map[string]int64 }{
		{map[string]int64{"input_tokens": 1203, "cached_tokens": 4096, "cache_write_tokens": 2048, "output_tokens": 777}, map[string]int64{"input_tokens": 7347, "uncached_input_tokens": 1203, "cached_tokens": 4096, "cache_write_tokens": 2048, "output_tokens": 777}},
		{map[string]int64{"input_tokens": 105, "output_tokens": 239}, map[string]int64{"input_tokens": 105, "uncached_input_tokens": 105, "output_tokens": 239}},
		{map[string]int64{"input_tokens": 9, "uncached_input_tokens": 7, "cached_tokens": 2}, map[string]int64{"input_tokens": 9, "uncached_input_tokens": 7, "cached_tokens": 2}},
		{map[string]int64{"cached_tokens": 6144}, map[string]int64{"cached_tokens": 6144}},
	} {
		got := map[string]int64{}
		for k, v := range test.in {
			got[k] = v
		}
		normalizeExclusiveInput(got)
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%v -> %v, want %v", test.in, got, test.want)
		}
	}
}
