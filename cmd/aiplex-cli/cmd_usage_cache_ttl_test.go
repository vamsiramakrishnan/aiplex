package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const ttlRecords = `{"records":[
{"id":"ul_openai","tenant_id":"t","started_at":"2026-11-09T10:00:00Z","finished_at":"2026-11-09T10:00:01Z","latency_ms":900,"plane":"llmplex","model_id":"gpt-model","status":"ok","source":"managed","requests":1,
 "usage":{"input_tokens":100,"output_tokens":3,"cache_read_tokens":40,"cache_write_tokens":30,"reasoning_tokens":null,"total_tokens":null,"cache_write_30m_tokens":12,"cache_write_12h_tokens":18},
 "cost":{"basis":"unknown"}},
{"id":"ul_anthropic","tenant_id":"t","started_at":"2026-11-09T09:00:00Z","finished_at":"2026-11-09T09:00:01Z","latency_ms":900,"plane":"llmplex","model_id":"claude-model","status":"ok","source":"managed","requests":1,
 "usage":{"input_tokens":100,"output_tokens":3,"cache_read_tokens":40,"cache_write_tokens":30,"reasoning_tokens":null,"total_tokens":null,"cache_write_5m_tokens":10,"cache_write_1h_tokens":20},
 "cost":{"basis":"unknown"}},
{"id":"ul_plain","tenant_id":"t","started_at":"2026-11-09T08:00:00Z","finished_at":"2026-11-09T08:00:01Z","latency_ms":900,"plane":"llmplex","model_id":"plain","status":"ok","source":"managed","requests":1,
 "usage":{"input_tokens":100,"output_tokens":3,"cache_read_tokens":null,"cache_write_tokens":30,"reasoning_tokens":null,"total_tokens":null},
 "cost":{"basis":"unknown"}}
]}`

func ttlServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/usage/records":
			w.Write([]byte(ttlRecords))
		case "/api/v1/usage/records/ul_openai":
			w.Write([]byte(`{"id":"ul_openai","tenant_id":"t","started_at":"2026-11-09T10:00:00Z","finished_at":"2026-11-09T10:00:01Z","latency_ms":900,"plane":"llmplex","model_id":"gpt-model","status":"ok","source":"managed","requests":1,"usage":{"input_tokens":100,"output_tokens":3,"cache_read_tokens":40,"cache_write_tokens":30,"reasoning_tokens":null,"total_tokens":null,"cache_write_30m_tokens":12,"cache_write_12h_tokens":18},"cost":{"basis":"unknown"}}`))
		case "/api/v1/usage/summary":
			w.Write([]byte(`{"group_by":["model_id"],"groups":[
{"key":{"model_id":"gpt-model"},"records":1,"requests":1,"errors":0,"denied":0,"unknown_status":0,"tokens":{"input_tokens":100,"cache_write_tokens":30},"tokens_unknown":{},"unknown_usage_records":0,"cost":[],"unknown_cost_records":1,"modality_tokens":{"cache_write_30m_tokens":12,"cache_write_12h_tokens":18}},
{"key":{"model_id":"plain"},"records":1,"requests":1,"errors":0,"denied":0,"unknown_status":0,"tokens":{"input_tokens":100,"cache_write_tokens":30},"tokens_unknown":{},"unknown_usage_records":0,"cost":[],"unknown_cost_records":1}],"records_scanned":2,"truncated":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func prefixedLine(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no %s row in\n%s", prefix, out)
	return ""
}

// A group of two records, one with a TTL breakdown (30 = 5m 10 + 1h 20) and
// one with a total only (70): the total sums both, the TTL figures only the
// record that reported them, so they need not add up to the total.
func TestUsageWideSummaryTTLsCountOnlyReportingRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"group_by":["model_id"],"groups":[
{"key":{"model_id":"claude-model"},"records":2,"requests":2,"errors":0,"denied":0,"unknown_status":0,"tokens":{"input_tokens":200,"cache_write_tokens":100},"tokens_unknown":{},"unknown_usage_records":0,"cost":[],"unknown_cost_records":2,"modality_tokens":{"cache_write_5m_tokens":10,"cache_write_1h_tokens":20}}],"records_scanned":2,"truncated":false}`))
	}))
	defer server.Close()
	out, err := runUsage(t, server, "wide", "summary", "--by", "model")
	if err != nil {
		t.Fatal(err)
	}
	if line := prefixedLine(t, out, "claude-model"); !strings.Contains(line, " 100 (5m=10 1h=20) ") {
		t.Errorf("summary cache-write cell: %q", line)
	}
}

// -o wide shows each reported cache-write TTL beside the cache-write total,
// never added to it; a record without a breakdown shows the total alone.
func TestUsageWideShowsCacheWriteTTLs(t *testing.T) {
	server := ttlServer(t)
	defer server.Close()
	out, err := runUsage(t, server, "wide", "records")
	if err != nil {
		t.Fatal(err)
	}
	if line := prefixedLine(t, out, "ul_openai"); !strings.Contains(line, "30 (30m=12 12h=18)") {
		t.Errorf("openai TTLs: %q", line)
	}
	if line := prefixedLine(t, out, "ul_anthropic"); !strings.Contains(line, "30 (5m=10 1h=20)") {
		t.Errorf("anthropic TTLs: %q", line)
	}
	if line := prefixedLine(t, out, "ul_plain"); strings.Contains(line, "30 (") {
		t.Errorf("record without a breakdown shows one: %q", line)
	}
	summary, err := runUsage(t, server, "wide", "summary", "--by", "model")
	if err != nil {
		t.Fatal(err)
	}
	if line := prefixedLine(t, summary, "gpt-model"); !strings.Contains(line, "30 (30m=12 12h=18)") || strings.Contains(line, "cache_write") {
		t.Errorf("summary TTLs: %q", line)
	}
	detail, err := runUsage(t, server, "table", "get", "ul_openai")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cache_write_30m_tokens (subset):", "cache_write_12h_tokens (subset):", "cache_write_5m_tokens (subset):"} {
		if !strings.Contains(detail, want) {
			t.Errorf("missing %q in\n%s", want, detail)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(prefixedLine(t, detail, "cache_write_5m_tokens (subset):")), "unknown") || !strings.HasSuffix(strings.TrimSpace(prefixedLine(t, detail, "cache_write_12h_tokens (subset):")), "18") {
		t.Errorf("subset values:\n%s", detail)
	}
}
