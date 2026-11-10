package userfederation

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

const (
	testAudience = "aiplex-client"
	testTenant   = "72f988bf-86f1-41af-91ab-2d7cd011db47"
)

var testKey, otherKey = mustKey(), mustKey()

func mustKey() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}

func sign(t *testing.T, key *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	if header == nil {
		header = map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"}
	}
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type rsaVerifier struct{ key *rsa.PublicKey }

func (v rsaVerifier) VerifySignature(_ context.Context, token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(v.key, crypto.SHA256, digest[:], sig) != nil {
		return nil, errors.New("invalid JWT signature")
	}
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.UseNumber()
	var claims map[string]any
	return claims, dec.Decode(&claims)
}

type staticKeys struct{ err error }

func (k staticKeys) ForProvider(context.Context, Provider) (SignatureVerifier, error) {
	if k.err != nil {
		return nil, k.err
	}
	return rsaVerifier{&testKey.PublicKey}, nil
}

func entraProvider(t *testing.T, id, groupsFrom string) Provider {
	t.Helper()
	in := map[string]string{"tenant_id": testTenant, "audience": testAudience, "groups_from": groupsFrom}
	p, err := NewProvider(id, ProviderRequest{Preset: PresetEntraID, Inputs: in})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func entraClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": "https://login.microsoftonline.com/" + testTenant + "/v2.0", "aud": testAudience, "sub": "pairwise-sub", "oid": "00000000-0000-0000-0000-000000000042",
		"tid": testTenant, "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Add(-time.Minute).Unix(), "nbf": now.Add(-time.Minute).Unix(),
		"groups": []string{"g-b", "g-a"}, "secret_marker": "do-not-echo-this-value",
	}
}

type fixture struct {
	store  *Store
	engine *Engine
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	f := &fixture{store: NewStore(resources.NewMemoryStore()), now: now}
	f.engine = &Engine{Keys: staticKeys{}, Now: func() time.Time { return now }}
	if _, err := f.store.Put(context.Background(), "tenant-a", entraProvider(t, "corp", "claim"), 0); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) eval(t *testing.T, token string) *Result {
	t.Helper()
	return f.engine.Evaluate(context.Background(), Input{Token: token, Finder: f.store.Tenant("tenant-a")})
}

func statusOf(r *Result, name string) (CheckStatus, string) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c.Status, c.Reason
		}
	}
	return "", ""
}

func TestEvaluateHappyPath(t *testing.T) {
	f := newFixture(t)
	r := f.eval(t, sign(t, testKey, nil, entraClaims(f.now)))
	if !r.Valid || r.Provider == nil || r.Provider.ID != "corp" {
		t.Fatalf("result = %+v", r)
	}
	if got := r.VerifiedSubject(); got != "00000000-0000-0000-0000-000000000042" {
		t.Fatalf("subject must be the immutable oid, got %q", got)
	}
	if !r.GroupsAvailable || r.Groups == nil || strings.Join(*r.Groups, ",") != "g-a,g-b" {
		t.Fatalf("groups = %+v", r.Groups)
	}
	names := []string{CheckTokenShape, CheckIssuer, CheckSignature, CheckAudience, CheckLifetime, CheckSubject, CheckRequiredClaims, CheckProofOfPossession, CheckClient, CheckGroups, CheckProvider}
	for i, c := range r.Checks {
		if c.Name != names[i] || c.Status != StatusPass {
			t.Fatalf("check %d = %+v", i, c)
		}
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "do-not-echo-this-value") {
		t.Fatalf("arbitrary claim value echoed: %s", raw)
	}
}

