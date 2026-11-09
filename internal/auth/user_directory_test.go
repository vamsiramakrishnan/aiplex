package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
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

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/directory"
	"github.com/vamsiramakrishnan/aiplex/pkg/membership"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

const (
	dirTenant   = "tenant-a"
	entraTID    = "72f988bf-86f1-41af-91ab-2d7cd011db47"
	entraAud    = "11112222-bbbb-3333-cccc-4444dddd5555"
	readerApp   = "99998888-aaaa-7777-bbbb-666655554444"
	aliceOID    = "00000000-0000-0000-0000-0000000000a1"
	groupAdmins = "aaaaaaaa-0000-0000-0000-000000000001"
	groupNested = "bbbbbbbb-0000-0000-0000-000000000002"
	googleAud   = "1234567890-abc.apps.googleusercontent.com"
	readerSecr  = "graph-reader-secret-value"
)

var entraIssuer = "https://login.microsoftonline.com/" + entraTID + "/v2.0"

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type testKeys struct{ v *JWKSVerifier }

func (k testKeys) ForProvider(context.Context, userfederation.Provider) (userfederation.SignatureVerifier, error) {
	return k.v, nil
}

type secretStub map[string]string

func (s secretStub) Resolve(_ context.Context, tenant, ref string) (string, error) {
	if v, ok := s[ref]; ok && tenant == dirTenant {
		return v, nil
	}
	return "", fmt.Errorf("missing")
}

// directoryState is the fake vendor directory: transitive groups per user.
type directoryState struct {
	mu          sync.Mutex
	entra       map[string][]string
	google      map[string][]string
	failStatus  int
	graphCalls  int
	googleCalls int
	authHeaders []string
}

type dirFixture struct {
	key       *rsa.PrivateKey
	clock     *testClock
	state     *directoryState
	providers *userfederation.Store
	dirs      *UserDirectories
	engine    *userfederation.Engine
	validator *WIFValidator
}

