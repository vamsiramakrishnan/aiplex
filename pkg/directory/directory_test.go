package directory

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/membership"
)

const (
	entraTenant = "72f988bf-86f1-41af-91ab-2d7cd011db47"
	entraClient = "11112222-bbbb-3333-cccc-4444dddd5555"
	userOID     = "6e7b768e-07e2-4810-8459-485f84f8f204"
	groupA      = "aaaaaaaa-0000-0000-0000-000000000001"
	groupB      = "bbbbbbbb-0000-0000-0000-000000000002"
	groupC      = "cccccccc-0000-0000-0000-000000000003"
	clientSecr  = "very-secret-client-value"
	apiToken    = "00QCjAl4MlV-WPXM-0HmjFx-vbGua"
)

type secrets map[string]string

func (s secrets) Resolve(_ context.Context, tenant, ref string) (string, error) {
	if tenant != "tenant-a" {
		return "", errors.New("wrong tenant")
	}
	v, ok := s[ref]
	if !ok {
		return "", errors.New("missing")
	}
	return v, nil
}

var fixedNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func clock() time.Time { return fixedNow }

func query(native string) membership.Query {
	return membership.Query{TenantID: "tenant-a", SubjectID: "principal-x", Kind: "human", Issuer: "https://issuer.test", PoolID: "user-federation/tenant-a", NativeSubject: native, Role: "principal"}
}

func target() Target {
	return Target{Tenant: "tenant-a", Issuer: "https://issuer.test", PoolID: "user-federation/tenant-a", EntraTenant: entraTenant, Domain: "example.com"}
}

type recorder struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
}

func (r *recorder) add(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	r.bodies = append(r.bodies, string(body))
}

func entraServer(t *testing.T, pages func(w http.ResponseWriter, r *http.Request, base string)) (*httptest.Server, *recorder) {
	rec := &recorder{}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		if r.URL.Path == "/"+entraTenant+"/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token_type":"Bearer","expires_in":3599,"access_token":"graph-access-token"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer graph-access-token" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json;odata.metadata=minimal")
		pages(w, r, server.URL)
	}))
	t.Cleanup(server.Close)
	return server, rec
}

