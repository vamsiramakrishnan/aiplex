package main

import (
	"strings"
	"testing"
)

const budgetRecord = `{"tenant_id":"t","kind":"budget","id":"platform","revision":2,"spec":{"id":"platform","display_name":"Platform team","limit":{"unit":"cost","currency":"USD","amount":"250.00"},"window":{"kind":"calendar","unit":"month","time_zone":"UTC"},"members":{"groups":[{"source":"source-x","id":"platform-eng"}]}}}`

const budgetStatusJSON = `{"id":"platform","display_name":"Platform team","unit":"cost","currency":"USD","limit":"250.000000","spent":"12.500000","reserved":"1.000000","remaining":"236.500000","window":{"start":"2026-11-01T00:00:00Z","end":"2026-12-01T00:00:00Z"},"unknown_cost_requests":3,"on_unknown_cost":"allow_and_flag","threshold_events":[{"percent":50,"at":"2026-11-05T10:00:00Z","consumed":"125.000000"}],"children":[{"id":"chat","limit":"10","spent":"1","reserved":"0","remaining":"9","unit":"cost"}]}`

func TestBudgetsListGetStatusAndDelete(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/budgets":                 "[" + budgetRecord + "]",
		"GET /api/v1/budgets/platform":        budgetRecord,
		"GET /api/v1/budgets/platform/status": budgetStatusJSON,
		"DELETE /api/v1/budgets/platform":     "204",
	})
	out, err := runSP(t, "", budgetsCmd(), "list")
	if err != nil || !strings.Contains(out, "250.00 USD") || !strings.Contains(out, "month (UTC)") || !strings.Contains(out, "Platform team") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	out, err = runSP(t, "", budgetsCmd(), "get", "platform")
	if err != nil || !strings.Contains(out, "Unknown cost: deny") || !strings.Contains(out, "group platform-eng") || !strings.Contains(out, "remaining 236.500000") || !strings.Contains(out, "Alerts:   50, 80, 100 %") {
		t.Fatalf("get: %v\n%s", err, out)
	}
	if strings.Contains(out, "Counts:") {
		t.Fatalf("a budget that does not count tool calls prints no Counts line:\n%s", out)
	}
	out, err = runSP(t, "", budgetsCmd(), "status", "platform")
	if err == nil && strings.Contains(out, "Counts:") {
		t.Fatalf("status of a budget that does not count tool calls prints no Counts line:\n%s", out)
	}
	if err != nil || !strings.Contains(out, "Unknown cost: 3 request(s) (on_unknown_cost allow_and_flag)") || !strings.Contains(out, "THRESHOLD") || !strings.Contains(out, "Child budgets") {
		t.Fatalf("status: %v\n%s", err, out)
	}
	output = "json"
	out, err = runSP(t, "", budgetsCmd(), "status", "platform")
	output = "table"
	if err != nil || !strings.Contains(out, `"unknown_cost_requests": 3`) {
		t.Fatalf("json passthrough: %v\n%s", err, out)
	}
	// No terminal and no --yes: refuse before deleting anything.
	calls = nil
	if _, err = runSP(t, "", budgetsCmd(), "delete", "platform"); err == nil {
		t.Fatal("delete without --yes ran")
	}
	for _, c := range calls {
		if c.method == "DELETE" {
			t.Fatalf("delete was sent: %+v", c)
		}
	}
	calls = nil
	if out, err = runSP(t, "", budgetsCmd(), "delete", "platform", "--yes"); err != nil || !strings.Contains(out, "Deleted budget platform") {
		t.Fatalf("delete: %v %s", err, out)
	}
	if last := calls[len(calls)-1]; last.method != "DELETE" || last.ifMatch != "2" {
		t.Fatalf("delete must carry the revision it read: %+v", calls)
	}
}

func TestBudgetsCreateBuildsTheSpecAndRefusesWithoutUpdate(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"PUT /api/v1/budgets/ci":       `{"tenant_id":"t","kind":"budget","id":"ci","revision":1,"spec":{"id":"ci"}}`,
		"GET /api/v1/budgets/platform": budgetRecord,
	})
	out, err := runSP(t, "", budgetsCmd(), "create", "ci", "--unit", "requests", "--amount", "5000", "--window", "6h", "--agent", "spiffe://x/ci", "--access-point", "ide", "--project", "apollo", "--threshold", "90")
	if err != nil || !strings.Contains(out, "Created budget ci") {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var put spCall
	for _, c := range calls {
		if c.method == "PUT" {
			put = c
		}
	}
	for _, want := range []string{`"unit":"requests"`, `"amount":"5000"`, `"kind":"tumbling"`, `"hours":6`, `"agents":["spiffe://x/ci"]`, `"access_points":["ide"]`, `"projects":["apollo"]`, `"thresholds":[90]`} {
		if !strings.Contains(put.body, want) {
			t.Errorf("PUT body lacks %s: %s", want, put.body)
		}
	}
	if put.ifMatch != "0" {
		t.Fatalf("a new budget is created with revision 0: %q", put.ifMatch)
	}
	_, err = runSP(t, "", budgetsCmd(), "create", "platform", "--unit", "requests", "--amount", "1", "--window", "day")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing budget needs --update: %v", err)
	}
	if _, err = runSP(t, "", budgetsCmd(), "create", "x", "--unit", "requests", "--amount", "1", "--window", "rolling24h"); err == nil {
		t.Fatal("a rolling window was accepted")
	}
	if _, err = runSP(t, "", budgetsCmd(), "create", "x"); err == nil || !strings.Contains(err.Error(), "--unit") {
		t.Fatalf("missing flags: %v", err)
	}
}

