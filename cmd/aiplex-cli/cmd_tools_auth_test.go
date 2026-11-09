package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = writer
	runErr := run()
	os.Stdout = old
	writer.Close()
	data, _ := io.ReadAll(reader)
	return string(data), runErr
}

func useToolsServer(t *testing.T, handler http.HandlerFunc, format string) {
	t.Helper()
	server := httptest.NewServer(handler)
	oldURL, oldToken, oldOutput := apiURL, token, output
	apiURL, token, output = server.URL, "session", format
	t.Cleanup(func() { server.Close(); apiURL, token, output = oldURL, oldToken, oldOutput })
}

const toolsCatalogBody = `{"types":[
 {"type":"api_key","title":"API key","description":"d","execution_credential_mode":"connection-auth","fields":[
   {"name":"in","type":"string","required":true,"secret_reference":false,"enum":["header","query"],"description":"x"},
   {"name":"secret_ref","type":"string","required":true,"secret_reference":true,"description":"y"}]},
 {"type":"oauth_user","title":"Delegated user OAuth","description":"d","execution_credential_mode":"delegated-oauth","fields":[]}],
 "default_type":"oauth_user","default_behaviour":"Default text.","secret_references":{"pattern":"p","namespace":"tenant/ns","note":"Reference names only."},
 "tools_policy":[{"name":"allow","type":"string[]","required":false,"secret_reference":false,"description":"Allowed tools."}]}`

