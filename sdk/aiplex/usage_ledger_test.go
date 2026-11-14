package aiplex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestFormatMoneyIsExact(t *testing.T) {
	for _, c := range []struct {
		amount string
		scale  uint8
		want   string
	}{
		{"1135", 6, "0.001135"},
		{"0", 6, "0.000000"},
		{"0", 0, "0"},
		{"5", 0, "5"},
		{"100", 2, "1.00"},
		{"123456789012345678901234567890", 6, "123456789012345678901234.567890"},
		{"007", 3, "0.007"},
		{"-1135", 6, "-0.001135"},
		{"-0", 2, "0.00"},
		{"9007199254740993", 0, "9007199254740993"},
	} {
		got, err := aiplex.FormatMoney(c.amount, c.scale)
		if err != nil || got != c.want {
			t.Errorf("FormatMoney(%q,%d)=%q,%v want %q", c.amount, c.scale, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "-", "1.5", "1e3", "abc", " 1"} {
		if got, err := aiplex.FormatMoney(bad, 2); err == nil {
			t.Errorf("FormatMoney(%q) accepted as %q", bad, got)
		}
	}
}

func TestFormatCostNeverRendersUnknownAsZero(t *testing.T) {
	if got := aiplex.FormatCost(usageledger.UnknownCost()); got != "unknown" {
		t.Fatal(got)
	}
	est := usageledger.Cost{Basis: usageledger.BasisEstimated, Currency: "USD", Amount: "1135", Scale: 6}
	if got := aiplex.FormatCost(est); got != "USD 0.001135 (estimated)" {
		t.Fatal(got)
	}
	total := usageledger.CostTotal{Currency: "EUR", Basis: "settled", Amount: "250", Scale: 2, Records: 2}
	if got := aiplex.FormatCostTotal(total); got != "EUR 2.50 (settled)" {
		t.Fatal(got)
	}
}

func TestNormalizeUsageGroupBy(t *testing.T) {
	got, err := aiplex.NormalizeUsageGroupBy([]string{"model", " agent", "user", "client", "tool", "plane"})
	if err != nil || strings.Join(got, ",") != "model_id,actor_id,principal_id,client_id,tool_name,plane" {
		t.Fatal(got, err)
	}
	if _, err = aiplex.NormalizeUsageGroupBy([]string{"model", "model_id"}); err == nil {
		t.Fatal("repeated field accepted")
	}
	if _, err = aiplex.NormalizeUsageGroupBy([]string{"colour"}); err == nil {
		t.Fatal("unknown field accepted")
	}
}

const recordsPage1 = `{"records":[
 {"id":"ul_1","tenant_id":"t","started_at":"2026-11-09T10:00:00Z","finished_at":"2026-11-09T10:00:01Z","latency_ms":1000,"plane":"llmplex","model_id":"m","streaming":false,"status":"ok","source":"managed","requests":1,
  "usage":{"input_tokens":0,"output_tokens":null,"cache_read_tokens":null,"cache_write_tokens":null,"reasoning_tokens":null,"total_tokens":null},
  "cost":{"basis":"estimated","currency":"USD","amount":"1135","scale":6}},
 {"id":"ul_2","tenant_id":"t","started_at":"2026-11-09T09:00:00Z","finished_at":"2026-11-09T09:00:01Z","latency_ms":5,"plane":"mcplex","tool_name":"search","streaming":false,"status":"denied","source":"managed","requests":1,
  "usage":null,"cost":{"basis":"unknown"}}],
 "next_cursor":"c2"}`

func TestQueryRecordsDecodesNullVersusZero(t *testing.T) {
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/usage/records" || r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		query = r.URL.Query()
		w.Write([]byte(recordsPage1))
	}))
	defer server.Close()
	since := time.Date(2026, 11, 8, 0, 0, 0, 0, time.FixedZone("x", 3600))
	page, err := aiplex.NewClient(server.URL).QueryUsageRecords(context.Background(), aiplex.UsageRecordsQuery{
		UsageFilter: aiplex.UsageFilter{Plane: "llmplex", ModelID: "m", ActorID: "agent", Since: since}, Limit: 2, Cursor: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"plane": "llmplex", "model_id": "m", "actor_id": "agent", "limit": "2", "cursor": "c1", "since": "2026-11-07T23:00:00Z"} {
		if query.Get(k) != v {
			t.Errorf("query %s=%q want %q", k, query.Get(k), v)
		}
	}
	if page.NextCursor != "c2" || len(page.Records) != 2 {
		t.Fatal(page)
	}
	first, second := page.Records[0], page.Records[1]
	if first.Usage == nil || first.Usage.InputTokens == nil || *first.Usage.InputTokens != 0 {
		t.Fatal("reported zero input must stay a known zero")
	}
	if first.Usage.OutputTokens != nil {
		t.Fatal("null output counter decoded as a value")
	}
	if second.Usage != nil {
		t.Fatal("null usage decoded as an object")
	}
	if second.Cost.Basis != usageledger.BasisUnknown || second.Cost.Amount != "" {
		t.Fatal(second.Cost)
	}
	if first.Cost.Amount != "1135" || first.Cost.Scale != 6 {
		t.Fatal(first.Cost)
	}
}

