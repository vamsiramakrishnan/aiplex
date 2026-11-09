package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type spCall struct{ method, path, query, ifMatch, body string }

// spServer records every call and answers from a route table keyed by
// "METHOD path" (query ignored). Unlisted routes answer 404.
func spServer(t *testing.T, calls *[]spCall, routes map[string]string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body bytes.Buffer
		_, _ = body.ReadFrom(r.Body)
		*calls = append(*calls, spCall{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("If-Match"), body.String()})
		w.Header().Set("Content-Type", "application/json")
		resp, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"nope"}`))
			return
		}
		switch {
		case resp == "204":
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(resp, "201:"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(strings.TrimPrefix(resp, "201:")))
		default:
			_, _ = w.Write([]byte(resp))
		}
	}))
	t.Cleanup(server.Close)
	oldURL, oldToken, oldOutput := apiURL, token, output
	apiURL, token, output = server.URL, "admin", "table"
	t.Cleanup(func() { apiURL, token, output = oldURL, oldToken, oldOutput })
}

func runSP(t *testing.T, stdin string, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

const spAPRecord = `{"tenant_id":"t","kind":"access-point","id":"ide","revision":3,"spec":{"id":"ide","display_name":"IDE","caller_kinds":["user","client"],"subjects":{},"models":[{"name":"gemini","surface_ref":"genai","native_model":"gemini-2.5-pro"}],"tools":[{"connection_ref":"github"}],"qos":{"class":"gold","weight":10}}}`

func TestAccessPointsListGetAndJSONPassthrough(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/access-points/":    "[" + spAPRecord + "]",
		"GET /api/v1/access-points/ide": spAPRecord,
	})
	out, err := runSP(t, "", accessPointsCmd(), "list")
	if err != nil || !strings.Contains(out, "ID") || !strings.Contains(out, "user,client") || !strings.Contains(out, "gold") {
		t.Fatal(out, err)
	}
	out, err = runSP(t, "", accessPointsCmd(), "get", "ide")
	if err != nil || !strings.Contains(out, "gemini -> gemini-2.5-pro on genai") || !strings.Contains(out, "class gold") {
		t.Fatal(out, err)
	}
	output = "json"
	out, err = runSP(t, "", accessPointsCmd(), "get", "ide")
	var rec map[string]any
	if err != nil || json.Unmarshal([]byte(out), &rec) != nil || rec["revision"] != float64(3) {
		t.Fatal(out, err)
	}
	output = "yaml"
	out, err = runSP(t, "", accessPointsCmd(), "get", "ide")
	if err != nil || !strings.Contains(out, "revision: 3") || !strings.Contains(out, "caller_kinds:") {
		t.Fatal(out, err)
	}
}

func TestAccessPointsApplyCreatesThenReplacesAtRevision(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"PUT /api/v1/access-points/new": "201:" + strings.ReplaceAll(spAPRecord, `"id":"ide"`, `"id":"new"`),
		"GET /api/v1/access-points/ide": spAPRecord,
		"PUT /api/v1/access-points/ide": spAPRecord,
	})
	dir := t.TempDir()
	file := filepath.Join(dir, "ap.yaml")
	if err := os.WriteFile(file, []byte("id: new\ncaller_kinds: [user]\nsubjects: {}\nmodels:\n  - {name: m, surface_ref: s, native_model: n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runSP(t, "", accessPointsCmd(), "apply", "-f", file)
	if err != nil || !strings.Contains(out, "Created access point new") {
		t.Fatal(out, err)
	}
	var put spCall
	for _, c := range calls {
		if c.method == http.MethodPut {
			put = c
		}
	}
	if put.ifMatch != "0" || put.path != "/api/v1/access-points/new" || !strings.Contains(put.body, `"caller_kinds":["user"]`) || !strings.Contains(put.body, `"native_model":"n"`) {
		t.Fatalf("put = %+v", put)
	}

	// Existing resource: the stored revision is sent in If-Match, and a
	// get -o json record can be applied back unchanged.
	calls = nil
	record := filepath.Join(dir, "record.json")
	if err = os.WriteFile(record, []byte(spAPRecord), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = runSP(t, "", accessPointsCmd(), "apply", "-f", record)
	if err != nil || !strings.Contains(out, "Updated access point ide (revision 3)") {
		t.Fatal(out, err)
	}
	put = spCall{}
	for _, c := range calls {
		if c.method == http.MethodPut {
			put = c
		}
	}
	if put.ifMatch != "3" || strings.Contains(put.body, `"revision"`) {
		t.Fatalf("replace = %+v", put)
	}
	// Unknown fields are rejected before anything is sent.
	calls = nil
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"id":"ide","caller_kinds":["user"],"subjects":{},"modles":[]}`), 0o600)
	if _, err = runSP(t, "", accessPointsCmd(), "apply", "-f", bad); err == nil || !strings.Contains(err.Error(), "modles") {
		t.Fatalf("typo accepted: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls sent for an invalid file: %+v", calls)
	}
	// stdin
	if _, err = runSP(t, `{"id":"ide","caller_kinds":["agent"],"subjects":{}}`, accessPointsCmd(), "apply", "-f", "-"); err != nil {
		t.Fatal(err)
	}
}

