package agentfederation

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

const testAudience = "https://aiplex.example.com"

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

// rsaVerifier is a minimal stand-in for the JWKS verifier (the JWKS path is
// covered end to end in internal/auth).
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

func (k staticKeys) ForBinding(context.Context, Binding) (SignatureVerifier, error) {
	if k.err != nil {
		return nil, k.err
	}
	return rsaVerifier{&testKey.PublicKey}, nil
}

type agentTable map[string]Agent

func (a agentTable) lookup(_ context.Context, id string) (Agent, error) {
	agent, ok := a[id]
	if !ok {
		return Agent{}, ErrNotFound
	}
	return agent, nil
}

func ghBinding(t *testing.T, client, id, repo string) Binding {
	t.Helper()
	b, err := NewBinding(client, id, BindingRequest{Preset: PresetGitHubActions, Inputs: map[string]string{"repository": repo, "ref": "refs/heads/main", "repository_id": "4242"}}, testAudience)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ghClaims(now time.Time, repo string) map[string]any {
	return map[string]any{"iss": "https://token.actions.githubusercontent.com", "sub": "repo:" + repo + ":ref:refs/heads/main", "aud": testAudience, "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Add(-time.Minute).Unix(), "nbf": now.Add(-time.Minute).Unix(), "repository_id": "4242", "secret_marker": "do-not-echo-this-value"}
}

type fixture struct {
	store  *Store
	engine *Engine
	agents agentTable
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	f := &fixture{store: NewStore(resources.NewMemoryStore()), agents: agentTable{"tutor-agent": {ClientID: "tutor-agent", Active: true, ActorSubject: "spiffe://aiplex/sa/tutor-agent"}, "idle-agent": {ClientID: "idle-agent", Active: false, ActorSubject: "idle-agent"}}, now: now}
	f.engine = &Engine{Keys: staticKeys{}, Agents: f.agents.lookup, Now: func() time.Time { return now }}
	if _, err := f.store.Put(context.Background(), "tenant-a", ghBinding(t, "tutor-agent", "ci", "octo-org/octo-repo"), 0); err != nil {
		t.Fatal(err)
	}
	return f
}

func checkStatus(r *Result, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{}
}

func TestEngineAcceptsMatchingToken(t *testing.T) {
	f := newFixture(t)
	token := sign(t, testKey, nil, ghClaims(f.now, "octo-org/octo-repo"))
	r := f.engine.Evaluate(context.Background(), Input{Token: token, Finder: f.store.Tenant("tenant-a")})
	if !r.Valid || r.Binding == nil || r.Binding.ClientID != "tutor-agent" || r.ActorSubject != "spiffe://aiplex/sa/tutor-agent" || r.VerifiedClaims() == nil {
		t.Fatalf("expected valid result, got %+v", r)
	}
	wantOrder := []string{CheckTokenShape, CheckIssuer, CheckSignature, CheckAudience, CheckLifetime, CheckSubject, CheckRequiredClaims, CheckProofOfPossession, CheckBinding}
	if len(r.Checks) != len(wantOrder) {
		t.Fatalf("checks = %+v", r.Checks)
	}
	for i, c := range r.Checks {
		if c.Name != wantOrder[i] || c.Status != StatusPass || c.Reason == "" {
			t.Fatalf("check %d = %+v", i, c)
		}
	}
}

func TestEngineReportsEachFailingCheck(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	disabled := ghBinding(t, "tutor-agent", "off", "octo-org/disabled")
	disabled.Disabled = true
	orphan := ghBinding(t, "ghost-agent", "ghost", "octo-org/ghost")
	idle := ghBinding(t, "idle-agent", "idle", "octo-org/idle")
	for _, b := range []Binding{disabled, orphan, idle} {
		if _, err := f.store.Put(ctx, "tenant-a", b, 0); err != nil {
			t.Fatal(err)
		}
	}
	finder := f.store.Tenant("tenant-a")
	mutate := func(change func(map[string]any)) map[string]any {
		c := ghClaims(f.now, "octo-org/octo-repo")
		change(c)
		return c
	}
	tests := []struct {
		name   string
		token  func() string
		keys   KeyResolver
		check  string
		reason string
	}{
		{"garbage", func() string { return "not-a-jwt" }, nil, CheckTokenShape, "segments"},
		{"bearer prefix", func() string { return "Bearer abc.def.ghi" }, nil, CheckTokenShape, "whitespace"},
		{"jwe", func() string { return "a.b.c.d.e" }, nil, CheckTokenShape, "JWE"},
		{"es384", func() string {
			return sign(t, testKey, map[string]any{"alg": "ES384", "kid": "k"}, ghClaims(f.now, "octo-org/octo-repo"))
		}, nil, CheckTokenShape, "ES384"},
		{"no kid", func() string {
			return sign(t, testKey, map[string]any{"alg": "RS256"}, ghClaims(f.now, "octo-org/octo-repo"))
		}, nil, CheckTokenShape, "kid"},
		{"unknown issuer", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["iss"] = "https://evil.example.com" }))
		}, nil, CheckIssuer, "no agent identity binding"},
		{"wrong key", func() string { return sign(t, otherKey, nil, ghClaims(f.now, "octo-org/octo-repo")) }, nil, CheckSignature, "invalid JWT signature"},
		{"keys unavailable", func() string { return sign(t, testKey, nil, ghClaims(f.now, "octo-org/octo-repo")) }, staticKeys{err: errors.New("fetch JWKS: dial refused")}, CheckSignature, "signing keys unavailable"},
		{"audience", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["aud"] = []string{"https://other.example.com"} }))
		}, nil, CheckAudience, "does not include"},
		{"expired", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["exp"] = f.now.Add(-time.Hour).Unix() }))
		}, nil, CheckLifetime, "expired"},
		{"no exp", func() string { return sign(t, testKey, nil, mutate(func(c map[string]any) { delete(c, "exp") })) }, nil, CheckLifetime, "no numeric exp"},
		{"not yet valid", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["nbf"] = f.now.Add(time.Hour).Unix() }))
		}, nil, CheckLifetime, "not valid until"},
		{"subject", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["sub"] = "repo:octo-org/octo-repo:pull_request" }))
		}, nil, CheckSubject, "no binding for issuer"},
		{"required claim mismatch", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["repository_id"] = "9999" }))
		}, nil, CheckRequiredClaims, "value differs from the binding: repository_id"},
		{"required claim missing", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { delete(c, "repository_id") }))
		}, nil, CheckRequiredClaims, "missing: repository_id"},
		{"cnf on bearer preset", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["cnf"] = map[string]any{"x5t#S256": "abc"} }))
		}, nil, CheckProofOfPossession, "cnf"},
		{"disabled binding", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["sub"] = "repo:octo-org/disabled:ref:refs/heads/main" }))
		}, nil, CheckBinding, "disabled"},
		{"unregistered agent", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["sub"] = "repo:octo-org/ghost:ref:refs/heads/main" }))
		}, nil, CheckBinding, "not registered"},
		{"inactive agent", func() string {
			return sign(t, testKey, nil, mutate(func(c map[string]any) { c["sub"] = "repo:octo-org/idle:ref:refs/heads/main" }))
		}, nil, CheckBinding, "not active"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := *f.engine
			if tc.keys != nil {
				engine.Keys = tc.keys
			}
			token := tc.token()
			r := engine.Evaluate(ctx, Input{Token: token, Finder: finder})
			if r.Valid || r.VerifiedClaims() != nil || r.MatchedBinding() != nil {
				t.Fatalf("expected invalid result: %+v", r.Checks)
			}
			c := checkStatus(r, tc.check)
			if c.Status != StatusFail || !strings.Contains(c.Reason, tc.reason) {
				t.Fatalf("check %s = %+v, want failure containing %q; all: %+v", tc.check, c, tc.reason, r.Checks)
			}
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), "do-not-echo-this-value") || strings.Contains(string(encoded), "9999") {
				t.Fatalf("result leaks token or claim values: %s", encoded)
			}
			if len(r.Checks) != 9 {
				t.Fatalf("every check must be reported: %+v", r.Checks)
			}
		})
	}
}

