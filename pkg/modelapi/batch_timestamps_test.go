package modelapi

import (
	"context"
	"testing"
	"time"
)

// created_at is stamped when the gateway first records a batch and settled_at
// when it settles; later fetches (including repeat settling fetches and
// status refreshes) never rewrite either.
func TestBatchRecordTimestampsAreSetOnce(t *testing.T) {
	h := newBatchHarness(t, Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01"}, ProtocolAnthropicBatches, []string{anthropicBatchModel},
		"anthropic.message-batches-create", "anthropic.message-batches-get", "anthropic.message-batches-results", "anthropic.message-batches-list")
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	h.clock = func() time.Time { return now }
	owner := Owner{"tenant", "alice", "native"}
	load := func() BatchRecord {
		t.Helper()
		record, _, err := h.store.LoadBatch(context.Background(), owner, ProviderAnthropic, "msgbatch_ts")
		if err != nil {
			t.Fatal(err)
		}
		return record
	}

	h.route("POST", "/v1/messages/batches", 200, "application/json", []byte(`{"id":"msgbatch_ts","type":"message_batch","processing_status":"in_progress","request_counts":{"processing":5,"succeeded":0,"errored":0,"canceled":0,"expired":0},"results_url":null}`))
	if w := h.do(h.gateway(nil), "POST", "/v1/messages/batches", "application/json", anthropicBatchBody(5)); w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	created := now
	if r := load(); !r.CreatedAt.Equal(created) || !r.SettledAt.IsZero() {
		t.Fatalf("after create: %+v", r)
	}

	// A partial fetch settles nothing and rewrites no timestamp.
	now = now.Add(time.Hour)
	h.route("GET", "/v1/messages/batches/msgbatch_ts/results", 200, "application/binary", fixture(t, "anthropic", "batch-results-partial.jsonl"))
	h.do(h.gateway(nil), "GET", "/v1/messages/batches/msgbatch_ts/results", "", nil)
	if r := load(); !r.CreatedAt.Equal(created) || !r.SettledAt.IsZero() {
		t.Fatalf("after partial fetch: %+v", r)
	}

	now = now.Add(time.Hour)
	settled := now
	h.route("GET", "/v1/messages/batches/msgbatch_ts/results", 200, "application/binary", fixture(t, "anthropic", "batch-results.jsonl"))
	h.do(h.gateway(nil), "GET", "/v1/messages/batches/msgbatch_ts/results", "", nil)
	if r := load(); !r.Settled || !r.CreatedAt.Equal(created) || !r.SettledAt.Equal(settled) {
		t.Fatalf("after settling fetch: %+v", r)
	}

	// Re-fetching later changes nothing.
	now = now.Add(24 * time.Hour)
	h.do(h.gateway(nil), "GET", "/v1/messages/batches/msgbatch_ts/results", "", nil)
	if r := load(); !r.CreatedAt.Equal(created) || !r.SettledAt.Equal(settled) {
		t.Fatalf("after re-fetch: %+v", r)
	}
}
