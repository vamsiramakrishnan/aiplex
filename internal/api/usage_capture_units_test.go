package api

import (
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Units the ledger now holds no longer mark a record partial; units it
// cannot hold (separately attributed transcription meters) still do.
func TestLedgerHeldUnitsAreNotPartial(t *testing.T) {
	for _, test := range []struct {
		name      string
		counters  map[string]int64
		dims      map[string]string
		wantBound string
		check     func(usageledger.Record) bool
	}{
		{"duration", map[string]int64{"audio_input_milliseconds": 3000}, nil, "", func(r usageledger.Record) bool {
			return r.Usage != nil && r.Usage.InputTokens == nil && *r.Usage.AudioInputMilliseconds == 3000
		}},
		{"images", map[string]int64{"generated_images": 2, "input_tokens": 50, "output_tokens": 50, "output_image_tokens": 50}, map[string]string{"image_size": "1024x1024", "image_quality": "high"}, "", func(r usageledger.Record) bool {
			return *r.Usage.GeneratedImages == 2 && *r.Usage.OutputImageTokens == 50 && r.ImageSize == "1024x1024" && r.ImageQuality == "high"
		}},
		{"batch", map[string]int64{"input_tokens": 5}, map[string]string{"batch": "true"}, "", func(r usageledger.Record) bool { return r.Batch }},
		{"unheld", map[string]int64{"input_tokens": 1, "transcription_audio_milliseconds": 10}, nil, usageledger.UsagePartial, func(usageledger.Record) bool { return true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := &ConnectedHandler{}
			records := attachLedger(t, h)
			_, capture := h.beginUsage(httptest.NewRecorder(), httptest.NewRequest("POST", "/x", nil), "tenant", usageledger.PlaneLLM, "surface")
			capture.observe(test.counters, true, "", true, "", test.dims)
			capture.finish()
			r := onlyRecord(t, records())
			if r.UsageBound != test.wantBound || !test.check(r) {
				t.Fatalf("%+v usage=%+v", r, r.Usage)
			}
		})
	}
}