func TestEngineUnverifiedClaimsAreFlagged(t *testing.T) {
	f := newFixture(t)
	token := sign(t, otherKey, nil, ghClaims(f.now, "octo-org/octo-repo"))
	r := f.engine.Evaluate(context.Background(), Input{Token: token, Finder: f.store.Tenant("tenant-a")})
	if c := checkStatus(r, CheckAudience); !strings.Contains(c.Reason, "claims not verified") {
		t.Fatalf("claim checks after a failed signature must be flagged as unverified: %+v", c)
	}
}

func TestEnginePinnedBindingExplainsMismatch(t *testing.T) {
	f := newFixture(t)
	pinned, err := f.store.Get(context.Background(), "tenant-a", "tutor-agent", "ci")
	if err != nil {
		t.Fatal(err)
	}
	claims := ghClaims(f.now, "octo-org/octo-repo")
	claims["iss"] = "https://token.actions.githubusercontent.com/"
	r := f.engine.Evaluate(context.Background(), Input{Token: sign(t, testKey, nil, claims), Pinned: &pinned})
	if c := checkStatus(r, CheckIssuer); c.Status != StatusFail || !strings.Contains(c.Reason, "does not exactly match") {
		t.Fatalf("issuer check = %+v", c)
	}
	claims = ghClaims(f.now, "octo-org/other")
	r = f.engine.Evaluate(context.Background(), Input{Token: sign(t, testKey, nil, claims), Pinned: &pinned})
	if c := checkStatus(r, CheckSubject); c.Status != StatusFail || !strings.Contains(c.Reason, "does not exactly match the binding subject") {
		t.Fatalf("subject check = %+v", c)
	}
}