func TestAccessPointsDeleteAndQuotaPlan(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/access-points/ide":             spAPRecord,
		"DELETE /api/v1/access-points/ide":          "204",
		"POST /api/v1/access-points/ide/quota-plan": `{"access_point_id":"ide","tenant_id":"t","applied":false,"fragment":{"rates":[{"id":"ap-ide-rpm","meter_id":"requests","rate":60,"interval":60000000000,"burst":60}],"concurrency":[{"id":"ap-ide-concurrency","limit":4}]},"scheduler":{"class":"gold","flow_id":"ap-ide","weight":10,"routes":[]},"policy":{},"validation":{"valid":true,"validator":"compile"},"warnings":["no quota policy is active"]}`,
	})
	out, err := runSP(t, "", accessPointsCmd(), "delete", "ide")
	if err != nil || !strings.Contains(out, "Deleted access point ide") {
		t.Fatal(out, err)
	}
	if last := calls[len(calls)-1]; last.method != http.MethodDelete || last.ifMatch != "3" {
		t.Fatalf("delete = %+v", last)
	}
	out, err = runSP(t, "", accessPointsCmd(), "quota-plan", "ide")
	for _, want := range []string{"not applied", "Validation: valid", "ap-ide-rpm", "Concurrency: ap-ide-concurrency limit 4", "class gold flow ap-ide weight 10", "Warning: no quota policy is active"} {
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q (%v)", want, out, err)
		}
	}
}

const spConnectJSON = `{"access_point_id":"ide","client":"claude-code","base_urls":{"anthropic":"https://g/ap/ide"},"models":[],"mcp_url":"https://g/ap/ide/mcp","mcp_catalog_url":"https://g/ap/ide/v0.1/servers","auth":{"scheme":"bearer","header":"Authorization: Bearer $AIPLEX_TOKEN","token_env":"AIPLEX_TOKEN","caller_kinds":["user"],"steps":["Sign in.","Send it."]},"snippets":[{"title":"Claude Code: ~/.claude/settings.json","language":"json","content":"{\n  \"env\": {\"ANTHROPIC_BASE_URL\": \"https://g/ap/ide\"}\n}\n"}],"notes":["Provide the token."]}`

func TestConnectPrintsSnippetsAndPassesThroughJSON(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/overview/access-points/ide/connect": spConnectJSON})
	out, err := runSP(t, "", connectCmd(), "ide", "--client", "claude-code")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"anthropic base URL", "https://g/ap/ide", "MCP URL", "1. Sign in.", "Claude Code: ~/.claude/settings.json [json]", `    {`, "Provide the token."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if calls[0].query != "client=claude-code" {
		t.Fatalf("query = %q", calls[0].query)
	}
	output = "json"
	out, err = runSP(t, "", connectCmd(), "ide")
	var got map[string]any
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got["access_point_id"] != "ide" {
		t.Fatal(out, err)
	}
}