func TestEvaluateFailingChecks(t *testing.T) {
	f := newFixture(t)
	good := func() map[string]any { return entraClaims(f.now) }
	mut := func(fn func(map[string]any)) string {
		c := good()
		fn(c)
		return sign(t, testKey, nil, c)
	}
	cases := []struct {
		name  string
		token string
		check string
		after []string // checks that must be skipped or failed (not pass) after the first failure
	}{
		{"not a jwt", "garbage", CheckTokenShape, nil},
		{"jwe", "a.b.c.d.e", CheckTokenShape, nil},
		{"alg none", sign(t, testKey, map[string]any{"alg": "none", "kid": "k1"}, good()), CheckTokenShape, nil},
		{"no kid", sign(t, testKey, map[string]any{"alg": "RS256"}, good()), CheckTokenShape, nil},
		{"unknown issuer", mut(func(c map[string]any) { c["iss"] = "https://evil.example" }), CheckIssuer, []string{CheckSignature}},
		{"missing issuer", mut(func(c map[string]any) { delete(c, "iss") }), CheckIssuer, nil},
		{"issuer trailing slash", mut(func(c map[string]any) { c["iss"] = c["iss"].(string) + "/" }), CheckIssuer, nil},
		{"other tenant issuer", mut(func(c map[string]any) {
			c["iss"] = "https://login.microsoftonline.com/11111111-1111-1111-1111-111111111111/v2.0"
		}), CheckIssuer, nil},
		{"bad signature", sign(t, otherKey, nil, good()), CheckSignature, nil},
		{"wrong audience", mut(func(c map[string]any) { c["aud"] = "someone-else" }), CheckAudience, nil},
		{"missing audience", mut(func(c map[string]any) { delete(c, "aud") }), CheckAudience, nil},
		{"expired", mut(func(c map[string]any) { c["exp"] = f.now.Add(-time.Minute).Unix() }), CheckLifetime, nil},
		{"no exp", mut(func(c map[string]any) { delete(c, "exp") }), CheckLifetime, nil},
		{"not yet valid", mut(func(c map[string]any) { c["nbf"] = f.now.Add(time.Hour).Unix() }), CheckLifetime, nil},
		{"issued in future", mut(func(c map[string]any) { c["iat"] = f.now.Add(time.Hour).Unix() }), CheckLifetime, nil},
		{"no oid", mut(func(c map[string]any) { delete(c, "oid") }), CheckSubject, nil},
		{"non-string oid", mut(func(c map[string]any) { c["oid"] = 42 }), CheckSubject, nil},
		{"missing tid", mut(func(c map[string]any) { delete(c, "tid") }), CheckRequiredClaims, nil},
		{"wrong tid", mut(func(c map[string]any) { c["tid"] = "11111111-1111-1111-1111-111111111111" }), CheckRequiredClaims, nil},
		{"cnf", mut(func(c map[string]any) { c["cnf"] = map[string]any{"x5t#S256": "x"} }), CheckProofOfPossession, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := f.eval(t, c.token)
			if r.Valid || r.VerifiedClaims() != nil || r.MatchedProvider() != nil || r.VerifiedSubject() != "" {
				t.Fatalf("invalid token accepted: %+v", r)
			}
			failed := r.Failed()
			if failed == nil || failed.Name != c.check || failed.Reason == "" {
				t.Fatalf("first failure = %+v, want %s (checks %+v)", failed, c.check, r.Checks)
			}
			if r.Groups != nil || r.GroupsAvailable {
				t.Fatalf("groups exposed for invalid token: %+v", r)
			}
			if len(r.Checks) != 11 {
				t.Fatalf("expected the full ordered check list, got %d", len(r.Checks))
			}
			if st, _ := statusOf(r, CheckGroups); st == StatusPass {
				t.Fatal("groups read from an invalid token")
			}
		})
	}
}

func TestEvaluateKeyFailures(t *testing.T) {
	f := newFixture(t)
	token := sign(t, testKey, nil, entraClaims(f.now))
	f.engine.Keys = staticKeys{err: errors.New("JWKS fetch denied")}
	r := f.eval(t, token)
	if st, reason := statusOf(r, CheckSignature); r.Valid || st != StatusFail || !strings.Contains(reason, "JWKS fetch denied") {
		t.Fatalf("signature = %s %s", st, reason)
	}
	f.engine.Keys = nil
	if r := f.eval(t, token); r.Valid {
		t.Fatal("accepted without key resolver")
	}
}

func TestEvaluateDisabledProvider(t *testing.T) {
	f := newFixture(t)
	p, _ := f.store.Get(context.Background(), "tenant-a", "corp")
	p.Disabled = true
	if _, err := f.store.Put(context.Background(), "tenant-a", p, p.Revision); err != nil {
		t.Fatal(err)
	}
	r := f.eval(t, sign(t, testKey, nil, entraClaims(f.now)))
	if st, reason := statusOf(r, CheckProvider); r.Valid || st != StatusFail || !strings.Contains(reason, "disabled") {
		t.Fatalf("provider check = %s %s", st, reason)
	}
}

