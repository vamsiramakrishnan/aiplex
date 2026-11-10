package toolexec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

func TestToolPolicyPermitsAndValidate(t *testing.T) {
	policy := ToolPolicy{Allow: []string{"search", "files.read*"}, Deny: []string{"files.read_secret", "search"}}
	for name, want := range map[string]bool{"search": false, "files.read": true, "files.read_all": true, "files.read_secret": false, "files.write": false, "other": false} {
		if policy.Permits(name) != want {
			t.Errorf("%s: permits=%v", name, !want)
		}
	}
	if !(ToolPolicy{}).Permits("anything") || (ToolPolicy{Deny: []string{"*"}}).Permits("anything") {
		t.Fatal("empty allow or wildcard deny misapplied")
	}
	for _, bad := range []ToolPolicy{{Allow: []string{}}, {Deny: []string{"a*b"}}, {Allow: []string{"a", "a"}}, {Allow: []string{"bad name"}}, {Deny: []string{"**"}}} {
		if bad.Validate() == nil {
			t.Errorf("invalid policy accepted %+v", bad)
		}
	}
	if (ToolPolicy{Allow: []string{"*"}, Deny: []string{"admin.*"}, ReadOnly: true}).Validate() != nil {
		t.Fatal("valid policy rejected")
	}
	connection := Connection{ID: "tool", Endpoint: "https://tools.example/mcp", CredentialMode: ConnectionAuth, AllowedTools: []string{"search"}, ProtocolVersion: "2025-03-26", Mode: StatelessJSON}
	if connection.Validate() != nil {
		t.Fatal("connection-auth execution without identity_ref rejected")
	}
	connection.IdentityRef = "host"
	if connection.Validate() == nil {
		t.Fatal("connection-auth execution accepted an identity_ref")
	}
}

type headerCredential struct{ name, value string }

func (c headerCredential) Apply(r *http.Request) error { r.Header.Set(c.name, c.value); return nil }

func TestGatewayDenialIsJSONRPCAndPrecedesQuotaAndCredentials(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer upstream.Close()
	prepared, acquired := 0, 0
	opts := toolOptions(upstream, &recordingLease{})
	opts.TokenSource = nil
	opts.Credentials = func(context.Context) (Credential, error) { acquired++; return headerCredential{"X-Key", "k"}, nil }
	opts.Prepare = func(context.Context, Invocation) (execution.Lease, error) { prepared++; return &recordingLease{}, nil }
	opts.Authorize = func(context.Context, contracts.RequestContext, Connection, Request) error {
		return &Denial{Code: RPCForbidden, Reason: "connection_policy", Message: "tool search is not permitted"}
	}
	gateway, err := NewGateway(toolConnection(upstream), opts)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, toolRequest(toolBody))
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int               `json:"code"`
			Message string            `json:"message"`
			Data    map[string]string `json:"data"`
		} `json:"error"`
	}
	if w.Code != 403 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.JSONRPC != "2.0" || string(response.ID) != "7" || response.Error.Code != RPCForbidden || response.Error.Data["reason"] != "connection_policy" || !strings.Contains(response.Error.Message, "not permitted") {
		t.Fatalf("denial was not a JSON-RPC error preserving the id: %d %s", w.Code, w.Body.String())
	}
	if prepared != 0 || acquired != 0 || calls != 0 {
		t.Fatal("denial reached quota, credentials or upstream", prepared, acquired, calls)
	}
	opts.Authorize = func(context.Context, contracts.RequestContext, Connection, Request) error {
		return errors.New("internal detail")
	}
	gateway, _ = NewGateway(toolConnection(upstream), opts)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, toolRequest(`{"jsonrpc":"2.0","id":"abc","method":"tools/call","params":{"name":"search"}}`))
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"id":"abc"`) || strings.Contains(w.Body.String(), "internal detail") {
		t.Fatal("generic denial leaked detail or lost the id", w.Body.String())
	}
}

func TestGatewayCredentialSourceAndUnauthorizedHook(t *testing.T) {
	var key string
	status := 401
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("X-Key")
		if status == 401 {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", token="k-123"`)
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":"invalid token k-123"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, toolResult)
	}))
	defer upstream.Close()
	var rejected []Credential
	opts := toolOptions(upstream, &recordingLease{})
	opts.TokenSource = nil
	opts.Credentials = func(context.Context) (Credential, error) { return headerCredential{"X-Key", "k-123"}, nil }
	opts.Unauthorized = func(_ context.Context, c Credential) { rejected = append(rejected, c) }
	connection := toolConnection(upstream)
	connection.IdentityRef, connection.CredentialMode = "", ConnectionAuth
	gateway, err := NewGateway(connection, opts)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, toolRequest(toolBody))
	if w.Code != 502 || key != "k-123" || len(rejected) != 1 || strings.Contains(w.Body.String()+w.Header().Get("WWW-Authenticate"), "k-123") {
		t.Fatalf("401 handling: status=%d key=%q rejected=%d body=%q", w.Code, key, len(rejected), w.Body.String())
	}
	status = 200
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, toolRequest(toolBody))
	if w.Code != 200 || len(rejected) != 1 {
		t.Fatal("successful call invalidated credential", w.Code)
	}
}

