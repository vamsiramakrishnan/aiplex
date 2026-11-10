package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

const policyMarker = "planted-policy-arg-31bd"

var allScopes = []string{"mcp:tools:search", "mcp:tools:delete", "mcp:tools:files.read"}

type ruleFixture struct {
	t        *testing.T
	h        *ConnectedHandler
	store    resources.Store
	upstream *toolUpstream
	records  func() []usageledger.Record
	logs     *bytes.Buffer
}

func newRuleFixture(t *testing.T, tools *toolexec.ToolPolicy) *ruleFixture {
	t.Helper()
	upstream := newToolUpstream(t)
	store := resources.NewMemoryStore()
	h, _ := authHandler(t, store, upstream.URL, apiSecrets{})
	cohesionRecord(t, store, "tool-connection", "surface", authConnection(upstream.URL+"/mcp", &toolcredential.Auth{Type: "none"}, tools))
	logs := &bytes.Buffer{}
	previous := log.Logger
	log.Logger = zerolog.New(&syncWriter{w: logs})
	t.Cleanup(func() { log.Logger = previous })
	return &ruleFixture{t: t, h: h, store: store, upstream: upstream, records: attachLedger(t, h), logs: logs}
}

func rulesOf(rules ...toolpolicy.Rule) *toolexec.ToolPolicy { return &toolexec.ToolPolicy{Rules: rules} }

func denyRule(id, when, message string) toolpolicy.Rule {
	return toolpolicy.Rule{ID: id, When: when, Effect: toolpolicy.Deny, Message: message}
}
func allowRule(id, when string) toolpolicy.Rule {
	return toolpolicy.Rule{ID: id, When: when, Effect: toolpolicy.Allow}
}

type ruleCaller struct {
	groups  []string
	unknown bool // the verifier recorded the group view as missing
	access  *accessPointCall
}

func (f *ruleFixture) request(body string, caller ruleCaller) *http.Request {
	r := trustedConnectedRequest("/api/v1/mcp/connections/surface/invoke", body, allScopes)
	r.Header.Set("Content-Type", "application/json")
	identity := models.WIFIdentity{PrincipalID: "principal-a", Subject: "subject", Groups: caller.groups, IsWorkforce: true}
	if caller.unknown {
		identity.VerifiedEvidence = &models.IdentityEvidence{GroupsPresent: false}
	} else if caller.groups != nil {
		identity.VerifiedEvidence = &models.IdentityEvidence{GroupsPresent: true, GroupsValid: true, Groups: caller.groups}
	}
	ctx := context.WithValue(r.Context(), keyWIFIdentity, &identity)
	ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: identity, Scopes: allScopes})
	if caller.access != nil {
		ctx = withAccessPointCall(ctx, caller.access)
	}
	return r.WithContext(ctx)
}