func TestGroupHandling(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name      string
		mutate    func(map[string]any)
		available bool
		groups    []string
		note      string
	}{
		{"present", nil, true, []string{"g-a", "g-b"}, ""},
		{"duplicates collapse", func(c map[string]any) { c["groups"] = []string{"b", "a", "b"} }, true, []string{"a", "b"}, ""},
		{"authoritative empty", func(c map[string]any) { c["groups"] = []string{} }, true, []string{}, ""},
		{"missing", func(c map[string]any) { delete(c, "groups") }, false, nil, "missing"},
		{"null", func(c map[string]any) { c["groups"] = nil }, false, nil, "null"},
		{"string not array", func(c map[string]any) { c["groups"] = "g-a" }, false, nil, "not an array"},
		{"object", func(c map[string]any) { c["groups"] = map[string]any{"a": 1} }, false, nil, "not an array"},
		{"mixed types", func(c map[string]any) { c["groups"] = []any{"a", 7} }, false, nil, "well-formed"},
		{"empty entry", func(c map[string]any) { c["groups"] = []any{"a", ""} }, false, nil, "well-formed"},
		{"control character", func(c map[string]any) { c["groups"] = []any{"a\nb"} }, false, nil, "well-formed"},
		{"padded entry", func(c map[string]any) { c["groups"] = []any{" a"} }, false, nil, "well-formed"},
		{"entra overage claim names", func(c map[string]any) {
			delete(c, "groups")
			c["_claim_names"] = map[string]any{"groups": "src1"}
			c["_claim_sources"] = map[string]any{"src1": map[string]any{"endpoint": "https://graph.windows.net/x"}}
		}, false, nil, "overage"},
		{"overage with stale list", func(c map[string]any) { c["_claim_names"] = map[string]any{"groups": "src1"} }, false, nil, "overage"},
		{"hasgroups", func(c map[string]any) { delete(c, "groups"); c["hasgroups"] = true }, false, nil, "overage"},
		{"malformed _claim_names", func(c map[string]any) { c["_claim_names"] = "x" }, false, nil, "overage"},
		{"unrelated _claim_names ignored", func(c map[string]any) { c["_claim_names"] = map[string]any{"other": "s"} }, true, []string{"g-a", "g-b"}, ""},
		{"too many", func(c map[string]any) {
			big := make([]string, 1025)
			for i := range big {
				big[i] = "g" + strings.Repeat("x", 3) + string(rune('a'+i%26)) + strings.Repeat("y", i/26)
			}
			c["groups"] = big
		}, false, nil, "1024"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			claims := entraClaims(f.now)
			if c.mutate != nil {
				c.mutate(claims)
			}
			r := f.eval(t, sign(t, testKey, nil, claims))
			if !r.Valid {
				t.Fatalf("authentication must not depend on groups: %+v", r.Checks)
			}
			if r.GroupsAvailable != c.available {
				t.Fatalf("available = %v, note %q", r.GroupsAvailable, r.GroupsNote)
			}
			raw, _ := json.Marshal(r)
			if c.available {
				if r.Groups == nil || strings.Join(*r.Groups, ",") != strings.Join(c.groups, ",") || r.GroupsNote != "" {
					t.Fatalf("groups = %+v", r.Groups)
				}
				if c.name == "authoritative empty" && !strings.Contains(string(raw), `"groups":[]`) {
					t.Fatalf("authoritative empty list must serialise as []: %s", raw)
				}
			} else {
				if r.Groups != nil || strings.Contains(string(raw), `"groups":`) || !strings.Contains(r.GroupsNote, c.note) {
					t.Fatalf("unavailable groups must be absent with a reason (%q): %s", c.note, raw)
				}
				if st, _ := statusOf(r, CheckGroups); st != StatusSkipped {
					t.Fatalf("groups check = %s", st)
				}
			}
		})
	}
}

