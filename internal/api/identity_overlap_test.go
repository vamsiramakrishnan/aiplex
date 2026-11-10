package api

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/directory"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

// identityAPI mounts agent identity bindings and user identity providers
// together, cross-wired as in cmd/aiplex-api main.
type identityAPI struct {
	router http.Handler
	key    *rsa.PrivateKey
	jwks   string
	users  *UserIdentityProviderHandler
	agents *AgentFederationHandler
}

func newIdentityAPI(t *testing.T, keys userfederation.KeyResolver, dirs *auth.UserDirectories) *identityAPI {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	t.Cleanup(server.Close)
	roots := server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	jwksResolver := auth.NewJWKSKeyResolver(endpointpolicy.Config{AllowLoopback: true, TLSConfig: &tls.Config{RootCAs: roots}})
	if keys == nil {
		keys = auth.UserKeyResolver{Keys: jwksResolver}
	} else if fk, ok := keys.(*fixedKeys); ok {
		v, err := auth.NewJWKSVerifier(auth.JWKSConfig{Issuer: "x", Audience: "x", JWKSURL: server.URL + "/jwks", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		fk.v = v
	}
	agents := registry.NewMemoryStore()
	agent := models.Agent{ClientID: "tutor-agent", DisplayName: "Tutor", Status: "active", SpiffeID: "spiffe://aiplex.test/sa/tutor-agent"}
	if err := agents.PutAgent(context.Background(), &agent); err != nil {
		t.Fatal(err)
	}
	store := resources.NewMemoryStore()
	tenant := func(r *http.Request) (string, error) { return r.Header.Get("X-Test-Tenant"), nil }
	userEngine := &userfederation.Engine{Keys: keys}
	if dirs != nil {
		userEngine.Directory = dirs
	}
	users := &UserIdentityProviderHandler{Providers: userfederation.NewStore(store), Engine: userEngine, Tenant: tenant, PublicURL: "https://aiplex.test", Directories: dirs, SecretName: func(tenant, ref string) string { return "aiplex-tool-x-" + ref }}
	agentH := &AgentFederationHandler{Bindings: agentfederation.NewStore(store), Agents: agents, Engine: &agentfederation.Engine{Keys: jwksResolver, Agents: AgentLookup(agents)}, Tenant: tenant, DefaultAudience: "https://aiplex.test", PublicURL: "https://aiplex.test"}
	agentH.Providers, users.Bindings = users.Providers, agentH.Bindings
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			role := models.IAMRole(req.Header.Get("X-Test-Role"))
			identity := models.WIFIdentity{PrincipalID: "principal-operator", Subject: "operator"}
			ctx := context.WithValue(req.Context(), keyWIFIdentity, &identity)
			ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: identity, Roles: []models.IAMRole{role}})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/api/v1", func(r chi.Router) {
		agentH.Mount(r)
		users.Mount(r)
	})
	return &identityAPI{router: r, key: key, jwks: server.URL + "/jwks", users: users, agents: agentH}
}

type fixedKeys struct{ v *auth.JWKSVerifier }

func (k *fixedKeys) ForProvider(context.Context, userfederation.Provider) (userfederation.SignatureVerifier, error) {
	return k.v, nil
}

