package aiplex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func TestListOverviewAccessPoints(t *testing.T) {
	var calls []seenCall
	c := recordingServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_points":[{"id":"ide","display_name":"IDE","caller_kinds":["user"],"qos_class":"gold","counts":{"models":2,"tools":1,"skills":0},"quota_coverage":{"state":"covered","detail":"d"}}],"next_cursor":"abc","skipped_invalid":1}`))
	})
	page, err := c.ListOverviewAccessPoints(context.Background(), 25, "cur")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(calls[0].query)
	if calls[0].path != "/api/v1/overview/access-points" || q.Get("limit") != "25" || q.Get("cursor") != "cur" {
		t.Fatalf("%+v", calls[0])
	}
	row := page.AccessPoints[0]
	if row.ID != "ide" || row.QoSClass != "gold" || row.Counts.Models != 2 || row.Counts.Tools != 1 || row.QuotaCoverage.State != "covered" || page.NextCursor != "abc" || page.SkippedInvalid != 1 {
		t.Fatalf("%+v", page)
	}
	if _, err = c.ListOverviewAccessPoints(context.Background(), -1, ""); err == nil {
		t.Fatal("negative limit must fail")
	}
	if _, err = c.ListOverviewAccessPoints(context.Background(), 0, ""); err != nil || calls[len(calls)-1].query != "" {
		t.Fatalf("no params: %+v %v", calls[len(calls)-1], err)
	}
}

func TestConnectAndUsageDecodeNewFields(t *testing.T) {
	var calls []seenCall
	c := recordingServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_point_id":"ide","client":"generic","base_urls":{},"endpoints":[{"protocol":"openai-realtime","transport":"websocket","url":"wss://g/ap/ide/v1/realtime","label":"OpenAI Realtime","query":["model"],"models":["voice"]}],"models":[],"auth":{"scheme":"bearer","header":"h","token_env":"AIPLEX_TOKEN","caller_kinds":[],"steps":[]},"snippets":[],"notes":[]}`))
	})
	out, err := c.ConnectAccessPoint(context.Background(), "ide", "")
	if err != nil || len(out.Endpoints) != 1 || out.Endpoints[0].Transport != "websocket" || out.Endpoints[0].Query[0] != "model" || out.Endpoints[0].Models[0] != "voice" {
		t.Fatalf("%+v %v", out, err)
	}
	var u OverviewUsage
	if err := json.Unmarshal([]byte(`{"requests":1,"records":4,"bounded_usage_records":3,"lower_bound_records":1,"ambiguous_usage_records":1,"partial_usage_records":1}`), &u); err != nil || u.BoundedUsageRecords != 3 || u.PartialUsageRecords != 1 {
		t.Fatalf("%+v %v", u, err)
	}
	var g UsageSummaryGroup
	var r UsageLedgerRecord
	if err := json.Unmarshal([]byte(`{"bounded_usage_records":2,"lower_bound_records":2}`), &g); err != nil || g.LowerBoundRecords != 2 {
		t.Fatalf("%+v %v", g, err)
	}
	if err := json.Unmarshal([]byte(`{"session":true,"usage_bound":"ambiguous"}`), &r); err != nil || !r.Session || r.UsageBound != usageledger.UsageAmbiguous {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestUsageFilterAccessPointAndCallerKind(t *testing.T) {
	var calls []seenCall
	c := recordingServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"records":[],"next_cursor":""}`))
	})
	if _, err := c.QueryUsageRecords(context.Background(), UsageRecordsQuery{UsageFilter: UsageFilter{AccessPointID: "ide", CallerKind: "agent"}}); err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(calls[0].query)
	if q.Get("access_point_id") != "ide" || q.Get("caller_kind") != "agent" {
		t.Fatalf("%v", calls[0].query)
	}
	got, err := NormalizeUsageGroupBy([]string{"access-point", "Caller-Kind"})
	if err != nil || strings.Join(got, ",") != "access_point_id,caller_kind" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err = NormalizeUsageGroupBy([]string{"access-point", "access_point_id"}); err == nil {
		t.Fatal("alias and wire name are the same field")
	}
}

func TestUsageFilterBatchErrorClassAndBound(t *testing.T) {
	var calls []seenCall
	c := recordingServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"records":[],"next_cursor":""}`))
	})
	no := false
	if _, err := c.QueryUsageRecords(context.Background(), UsageRecordsQuery{UsageFilter: UsageFilter{Batch: &no, ErrorClass: "timeout", UsageBound: "partial"}}); err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(calls[0].query)
	if q.Get("batch") != "false" || q.Get("error_class") != "timeout" || q.Get("usage_bound") != "partial" {
		t.Fatalf("%v", calls[0].query)
	}
	got, err := NormalizeUsageGroupBy([]string{"error-class", "bound", "source", "batch"})
	if err != nil || strings.Join(got, ",") != "error_class,usage_bound,source,batch" {
		t.Fatalf("%v %v", got, err)
	}
}
