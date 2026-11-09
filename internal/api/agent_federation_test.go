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
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type federationAPI struct {
	router http.Handler
	key    *rsa.PrivateKey
	jwks   string
	agents *registry.MemoryStore
}

func newFederationAPI(t *testing.T) *federationAPI {
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
	agents := registry.NewMemoryStore()
	for _, a := range []models.Agent{{ClientID: "tutor-agent", DisplayName: "Tutor", Status: "active", SpiffeID: "spiffe://aiplex.test/sa/tutor-agent"}, {ClientID: "research-agent", DisplayName: "Research", Status: "active"}} {
		agent := a
		if err := agents.PutAgent(context.Background(), &agent); err != nil {
			t.Fatal(err)
		}
	}
	h := &AgentFederationHandler{
		Bindings: agentfederation.NewStore(resources.NewMemoryStore()),
		Agents:   agents,
		Engine:   &agentfederation.Engine{Keys: auth.NewJWKSKeyResolver(endpointpolicy.Config{AllowLoopback: true, TLSConfig: &tls.Config{RootCAs: roots}}), Agents: AgentLookup(agents)},
		Tenant: func(r *http.Request) (string, error) {
			return r.Header.Get("X-Test-Tenant"), nil
		},
		DefaultAudience: "https://aiplex.test",
		PublicURL:       "https://aiplex.test",
	}
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
		agentH := NewAgentHandler(agents)
		r.Get("/agents/{clientId}", agentH.Get)
		h.Mount(r)
	})
	return &federationAPI{router: r, key: key, jwks: server.URL + "/jwks", agents: agents}
}

