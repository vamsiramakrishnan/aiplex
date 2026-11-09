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
)

const identityCatalogJSON = `{"presets":[
 {"id":"github-actions","display_name":"GitHub Actions","cloud":"github","inputs":[
   {"name":"repository","label":"Repository","required":true,"example":"acme/tutor"},
   {"name":"ref","label":"Ref","required":true,"example":"refs/heads/main"},
   {"name":"environment","label":"Environment"},
   {"name":"audience","label":"Audience","required":true,"default":"https://aiplex.example.com"}],
  "allows_required_claims":true,"algorithms":["RS256"]},
 {"id":"azure-managed-identity","display_name":"Azure","cloud":"azure","inputs":[
   {"name":"token_version","label":"Token version","required":true,"enum":["1.0","2.0"]}],"algorithms":["RS256"]}],
 "default_audience":"https://aiplex.example.com","live_authentication":false}`

type identityCall struct{ method, path, ifMatch, body string }

func identityServer(t *testing.T, calls *[]identityCall, verifyValid bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body bytes.Buffer
		_, _ = body.ReadFrom(r.Body)
		*calls = append(*calls, identityCall{r.Method, r.URL.Path, r.Header.Get("If-Match"), body.String()})
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/agents/identity-presets":
			_, _ = w.Write([]byte(identityCatalogJSON))
		case r.URL.Path == "/api/v1/agents/identities/verify":
			if verifyValid {
				_, _ = w.Write([]byte(`{"valid":true,"checks":[{"name":"signature","status":"pass","reason":"ok"}],"principal_id":"p1","live_authentication":false}`))
			} else {
				_, _ = w.Write([]byte(`{"valid":false,"checks":[{"name":"signature","status":"pass","reason":"ok"},{"name":"audience","status":"fail","reason":"aud mismatch"},{"name":"binding","status":"skipped","reason":"earlier failure"}],"live_authentication":false}`))
			}
		case strings.HasSuffix(r.URL.Path, "/plan"):
			_, _ = w.Write([]byte(`{"binding":{"client_id":"agent","id":"gh","preset":"github-actions"},"cloud":"github","issuer":"https://i","audience":"https://a","subject":"s","steps":[{"title":"Add workflow","description":"desc","language":"yaml","content":"on: push"}],"token_snippet":{"title":"Acquire a token","content":"TOKEN=x"},"aiplex_steps":[{"title":"Dry-run"}],"notes":["be careful"]}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"gh","client_id":"agent","preset":"github-actions","issuer":"https://i","subject":"s","audience":"https://a","revision":1}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/identities"):
			_, _ = w.Write([]byte(`[{"id":"gh","client_id":"agent","preset":"github-actions","issuer":"https://i","subject":"s","disabled":true,"revision":3}]`))
		default:
			_, _ = w.Write([]byte(`{"id":"gh","client_id":"agent","preset":"github-actions","issuer":"https://i","subject":"s","revision":3}`))
		}
	}))
	t.Cleanup(server.Close)
	oldURL, oldToken, oldOutput := apiURL, token, output
	apiURL, token, output = server.URL, "admin", "table"
	t.Cleanup(func() { apiURL, token, output = oldURL, oldToken, oldOutput })
	return server
}

func runIdentity(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := agentIdentityCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestIdentityBindListsEveryMissingRequiredInputWithoutWriting(t *testing.T) {
	var calls []identityCall
	identityServer(t, &calls, true)
	_, err := runIdentity(t, "", "bind", "agent", "gh", "--preset", "github-actions", "--input", "environment=prod")
	if err == nil {
		t.Fatal("missing inputs accepted")
	}
	for _, want := range []string{"repository=acme/tutor", "ref=refs/heads/main"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "audience") {
		t.Errorf("defaulted input reported missing: %v", err)
	}
	for _, c := range calls {
		if c.method == http.MethodPut {
			t.Fatal("PUT sent despite validation failure")
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"bind", "a", "b", "--preset", "nope"}, "available presets: github-actions, azure-managed-identity"},
		{[]string{"bind", "a", "b", "--preset", "github-actions", "--input", "repository=x", "--input", "ref=y", "--input", "bogus=1"}, "unknown input"},
		{[]string{"bind", "a", "b", "--preset", "azure-managed-identity", "--input", "token_version=3.0"}, "one of: 1.0, 2.0"},
		{[]string{"bind", "a", "b", "--preset", "github-actions", "--input", "novalue"}, "key=value"},
		{[]string{"bind", "a", "b"}, "--preset is required"},
		{[]string{"bind", "a", "b", "--preset", "azure-managed-identity", "--input", "token_version=1.0", "--claim", "x=y"}, "does not accept --claim"},
	} {
		if _, err := runIdentity(t, "", tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestIdentityBindCreateAndReplaceSendRevision(t *testing.T) {
	var calls []identityCall
	identityServer(t, &calls, true)
	args := []string{"bind", "agent", "gh", "--preset", "github-actions", "--input", "repository=acme/tutor", "--input", "ref=refs/heads/main", "--claim", "job_workflow_ref=x", "--display-name", "CI"}
	out, err := runIdentity(t, "", args...)
	if err != nil || !strings.Contains(out, "Created identity binding gh") || !strings.Contains(out, "live authentication is off") {
		t.Fatal(out, err)
	}
	var put identityCall
	for _, c := range calls {
		if c.method == http.MethodPut {
			put = c
		}
	}
	if put.ifMatch != "" || put.path != "/api/v1/agents/agent/identities/gh" {
		t.Fatalf("create request wrong: %+v", put)
	}
	var body map[string]any
	if json.Unmarshal([]byte(put.body), &body) != nil || body["preset"] != "github-actions" || body["display_name"] != "CI" || body["required_claims"].(map[string]any)["job_workflow_ref"] != "x" {
		t.Fatalf("body wrong: %s", put.body)
	}
	calls = nil
	if _, err = runIdentity(t, "", append(args, "--replace")...); err != nil {
		t.Fatal(err)
	}
	var replaced bool
	for _, c := range calls {
		if c.method == http.MethodPut {
			replaced = c.ifMatch == `"3"`
		}
	}
	if !replaced {
		t.Fatalf("replace did not send current revision: %+v", calls)
	}
}

func TestIdentityListGetDeletePlan(t *testing.T) {
	var calls []identityCall
	identityServer(t, &calls, true)
	out, err := runIdentity(t, "", "list", "agent")
	if err != nil || !strings.Contains(out, "disabled") || !strings.Contains(out, "github-actions") {
		t.Fatal(out, err)
	}
	out, err = runIdentity(t, "", "get", "agent", "gh")
	if err != nil || !strings.Contains(out, "revision 3") {
		t.Fatal(out, err)
	}
	out, err = runIdentity(t, "", "presets")
	if err != nil || !strings.Contains(out, "repository,ref") || !strings.Contains(out, "live authentication is off") {
		t.Fatal(out, err)
	}
	calls = nil
	if out, err = runIdentity(t, "", "delete", "agent", "gh"); err != nil || !strings.Contains(out, "Deleted") {
		t.Fatal(out, err)
	}
	if last := calls[len(calls)-1]; last.method != http.MethodDelete || last.ifMatch != `"3"` {
		t.Fatalf("delete wrong: %+v", last)
	}
	out, err = runIdentity(t, "", "plan", "agent", "gh")
	for _, want := range []string{"1. Add workflow", "     on: push", "Getting a token", "TOKEN=x", "In AIPlex", "be careful"} {
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("plan output lacks %q:\n%s (%v)", want, out, err)
		}
	}
	output = "json"
	out, err = runIdentity(t, "", "plan", "agent", "gh")
	var plan map[string]any
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil || plan["cloud"] != "github" {
		t.Fatal(out, err)
	}
}

func TestIdentityVerifyReadsTokenFromFileOrStdinOnly(t *testing.T) {
	const secret = "aaa.bbb.SECRETSIGNATURE"
	var calls []identityCall
	identityServer(t, &calls, false)

	// A token flag must not exist.
	if _, err := runIdentity(t, "", "verify", "--token", secret); err == nil {
		t.Fatal("token accepted as a flag")
	}
	if _, err := runIdentity(t, ""); err != nil { // group command prints help
		t.Fatal(err)
	}
	if _, err := runIdentity(t, "", "verify"); err == nil || !strings.Contains(err.Error(), "--token-file is required") {
		t.Fatal("missing token source accepted", err)
	}
	if _, err := runIdentity(t, "", "verify", "--agent", "a", "--token-file", "-"); err == nil {
		t.Fatal("half-pinned verify accepted")
	}

	// stdin, invalid token: checks rendered, error returned (non-zero exit), token never echoed.
	out, err := runIdentity(t, secret+"\n", "verify", "--token-file", "-")
	if err == nil {
		t.Fatal("invalid token returned success")
	}
	if strings.Contains(out+err.Error(), secret) {
		t.Fatal("token leaked into output")
	}
	for _, want := range []string{"PASS  signature", "FAIL  audience", "aud mismatch", "SKIP  binding", "NOT valid"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	var sent map[string]string
	if json.Unmarshal([]byte(calls[len(calls)-1].body), &sent) != nil || sent["token"] != secret {
		t.Fatalf("token not sent in body: %+v", calls[len(calls)-1])
	}

	// file + pinned binding, valid token.
	identityServerValid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"valid": true, "checks": []map[string]string{{"name": "signature", "status": "pass", "reason": "ok"}}, "principal_id": "p1"})
	}))
	defer identityServerValid.Close()
	apiURL = identityServerValid.URL
	path := filepath.Join(t.TempDir(), "token.txt")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = runIdentity(t, "", "verify", "--agent", "agent", "--binding", "gh", "--token-file", path)
	if err != nil || !strings.Contains(out, "Result: valid") || !strings.Contains(out, "p1") || strings.Contains(out, secret) {
		t.Fatal(out, err)
	}
}

func TestReadIdentityTokenRejectsUnsafeInput(t *testing.T) {
	if _, err := readIdentityToken("-", strings.NewReader("  \n")); err == nil {
		t.Fatal("empty token accepted")
	}
	if _, err := readIdentityToken("-", strings.NewReader("a b")); err == nil {
		t.Fatal("whitespace token accepted")
	}
	if _, err := readIdentityToken("-", strings.NewReader(strings.Repeat("x", maxIdentityTokenBytes+1))); err == nil {
		t.Fatal("oversized token accepted")
	}
	if _, err := readIdentityToken(t.TempDir(), nil); err == nil {
		t.Fatal("directory accepted")
	}
	_, err := readIdentityToken(filepath.Join(t.TempDir(), "missing"), nil)
	if err == nil {
		t.Fatal("missing file accepted")
	}
}
