package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// codeOf is the exit code main would return for err.
func codeOf(cmd *cobra.Command, err error) int {
	if err == nil {
		return 0
	}
	return explain(cmd, nil, err).code
}

func noRequests(t *testing.T) *[]spCall {
	t.Helper()
	var calls []spCall
	spServer(t, &calls, map[string]string{})
	return &calls
}

const a2aDelegationJSON = `{"id":"d-1","caller_agent_id":"tutor","callee_agent_id":"research","task_type":"research","status":"running","user_id":"","started_at":"2026-11-01T10:00:00Z","reported_by":"collector"}`

func TestA2AInvokeSendsTheCallAndStreamsTheReply(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var gotBody, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody, gotPath = string(b), r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "req-9")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	defer srv.Close()
	oldURL, oldToken, oldOut := apiURL, token, output
	apiURL, token, output = srv.URL, "t", "table"
	defer func() { apiURL, token, output = oldURL, oldToken, oldOut }()

	pr, pw := io.Pipe()
	cmd := a2aCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetOut(pw)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"invoke", "bedrock", "--operation", "invoke", "--session", "s-1", "--payload", `{"input": "hello"}`})
	done := make(chan error, 1)
	go func() { err := cmd.Execute(); pw.Close(); done <- err }()

	first := make([]byte, len("data: first\n\n"))
	got := make(chan string, 1)
	go func() { _, _ = io.ReadFull(pr, first); got <- string(first) }()
	select {
	case s := <-got:
		if s != "data: first\n\n" {
			t.Fatalf("first chunk: %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first event was not printed while the reply was still open")
	}
	close(release)
	rest, _ := io.ReadAll(pr)
	if err := <-done; err != nil || string(rest) != "data: second\n\n" {
		t.Fatalf("%v %q", err, rest)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/api/v1/a2a/connections/bedrock/invoke" {
		t.Fatal(gotPath)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil || sent["operation"] != "invoke" || sent["session_id"] != "s-1" {
		t.Fatalf("%v %s", err, gotBody)
	}
	if p, _ := sent["payload"].(map[string]any); p["input"] != "hello" {
		t.Fatalf("payload not sent as given: %s", gotBody)
	}
}

func TestA2AInvokeJSONOutputIsAnEnvelopeAroundTheRawReply(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"POST /api/v1/a2a/connections/c1/invoke": `{"answer":42}`})
	output = "json"
	out, err := runSP(t, "", a2aCmd(), "invoke", "c1", "--operation", "invoke")
	if err != nil {
		t.Fatal(err, out)
	}
	var env struct {
		Status      int             `json:"status"`
		ContentType string          `json:"content_type"`
		Body        json.RawMessage `json:"body"`
	}
	if json.Unmarshal([]byte(out), &env) != nil || env.Status != 200 || !strings.Contains(string(env.Body), `"answer"`) {
		t.Fatalf("%s", out)
	}
	if strings.Contains(calls[0].body, "payload") {
		t.Fatalf("no payload was given, none may be sent: %s", calls[0].body)
	}
}

func TestA2AInvokePayloadFileAndStdin(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"POST /api/v1/a2a/connections/c1/invoke": `{}`})
	if _, err := runSP(t, `{"from":"stdin"}`, a2aCmd(), "invoke", "c1", "--operation", "invoke", "--payload-file", "-"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls[0].body, `"from":"stdin"`) {
		t.Fatal(calls[0].body)
	}
	f := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(f, []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runSP(t, "", a2aCmd(), "invoke", "c1", "--operation", "invoke", "--payload-file", f); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls[1].body, `"from":"file"`) {
		t.Fatal(calls[1].body)
	}
}