func TestBudgetsPreviewRendersDrawsAndWhy(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"POST /api/v1/budgets/preview": `{"hypothetical":true,"plane":"llmplex","access_point":"ide","draws":[{"id":"team","direct":true,"reasons":["group:platform-eng"],"unit":"requests","limit":"20","window":{"kind":"calendar","unit":"month","time_zone":"UTC"}},{"id":"org","direct":false,"via_child":"team","unit":"requests","limit":"50","window":{"kind":"calendar","unit":"month","time_zone":"UTC"}}],"skipped":[{"id":"spend","reason":"tokens budgets count model traffic only"}],"notes":["The subject is hypothetical"]}`,
		"GET /api/v1/iam/whoami":       `{"identity":{"provider":"https://idp.example","pool_id":"people"}}`,
	})
	out, err := runSP(t, "", budgetsCmd(), "preview", "--group", "platform-eng", "--access-point", "ide")
	if err != nil || !strings.Contains(out, "group:platform-eng") || !strings.Contains(out, "via team") || !strings.Contains(out, "Skipped spend") || !strings.Contains(out, "hypothetical") {
		t.Fatalf("%v\n%s", err, out)
	}
	var post spCall
	for _, c := range calls {
		if c.method == "POST" {
			post = c
		}
	}
	if !strings.Contains(post.body, `"id":"platform-eng"`) || !strings.Contains(post.body, `"source":"source-`) {
		t.Fatalf("groups must be qualified with the signed-in identity provider: %s", post.body)
	}
}

func TestBudgetsIncludeToolCallsIsOptInOnCreateAndUpdate(t *testing.T) {
	var calls []spCall
	opted := strings.Replace(budgetRecord, `"members"`, `"include_tool_calls":true,"members"`, 1)
	spServer(t, &calls, map[string]string{
		"PUT /api/v1/budgets/spend":           `{"tenant_id":"t","kind":"budget","id":"spend","revision":1,"spec":{"id":"spend"}}`,
		"PUT /api/v1/budgets/platform":        opted,
		"GET /api/v1/budgets/platform":        opted,
		"GET /api/v1/budgets/platform/status": strings.Replace(budgetStatusJSON, `"unit":"cost"`, `"unit":"cost","include_tool_calls":true`, 1),
	})
	lastPut := func() string {
		for i := len(calls) - 1; i >= 0; i-- {
			if calls[i].method == "PUT" {
				return calls[i].body
			}
		}
		return ""
	}
	if _, err := runSP(t, "", budgetsCmd(), "create", "spend", "--unit", "cost", "--currency", "USD", "--amount", "5.00", "--window", "month"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(lastPut(), "include_tool_calls") {
		t.Fatalf("a budget created without the flag must not opt in: %s", lastPut())
	}
	if _, err := runSP(t, "", budgetsCmd(), "create", "spend", "--unit", "cost", "--currency", "USD", "--amount", "5.00", "--window", "month", "--include-tool-calls"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastPut(), `"include_tool_calls":true`) {
		t.Fatalf("create --include-tool-calls: %s", lastPut())
	}
	// update keeps the saved value unless the flag is given.
	if _, err := runSP(t, "", budgetsCmd(), "update", "platform", "--amount", "300.00"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastPut(), `"include_tool_calls":true`) {
		t.Fatalf("update dropped the opt-in: %s", lastPut())
	}
	if _, err := runSP(t, "", budgetsCmd(), "update", "platform", "--include-tool-calls=false"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(lastPut(), "include_tool_calls") {
		t.Fatalf("update --include-tool-calls=false did not turn it off: %s", lastPut())
	}
	out, err := runSP(t, "", budgetsCmd(), "get", "platform")
	if err != nil || !strings.Contains(out, "Counts:   model and tool calls") {
		t.Fatalf("get: %v\n%s", err, out)
	}
	out, err = runSP(t, "", budgetsCmd(), "status", "platform")
	if err != nil || !strings.Contains(out, "Counts:    model and tool calls") {
		t.Fatalf("status: %v\n%s", err, out)
	}
}
