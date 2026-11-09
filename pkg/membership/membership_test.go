package membership

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
)

func fixtureQuery() Query {
	return Query{TenantID: "tenant", SubjectID: "canonical-subject", Kind: "agent", Issuer: "https://issuer.example", PoolID: "workload", NativeSubject: "native-agent", Role: "principal"}
}
func fixtureSnapshot(q Query, now time.Time) Snapshot {
	return Snapshot{Version: Version, Query: q, Authority: "directory", Ref: contracts.SnapshotRef{ID: "directory-observation", Version: "7"}, Groups: []Group{{Source: "native-directory", ID: "research"}}, Complete: true, Expanded: true, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)}
}
func TestSignedHTTPAuthorityRejectsWrongProofAndPreservesIndependentSubject(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	q := fixtureQuery()
	mode := "valid"
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer private-directory-token" {
			t.Error("unbound directory transport")
		}
		var got Query
		if json.NewDecoder(r.Body).Decode(&got) != nil {
			t.Error("query absent")
		}
		signed := SignedSnapshot{Snapshot: fixtureSnapshot(got, now), QueryDigest: Digest(got), KeyID: "key"}
		switch mode {
		case "tenant":
			signed.Snapshot.Query.TenantID = "other"
		case "role":
			signed.Snapshot.Query.Role = "actor"
		case "partial":
			signed.Snapshot.Complete = false
		case "unexpanded":
			signed.Snapshot.Expanded = false
		case "missing-groups":
			signed.Snapshot.Groups = nil
		case "expired":
			signed.Snapshot.ExpiresAt = now
		case "future":
			signed.Snapshot.IssuedAt = now.Add(time.Hour)
		case "unapproved-source":
			signed.Snapshot.Groups[0].Source = "spoofed-directory"
		case "too-long":
			signed.Snapshot.ExpiresAt = now.Add(24 * time.Hour)
		case "wrong-request":
			signed.QueryDigest = Digest("wrong")
		}
		raw, _ := SigningBytes(signed)
		signed.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, raw))
		if mode == "signature" {
			signed.Signature = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
		}
		if mode == "unknown-key" {
			signed.KeyID = "untrusted"
		}
		if mode == "redirect" {
			w.Header().Set("Location", "https://unapproved.example")
			w.WriteHeader(302)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if mode == "duplicate-envelope" {
			raw, _ := json.Marshal(signed)
			_, _ = w.Write([]byte(strings.Replace(string(raw), `"key_id":"key"`, `"key_id":"key","KEY_ID":"key"`, 1)))
			return
		}
		_ = json.NewEncoder(w).Encode(signed)
	}))
	defer server.Close()
	policy, err := endpointpolicy.New(endpointpolicy.Config{AllowedOrigins: []string{server.URL}, AllowLoopback: true, TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig})
	if err != nil {
		t.Fatal(err)
	}
	cfg := HTTPConfig{Endpoint: server.URL + "/resolve", Authority: "directory", AllowedSources: []string{"native-directory"}, PublicKeys: map[string]string{"key": base64.RawURLEncoding.EncodeToString(pub)}, MaxAge: 5 * time.Minute}
	authority, err := NewHTTP(cfg, policy.Client(), func(context.Context, string) (string, error) { return "private-directory-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	cfg.PublicKeys["key"] = "mutated"
	cfg.AllowedSources[0] = "mutated"
	snapshot, err := authority.Resolve(context.Background(), q, now)
	if err != nil || snapshot.Query != q || snapshot.Ref.Version != "7" {
		t.Fatal(snapshot, err)
	}
	snapshot.Groups[0].ID = "mutated"
	if again, err := authority.Resolve(context.Background(), q, now); err != nil || again.Groups[0].ID != "research" {
		t.Fatal(again, err)
	}
	for _, name := range []string{"tenant", "role", "partial", "unexpanded", "missing-groups", "expired", "future", "unapproved-source", "too-long", "wrong-request", "signature", "unknown-key", "redirect", "duplicate-envelope"} {
		t.Run(name, func(t *testing.T) {
			mode = name
			if _, err := authority.Resolve(context.Background(), q, now); err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
	if calls != 16 {
		t.Fatal(calls)
	}
}
func TestDirectoryExpansionIsCompleteBoundedAndCycleAware(t *testing.T) {
	leaf := Group{"directory", "leaf"}
	parent := Group{"directory", "parent"}
	root := Group{"directory", "root"}
	groups, err := Expand([]Group{leaf, parent}, map[Group][]Group{leaf: {parent}, parent: {root}}, 4)
	if err != nil || len(groups) != 3 {
		t.Fatal(groups, err)
	}
	if _, err = Expand([]Group{leaf}, map[Group][]Group{leaf: {parent}, parent: {leaf}}, 4); err == nil {
		t.Fatal("cyclic partial group view accepted")
	}
	if _, err = Expand([]Group{leaf}, map[Group][]Group{leaf: {parent}, parent: {root}}, 2); err == nil {
		t.Fatal("expansion bound ignored")
	}
	empty, err := Expand([]Group{}, nil, 4)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
}

type fixtureAuthority struct {
	resolve func(context.Context, Query, time.Time) (Snapshot, error)
}

func (f *fixtureAuthority) Resolve(ctx context.Context, q Query, now time.Time) (Snapshot, error) {
	return f.resolve(ctx, q, now)
}
func (*fixtureAuthority) ConfigurationDigest() string { return Digest("fixture-authority") }
func TestCacheDoesNotRenewStaleDirectoryEvidenceOrServeOnSourceFailure(t *testing.T) {
	now := time.Now().UTC()
	q := fixtureQuery()
	var calls atomic.Int32
	source := &fixtureAuthority{resolve: func(_ context.Context, q Query, at time.Time) (Snapshot, error) {
		calls.Add(1)
		if calls.Load() > 1 {
			return Snapshot{}, ErrUnavailable
		}
		return fixtureSnapshot(q, now), nil
	}}
	cache, err := NewCache(source, 10*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	first, err := cache.Resolve(context.Background(), q, now)
	if err != nil {
		t.Fatal(err)
	}
	first.Groups[0].ID = "mutated"
	second, err := cache.Resolve(context.Background(), q, now.Add(time.Second))
	if err != nil || second.Groups[0].ID != "research" || second.IssuedAt != now.Add(-time.Second) || calls.Load() != 1 {
		t.Fatal(second, err, calls.Load())
	}
	if _, err = cache.Resolve(context.Background(), q, now.Add(11*time.Second)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("stale fallback accepted", err)
	}
	// A different role or tenant cannot hit the principal's cached view.
	other := q
	other.Role = "actor"
	if _, err = cache.Resolve(context.Background(), other, now); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
func TestCacheRevocationFencesInflightReadAndRequiresNewObservation(t *testing.T) {
	now := time.Now().UTC()
	q := fixtureQuery()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	source := &fixtureAuthority{resolve: func(context.Context, Query, time.Time) (Snapshot, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return fixtureSnapshot(q, now), nil
	}}
	cache, _ := NewCache(source, time.Minute, 10)
	result := make(chan error, 1)
	go func() { _, err := cache.Resolve(context.Background(), q, now); result <- err }()
	<-entered
	if err := cache.Invalidate(q, now); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrUnavailable) {
		t.Fatal("pre-revocation inflight result repopulated cache", err)
	}
	if _, err := cache.Resolve(context.Background(), q, now); !errors.Is(err, ErrUnavailable) {
		t.Fatal("old signed observation survived invalidation", err)
	}
	source.resolve = func(context.Context, Query, time.Time) (Snapshot, error) {
		s := fixtureSnapshot(q, now)
		s.IssuedAt = now
		s.Ref.Version = "8"
		return s, nil
	}
	if s, err := cache.Resolve(context.Background(), q, now); err != nil || s.Ref.Version != "8" {
		t.Fatal(s, err)
	}
}
func TestCacheConcurrentReadsCoalesceAndOwnSnapshots(t *testing.T) {
	now := time.Now().UTC()
	q := fixtureQuery()
	var calls atomic.Int32
	source := &fixtureAuthority{resolve: func(context.Context, Query, time.Time) (Snapshot, error) {
		calls.Add(1)
		return fixtureSnapshot(q, now), nil
	}}
	cache, _ := NewCache(source, time.Minute, 10)
	var wait sync.WaitGroup
	for i := 0; i < 30; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			s, err := cache.Resolve(context.Background(), q, now)
			if err != nil {
				t.Error(err)
				return
			}
			s.Groups[0].ID = "private-copy"
		}()
	}
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatal("directory stampede", calls.Load())
	}
}

func TestCacheCannotTurnMissingGroupsIntoAuthoritativeEmptyView(t *testing.T) {
	now := time.Now().UTC()
	q := fixtureQuery()
	source := &fixtureAuthority{resolve: func(context.Context, Query, time.Time) (Snapshot, error) {
		s := fixtureSnapshot(q, now)
		s.Groups = nil
		return s, nil
	}}
	cache, _ := NewCache(source, time.Minute, 10)
	for i := 0; i < 2; i++ {
		s, err := cache.Resolve(context.Background(), q, now)
		if err != nil {
			t.Fatal(err)
		}
		if s.Groups != nil || Validate(s, q, "directory", []string{"native-directory"}, now, 5*time.Minute) == nil {
			t.Fatal("copy/cache synthesized complete empty membership from missing groups", s)
		}
	}
}