func listOptions(seen *[]ListedTool, deny error) *ListOptions {
	return &ListOptions{
		Authorize: func(context.Context, contracts.RequestContext, Connection) error { return deny },
		Observe:   func(_ context.Context, _ contracts.RequestContext, tools []ListedTool) { *seen = tools },
		Visible: func(_ context.Context, _ contracts.RequestContext, tool ListedTool) bool {
			return tool.Name != "delete" && tool.ReadOnlyHint != nil && *tool.ReadOnlyHint
		},
	}
}

const listResult = `{"jsonrpc":"2.0","id":3,"result":{"tools":[{"name":"search","annotations":{"readOnlyHint":true}},{"name":"delete","annotations":{"readOnlyHint":true}},{"name":"write","annotations":{"readOnlyHint":false}},{"name":"plain"},{"name":"bad name"}],"nextCursor":"page-2"}}`

func TestGatewayListFiltersAndObservesAnnotations(t *testing.T) {
	for _, mode := range []string{StatelessJSON, StatelessStreamableHTTP} {
		t.Run(mode, func(t *testing.T) {
			var received string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				received = string(raw)
				if mode == StatelessStreamableHTTP {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\ndata: "+listResult+"\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, listResult)
			}))
			defer upstream.Close()
			var seen []ListedTool
			prepared := 0
			opts := toolOptions(upstream, &recordingLease{})
			opts.Prepare = func(context.Context, Invocation) (execution.Lease, error) { prepared++; return &recordingLease{}, nil }
			opts.List = listOptions(&seen, nil)
			connection := toolConnection(upstream)
			connection.Mode = mode
			gateway, _ := NewGateway(connection, opts)
			body := `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"cursor":"page-1"}}`
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, toolRequest(body))
			if w.Code != 200 || received != body || prepared != 0 {
				t.Fatalf("list dispatch: %d %s", w.Code, w.Body.String())
			}
			var response struct {
				ID     json.RawMessage `json:"id"`
				Result struct {
					Tools      []map[string]any `json:"tools"`
					NextCursor string           `json:"nextCursor"`
				} `json:"result"`
			}
			if json.Unmarshal(w.Body.Bytes(), &response) != nil || string(response.ID) != "3" || response.Result.NextCursor != "page-2" || len(response.Result.Tools) != 1 || response.Result.Tools[0]["name"] != "search" {
				t.Fatal("list was not filtered to callable tools", w.Body.String())
			}
			if len(seen) != 4 || seen[3].Name != "plain" || seen[3].ReadOnlyHint != nil || seen[2].ReadOnlyHint == nil || *seen[2].ReadOnlyHint {
				t.Fatal("annotations were not observed faithfully", seen)
			}
		})
	}
}

func TestGatewayListDenialAndDisabledList(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer upstream.Close()
	var seen []ListedTool
	opts := toolOptions(upstream, &recordingLease{})
	acquired := 0
	opts.TokenSource = TokenSourceFunc(func(context.Context) (string, error) { acquired++; return "t", nil })
	opts.List = listOptions(&seen, &Denial{Reason: "scope", Message: "caller may not call any tool"})
	gateway, _ := NewGateway(toolConnection(upstream), opts)
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, toolRequest(`{"jsonrpc":"2.0","id":"l","method":"tools/list"}`))
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"id":"l"`) || calls != 0 || acquired != 0 {
		t.Fatal("list denial was not a JSON-RPC error before credentials", w.Code, w.Body.String())
	}
	opts.List = nil
	gateway, _ = NewGateway(toolConnection(upstream), opts)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, toolRequest(`{"jsonrpc":"2.0","id":"l","method":"tools/list"}`))
	if w.Code != 400 || calls != 0 {
		t.Fatal("tools/list accepted without list governance")
	}
}
