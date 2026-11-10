package api

import (
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// One rejection rule for batches: a documented not-processed rejection of the
// create releases it; an ambiguous create failure is retained; a later poll or
// results fetch that is rejected never releases an open batch.
func TestBatchRejectionsFollowTheNotProcessedRule(t *testing.T) {
	anthropic := modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}
	newFixture := func() *mediaFixture {
		return newMediaFixture(t, anthropic, modelapi.ProtocolAnthropicBatches, anthropicRef,
			[]string{"anthropic.message-batches-create", "anthropic.message-batches-get", "anthropic.message-batches-results"},
			[]QuotaBinding{
				{ID: "batch", OperationID: "anthropic.message-batches-create", Model: "model-v1", Profile: quotaProfileBatch, Meters: map[string]string{"requests": "requests", "batch_requests": "batch_items", "output_tokens": "out"}},
				{ID: "batch-results", OperationID: "anthropic.message-batches-results", Profile: quotaProfileRequests, Meters: map[string]string{"requests": "requests"}},
				{ID: "batch-get", OperationID: "anthropic.message-batches-get", Profile: quotaProfileRequests, Meters: map[string]string{"requests": "requests"}},
			},
			map[string]quotaplex.Meter{"batch_items": {ID: "batch_items", Unit: "requests"}, "out": {ID: "out", Unit: "tokens", Billable: true}},
			[]quotaplex.UnitPrice{{MeterID: "out", MoneyMeterID: "usd", Numerator: 2, Denominator: 1}, {MeterID: "requests", MoneyMeterID: "usd", Numerator: 0, Denominator: 1}},
			&unitBounds{native: map[string]int64{"output_tokens": 8000}})
	}
	body := []byte(`{"requests":[{"custom_id":"a","params":{"model":"model-v1","max_tokens":8000,"messages":[{"role":"user","content":"x"}]}}]}`)
	for _, test := range []struct {
		status int
		state  string
	}{{429, "settled"}, {400, "settled"}, {529, "settled"}, {500, "unknown"}} {
		m := newFixture()
		m.route("POST", "/v1/messages/batches", mediaRoute{test.status, "application/json", []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"x"}}`), false})
		w := m.call(t, "POST", "/v1/messages/batches", "create", "application/json", body)
		info := m.attempt(t, w)
		if info.Decision.State != test.state {
			t.Fatalf("create %d: state %s (client %d)", test.status, info.Decision.State, w.Code)
		}
		if test.state == "settled" && (w.Code != test.status || info.Actual["out"] != 0) {
			t.Fatalf("released create %d: client %d actual %v", test.status, w.Code, info.Actual)
		}
	}
	m := newFixture()
	m.route("POST", "/v1/messages/batches", mediaRoute{200, "application/json", []byte(`{"id":"msgbatch_r1","type":"message_batch","processing_status":"in_progress"}`), false})
	w := m.call(t, "POST", "/v1/messages/batches", "open", "application/json", body)
	open := w.Header().Get("X-Aiplex-Attempt-ID")
	for _, status := range []int{404, 429} {
		m.route("GET", "/v1/messages/batches/msgbatch_r1/results", mediaRoute{status, "application/json", []byte(`{"type":"error","error":{"type":"not_found_error","message":"x"}}`), false})
		m.call(t, "GET", "/v1/messages/batches/msgbatch_r1/results", "", "", nil)
		m.route("GET", "/v1/messages/batches/msgbatch_r1", mediaRoute{status, "application/json", []byte(`{"type":"error","error":{"type":"not_found_error","message":"x"}}`), false})
		m.call(t, "GET", "/v1/messages/batches/msgbatch_r1", "", "", nil)
		if info := quotaHostModelAttempt(t, m.quotaHostModelFixture, open); info.Decision.State != "unknown" || info.UnknownReason != "batch_results_pending" {
			t.Fatalf("a %d poll released the open batch: %s %q", status, info.Decision.State, info.UnknownReason)
		}
	}
}