func (f *identityAPI) do(t *testing.T, method, path, role string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("X-Test-Role", role)
	r.Header.Set("X-Test-Tenant", "tenant-a")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

func (f *identityAPI) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type apiError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

func TestIdentityOverlapRejectedAtWriteUnlessAcknowledged(t *testing.T) {
	f := newIdentityAPI(t, nil, nil)
	provider := map[string]any{"preset": "oidc", "inputs": map[string]string{"issuer": "https://idp.test", "jwks_url": f.jwks, "audience": "https://aiplex.test", "groups_from": "claim", "group_claim": "groups"}}
	if w := f.do(t, "PUT", "/api/v1/identity/user-providers/corp", "admin", provider); w.Code != 201 {
		t.Fatalf("provider: %d %s", w.Code, w.Body)
	}
	binding := map[string]any{"preset": "oidc", "inputs": map[string]string{"issuer": "https://idp.test", "jwks_url": f.jwks, "subject": "svc-1", "audience": "https://aiplex.test"}}
	w := f.do(t, "PUT", "/api/v1/agents/tutor-agent/identities/svc", "admin", binding)
	var e apiError
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	conflict, _ := e.Details["conflict"].(map[string]any)
	if w.Code != 409 || e.Code != auth.CodeOverlapsUserProvider || !strings.Contains(e.Message, `user identity provider "corp"`) || conflict["kind"] != "user-identity-provider" || conflict["id"] != "corp" {
		t.Fatalf("overlapping binding: %d %s", w.Code, w.Body)
	}
	// A distinct audience is not an overlap.
	distinct := map[string]any{"preset": "oidc", "inputs": map[string]string{"issuer": "https://idp.test", "jwks_url": f.jwks, "subject": "svc-2", "audience": "https://agents.aiplex.test"}}
	if w := f.do(t, "PUT", "/api/v1/agents/tutor-agent/identities/svc2", "admin", distinct); w.Code != 201 || strings.Contains(w.Body.String(), "warnings") {
		t.Fatalf("distinct audience: %d %s", w.Code, w.Body)
	}
	// Acknowledged: saved with a warning.
	binding["acknowledge_overlap"] = true
	w = f.do(t, "PUT", "/api/v1/agents/tutor-agent/identities/svc", "admin", binding)
	var view BindingView
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &view) != nil || len(view.Warnings) != 1 || !strings.Contains(view.Warnings[0], "corp") {
		t.Fatalf("acknowledged binding: %d %s", w.Code, w.Body)
	}
	// The other direction: a second provider write that keeps the overlap must acknowledge too.
	provider["display_name"] = "renamed"
	r := httptest.NewRequest("PUT", "/api/v1/identity/user-providers/corp", strings.NewReader(mustJSON(provider)))
	r.Header.Set("X-Test-Role", "admin")
	r.Header.Set("X-Test-Tenant", "tenant-a")
	r.Header.Set("If-Match", "1")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, r)
	e = apiError{}
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 409 || e.Code != auth.CodeOverlapsAgentBinding || !strings.Contains(e.Message, `agent identity binding "tutor-agent/svc"`) {
		t.Fatalf("overlapping provider update: %d %s", rec.Code, rec.Body)
	}
	provider["acknowledge_overlap"] = true
	r = httptest.NewRequest("PUT", "/api/v1/identity/user-providers/corp", strings.NewReader(mustJSON(provider)))
	r.Header.Set("X-Test-Role", "admin")
	r.Header.Set("X-Test-Tenant", "tenant-a")
	r.Header.Set("If-Match", "1")
	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, r)
	var pv UserProviderView
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &pv) != nil || len(pv.Warnings) != 1 || !strings.Contains(pv.Warnings[0], "tutor-agent/svc") {
		t.Fatalf("acknowledged provider: %d %s", rec.Code, rec.Body)
	}
	// A fresh provider overlapping an existing binding is rejected too.
	other := map[string]any{"preset": "oidc", "inputs": map[string]string{"issuer": "https://idp.test", "jwks_url": f.jwks, "audience": "https://agents.aiplex.test", "groups_from": "claim", "group_claim": "groups"}}
	if w := f.do(t, "PUT", "/api/v1/identity/user-providers/agents-aud", "admin", other); w.Code != 409 || !strings.Contains(w.Body.String(), auth.CodeOverlapsAgentBinding) || !strings.Contains(w.Body.String(), "tutor-agent/svc2") {
		t.Fatalf("new overlapping provider: %d %s", w.Code, w.Body)
	}

	// Both dry-runs report a token claimed by both kinds.
	now := time.Now()
	token := f.token(t, map[string]any{"iss": "https://idp.test", "sub": "svc-1", "aud": "https://aiplex.test", "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(5 * time.Minute).Unix(), "groups": []string{"g"}})
	for path, want := range map[string]string{"/api/v1/agents/identities/verify": `user identity provider "corp"`, "/api/v1/identity/user-providers/verify": `agent identity binding "tutor-agent/svc"`} {
		w := f.do(t, "POST", path, "admin", map[string]any{"token": token})
		var out struct {
			Valid       bool                    `json:"valid"`
			Checks      []agentfederation.Check `json:"checks"`
			PrincipalID string                  `json:"principal_id"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		last := out.Checks[len(out.Checks)-1]
		if out.Valid || out.PrincipalID != "" || last.Name != auth.CheckFederationOverlap || last.Status != agentfederation.StatusFail || !strings.Contains(last.Reason, want) || strings.Contains(w.Body.String(), token) {
			t.Fatalf("%s: overlap not reported: %s", path, w.Body)
		}
	}
	// A user token of the same issuer is not ambiguous.
	userToken := f.token(t, map[string]any{"iss": "https://idp.test", "sub": "alice", "aud": "https://aiplex.test", "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(5 * time.Minute).Unix(), "groups": []string{"g"}})
	if w := f.do(t, "POST", "/api/v1/identity/user-providers/verify", "admin", map[string]any{"token": userToken}); !strings.Contains(w.Body.String(), `"valid":true`) || strings.Contains(w.Body.String(), auth.CheckFederationOverlap) {
		t.Fatalf("user token: %s", w.Body)
	}
}

const (
	apiEntraTID = "72f988bf-86f1-41af-91ab-2d7cd011db47"
	apiOID      = "00000000-0000-0000-0000-0000000000a1"
	apiGroup    = "aaaaaaaa-0000-0000-0000-000000000001"
)

func TestUserProviderDirectoryDryRunPlanAndInvalidate(t *testing.T) {
	var mu sync.Mutex
	graphCalls, fail := 0, 0
	dir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/token") {
			_, _ = io.WriteString(w, `{"token_type":"Bearer","expires_in":3600,"access_token":"graph-token"}`)
			return
		}
		graphCalls++
		if fail != 0 {
			w.WriteHeader(fail)
			_, _ = io.WriteString(w, `{"error":{"message":"tenant-internal-detail"}}`)
			return
		}
		fmt.Fprintf(w, `{"value":[{"@odata.type":"#microsoft.graph.group","id":"%s"}]}`, apiGroup)
	}))
	defer dir.Close()
	dirs := &auth.UserDirectories{Deps: directory.Deps{HTTP: dir.Client(), Secrets: staticSecrets{"graph-reader": "s3cr3t-value"}, Endpoints: directory.Endpoints{EntraLogin: dir.URL, Graph: dir.URL}}}
	f := newIdentityAPI(t, &fixedKeys{}, dirs)

	provider := map[string]any{"preset": "entra-id", "inputs": map[string]string{"tenant_id": apiEntraTID, "audience": "api://aiplex", "groups_from": "claim"},
		"directory": map[string]any{"kind": "entra-graph", "credential": map[string]string{"client_id": "99998888-aaaa-7777-bbbb-666655554444", "client_secret_ref": "graph-reader"}}}
	w := f.do(t, "PUT", "/api/v1/identity/user-providers/entra", "admin", provider)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"mode":"fallback"`) || !strings.Contains(w.Body.String(), `"client_claim":"azp"`) || strings.Contains(w.Body.String(), "s3cr3t") {
		t.Fatalf("provider: %d %s", w.Code, w.Body)
	}
	// A secret value instead of a reference is refused.
	bad := map[string]any{"preset": "entra-id", "inputs": map[string]string{"tenant_id": apiEntraTID, "audience": "api://other", "groups_from": "claim"},
		"directory": map[string]any{"kind": "entra-graph", "credential": map[string]string{"client_id": "99998888-aaaa-7777-bbbb-666655554444", "client_secret_ref": "Abc~123.secret=="}}}
	if w := f.do(t, "PUT", "/api/v1/identity/user-providers/bad", "admin", bad); w.Code != 400 || strings.Contains(w.Body.String(), "Abc~123") {
		t.Fatalf("secret value accepted: %d %s", w.Code, w.Body)
	}

	now := time.Now()
	token := f.token(t, map[string]any{"iss": "https://login.microsoftonline.com/" + apiEntraTID + "/v2.0", "aud": "api://aiplex", "tid": apiEntraTID, "oid": apiOID, "azp": "client-app-1", "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(), "hasgroups": true})
	type verifyOut struct {
		Valid        bool                   `json:"valid"`
		Checks       []userfederation.Check `json:"checks"`
		GroupsSource string                 `json:"groups_source"`
		Groups       *[]string              `json:"groups"`
		ClientID     string                 `json:"client_id"`
	}
	verify := func() verifyOut {
		w := f.do(t, "POST", "/api/v1/identity/user-providers/verify", "deployer", map[string]any{"token": token})
		var out verifyOut
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("verify: %d %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "tenant-internal-detail") || strings.Contains(w.Body.String(), "s3cr3t") || strings.Contains(w.Body.String(), "graph-token") {
			t.Fatalf("dry-run leaks directory detail or credentials: %s", w.Body)
		}
		return out
	}
	out := verify()
	if !out.Valid || out.GroupsSource != "directory:entra-graph" || out.Groups == nil || strings.Join(*out.Groups, ",") != apiGroup || out.ClientID != "client-app-1" {
		t.Fatalf("directory dry-run: %+v", out)
	}
	// Dry-runs share the cache; invalidation forces a new lookup, and a failing
	// directory then reports why groups are unavailable.
	verify()
	mu.Lock()
	calls := graphCalls
	fail = 403
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("graph calls = %d, want 1 (cached)", calls)
	}
	if w := f.do(t, "POST", "/api/v1/identity/user-providers/entra/membership/invalidate", "deployer", map[string]string{"subject": apiOID}); w.Code != 403 {
		t.Fatalf("invalidate requires admin: %d", w.Code)
	}
	w = f.do(t, "POST", "/api/v1/identity/user-providers/entra/membership/invalidate", "admin", map[string]string{"subject": apiOID})
	var inv InvalidateResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &inv) != nil || inv.PrincipalID != auth.CanonicalPrincipalID("https://login.microsoftonline.com/"+apiEntraTID+"/v2.0", auth.UserFederationPoolID("tenant-a"), apiOID) {
		t.Fatalf("invalidate: %d %s", w.Code, w.Body)
	}
	out = verify()
	groupsCheck := userfederation.Check{}
	for _, c := range out.Checks {
		if c.Name == userfederation.CheckGroups {
			groupsCheck = c
		}
	}
	if !out.Valid || out.GroupsSource != "unavailable" || out.Groups != nil || !strings.Contains(groupsCheck.Reason, "denied the lookup (HTTP 403)") {
		t.Fatalf("failing directory dry-run: %+v", out)
	}
	if w := f.do(t, "POST", "/api/v1/identity/user-providers/entra/membership/invalidate", "admin", map[string]string{}); w.Code != 400 {
		t.Fatalf("empty subject: %d", w.Code)
	}
	// Plan includes the directory-side steps.
	w = f.do(t, "POST", "/api/v1/identity/user-providers/entra/plan", "deployer", nil)
	for _, want := range []string{"User.Read.All", "grant admin consent", "gcloud secrets create 'aiplex-tool-x-graph-reader' --data-file=-", "membership/invalidate", `"directory":{"kind":"entra-graph","mode":"fallback"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("plan lacks %q: %s", want, w.Body)
		}
	}
	// A provider without a directory has nothing to invalidate.
	plain := map[string]any{"preset": "oidc", "inputs": map[string]string{"issuer": "https://idp.test", "jwks_url": f.jwks, "audience": "a", "groups_from": "claim", "group_claim": "groups"}}
	if w := f.do(t, "PUT", "/api/v1/identity/user-providers/plain", "admin", plain); w.Code != 201 {
		t.Fatal(w.Body)
	}
	if w := f.do(t, "POST", "/api/v1/identity/user-providers/plain/membership/invalidate", "admin", map[string]string{"subject": "x"}); w.Code != 409 || !strings.Contains(w.Body.String(), "DIRECTORY_NOT_CONFIGURED") {
		t.Fatalf("plain invalidate: %d %s", w.Code, w.Body)
	}
}

type staticSecrets map[string]string

func (s staticSecrets) Resolve(_ context.Context, _ string, ref string) (string, error) {
	if v, ok := s[ref]; ok {
		return v, nil
	}
	return "", fmt.Errorf("missing")
}