func entraAuthority(t *testing.T, server *httptest.Server) *Authority {
	t.Helper()
	a, err := New(Spec{Kind: KindEntraGraph, Credential: Credential{ClientID: entraClient, ClientSecretRef: "graph-reader"}}, target(), Deps{HTTP: server.Client(), Secrets: secrets{"graph-reader": clientSecr}, Clock: clock, MaxAge: 2 * time.Minute, Endpoints: Endpoints{EntraLogin: server.URL, Graph: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestEntraGraphTransitivePaginationAndPinnedCredential(t *testing.T) {
	server, rec := entraServer(t, func(w http.ResponseWriter, r *http.Request, base string) {
		if r.URL.Path != "/v1.0/users/"+userOID+"/transitiveMemberOf" || (r.URL.RawQuery != "" && r.URL.Query().Get("$skiptoken") != "page2") {
			w.WriteHeader(404)
			return
		}
		if r.URL.Query().Get("$skiptoken") == "page2" {
			fmt.Fprintf(w, `{"value":[{"@odata.type":"#microsoft.graph.group","id":"%s"},{"@odata.type":"#microsoft.graph.administrativeUnit","id":"x"}]}`, groupC)
			return
		}
		// Direct and nested groups come back in one transitive listing; a
		// directory role is skipped; IDs are normalised to lower case.
		fmt.Fprintf(w, `{"value":[{"@odata.type":"#microsoft.graph.group","id":"%s"},{"@odata.type":"#microsoft.graph.directoryRole","id":"role-1"},{"@odata.type":"#microsoft.graph.group","id":"%s"}],"@odata.nextLink":"%s/v1.0/users/%s/transitiveMemberOf?$skiptoken=page2"}`, groupA, strings.ToUpper(groupB), base, userOID)
	})
	a := entraAuthority(t, server)
	snapshot, err := a.Resolve(context.Background(), query(userOID), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Complete || !snapshot.Expanded || snapshot.Authority != "directory:entra-graph" || !snapshot.IssuedAt.Equal(fixedNow) || !snapshot.ExpiresAt.Equal(fixedNow.Add(2*time.Minute)) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	var got []string
	for _, g := range snapshot.Groups {
		if g.Source != "entra-graph:"+entraTenant {
			t.Fatalf("group source = %q", g.Source)
		}
		got = append(got, g.ID)
	}
	if strings.Join(got, ",") != strings.Join([]string{groupA, groupB, groupC}, ",") {
		t.Fatalf("groups = %v", got)
	}
	// The client secret goes only to the token endpoint, the access token only to Graph.
	for i, req := range rec.requests {
		if strings.HasSuffix(req.URL.Path, "/token") {
			form, _ := url.ParseQuery(rec.bodies[i])
			if form.Get("client_secret") != clientSecr || form.Get("scope") != "https://graph.microsoft.com/.default" || form.Get("grant_type") != "client_credentials" || form.Get("client_id") != entraClient {
				t.Fatalf("token form = %v", form)
			}
			continue
		}
		if strings.Contains(rec.bodies[i], clientSecr) || strings.Contains(req.URL.String(), clientSecr) {
			t.Fatal("client secret sent to Graph")
		}
	}
	// The token is cached across lookups.
	if _, err := a.Resolve(context.Background(), query(userOID), fixedNow); err != nil {
		t.Fatal(err)
	}
	tokens := 0
	for _, req := range rec.requests {
		if strings.HasSuffix(req.URL.Path, "/token") {
			tokens++
		}
	}
	if tokens != 1 {
		t.Fatalf("token requests = %d", tokens)
	}
	// Query binding: another tenant, an actor query or an agent are refused.
	for _, q := range []membership.Query{func() membership.Query { q := query(userOID); q.TenantID = "tenant-b"; return q }(), func() membership.Query { q := query(userOID); q.Kind = "agent"; return q }()} {
		if _, err := a.Resolve(context.Background(), q, fixedNow); !errors.Is(err, membership.ErrInvalidEvidence) {
			t.Fatalf("foreign query = %v", err)
		}
	}
}

func TestEntraGraphFailuresAreNeverPartial(t *testing.T) {
	cases := map[string]struct {
		page   func(w http.ResponseWriter, r *http.Request, base string)
		reason string
	}{
		"throttled": {func(w http.ResponseWriter, r *http.Request, base string) {
			w.Header().Set("Retry-After", "10")
			w.WriteHeader(429)
		}, "throttled"},
		"permission": {func(w http.ResponseWriter, r *http.Request, base string) {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{"error":{"code":"Authorization_RequestDenied","message":"secret internal detail"}}`)
		}, "denied"},
		"truncated second page": {func(w http.ResponseWriter, r *http.Request, base string) {
			if r.URL.Query().Get("$skiptoken") == "" {
				fmt.Fprintf(w, `{"value":[{"@odata.type":"#microsoft.graph.group","id":"%s"}],"@odata.nextLink":"%s/v1.0/users/%s/transitiveMemberOf?$skiptoken=2"}`, groupA, base, userOID)
				return
			}
			fmt.Fprintf(w, `{"value":[{"@odata.type":"#microsoft.graph.group","id":"%s"}`, groupB)
		}, "truncated"},
		"second page throttled": {func(w http.ResponseWriter, r *http.Request, base string) {
			if r.URL.Query().Get("$skiptoken") == "" {
				fmt.Fprintf(w, `{"value":[{"@odata.type":"#microsoft.graph.group","id":"%s"}],"@odata.nextLink":"%s/v1.0/users/%s/transitiveMemberOf?$skiptoken=2"}`, groupA, base, userOID)
				return
			}
			w.WriteHeader(429)
		}, "throttled"},
		"missing value": {func(w http.ResponseWriter, r *http.Request, base string) { _, _ = io.WriteString(w, `{}`) }, "no value"},
		"looping pagination": {func(w http.ResponseWriter, r *http.Request, base string) {
			fmt.Fprintf(w, `{"value":[],"@odata.nextLink":"%s/v1.0/users/%s/transitiveMemberOf"}`, base, userOID)
		}, "terminate"},
		"unknown user": {func(w http.ResponseWriter, r *http.Request, base string) { w.WriteHeader(404) }, "does not know"},
		"untyped entry": {func(w http.ResponseWriter, r *http.Request, base string) {
			_, _ = io.WriteString(w, `{"value":[{"id":"x"}]}`)
		}, "without a type"},
		"too many groups": {func(w http.ResponseWriter, r *http.Request, base string) {
			var entries []string
			for i := 0; i < 1100; i++ {
				entries = append(entries, fmt.Sprintf(`{"@odata.type":"#microsoft.graph.group","id":"aaaaaaaa-0000-0000-0000-%012d"}`, i))
			}
			_, _ = io.WriteString(w, `{"value":[`+strings.Join(entries, ",")+`]}`)
		}, "more than 1024"},
	}
	for name, tc := range cases {
		server, _ := entraServer(t, tc.page)
		snapshot, err := entraAuthority(t, server).Resolve(context.Background(), query(userOID), fixedNow)
		if err == nil || snapshot.Groups != nil {
			t.Fatalf("%s: partial view returned: %+v", name, snapshot)
		}
		if !errors.Is(err, membership.ErrUnavailable) || !strings.Contains(Reason(err), tc.reason) {
			t.Fatalf("%s: err = %v (reason %q)", name, err, Reason(err))
		}
		if strings.Contains(err.Error(), "secret internal detail") || strings.Contains(err.Error(), clientSecr) || strings.Contains(err.Error(), "graph-access-token") {
			t.Fatalf("%s: error leaks directory body or credentials: %v", name, err)
		}
	}
}

func TestEntraGraphNextLinkOffOriginNeverReceivesToken(t *testing.T) {
	var foreignHits atomic.Int32
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"value":[]}`)
	}))
	defer foreign.Close()
	server, _ := entraServer(t, func(w http.ResponseWriter, r *http.Request, base string) {
		fmt.Fprintf(w, `{"value":[],"@odata.nextLink":"%s/steal"}`, foreign.URL)
	})
	_, err := entraAuthority(t, server).Resolve(context.Background(), query(userOID), fixedNow)
	if err == nil || !strings.Contains(Reason(err), "outside the pinned") || foreignHits.Load() != 0 {
		t.Fatalf("off-origin link: err=%v hits=%d", err, foreignHits.Load())
	}
}

func TestEntraTokenFailureDoesNotLeakSecret(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = w.Write(body) // echoes the client secret back
	}))
	defer server.Close()
	_, err := entraAuthority(t, server).Resolve(context.Background(), query(userOID), fixedNow)
	if err == nil || strings.Contains(err.Error(), clientSecr) || !strings.Contains(Reason(err), "token endpoint") {
		t.Fatalf("err = %v", err)
	}
	// A subject that is not an object ID is never sent.
	if _, err := entraAuthority(t, server).Resolve(context.Background(), query("user@example.com"), fixedNow); err == nil || !strings.Contains(Reason(err), "oid") {
		t.Fatalf("non-oid = %v", err)
	}
}

func googleAuthority(t *testing.T, server *httptest.Server, tokens *atomic.Int32) *Authority {
	t.Helper()
	a, err := New(Spec{Kind: KindGoogleCloudIdentity, Mode: ModeAlways, Credential: Credential{IdentityRef: "directory-reader"}}, target(), Deps{HTTP: server.Client(), Clock: clock, Endpoints: Endpoints{CloudIdentity: server.URL},
		GoogleToken: func(_ context.Context, tenant, ref string) (string, error) {
			if tenant != "tenant-a" || ref != "directory-reader" {
				return "", errors.New("wrong profile")
			}
			tokens.Add(1)
			return "google-access-token", nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCloudIdentityTransitiveSearch(t *testing.T) {
	var queries []url.Values
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/groups/-/memberships:searchTransitiveGroups" || r.Header.Get("Authorization") != "Bearer google-access-token" {
			w.WriteHeader(403)
			return
		}
		queries = append(queries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = io.WriteString(w, `{"memberships":[{"groupKey":{"id":"eng@example.com"},"relationType":"DIRECT","group":"groups/1"}],"nextPageToken":"p2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"memberships":[{"groupKey":{"id":"All-Staff@example.com"},"relationType":"INDIRECT"}]}`)
	}))
	defer server.Close()
	var tokens atomic.Int32
	a := googleAuthority(t, server, &tokens)
	ctx := WithLookupKey(context.Background(), "alice@example.com")
	snapshot, err := a.Resolve(ctx, query("1234567890"), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Groups) != 2 || snapshot.Groups[0].ID != "all-staff@example.com" || snapshot.Groups[1].ID != "eng@example.com" || snapshot.Groups[0].Source != "google-cloud-identity:example.com" {
		t.Fatalf("groups = %+v", snapshot.Groups)
	}
	if len(queries) != 2 || queries[0].Get("query") != "member_key_id == 'alice@example.com' && 'cloudidentity.googleapis.com/groups.discussion_forum' in labels" || queries[0].Get("pageSize") != "1000" || queries[1].Get("pageToken") != "p2" {
		t.Fatalf("queries = %v", queries)
	}
	// Without a verified email nothing is sent.
	if _, err := a.Resolve(context.Background(), query("1234567890"), fixedNow); err == nil || !strings.Contains(Reason(err), "verified email") {
		t.Fatalf("no email = %v", err)
	}
	if _, err := a.Resolve(WithLookupKey(context.Background(), "x' || true || 'y@example.com"), query("1234567890"), fixedNow); err == nil {
		t.Fatal("CEL injection accepted")
	}
}

func TestCloudIdentityFailuresAreNeverPartial(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"permission": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) },
		"throttled":  func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) },
		"truncated": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("pageToken") == "" {
				_, _ = io.WriteString(w, `{"memberships":[{"groupKey":{"id":"eng@example.com"}}],"nextPageToken":"p2"}`)
				return
			}
			_, _ = io.WriteString(w, `{"memberships":[{"groupKey":{"id":"a`)
		},
		"repeated token": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"memberships":[],"nextPageToken":"same"}`)
		},
	} {
		server := httptest.NewTLSServer(handler)
		var tokens atomic.Int32
		snapshot, err := googleAuthority(t, server, &tokens).Resolve(WithLookupKey(context.Background(), "alice@example.com"), query("1"), fixedNow)
		server.Close()
		if err == nil || snapshot.Groups != nil || !errors.Is(err, membership.ErrUnavailable) {
			t.Fatalf("%s: %+v %v", name, snapshot, err)
		}
	}
}

func oktaServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, base string)) *httptest.Server {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler(w, r, server.URL) }))
	t.Cleanup(server.Close)
	return server
}

func oktaAuthority(t *testing.T, server *httptest.Server, cred Credential, groupID string, store secrets) *Authority {
	t.Helper()
	a, err := New(Spec{Kind: KindOkta, OrgURL: server.URL, GroupID: groupID, Credential: cred}, target(), Deps{HTTP: server.Client(), Secrets: store, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOktaAPITokenPagination(t *testing.T) {
	server := oktaServer(t, func(w http.ResponseWriter, r *http.Request, base string) {
		if r.Header.Get("Authorization") != "SSWS "+apiToken || r.URL.Path != "/api/v1/users/00u1abc/groups" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			w.Header().Set("Link", `<`+base+`/api/v1/users/00u1abc/groups?limit=200>; rel="self", <`+base+`/api/v1/users/00u1abc/groups?after=00g2&limit=200>; rel="next"`)
			_, _ = io.WriteString(w, `[{"id":"00g1","profile":{"name":"Everyone"},"type":"BUILT_IN"}]`)
			return
		}
		_, _ = io.WriteString(w, `[{"id":"00g2","profile":{"name":"Engineering"},"type":"OKTA_GROUP"}]`)
	})
	store := secrets{"okta-token": apiToken}
	snapshot, err := oktaAuthority(t, server, Credential{APITokenRef: "okta-token"}, "", store).Resolve(context.Background(), query("00u1abc"), fixedNow)
	if err != nil || len(snapshot.Groups) != 2 || snapshot.Groups[0].ID != "00g1" || snapshot.Groups[1].ID != "00g2" || !strings.HasPrefix(snapshot.Groups[0].Source, "okta:127.0.0.1:") {
		t.Fatalf("snapshot = %+v %v", snapshot, err)
	}
	snapshot, err = oktaAuthority(t, server, Credential{APITokenRef: "okta-token"}, GroupIDName, store).Resolve(context.Background(), query("00u1abc"), fixedNow)
	if err != nil || snapshot.Groups[0].ID != "Engineering" || snapshot.Groups[1].ID != "Everyone" {
		t.Fatalf("names = %+v %v", snapshot, err)
	}
}

func TestOktaFailuresAndOriginPinning(t *testing.T) {
	var foreignHits atomic.Int32
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignHits.Add(1) }))
	defer foreign.Close()
	store := secrets{"okta-token": apiToken}
	for name, tc := range map[string]struct {
		handler func(w http.ResponseWriter, r *http.Request, base string)
		reason  string
	}{
		"throttled":  {func(w http.ResponseWriter, r *http.Request, base string) { w.WriteHeader(429) }, "throttled"},
		"permission": {func(w http.ResponseWriter, r *http.Request, base string) { w.WriteHeader(403) }, "denied"},
		"off-origin next": {func(w http.ResponseWriter, r *http.Request, base string) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Link", `<`+foreign.URL+`/api/v1/users/x/groups?after=1>; rel="next"`)
			_, _ = io.WriteString(w, `[{"id":"00g1"}]`)
		}, "outside the pinned"},
		"truncated": {func(w http.ResponseWriter, r *http.Request, base string) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"id":"00g1"},{"id":"00`)
		}, "truncated"},
		"redirect": {func(w http.ResponseWriter, r *http.Request, base string) {
			http.Redirect(w, r, foreign.URL, http.StatusFound)
		}, "redirect"},
	} {
		server := oktaServer(t, tc.handler)
		snapshot, err := oktaAuthority(t, server, Credential{APITokenRef: "okta-token"}, "", store).Resolve(context.Background(), query("00u1abc"), fixedNow)
		if err == nil || snapshot.Groups != nil || !strings.Contains(Reason(err), tc.reason) || strings.Contains(err.Error(), apiToken) {
			t.Fatalf("%s: %+v %v", name, snapshot, err)
		}
	}
	if foreignHits.Load() != 0 {
		t.Fatalf("credentialed request reached a foreign origin %d times", foreignHits.Load())
	}
}

