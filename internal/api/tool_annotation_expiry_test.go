package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

func annotationHandler(t *testing.T, start time.Time) (*ConnectedHandler, *time.Time) {
	t.Helper()
	now := start
	previous := toolAnnotationClock
	toolAnnotationClock = func() time.Time { return now }
	t.Cleanup(func() { toolAnnotationClock = previous })
	return &ConnectedHandler{Store: resources.NewMemoryStore()}, &now
}

func hint(name string, readOnly bool) toolexec.ListedTool {
	return toolexec.ListedTool{Name: name, ReadOnlyHint: &readOnly}
}

// A listing that does not contain a tool must not extend that tool's hint.
func TestReadOnlyHintExpiresPerToolDespiteLaterListingsOfOtherPages(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h, now := annotationHandler(t, t0)
	ctx := context.Background()
	record := resources.Record{TenantID: "tenant", ID: "conn", Revision: 3}
	allowed := map[string]bool{"a": true, "b": true}

	h.observeAnnotations(ctx, "tenant", record, allowed, []toolexec.ListedTool{hint("a", true)})
	for _, step := range []time.Duration{6 * time.Hour, 12 * time.Hour, 18 * time.Hour, 23 * time.Hour} {
		*now = t0.Add(step)
		h.observeAnnotations(ctx, "tenant", record, allowed, []toolexec.ListedTool{hint("b", true)})
		if v, ok := h.readOnlyHint(ctx, "tenant", record, "a"); !ok || !v {
			t.Fatalf("a should still be valid at +%s", step)
		}
	}
	*now = t0.Add(toolAnnotationTTL + time.Minute)
	if _, ok := h.readOnlyHint(ctx, "tenant", record, "a"); ok {
		t.Fatal("a stayed valid past T0+TTL because other pages were listed")
	}
	if v, ok := h.readOnlyHint(ctx, "tenant", record, "b"); !ok || !v {
		t.Fatal("b was observed recently and must stay valid")
	}
	// A further listing of another page drops the expired hint instead of refreshing it.
	h.observeAnnotations(ctx, "tenant", record, allowed, []toolexec.ListedTool{hint("b", true)})
	if _, ok := h.readOnlyHint(ctx, "tenant", record, "a"); ok {
		t.Fatal("expired hint was resurrected by a later listing")
	}
	saved, _ := h.Store.Get(ctx, "tenant", toolAnnotationsKind, "conn")
	var stored toolAnnotations
	_ = json.Unmarshal(saved.Spec, &stored)
	if _, carried := stored.ReadOnly["a"]; carried {
		t.Fatalf("expired hint carried forward: %+v", stored)
	}
	// Observing the tool itself renews it.
	h.observeAnnotations(ctx, "tenant", record, allowed, []toolexec.ListedTool{hint("a", true)})
	if v, ok := h.readOnlyHint(ctx, "tenant", record, "a"); !ok || !v {
		t.Fatal("re-observed hint should be valid")
	}
}

// Records written before per-tool times existed keep working: their tools
// count as observed at the record's time and then age out.
func TestReadOnlyHintReadsRecordsWithoutPerToolTimes(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h, now := annotationHandler(t, t0)
	ctx := context.Background()
	record := resources.Record{TenantID: "tenant", ID: "conn", Revision: 1}
	legacy := `{"connection_id":"conn","connection_revision":1,"observed_at":"` + t0.Format(time.RFC3339) + `","read_only_hints":{"old":true}}`
	if _, err := h.Store.Put(ctx, resources.Record{TenantID: "tenant", Kind: toolAnnotationsKind, ID: "conn", Spec: json.RawMessage(legacy)}, 0); err != nil {
		t.Fatal(err)
	}
	*now = t0.Add(time.Hour)
	if v, ok := h.readOnlyHint(ctx, "tenant", record, "old"); !ok || !v {
		t.Fatal("legacy record not readable")
	}
	h.observeAnnotations(ctx, "tenant", record, map[string]bool{"old": true, "new": true}, []toolexec.ListedTool{hint("new", true)})
	*now = t0.Add(toolAnnotationTTL + time.Minute)
	if _, ok := h.readOnlyHint(ctx, "tenant", record, "old"); ok {
		t.Fatal("legacy hint extended beyond its original observation time")
	}
	if v, ok := h.readOnlyHint(ctx, "tenant", record, "new"); !ok || !v {
		t.Fatal("new hint should be valid")
	}
}
