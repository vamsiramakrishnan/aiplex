//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// claudeCodeLogs is an OTLP/JSON export built from the documented Claude Code
// api_request event (code.claude.com/docs/en/monitoring-usage), not captured
// from a real client. at is the event time.
func claudeCodeLogs(at time.Time, clientRequestID, sequence string) string {
	attr := func(k, v string) string { return fmt.Sprintf(`{"key":%q,"value":{"stringValue":%q}}`, k, v) }
	num := func(k string, v int) string { return fmt.Sprintf(`{"key":%q,"value":{"intValue":"%d"}}`, k, v) }
	return fmt.Sprintf(`{"resourceLogs":[{"resource":{"attributes":[%s,%s,%s]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"%d","body":{"stringValue":"claude_code.api_request"},"attributes":[%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s]}]}]}]}`,
		attr("service.name", "claude-code"), attr("service.version", "2.1.312"), attr("user.email", "someone@example.com"),
		at.UnixNano(),
		attr("event.name", "api_request"), num("event.sequence", 0), attr("session.id", "e2e-session"), attr("prompt.id", "e2e-prompt"),
		attr("model", "claude-e2e"), num("cost_usd_micros", 4200), num("duration_ms", 900),
		num("input_tokens", 100), num("output_tokens", 20), num("cache_read_tokens", 30), num("cache_creation_tokens", 0),
		attr("client_request_id", clientRequestID)+","+attr("request_id", "req_e2e_"+sequence))
}

func postOTLP(e *env, signal, body string, headers ...string) httpResp {
	return e.do("POST", "/otlp/v1/"+signal, body, append(e.auth(), append([]string{"Content-Type", "application/json"}, headers...)...)...)
}

// TestOTLPIngestRecordsReportedUsage posts harness telemetry to a running
// `aiplex dev` and reads it back from the ledger: identity from the token,
// idempotent retries, separate sources in summaries and the correlation rule.
func TestOTLPIngestRecordsReportedUsage(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	seeded, err := e.stack.Seed(ctx, devstack.SeedOptions{})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := time.Now().UTC().Add(-time.Second)

	// A managed exchange that carries the harness's per-request id.
	chat := e.post("/ap/"+seeded.AccessPoint+"/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"hi"}]}`, "X-Client-Request-Id", "e2e-client-req-1")
	if chat.Status != 200 {
		t.Fatalf("chat: %d %s", chat.Status, chat.Body)
	}

	// The harness reports the same request (correlated) and another (not).
	for _, c := range []struct{ id, seq string }{{"e2e-client-req-1", "1"}, {"e2e-client-req-2", "2"}} {
		w := postOTLP(e, "logs", claudeCodeLogs(now, c.id, c.seq))
		if w.Status != 200 {
			t.Fatalf("otlp logs: %d %s", w.Status, w.Body)
		}
	}
	// A retry of the first export is idempotent.
	if w := postOTLP(e, "logs", claudeCodeLogs(now, "e2e-client-req-1", "1")); w.Status != 200 {
		t.Fatalf("retry: %d %s", w.Status, w.Body)
	}
	// Metrics and traces are accepted and not recorded; gzip works.
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write([]byte(`{"resourceMetrics":[]}`))
	_ = zw.Close()
	req, _ := http.NewRequest("POST", e.stack.APIURL+"/otlp/v1/metrics", &zipped)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Authorization", "Bearer "+e.stack.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("gzip metrics: %v %v", resp, err)
	}
	resp.Body.Close()
	eventually(t, 10*time.Second, func() error {
		r := e.do("GET", "/api/v1/usage/records?source=reported&harness=claude-code", "", e.auth()...)
		var page struct {
			Records []json.RawMessage `json:"records"`
		}
		if err := json.Unmarshal([]byte(r.Body), &page); err != nil || len(page.Records) != 2 {
			return fmt.Errorf("reported records: %s (%v)", r.Body, err)
		}
		return nil
	})
	r := e.do("GET", "/api/v1/usage/records?source=reported&limit=10", "", e.auth()...)
	var page struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal([]byte(r.Body), &page); err != nil {
		t.Fatal(err)
	}
	for _, raw := range page.Records {
		var one struct {
			Source, Harness string
			HarnessVersion  string `json:"harness_version"`
			SessionID       string `json:"session_id"`
			ClientRequestID string `json:"client_request_id"`
			ModelID         string `json:"model_id"`
			PrincipalID     string `json:"principal_id"`
			CallerKind      string `json:"caller_kind"`
			Cost            struct{ Basis, Currency, Amount string }
			Usage           struct {
				InputTokens int64 `json:"input_tokens"`
				TotalTokens int64 `json:"total_tokens"`
			}
		}
		if err := json.Unmarshal(raw, &one); err != nil {
			t.Fatal(err)
		}
		// 100 input + 30 cache read + 0 cache write; Claude Code excludes cache from input_tokens.
		if one.Source != "reported" || one.Harness != "claude-code" || one.HarnessVersion != "2.1.312" || one.SessionID != "e2e-session" || one.ModelID != "claude-e2e" ||
			one.Cost.Basis != "reported" || one.Cost.Currency != "USD" || one.Cost.Amount != "4200" || one.Usage.InputTokens != 130 || one.Usage.TotalTokens != 150 || one.PrincipalID == "" || one.CallerKind == "" {
			t.Fatalf("reported record: %s", raw)
		}
		if strings.Contains(string(raw), "someone@example.com") {
			t.Fatalf("a payload label became part of the record: %s", raw)
		}
	}
	if len(page.Records) != 2 {
		t.Fatalf("a retried export must not duplicate records: %d", len(page.Records))
	}

	// Summaries keep the sources apart and leave out the correlated twin.
	sum := e.mustCLI("usage", "summary", "-o", "json")
	var s struct {
		SourceSplit                bool     `json:"source_split"`
		ExcludedCorrelatedReported int      `json:"excluded_correlated_reported"`
		Notes                      []string `json:"notes"`
		Groups                     []struct {
			Key     map[string]string `json:"key"`
			Records int               `json:"records"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(sum.Out), &s); err != nil {
		t.Fatalf("%v\n%s", err, sum.Out)
	}
	counts := map[string]int{}
	for _, g := range s.Groups {
		counts[g.Key["source"]] += g.Records
	}
	if !s.SourceSplit || s.ExcludedCorrelatedReported != 1 || counts["reported"] != 1 || counts["managed"] < 1 || len(s.Notes) == 0 {
		t.Fatalf("summary: %s", sum.Out)
	}
	table := e.mustCLI("usage", "summary", "--source", "reported", "--by", "harness,model")
	contains(t, table.Out, "claude-code", "reported summary")
	contains(t, table.Out, "(reported)", "cost basis")
	notContains(t, table.Out, "(settled)", "no managed basis in the reported summary")

	// Rejected shapes.
	if w := postOTLP(e, "logs", `{"resourceLogs":`+strings.Repeat("[", 10)); w.Status != 400 {
		t.Fatalf("malformed export: %d %s", w.Status, w.Body)
	}
	if w := e.do("POST", "/otlp/v1/logs", "x", append(e.auth(), "Content-Type", "text/plain")...); w.Status != 415 {
		t.Fatalf("content type: %d", w.Status)
	}
}
