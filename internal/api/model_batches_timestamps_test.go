package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestListModelBatchesTimestampsOrderAndOlderThan(t *testing.T) {
	store := resources.NewMemoryStore()
	h := NewConnectedHandler(store, nil, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	owner := modelapi.Owner{TenantID: "tenant", PrincipalID: "alice", SurfaceID: "s1"}
	day := func(d int) time.Time { return time.Date(2026, 11, d, 8, 0, 0, 0, time.UTC) }
	for _, rec := range []modelapi.BatchRecord{
		{BatchID: "old-open", CreatedAt: day(1)},
		{BatchID: "mid-settled", CreatedAt: day(5), Settled: true, SettledAt: day(6)},
		{BatchID: "new-open", CreatedAt: day(9)},
		{BatchID: "legacy"}, // written before timestamps existed
	} {
		rec.Owner, rec.Provider, rec.Expected = owner, "openai", -1
		spec, _ := json.Marshal(rec)
		if _, err := store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: modelBatchKind, ID: batchRecordID(owner, rec.Provider, rec.BatchID), Spec: spec}, 0); err != nil {
			t.Fatal(err)
		}
	}
	call := func(query string) (ModelBatchPage, int, string) {
		r := httptest.NewRequest("GET", "/api/v1/llm/batches"+query, nil)
		r = r.WithContext(context.WithValue(r.Context(), keyWIFAccess, &models.ResolvedAccess{Roles: []models.IAMRole{models.RoleAdmin}}))
		w := httptest.NewRecorder()
		h.ListModelBatches(w, r)
		var page ModelBatchPage
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		return page, w.Code, w.Body.String()
	}
	ids := func(page ModelBatchPage) string {
		out := ""
		for _, b := range page.Batches {
			out += b.BatchID + ","
		}
		return out
	}

	page, code, body := call("?order=newest")
	if code != 200 || ids(page) != "new-open,mid-settled,old-open,legacy," {
		t.Fatalf("newest first, legacy last: %d %s", code, body)
	}
	if page.Batches[1].SettledAt == nil || !page.Batches[1].SettledAt.Equal(day(6)) || page.Batches[0].SettledAt != nil {
		t.Fatalf("settled_at: %s", body)
	}
	if !strings.Contains(body, `"created_at":"2026-11-09T08:00:00Z"`) {
		t.Fatalf("created_at not returned: %s", body)
	}
	if legacy := page.Batches[3]; legacy.CreatedAt != nil || legacy.SettledAt != nil || strings.Contains(strings.Split(body, `"batch_id":"legacy"`)[1], "created_at") {
		t.Fatalf("legacy record must omit timestamps: %s", body)
	}

	// Paging through order=newest visits every batch once.
	seen, cursor := "", ""
	for i := 0; i < 5; i++ {
		page, code, body = call("?order=newest&limit=1&cursor=" + cursor)
		if code != 200 {
			t.Fatal(body)
		}
		seen += ids(page)
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if seen != "new-open,mid-settled,old-open,legacy," {
		t.Fatalf("paging: %s", seen)
	}

	// Batches whose results were never fetched and are older than day 8.
	page, code, body = call("?state=open&order=newest&older_than=" + day(8).Format(time.RFC3339))
	if code != 200 || ids(page) != "old-open," {
		t.Fatalf("older_than excludes recent and legacy records: %d %s", code, body)
	}
	if page, _, _ = call("?older_than=" + day(1).Format(time.RFC3339)); len(page.Batches) != 0 {
		t.Fatal("older_than is exclusive")
	}

	// Default order is unchanged: by id, all records.
	if page, _, _ = call(""); len(page.Batches) != 4 {
		t.Fatal(ids(page))
	}
	for _, bad := range []string{"?order=oldest", "?older_than=yesterday", "?order=newest&cursor=abc", "?cursor=n:-:x"} {
		if _, code, _ := call(bad); code != 400 {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}
}
