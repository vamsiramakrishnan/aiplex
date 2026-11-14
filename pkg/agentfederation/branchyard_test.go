package agentfederation_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

const byAudience = "https://aiplex.example.com"

func TestDeriveBranchyard(t *testing.T) {
	good, err := agentfederation.Derive(agentfederation.PresetBranchyard, map[string]string{"server": "https://by.example.com", "tenant": "acme", "audience": byAudience}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := agentfederation.Trust{Issuer: "https://by.example.com", JWKSURL: "https://by.example.com/.well-known/jwks.json", Audience: byAudience, SubjectClaim: "sub", Subject: "branchyard:acme", RequiredClaims: map[string]string{"by_purpose": "aiplex"}}
	if good.Issuer != want.Issuer || good.JWKSURL != want.JWKSURL || good.Audience != want.Audience || good.SubjectClaim != want.SubjectClaim || good.Subject != want.Subject || len(good.RequiredClaims) != 1 || good.RequiredClaims["by_purpose"] != "aiplex" {
		t.Fatalf("got %+v\nwant %+v", good, want)
	}
	// The audience defaults as for the other presets.
	if d, err := agentfederation.Derive(agentfederation.PresetBranchyard, map[string]string{"server": "https://by.example.com", "tenant": "acme"}, nil, byAudience); err != nil || d.Audience != byAudience {
		t.Fatalf("default audience: %+v %v", d, err)
	}
	// A development server on loopback, with a port.
	for _, server := range []string{"http://127.0.0.1:8421", "http://[::1]:8421", "http://localhost:8421", "https://127.0.0.1:8443", "https://by.example.com:8443"} {
		d, err := agentfederation.Derive(agentfederation.PresetBranchyard, map[string]string{"server": server, "tenant": "acme", "audience": byAudience}, nil, "")
		if err != nil || d.Issuer != server || d.JWKSURL != server+"/.well-known/jwks.json" {
			t.Fatalf("%s: %+v %v", server, d, err)
		}
	}
	// The plan is the Branchyard backend configuration of contract sections
	// 2 and 3: auth = "turn-token" with the binding audience, no key.
	binding, err := agentfederation.NewBinding("acme-agent", "by", agentfederation.BindingRequest{Preset: agentfederation.PresetBranchyard, Inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme", "audience": byAudience}}, "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := agentfederation.RenderPlan(binding, agentfederation.PlanContext{APIBaseURL: "https://aiplex.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	config := plan.Steps[1].Content
	for _, want := range []string{`auth = "turn-token"`, `audience = "` + byAudience + `"`, `control_plane = "aiplex"`, `url = "https://aiplex.example.com/ap/ACCESS_POINT"`, `api = "anthropic"`, `api = "openai"`} {
		if !strings.Contains(config, want) {
			t.Fatalf("plan lacks %s:\n%s", want, config)
		}
	}
	if strings.Contains(config, "key =") || strings.Contains(plan.TokenSnippet.Description+plan.Steps[0].Description, "RS256") {
		t.Fatalf("plan: %+v", plan)
	}
	// Tenant length boundary: 128 characters accepted, 129 refused (below).
	if d, err := agentfederation.Derive(agentfederation.PresetBranchyard, map[string]string{"server": "https://by.example.com", "tenant": strings.Repeat("a", 128), "audience": byAudience}, nil, ""); err != nil || d.Subject != "branchyard:"+strings.Repeat("a", 128) {
		t.Fatalf("128-character tenant: %+v %v", d, err)
	}
	p, _ := agentfederation.Lookup(agentfederation.PresetBranchyard)
	if strings.Join(p.Algorithms, ",") != "EdDSA" || p.AllowsRequiredClaims {
		t.Fatalf("branchyard preset: algorithms %v, extra claims %v", p.Algorithms, p.AllowsRequiredClaims)
	}
	notes := strings.Join(p.Notes, " ")
	for _, s := range []string{"EdDSA", "cannot be federated", "Not validated against a running Branchyard server", "aiplex-authz", "RS256 only", "/ap/{id}/", "at most one hour"} {
		if !strings.Contains(notes, s) {
			t.Fatalf("notes do not say %q: %s", s, notes)
		}
	}
	for _, other := range agentfederation.Catalog("") {
		if other.ID != agentfederation.PresetBranchyard && strings.Contains(strings.Join(other.Algorithms, ","), "EdDSA") {
			t.Fatalf("preset %s accepts EdDSA", other.ID)
		}
	}

	refused := map[string]struct {
		inputs  map[string]string
		extra   map[string]string
		wantErr string
	}{
		"trailing slash":          {inputs: map[string]string{"server": "https://by.example.com/", "tenant": "acme"}, wantErr: "trailing slash"},
		"path":                    {inputs: map[string]string{"server": "https://by.example.com/tenant/acme", "tenant": "acme"}, wantErr: "no path"},
		"jwks path":               {inputs: map[string]string{"server": "https://evil.example/.well-known/jwks.json?", "tenant": "acme"}, wantErr: "no path"},
		"query":                   {inputs: map[string]string{"server": "https://by.example.com?x=1", "tenant": "acme"}, wantErr: "query"},
		"empty query":             {inputs: map[string]string{"server": "https://by.example.com?", "tenant": "acme"}, wantErr: "query"},
		"fragment":                {inputs: map[string]string{"server": "https://by.example.com#x", "tenant": "acme"}, wantErr: "fragment"},
		"empty fragment":          {inputs: map[string]string{"server": "https://by.example.com#", "tenant": "acme"}, wantErr: "fragment"},
		"user information":        {inputs: map[string]string{"server": "https://evil.example@by.example.com", "tenant": "acme"}, wantErr: "user information"},
		"user and password":       {inputs: map[string]string{"server": "https://u:p@by.example.com", "tenant": "acme"}, wantErr: "user information"},
		"empty port":              {inputs: map[string]string{"server": "https://by.example.com:", "tenant": "acme"}, wantErr: "https://host"},
		"bad port":                {inputs: map[string]string{"server": "https://by.example.com:x", "tenant": "acme"}, wantErr: "https://host"},
		"upper-case scheme":       {inputs: map[string]string{"server": "HTTPS://by.example.com", "tenant": "acme"}, wantErr: "https://host"},
		"no scheme":               {inputs: map[string]string{"server": "by.example.com", "tenant": "acme"}, wantErr: "https://host"},
		"opaque":                  {inputs: map[string]string{"server": "https:by.example.com", "tenant": "acme"}, wantErr: "https://host"},
		"percent escape":          {inputs: map[string]string{"server": "https://by%2eexample.com", "tenant": "acme"}, wantErr: "https://host"},
		"backslash":               {inputs: map[string]string{"server": "https://by.example.com\\@evil.example", "tenant": "acme"}, wantErr: "https://host"},
		"wildcard host":           {inputs: map[string]string{"server": "https://*.example.com", "tenant": "acme"}, wantErr: "https://host"},
		"local yard issuer":       {inputs: map[string]string{"server": "branchyard:local:abc", "tenant": "acme"}, wantErr: "https://host"},
		"file scheme":             {inputs: map[string]string{"server": "file:///r/.branchyard/gateway/jwks.json", "tenant": "acme"}, wantErr: "https://host"},
		"http public host":        {inputs: map[string]string{"server": "http://by.example.com", "tenant": "acme"}, wantErr: "https"},
		"http private address":    {inputs: map[string]string{"server": "http://10.0.0.1:8421", "tenant": "acme"}, wantErr: "https"},
		"http metadata address":   {inputs: map[string]string{"server": "http://169.254.169.254", "tenant": "acme"}, wantErr: "https"},
		"http loopback-like name": {inputs: map[string]string{"server": "http://127.0.0.1.example.com", "tenant": "acme"}, wantErr: "https"},
		"ws scheme":               {inputs: map[string]string{"server": "ws://127.0.0.1:8421", "tenant": "acme"}, wantErr: "https"},
		"empty server":            {inputs: map[string]string{"tenant": "acme"}, wantErr: `"server" is required`},
		"empty tenant":            {inputs: map[string]string{"server": "https://by.example.com"}, wantErr: `"tenant" is required`},
		"tenant with slash":       {inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme/admin"}, wantErr: "tenant must be"},
		"tenant with colon":       {inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme:admin"}, wantErr: "tenant must be"},
		"tenant with space":       {inputs: map[string]string{"server": "https://by.example.com", "tenant": "ac me"}, wantErr: "tenant must be"},
		"tenant wildcard":         {inputs: map[string]string{"server": "https://by.example.com", "tenant": "*"}, wantErr: "tenant must be"},
		"tenant prefix wildcard":  {inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme*"}, wantErr: "tenant must be"},
		"tenant look-alike":       {inputs: map[string]string{"server": "https://by.example.com", "tenant": "аcme"}, wantErr: "tenant must be"},
		"tenant zero width":       {inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme​"}, wantErr: "tenant must be"},
		"tenant control":          {inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme\x01"}, wantErr: "tenant must be"},
		"tenant too long":         {inputs: map[string]string{"server": "https://by.example.com", "tenant": strings.Repeat("a", 129)}, wantErr: "tenant must be"},
		"tenant padded":           {inputs: map[string]string{"server": "https://by.example.com", "tenant": " acme"}, wantErr: "whitespace"},
		"jwks override":           {inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme", "jwks_url": "https://evil.example/jwks"}, wantErr: "not defined"},
		"issuer override":         {inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme", "issuer": "https://evil.example"}, wantErr: "not defined"},
		"subject override":        {inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme", "subject": "branchyard:other"}, wantErr: "not defined"},
		"extra required claim":    {inputs: map[string]string{"server": "https://by.example.com", "tenant": "acme"}, extra: map[string]string{"by_purpose": "connect"}, wantErr: "does not accept"},
	}
	for name, tc := range refused {
		t.Run(name, func(t *testing.T) {
			tc.inputs["audience"] = byAudience
			got, err := agentfederation.Derive(agentfederation.PresetBranchyard, tc.inputs, tc.extra, "")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %+v, error %v, want %q", got, err, tc.wantErr)
			}
		})
	}
	// The http loopback exception belongs to this preset alone: the generic
	// presets still refuse a plain http issuer, loopback or not.
	if _, err := agentfederation.Derive(agentfederation.PresetOIDC, map[string]string{"issuer": "http://127.0.0.1:8421", "jwks_url": "http://127.0.0.1:8421/.well-known/jwks.json", "subject": "branchyard:acme", "audience": byAudience}, nil, ""); err == nil {
		t.Fatal("oidc preset accepted an http issuer")
	}
	if err := (agentfederation.Trust{Issuer: "http://127.0.0.1:8421", JWKSURL: "http://127.0.0.1:8421/.well-known/jwks.json", Audience: byAudience, SubjectClaim: "sub", Subject: "branchyard:acme"}).Validate(); err == nil {
		t.Fatal("Trust.Validate accepted an http issuer")
	}
}

// branchyard is a stand-in Branchyard server: an Ed25519 key published at
// /.well-known/jwks.json over TLS on loopback, beside an RSA and an EC key,
// signing tokens shaped as contract section 7 says.
type branchyard struct {
	server  *httptest.Server
	issuer  string
	ed      ed25519.PrivateKey
	rsa     *rsa.PrivateKey
	p256    *ecdsa.PrivateKey
	engine  *agentfederation.Engine
	store   *agentfederation.Store
	tenant  string
	binding agentfederation.Binding
}

func newBranchyard(t *testing.T) *branchyard {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p256, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := []map[string]string{
		{"kty": "OKP", "crv": "Ed25519", "kid": "by-ed", "use": "sig", "alg": "EdDSA", "x": base64.RawURLEncoding.EncodeToString(pub)},
		{"kty": "RSA", "kid": "by-rsa", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()), "e": "AQAB"},
		{"kty": "EC", "crv": "P-256", "kid": "by-p256", "use": "sig", "alg": "ES256", "x": pad(p256.X, 32), "y": pad(p256.Y, 32)},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	t.Cleanup(srv.Close)
	roots := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	store := agentfederation.NewStore(resources.NewMemoryStore())
	b := &branchyard{server: srv, issuer: srv.URL, ed: priv, rsa: rsaKey, p256: p256, store: store, tenant: "acme"}
	b.engine = &agentfederation.Engine{Keys: auth.NewJWKSKeyResolver(endpointpolicy.Config{AllowLoopback: true, TLSConfig: &tls.Config{RootCAs: roots}}), Agents: func(_ context.Context, id string) (agentfederation.Agent, error) {
		return agentfederation.Agent{ClientID: id, Active: true, ActorSubject: "spiffe://aiplex.test/sa/" + id}, nil
	}}
	b.binding = b.bind(t, "acme-agent", "acme")
	return b
}

func (b *branchyard) bind(t *testing.T, agent, tenant string) agentfederation.Binding {
	t.Helper()
	binding, err := agentfederation.NewBinding(agent, "by", agentfederation.BindingRequest{Preset: agentfederation.PresetBranchyard, Inputs: map[string]string{"server": b.issuer, "tenant": tenant, "audience": byAudience}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.Put(context.Background(), "tenant-a", binding, 0); err != nil {
		t.Fatal(err)
	}
	return binding
}

// claims are a turn token's claims as contract section 7 lists them.
func (b *branchyard) claims() map[string]any {
	now := time.Now()
	return map[string]any{"iss": b.issuer, "aud": byAudience, "sub": "branchyard:" + b.tenant, "iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(), "jti": "j-1", "by_purpose": "aiplex", "by_tenant": b.tenant, "by_branch": "app/feature", "by_turn": "t-7"}
}

func (b *branchyard) sign(t *testing.T, alg, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": alg, "kid": kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	var sig []byte
	switch kid {
	case "by-rsa":
		var err error
		if sig, err = rsa.SignPKCS1v15(rand.Reader, b.rsa, crypto.SHA256, digest(crypto.SHA256, input)); err != nil {
			t.Fatal(err)
		}
	case "by-p256":
		sig = ecRaw(b.p256, crypto.SHA256, 32)(t, input)
	default:
		sig = ed25519.Sign(b.ed, []byte(input))
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func firstFailure(r *agentfederation.Result) *agentfederation.Check {
	for i := range r.Checks {
		if r.Checks[i].Status == agentfederation.StatusFail {
			return &r.Checks[i]
		}
	}
	return nil
}

// A Branchyard turn token verifies through the engine and the production JWKS
// resolver, and each way it can be wrong fails the check that says so, both
// when the binding is looked up from the token and when it is pinned.
func TestBranchyardTokenThroughEngine(t *testing.T) {
	b := newBranchyard(t)
	with := func(change func(map[string]any)) map[string]any {
		c := b.claims()
		change(c)
		return c
	}
	cases := []struct {
		name    string
		token   string
		failing string // first failing check, "" when valid
		reason  string
	}{
		{"valid", b.sign(t, "EdDSA", "by-ed", b.claims()), "", ""},
		{"wrong by_purpose", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["by_purpose"] = "connect" })), agentfederation.CheckRequiredClaims, "value differs from the binding: by_purpose"},
		{"by_purpose differs in case", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["by_purpose"] = "AIPlex" })), agentfederation.CheckRequiredClaims, "value differs from the binding: by_purpose"},
		{"missing by_purpose", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { delete(c, "by_purpose") })), agentfederation.CheckRequiredClaims, "missing: by_purpose"},
		{"by_purpose a number", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["by_purpose"] = 1 })), agentfederation.CheckRequiredClaims, "value differs from the binding: by_purpose"},
		{"by_purpose an array", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["by_purpose"] = []string{"aiplex"} })), agentfederation.CheckRequiredClaims, "value differs from the binding: by_purpose"},
		{"by_purpose an object", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["by_purpose"] = map[string]string{"v": "aiplex"} })), agentfederation.CheckRequiredClaims, "value differs from the binding: by_purpose"},
		{"by_purpose null", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["by_purpose"] = nil })), agentfederation.CheckRequiredClaims, "value differs from the binding: by_purpose"},
		{"by_purpose true", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["by_purpose"] = true })), agentfederation.CheckRequiredClaims, "value differs from the binding: by_purpose"},
		{"wrong aud", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["aud"] = "https://other.example.com" })), agentfederation.CheckAudience, "does not include the binding audience"},
		{"aud with a trailing slash", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["aud"] = byAudience + "/" })), agentfederation.CheckAudience, "does not include the binding audience"},
		{"no aud", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { delete(c, "aud") })), agentfederation.CheckAudience, "no aud claim"},
		{"another tenant's sub", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["sub"] = "branchyard:globex" })), agentfederation.CheckSubject, "no binding for issuer"},
		{"sub with a suffix", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["sub"] = "branchyard:acme2" })), agentfederation.CheckSubject, "no binding for issuer"},
		{"sub without the prefix", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["sub"] = "acme" })), agentfederation.CheckSubject, "no binding for issuer"},
		{"expired", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() })), agentfederation.CheckLifetime, "expired"},
		{"lifetime exactly an hour", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["exp"] = c["iat"].(int64) + 3600 })), "", ""},
		{"lifetime an hour and a second", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["exp"] = c["iat"].(int64) + 3601 })), agentfederation.CheckLifetime, "at most 3600 seconds"},
		{"lifetime a hundred years", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["exp"] = c["iat"].(int64) + 100*365*86400 })), agentfederation.CheckLifetime, "at most 3600 seconds"},
		{"no iat", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { delete(c, "iat") })), agentfederation.CheckLifetime, "numeric iat"},
		{"iat a string", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["iat"] = "1700000000" })), agentfederation.CheckLifetime, "iat claim is not a number"},
		{"iat very negative", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["iat"] = int64(-9e18) })), agentfederation.CheckLifetime, "at most 3600 seconds"},
		{"iat beyond int64", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["iat"] = 1e30 })), agentfederation.CheckLifetime, "at most 3600 seconds"},
		{"no exp", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { delete(c, "exp") })), agentfederation.CheckLifetime, "no numeric exp"},
		{"iss with a trailing slash", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["iss"] = b.issuer + "/" })), agentfederation.CheckIssuer, "no agent identity binding in this tenant trusts issuer"},
		{"iss as http", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["iss"] = strings.Replace(b.issuer, "https://", "http://", 1) })), agentfederation.CheckIssuer, "no agent identity binding in this tenant trusts issuer"},
		{"local yard iss", b.sign(t, "EdDSA", "by-ed", with(func(c map[string]any) { c["iss"] = "branchyard:local:abc" })), agentfederation.CheckIssuer, "no agent identity binding in this tenant trusts issuer"},
		{"RS256 from the same key set", b.sign(t, "RS256", "by-rsa", b.claims()), agentfederation.CheckSignature, "preset branchyard accepts EdDSA only"},
		{"ES256 from the same key set", b.sign(t, "ES256", "by-p256", b.claims()), agentfederation.CheckSignature, "preset branchyard accepts EdDSA only"},
		{"EdDSA naming the RSA key", b.sign(t, "EdDSA", "by-rsa", b.claims()), agentfederation.CheckSignature, "invalid JWT signature"},
		{"EdDSA naming the EC key", b.sign(t, "EdDSA", "by-p256", b.claims()), agentfederation.CheckSignature, "invalid JWT signature"},
		{"EdDSA naming an unknown key", b.sign(t, "EdDSA", "by-none", b.claims()), agentfederation.CheckSignature, "unknown JWT signing key"},
		{"Ed25519 as alg", b.sign(t, "Ed25519", "by-ed", b.claims()), agentfederation.CheckTokenShape, "not supported"},
		{"EDDSA", b.sign(t, "EDDSA", "by-ed", b.claims()), agentfederation.CheckTokenShape, "not supported"},
		{"eddsa", b.sign(t, "eddsa", "by-ed", b.claims()), agentfederation.CheckTokenShape, "not supported"},
		{"none", b.sign(t, "none", "by-ed", b.claims()), agentfederation.CheckTokenShape, "not supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, in := range []agentfederation.Input{{Token: tc.token, Finder: b.store.Tenant("tenant-a")}, {Token: tc.token, Pinned: &b.binding}} {
				r := b.engine.Evaluate(context.Background(), in)
				if tc.failing == "" {
					if !r.Valid || r.ActorSubject != "spiffe://aiplex.test/sa/acme-agent" || r.Binding == nil || r.Binding.ClientID != "acme-agent" {
						t.Fatalf("valid token rejected: %+v", r.Checks)
					}
					continue
				}
				if r.Valid || r.VerifiedClaims() != nil {
					t.Fatalf("token accepted: %+v", r.Checks)
				}
				failed := firstFailure(r)
				want := tc.failing
				if in.Pinned != nil && want == agentfederation.CheckIssuer {
					// A pinned binding names its issuer; the token's differs.
					if failed == nil || failed.Name != want || !strings.Contains(failed.Reason, "does not exactly match the binding issuer") {
						t.Fatalf("pinned: first failure %+v", failed)
					}
					continue
				}
				if in.Pinned != nil && want == agentfederation.CheckSubject {
					if failed == nil || failed.Name != want || !strings.Contains(failed.Reason, "does not exactly match the binding subject") {
						t.Fatalf("pinned: first failure %+v", failed)
					}
					continue
				}
				if failed == nil || failed.Name != want || !strings.Contains(failed.Reason, tc.reason) {
					t.Fatalf("first failure %+v, want %s %q", failed, want, tc.reason)
				}
			}
		})
	}
}