const spSubjectsJSON = `{"kind":"agent","since":"2026-11-08T12:00:00Z","subjects":[
 {"kind":"agent","id":"tutor","display_name":"Tutor","identity":{"source":"spiffe","detail":"d"},"access_points":["agents"],"last_seen":"2026-11-09T10:02:00Z","usage":{"requests":3,"records":3,"errors":0,"denied":0,"tokens":{"input_tokens":10},"tokens_unknown":{"input_tokens":1,"output_tokens":3,"total_tokens":3},"cost":[{"currency":"USD","basis":"settled","amount":"1234","scale":4}],"unknown_cost_records":1},"known_from":["usage-ledger"]},
 {"kind":"agent","id":"quiet","identity":{"source":"unknown","detail":"d"},"access_points":[],"last_seen":null,"usage":{"requests":0,"records":0,"errors":0,"denied":0,"tokens":{},"tokens_unknown":{},"cost":[],"unknown_cost_records":0},"known_from":["access-point"]},
 {"kind":"agent","id":"nolog","identity":{"source":"unknown","detail":"d"},"access_points":["agents"],"last_seen":null,"usage":null,"known_from":["access-point"]}],
 "next_cursor":"abc","usage_available":true,"usage_truncated":false,"derived_from":["x"],"limitations":["There is no user directory."]}`

func TestWhoListShowsUnknownNotZero(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/overview/subjects": spSubjectsJSON})
	out, err := runSP(t, "", whoCmd(), "--kind", "agent", "--since", "7d", "--limit", "3")
	if err != nil {
		t.Fatal(out, err)
	}
	lines := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			lines[f[0]] = l
		}
	}
	if l := lines["tutor"]; !strings.Contains(l, "10 (+1 unknown)") || !strings.Contains(l, "USD 0.1234 (settled)") || !strings.Contains(l, "+1 unknown") {
		t.Errorf("tutor row = %q", l)
	}
	// output tokens were never reported for tutor: unknown, not 0.
	if l := lines["tutor"]; strings.Count(l, "unknown") < 2 || !strings.Contains(l, " unknown ") {
		t.Errorf("tutor output tokens should read unknown: %q", l)
	}
	// The AUTH column legitimately reads "unknown" for both of these.
	if l := lines["quiet"]; !strings.Contains(l, "never") || strings.Count(l, "unknown") != 1 || !strings.Contains(l, " 0 ") {
		t.Errorf("an empty window is a real zero, with no unknown usage: %q", l)
	}
	if l := lines["nolog"]; strings.Count(l, "unknown") != 5 {
		t.Errorf("no usage at all must read unknown for requests, in, out and cost (plus auth): %q", l)
	}
	if !strings.Contains(out, "More: aiplex who --kind agent --cursor abc") || !strings.Contains(out, "There is no user directory.") {
		t.Errorf("missing footer: %s", out)
	}
	if calls[0].query != "kind=agent&limit=3&since=7d" {
		t.Errorf("query = %s", calls[0].query)
	}
	// A bare kind lists too.
	if _, err = runSP(t, "", whoCmd(), "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err = runSP(t, "", whoCmd()); err == nil {
		t.Fatal("no kind accepted")
	}
}

const spDetailJSON = `{"kind":"user","id":"principal-1","identity":{"source":"workforce-principal","detail":"hashed"},
"user_identity_providers":[{"id":"okta","issuer":"https://i","disabled":false}],
"access_points":[{"id":"ide","admits":"yes","reason":"access point admits every caller of kind user"}],
"access":{"models":[{"kind":"model","name":"gemini","access_point_id":"ide","state":"granted","reasons":[{"type":"access-point","ref":"ide"},{"type":"manual-scope","ref":"principal-1"}]}],
 "tools":[{"kind":"tool","name":"github__search","access_point_id":"ide","state":"unknown","reasons":[{"type":"access-point","ref":"ide"},{"type":"role-binding","ref":"rb-eng","conditional":true}]}],"skills":[],"scopes_known":true,"scopes_note":"Group memberships are not stored.","truncated":false},
"quota":{"access_points":[{"access_point_id":"ide","qos_class":"gold","coverage":{"state":"unknown","detail":"no policy"},"allocations":{"budgets":[],"rates":[],"concurrency":[]}}],"balance":{"available":false,"reason":"keys are not exported"}},
"since":"2026-11-08T12:00:00Z","last_seen":"2026-11-09T10:00:00Z",
"usage":{"requests":2,"records":2,"errors":0,"denied":0,"tokens":{},"tokens_unknown":{"input_tokens":2},"cost":[],"unknown_cost_records":2},
"usage_by_model":[{"key":{"model_id":"gemini-2.5-pro"},"usage":{"requests":2,"records":2,"errors":0,"denied":0,"tokens":{},"tokens_unknown":{"input_tokens":2},"cost":[],"unknown_cost_records":2}}],
"usage_by_tool":[],"notes":["Which identity provider issued this principal is not recoverable."],"known_from":["usage-ledger"]}`

