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
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

type userProviderAPI struct {
	router http.Handler
	key    *rsa.PrivateKey
	jwks   string
}

func newUserProviderAPI(t *testing.T) *userProviderAPI {
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
	h := &UserIdentityProviderHandler{
		Providers: userfederation.NewStore(resources.NewMemoryStore()),
		Engine:    &userfederation.Engine{Keys: auth.UserKeyResolver{Keys: auth.NewJWKSKeyResolver(endpointpolicy.Config{AllowLoopback: true, TLSConfig: &tls.Config{RootCAs: roots}})}},
		Tenant:    func(r *http.Request) (string, error) { return r.Header.Get("X-Test-Tenant"), nil },
		PublicURL: "https://aiplex.test",
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
	r.Route("/api/v1", func(r chi.Router) { h.Mount(r) })
	return &userProviderAPI{router: r, key: key, jwks: server.URL + "/jwks"}
}

func (f *userProviderAPI) do(t *testing.T, method, path, role, tenant, ifMatch string, body any) *httptest.ResponseRecorder {
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

func (f *userProviderAPI) provider(audience string) map[string]any {
	return map[string]any{"preset": "oidc", "display_name": "Corporate SSO", "inputs": map[string]string{"issuer": "https://idp.test", "jwks_url": f.jwks, "audience": audience, "groups_from": "claim", "group_claim": "groups"}, "required_claims": map[string]string{"org": "acme"}}
}

func (f *userProviderAPI) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestUserProviderPresetsCatalog(t *testing.T) {
	f := newUserProviderAPI(t)
	// The catalog is readable by any authenticated role.
	for _, role := range []string{"viewer", "deployer", "admin"} {
		w := f.do(t, "GET", "/api/v1/identity/user-provider-presets", role, "tenant-a", "", nil)
		var body struct {
			Presets []userfederation.Preset `json:"presets"`
			Live    bool                    `json:"live_authentication"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Presets) != 5 || body.Live {
			t.Fatalf("%s presets: %d %s", role, w.Code, w.Body)
		}
	}
}

func TestUserProviderRoleMatrix(t *testing.T) {
	f := newUserProviderAPI(t)
	path := "/api/v1/identity/user-providers/corp"
	// Only admins write.
	for _, role := range []string{"viewer", "deployer", ""} {
		if w := f.do(t, "PUT", path, role, "tenant-a", "", f.provider("a")); w.Code != 403 {
			t.Fatalf("%q PUT = %d", role, w.Code)
		}
	}
	if w := f.do(t, "PUT", path, "admin", "", "", f.provider("a")); w.Code != 403 || !strings.Contains(w.Body.String(), "TENANT_REQUIRED") {
		t.Fatalf("no tenant PUT = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "PUT", path, "admin", "tenant-a", "", f.provider("a")); w.Code != 201 || w.Header().Get("ETag") != `"1"` {
		t.Fatalf("admin PUT = %d %s", w.Code, w.Body)
	}
	matrix := []struct {
		method, path string
		body         any
		viewer       int
		deployer     int
		admin        int
	}{
		{"GET", "/api/v1/identity/user-providers", nil, 403, 200, 200},
		{"GET", path, nil, 403, 200, 200},
		{"POST", path + "/plan", nil, 403, 200, 200},
		{"POST", "/api/v1/identity/user-providers/verify", map[string]any{"token": "x.y.z"}, 403, 200, 200},
		{"DELETE", path, nil, 403, 403, 428},
	}
	for _, m := range matrix {
		for role, want := range map[string]int{"viewer": m.viewer, "deployer": m.deployer, "admin": m.admin} {
			if w := f.do(t, m.method, m.path, role, "tenant-a", "", m.body); w.Code != want {
				t.Fatalf("%s %s as %s = %d want %d: %s", m.method, m.path, role, w.Code, want, w.Body)
			}
		}
	}
}

func TestUserProviderLifecycle(t *testing.T) {
	f := newUserProviderAPI(t)
	path := "/api/v1/identity/user-providers/corp"
	w := f.do(t, "PUT", path, "admin", "tenant-a", "", f.provider("aiplex-users"))
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var created struct {
		ID            string `json:"id"`
		Issuer        string `json:"issuer"`
		Audience      string `json:"audience"`
		GroupsFrom    string `json:"groups_from"`
		PrincipalPool string `json:"principal_pool"`
		Revision      uint64 `json:"revision"`
	}
	if json.Unmarshal(w.Body.Bytes(), &created) != nil || created.Issuer != "https://idp.test" || created.GroupsFrom != "claim" || created.PrincipalPool != auth.UserFederationPoolID("tenant-a") || created.Revision != 1 {
		t.Fatalf("created = %s", w.Body)
	}
	// Creating over an existing provider conflicts; stale revisions conflict.
	if w := f.do(t, "PUT", path, "admin", "tenant-a", "", f.provider("aiplex-users")); w.Code != 409 {
		t.Fatalf("recreate = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "PUT", path, "admin", "tenant-a", `"9"`, f.provider("aiplex-users")); w.Code != 409 || !strings.Contains(w.Body.String(), "REVISION_CONFLICT") {
		t.Fatalf("stale = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "PUT", path, "admin", "tenant-a", `"1"`, f.provider("aiplex-users")); w.Code != 200 || w.Header().Get("ETag") != `"2"` {
		t.Fatalf("update = %d %s", w.Code, w.Body)
	}
	// Duplicate issuer+audience under another ID.
	if w := f.do(t, "PUT", "/api/v1/identity/user-providers/corp-copy", "admin", "tenant-a", "", f.provider("aiplex-users")); w.Code != 409 || !strings.Contains(w.Body.String(), "SOURCE_ALREADY_CONFIGURED") || !strings.Contains(w.Body.String(), `\"corp\"`) {
		t.Fatalf("duplicate = %d %s", w.Code, w.Body)
	}
	// Another tenant is independent and cannot see this one.
	if w := f.do(t, "PUT", "/api/v1/identity/user-providers/corp-copy", "admin", "tenant-b", "", f.provider("aiplex-users")); w.Code != 201 {
		t.Fatalf("tenant-b create = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "GET", "/api/v1/identity/user-providers", "admin", "tenant-c", "", nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("tenant-c list = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "GET", path, "admin", "tenant-b", "", nil); w.Code != 404 {
		t.Fatalf("cross-tenant read = %d", w.Code)
	}
	// Invalid requests.
	for name, body := range map[string]any{
		"unknown preset":   map[string]any{"preset": "nope"},
		"unknown field":    `{"preset":"oidc","inputs":{},"extra":1}`,
		"http issuer":      map[string]any{"preset": "oidc", "inputs": map[string]string{"issuer": "http://idp.test", "jwks_url": f.jwks, "audience": "a", "groups_from": "membership-authority"}},
		"missing groups":   map[string]any{"preset": "oidc", "inputs": map[string]string{"issuer": "https://idp2.test", "jwks_url": f.jwks, "audience": "a"}},
		"wildcard aud":     map[string]any{"preset": "oidc", "inputs": map[string]string{"issuer": "https://idp2.test", "jwks_url": f.jwks, "audience": "*", "groups_from": "membership-authority"}},
		"reserved id":      nil,
		"bad id uppercase": nil,
	} {
		id := "bad"
		if name == "reserved id" {
			id, body = "verify", f.provider("zzz")
		}
		if name == "bad id uppercase" {
			id, body = "Bad", f.provider("zzz")
		}
		if w := f.do(t, "PUT", "/api/v1/identity/user-providers/"+id, "admin", "tenant-a", "", body); w.Code != 400 {
			t.Fatalf("%s = %d %s", name, w.Code, w.Body)
		}
	}
	// Deletion needs the current revision and releases the source.
	if w := f.do(t, "DELETE", path, "admin", "tenant-a", "", nil); w.Code != 428 {
		t.Fatalf("delete without revision = %d", w.Code)
	}
	if w := f.do(t, "DELETE", path, "admin", "tenant-a", `"2"`, nil); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "PUT", "/api/v1/identity/user-providers/corp-copy", "admin", "tenant-a", "", f.provider("aiplex-users")); w.Code != 201 {
		t.Fatalf("source not released: %d %s", w.Code, w.Body)
	}
}

func TestUserProviderValidateJWKSPolicy(t *testing.T) {
	f := newUserProviderAPI(t)
	h := &UserIdentityProviderHandler{Providers: userfederation.NewStore(resources.NewMemoryStore()), Tenant: func(*http.Request) (string, error) { return "t", nil }, ValidateJWKS: func(string) error { return http.ErrNotSupported }}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			identity := models.WIFIdentity{PrincipalID: "p", Subject: "s"}
			ctx := context.WithValue(req.Context(), keyWIFIdentity, &identity)
			ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: identity, Roles: []models.IAMRole{models.RoleAdmin}})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/api/v1", func(r chi.Router) { h.Mount(r) })
	raw, _ := json.Marshal(f.provider("a"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/api/v1/identity/user-providers/corp", bytes.NewReader(raw)))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "outbound endpoint policy") {
		t.Fatalf("policy-denied JWKS = %d %s", w.Code, w.Body)
	}
}

func TestUserProviderPlan(t *testing.T) {
	f := newUserProviderAPI(t)
	if w := f.do(t, "PUT", "/api/v1/identity/user-providers/corp", "admin", "tenant-a", "", f.provider("aiplex-users")); w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	w := f.do(t, "POST", "/api/v1/identity/user-providers/corp/plan", "deployer", "tenant-a", "", nil)
	var plan userfederation.Plan
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil {
		t.Fatalf("plan = %d %s", w.Code, w.Body)
	}
	if plan.Issuer != "https://idp.test" || plan.Audience != "aiplex-users" || plan.PrincipalPool != auth.UserFederationPoolID("tenant-a") || len(plan.Steps) == 0 || plan.TokenSnippet.Title == "" || len(plan.AIPlex) < 3 {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.Contains(w.Body.String(), "AIPLEX_USER_FEDERATION=enabled") {
		t.Fatal("plan must say live federation is off and how to enable it")
	}
	if w := f.do(t, "POST", "/api/v1/identity/user-providers/missing/plan", "admin", "tenant-a", "", nil); w.Code != 404 {
		t.Fatalf("missing plan = %d", w.Code)
	}
	for _, preset := range userfederation.Catalog() {
		p := userfederation.Provider{ID: "x", Preset: preset.ID, Trust: userfederation.Trust{Issuer: "https://i.example", JWKSURL: "https://i.example/k", Audience: "a", SubjectClaim: "sub", GroupsFrom: "membership-authority", RequiredClaims: map[string]string{"hd": "example.com", "tid": "t"}}}
		if _, err := userfederation.RenderPlan(p, userfederation.PlanContext{PrincipalPool: "user-federation/t"}); err != nil {
			t.Fatalf("%s plan: %v", preset.ID, err)
		}
	}
}

func TestUserProviderVerifyNeverExposesToken(t *testing.T) {
	f := newUserProviderAPI(t)
	if w := f.do(t, "PUT", "/api/v1/identity/user-providers/corp", "admin", "tenant-a", "", f.provider("aiplex-users")); w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var logs bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&logs)
	defer func() { log.Logger = previous }()

	now := time.Now()
	claims := func(mutate func(map[string]any)) map[string]any {
		c := map[string]any{"iss": "https://idp.test", "sub": "user-42", "aud": "aiplex-users", "exp": now.Add(time.Minute).Unix(), "iat": now.Unix(), "org": "acme", "groups": []string{"students", "admins"}, "private_note": "classified-claim-value"}
		if mutate != nil {
			mutate(c)
		}
		return c
	}
	good := f.token(t, claims(nil))
	noGroups := f.token(t, claims(func(c map[string]any) { delete(c, "groups") }))
	bad := f.token(t, claims(func(c map[string]any) { c["org"] = "platform-secret-org" }))

	type resp struct {
		Valid           bool                   `json:"valid"`
		Checks          []userfederation.Check `json:"checks"`
		PrincipalID     string                 `json:"principal_id"`
		IsWorkforce     bool                   `json:"is_workforce"`
		Subject         string                 `json:"subject"`
		GroupsAvailable bool                   `json:"groups_available"`
		Groups          *[]string              `json:"groups"`
		GroupsNote      string                 `json:"groups_note"`
	}
	run := func(name string, body map[string]any, tenant string) (resp, string) {
		r := httptest.NewRequest("POST", "/api/v1/identity/user-providers/verify", strings.NewReader(mustJSON(body)))
		r.Header.Set("X-Test-Role", "admin")
		r.Header.Set("X-Test-Tenant", tenant)
		r = r.WithContext(log.Logger.WithContext(r.Context()))
		w := httptest.NewRecorder()
		Logger(f.router).ServeHTTP(w, r)
		var out resp
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Checks) != 10 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
		for _, secret := range []string{good, bad, noGroups, strings.Split(good, ".")[1], strings.Split(good, ".")[2], "classified-claim-value", "platform-secret-org"} {
			if strings.Contains(w.Body.String()+logs.String(), secret) {
				t.Fatalf("%s: response or logs leak %q", name, secret[:12])
			}
		}
		return out, w.Body.String()
	}

	out, raw := run("lookup", map[string]any{"token": good}, "tenant-a")
	if !out.Valid || out.PrincipalID != auth.CanonicalPrincipalID("https://idp.test", auth.UserFederationPoolID("tenant-a"), "user-42") || !out.IsWorkforce || out.Subject != "user-42" || !out.GroupsAvailable || out.Groups == nil || strings.Join(*out.Groups, ",") != "admins,students" {
		t.Fatalf("lookup: %s", raw)
	}
	out, _ = run("pinned", map[string]any{"token": good, "provider_id": "corp"}, "tenant-a")
	if !out.Valid {
		t.Fatal("pinned verify failed")
	}
	out, raw = run("groups unavailable", map[string]any{"token": noGroups}, "tenant-a")
	if !out.Valid || out.GroupsAvailable || out.Groups != nil || out.GroupsNote == "" || strings.Contains(raw, `"groups":`) {
		t.Fatalf("unavailable groups must be explicit and absent: %s", raw)
	}
	out, raw = run("tenant pin mismatch", map[string]any{"token": bad}, "tenant-a")
	failed := false
	for _, c := range out.Checks {
		failed = failed || (c.Name == userfederation.CheckRequiredClaims && c.Status == userfederation.StatusFail)
	}
	if out.Valid || !failed || out.PrincipalID != "" {
		t.Fatalf("mismatch: %s", raw)
	}
	out, raw = run("other tenant", map[string]any{"token": good}, "tenant-b")
	if out.Valid || strings.Contains(raw, "principal_id") {
		t.Fatalf("tenant-b verified a tenant-a provider: %s", raw)
	}

	// Malformed bodies do not echo their content; pinned lookups of missing providers 404.
	r := httptest.NewRequest("POST", "/api/v1/identity/user-providers/verify", strings.NewReader(`{"token":"`+good+`","extra":1}`))
	r.Header.Set("X-Test-Role", "admin")
	r.Header.Set("X-Test-Tenant", "tenant-a")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	if w.Code != 400 || strings.Contains(w.Body.String(), good) {
		t.Fatalf("malformed verify = %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "POST", "/api/v1/identity/user-providers/verify", "admin", "tenant-a", "", map[string]any{"token": good, "provider_id": "missing"}); w.Code != 404 || strings.Contains(w.Body.String(), good) {
		t.Fatalf("missing provider verify = %d %s", w.Code, w.Body)
	}
}
