package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/membership"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

type quotaDirectoryFixture struct {
	manifest, revision string
	queries            []membership.Query
	unavailable        bool
}

func (d *quotaDirectoryFixture) ConfigurationDigest() string { return membership.Digest(d.manifest) }
func (d *quotaDirectoryFixture) Resolve(_ context.Context, q membership.Query, at time.Time) (membership.Snapshot, error) {
	d.queries = append(d.queries, q)
	if d.unavailable {
		return membership.Snapshot{}, membership.ErrUnavailable
	}
	group := q.Role + "-group"
	if d.revision == "revoked" {
		group = q.Role + "-revoked-group"
	}
	return membership.Snapshot{Version: membership.Version, Query: q, Authority: "directory", Ref: contracts.SnapshotRef{ID: "view", Version: d.revision}, Groups: []membership.Group{{Source: "directory", ID: group}}, Complete: true, Expanded: true, IssuedAt: at.Add(-time.Minute), ExpiresAt: at.Add(time.Minute)}, nil
}
func TestQuotaHostDirectoryMembershipRechecksBeforeDispatchAndSeparatesActor(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	store := quotaplex.NewMemoryStore()
	cfg := quotaHostTestConfig(now)
	directory := &quotaDirectoryFixture{manifest: "authority-v1", revision: "7"}
	resolver, err := auth.NewDirectoryMembershipResolver(directory, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewQuotaHost(context.Background(), cfg, store, func() time.Time { return now }, nil, nil, WithQuotaMembershipResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	identity := quotaHostTestIdentity(t, now, func(c map[string]any) { delete(c, "groups"); c["act"] = map[string]any{"sub": "native-actor"} })
	r, in := quotaHostTestInvocation(identity, "directory-attempt", `{"contents":[]}`)
	lease, err := host.Prepare(context.Background(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(directory.queries) != 2 || directory.queries[0].Role != "principal" || directory.queries[1].Role != "actor" || directory.queries[0].SubjectID == directory.queries[1].SubjectID {
		t.Fatal(directory.queries)
	}
	directory.revision = "revoked"
	quotaHostTestStatus(t, lease.BeforeDispatch(context.Background()), 409)
	if err := lease.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, err := host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", quotaHostTestAttempt(t, lease))
	if err != nil || attempt.Decision.State != "aborted" || attempt.Claim.Fence != 0 || attempt.Demand.Principal.Membership.Groups[0].ID != "principal-group" || attempt.Demand.Actor.Membership.Groups[0].ID != "actor-group" {
		t.Fatal(attempt, err)
	}
}

func TestQuotaHostFreshDirectoryObservationPreservesPinnedAdmissionExpiry(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	directory := &quotaDirectoryFixture{manifest: "authority-v1", revision: "7"}
	resolver, _ := auth.NewDirectoryMembershipResolver(directory, func() time.Time { return now })
	host, err := NewQuotaHost(context.Background(), quotaHostTestConfig(now), quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil, WithQuotaMembershipResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	identity := quotaHostTestIdentity(t, now, func(c map[string]any) { delete(c, "groups") })
	r, in := quotaHostTestInvocation(identity, "fresh-directory-attempt", `{"contents":[]}`)
	lease, err := host.Prepare(context.Background(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", quotaHostTestAttempt(t, lease))
	if err != nil {
		t.Fatal(err)
	}
	directory.revision = "8"
	now = now.Add(time.Second)
	if err = lease.BeforeDispatch(context.Background()); err != nil {
		t.Fatal("renewed complete directory observation changed equivalent eligibility", err)
	}
	after, err := host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", quotaHostTestAttempt(t, lease))
	if err != nil || after.Demand.Principal.Membership.Ref.Version != "7" || after.Demand.Principal.Membership.ExpiresAt != pinned.Demand.Principal.Membership.ExpiresAt || after.Claim.Fence == 0 {
		t.Fatal("dispatch rewrote or extended original membership evidence", after, err)
	}
}
func TestQuotaHostDirectoryTrustManifestRequiresPolicyRevision(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	store := quotaplex.NewMemoryStore()
	cfg := quotaHostTestConfig(now)
	first, _ := auth.NewDirectoryMembershipResolver(&quotaDirectoryFixture{manifest: "authority-v1", revision: "7"}, func() time.Time { return now })
	if _, err := NewQuotaHost(context.Background(), cfg, store, func() time.Time { return now }, nil, nil, WithQuotaMembershipResolver(first)); err != nil {
		t.Fatal(err)
	}
	changed, _ := auth.NewDirectoryMembershipResolver(&quotaDirectoryFixture{manifest: "authority-v2", revision: "7"}, func() time.Time { return now })
	if _, err := NewQuotaHost(context.Background(), cfg, store, func() time.Time { return now }, nil, nil, WithQuotaMembershipResolver(changed)); !errors.Is(err, quotaplex.ErrConflict) {
		t.Fatal("changed directory trust silently reused active host epoch", err)
	}
}

type liveObservationDirectory struct{}

func (liveObservationDirectory) ConfigurationDigest() string {
	return membership.Digest("fresh-per-read-directory")
}
func (liveObservationDirectory) Resolve(_ context.Context, q membership.Query, _ time.Time) (membership.Snapshot, error) {
	// Native directory observation happens after the lookup began. The host must
	// validate at receipt without manufacturing or rewriting this timestamp.
	at := time.Now().UTC()
	return membership.Snapshot{Version: membership.Version, Query: q, Authority: "directory", Ref: contracts.SnapshotRef{ID: "view", Version: at.Format(time.RFC3339Nano)}, Groups: []membership.Group{}, Complete: true, Expanded: true, IssuedAt: at, ExpiresAt: at.Add(time.Minute)}, nil
}
func TestQuotaHostAcceptsDirectoryObservationTakenDuringLookup(t *testing.T) {
	now := time.Now().UTC()
	resolver, _ := auth.NewDirectoryMembershipResolver(liveObservationDirectory{})
	host, err := NewQuotaHost(context.Background(), quotaHostTestConfig(now), quotaplex.NewMemoryStore(), time.Now, nil, nil, WithQuotaMembershipResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	identity := quotaHostTestIdentity(t, now, func(c map[string]any) { delete(c, "groups") })
	r, in := quotaHostTestInvocation(identity, "live-directory-attempt", `{"contents":[]}`)
	lease, err := host.Prepare(context.Background(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.BeforeDispatch(context.Background()); err != nil {
		t.Fatal("fresh observation timestamp was treated as future or a false eligibility change", err)
	}
}