func TestWhoDetailShowsReasonsAndOmittedBalance(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/overview/subjects/user/principal-1": spDetailJSON})
	out, err := runSP(t, "", whoCmd(), "user", "principal-1")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"access-point:ide,manual-scope:principal-1", "role-binding:rb-eng (conditional)", "Remaining balance: unavailable - keys are not exported", "ide class gold: unknown - no policy", "Identity providers in this tenant: okta", "Requests 2   In unknown   Out unknown   Cost unknown", "gemini-2.5-pro"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	output = "json"
	out, err = runSP(t, "", whoCmd(), "user", "principal-1")
	if err != nil || !strings.Contains(out, `"reasons"`) {
		t.Fatal(out, err)
	}
}

const spProviderJSON = `{"id":"okta","preset":"okta","issuer":"https://i","audience":"a","subject_claim":"sub","revision":2,"principal_pool":"user-federation/t","directory":{"kind":"scim"}}`

func TestUserProvidersCRUDKeepsUnknownFields(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/identity/user-provider-presets":     `{"presets":[{"id":"okta","display_name":"Okta","inputs":[{"name":"domain","label":"Domain","required":true}]}],"live_authentication":false}`,
		"GET /api/v1/identity/user-providers":            "[" + spProviderJSON + "]",
		"GET /api/v1/identity/user-providers/okta":       spProviderJSON,
		"PUT /api/v1/identity/user-providers/okta":       spProviderJSON,
		"DELETE /api/v1/identity/user-providers/okta":    "204",
		"POST /api/v1/identity/user-providers/okta/plan": `{"provider":{"id":"okta","preset":"okta"},"vendor":"okta","issuer":"https://i","audience":"a","subject_claim":"sub","groups_from":"claim","principal_pool":"user-federation/t","steps":[{"title":"Create an app","content":"echo hi"}],"token_snippet":{"title":"Get a token"},"aiplex_steps":[{"title":"Verify"}],"notes":["n"]}`,
	})
	cmd := func() *cobra.Command {
		root := identityUserCmd()
		return root
	}
	out, err := runSP(t, "", cmd(), "user-providers", "presets")
	if err != nil || !strings.Contains(out, "okta") || !strings.Contains(out, "domain") || !strings.Contains(out, "live authentication is off") {
		t.Fatal(out, err)
	}
	out, err = runSP(t, "", cmd(), "user-providers", "list")
	if err != nil || !strings.Contains(out, "okta") || !strings.Contains(out, "https://i") {
		t.Fatal(out, err)
	}
	out, err = runSP(t, "", cmd(), "user-providers", "get", "okta")
	if err != nil || !strings.Contains(out, "user-federation/t") || !strings.Contains(out, "directory") {
		t.Fatal(out, err)
	}
	calls = nil
	file := filepath.Join(t.TempDir(), "okta.yaml")
	body := "id: okta\npreset: okta\ninputs: {domain: acme.okta.com}\ndisplay_name: Acme\ndirectory: {kind: scim}\nclient_ids: [a, b]\nacknowledge_overlap: true\n"
	if err = os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = runSP(t, "", cmd(), "user-providers", "apply", "-f", file)
	if err != nil || !strings.Contains(out, "Updated user identity provider okta (revision 2)") {
		t.Fatal(out, err)
	}
	var put spCall
	for _, c := range calls {
		if c.method == http.MethodPut {
			put = c
		}
	}
	var sent map[string]any
	if json.Unmarshal([]byte(put.body), &sent) != nil || put.ifMatch != "2" {
		t.Fatalf("put = %+v", put)
	}
	for _, key := range []string{"preset", "inputs", "directory", "client_ids", "acknowledge_overlap"} {
		if _, ok := sent[key]; !ok {
			t.Errorf("request dropped %q: %s", key, put.body)
		}
	}
	if _, leaked := sent["id"]; leaked {
		t.Errorf("provider id sent in the body: %s", put.body)
	}
	out, err = runSP(t, "", cmd(), "user-providers", "plan", "okta")
	if err != nil || !strings.Contains(out, "1. Create an app") || !strings.Contains(out, "Getting a token") || !strings.Contains(out, "echo hi") {
		t.Fatal(out, err)
	}
	out, err = runSP(t, "", cmd(), "user-providers", "delete", "okta")
	if err != nil || !strings.Contains(out, "Deleted user identity provider okta") {
		t.Fatal(out, err)
	}
	if last := calls[len(calls)-1]; last.method != http.MethodDelete || last.ifMatch != "2" {
		t.Fatalf("delete = %+v", last)
	}
	if _, err = runSP(t, "", cmd(), "user-providers", "apply", "-f", "-"); err == nil {
		t.Fatal("empty stdin accepted")
	}
}

