package aiplex_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestListToolAuthTypesDecodesCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/mcp/auth-types" {
			t.Error("unexpected request", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"types":[{"type":"api_key","title":"API key","description":"d","execution_credential_mode":"connection-auth","fields":[
			{"name":"in","type":"string","required":true,"secret_reference":false,"enum":["header","query"],"description":"where"},
			{"name":"secret_ref","type":"string","required":true,"secret_reference":true,"description":"ref"}]}],
			"default_type":"oauth_user","default_behaviour":"b",
			"secret_references":{"pattern":"^x$","namespace":"tenant/ns","note":"n"},
			"tools_policy":[{"name":"allow","type":"string[]","required":false,"secret_reference":false,"description":"a"}]}`))
	}))
	defer server.Close()
	catalog, err := aiplex.NewClient(server.URL).ListToolAuthTypes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Types) != 1 || catalog.DefaultType != "oauth_user" || catalog.SecretReferences.Namespace != "tenant/ns" || len(catalog.ToolsPolicy) != 1 {
		t.Fatalf("catalog not decoded: %+v", catalog)
	}
	fields := catalog.Types[0].Fields
	if len(fields) != 2 || !fields[1].SecretReference || fields[0].Enum[1] != "query" || catalog.Types[0].ExecutionCredentialMode != "connection-auth" {
		t.Fatalf("fields not decoded: %+v", fields)
	}
}

func TestUpdateToolConnectionPreservesUnknownFieldsAndUsesReadRevision(t *testing.T) {
	var put map[string]json.RawMessage
	var ifMatch string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mcp/connections/files" {
			t.Error("wrong path", r.URL.Path)
		}
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"tenant_id":"t","kind":"tool-connection","id":"files","revision":7,"spec":{"id":"files","resource_url":"https://files.example/mcp","network_profile_ref":"egress","client":{},
				"execution":{"id":"files","endpoint":"https://files.example/mcp","identity_ref":"operator","credential_mode":"operator-bearer","allowed_tools":["files.read"],"protocol_version":"2025-06-18","mode":"stateless-json"}}}`))
		case http.MethodPut:
			ifMatch = r.Header.Get("If-Match")
			if json.NewDecoder(r.Body).Decode(&put) != nil {
				t.Error("invalid body")
			}
			w.Write([]byte(`{"id":"files","revision":8,"spec":{"id":"files","auth":{"type":"api_key","in":"header","name":"X-Api-Key","secret_ref":"search-key"},"tools":{"allow":["files.*"],"deny":["files.delete"],"read_only":true}}}`))
		}
	}))
	defer server.Close()
	updated, err := aiplex.NewClient(server.URL).UpdateToolConnection(context.Background(), "files", aiplex.ToolConnectionChange{
		Auth:  &aiplex.ToolAuth{Type: aiplex.ToolAuthAPIKey, In: "header", Name: "X-Api-Key", SecretRef: "search-key"},
		Tools: &aiplex.ToolPolicy{Allow: []string{"files.*"}, Deny: []string{"files.delete"}, ReadOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ifMatch != "7" {
		t.Fatal("PUT must carry the revision that was read, got", ifMatch)
	}
	if updated.Revision != 8 || updated.Auth == nil || updated.Auth.SecretRef != "search-key" || updated.Tools == nil || !updated.Tools.ReadOnly {
		t.Fatalf("response not decoded: %+v", updated)
	}
	for _, key := range []string{"network_profile_ref", "resource_url", "client"} {
		if _, ok := put[key]; !ok {
			t.Errorf("unmodelled field %s dropped", key)
		}
	}
	var execution map[string]any
	if json.Unmarshal(put["execution"], &execution) != nil {
		t.Fatal("execution missing")
	}
	if execution["credential_mode"] != "connection-auth" || execution["identity_ref"] != "" || execution["protocol_version"] != "2025-06-18" {
		t.Fatalf("execution not aligned or fields lost: %v", execution)
	}
}

func TestUpdateToolConnectionSurfacesRevisionConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"id":"files","revision":3,"spec":{"id":"files","execution":{"identity_ref":"","credential_mode":"delegated-oauth"}}}`))
			return
		}
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"code":"REVISION_CONFLICT","message":"resource revision changed"}`))
	}))
	defer server.Close()
	_, err := aiplex.NewClient(server.URL).UpdateToolConnection(context.Background(), "files", aiplex.ToolConnectionChange{Tools: &aiplex.ToolPolicy{ReadOnly: true}})
	var apiErr *aiplex.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || apiErr.Code != "REVISION_CONFLICT" {
		t.Fatalf("conflict not surfaced: %v", err)
	}
}

func TestUpdateToolConnectionRejectsUnmanagedAndUnsafeChangesBeforeWriting(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			writes++
		}
		w.Write([]byte(`{"id":"bare","revision":1,"spec":{"id":"bare","resource_url":"https://x.example"}}`))
	}))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	ctx := context.Background()
	if _, err := client.UpdateToolConnection(ctx, "bare", aiplex.ToolConnectionChange{Auth: &aiplex.ToolAuth{Type: aiplex.ToolAuthBearer, SecretRef: "k"}}); err == nil {
		t.Fatal("non-oauth auth on a connection without managed execution accepted")
	}
	if _, err := client.UpdateToolConnection(ctx, "bare", aiplex.ToolConnectionChange{Tools: &aiplex.ToolPolicy{}}); err == nil {
		t.Fatal("tools policy without managed execution accepted")
	}
	if _, err := client.UpdateToolConnection(ctx, "bare", aiplex.ToolConnectionChange{Auth: &aiplex.ToolAuth{Type: "none"}, ClearAuth: true}); err == nil {
		t.Fatal("contradictory change accepted")
	}
	if _, err := client.UpdateToolConnection(ctx, "../other", aiplex.ToolConnectionChange{}); err == nil {
		t.Fatal("unsafe id accepted")
	}
	if writes != 0 {
		t.Fatal("rejected change reached the API")
	}
}

func TestValidToolSecretRef(t *testing.T) {
	for ref, want := range map[string]bool{"search-key": true, "a": true, "-x": false, "has space": false, "sk/live": false, "": false} {
		if aiplex.ValidToolSecretRef(ref) != want {
			t.Errorf("ValidToolSecretRef(%q) != %v", ref, want)
		}
	}
}
