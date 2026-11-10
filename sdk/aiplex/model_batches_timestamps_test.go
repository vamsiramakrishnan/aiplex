package aiplex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestListModelBatchesOrderOlderThanAndTimestamps(t *testing.T) {
	var query map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		_, _ = w.Write([]byte(`{"batches":[{"id":"i1","surface_id":"oa","provider":"openai","batch_id":"b1","create_operation":"op","state":"settled","ended":true,"expected":2,"created_at":"2026-11-01T08:00:00Z","settled_at":"2026-11-02T08:00:00Z"},{"id":"i2","surface_id":"oa","provider":"openai","batch_id":"b2","create_operation":"op","state":"open","ended":false,"expected":null}]}`))
	}))
	defer server.Close()
	cutoff := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	page, err := aiplex.NewClient(server.URL).ListModelBatches(context.Background(), aiplex.ModelBatchQuery{State: "open", Order: "newest", OlderThan: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if query["order"][0] != "newest" || query["older_than"][0] != "2026-11-08T00:00:00Z" {
		t.Fatalf("query = %v", query)
	}
	if page.Batches[0].CreatedAt == nil || page.Batches[0].SettledAt == nil || page.Batches[1].CreatedAt != nil || page.Batches[1].SettledAt != nil {
		t.Fatalf("timestamps: %+v", page.Batches)
	}
}