func newDirFixture(t *testing.T) *dirFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	t.Cleanup(jwks.Close)
	verifier, err := NewJWKSVerifier(JWKSConfig{Issuer: "x", Audience: "x", JWKSURL: jwks.URL + "/jwks", HTTPClient: jwks.Client()})
	if err != nil {
		t.Fatal(err)
	}
	state := &directoryState{entra: map[string][]string{aliceOID: {groupAdmins, groupNested}}, google: map[string][]string{"alice@example.com": {"eng@example.com", "all-staff@example.com"}}}
	dir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/"+entraTID+"/oauth2/v2.0/token":
			_ = r.ParseForm()
			if r.PostForm.Get("client_secret") != readerSecr || r.PostForm.Get("client_id") != readerApp {
				w.WriteHeader(401)
				return
			}
			_, _ = io.WriteString(w, `{"token_type":"Bearer","expires_in":3600,"access_token":"graph-token"}`)
		case strings.HasPrefix(r.URL.Path, "/v1.0/users/"):
			state.graphCalls++
			state.authHeaders = append(state.authHeaders, r.Header.Get("Authorization"))
			if state.failStatus != 0 {
				w.WriteHeader(state.failStatus)
				_, _ = io.WriteString(w, `{"error":{"message":"internal directory detail"}}`)
				return
			}
			oid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.0/users/"), "/transitiveMemberOf")
			var entries []string
			for _, g := range state.entra[oid] {
				entries = append(entries, fmt.Sprintf(`{"@odata.type":"#microsoft.graph.group","id":"%s"}`, g))
			}
			_, _ = io.WriteString(w, `{"value":[`+strings.Join(entries, ",")+`]}`)
		case r.URL.Path == "/v1/groups/-/memberships:searchTransitiveGroups":
			state.googleCalls++
			if state.failStatus != 0 {
				w.WriteHeader(state.failStatus)
				return
			}
			q := r.URL.Query().Get("query")
			email := strings.TrimSuffix(strings.TrimPrefix(q, "member_key_id == '"), "' && 'cloudidentity.googleapis.com/groups.discussion_forum' in labels")
			var entries []string
			for _, g := range state.google[email] {
				entries = append(entries, fmt.Sprintf(`{"groupKey":{"id":"%s"},"relationType":"DIRECT_AND_INDIRECT"}`, g))
			}
			_, _ = io.WriteString(w, `{"memberships":[`+strings.Join(entries, ",")+`]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(dir.Close)
	clock := &testClock{t: time.Now().UTC().Truncate(time.Second)}
	f := &dirFixture{key: key, clock: clock, state: state, providers: userfederation.NewStore(resources.NewMemoryStore())}
	f.dirs = &UserDirectories{CacheTTL: time.Minute, Deps: directory.Deps{HTTP: dir.Client(), Secrets: secretStub{"graph-reader": readerSecr}, Clock: clock.Now,
		Endpoints: directory.Endpoints{EntraLogin: dir.URL, Graph: dir.URL, CloudIdentity: dir.URL},
		GoogleToken: func(_ context.Context, tenant, ref string) (string, error) {
			if tenant != dirTenant || ref != "dir-reader" {
				return "", fmt.Errorf("no profile")
			}
			return "google-token", nil
		}}}
	f.engine = &userfederation.Engine{Keys: testKeys{verifier}, Directory: f.dirs, Now: clock.Now}
	f.validator = NewWIFValidator(nil, WIFConfig{}).WithUserFederation(&UserFederation{Tenant: dirTenant, Providers: f.providers, Engine: f.engine})
	return f
}

func (f *dirFixture) put(t *testing.T, id string, req userfederation.ProviderRequest) userfederation.Provider {
	t.Helper()
	p, err := userfederation.NewProvider(id, req)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := f.providers.Get(context.Background(), dirTenant, id); err == nil {
		p, err = f.providers.Put(context.Background(), dirTenant, p, current.Revision)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p, err = f.providers.Put(context.Background(), dirTenant, p, 0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func entraRequest(mode string) userfederation.ProviderRequest {
	return userfederation.ProviderRequest{Preset: userfederation.PresetEntraID, Inputs: map[string]string{"tenant_id": entraTID, "audience": entraAud, "groups_from": "claim"},
		Directory: &directory.Spec{Kind: directory.KindEntraGraph, Mode: mode, Credential: directory.Credential{ClientID: readerApp, ClientSecretRef: "graph-reader"}}}
}

func workspaceRequest() userfederation.ProviderRequest {
	return userfederation.ProviderRequest{Preset: userfederation.PresetGoogleWorkspace, Inputs: map[string]string{"hosted_domain": "example.com", "audience": googleAud, "groups_from": "membership-authority"},
		Directory: &directory.Spec{Kind: directory.KindGoogleCloudIdentity, Credential: directory.Credential{IdentityRef: "dir-reader"}}}
}

func (f *dirFixture) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *dirFixture) entraToken(t *testing.T, mutate func(map[string]any)) string {
	now := f.clock.Now()
	claims := map[string]any{"iss": entraIssuer, "aud": entraAud, "tid": entraTID, "oid": aliceOID, "sub": "pairwise-sub", "azp": "client-app-1", "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(), "groups": []string{"token-group"}}
	if mutate != nil {
		mutate(claims)
	}
	return f.sign(t, claims)
}

func overage(c map[string]any) {
	delete(c, "groups")
	c["_claim_names"] = map[string]any{"groups": "src1"}
	c["_claim_sources"] = map[string]any{"src1": map[string]any{"endpoint": "https://graph.windows.net/x"}}
}

func (f *dirFixture) graphCalls() int {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	return f.state.graphCalls
}

func (f *dirFixture) setFail(status int) {
	f.state.mu.Lock()
	f.state.failStatus = status
	f.state.mu.Unlock()
}

func TestEntraOverageResolvesThroughDirectory(t *testing.T) {
	f := newDirFixture(t)
	f.put(t, "entra", entraRequest(""))
	identity, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, overage)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(identity.Groups, ",") != groupAdmins+","+groupNested {
		t.Fatalf("groups = %v", identity.Groups)
	}
	ev := identity.VerifiedEvidence
	if !ev.GroupsPresent || !ev.GroupsValid || ev.GroupsSource != "directory:entra-graph" || !ev.GroupsObservedAt.Equal(f.clock.Now()) || !ev.GroupsExpiresAt.Equal(f.clock.Now().Add(5*time.Minute)) {
		t.Fatalf("evidence = %+v", ev)
	}
	// Quota (no quota membership authority) sees the same groups, bounded by the directory observation.
	snapshot, err := PrincipalMembership(identity, dirTenant, f.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	source := MembershipSource(entraIssuer, UserFederationPoolID(dirTenant))
	if len(snapshot.Groups) != 2 || snapshot.Groups[0] != source+":"+groupAdmins || !snapshot.IssuedAt.Equal(ev.GroupsObservedAt) || !snapshot.ExpiresAt.Equal(ev.GroupsExpiresAt) {
		t.Fatalf("membership = %+v", snapshot)
	}
	if _, err := PrincipalMembership(identity, dirTenant, f.clock.Now().Add(6*time.Minute)); err == nil {
		t.Fatal("membership outlived the directory observation")
	}
	// Only the app-only Graph token reached Graph.
	f.state.mu.Lock()
	headers := append([]string(nil), f.state.authHeaders...)
	f.state.mu.Unlock()
	for _, h := range headers {
		if h != "Bearer graph-token" {
			t.Fatalf("graph auth header = %q", h)
		}
	}
	// hasgroups behaves the same; a token that carries groups keeps them (fallback mode, no lookup).
	f.dirs.Invalidate(dirTenant, mustProvider(t, f, "entra"), aliceOID, f.clock.Now())
	if identity, err = f.validator.ExtractIdentity(bearer(f.entraToken(t, func(c map[string]any) { delete(c, "groups"); c["hasgroups"] = true }))); err != nil || len(identity.Groups) != 2 {
		t.Fatalf("hasgroups: %+v %v", identity, err)
	}
	calls := f.graphCalls()
	identity, err = f.validator.ExtractIdentity(bearer(f.entraToken(t, nil)))
	if err != nil || strings.Join(identity.Groups, ",") != "token-group" || identity.VerifiedEvidence.GroupsSource != "groups" || f.graphCalls() != calls {
		t.Fatalf("token groups in fallback: %+v %v (calls %d->%d)", identity, err, calls, f.graphCalls())
	}
}

func mustProvider(t *testing.T, f *dirFixture, id string) userfederation.Provider {
	t.Helper()
	p, err := f.providers.Get(context.Background(), dirTenant, id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDirectoryAlwaysModeIgnoresTokenGroups(t *testing.T) {
	f := newDirFixture(t)
	f.put(t, "entra", entraRequest(directory.ModeAlways))
	identity, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, nil)))
	if err != nil || strings.Join(identity.Groups, ",") != groupAdmins+","+groupNested {
		t.Fatalf("always: %+v %v", identity, err)
	}
	// Outage in always mode: groups unavailable, never the token's groups, never empty.
	f.dirs.Invalidate(dirTenant, mustProvider(t, f, "entra"), aliceOID, f.clock.Now())
	f.setFail(503)
	identity, err = f.validator.ExtractIdentity(bearer(f.entraToken(t, nil)))
	if err != nil {
		t.Fatalf("outage must not fail authentication: %v", err)
	}
	if identity.Groups != nil || identity.VerifiedEvidence.GroupsPresent {
		t.Fatalf("always-mode outage leaked groups: %v %+v", identity.Groups, identity.VerifiedEvidence)
	}
	if _, err := PrincipalMembership(identity, dirTenant, f.clock.Now()); err == nil {
		t.Fatal("outage produced a membership view")
	}
	// groups_from membership-authority + directory means always.
	p, err := userfederation.NewProvider("x", workspaceRequest())
	if err != nil || p.Directory.Mode != directory.ModeAlways {
		t.Fatalf("workspace mode = %+v %v", p.Directory, err)
	}
	bad := workspaceRequest()
	bad.Directory.Mode = directory.ModeFallback
	if _, err := userfederation.NewProvider("x", bad); err == nil {
		t.Fatal("fallback accepted with groups_from membership-authority")
	}
}

func TestDirectoryOutageFailsClosedInFallback(t *testing.T) {
	f := newDirFixture(t)
	f.put(t, "entra", entraRequest(""))
	for _, status := range []int{429, 403, 500} {
		f.setFail(status)
		identity, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, overage)))
		if err != nil {
			t.Fatalf("%d: %v", status, err)
		}
		if identity.Groups != nil || identity.VerifiedEvidence.GroupsPresent {
			t.Fatalf("%d: outage produced groups %v", status, identity.Groups)
		}
		// Dry-run explains where groups came from and why they are missing, without the directory body.
		result := f.engine.Evaluate(context.Background(), userfederation.Input{Token: f.entraToken(t, overage), Finder: f.providers.Tenant(dirTenant), Tenant: dirTenant})
		check := findCheck(result.Checks, userfederation.CheckGroups)
		if !result.Valid || result.GroupsSource != "unavailable" || result.GroupsAvailable || !strings.Contains(check.Reason, "directory:entra-graph lookup failed") || strings.Contains(check.Reason, "internal directory detail") || strings.Contains(check.Reason, readerSecr) {
			t.Fatalf("%d: dry-run = %+v / %+v", status, result, check)
		}
	}
	// Failures are not cached: recovery is immediate.
	f.setFail(0)
	identity, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, overage)))
	if err != nil || len(identity.Groups) != 2 {
		t.Fatalf("recovery: %+v %v", identity, err)
	}
	// Without a resolver on the server, a directory provider reports groups as unavailable.
	engine := *f.engine
	engine.Directory = nil
	result := engine.Evaluate(context.Background(), userfederation.Input{Token: f.entraToken(t, overage), Finder: f.providers.Tenant(dirTenant), Tenant: dirTenant})
	if !result.Valid || result.GroupsAvailable || !strings.Contains(findCheck(result.Checks, userfederation.CheckGroups).Reason, "not configured") {
		t.Fatalf("no resolver: %+v", result)
	}
}

func findCheck(checks []userfederation.Check, name string) userfederation.Check {
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	return userfederation.Check{}
}

type bindingStore struct{ bindings []models.RoleBinding }

func (s *bindingStore) ListRoleBindings(context.Context) ([]models.RoleBinding, error) {
	return s.bindings, nil
}
func (s *bindingStore) GetUserScopes(context.Context, string) ([]string, error) { return nil, nil }

func TestDirectoryGroupGrantsRoleAndLosesItAfterInvalidation(t *testing.T) {
	f := newDirFixture(t)
	provider := f.put(t, "entra", entraRequest(""))
	store := &bindingStore{bindings: []models.RoleBinding{{ID: "rb-admins", Group: groupNested, Role: models.RoleDeployer, Provider: entraIssuer, PoolID: UserFederationPoolID(dirTenant), Scopes: []string{"mcp:tools:deploy"}}}}
	validator := NewWIFValidator(store, WIFConfig{}).WithUserFederation(&UserFederation{Tenant: dirTenant, Providers: f.providers, Engine: f.engine})
	access := func() *models.ResolvedAccess {
		identity, err := validator.ExtractIdentity(bearer(f.entraToken(t, overage)))
		if err != nil {
			t.Fatal(err)
		}
		access, err := validator.SyncUserScopes(context.Background(), identity)
		if err != nil {
			t.Fatal(err)
		}
		return access
	}
	if a := access(); len(a.Roles) != 1 || a.Roles[0] != models.RoleDeployer || strings.Join(a.Scopes, ",") != "mcp:tools:deploy" {
		t.Fatalf("directory group did not grant access: %+v", a)
	}
	calls := f.graphCalls()
	access()
	if f.graphCalls() != calls {
		t.Fatal("second request missed the cache")
	}
	// Remove alice from the nested group in the directory. The cached view still
	// grants access until invalidation...
	f.state.mu.Lock()
	f.state.entra[aliceOID] = []string{groupAdmins}
	f.state.mu.Unlock()
	if a := access(); len(a.Roles) != 1 {
		t.Fatalf("cache hit expected before invalidation: %+v", a)
	}
	// ...and loses it right after.
	principal, err := f.dirs.Invalidate(dirTenant, provider, aliceOID, f.clock.Now())
	if err != nil || principal != CanonicalPrincipalID(entraIssuer, UserFederationPoolID(dirTenant), aliceOID) {
		t.Fatalf("invalidate = %s %v", principal, err)
	}
	if a := access(); len(a.Roles) != 0 || len(a.Scopes) != 0 {
		t.Fatalf("access survived removal + invalidation: %+v", a)
	}
	if f.graphCalls() != calls+1 {
		t.Fatalf("invalidation did not force a lookup: %d -> %d", calls, f.graphCalls())
	}
	// The TTL alone also re-resolves.
	f.state.mu.Lock()
	f.state.entra[aliceOID] = []string{groupAdmins, groupNested}
	f.state.mu.Unlock()
	f.clock.Advance(61 * time.Second)
	if a := access(); len(a.Roles) != 1 {
		t.Fatalf("TTL expiry did not re-resolve: %+v", a)
	}
}

func TestWorkspaceProviderResolvesThroughCloudIdentity(t *testing.T) {
	f := newDirFixture(t)
	f.put(t, "workspace", workspaceRequest())
	now := f.clock.Now()
	claims := map[string]any{"iss": "https://accounts.google.com", "aud": googleAud, "hd": "example.com", "sub": "1045", "email": "alice@example.com", "email_verified": true, "azp": googleAud, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix()}
	identity, err := f.validator.ExtractIdentity(bearer(f.sign(t, claims)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(identity.Groups, ",") != "all-staff@example.com,eng@example.com" || identity.VerifiedEvidence.GroupsSource != "directory:google-cloud-identity" || identity.PrincipalID != CanonicalPrincipalID("https://accounts.google.com", UserFederationPoolID(dirTenant), "1045") {
		t.Fatalf("workspace identity = %+v", identity)
	}
	// No client_claim default for Google: azp is not carried.
	if identity.AuthorizedParty != "" {
		t.Fatalf("authorized party without client_claim: %q", identity.AuthorizedParty)
	}
	// An unverified email is never used as the lookup key.
	claims["sub"], claims["email_verified"] = "1046", false
	identity, err = f.validator.ExtractIdentity(bearer(f.sign(t, claims)))
	if err != nil || identity.Groups != nil {
		t.Fatalf("unverified email: %+v %v", identity.Groups, err)
	}
}

func TestClientClaimCarriedOnlyWhenVerified(t *testing.T) {
	f := newDirFixture(t)
	req := entraRequest("")
	f.put(t, "entra", req)
	identity, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, nil)))
	if err != nil || identity.AuthorizedParty != "client-app-1" {
		t.Fatalf("entra v2 azp: %+v %v", identity, err)
	}
	// Allowlist enforced.
	req.ClientIDs = []string{"client-app-2"}
	f.put(t, "entra", req)
	if _, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, nil))); err == nil || !strings.Contains(err.Error(), "client") {
		t.Fatalf("client outside allowlist accepted: %v", err)
	}
	if _, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, func(c map[string]any) { delete(c, "azp") }))); err == nil {
		t.Fatal("token without the client claim accepted by an allowlisted provider")
	}
	req.ClientIDs = []string{"client-app-1", "client-app-2"}
	f.put(t, "entra", req)
	if identity, err = f.validator.ExtractIdentity(bearer(f.entraToken(t, nil))); err != nil || identity.AuthorizedParty != "client-app-1" {
		t.Fatalf("allowlisted: %+v %v", identity, err)
	}
	// v1.0 tokens default to appid.
	v1 := userfederation.ProviderRequest{Preset: userfederation.PresetEntraID, Inputs: map[string]string{"tenant_id": entraTID, "audience": "api://aiplex", "token_version": "1.0", "groups_from": "claim"}}
	p, err := userfederation.NewProvider("v1", v1)
	if err != nil || p.ClientClaim != "appid" {
		t.Fatalf("v1 client claim = %q %v", p.ClientClaim, err)
	}
	// Disabled with "none": nothing carried.
	req.ClientIDs = nil
	req.ClientClaim = userfederation.ClientClaimNone
	f.put(t, "entra", req)
	if identity, err = f.validator.ExtractIdentity(bearer(f.entraToken(t, nil))); err != nil || identity.AuthorizedParty != "" {
		t.Fatalf("client_claim none: %+v %v", identity, err)
	}
	// A forged token never yields a client.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	forger := &dirFixture{key: other, clock: f.clock}
	result := f.engine.Evaluate(context.Background(), userfederation.Input{Token: forger.entraToken(t, nil), Finder: f.providers.Tenant(dirTenant), Tenant: dirTenant})
	if result.Valid || result.ClientID != "" || result.VerifiedClient() != "" {
		t.Fatalf("forged token carried a client: %+v", result)
	}
}

func TestAgentFederationAuthorizedPartyIsRegisteredClient(t *testing.T) {
	f := newUserFixture(t)
	v := NewWIFValidator(nil, WIFConfig{}).WithFederation(f.agents)
	p, _ := f.providers.Get(context.Background(), "tenant-a", "corp")
	_ = f.providers.Delete(context.Background(), "tenant-a", "corp", p.Revision)
	identity, err := v.ExtractIdentity(bearer(f.token(t, func(c map[string]any) { c["sub"] = "svc-1"; c["azp"] = "evil-client"; c["appid"] = "evil"; c["cid"] = "evil" })))
	if err != nil || identity.AuthorizedParty != "tutor-agent" {
		t.Fatalf("agent authorized party influenced by the external token: %+v %v", identity, err)
	}
}

type quotaAuthority struct {
	calls int
}

func (a *quotaAuthority) ConfigurationDigest() string { return strings.Repeat("ab", 32) }
func (a *quotaAuthority) Resolve(_ context.Context, q membership.Query, at time.Time) (membership.Snapshot, error) {
	a.calls++
	return membership.Snapshot{Version: membership.Version, Query: q, Authority: "quota-dir", Ref: contracts.SnapshotRef{ID: "q", Version: "1"}, Groups: []membership.Group{{Source: "quota-dir", ID: "quota-group"}}, Complete: true, Expanded: true, IssuedAt: at.Add(-time.Second), ExpiresAt: at.Add(time.Minute)}, nil
}

// Precedence: a configured quota membership authority is the sole authority
// for quota eligibility; the provider directory feeds the identity's groups
// (role bindings, and quota only without a quota authority).
func TestQuotaMembershipAuthorityTakesPrecedenceForQuota(t *testing.T) {
	f := newDirFixture(t)
	f.put(t, "entra", entraRequest(""))
	identity, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, overage)))
	if err != nil || len(identity.Groups) != 2 {
		t.Fatalf("identity: %+v %v", identity, err)
	}
	calls := f.graphCalls()
	quota := &quotaAuthority{}
	resolver, err := NewDirectoryMembershipResolver(quota, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := resolver.ResolveQuotaSubject(context.Background(), dirTenant, identity, "principal", f.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if quota.calls != 1 || f.graphCalls() != calls || len(subject.Membership.Groups) != 1 || subject.Membership.Groups[0].ID != "quota-group" || subject.Kind != "human" {
		t.Fatalf("quota subject = %+v (quota calls %d, graph %d->%d)", subject, quota.calls, calls, f.graphCalls())
	}
	// The identity's own group view is unchanged by the quota authority.
	if strings.Join(identity.Groups, ",") != groupAdmins+","+groupNested {
		t.Fatalf("identity groups changed: %v", identity.Groups)
	}
}

func TestDirectoryCacheResetsWhenConfigurationChanges(t *testing.T) {
	f := newDirFixture(t)
	f.put(t, "entra", entraRequest(""))
	if _, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, overage))); err != nil {
		t.Fatal(err)
	}
	calls := f.graphCalls()
	// Change the credential: a fresh cache must be used (no view obtained under the old credential).
	req := entraRequest("")
	req.Directory.Credential.ClientSecretRef = "graph-reader-2"
	f.put(t, "entra", req)
	identity, err := f.validator.ExtractIdentity(bearer(f.entraToken(t, overage)))
	if err != nil || identity.Groups != nil || f.graphCalls() != calls {
		t.Fatalf("stale view reused after credential change: %+v %v", identity, err)
	}
}

// Overlap: write-time predicate and token claimants.
func TestOverlapPredicateAndClaimants(t *testing.T) {
	f := newUserFixture(t)
	b, _ := f.bindings.Get(context.Background(), "tenant-a", "tutor-agent", "svc")
	p, _ := f.providers.Get(context.Background(), "tenant-a", "corp")
	if !Overlaps(b, p) {
		t.Fatal("same issuer and audience must overlap")
	}
	o, err := BindingOverlap(context.Background(), f.providers, "tenant-a", b)
	if err != nil || o == nil || o.ID != "corp" || o.Kind != "user-identity-provider" {
		t.Fatalf("binding overlap = %+v %v", o, err)
	}
	o, err = ProviderOverlap(context.Background(), f.bindings, "tenant-a", p)
	if err != nil || o == nil || o.ID != "tutor-agent/svc" || !strings.Contains(o.Error(), "acknowledge_overlap") {
		t.Fatalf("provider overlap = %+v %v", o, err)
	}
	if o, _ = ProviderOverlap(context.Background(), f.bindings, "tenant-b", p); o != nil {
		t.Fatal("overlap across tenants")
	}
	other := b
	other.Audience = "agents-only"
	if Overlaps(other, p) {
		t.Fatal("different audiences must not overlap at write time")
	}
	agent, user, err := TokenClaimants(context.Background(), "tenant-a", f.token(t, func(c map[string]any) { c["sub"] = "svc-1" }), f.bindings, f.providers)
	if err != nil || agent == nil || user == nil {
		t.Fatalf("claimants = %v %v %v", agent, user, err)
	}
	agent, user, _ = TokenClaimants(context.Background(), "tenant-a", f.token(t, nil), f.bindings, f.providers)
	if agent != nil || user == nil {
		t.Fatalf("user-only claimants = %v %v", agent, user)
	}
	_ = agentfederation.Binding{}
}