func TestStoreRejectsDuplicateSubjectAndIsolatesTenants(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	// Same subject, different agent, same tenant: rejected.
	_, err := f.store.Put(ctx, "tenant-a", ghBinding(t, "idle-agent", "dup", "octo-org/octo-repo"), 0)
	var dup *DuplicateSubjectError
	if !errors.As(err, &dup) || dup.ClientID != "tutor-agent" || dup.BindingID != "ci" {
		t.Fatalf("duplicate subject error = %v", err)
	}
	// Same subject for the same agent under a different binding id: also rejected.
	if _, err = f.store.Put(ctx, "tenant-a", ghBinding(t, "tutor-agent", "ci-2", "octo-org/octo-repo"), 0); !errors.Is(err, ErrDuplicateSubject) {
		t.Fatalf("second binding of the same subject = %v", err)
	}
	// Other tenant: independent.
	if _, err = f.store.Put(ctx, "tenant-b", ghBinding(t, "idle-agent", "dup", "octo-org/octo-repo"), 0); err != nil {
		t.Fatalf("tenant-b binding: %v", err)
	}
	if _, err = f.store.Get(ctx, "tenant-b", "tutor-agent", "ci"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant-b sees tenant-a binding: %v", err)
	}
	b, err := f.store.FindBySubject(ctx, "tenant-b", "https://token.actions.githubusercontent.com", "sub", "repo:octo-org/octo-repo:ref:refs/heads/main")
	if err != nil || b.ClientID != "idle-agent" {
		t.Fatalf("tenant-b lookup = %+v %v", b, err)
	}
	token := sign(t, testKey, nil, ghClaims(f.now, "octo-org/octo-repo"))
	if r := f.engine.Evaluate(ctx, Input{Token: token, Finder: f.store.Tenant("tenant-c")}); r.Valid || checkStatus(r, CheckIssuer).Status != StatusFail {
		t.Fatalf("tenant without bindings must not accept the token: %+v", r.Checks)
	}
	// Replacing the subject releases the old one; deleting releases the new one.
	current, _ := f.store.Get(ctx, "tenant-a", "tutor-agent", "ci")
	moved := ghBinding(t, "tutor-agent", "ci", "octo-org/moved")
	if _, err = f.store.Put(ctx, "tenant-a", moved, current.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Put(ctx, "tenant-a", ghBinding(t, "idle-agent", "dup", "octo-org/octo-repo"), 0); err != nil {
		t.Fatalf("released subject should be bindable: %v", err)
	}
	current, _ = f.store.Get(ctx, "tenant-a", "tutor-agent", "ci")
	if err = f.store.Delete(ctx, "tenant-a", "tutor-agent", "ci", current.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FindBySubject(ctx, "tenant-a", moved.Issuer, "sub", moved.Subject); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted binding still resolvable: %v", err)
	}
	// Stale revision is a conflict.
	if _, err = f.store.Put(ctx, "tenant-a", ghBinding(t, "idle-agent", "dup", "octo-org/x"), 99); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v", err)
	}
}

func TestStaleIndexEntryIsTakenOverAfterGracePeriod(t *testing.T) {
	ctx := context.Background()
	mem := resources.NewMemoryStore()
	store := NewStore(mem)
	b := ghBinding(t, "tutor-agent", "ci", "octo-org/octo-repo")
	payload, _ := json.Marshal(subjectIndex{ClientID: "gone", BindingID: "gone", Issuer: b.Issuer, SubjectClaim: "sub", Subject: b.Subject, ReservedAt: time.Now().Unix()})
	if _, err := mem.Put(ctx, resources.Record{TenantID: "t", Kind: SubjectIndexKind, ID: b.subjectKey(), Spec: payload}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, "t", b, 0); !errors.Is(err, ErrDuplicateSubject) {
		t.Fatalf("fresh reservation must block (in-flight write): %v", err)
	}
	rec, _ := mem.Get(ctx, "t", SubjectIndexKind, b.subjectKey())
	payload, _ = json.Marshal(subjectIndex{ClientID: "gone", BindingID: "gone", Issuer: b.Issuer, SubjectClaim: "sub", Subject: b.Subject, ReservedAt: time.Now().Add(-time.Hour).Unix()})
	if _, err := mem.Put(ctx, resources.Record{TenantID: "t", Kind: SubjectIndexKind, ID: b.subjectKey(), Spec: payload}, rec.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, "t", b, 0); err != nil {
		t.Fatalf("orphaned reservation should be reclaimed: %v", err)
	}
	if got, err := store.FindBySubject(ctx, "t", b.Issuer, "sub", b.Subject); err != nil || got.ClientID != "tutor-agent" {
		t.Fatal(fmt.Sprint(got, err))
	}
}
