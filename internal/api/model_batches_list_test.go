package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestListModelBatches(t *testing.T) {
	store := resources.NewMemoryStore()
	tenant := "tenant"
	h := NewConnectedHandler(store, nil, func(*http.Request) (string, error) { return tenant, nil }, nil, true)
	put := func(owner modelapi.Owner, rec modelapi.BatchRecord) {
		t.Helper()
		rec.Owner = owner
		spec, _ := json.Marshal(rec)
		if _, err := store.Put(context.Background(), resources.Record{TenantID: owner.TenantID, Kind: modelBatchKind, ID: batchRecordID(owner, rec.Provider, rec.BatchID), Spec: spec}, 0); err != nil {
			t.Fatal(err)
		}
	}
	a := modelapi.Owner{TenantID: "tenant", PrincipalID: "alice", SurfaceID: "s1"}
	b := modelapi.Owner{TenantID: "tenant", PrincipalID: "alice", SurfaceID: "s2"}
	put(a, modelapi.BatchRecord{Provider: "openai", BatchID: "b1", AttemptID: "att-1", CreateOperation: "openai.v1.batches.create", Expected: -1})
	put(a, modelapi.BatchRecord{Provider: "openai", BatchID: "b2", Expected: 0, Ended: true, Status: "completed", Settled: true})
	put(b, modelapi.BatchRecord{Provider: "anthropic", BatchID: "b3", Expected: 7})
	put(a, modelapi.BatchRecord{Provider: "openai", BatchID: "file-out", AliasOf: "b2", AliasPart: "output"})
	put(modelapi.Owner{TenantID: "other", SurfaceID: "s1"}, modelapi.BatchRecord{Provider: "openai", BatchID: "foreign"})

	call := func(query string, roles []models.IAMRole) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/llm/batches"+query, nil)
		access := &models.ResolvedAccess{Roles: roles}
		r = r.WithContext(context.WithValue(r.Context(), keyWIFAccess, access))
		w := httptest.NewRecorder()
		h.ListModelBatches(w, r)
		return w
	}
	admin := []models.IAMRole{models.RoleAdmin}
	decode := func(w *httptest.ResponseRecorder) ModelBatchPage {
		t.Helper()
		var page ModelBatchPage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatalf("status %d %s", w.Code, w.Body.String())
		}
		return page
	}
	if w := call("", nil); w.Code != 403 {
		t.Fatal("batches listed without admin role", w.Code)
	}
	all := decode(call("", admin))
	if len(all.Batches) != 3 {
		t.Fatalf("expected 3 batches (no alias, no foreign tenant): %+v", all.Batches)
	}
	open := decode(call("?state=open", admin))
	if len(open.Batches) != 2 {
		t.Fatalf("open: %+v", open.Batches)
	}
	for _, v := range open.Batches {
		if v.State != "open" {
			t.Fatal("non-open batch returned", v)
		}
		if v.BatchID == "b1" && v.Expected != nil {
			t.Fatal("unknown expected count must be null, not zero")
		}
		if v.BatchID == "b3" && (v.Expected == nil || *v.Expected != 7) {
			t.Fatal("known expected count lost")
		}
	}
	settled := decode(call("?state=settled", admin))
	if len(settled.Batches) != 1 || settled.Batches[0].BatchID != "b2" || !settled.Batches[0].Ended {
		t.Fatalf("settled: %+v", settled.Batches)
	}
	if s2 := decode(call("?surface_id=s2", admin)); len(s2.Batches) != 1 || s2.Batches[0].SurfaceID != "s2" {
		t.Fatalf("surface filter: %+v", s2.Batches)
	}
	first := decode(call("?limit=2", admin))
	if len(first.Batches) != 2 || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	second := decode(call("?limit=2&cursor="+first.NextCursor, admin))
	if len(second.Batches) != 1 || second.NextCursor != "" || second.Batches[0].ID == first.Batches[0].ID || second.Batches[0].ID == first.Batches[1].ID {
		t.Fatalf("second page: %+v", second)
	}
	for _, bad := range []string{"?state=weird", "?limit=0", "?limit=501", "?limit=x"} {
		if w := call(bad, admin); w.Code != 400 {
			t.Fatal(bad, w.Code)
		}
	}
	tenant = "empty"
	if page := decode(call("", admin)); page.Batches == nil || len(page.Batches) != 0 {
		t.Fatal("empty tenant must return an empty array")
	}
}