func TestA2AInvokeRefusesBadInputWithExitCode2AndSendsNothing(t *testing.T) {
	calls := noRequests(t)
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.json")
	_ = os.WriteFile(empty, nil, 0o600)
	secret := "sk-live-SECRET-VALUE"
	cases := map[string][]string{
		"no operation":             {"invoke", "c1"},
		"no connection":            {"invoke", "--operation", "invoke"},
		"payload and file":         {"invoke", "c1", "--operation", "invoke", "--payload", "{}", "--payload-file", "-"},
		"payload not JSON":         {"invoke", "c1", "--operation", "invoke", "--payload", `{"k":"` + secret},
		"empty payload":            {"invoke", "c1", "--operation", "invoke", "--payload", ""},
		"empty payload file":       {"invoke", "c1", "--operation", "invoke", "--payload-file", empty},
		"missing payload file":     {"invoke", "c1", "--operation", "invoke", "--payload-file", filepath.Join(dir, "nope.json")},
		"padded operation":         {"invoke", "c1", "--operation", " invoke"},
		"traversal connection ID":  {"invoke", "..", "--operation", "invoke"},
		"slash in connection ID":   {"invoke", "a/b", "--operation", "invoke"},
		"unknown flag":             {"invoke", "c1", "--operation", "invoke", "--nope"},
		"oversized payload (1MiB)": {"invoke", "c1", "--operation", "invoke", "--payload", `"` + strings.Repeat("x", 1<<20) + `"`},
	}
	for name, args := range cases {
		cmd := a2aCmd()
		out, err := runSP(t, "", cmd, args...)
		if err == nil || codeOf(cmd, err) != exitUsage {
			t.Errorf("%s: want exit 2, got %v (code %d)", name, err, codeOf(cmd, err))
			continue
		}
		if strings.Contains(out+err.Error(), secret) {
			t.Errorf("%s: the payload leaked into the error: %v", name, err)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("requests were sent: %+v", *calls)
	}
}

func TestA2AInvokeMapsServerRefusals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"code":"ACTION_DENIED","message":"missing a2a:agent:c1:invoke"}`)
	}))
	defer srv.Close()
	old := apiURL
	apiURL = srv.URL
	defer func() { apiURL = old }()
	cmd := a2aCmd()
	_, err := runSP(t, "", cmd, "invoke", "c1", "--operation", "invoke", "--payload", `{"k":"SECRETISH"}`)
	if codeOf(cmd, err) != exitAuth || strings.Contains(err.Error(), "SECRETISH") {
		t.Fatalf("%v", err)
	}
}

func TestA2ADelegationsListFiltersAndStatesWhatItShows(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/a2a/delegations": "[" + a2aDelegationJSON + `,{"id":"d-2","caller_agent_id":"a","callee_agent_id":"b","task_type":"t","status":"completed","started_at":"2026-11-01T10:00:00Z","completed_at":"2026-11-01T10:00:02Z","duration_ms":2000}]`})
	out, err := runSP(t, "", a2aCmd(), "delegations", "list", "--agent", "tutor", "--limit", "2")
	if err != nil {
		t.Fatal(err, out)
	}
	if calls[0].query != "agent_id=tutor&limit=2" {
		t.Fatal(calls[0].query)
	}
	// Running has no duration yet: unknown, not 0. Completed shows its own.
	lines := strings.Split(out, "\n")
	var d1, d2 string
	for _, l := range lines {
		if strings.HasPrefix(l, "d-1") {
			d1 = l
		}
		if strings.HasPrefix(l, "d-2") {
			d2 = l
		}
	}
	if d1 == "" || strings.Contains(d1, "0ms") || !strings.Contains(d1, "unknown") || !strings.Contains(d2, "2000ms") {
		t.Fatalf("\n%s", out)
	}
	if !strings.Contains(out, "reported history") || !strings.Contains(out, "not proof") {
		t.Fatalf("the reported-history caveat is missing:\n%s", out)
	}
	if !strings.Contains(out, "limit") {
		t.Fatalf("a full page must say it may be truncated:\n%s", out)
	}
}

func TestA2ADelegationsListRejectsBadLimitBeforeSending(t *testing.T) {
	calls := noRequests(t)
	for _, limit := range []string{"0", "-3", "1001", "abc"} {
		cmd := a2aCmd()
		_, err := runSP(t, "", cmd, "delegations", "list", "--limit", limit)
		if err == nil || codeOf(cmd, err) != exitUsage {
			t.Errorf("--limit %s: %v (code %d)", limit, err, codeOf(cmd, err))
		}
	}
	cmd := a2aCmd()
	if _, err := runSP(t, "", cmd, "delegations", "list", "--agent", " x"); codeOf(cmd, err) != exitUsage {
		t.Errorf("padded agent: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("%+v", *calls)
	}
}

func TestA2ADelegationsListJSONAndEmpty(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/a2a/delegations": "[]"})
	out, err := runSP(t, "", a2aCmd(), "delegations", "list", "--agent", "ghost")
	if err != nil || !strings.Contains(out, "No delegations for agent ghost") {
		t.Fatal(out, err)
	}
	output = "json"
	out, err = runSP(t, "", a2aCmd(), "delegations", "list")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestA2ADelegationsGetAndChain(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/a2a/delegations/d-1":       a2aDelegationJSON,
		"GET /api/v1/a2a/delegations/d-1/chain": `{"root":` + a2aDelegationJSON + `,"children":[{"id":"d-3","caller_agent_id":"research","callee_agent_id":"search","task_type":"q","status":"failed","started_at":"2026-11-01T10:00:01Z"}],"depth":2,"total_duration_ms":0}`,
	})
	out, err := runSP(t, "", a2aCmd(), "delegations", "get", "d-1")
	if err != nil || !strings.Contains(out, "tutor") || !strings.Contains(out, "User:     -") || !strings.Contains(out, "not proof") {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Duration: unknown") || strings.Contains(out, "0ms") {
		t.Fatalf("an unknown duration must not print as 0:\n%s", out)
	}
	out, err = runSP(t, "", a2aCmd(), "delegations", "chain", "d-1")
	if err != nil || !strings.Contains(out, "tutor -> research") || !strings.Contains(out, "research -> search") || !strings.Contains(out, "not proof") {
		t.Fatalf("%v\n%s", err, out)
	}
	cmd := a2aCmd()
	if _, err = runSP(t, "", cmd, "delegations", "get", "missing"); codeOf(cmd, err) != exitNotFound {
		t.Fatalf("%v", err)
	}
	for _, bad := range []string{"..", "a/b"} {
		for _, verb := range []string{"get", "chain"} {
			cmd = a2aCmd()
			if _, err = runSP(t, "", cmd, "delegations", verb, bad); codeOf(cmd, err) != exitUsage {
				t.Errorf("%s %q: %v", verb, bad, err)
			}
		}
	}
}

func TestA2ADelegationsUpdate(t *testing.T) {
	failedRunning := strings.Replace(a2aDelegationJSON, `"status":"running"`, `"status":"running","error":"timeout upstream"`, 1)
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/a2a/delegations/d-1":   a2aDelegationJSON,
		"PATCH /api/v1/a2a/delegations/d-1": strings.Replace(a2aDelegationJSON, `"running"`, `"completed"`, 1),
		"GET /api/v1/a2a/delegations/d-2":   failedRunning,
		"PATCH /api/v1/a2a/delegations/d-2": failedRunning,
	})
	out, err := runSP(t, "", a2aCmd(), "delegations", "update", "d-1", "--status", "completed")
	if err != nil || !strings.Contains(out, "running -> completed") {
		t.Fatalf("%v\n%s", err, out)
	}
	last := calls[len(calls)-1]
	if last.method != "PATCH" || last.body != `{"status":"completed"}` {
		t.Fatalf("%+v", last)
	}

	// d-2 holds an error text; the PATCH replaces it, so it must be stated.
	n := len(calls)
	cmd := a2aCmd()
	_, err = runSP(t, "", cmd, "delegations", "update", "d-2", "--status", "failed")
	if err == nil || codeOf(cmd, err) != exitUsage || !strings.Contains(err.Error(), "timeout upstream") {
		t.Fatalf("silently dropping the stored error must be refused: %v", err)
	}
	for _, c := range calls[n:] {
		if c.method == "PATCH" {
			t.Fatalf("PATCH sent: %+v", c)
		}
	}
	if _, err = runSP(t, "", a2aCmd(), "delegations", "update", "d-2", "--status", "failed", "--error", "gave up"); err != nil {
		t.Fatal(err)
	}
	if last = calls[len(calls)-1]; last.body != `{"status":"failed","error":"gave up"}` {
		t.Fatalf("%+v", last)
	}
	if _, err = runSP(t, "", a2aCmd(), "delegations", "update", "d-2", "--status", "failed", "--clear-error"); err != nil {
		t.Fatal(err)
	}
	if last = calls[len(calls)-1]; last.body != `{"status":"failed"}` {
		t.Fatalf("%+v", last)
	}

	n = len(calls)
	for name, args := range map[string][]string{
		"no status":       {"delegations", "update", "d-1"},
		"bad status":      {"delegations", "update", "d-1", "--status", "done"},
		"error and clear": {"delegations", "update", "d-1", "--status", "failed", "--error", "x", "--clear-error"},
		"bad id":          {"delegations", "update", "../x", "--status", "failed"},
		"long error":      {"delegations", "update", "d-1", "--status", "failed", "--error", strings.Repeat("x", 8193)},
	} {
		cmd = a2aCmd()
		if _, err = runSP(t, "", cmd, args...); err == nil || codeOf(cmd, err) != exitUsage {
			t.Errorf("%s: %v (code %d)", name, err, codeOf(cmd, err))
		}
	}
	for _, c := range calls[n:] {
		if c.method == "PATCH" {
			t.Fatalf("PATCH sent for refused input: %+v", c)
		}
	}
}

const a2aConnRecord = `{"tenant_id":"t","kind":"agent-connection","id":"bedrock","revision":4,"spec":{"id":"bedrock","provider":"aws-agentcore-runtime","endpoint":"https://bedrock-agentcore.us-east-1.amazonaws.com","resource_id":"arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/r1","identity_ref":"aws-prod","region":"us-east-1","governance_mode":"observe"}}`

func TestA2AConnectionsListGetDelete(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/a2a/connections/":           "[" + a2aConnRecord + `,{"tenant_id":"t","kind":"agent-connection","id":"odd","revision":1,"spec":{}}]`,
		"GET /api/v1/a2a/connections/bedrock":    a2aConnRecord,
		"DELETE /api/v1/a2a/connections/bedrock": "204",
	})
	out, err := runSP(t, "", a2aCmd(), "connections", "list")
	if err != nil || !strings.Contains(out, "bedrock") || !strings.Contains(out, "aws-agentcore-runtime") || !strings.Contains(out, "aws-prod") {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "unknown") {
		t.Fatalf("a connection without a provider must show it as unknown:\n%s", out)
	}
	out, err = runSP(t, "", a2aCmd(), "connections", "get", "bedrock")
	if err != nil || !strings.Contains(out, "Agent connection bedrock (revision 4)") || !strings.Contains(out, "Identity:") || !strings.Contains(out, "aws-prod") {
		t.Fatalf("%v\n%s", err, out)
	}
	output = "json"
	out, err = runSP(t, "", a2aCmd(), "connections", "get", "bedrock")
	var rec map[string]any
	if err != nil || json.Unmarshal([]byte(out), &rec) != nil || rec["revision"] != float64(4) {
		t.Fatalf("%v\n%s", err, out)
	}
	output = "table"

	cmd := a2aCmd()
	if _, err = runSP(t, "", cmd, "connections", "delete", "bedrock"); err == nil || codeOf(cmd, err) != exitUsage {
		t.Fatalf("delete without --yes and without a terminal must be refused with exit 2: %v", err)
	}
	for _, c := range calls {
		if c.method == "DELETE" {
			t.Fatal("deleted without confirmation")
		}
	}
	out, err = runSP(t, "", a2aCmd(), "connections", "delete", "bedrock", "--yes")
	if err != nil || !strings.Contains(out, "Deleted agent connection bedrock") {
		t.Fatalf("%v\n%s", err, out)
	}
	if last := calls[len(calls)-1]; last.method != "DELETE" || last.ifMatch != "4" {
		t.Fatalf("delete must carry the revision it read: %+v", last)
	}
	cmd = a2aCmd()
	if _, err = runSP(t, "", cmd, "connections", "get", "../x"); codeOf(cmd, err) != exitUsage {
		t.Fatalf("%v", err)
	}
}

func TestA2AExistingDelegationCommandsStillWork(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/a2a/delegations":     "[" + a2aDelegationJSON + "]",
		"GET /api/v1/a2a/delegations/d-1": a2aDelegationJSON,
	})
	for _, c := range []*cobra.Command{listCmd(), getCmd()} {
		args := []string{"delegations"}
		if c.Name() == "get" {
			args = []string{"delegation", "d-1"}
		}
		var buf bytes.Buffer
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w
		_, err := runSP(t, "", c, args...)
		_ = w.Close()
		os.Stdout = old
		_, _ = buf.ReadFrom(r)
		if err != nil || !strings.Contains(buf.String(), "tutor") {
			t.Fatalf("%s: %v\n%s", c.Name(), err, buf.String())
		}
	}
}

func chainOut(t *testing.T, body string) string {
	t.Helper()
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/a2a/delegations/c/chain": body})
	out, err := runSP(t, "", a2aCmd(), "delegations", "chain", "c")
	if err != nil {
		t.Fatal(err, out)
	}
	return out
}

func span(id, status string, durationMs int, finished bool) string {
	done := ""
	if finished {
		done = `,"completed_at":"2026-11-01T10:00:09Z"`
	}
	return fmt.Sprintf(`{"id":%q,"caller_agent_id":"a","callee_agent_id":"b","task_type":"t","status":%q,"started_at":"2026-11-01T10:00:00Z","duration_ms":%d%s}`, id, status, durationMs, done)
}

func TestA2AChainSpanTimeSaysWhatItLeavesOut(t *testing.T) {
	// Every span finished: the total, including a real zero.
	out := chainOut(t, `{"root":`+span("r", "completed", 1500, true)+`,"children":[`+span("c", "completed", 0, true)+`],"depth":2,"total_duration_ms":1500}`)
	if !strings.Contains(out, "recorded span time 1500ms (") || strings.Contains(out, "not counted") {
		t.Fatal(out)
	}
	out = chainOut(t, `{"root":`+span("r", "completed", 0, true)+`,"depth":1,"total_duration_ms":0}`)
	if !strings.Contains(out, "recorded span time 0ms (") {
		t.Fatalf("a finished span of 0ms is 0ms:\n%s", out)
	}
	// Some unfinished: the known total and how many are left out. The API's
	// own total is deliberately wrong here to show it is not trusted.
	out = chainOut(t, `{"root":`+span("r", "completed", 1500, true)+`,"children":[`+span("c", "running", 0, false)+`,`+span("d", "pending", 0, false)+`],"depth":2,"total_duration_ms":99999}`)
	if !strings.Contains(out, "1500ms for 1 of 3 spans (2 unfinished or without a duration are not counted") || strings.Contains(out, "99999") {
		t.Fatal(out)
	}
	// Nothing finished: unknown, with the count.
	out = chainOut(t, `{"root":`+span("r", "running", 0, false)+`,"children":[`+span("c", "pending", 0, false)+`],"depth":2,"total_duration_ms":0}`)
	if !strings.Contains(out, "recorded span time unknown (2 of 2 spans unfinished") || strings.Contains(out, "0ms") {
		t.Fatal(out)
	}
}

func TestA2ADelegationsUpdateValidatesFlagsBeforeAnyRequest(t *testing.T) {
	calls := noRequests(t)
	for name, args := range map[string][]string{
		"bad status": {"delegations", "update", "d-1", "--status", "done"},
		"long error": {"delegations", "update", "d-1", "--status", "failed", "--error", strings.Repeat("x", 8193)},
	} {
		cmd := a2aCmd()
		if _, err := runSP(t, "", cmd, args...); err == nil || codeOf(cmd, err) != exitUsage {
			t.Errorf("%s: %v (code %d)", name, err, codeOf(cmd, err))
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("requests were sent: %+v", *calls)
	}
}

func TestA2AInvokeYAMLAndPlainTextReplies(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"POST /api/v1/a2a/connections/c1/invoke": `{"answer":{"text":"forty-two"},"n":42}`, "POST /api/v1/a2a/connections/c2/invoke": "just some text"})
	output = "yaml"
	out, err := runSP(t, "", a2aCmd(), "invoke", "c1", "--operation", "invoke")
	if err != nil || !strings.Contains(out, "status: 200") || !strings.Contains(out, "text: forty-two") || !strings.Contains(out, "\"n\": 42") || strings.Contains(out, "- 123") {
		t.Fatalf("the body must be structured YAML:\n%s %v", out, err)
	}
	out, err = runSP(t, "", a2aCmd(), "invoke", "c2", "--operation", "invoke")
	if err != nil || !strings.Contains(out, "body: just some text") {
		t.Fatalf("plain text must print as a string:\n%s %v", out, err)
	}
	output = "json"
	out, err = runSP(t, "", a2aCmd(), "invoke", "c2", "--operation", "invoke")
	if err != nil || !strings.Contains(out, `"body": "just some text"`) {
		t.Fatalf("%s %v", out, err)
	}
}

// The SDK refuses a malformed ID before sending; commands that were not
// written for A2APlex must exit 2 (invalid input), as docs/cli.md says.
func TestBadIDsInOtherCommandsExitWithUsageCode(t *testing.T) {
	calls := noRequests(t)
	for _, tc := range []struct {
		cmd  func() *cobra.Command
		args []string
	}{
		{accessPointsCmd, []string{"get", "../x"}},
		{budgetsCmd, []string{"get", "a%2Fb"}},
	} {
		cmd := tc.cmd()
		_, err := runSP(t, "", cmd, tc.args...)
		if err == nil || codeOf(cmd, err) != exitUsage {
			t.Errorf("%v: %v (code %d)", tc.args, err, codeOf(cmd, err))
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("requests were sent: %+v", *calls)
	}
}
