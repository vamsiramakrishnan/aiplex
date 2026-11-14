package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// The request_id, client_request_id and trace_id filters find the ledger
// record of one call (docs/usage-ledger.md, "Find the record of a call").

const (
	testTraceA = "0af7651916cd43dd8448eb211c80319c"
	testTraceB = "4bf92f3577b34da6a3ce929d0e0e4736"
)

func correlationFixture(t *testing.T) *UsageHandler {
	t.Helper()
	store := usageledger.NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	add := func(id, principal, requestID, clientRequestID, traceID string) {
		r := usageledger.Record{ID: id, TenantID: "tenant", StartedAt: now, FinishedAt: now, Plane: usageledger.PlaneLLM, PrincipalID: principal, RequestID: requestID, TraceID: traceID, Status: usageledger.StatusOK, Source: usageledger.SourceManaged, Requests: 1, Cost: usageledger.UnknownCost()}
		r.ClientRequestID = clientRequestID
		if err := store.Append(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	add("r-alice", "alice", "req-alice", "by-alice-1", testTraceA)
	add("r-bob", "bob", "req-bob", "by-bob-1", testTraceB)
	add("r-bob-2", "bob", "req-bob", "by-bob-2", testTraceB)
	add("r-bare", "alice", "", "", "")
	return NewUsageHandler(store, func(*http.Request) (string, error) { return "tenant", nil }, false)
}

func TestUsageCorrelationFiltersFindOneCall(t *testing.T) {
	h := correlationFixture(t)
	admin := adminAccess("root")
	for query, want := range map[string]string{
		"client_request_id=by-alice-1":                  "r-alice",
		"client_request_id=by-bob-2":                    "r-bob-2",
		"request_id=req-alice":                          "r-alice",
		"trace_id=" + testTraceA:                        "r-alice",
		"client_request_id=by-bob-1&request_id=req-bob": "r-bob",
		"client_request_id=none":                        "",
		"request_id=req-bo":                             "", // an exact match, never a prefix
	} {
		ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?"+query, admin, 200))
		if strings.Join(ids, ",") != want {
			t.Errorf("%s: %v, want %q", query, ids, want)
		}
	}
	// Several records can share a request id or a trace.
	if ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?request_id=req-bob", admin, 200)); len(ids) != 2 {
		t.Errorf("a shared request id finds both records: %v", ids)
	}
	if ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?trace_id="+testTraceB, admin, 200)); len(ids) != 2 {
		t.Errorf("a trace finds its records: %v", ids)
	}
	// The summary route shares the filter set.
	var summary usageledger.Summary
	body := usageGet(t, h.Summary, "/api/v1/usage/summary?client_request_id=by-alice-1", admin, 200)
	if err := json.Unmarshal(body, &summary); err != nil || summary.RecordsScanned != 1 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
}

func TestUsageCorrelationFiltersDoNotWidenVisibility(t *testing.T) {
	h := correlationFixture(t)
	alice := viewerAccess("alice")
	if ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?client_request_id=by-alice-1", alice, 200)); strings.Join(ids, ",") != "r-alice" {
		t.Fatalf("a caller finds her own record: %v", ids)
	}
	for _, query := range []string{"client_request_id=by-bob-1", "request_id=req-bob", "trace_id=" + testTraceB} {
		if ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?"+query, alice, 200)); len(ids) != 0 {
			t.Errorf("%s: a non-admin read another caller's record: %v", query, ids)
		}
	}
	var summary usageledger.Summary
	if err := json.Unmarshal(usageGet(t, h.Summary, "/api/v1/usage/summary?client_request_id=by-bob-1", alice, 200), &summary); err != nil || summary.RecordsScanned != 0 {
		t.Fatalf("summary widened visibility: %+v %v", summary, err)
	}
	// Combining a filter with the caller's own principal id does not help either.
	if ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?principal_id=bob&client_request_id=by-bob-1", alice, 200)); len(ids) != 0 {
		t.Fatalf("%v", ids)
	}
}

func TestUsageCorrelationFiltersAreValidated(t *testing.T) {
	h := correlationFixture(t)
	admin := adminAccess("root")
	for _, query := range []string{
		"client_request_id=has%20space",
		"client_request_id=has%0Anewline",
		"client_request_id=" + strings.Repeat("a", 129),
		"client_request_id=a&client_request_id=b",
		"client_request_id=semi;colon",
		"request_id=bad/slash",
		"request_id=" + strings.Repeat("a", 129),
		"trace_id=" + strings.ToUpper(testTraceA),
		"trace_id=short",
		"trace_id=" + strings.Repeat("0", 32),
		"trace_id=" + testTraceA + "0",
	} {
		w := usageGet(t, h.ListRecords, "/api/v1/usage/records?"+query, admin, 400)
		if !strings.Contains(string(w), "INVALID_QUERY") {
			t.Errorf("%s: %s", query, w)
		}
	}
	usageGet(t, h.Summary, "/api/v1/usage/summary?trace_id=bad", admin, 400)
	// The longest stored value is accepted.
	usageGet(t, h.ListRecords, "/api/v1/usage/records?client_request_id="+strings.Repeat("a", 128), admin, 200)
}

// A correlation filter that is present but empty is refused: it must never
// read as "no filter" and return an unrelated record.
func TestUsageCorrelationFiltersRefuseAnEmptyValue(t *testing.T) {
	h := correlationFixture(t)
	for _, name := range []string{"client_request_id", "request_id", "trace_id"} {
		for _, query := range []string{name + "=", name + "=&plane=llmplex"} {
			usageGet(t, h.ListRecords, "/api/v1/usage/records?"+query, adminAccess("root"), 400)
			usageGet(t, h.Summary, "/api/v1/usage/summary?"+query, adminAccess("root"), 400)
		}
	}
}