func (f *federationAPI) do(t *testing.T, method, path, role, tenant, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if s, ok := body.(string); ok {
		reader = bytes.NewReader([]byte(s))
	} else {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, path, reader)
	r.Header.Set("X-Test-Role", role)
	r.Header.Set("X-Test-Tenant", tenant)
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

func (f *federationAPI) binding(subject string) map[string]any {
	return map[string]any{"preset": "oidc", "display_name": "External worker", "inputs": map[string]string{"issuer": "https://issuer.test", "jwks_url": f.jwks, "subject": subject}, "required_claims": map[string]string{"team": "research"}}
}

func (f *federationAPI) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestAgentIdentityPresetsCatalog(t *testing.T) {
	f := newFederationAPI(t)
	w := f.do(t, "GET", "/api/v1/agents/identity-presets", "viewer", "tenant-a", "", nil)
	var body struct {
		Presets         []agentfederation.Preset `json:"presets"`
		DefaultAudience string                   `json:"default_audience"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Presets) != 9 || body.DefaultAudience != "https://aiplex.test" {
		t.Fatalf("presets: %d %s", w.Code, w.Body)
	}
}

func TestAgentIdentityBindingLifecycle(t *testing.T) {
	f := newFederationAPI(t)
	path := "/api/v1/agents/tutor-agent/identities/worker"

	if w := f.do(t, "PUT", path, string(models.RoleDeployer), "tenant-a", "", f.binding("workload-7")); w.Code != 403 {
		t.Fatalf("deployer write = %d %s", w.Code, w.Body)
	}
	w := f.do(t, "PUT", path, string(models.RoleAdmin), "tenant-a", "", f.binding("workload-7"))
	if w.Code != 201 || w.Header().Get("ETag") != `"1"` {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var created BindingView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	pool := auth.FederationPoolID("tenant-a")
	if created.Issuer != "https://issuer.test" || created.Audience != "https://aiplex.test" || created.Subject != "workload-7" || created.RequiredClaims["team"] != "research" ||
		created.PrincipalID != auth.CanonicalPrincipalID("https://issuer.test", pool, "workload-7") || created.ActorID != auth.CanonicalPrincipalID("https://issuer.test", pool, "spiffe://aiplex.test/sa/tutor-agent") {
		t.Fatalf("created = %+v", created)
	}
	// Create without If-Match on an existing binding conflicts.
	if w := f.do(t, "PUT", path, "admin", "tenant-a", "", f.binding("workload-7")); w.Code != 409 {
		t.Fatalf("re-create = %d", w.Code)
	}
	// Same subject bound to another agent: rejected with the owner named.
	w = f.do(t, "PUT", "/api/v1/agents/research-agent/identities/worker", "admin", "tenant-a", "", f.binding("workload-7"))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "SUBJECT_ALREADY_BOUND") || !strings.Contains(w.Body.String(), "tutor-agent") {
		t.Fatalf("duplicate subject = %d %s", w.Code, w.Body)
	}
	// Another tenant may bind the same subject independently and cannot see tenant-a's binding.
	if w := f.do(t, "GET", path, "admin", "tenant-b", "", nil); w.Code != 404 {
		t.Fatalf("cross-tenant read = %d", w.Code)
	}
	if w := f.do(t, "PUT", "/api/v1/agents/research-agent/identities/worker", "admin", "tenant-b", "", f.binding("workload-7")); w.Code != 201 {
		t.Fatalf("tenant-b create = %d %s", w.Code, w.Body)
	}
	// Agent must exist; inputs are validated strictly.
	if w := f.do(t, "PUT", "/api/v1/agents/ghost/identities/x", "admin", "tenant-a", "", f.binding("workload-9")); w.Code != 404 || !strings.Contains(w.Body.String(), "AGENT_NOT_FOUND") {
		t.Fatalf("unknown agent = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "PUT", "/api/v1/agents/tutor-agent/identities/wild", "admin", "tenant-a", "", f.binding("workload-*")); w.Code != 400 {
		t.Fatalf("wildcard subject = %d", w.Code)
	}
	if w := f.do(t, "PUT", "/api/v1/agents/tutor-agent/identities/Bad_ID", "admin", "tenant-a", "", f.binding("workload-10")); w.Code != 400 {
		t.Fatalf("invalid id = %d", w.Code)
	}
	if w := f.do(t, "PUT", "/api/v1/agents/tutor-agent/identities/extra", "admin", "tenant-a", "", `{"preset":"oidc","inputs":{},"issuer":"https://x"}`); w.Code != 400 {
		t.Fatalf("derived fields must not be accepted as input = %d", w.Code)
	}
	// Reads.
	if w := f.do(t, "GET", "/api/v1/agents/tutor-agent/identities", "deployer", "tenant-a", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"worker"`) {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "GET", path, "viewer", "tenant-a", "", nil); w.Code != 403 {
		t.Fatalf("viewer read = %d", w.Code)
	}
	// Plan.
	w = f.do(t, "POST", path+"/plan", "admin", "tenant-a", "", nil)
	var plan agentfederation.Plan
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || len(plan.Steps) == 0 || len(plan.AIPlex) < 2 || plan.Binding.ClientID != "tutor-agent" {
		t.Fatalf("plan = %d %s", w.Code, w.Body)
	}
	// Update and delete with revisions.
	if w := f.do(t, "PUT", path, "admin", "tenant-a", `"1"`, f.binding("workload-8")); w.Code != 200 || w.Header().Get("ETag") != `"2"` {
		t.Fatalf("update = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "DELETE", path, "admin", "tenant-a", "", nil); w.Code != 428 {
		t.Fatalf("delete without If-Match = %d", w.Code)
	}
	if w := f.do(t, "DELETE", path, "admin", "tenant-a", `"1"`, nil); w.Code != 409 {
		t.Fatalf("stale delete = %d", w.Code)
	}
	if w := f.do(t, "DELETE", path, "admin", "tenant-a", `"2"`, nil); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "GET", path, "admin", "tenant-a", "", nil); w.Code != 404 {
		t.Fatalf("deleted read = %d", w.Code)
	}
}

func TestAgentIdentityVerifyNeverExposesToken(t *testing.T) {
	f := newFederationAPI(t)
	if w := f.do(t, "PUT", "/api/v1/agents/tutor-agent/identities/worker", "admin", "tenant-a", "", f.binding("workload-7")); w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var logs bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&logs)
	defer func() { log.Logger = previous }()

	now := time.Now()
	good := f.token(t, map[string]any{"iss": "https://issuer.test", "sub": "workload-7", "aud": "https://aiplex.test", "exp": now.Add(time.Minute).Unix(), "iat": now.Unix(), "team": "research", "private_note": "classified-claim-value"})
	bad := f.token(t, map[string]any{"iss": "https://issuer.test", "sub": "workload-7", "aud": "https://aiplex.test", "exp": now.Add(time.Minute).Unix(), "team": "platform-secret-team", "private_note": "classified-claim-value"})

	for _, tc := range []struct {
		name  string
		body  map[string]any
		valid bool
		fail  string
	}{
		{"lookup", map[string]any{"token": good}, true, ""},
		{"pinned", map[string]any{"token": good, "client_id": "tutor-agent", "binding_id": "worker"}, true, ""},
		{"claims mismatch", map[string]any{"token": bad}, false, agentfederation.CheckRequiredClaims},
	} {
		r := httptest.NewRequest("POST", "/api/v1/agents/identities/verify", strings.NewReader(mustJSON(tc.body)))
		r.Header.Set("X-Test-Role", "admin")
		r.Header.Set("X-Test-Tenant", "tenant-a")
		r = r.WithContext(log.Logger.WithContext(r.Context()))
		w := httptest.NewRecorder()
		Logger(f.router).ServeHTTP(w, r)
		var resp struct {
			Valid       bool                    `json:"valid"`
			Checks      []agentfederation.Check `json:"checks"`
			PrincipalID string                  `json:"principal_id"`
			ActorID     string                  `json:"actor_id"`
			Subject     string                  `json:"subject"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &resp) != nil || resp.Valid != tc.valid || len(resp.Checks) != 9 {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
		}
		if tc.valid && (resp.PrincipalID == "" || resp.ActorID == "" || resp.Subject != "workload-7") {
			t.Fatalf("%s: identities missing: %s", tc.name, w.Body)
		}
		if !tc.valid {
			failed := false
			for _, c := range resp.Checks {
				failed = failed || (c.Name == tc.fail && c.Status == agentfederation.StatusFail)
			}
			if !failed {
				t.Fatalf("%s: expected %s to fail: %s", tc.name, tc.fail, w.Body)
			}
		}
		out := w.Body.String() + logs.String()
		for _, secret := range []string{good, bad, strings.Split(good, ".")[1], strings.Split(good, ".")[2], "classified-claim-value", "platform-secret-team"} {
			if strings.Contains(out, secret) {
				t.Fatalf("%s: response or logs leak %q", tc.name, secret[:12])
			}
		}
	}
	// Malformed bodies do not echo their content.
	r := httptest.NewRequest("POST", "/api/v1/agents/identities/verify", strings.NewReader(`{"token":"`+good+`","extra":1}`))
	r.Header.Set("X-Test-Role", "admin")
	r.Header.Set("X-Test-Tenant", "tenant-a")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	if w.Code != 400 || strings.Contains(w.Body.String(), good) {
		t.Fatalf("malformed verify = %d %s", w.Code, w.Body)
	}
	// Viewers cannot dry-run.
	if w := f.do(t, "POST", "/api/v1/agents/identities/verify", "viewer", "tenant-a", "", map[string]any{"token": good}); w.Code != 403 {
		t.Fatalf("viewer verify = %d", w.Code)
	}
	// Tenant isolation: tenant-b has no binding for this subject.
	w = f.do(t, "POST", "/api/v1/agents/identities/verify", "admin", "tenant-b", "", map[string]any{"token": good})
	if w.Code != 200 || strings.Contains(w.Body.String(), `"valid":true`) {
		t.Fatalf("tenant-b verify = %d %s", w.Code, w.Body)
	}
}