func TestOktaServiceAppPrivateKeyJWT(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalECPrivateKey(key)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
	tokenType := "Bearer"
	server := oktaServer(t, func(w http.ResponseWriter, r *http.Request, base string) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/oauth2/v1/token" {
			_ = r.ParseForm()
			assertion := r.PostForm.Get("client_assertion")
			parsed, err := jwt.Parse(assertion, func(*jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience(base+"/oauth2/v1/token"), jwt.WithIssuer("0oa1service"), jwt.WithTimeFunc(clock))
			if err != nil || parsed.Header["kid"] != "k1" || r.PostForm.Get("scope") != "okta.users.read" || r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("client_assertion_type") != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token_type": tokenType, "expires_in": 3600, "access_token": "okta-access"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer okta-access" {
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `[{"id":"00g9"}]`)
	})
	cred := Credential{ClientID: "0oa1service", PrivateKeyRef: "okta-key", KeyID: "k1"}
	snapshot, err := oktaAuthority(t, server, cred, "", secrets{"okta-key": pemKey}).Resolve(context.Background(), query("alice@example.com"), fixedNow)
	if err != nil || len(snapshot.Groups) != 1 || snapshot.Groups[0].ID != "00g9" {
		t.Fatalf("service app = %+v %v", snapshot, err)
	}
	tokenType = "DPoP"
	if _, err = oktaAuthority(t, server, cred, "", secrets{"okta-key": pemKey}).Resolve(context.Background(), query("alice@example.com"), fixedNow); err == nil || !strings.Contains(Reason(err), "DPoP") {
		t.Fatalf("DPoP = %v", err)
	}
}

