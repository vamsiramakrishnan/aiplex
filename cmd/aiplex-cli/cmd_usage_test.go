package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func captureUsageStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn()
	w.Close()
	os.Stdout = original
	return <-done
}

func runUsage(t *testing.T, server *httptest.Server, format string, args ...string) (string, error) {
	t.Helper()
	oldURL, oldToken, oldOutput := apiURL, token, output
	apiURL, token, output = server.URL, "session", format
	t.Cleanup(func() { apiURL, token, output = oldURL, oldToken, oldOutput })
	var err error
	out := captureUsageStdout(t, func() {
		cmd := usageCmd()
		root := &cobra.Command{Use: "aiplex", SilenceUsage: true, SilenceErrors: true}
		root.AddCommand(cmd)
		root.SetArgs(append([]string{"usage"}, args...))
		err = root.Execute()
	})
	return out, err
}

func TestParseUsageTime(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"24h": "2026-11-08T12:00:00Z", "7d": "2026-11-02T12:00:00Z", "30m": "2026-11-09T11:30:00Z", "2w": "2026-10-26T12:00:00Z",
		"1h30m": "2026-11-09T10:30:00Z", "2026-11-01T00:00:00+01:00": "2026-10-31T23:00:00Z",
	} {
		got, err := parseUsageTime(in, now)
		if err != nil || got.Format(time.RFC3339) != want {
			t.Errorf("parseUsageTime(%q)=%v,%v want %s", in, got, err, want)
		}
	}
	if got, err := parseUsageTime("", now); err != nil || !got.IsZero() {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"yesterday", "-5h", "0h", "2026-13-01", "99999999999999d"} {
		if _, err := parseUsageTime(bad, now); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

const cliRecords = `{"records":[
 {"id":"ul_a","tenant_id":"t","started_at":"2026-11-09T10:00:00Z","finished_at":"2026-11-09T10:00:01Z","latency_ms":1200,"plane":"llmplex","model_id":"gemini-2.5-flash","actor_id":"tutor","principal_id":"stu","client_id":"cli","streaming":false,"status":"ok","source":"managed","requests":1,
  "usage":{"input_tokens":0,"output_tokens":null,"cache_read_tokens":null,"cache_write_tokens":null,"reasoning_tokens":null,"total_tokens":null},
  "cost":{"basis":"estimated","currency":"USD","amount":"1135","scale":6}},
 {"id":"ul_b","tenant_id":"t","started_at":"2026-11-09T09:00:00Z","finished_at":"2026-11-09T09:00:01Z","latency_ms":5,"plane":"mcplex","tool_name":"search","streaming":false,"status":"denied","source":"managed","requests":1,
  "usage":null,"cost":{"basis":"unknown"}}],"next_cursor":"CUR2"}`

func TestUsageRecordsTableShowsUnknownNotZero(t *testing.T) {
	var query map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/usage/records" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		query = r.URL.Query()
		w.Write([]byte(cliRecords))
	}))
	defer server.Close()
	out, err := runUsage(t, server, "table", "records", "--model", "gemini-2.5-flash", "--agent", "tutor", "--user", "stu", "--client", "cli", "--tool", "search", "--since", "24h", "--limit", "5")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"model_id": "gemini-2.5-flash", "actor_id": "tutor", "principal_id": "stu", "client_id": "cli", "tool_name": "search", "limit": "5"} {
		if len(query[name]) != 1 || query[name][0] != want {
			t.Errorf("query %s=%v want %s", name, query[name], want)
		}
	}
	if len(query["since"]) != 1 {
		t.Error("since not forwarded")
	}
	lines := strings.Split(out, "\n")
	var a, b string
	for _, l := range lines {
		if strings.HasPrefix(l, "ul_a") {
			a = l
		}
		if strings.HasPrefix(l, "ul_b") {
			b = l
		}
	}
	// Known zero input, unknown output, exact estimated cost.
	fa := strings.Fields(a)
	if !strings.Contains(a, "USD 0.001135 (estimated)") || fa[len(fa)-6] != "0" || fa[len(fa)-5] != "unknown" {
		t.Errorf("record a rendering wrong: %q", a)
	}
	// Null usage and unknown cost must never become zero.
	if strings.Contains(b, " 0 ") || strings.Count(b, "unknown") != 3 {
		t.Errorf("record b must show unknown for in/out/cost: %q", b)
	}
	if !strings.Contains(out, "--cursor CUR2") {
		t.Errorf("next cursor not shown: %s", out)
	}
}

func TestUsageRecordsJSONPassesNullsThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(cliRecords)) }))
	defer server.Close()
	out, err := runUsage(t, server, "json", "records")
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Records []map[string]any `json:"records"`
		Next    string           `json:"next_cursor"`
	}
	if err = json.Unmarshal([]byte(out), &decoded); err != nil || decoded.Next != "CUR2" || len(decoded.Records) != 2 {
		t.Fatal(out, err)
	}
	if decoded.Records[1]["usage"] != nil {
		t.Error("null usage was rewritten")
	}
	usage := decoded.Records[0]["usage"].(map[string]any)
	if v, ok := usage["output_tokens"]; !ok || v != nil {
		t.Error("null counter dropped or zeroed")
	}
	if v := usage["input_tokens"]; v != float64(0) {
		t.Error("reported zero lost")
	}
	if cost := decoded.Records[1]["cost"].(map[string]any); cost["basis"] != "unknown" || cost["amount"] != nil || cost["scale"] != nil {
		t.Error("unknown cost gained an amount", cost)
	}
}

func TestUsageRecordsAllFollowsCursorsAndEmptyState(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Query().Get("limit") != "1000" {
			t.Errorf("--all should request the largest page: %v", r.URL.Query())
		}
		if r.URL.Query().Get("cursor") == "" {
			w.Write([]byte(`{"records":[{"id":"r1","usage":null,"cost":{"basis":"unknown"}}],"next_cursor":"n"}`))
			return
		}
		w.Write([]byte(`{"records":[{"id":"r2","usage":null,"cost":{"basis":"unknown"}}]}`))
	}))
	defer server.Close()
	out, err := runUsage(t, server, "json", "records", "--all")
	if err != nil || pages != 2 || !strings.Contains(out, `"r1"`) || !strings.Contains(out, `"r2"`) || !strings.Contains(out, `"truncated": false`) {
		t.Fatal(out, err, pages)
	}
	if _, err = runUsage(t, server, "table", "records", "--all", "--cursor", "x"); err == nil {
		t.Fatal("--all with --cursor accepted")
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"records":[]}`)) }))
	defer empty.Close()
	out, err = runUsage(t, empty, "table", "records")
	if err != nil || !strings.Contains(out, "No usage records match") {
		t.Fatal(out, err)
	}
}

func TestUsageGetShowsEveryFieldAndUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/usage/records/ul_b" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`{"id":"ul_b","tenant_id":"t","started_at":"2026-11-09T09:00:00Z","finished_at":"2026-11-09T09:00:01Z","latency_ms":5,"plane":"mcplex","tool_name":"search","trace_id":"tr","streaming":true,"status":"denied","source":"managed","requests":1,"usage":null,"cost":{"basis":"unknown"}}`))
	}))
	defer server.Close()
	out, err := runUsage(t, server, "table", "get", "ul_b")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(strings.ReplaceAll(out, "\n", " | ")), " ")
	for _, want := range []string{"trace_id: tr", "quota_attempt_id: —", "streaming: true", "input_tokens: unknown", "total_tokens: unknown", "cost: unknown", "http_status: —"} {
		if !strings.Contains(flat, want) {
			t.Errorf("detail missing %q in:\n%s", want, out)
		}
	}
}

const cliSummary = `{"group_by":["model_id","actor_id"],"records_scanned":7,"truncated":true,"groups":[
 {"key":{"model_id":"m1","actor_id":"a"},"records":4,"requests":4,"errors":1,"denied":0,"unknown_status":0,
  "tokens":{"input_tokens":120,"output_tokens":30},"tokens_unknown":{"input_tokens":1,"cache_read_tokens":4},"unknown_usage_records":0,
  "cost":[{"currency":"USD","basis":"estimated","amount":"1135","scale":6,"records":3},{"currency":"USD","basis":"settled","amount":"250","scale":2,"records":1},{"currency":"EUR","basis":"estimated","amount":"5","scale":3,"records":1}],"unknown_cost_records":0},
 {"key":{"model_id":"m2","actor_id":""},"records":3,"requests":3,"errors":0,"denied":2,"unknown_status":0,
  "tokens":{},"tokens_unknown":{"input_tokens":3,"output_tokens":3},"unknown_usage_records":3,"cost":[],"unknown_cost_records":3}]}`

func TestUsageSummaryAliasesTableUnknownsAndTruncation(t *testing.T) {
	var query map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Write([]byte(cliSummary))
	}))
	defer server.Close()
	out, err := runUsage(t, server, "table", "summary", "--by", "model,agent", "--bucket", "day", "--status", "error")
	if err != nil {
		t.Fatal(err)
	}
	if query["group_by"][0] != "model_id,actor_id" || query["bucket"][0] != "day" || query["status"][0] != "error" {
		t.Fatal(query)
	}
	if !strings.Contains(out, "120 (+1 unknown)") {
		t.Errorf("partial token coverage hidden:\n%s", out)
	}
	for _, want := range []string{"USD 0.001135 (estimated)", "USD 2.50 (settled)", "EUR 0.005 (estimated)", "usage:3 cost:3", "7 records scanned", "WARNING: truncated"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	var unknownRow string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "m2") {
			unknownRow = l
		}
	}
	if unknownRow == "" || strings.Count(unknownRow, "unknown") != 3 || !strings.Contains(unknownRow, "—") {
		t.Errorf("fully unknown group must say unknown for in/out/cost and (none) agent: %q", unknownRow)
	}
	if _, err = runUsage(t, server, "table", "summary", "--by", "colour"); err == nil {
		t.Fatal("unknown group field accepted")
	}
	jsonOut, err := runUsage(t, server, "json", "summary")
	if err != nil || !strings.Contains(jsonOut, `"truncated": true`) {
		t.Fatal(jsonOut, err)
	}
}

func TestUsagePricesLifecycle(t *testing.T) {
	spec := `{"id":"p1","model_id":"m","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":3,"denominator":1000}}}`
	var puts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/usage/prices/":
			w.Write([]byte(`[{"id":"p1","revision":4,"spec":` + spec + `}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/usage/prices/p1":
			w.Write([]byte(`{"id":"p1","revision":4,"spec":` + spec + `}`))
		case r.Method == http.MethodPut:
			puts = append(puts, r.Header.Get("If-Match"))
			if r.Header.Get("If-Match") != "4" && r.Header.Get("If-Match") != "0" {
				w.WriteHeader(409)
				w.Write([]byte(`{"code":"REVISION_CONFLICT","message":"resource revision changed"}`))
				return
			}
			if r.Header.Get("If-Match") == "0" {
				w.WriteHeader(409)
				w.Write([]byte(`{"code":"REVISION_CONFLICT","message":"exists"}`))
				return
			}
			w.Write([]byte(`{"id":"p1","revision":5,"spec":` + spec + `}`))
		case r.Method == http.MethodDelete:
			if r.Header.Get("If-Match") != "4" {
				t.Errorf("delete revision %q", r.Header.Get("If-Match"))
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	defer server.Close()
	out, err := runUsage(t, server, "table", "prices", "list")
	if err != nil || !strings.Contains(out, "input_tokens=3/1000") || !strings.Contains(out, "USD") || !strings.Contains(out, "4") {
		t.Fatal(out, err)
	}
	out, err = runUsage(t, server, "json", "prices", "get", "p1")
	if err != nil || !strings.Contains(out, `"revision": 4`) {
		t.Fatal(out, err)
	}
	file := filepath.Join(t.TempDir(), "price.json")
	if err = os.WriteFile(file, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err = runUsage(t, server, "json", "prices", "set", "-f", file, "--revision", "4"); err != nil || !strings.Contains(out, `"revision": 5`) {
		t.Fatal(out, err)
	}
	// Creating when the price exists, or replacing with a stale revision, is a clear conflict error.
	_, err = runUsage(t, server, "json", "prices", "set", "-f", file)
	if err == nil || !strings.Contains(err.Error(), "prices get p1") {
		t.Fatal("conflict not explained", err)
	}
	if _, err = runUsage(t, server, "json", "prices", "set", "-f", file, "--revision", "3"); err == nil {
		t.Fatal("stale revision accepted")
	}
	if out, err = runUsage(t, server, "table", "prices", "delete", "p1"); err != nil || !strings.Contains(out, "revision 4") {
		t.Fatal(out, err)
	}
	if err = os.WriteFile(file, []byte(`{"id":"p1","bogus":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = runUsage(t, server, "json", "prices", "set", "-f", file); err == nil {
		t.Fatal("unknown price field accepted")
	}
}

func TestUsagePricesEmptyExplainsUnknownCost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) }))
	defer server.Close()
	out, err := runUsage(t, server, "table", "prices", "list")
	if err != nil || !strings.Contains(out, "Cost is unknown until a price is set") {
		t.Fatal(out, err)
	}
}

func TestUsageForbiddenIsSurfaced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"code":"FORBIDDEN","message":"insufficient role"}`))
	}))
	defer server.Close()
	if _, err := runUsage(t, server, "table", "prices", "set", "-f", "/nonexistent"); err == nil {
		t.Fatal("missing file accepted")
	}
	_, err := runUsage(t, server, "table", "prices", "delete", "p1")
	if err == nil || !strings.Contains(err.Error(), "FORBIDDEN") {
		t.Fatal(err)
	}
}