func TestQueryRecordsRejectsBadLimitBeforeRequest(t *testing.T) {
	client := aiplex.NewClient("http://127.0.0.1:1")
	for _, limit := range []int{-1, 1001} {
		if _, err := client.QueryUsageRecords(context.Background(), aiplex.UsageRecordsQuery{Limit: limit}); err == nil {
			t.Errorf("limit %d accepted", limit)
		}
	}
}

func TestPagerWalksAllPagesAndStops(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		switch r.URL.Query().Get("cursor") {
		case "":
			w.Write([]byte(`{"records":[{"id":"a","usage":null,"cost":{"basis":"unknown"}},{"id":"b","usage":null,"cost":{"basis":"unknown"}}],"next_cursor":"p2"}`))
		case "p2":
			w.Write([]byte(`{"records":[{"id":"c","usage":null,"cost":{"basis":"unknown"}}]}`))
		default:
			t.Error("fetched past the last page")
		}
	}))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	pager := client.PageUsageRecords(aiplex.UsageRecordsQuery{Limit: 2})
	var ids []string
	for pager.Next(context.Background()) {
		ids = append(ids, pager.Record().ID)
	}
	if pager.Err() != nil || strings.Join(ids, ",") != "a,b,c" || pager.Pages != 2 || pager.Cursor() != "" {
		t.Fatal(ids, pager.Err(), pager.Pages)
	}
	if pager.Next(context.Background()) {
		t.Fatal("exhausted pager advanced")
	}
	if len(cursors) != 2 {
		t.Fatal(cursors)
	}

	records, truncated, err := client.CollectUsageRecords(context.Background(), aiplex.UsageRecordsQuery{}, 2)
	if err != nil || !truncated || len(records) != 2 {
		t.Fatal(records, truncated, err)
	}
	records, truncated, err = client.CollectUsageRecords(context.Background(), aiplex.UsageRecordsQuery{}, 3)
	if err != nil || truncated || len(records) != 3 {
		t.Fatal(records, truncated, err)
	}
}

func TestPagerReportsErrorAndStuckCursor(t *testing.T) {
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"records":[{"id":"x","usage":null,"cost":{"basis":"unknown"}}],"next_cursor":"same"}`))
	}))
	defer stuck.Close()
	pager := aiplex.NewClient(stuck.URL).PageUsageRecords(aiplex.UsageRecordsQuery{Cursor: "same"})
	for pager.Next(context.Background()) {
	}
	if pager.Err() == nil {
		t.Fatal("non-advancing cursor not detected")
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"code":"INVALID_QUERY","message":"bad cursor"}`))
	}))
	defer failing.Close()
	pager = aiplex.NewClient(failing.URL).PageUsageRecords(aiplex.UsageRecordsQuery{})
	if pager.Next(context.Background()) || pager.Err() == nil || !strings.Contains(pager.Err().Error(), "INVALID_QUERY") {
		t.Fatal(pager.Err())
	}
}

func TestGetRecordEscapesID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v1/usage/records/ul_1" {
			t.Errorf("path %s", r.URL.EscapedPath())
		}
		w.Write([]byte(`{"id":"ul_1","usage":null,"cost":{"basis":"unknown"}}`))
	}))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	record, err := client.GetUsageLedgerRecord(context.Background(), "ul_1")
	if err != nil || record.ID != "ul_1" || record.Usage != nil {
		t.Fatal(record, err)
	}
	if _, err = client.GetUsageLedgerRecord(context.Background(), "../x"); err == nil {
		t.Fatal("traversal id accepted")
	}
	if _, err = client.GetUsageLedgerRecord(context.Background(), ""); err == nil {
		t.Fatal("empty id accepted")
	}
}