// The hint for an algorithm that is never accepted names what the binding's
// preset accepts: never EdDSA to an oidc binding, only EdDSA to a branchyard
// binding, and the RS256/ES256/ES384 list when no binding is known.
func TestUnsupportedAlgorithmHintFollowsThePreset(t *testing.T) {
	b := newBranchyard(t)
	oidc, err := agentfederation.NewBinding("generic-agent", "oidc", agentfederation.BindingRequest{Preset: agentfederation.PresetOIDC, Inputs: map[string]string{"issuer": "https://oidc.example.com", "jwks_url": "https://oidc.example.com/jwks", "subject": "w-1", "audience": byAudience}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.Put(context.Background(), "tenant-a", oidc, 0); err != nil {
		t.Fatal(err)
	}
	oidcClaims := map[string]any{"iss": "https://oidc.example.com", "sub": "w-1", "aud": byAudience, "exp": time.Now().Add(time.Minute).Unix()}
	shape := func(in agentfederation.Input) string {
		t.Helper()
		r := b.engine.Evaluate(context.Background(), in)
		f := firstFailure(r)
		if r.Valid || f == nil || f.Name != agentfederation.CheckTokenShape || !strings.Contains(f.Reason, "not supported") {
			t.Fatalf("first failure %+v", f)
		}
		return f.Reason
	}
	for _, alg := range []string{"none", "HS256", "eddsa"} {
		oidcToken := b.sign(t, alg, "by-ed", oidcClaims)
		for _, in := range []agentfederation.Input{{Token: oidcToken, Pinned: &oidc}, {Token: oidcToken, Finder: b.store.Tenant("tenant-a")}} {
			if msg := shape(in); strings.Contains(msg, "EdDSA") || !strings.Contains(msg, "RS256, ES256 or ES384") {
				t.Fatalf("oidc binding, alg %s: %q", alg, msg)
			}
		}
		byToken := b.sign(t, alg, "by-ed", b.claims())
		for _, in := range []agentfederation.Input{{Token: byToken, Pinned: &b.binding}, {Token: byToken, Finder: b.store.Tenant("tenant-a")}} {
			if msg := shape(in); !strings.Contains(msg, "request an EdDSA token") || strings.Contains(msg, "RS256") {
				t.Fatalf("branchyard binding, alg %s: %q", alg, msg)
			}
		}
		// No binding trusts this issuer: the default list, without EdDSA.
		stray := b.sign(t, alg, "by-ed", map[string]any{"iss": "https://nobody.example.com", "sub": "x", "aud": byAudience, "exp": time.Now().Add(time.Minute).Unix()})
		if msg := shape(agentfederation.Input{Token: stray, Finder: b.store.Tenant("tenant-a")}); strings.Contains(msg, "EdDSA") {
			t.Fatalf("no binding, alg %s: %q", alg, msg)
		}
	}
}

// The one-hour limit is the branchyard preset's: an oidc binding still
// accepts a token that lives for a hundred years, and one without iat.
func TestLifetimeLimitIsBranchyardOnly(t *testing.T) {
	b := newBranchyard(t)
	oidc, err := agentfederation.NewBinding("generic-agent", "oidc", agentfederation.BindingRequest{Preset: agentfederation.PresetOIDC, Inputs: map[string]string{"issuer": b.issuer, "jwks_url": b.issuer + "/.well-known/jwks.json", "subject": "w-1", "audience": byAudience}}, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for name, c := range map[string]map[string]any{
		"a hundred years": {"iss": b.issuer, "sub": "w-1", "aud": byAudience, "iat": now.Unix(), "exp": now.Add(100 * 365 * 24 * time.Hour).Unix()},
		"no iat":          {"iss": b.issuer, "sub": "w-1", "aud": byAudience, "exp": now.Add(2 * time.Hour).Unix()},
	} {
		if r := b.engine.Evaluate(context.Background(), agentfederation.Input{Token: b.sign(t, "RS256", "by-rsa", c), Pinned: &oidc}); !r.Valid {
			t.Fatalf("oidc binding, %s: %+v", name, r.Checks)
		}
	}
}

// One Branchyard server, two tenants, two agents: a tenant's token
// authenticates as its own tenant's agent and never as the other's, and a
// generic binding over the same key set does not accept EdDSA.
func TestBranchyardTenantsStaySeparate(t *testing.T) {
	b := newBranchyard(t)
	globex := b.bind(t, "globex-agent", "globex")
	acmeToken := b.sign(t, "EdDSA", "by-ed", b.claims())
	b.tenant = "globex"
	globexToken := b.sign(t, "EdDSA", "by-ed", b.claims())

	for token, agent := range map[string]string{acmeToken: "acme-agent", globexToken: "globex-agent"} {
		r := b.engine.Evaluate(context.Background(), agentfederation.Input{Token: token, Finder: b.store.Tenant("tenant-a")})
		if !r.Valid || r.Binding.ClientID != agent {
			t.Fatalf("token for %s: %+v", agent, r.Checks)
		}
	}
	// Pinned to the other tenant's binding, each is refused at the subject.
	if r := b.engine.Evaluate(context.Background(), agentfederation.Input{Token: acmeToken, Pinned: &globex}); r.Valid || firstFailure(r).Name != agentfederation.CheckSubject {
		t.Fatalf("acme token against globex binding: %+v", r.Checks)
	}
	if r := b.engine.Evaluate(context.Background(), agentfederation.Input{Token: globexToken, Pinned: &b.binding}); r.Valid || firstFailure(r).Name != agentfederation.CheckSubject {
		t.Fatalf("globex token against acme binding: %+v", r.Checks)
	}
	// The bindings live in tenant-a only.
	if r := b.engine.Evaluate(context.Background(), agentfederation.Input{Token: acmeToken, Finder: b.store.Tenant("tenant-b")}); r.Valid {
		t.Fatalf("acme token in tenant-b: %+v", r.Checks)
	}

	// An oidc binding over the same server and key set accepts RS256 and
	// refuses EdDSA: EdDSA belongs to the branchyard preset.
	oidc, err := agentfederation.NewBinding("generic-agent", "oidc", agentfederation.BindingRequest{Preset: agentfederation.PresetOIDC, Inputs: map[string]string{"issuer": b.issuer, "jwks_url": b.issuer + "/.well-known/jwks.json", "subject": "branchyard:initech", "audience": byAudience}}, "")
	if err != nil {
		t.Fatal(err)
	}
	b.tenant = "initech"
	c := b.claims()
	delete(c, "by_purpose")
	if r := b.engine.Evaluate(context.Background(), agentfederation.Input{Token: b.sign(t, "RS256", "by-rsa", c), Pinned: &oidc}); !r.Valid {
		t.Fatalf("RS256 on an oidc binding: %+v", r.Checks)
	}
	r := b.engine.Evaluate(context.Background(), agentfederation.Input{Token: b.sign(t, "EdDSA", "by-ed", c), Pinned: &oidc})
	if f := firstFailure(r); r.Valid || f.Name != agentfederation.CheckSignature || !strings.Contains(f.Reason, "preset oidc accepts RS256, ES256 or ES384 only") {
		t.Fatalf("EdDSA on an oidc binding: %+v", r.Checks)
	}
	// The resolver's verifier for an oidc binding refuses EdDSA on its own,
	// whatever the engine checked first.
	verifier, err := b.engine.Keys.ForBinding(context.Background(), oidc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifySignature(context.Background(), b.sign(t, "EdDSA", "by-ed", c)); err == nil || !strings.Contains(err.Error(), "JWT requires RS256, ES256 or ES384") {
		t.Fatalf("oidc verifier and EdDSA: %v", err)
	}
	verifier, err = b.engine.Keys.ForBinding(context.Background(), b.binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifySignature(context.Background(), b.sign(t, "RS256", "by-rsa", b.claims())); err == nil || !strings.Contains(err.Error(), "JWT requires EdDSA") {
		t.Fatalf("branchyard verifier and RS256: %v", err)
	}
	// A binding whose preset is unknown verifies nothing.
	unknown := b.binding
	unknown.Preset = "branchyard-v2"
	if _, err := b.engine.Keys.ForBinding(context.Background(), unknown); err == nil {
		t.Fatal("resolver accepted an unknown preset")
	}
	if r := b.engine.Evaluate(context.Background(), agentfederation.Input{Token: acmeToken, Pinned: &unknown}); r.Valid || firstFailure(r).Name != agentfederation.CheckSignature {
		t.Fatalf("unknown preset: %+v", r.Checks)
	}
}

// A development server's http loopback issuer is reachable only where the
// outbound endpoint policy allows loopback, which the API permits only in
// development mode: without it the binding's JWKS URL is refused when saved
// (the API's ValidateJWKS) and when keys are fetched (the resolver).
func TestBranchyardLoopbackIssuerNeedsLoopbackPolicy(t *testing.T) {
	binding, err := agentfederation.NewBinding("acme-agent", "dev", agentfederation.BindingRequest{Preset: agentfederation.PresetBranchyard, Inputs: map[string]string{"server": "http://127.0.0.1:8421", "tenant": "acme", "audience": byAudience}}, "")
	if err != nil {
		t.Fatal(err)
	}
	production := endpointpolicy.Config{AllowedOrigins: []string{"http://127.0.0.1:8421"}}
	if p, err := endpointpolicy.New(production); err == nil {
		if err := p.ValidateURL(binding.JWKSURL); err == nil {
			t.Fatal("production outbound policy accepted a loopback JWKS URL")
		}
	}
	resolver := auth.NewJWKSKeyResolver(endpointpolicy.Config{})
	if _, err := resolver.ForBinding(context.Background(), binding); err == nil {
		t.Fatal("resolver without loopback opt-in accepted a loopback JWKS URL")
	}
}
