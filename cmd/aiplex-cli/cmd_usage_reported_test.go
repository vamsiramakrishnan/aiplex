package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const cliReportedSummary = `{"group_by":["model_id","source"],"source_split":true,"excluded_correlated_reported":2,
 "notes":["Managed and reported records are separate groups and are not added together. Reported records that share a trace, client request id or request id with a managed record are left out."],
 "groups":[
  {"key":{"model_id":"claude-sonnet-4-5","source":"managed"},"records":3,"requests":3,"errors":0,"denied":0,"unknown_status":0,"tokens":{"input_tokens":30},"tokens_unknown":{"input_tokens":0},"unknown_usage_records":0,"cost":[{"currency":"USD","basis":"estimated","amount":"900","scale":6,"records":3}],"unknown_cost_records":0,
   "feedback":{"records":2,"without_feedback":1,"positive":1,"negative":0,"neutral":1,"average_score":0.5}},
  {"key":{"model_id":"claude-sonnet-4-5","source":"reported"},"records":1,"requests":1,"errors":0,"denied":0,"unknown_status":0,"tokens":{"input_tokens":10},"tokens_unknown":{},"unknown_usage_records":0,"cost":[{"currency":"USD","basis":"reported","amount":"12346","scale":6,"records":1}],"unknown_cost_records":0,
   "feedback":{"records":0,"without_feedback":1,"positive":0,"negative":0,"neutral":0,"average_score":null}}],
 "records_scanned":6,"truncated":false}`

func TestUsageSummaryKeepsSourcesApartAndSaysSo(t *testing.T) {
	var query map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		_, _ = w.Write([]byte(cliReportedSummary))
	}))
	defer server.Close()
	out, err := runUsage(t, server, "wide", "summary", "--by", "model,tag:experiment", "--tag", "team=x", "--harness", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if query["group_by"][0] != "model_id,tag:experiment" || query["tag.team"][0] != "x" || query["harness"][0] != "claude-code" || query["feedback"][0] != "true" {
		t.Fatalf("query: %v", query)
	}
	for _, want := range []string{"SOURCE", "managed", "reported", "USD 0.012346 (reported)", "USD 0.000900 (estimated)", "avg +0.50", "1 without", "none (1 without)", "FEEDBACK",
		"2 reported records were left out", "NOTE: Managed and reported records are separate groups"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Without -o wide the feedback lookup is not requested.
	if _, err = runUsage(t, server, "table", "summary", "--merge-sources"); err != nil {
		t.Fatal(err)
	}
	if _, asked := query["feedback"]; asked || query["merge_sources"][0] != "true" {
		t.Fatalf("query: %v", query)
	}
	if _, err = runUsage(t, server, "table", "summary", "--tag", "BAD"); err == nil {
		t.Fatal("an invalid --tag was accepted")
	}
}

func TestUsageFeedbackCommand(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/usage/records/ul_1/feedback":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"fb_1","tenant_id":"t","record_id":"ul_1","by":"alice","created_at":"2026-11-09T10:00:00Z","score":-0.5,"label":"wrong"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/usage/records/ul_1/feedback":
			_, _ = w.Write([]byte(`{"record_id":"ul_1","feedback":[{"id":"fb_2","tenant_id":"t","record_id":"ul_1","by":"alice","created_at":"2026-11-09T11:00:00Z","score":1,"label":"helpful"},{"id":"fb_1","tenant_id":"t","record_id":"ul_1","by":"alice","created_at":"2026-11-09T10:00:00Z","score":-0.5}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/usage/records/ul_1":
			_, _ = w.Write([]byte(`{"id":"ul_1","tenant_id":"t","started_at":"2026-11-09T10:00:00Z","finished_at":"2026-11-09T10:00:01Z","latency_ms":1,"plane":"llmplex","status":"ok","source":"reported","harness":"claude-code","harness_version":"2.1.312","session_id":"s1","tags":{"experiment":"arm-a"},"reported_attributes":{"request_id":"req_1"},"requests":1,"usage":null,"cost":{"basis":"reported","currency":"USD","amount":"12346","scale":6},"feedback":{"id":"fb_2","tenant_id":"t","record_id":"ul_1","by":"alice","created_at":"2026-11-09T11:00:00Z","score":1,"label":"helpful"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	out, err := runUsage(t, server, "table", "feedback", "ul_1", "--score", "-0.5", "--label", "wrong")
	if err != nil || !strings.Contains(out, "Recorded feedback fb_1 on ul_1: score -0.5") || body["score"] != -0.5 || body["label"] != "wrong" {
		t.Fatalf("%v %s %v", err, out, body)
	}
	if _, found := body["comment"]; found {
		t.Fatal("an empty comment must not be sent")
	}
	if _, err = runUsage(t, server, "table", "feedback", "ul_1"); err == nil {
		t.Fatal("--score is required")
	}
	if _, err = runUsage(t, server, "table", "feedback", "ul_1", "--score", "2"); err == nil {
		t.Fatal("a score above 1 was accepted client-side")
	}
	out, err = runUsage(t, server, "table", "feedback", "ul_1", "--history")
	if err != nil || strings.Index(out, "helpful") > strings.Index(out, "-0.5") {
		t.Fatalf("history newest first: %v\n%s", err, out)
	}
	out, err = runUsage(t, server, "table", "get", "ul_1")
	for _, want := range []string{"harness:", "claude-code", "harness_version:", "2.1.312", "session_id:", "experiment=arm-a", "request_id=req_1", "feedback:", "helpful", "USD 0.012346 (reported)"} {
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("missing %q (%v) in:\n%s", want, err, out)
		}
	}
}