func TestSummarizeUsageSendsNormalizedGroupingAndKeepsUnknowns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/usage/summary" || q.Get("group_by") != "model_id,actor_id" || q.Get("bucket") != "day" || q.Get("status") != "error" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"group_by":["model_id","actor_id"],"bucket":"day","records_scanned":3,"truncated":true,"groups":[
		 {"key":{"model_id":"m","actor_id":"a"},"bucket_start":"2026-11-09T00:00:00Z","records":3,"requests":3,"errors":1,"denied":0,"unknown_status":0,
		  "tokens":{"input_tokens":10},"tokens_unknown":{"input_tokens":1,"output_tokens":3},"unknown_usage_records":1,
		  "cost":[{"currency":"USD","basis":"estimated","amount":"1135","scale":6,"records":2}],"unknown_cost_records":1}]}`))
	}))
	defer server.Close()
	summary, err := aiplex.NewClient(server.URL).SummarizeUsage(context.Background(), aiplex.UsageSummaryQuery{
		UsageFilter: aiplex.UsageFilter{Status: "error"}, GroupBy: []string{"model", "agent"}, Bucket: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Truncated || summary.RecordsScanned != 3 || len(summary.Groups) != 1 {
		t.Fatal(summary)
	}
	g := summary.Groups[0]
	if _, known := g.Tokens["output_tokens"]; known || g.TokensUnknown["output_tokens"] != 3 || g.Tokens["input_tokens"] != 10 {
		t.Fatal("unreported output must be absent, not zero", g.Tokens, g.TokensUnknown)
	}
	if g.UnknownUsageRecords != 1 || g.UnknownCostRecords != 1 || aiplex.FormatCostTotal(g.Cost[0]) != "USD 0.001135 (estimated)" {
		t.Fatal(g)
	}
	if _, err = aiplex.NewClient(server.URL).SummarizeUsage(context.Background(), aiplex.UsageSummaryQuery{Bucket: "week"}); err == nil {
		t.Fatal("bad bucket accepted")
	}
	if _, err = aiplex.NewClient(server.URL).SummarizeUsage(context.Background(), aiplex.UsageSummaryQuery{GroupBy: []string{"nope"}}); err == nil {
		t.Fatal("bad group accepted")
	}
}

func TestModelPriceLifecycleAndRevisionConflict(t *testing.T) {
	var seenIfMatch []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spec := `{"id":"p1","model_id":"m","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":3,"denominator":1000}}}`
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/usage/prices/":
			w.Write([]byte(`[{"tenant_id":"t","kind":"model-price","id":"p1","revision":4,"spec":` + spec + `}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/usage/prices/p1":
			w.Write([]byte(`{"id":"p1","revision":4,"spec":` + spec + `}`))
		case r.Method == http.MethodPut:
			seenIfMatch = append(seenIfMatch, r.Header.Get("If-Match"))
			var body aiplex.ModelPrice
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "p1" || body.Meters["input_tokens"].Denominator != 1000 {
				t.Error("price body not sent as bare spec")
			}
			if r.Header.Get("If-Match") == "3" {
				w.WriteHeader(409)
				w.Write([]byte(`{"code":"REVISION_CONFLICT","message":"resource revision changed"}`))
				return
			}
			w.Write([]byte(`{"id":"p1","revision":5,"spec":` + spec + `}`))
		case r.Method == http.MethodDelete:
			seenIfMatch = append(seenIfMatch, r.Header.Get("If-Match"))
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	ctx := context.Background()
	list, err := client.ListModelPrices(ctx)
	if err != nil || len(list) != 1 || list[0].Revision != 4 || list[0].Price.Meters["input_tokens"].Numerator != 3 {
		t.Fatal(list, err)
	}
	got, err := client.GetModelPrice(ctx, "p1")
	if err != nil || got.Revision != 4 || got.Price.Currency != "USD" || got.Price.Scale != 6 {
		t.Fatal(got, err)
	}
	updated, err := client.PutModelPrice(ctx, got.Price, got.Revision)
	if err != nil || updated.Revision != 5 {
		t.Fatal(updated, err)
	}
	if _, err = client.PutModelPrice(ctx, got.Price, 3); !aiplex.IsRevisionConflict(err) {
		t.Fatal("stale revision not reported as conflict", err)
	}
	if err = client.DeleteModelPrice(ctx, "p1", 5); err != nil {
		t.Fatal(err)
	}
	if err = client.DeleteModelPrice(ctx, "p1", 0); err == nil {
		t.Fatal("delete without revision accepted")
	}
	if strings.Join(seenIfMatch, ",") != "4,3,5" {
		t.Fatal(seenIfMatch)
	}
	if _, err = client.PutModelPrice(ctx, aiplex.ModelPrice{ID: "p1"}, 0); err == nil {
		t.Fatal("invalid price sent to server")
	}
}

func TestSummarizeUsageSendsTheCorrelationFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/usage/summary" || q.Get("client_request_id") != "by-1" || q.Get("request_id") != "req-1" || q.Get("trace_id") != "0af7651916cd43dd8448eb211c80319c" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"groups":[],"records_scanned":0}`))
	}))
	defer server.Close()
	_, err := aiplex.NewClient(server.URL).SummarizeUsage(context.Background(), aiplex.UsageSummaryQuery{
		UsageFilter: aiplex.UsageFilter{ClientRequestID: "by-1", RequestID: "req-1", TraceID: "0af7651916cd43dd8448eb211c80319c"}})
	if err != nil {
		t.Fatal(err)
	}
}
