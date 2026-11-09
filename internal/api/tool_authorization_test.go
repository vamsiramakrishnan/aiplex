package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

type revokeOnReadVault struct {
	vault  *toolauth.MemorySessionVault
	onRead func()
}

func (v *revokeOnReadVault) Seal(ctx context.Context, b string, s toolauth.SessionSecrets) (string, error) {
	return v.vault.Seal(ctx, b, s)
}
func (v *revokeOnReadVault) Open(ctx context.Context, b, ref string) (toolauth.SessionSecrets, error) {
	s, e := v.vault.Open(ctx, b, ref)
	if e == nil && s.AccessToken != "" && v.onRead != nil {
		callback := v.onRead
		v.onRead = nil
		callback()
	}
	return s, e
}
func (v *revokeOnReadVault) Destroy(ctx context.Context, ref string) error {
	return v.vault.Destroy(ctx, ref)
}

func TestDelegatedToolAuthorizationFeedsQuotaManagedExecution(t *testing.T) {
	for _, test := range []struct {
		name                      string
		revokeBeforeClaim, stream bool
	}{{name: "JSON"}, {name: "SSE", stream: true}, {name: "revoked after token read", revokeBeforeClaim: true}} {
		t.Run(test.name, func(t *testing.T) {
			var origin string
			var tokens, calls atomic.Int32
			nativeResponse := managedToolResponse
			if test.stream {
				nativeResponse = "event: message\ndata: " + managedToolResponse + "\n\n"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/mcp":
					fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q]}`, origin+"/mcp", origin)
				case "/.well-known/oauth-authorization-server":
					fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"code_challenge_methods_supported":["S256"],"authorization_response_iss_parameter_supported":true}`, origin, origin+"/authorize", origin+"/token")
				case "/token":
					tokens.Add(1)
					_ = r.ParseForm()
					if r.Form.Get("resource") != origin+"/mcp" || r.Form.Get("code_verifier") == "" {
						t.Error("OAuth resource/PKCE binding missing")
					}
					fmt.Fprint(w, `{"access_token":"private-delegated-token","token_type":"Bearer","expires_in":3600,"scope":"read"}`)
				case "/mcp":
					calls.Add(1)
					if r.Header.Get("Authorization") != "Bearer private-delegated-token" {
						t.Error("OAuth token did not replace caller/operator credentials")
					}
					if test.stream {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					fmt.Fprint(w, nativeResponse)
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			origin = server.URL
			h, quota, request := protectedToolFixture(t, origin+"/mcp")
			// No operator credential is configured. Runtime must use the owned OAuth grant.
			connection := managedToolConnection(origin + "/mcp")
			connection.Execution.CredentialMode = toolexec.DelegatedOAuth
			connection.Execution.IdentityRef = ""
			connection.Issuer = origin
			connection.Client = toolauth.ClientProfile{ID: "profile", Name: "Tool", RedirectURIs: []string{"http://127.0.0.1:4321/callback"}, ApplicationType: "native", Scopes: []string{"read"}}
			if test.stream {
				connection.Execution.Mode = toolexec.StatelessStreamableHTTP
			}
			raw, _ := json.Marshal(connection)
			saved, e := h.Store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "tool-connection", ID: "surface", Spec: raw}, 1)
			if e != nil {
				t.Fatal(e)
			}
			h.Tools, e = toolauth.NewService(toolauth.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{origin}, AllowLoopback: true}, Credentials: toolauth.NewMemoryCredentials(), PreRegistered: []toolauth.Registration{{TenantID: "tenant", Issuer: origin, ProfileID: "profile", ClientID: "client", RedirectURIs: connection.Client.RedirectURIs}}})
			if e != nil {
				t.Fatal(e)
			}
			vault := &revokeOnReadVault{vault: toolauth.NewMemorySessionVault()}
			h.ToolSessions, e = toolauth.NewRuntime(toolauth.RuntimeConfig{Registration: h.Tools, Store: h.Store, Vault: vault})
			if e != nil {
				t.Fatal(e)
			}
			authRequest := func(body string) *http.Request {
				r := request("authorize")
				r.Body = io.NopCloser(strings.NewReader(body))
				r.ContentLength = int64(len(body))
				return r
			}
			w := httptest.NewRecorder()
			h.StartToolAuthorization(w, authRequest(`{"redirect_uri":"https://unregistered.example/callback"}`))
			if w.Code != 409 || tokens.Load() != 0 {
				t.Fatal("unregistered redirect accepted", w.Code, w.Body.String())
			}
			originalAccess := h.ResolveAccess
			h.ResolveAccess = func(ctx context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
				access, err := originalAccess(ctx, identity)
				if err == nil {
					access.Scopes = nil
				}
				return access, err
			}
			w = httptest.NewRecorder()
			h.StartToolAuthorization(w, authRequest(`{"redirect_uri":"http://127.0.0.1:4321/callback"}`))
			if w.Code != 403 || tokens.Load() != 0 {
				t.Fatal("missing grant started OAuth", w.Code, w.Body.String())
			}
			h.ResolveAccess = originalAccess
			w = httptest.NewRecorder()
			h.StartToolAuthorization(w, authRequest(`{"redirect_uri":"http://127.0.0.1:4321/callback"}`))
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var challenge toolauth.AuthorizationChallenge
			if json.Unmarshal(w.Body.Bytes(), &challenge) != nil {
				t.Fatal(w.Body.String())
			}
			u, _ := url.Parse(challenge.AuthorizationURL)
			body, _ := json.Marshal(toolauth.AuthorizationResponse{FlowID: challenge.FlowID, State: u.Query().Get("state"), Code: "private-code", Issuer: origin})
			w = httptest.NewRecorder()
			h.CompleteToolAuthorization(w, authRequest(string(body)))
			if w.Code != 200 || tokens.Load() != 1 || strings.Contains(w.Body.String(), "private-delegated-token") {
				t.Fatal(w.Code, w.Body.String(), tokens.Load())
			}
			r := request("tool-call")
			key, e := h.toolSessionKey(r.Context(), "tenant", saved, connection)
			if e != nil {
				t.Fatal(e)
			}
			if test.revokeBeforeClaim {
				vault.onRead = func() {
					if e := h.ToolSessions.Revoke(context.Background(), key); e != nil {
						t.Error(e)
					}
				}
			}
			w = httptest.NewRecorder()
			h.NativeTool(w, r)
			if test.revokeBeforeClaim {
				if w.Code != 409 || calls.Load() != 0 {
					t.Fatal("revoked OAuth grant reached provider", w.Code, calls.Load(), w.Body.String())
				}
				attempt, e := quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get("X-Aiplex-Attempt-ID"))
				if e != nil || attempt.Decision.State != "aborted" {
					t.Fatal("undispatched OAuth hold was not released", attempt, e)
				}
			} else {
				if w.Code != 200 || calls.Load() != 1 || w.Body.String() != nativeResponse {
					t.Fatal("delegated managed tool failed", w.Code, calls.Load(), w.Body.String())
				}
				attempt, e := quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get("X-Aiplex-Attempt-ID"))
				if e != nil || attempt.Decision.State != "settled" || attempt.Actual["calls"] != 1 {
					t.Fatal("delegated tool usage was not settled", attempt, e)
				}
				w = httptest.NewRecorder()
				h.RevokeToolAuthorization(w, authRequest(""))
				if w.Code != 204 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			w = httptest.NewRecorder()
			h.GetToolAuthorization(w, authRequest(""))
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"revoked"`) {
				t.Fatal("revocation status unavailable", w.Code, w.Body.String())
			}
			before := calls.Load()
			w = httptest.NewRecorder()
			h.NativeTool(w, request("after-revocation"))
			if w.Code == 200 || calls.Load() != before {
				t.Fatal("revoked grant reused")
			}
			for _, kind := range []string{"tool-oauth-session", "tool-oauth-flow"} {
				records, e := h.Store.List(context.Background(), "tenant", kind)
				if e != nil {
					t.Fatal(e)
				}
				for _, record := range records {
					if strings.Contains(string(record.Spec), "private-code") || strings.Contains(string(record.Spec), "private-delegated-token") {
						t.Fatal("OAuth secret in resource JSON")
					}
				}
			}
		})
	}
}