func TestUserProvidersVerifyTokenHandling(t *testing.T) {
	const secret = "eyJhbGciOiJSUzI1NiJ9.SECRETBODY.SIG"
	var calls []spCall
	spServer(t, &calls, map[string]string{"POST /api/v1/identity/user-providers/verify": `{"valid":true,"checks":[{"name":"signature","status":"pass","reason":"ok"}],"groups_available":false,"groups_note":"claim absent","provider":{"id":"okta","preset":"okta"},"principal_id":"principal-9","is_workforce":true,"live_authentication":false}`})
	root := func() *cobra.Command { return identityUserCmd() }
	dir := t.TempDir()
	file := filepath.Join(dir, "token.txt")
	if err := os.WriteFile(file, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runSP(t, "", root(), "user-providers", "verify", "--token-file", file, "--provider", "okta")
	if err != nil || !strings.Contains(out, "Result: valid") || !strings.Contains(out, "principal-9") || !strings.Contains(out, "Groups:    unknown (claim absent)") {
		t.Fatal(out, err)
	}
	if strings.Contains(out, "SECRETBODY") {
		t.Fatal("token printed")
	}
	if !strings.Contains(calls[0].body, `"provider_id":"okta"`) || !strings.Contains(calls[0].body, "SECRETBODY") {
		t.Fatalf("request body = %s", calls[0].body)
	}
	// stdin
	out, err = runSP(t, secret, root(), "user-providers", "verify", "--token-file", "-")
	if err != nil || strings.Contains(out, "SECRETBODY") {
		t.Fatal(out, err)
	}
	// JSON output never contains the token either.
	output = "json"
	out, err = runSP(t, secret, root(), "user-providers", "verify", "--token-file", "-")
	if err != nil || strings.Contains(out, "SECRETBODY") || !strings.Contains(out, `"valid": true`) {
		t.Fatal(out, err)
	}
	output = "table"
	// The token is never accepted as a flag, and a file is required.
	if _, err = runSP(t, "", root(), "user-providers", "verify", "--token", secret); err == nil {
		t.Fatal("token flag accepted")
	} else if strings.Contains(err.Error(), "SECRETBODY") {
		t.Fatal("token echoed in error")
	}
	if _, err = runSP(t, "", root(), "user-providers", "verify"); err == nil || !strings.Contains(err.Error(), "--token-file") {
		t.Fatal(err)
	}
}

func TestUserProvidersVerifyInvalidExitsNonZero(t *testing.T) {
	const secret = "tok.SECRETBODY.sig"
	var calls []spCall
	spServer(t, &calls, map[string]string{"POST /api/v1/identity/user-providers/verify": `{"valid":false,"checks":[{"name":"signature","status":"pass","reason":"ok"},{"name":"audience","status":"fail","reason":"aud mismatch"}],"groups_available":false,"is_workforce":false,"live_authentication":true}`})
	out, err := runSP(t, secret, identityUserCmd(), "user-providers", "verify", "--token-file", "-")
	if err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("invalid token exited zero: %v", err)
	}
	if !strings.Contains(out, "FAIL") || !strings.Contains(out, "aud mismatch") || !strings.Contains(out, "Result: NOT valid") || strings.Contains(out, "SECRETBODY") {
		t.Fatal(out)
	}
}