func TestToolsAuthTypesTableAndJSON(t *testing.T) {
	useToolsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mcp/auth-types" {
			t.Error("wrong path", r.URL.Path)
		}
		w.Write([]byte(toolsCatalogBody))
	}, "table")
	out, err := captureStdout(t, func() error {
		cmd := toolsCmd()
		cmd.SetArgs([]string{"auth-types"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"api_key", "secret_ref* (ref)", "oauth_user (default)", "tenant/ns", "Reference names only.", "Allowed tools."} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
	output = "json"
	out, err = captureStdout(t, func() error {
		cmd := toolsCmd()
		cmd.SetArgs([]string{"auth-types"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Types       []map[string]any `json:"types"`
		DefaultType string           `json:"default_type"`
	}
	if json.Unmarshal([]byte(out), &decoded) != nil || len(decoded.Types) != 2 || decoded.DefaultType != "oauth_user" {
		t.Fatalf("json output invalid: %s", out)
	}
}

func TestToolsSetAuthReadsMergesAndWritesWithRevision(t *testing.T) {
	var put map[string]any
	var ifMatch string
	useToolsServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"id":"search","revision":4,"spec":{"id":"search","resource_url":"https://s.example/mcp","network_profile_ref":"egress",
				"tools":{"allow":["files.*"],"deny":["files.old"]},
				"execution":{"id":"search","endpoint":"https://s.example/mcp","identity_ref":"op","credential_mode":"operator-bearer","allowed_tools":["files.read"],"protocol_version":"2025-06-18","mode":"stateless-json"}}}`))
		case http.MethodPut:
			ifMatch = r.Header.Get("If-Match")
			json.NewDecoder(r.Body).Decode(&put)
			w.Write([]byte(`{"id":"search","revision":5,"spec":{"id":"search","auth":{"type":"api_key","in":"header","name":"X-Api-Key","secret_ref":"search-key"},"tools":{"allow":["files.*"],"deny":["files.delete"],"read_only":true}}}`))
		}
	}, "json")
	out, err := captureStdout(t, func() error {
		cmd := toolsCmd()
		cmd.SetArgs([]string{"set-auth", "search", "--auth-type", "api_key", "--auth-in", "header", "--auth-name", "X-Api-Key",
			"--secret-ref", "search-key", "--deny", "files.delete", "--read-only"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatal(err)
	}
	if ifMatch != "4" {
		t.Fatal("revision from the read not sent:", ifMatch)
	}
	auth := put["auth"].(map[string]any)
	if auth["type"] != "api_key" || auth["secret_ref"] != "search-key" || auth["in"] != "header" {
		t.Fatalf("auth block wrong: %v", auth)
	}
	tools := put["tools"].(map[string]any)
	if allow := tools["allow"].([]any); len(allow) != 1 || allow[0] != "files.*" {
		t.Fatalf("existing allow list not kept: %v", tools)
	}
	if deny := tools["deny"].([]any); len(deny) != 1 || deny[0] != "files.delete" || tools["read_only"] != true {
		t.Fatalf("deny/read_only not applied: %v", tools)
	}
	execution := put["execution"].(map[string]any)
	if execution["credential_mode"] != "connection-auth" || execution["identity_ref"] != "" {
		t.Fatalf("execution not aligned: %v", execution)
	}
	if put["network_profile_ref"] != "egress" {
		t.Fatal("unmodelled field dropped")
	}
	if !strings.Contains(out, `"revision": 5`) {
		t.Fatalf("result not printed: %s", out)
	}
}

func TestToolsSetAuthPolicyOnlyKeepsAuth(t *testing.T) {
	var put map[string]any
	useToolsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"id":"s","revision":2,"spec":{"id":"s","auth":{"type":"bearer","secret_ref":"tok"},"execution":{"id":"s","identity_ref":"","credential_mode":"connection-auth"}}}`))
			return
		}
		json.NewDecoder(r.Body).Decode(&put)
		w.Write([]byte(`{"id":"s","revision":3,"spec":{"id":"s"}}`))
	}, "json")
	_, err := captureStdout(t, func() error {
		cmd := toolsCmd()
		cmd.SetArgs([]string{"set-auth", "s", "--allow", "a", "--allow", "b*"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatal(err)
	}
	if put["auth"].(map[string]any)["secret_ref"] != "tok" {
		t.Fatal("auth block changed by a policy-only update")
	}
	if allow := put["tools"].(map[string]any)["allow"].([]any); len(allow) != 2 {
		t.Fatalf("allow list wrong: %v", allow)
	}
}

func TestToolsSetAuthRejectsSecretValuesAndBadFlagsBeforeAnyRequest(t *testing.T) {
	requests := 0
	useToolsServer(t, func(http.ResponseWriter, *http.Request) { requests++ }, "json")
	secret := "sk-live/abc 123"
	cases := [][]string{
		{"set-auth", "c", "--auth-type", "bearer", "--secret-ref", secret},
		{"set-auth", "c", "--auth-type", "oauth_client_credentials", "--client-secret-ref", secret},
		{"set-auth", "c", "--secret-ref", "ok"},
		{"set-auth", "c"},
		{"set-auth", "c", "--clear-auth", "--auth-type", "none"},
		{"set-auth", "c", "--clear-tools", "--deny", "x"},
	}
	for _, args := range cases {
		cmd := toolsCmd()
		cmd.SetArgs(args)
		var err error
		out, _ := captureStdout(t, func() error { err = cmd.Execute(); return err })
		if err == nil {
			t.Errorf("accepted %v", args)
			continue
		}
		if strings.Contains(err.Error(), "abc") || strings.Contains(out, "abc") {
			t.Errorf("secret echoed for %v: %v", args, err)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid input reached the API (%d requests)", requests)
	}
	for _, flag := range []string{"secret", "password", "token", "client-secret", "api-key"} {
		if toolsSetAuthCmd().Flags().Lookup(flag) != nil {
			t.Errorf("flag --%s would accept a secret value", flag)
		}
	}
}

func TestToolsSetAuthReportsRevisionConflict(t *testing.T) {
	useToolsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"id":"s","revision":2,"spec":{"id":"s","execution":{"id":"s","identity_ref":"","credential_mode":"delegated-oauth"}}}`))
			return
		}
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"code":"REVISION_CONFLICT","message":"resource revision changed"}`))
	}, "json")
	_, err := captureStdout(t, func() error {
		cmd := toolsCmd()
		cmd.SetArgs([]string{"set-auth", "s", "--read-only"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "REVISION_CONFLICT") {
		t.Fatalf("conflict not reported: %v", err)
	}
}