func (f *ruleFixture) call(name, args string, caller ruleCaller) *httptest.ResponseRecorder {
	body := `{"jsonrpc":"2.0","id":"rpc","method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	w := httptest.NewRecorder()
	f.h.NativeTool(w, f.request(body, caller))
	return w
}

func (f *ruleFixture) list(caller ruleCaller) []string {
	w := httptest.NewRecorder()
	f.h.NativeTool(w, f.request(`{"jsonrpc":"2.0","id":9,"method":"tools/list"}`, caller))
	var response struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		f.t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	names := []string{}
	for _, tool := range response.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

type policyDenied struct {
	ID    string `json:"id"`
	Error struct {
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Data    map[string]string `json:"data"`
	} `json:"error"`
}

func decodeDenied(t *testing.T, w *httptest.ResponseRecorder) policyDenied {
	t.Helper()
	var out policyDenied
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("not JSON-RPC: %s", w.Body.String())
	}
	return out
}

func TestToolRulesDenyByArgumentRecordsWithoutArguments(t *testing.T) {
	spans := installSpans(t, false)
	probe := newLeakProbe(t)
	f := newRuleFixture(t, rulesOf(denyRule("no-etc", "path_under(args.path, '/etc')", "files under /etc are off limits")))
	f.logs.Reset()
	args := func(path string) string {
		return `{"path":"` + path + `","note":"` + policyMarker + `"}`
	}
	denied := f.call("files.read", args("/srv/../etc/passwd"), ruleCaller{})
	d := decodeDenied(t, denied)
	if denied.Code != 403 || d.ID != "rpc" || d.Error.Code != toolexec.RPCForbidden || d.Error.Data["reason"] != "tool_policy_denied" || d.Error.Data["rule"] != "no-etc" || d.Error.Message != "files under /etc are off limits" {
		t.Fatalf("denial %d %s", denied.Code, denied.Body.String())
	}
	if tools, _ := f.upstream.counts(); tools != 0 {
		t.Fatal("refused call reached the upstream")
	}
	if allowed := f.call("files.read", args("/srv/data/a.txt"), ruleCaller{}); allowed.Code != 200 {
		t.Fatalf("permitted call refused: %d %s", allowed.Code, allowed.Body.String())
	}
	records := f.records()
	var refused usageledger.Record
	for _, r := range records {
		if r.ErrorClass != "" {
			refused = r
		}
	}
	if len(records) != 2 || refused.ErrorClass != "tool_policy_denied" || refused.ToolPolicyRule != "no-etc" || refused.Status != usageledger.StatusDenied || refused.ToolName != "files.read" {
		t.Fatalf("ledger %+v", records)
	}
	raw, _ := json.Marshal(records)
	for where, text := range map[string]string{"ledger": string(raw), "logs": f.logs.String(), "metrics": probe.metricText(t), "response": denied.Body.String()} {
		if strings.Contains(text, policyMarker) || strings.Contains(text, "/etc/passwd") {
			t.Fatalf("%s leaked call arguments: %s", where, text)
		}
	}
	assertNoMarker(t, spans.GetSpans(), policyMarker, "/etc/passwd")
	attrs := map[string]string{}
	for _, s := range spans.GetSpans() {
		for k, v := range spanKV(s) {
			attrs[k] = v
		}
	}
	if attrs["aiplex.tool_policy.decision"] != "tool_policy_denied" || attrs["aiplex.tool_policy.rule"] != "no-etc" {
		t.Fatalf("span attributes %v", attrs)
	}
	metrics := probe.metricText(t)
	if !strings.Contains(metrics, "aiplex.tool_policy.decisions") || !strings.Contains(metrics, "aiplex.tool_policy.decision=deny") || strings.Contains(metrics, "no-etc") {
		t.Fatalf("metrics %s", metrics)
	}
}

func TestToolRulesAllowRulesDefaultDenyAndGroups(t *testing.T) {
	f := newRuleFixture(t, rulesOf(
		allowRule("eng", "caller.groups_known && 'eng' in caller.groups"),
		denyRule("no-delete-args", "has(args.force) && args.force == true", "force is not allowed"),
	))
	cases := []struct {
		name    string
		caller  ruleCaller
		args    string
		status  int
		reason  string
		ruleID  string
		message string
	}{
		{"group member", ruleCaller{groups: []string{"eng"}}, `{}`, 200, "", "", ""},
		{"other group", ruleCaller{groups: []string{"sales"}}, `{}`, 403, "tool_policy_denied", "", "tool call is not permitted by any allow rule of this tool's policy"},
		{"unknown membership is not empty", ruleCaller{unknown: true}, `{}`, 403, "tool_policy_denied", "", ""},
		{"deny wins over allow", ruleCaller{groups: []string{"eng"}}, `{"force":true}`, 403, "tool_policy_denied", "no-delete-args", "force is not allowed"},
	}
	for _, c := range cases {
		w := f.call("search", c.args, c.caller)
		if w.Code != c.status {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
		if c.status == 403 {
			d := decodeDenied(t, w)
			if d.Error.Data["reason"] != c.reason || d.Error.Data["rule"] != c.ruleID || (c.message != "" && d.Error.Message != c.message) {
				t.Fatalf("%s: %s", c.name, w.Body.String())
			}
		}
	}
}

func TestToolRulesEvaluationErrorDenies(t *testing.T) {
	f := newRuleFixture(t, rulesOf(
		denyRule("needs-path", "args.path.startsWith('/tmp')", "x"),
	))
	w := f.call("search", `{"other":"`+policyMarker+`"}`, ruleCaller{})
	d := decodeDenied(t, w)
	if w.Code != 403 || d.Error.Data["reason"] != "tool_policy_error" || d.Error.Data["rule"] != "needs-path" || strings.Contains(w.Body.String(), "path") && strings.Contains(w.Body.String(), "no such key") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if tools, _ := f.upstream.counts(); tools != 0 {
		t.Fatal("call reached upstream despite a rule error")
	}
	records := f.records()
	if len(records) != 1 || records[0].ErrorClass != "tool_policy_error" || records[0].ToolPolicyRule != "needs-path" {
		t.Fatalf("%+v", records)
	}
	// Unguarded unknown group access is an error, not an empty list.
	g := newRuleFixture(t, rulesOf(denyRule("blocked", "'contractor' in caller.groups", "")))
	w = g.call("search", `{}`, ruleCaller{unknown: true})
	if w.Code != 403 || decodeDenied(t, w).Error.Data["reason"] != "tool_policy_error" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// Arguments over the evaluation cap are refused, not truncated.
	big := `{"x":"` + strings.Repeat("a", toolpolicy.MaxArgsBytes) + `"}`
	f2 := newRuleFixture(t, rulesOf(denyRule("never", "false", "")))
	w = f2.call("search", big, ruleCaller{})
	if w.Code != 403 || decodeDenied(t, w).Error.Data["reason"] != "tool_policy_error" {
		t.Fatalf("oversize arguments: %d %s", w.Code, w.Body.String())
	}
}

func TestToolRulesAnnotationsComeFromGatewayObservation(t *testing.T) {
	f := newRuleFixture(t, rulesOf(denyRule("destructive", "has(tool.annotations.destructive) && tool.annotations.destructive", "destructive tools are blocked")))
	f.upstream.list = `[{"name":"search","annotations":{"readOnlyHint":true,"destructiveHint":false}},{"name":"delete","annotations":{"destructiveHint":true}},{"name":"files.read"}]`
	// Before any listing the annotation is unknown (absent) and the guard passes.
	if w := f.call("delete", `{}`, ruleCaller{}); w.Code != 200 {
		t.Fatalf("unknown annotation guarded: %d %s", w.Code, w.Body.String())
	}
	if names := f.list(ruleCaller{}); strings.Join(names, ",") != "search,files.read" {
		t.Fatalf("a tool whose observed annotations make every call denied is hidden: %v", names)
	}
	if w := f.call("delete", `{}`, ruleCaller{}); w.Code != 403 || decodeDenied(t, w).Error.Data["rule"] != "destructive" {
		t.Fatalf("observed destructive tool allowed: %d %s", w.Code, w.Body.String())
	}
	if w := f.call("search", `{}`, ruleCaller{}); w.Code != 200 {
		t.Fatalf("non-destructive tool refused: %d %s", w.Code, w.Body.String())
	}
}

func TestToolRulesListHidesOnlyWhenDecidableWithoutArguments(t *testing.T) {
	// A caller-only deny hides every tool from a user; the args deny cannot hide.
	f := newRuleFixture(t, rulesOf(denyRule("users-out", "caller.kind == 'agent' && tool.name == 'delete'", ""), denyRule("by-args", "args.x == 1", "")))
	agent := ruleCaller{access: &accessPointCall{ID: "team", CallerKind: "agent"}}
	user := ruleCaller{access: &accessPointCall{ID: "team", CallerKind: "user"}}
	if names := f.list(agent); strings.Join(names, ",") != "search,files.read" {
		t.Fatalf("agent list %v", names)
	}
	if names := f.list(user); strings.Join(names, ",") != "search,delete,files.read" {
		t.Fatalf("user list %v", names)
	}
	// A call to a hidden tool is refused as well.
	if w := f.call("delete", `{}`, agent); w.Code != 403 {
		t.Fatalf("hidden tool callable: %d", w.Code)
	}
	// Default deny with only decidable allow rules hides tools the allow misses.
	g := newRuleFixture(t, rulesOf(allowRule("only-search", "tool.name == 'search'")))
	if names := g.list(user); strings.Join(names, ",") != "search" {
		t.Fatalf("allow list %v", names)
	}
	// An allow rule that reads args might match, so it hides nothing.
	h := newRuleFixture(t, rulesOf(allowRule("by-args", "args.ok == true")))
	if names := h.list(user); len(names) != 3 {
		t.Fatalf("args allow hid tools: %v", names)
	}
}

func TestToolRulesAccessPointCanOnlyAddRestrictions(t *testing.T) {
	f := newRuleFixture(t, rulesOf(denyRule("conn-no-big", "args.n > 10", "too big for this connection")))
	ap := &accessPointCall{ID: "team", CallerKind: "user", toolRules: map[string][]toolpolicy.Rule{"surface": {denyRule("ap-no-delete", "tool.name == 'delete'", "not through this access point")}}}
	with := ruleCaller{access: ap}
	// Passing both sets works.
	if w := f.call("search", `{"n":1}`, with); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// The access point's rule restricts further.
	w := f.call("delete", `{"n":1}`, with)
	if d := decodeDenied(t, w); w.Code != 403 || d.Error.Data["rule"] != "ap-no-delete" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// The connection's rule still applies through the access point.
	if w := f.call("search", `{"n":11}`, with); w.Code != 403 || decodeDenied(t, w).Error.Data["rule"] != "conn-no-big" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// An allow rule on the access point cannot loosen the connection.
	loose := &accessPointCall{ID: "team", CallerKind: "user", toolRules: map[string][]toolpolicy.Rule{"surface": {allowRule("ap-allow-all", "true")}}}
	if w := f.call("search", `{"n":11}`, ruleCaller{access: loose}); w.Code != 403 {
		t.Fatalf("access point rule loosened the connection: %d", w.Code)
	}
	// Without an access point only the connection's rules apply.
	if w := f.call("delete", `{"n":1}`, ruleCaller{}); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// Records carry the access point id.
	for _, r := range f.records() {
		if r.ErrorClass != "" && r.AccessPointID != "team" && r.ToolPolicyRule == "ap-no-delete" {
			t.Fatalf("record %+v", r)
		}
	}
}

func TestToolRulesScopeAndListsAreCheckedFirst(t *testing.T) {
	f := newRuleFixture(t, &toolexec.ToolPolicy{Deny: []string{"delete"}, Rules: []toolpolicy.Rule{allowRule("any", "true")}})
	w := f.call("delete", `{}`, ruleCaller{})
	if d := decodeDenied(t, w); w.Code != 403 || d.Error.Data["reason"] != "connection_policy" {
		t.Fatalf("name list is not evaluated before rules: %d %s", w.Code, w.Body.String())
	}
}

func TestToolRulesValidatedOnSave(t *testing.T) {
	const origin = "https://tools.example"
	store := resources.NewMemoryStore()
	h, _ := authHandler(t, store, origin, apiSecrets{})
	save := func(rules ...toolpolicy.Rule) error {
		raw, _ := json.Marshal(authConnection(origin+"/mcp", &toolcredential.Auth{Type: "none"}, rulesOf(rules...)))
		return h.validate(context.Background(), "tenant", "tool-connection", "surface", raw)
	}
	if err := save(denyRule("ok", "args.x == 1", "")); err != nil {
		t.Fatal(err)
	}
	for name, rule := range map[string]toolpolicy.Rule{
		"type error":    denyRule("bad", "args.x + ", ""),
		"unknown field": denyRule("bad", "caller.email == 'a'", ""),
		"not boolean":   denyRule("bad", "'a'", ""),
		"bad effect":    {ID: "bad", When: "true", Effect: "log"},
	} {
		if err := save(rule); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	raw, _ := json.Marshal(authConnection(origin+"/mcp", &toolcredential.Auth{Type: "none"}, rulesOf(denyRule("ok", "true", ""))))
	unknown := strings.Replace(string(raw), `"effect":"deny"`, `"effect":"deny","script":"x"`, 1)
	if err := h.validate(context.Background(), "tenant", "tool-connection", "surface", []byte(unknown)); err == nil {
		t.Error("unknown rule field accepted")
	}
	// Access point exposure rules are validated too.
	apErr := h.validateAccessPointTools(context.Background(), "tenant", []AccessPointTool{{ConnectionRef: "surface", Rules: []toolpolicy.Rule{denyRule("bad", "nope(", "")}}})
	if apErr == nil {
		t.Error("access point accepted a rule that does not compile")
	}
}

func TestToolRulesDryRun(t *testing.T) {
	f := newRuleFixture(t, rulesOf(
		allowRule("eng", "caller.groups_known && 'eng' in caller.groups"),
		denyRule("no-etc", "path_under(args.path, '/etc')", "no"),
	))
	test := func(body string) (int, ToolRulesTestResult) {
		r := trustedConnectedRequest("/api/v1/mcp/connections/surface/rules/test", body, nil)
		w := httptest.NewRecorder()
		f.h.TestToolRules(w, r)
		var out ToolRulesTestResult
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	code, out := test(`{"tool":"files.read","args":{"path":"/etc/hosts","m":"` + policyMarker + `"},"subject":{"kind":"user","groups":["eng"]}}`)
	if code != 200 || out.Decision != "deny" || out.Rule != "no-etc" || len(out.Trace) != 2 || out.Trace[0].Result != "matched" || out.Trace[1].Result != "matched" {
		t.Fatalf("%d %+v", code, out)
	}
	code, out = test(`{"tool":"files.read","args":{"path":"/srv/a"},"subject":{"kind":"user","groups":["sales"]}}`)
	if out.Decision != "deny" || out.Reason != toolpolicy.ReasonNoAllowRule || out.Trace[0].Result != "not_matched" {
		t.Fatalf("%d %+v", code, out)
	}
	code, out = test(`{"tool":"files.read","args":{"path":"/srv/a"},"subject":{"kind":"user"}}`)
	if out.Decision != "deny" || out.Reason != toolpolicy.ReasonNoAllowRule {
		t.Fatalf("unknown groups: %+v", out)
	}
	code, out = test(`{"tool":"files.read","args":{"path":"/srv/a"},"subject":{"kind":"user","groups":["eng"]}}`)
	if code != 200 || out.Decision != "allow" || out.ListHidden {
		t.Fatalf("%d %+v", code, out)
	}
	// An erroring rule shows the evaluator error.
	_, out = test(`{"tool":"files.read","args":{},"subject":{"groups":["eng"]}}`)
	if out.Decision != "error" || out.Trace[1].Result != "error" || out.Trace[1].Error == "" {
		t.Fatalf("%+v", out)
	}
	// Candidate rules can be tried without saving them.
	_, out = test(`{"tool":"search","subject":{},"rules":[{"id":"cand","when":"tool.name == 'search'","effect":"deny"}]}`)
	if out.Decision != "deny" || out.Rule != "cand" {
		t.Fatalf("%+v", out)
	}
	if code, _ = test(`{"tool":"search","subject":{},"rules":[{"id":"cand","when":"((","effect":"deny"}]}`); code != 400 {
		t.Fatalf("invalid candidate rules: %d", code)
	}
	// The sample is not stored anywhere.
	if len(f.records()) != 0 {
		t.Fatal("dry run wrote a ledger record")
	}
	saved, _ := f.store.Get(context.Background(), "tenant", "tool-connection", "surface")
	if strings.Contains(string(saved.Spec), policyMarker) || strings.Contains(f.logs.String(), policyMarker) {
		t.Fatal("dry run stored or logged the sample")
	}
}

func TestToolRulesTimeIsProvidedNotCallerControlled(t *testing.T) {
	f := newRuleFixture(t, rulesOf(denyRule("old", "request.time < timestamp('2000-01-01T00:00:00Z')", "")))
	if w := f.call("search", `{}`, ruleCaller{}); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	_ = time.Now
}

func TestToolRulesSeeAccessPointLabels(t *testing.T) {
	f := newRuleFixture(t, rulesOf(denyRule("no-prod-delete", "'env' in access_point.labels && access_point.labels.env == 'prod' && tool.name == 'delete'", "not in production")))
	prod := ruleCaller{access: &accessPointCall{ID: "team", CallerKind: "user", labels: map[string]string{"env": "prod"}}}
	dev := ruleCaller{access: &accessPointCall{ID: "team", CallerKind: "user", labels: map[string]string{"env": "dev"}}}
	if w := f.call("delete", `{"n":1}`, prod); w.Code != 403 || decodeDenied(t, w).Error.Data["rule"] != "no-prod-delete" {
		t.Fatalf("the access point's labels reach the rule: %d %s", w.Code, w.Body.String())
	}
	if w := f.call("delete", `{"n":1}`, dev); w.Code != 200 {
		t.Fatalf("another label value is allowed: %d %s", w.Code, w.Body.String())
	}
	if w := f.call("delete", `{"n":1}`, ruleCaller{}); w.Code != 200 {
		t.Fatalf("without an access point labels are empty: %d %s", w.Code, w.Body.String())
	}
}

// A read-only tool call whose transport fails is sent once more with the
// same, already checked and hooked body: rules and hooks are not run again.
func TestToolRetryResendsTheCheckedBodyOnce(t *testing.T) {
	f := newRuleFixture(t, rulesOf(denyRule("no-big", "args.n > 10", "too big")))
	putHookPolicy(t, f.store, `{"id":"mask","scope":{"kind":"tenant"},"planes":["mcplex"],"hooks":[{"id":"m","type":"redact","phases":["request"],"redact":{"rules":[{"id":"email","builtin":"email","action":"mask"}]}}]}`)
	f.list(ruleCaller{}) // observe readOnlyHint=true for "search"
	f.upstream.mu.Lock()
	f.upstream.dropCalls, f.upstream.bodies = 1, nil
	f.upstream.mu.Unlock()
	w := f.call("search", `{"n":1,"to":"jane@example.com"}`, ruleCaller{})
	if w.Code != 200 {
		t.Fatalf("retried read-only call: %d %s", w.Code, w.Body.String())
	}
	f.upstream.mu.Lock()
	bodies := append([]string(nil), f.upstream.bodies...)
	f.upstream.mu.Unlock()
	if len(bodies) != 2 || bodies[0] != bodies[1] || !strings.Contains(bodies[0], "[EMAIL]") {
		t.Fatalf("the retry re-sends the same hooked body: %q", bodies)
	}
	var rec *usageledger.Record
	for _, r := range f.records() {
		if r.ToolName == "search" && r.Operation == "tools/call" {
			r := r
			rec = &r
		}
	}
	if rec == nil || rec.Hooks == nil || len(rec.Hooks.Results) != 1 {
		t.Fatalf("hooks ran once: %+v", rec)
	}
	// A tool that is not read-only is never re-sent.
	f.upstream.mu.Lock()
	f.upstream.dropCalls, f.upstream.bodies = 1, nil
	f.upstream.mu.Unlock()
	w = f.call("delete", `{"n":1}`, ruleCaller{})
	f.upstream.mu.Lock()
	sends := len(f.upstream.bodies)
	f.upstream.mu.Unlock()
	if w.Code != http.StatusBadGateway || sends != 1 {
		t.Fatalf("non-read-only call retried: %d (%d sends)", w.Code, sends)
	}
}