func TestSpecValidation(t *testing.T) {
	bad := map[string]Spec{
		"no kind":            {},
		"unknown kind":       {Kind: "ldap"},
		"bad mode":           {Kind: KindEntraGraph, Mode: "sometimes", Credential: Credential{ClientID: entraClient, ClientSecretRef: "s"}},
		"secret value":       {Kind: KindEntraGraph, Credential: Credential{ClientID: entraClient, ClientSecretRef: "abc~def.ghi=="}},
		"foreign field":      {Kind: KindEntraGraph, Credential: Credential{ClientID: entraClient, ClientSecretRef: "s", IdentityRef: "x"}},
		"google no profile":  {Kind: KindGoogleCloudIdentity},
		"okta no org":        {Kind: KindOkta, Credential: Credential{APITokenRef: "t"}},
		"okta org path":      {Kind: KindOkta, OrgURL: "https://acme.okta.com/oauth2/default", Credential: Credential{APITokenRef: "t"}},
		"okta both creds":    {Kind: KindOkta, OrgURL: "https://acme.okta.com", Credential: Credential{APITokenRef: "t", ClientID: "c", PrivateKeyRef: "k"}},
		"okta http":          {Kind: KindOkta, OrgURL: "http://acme.okta.com", Credential: Credential{APITokenRef: "t"}},
		"group id elsewhere": {Kind: KindEntraGraph, GroupID: "name", Credential: Credential{ClientID: entraClient, ClientSecretRef: "s"}},
	}
	for name, spec := range bad {
		if _, err := spec.Normalize(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	s, err := Spec{Kind: KindOkta, OrgURL: "https://ACME.okta.com/", Credential: Credential{APITokenRef: "t"}}.Normalize()
	if err != nil || s.OrgURL != "https://acme.okta.com" || s.Mode != ModeFallback || s.GroupID != GroupIDObjectID {
		t.Fatalf("okta normalized = %+v %v", s, err)
	}
}
