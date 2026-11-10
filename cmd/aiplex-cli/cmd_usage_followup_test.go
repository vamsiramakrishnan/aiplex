package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const cliBoundedRecords = `{"records":[
 {"id":"ul_s","tenant_id":"t","started_at":"2026-11-09T10:00:00Z","finished_at":"2026-11-09T10:00:01Z","latency_ms":9,"plane":"llmplex","operation":"openai.v1.realtime","access_point_id":"ide","caller_kind":"agent","streaming":true,"session":true,"status":"unknown","source":"managed","requests":1,
  "usage":{"input_tokens":120,"output_tokens":30,"cache_read_tokens":null,"cache_write_tokens":null,"reasoning_tokens":null,"total_tokens":null},"usage_bound":"lower_bound","cost":{"basis":"unknown"}},
 {"id":"ul_f","tenant_id":"t","started_at":"2026-11-09T09:00:00Z","finished_at":"2026-11-09T09:00:01Z","latency_ms":5,"plane":"llmplex","streaming":false,"status":"ok","source":"managed","requests":1,
  "usage":{"input_tokens":7,"output_tokens":null,"cache_read_tokens":null,"cache_write_tokens":null,"reasoning_tokens":null,"total_tokens":null},"cost":{"basis":"unknown"}}]}`

func TestUsageRecordsFilterByAccessPointAndShowBoundAndSession(t *testing.T) {
	var query map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Write([]byte(cliBoundedRecords))
	}))
	defer server.Close()
	out, err := runUsage(t, server, "wide", "records", "--access-point", "ide", "--caller-kind", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if query["access_point_id"][0] != "ide" || query["caller_kind"][0] != "agent" {
		t.Fatalf("query = %v", query)
	}
	var s, f string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "ul_s") {
			s = l
		}
		if strings.HasPrefix(l, "ul_f") {
			f = l
		}
	}
	if !strings.Contains(s, "120 (lower_bound)") || !strings.Contains(s, "30 (lower_bound)") || !strings.Contains(s, "session") {
		t.Fatalf("bounded session row = %q", s)
	}
	if strings.Contains(f, "lower_bound") || strings.Contains(f, "session") {
		t.Fatalf("final row must carry no bound or session marker: %q", f)
	}
}

func TestUsageGetShowsSessionAndBound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.TrimSuffix(strings.TrimPrefix(strings.Split(cliBoundedRecords, "\n {\"id\":\"ul_f\"")[0], `{"records":[` + "\n"), ",")))
	}))
	defer server.Close()
	out, err := runUsage(t, server, "table", "get", "ul_s")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"session:", "true", "usage_bound:", "lower_bound", "input_tokens:", "120 (lower_bound)", "access_point_id:"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail lacks %q:\n%s", want, out)
		}
	}
}

func TestUsageSummaryByAccessPointShowsBoundedCounts(t *testing.T) {
	var query map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Write([]byte(`{"group_by":["access_point_id","caller_kind"],"groups":[{"key":{"access_point_id":"ide","caller_kind":"agent"},"records":3,"requests":3,"errors":0,"denied":0,"unknown_status":0,"tokens":{"input_tokens":150},"tokens_unknown":{},"unknown_usage_records":0,"bounded_usage_records":2,"lower_bound_records":1,"ambiguous_usage_records":1,"cost":[],"unknown_cost_records":3}],"records_scanned":3,"truncated":false}`))
	}))
	defer server.Close()
	out, err := runUsage(t, server, "wide", "summary", "--by", "access-point,caller-kind", "--access-point", "ide")
	if err != nil {
		t.Fatal(err)
	}
	if query["group_by"][0] != "access_point_id,caller_kind" || query["access_point_id"][0] != "ide" {
		t.Fatalf("query = %v", query)
	}
	for _, want := range []string{"ACCESS POINT", "CALLER KIND", "150 [incl. 2 bounded]", "2 (lower_bound:1 ambiguous:1)", "NOTE:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestUsageBatchErrorClassAndBoundFlags(t *testing.T) {
	var query map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Write([]byte(`{"records":[]}`))
	}))
	defer server.Close()
	if _, err := runUsage(t, server, "wide", "records", "--batch=false", "--error-class", "timeout", "--bound", "none"); err != nil {
		t.Fatal(err)
	}
	if query["batch"][0] != "false" || query["error_class"][0] != "timeout" || query["usage_bound"][0] != "none" {
		t.Fatalf("query = %v", query)
	}
	if _, err := runUsage(t, server, "wide", "records", "--batch"); err != nil || query["batch"][0] != "true" {
		t.Fatalf("bare --batch means true: %v %v", err, query)
	}
	if _, err := runUsage(t, server, "wide", "records", "--bound", "final"); err == nil {
		t.Fatal("unknown bound must be rejected before the request")
	}
	if _, err := runUsage(t, server, "wide", "summary", "--by", "error-class,bound,batch,source"); err != nil {
		t.Fatal(err)
	}
	if query["group_by"][0] != "error_class,usage_bound,batch,source" {
		t.Fatalf("query = %v", query)
	}
}