func TestMembershipAuthorityIgnoresTokenGroups(t *testing.T) {
	f := newFixture(t)
	p, err := NewProvider("authority", ProviderRequest{Preset: PresetEntraID, Inputs: map[string]string{"tenant_id": testTenant, "audience": "other-aud", "groups_from": "membership-authority"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Put(context.Background(), "tenant-a", p, 0); err != nil {
		t.Fatal(err)
	}
	claims := entraClaims(f.now)
	claims["aud"] = "other-aud"
	r := f.eval(t, sign(t, testKey, nil, claims))
	if !r.Valid || r.GroupsAvailable || r.Groups != nil || !strings.Contains(r.GroupsNote, "membership authority") {
		t.Fatalf("result = %+v", r)
	}
}

func TestDottedGroupClaim(t *testing.T) {
	trust := Trust{GroupsFrom: GroupsFromClaim, GroupClaim: "google.sign_in_attributes.groups"}
	claims := map[string]any{"google": map[string]any{"sign_in_attributes": map[string]any{"groups": []any{"x", "y"}}}}
	if g := ExtractGroups(claims, trust); !g.Available || strings.Join(g.List, ",") != "x,y" {
		t.Fatalf("nested = %+v", g)
	}
	if g := ExtractGroups(map[string]any{"google": "oops"}, trust); g.Available {
		t.Fatal("non-object parent accepted")
	}
	flat := map[string]any{"google.sign_in_attributes.groups": []any{"z"}}
	if g := ExtractGroups(flat, trust); !g.Available || g.List[0] != "z" {
		t.Fatalf("exact top-level dotted name = %+v", g)
	}
}

func TestWorkspaceHostedDomainPinning(t *testing.T) {
	f := newFixture(t)
	p, err := NewProvider("workspace", ProviderRequest{Preset: PresetGoogleWorkspace, Inputs: map[string]string{"hosted_domain": "example.com", "audience": "gclient", "groups_from": "membership-authority"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Put(context.Background(), "tenant-a", p, 0); err != nil {
		t.Fatal(err)
	}
	base := func() map[string]any {
		return map[string]any{"iss": "https://accounts.google.com", "aud": "gclient", "sub": "1234567890", "hd": "example.com", "exp": f.now.Add(time.Minute).Unix(), "iat": f.now.Unix()}
	}
	if r := f.eval(t, sign(t, testKey, nil, base())); !r.Valid {
		t.Fatalf("workspace user rejected: %+v", r.Checks)
	}
	for name, mutate := range map[string]func(map[string]any){
		"other domain":     func(c map[string]any) { c["hd"] = "evil.example" },
		"personal account": func(c map[string]any) { delete(c, "hd") },
		"hd wrong type":    func(c map[string]any) { c["hd"] = []string{"example.com"} },
	} {
		c := base()
		mutate(c)
		r := f.eval(t, sign(t, testKey, nil, c))
		if r.Valid || r.Failed() == nil || r.Failed().Name != CheckRequiredClaims {
			t.Fatalf("%s: %+v", name, r.Checks)
		}
	}
}

func TestAmbiguousAudienceFailsClosed(t *testing.T) {
	f := newFixture(t)
	p, err := NewProvider("second", ProviderRequest{Preset: PresetEntraID, Inputs: map[string]string{"tenant_id": testTenant, "audience": "second-aud", "groups_from": "claim"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Put(context.Background(), "tenant-a", p, 0); err != nil {
		t.Fatal(err)
	}
	claims := entraClaims(f.now)
	claims["aud"] = []string{testAudience, "second-aud"}
	r := f.eval(t, sign(t, testKey, nil, claims))
	if st, reason := statusOf(r, CheckIssuer); r.Valid || st != StatusFail || !strings.Contains(reason, "unambiguous") {
		t.Fatalf("issuer = %s %s", st, reason)
	}
	// Each audience alone selects its own provider.
	claims["aud"] = "second-aud"
	if r := f.eval(t, sign(t, testKey, nil, claims)); !r.Valid || r.Provider.ID != "second" {
		t.Fatalf("second: %+v", r)
	}
}

func TestPinnedEvaluation(t *testing.T) {
	f := newFixture(t)
	p, _ := f.store.Get(context.Background(), "tenant-a", "corp")
	token := sign(t, testKey, nil, entraClaims(f.now))
	if r := f.engine.Evaluate(context.Background(), Input{Token: token, Pinned: &p}); !r.Valid {
		t.Fatalf("pinned: %+v", r.Checks)
	}
	c := entraClaims(f.now)
	c["iss"] = "https://login.microsoftonline.com/11111111-1111-1111-1111-111111111111/v2.0"
	if r := f.engine.Evaluate(context.Background(), Input{Token: sign(t, testKey, nil, c), Pinned: &p}); r.Valid || r.Failed().Name != CheckIssuer {
		t.Fatalf("pinned mismatch: %+v", r.Checks)
	}
}
