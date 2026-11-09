package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/membership"
)

type directoryFixtureAuthority struct {
	resolve func(context.Context, membership.Query, time.Time) (membership.Snapshot, error)
}

func (f *directoryFixtureAuthority) Resolve(ctx context.Context, q membership.Query, at time.Time) (membership.Snapshot, error) {
	return f.resolve(ctx, q, at)
}
func (*directoryFixtureAuthority) ConfigurationDigest() string {
	return membership.Digest("directory-authority")
}
func directoryFixtureSnapshot(q membership.Query, at time.Time) membership.Snapshot {
	return membership.Snapshot{Version: membership.Version, Query: q, Authority: "trusted-directory", Ref: contracts.SnapshotRef{ID: "directory-view", Version: "7"}, Groups: []membership.Group{{Source: "independent-directory", ID: q.Role + "-group"}}, Complete: true, Expanded: true, IssuedAt: at.Add(-time.Second), ExpiresAt: at.Add(5 * time.Minute)}
}
func TestDirectoryMembershipSuppliesMissingGroupsAndIndependentActor(t *testing.T) {
	now := time.Now().UTC()
	claims := quotaIdentityClaims(now)
	delete(claims, "groups")
	claims["act"] = map[string]any{"sub": "native-agent"}
	identity := quotaIdentityFixture(t, claims)
	if _, err := PrincipalMembership(identity, "tenant", now); err == nil {
		t.Fatal("missing JWT group view was accepted without directory")
	}
	queries := []membership.Query{}
	authority := &directoryFixtureAuthority{resolve: func(_ context.Context, q membership.Query, at time.Time) (membership.Snapshot, error) {
		queries = append(queries, q)
		return directoryFixtureSnapshot(q, at), nil
	}}
	resolver, err := NewDirectoryMembershipResolver(authority)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := resolver.ResolveQuotaSubject(context.Background(), "tenant", identity, "principal", now)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := resolver.ResolveQuotaSubject(context.Background(), "tenant", identity, "actor", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || queries[0].NativeSubject != identity.Subject || queries[1].NativeSubject != "native-agent" || queries[0].SubjectID == queries[1].SubjectID || principal.Kind != "human" || actor.Kind != "agent" || principal.Membership.Groups[0].ID != "principal-group" || actor.Membership.Groups[0].ID != "actor-group" {
		t.Fatal(principal, actor, queries)
	}
	if identity.VerifiedEvidence.ActorSubject != "native-agent" {
		t.Fatal("actor claim was not pinned at verification")
	}
	identity.ActorSubject = "substituted"
	identity.ActorID = CanonicalPrincipalID(identity.Provider, identity.PoolID, "substituted")
	if _, err = resolver.ResolveQuotaSubject(context.Background(), "tenant", identity, "actor", now); err == nil || len(queries) != 2 {
		t.Fatal("mutable actor replaced signed claim", err)
	}
}
func TestDirectoryMembershipCannotExtendCredentialOrCertificateLifetime(t *testing.T) {
	now := time.Now().UTC()
	identity := quotaIdentityFixture(t, quotaIdentityClaims(now))
	identity.VerifiedEvidence.CredentialExpiresAt = now.Add(10 * time.Second)
	authority := &directoryFixtureAuthority{resolve: func(_ context.Context, q membership.Query, at time.Time) (membership.Snapshot, error) {
		return directoryFixtureSnapshot(q, at), nil
	}}
	resolver, _ := NewDirectoryMembershipResolver(authority)
	subject, err := resolver.ResolveQuotaSubject(context.Background(), "tenant", identity, "principal", now)
	if err != nil || subject.Membership.ExpiresAt != identity.VerifiedEvidence.CredentialExpiresAt {
		t.Fatal(subject, err)
	}
	if _, err = resolver.ResolveQuotaSubject(context.Background(), "tenant", identity, "principal", now.Add(10*time.Second)); err == nil {
		t.Fatal("expired certificate revived by directory")
	}
}
func TestDirectoryMembershipFailsClosedWithoutClaimsFallback(t *testing.T) {
	now := time.Now().UTC()
	identity := quotaIdentityFixture(t, quotaIdentityClaims(now))
	queries := 0
	authority := &directoryFixtureAuthority{resolve: func(context.Context, membership.Query, time.Time) (membership.Snapshot, error) {
		queries++
		return membership.Snapshot{}, membership.ErrUnavailable
	}}
	resolver, _ := NewDirectoryMembershipResolver(authority)
	if _, err := resolver.ResolveQuotaSubject(context.Background(), "tenant", identity, "principal", now); !errors.Is(err, membership.ErrUnavailable) {
		t.Fatal("signed groups bypassed required directory", err)
	}
	for _, change := range []func(*models.WIFIdentity){func(i *models.WIFIdentity) { i.VerifiedEvidence = nil }, func(i *models.WIFIdentity) { i.Provider = "other-issuer" }, func(i *models.WIFIdentity) { i.PrincipalID = "other-principal" }} {
		copy := *identity
		change(&copy)
		if _, err := resolver.ResolveQuotaSubject(context.Background(), "tenant", &copy, "principal", now); err == nil {
			t.Fatal("unverified identity obtained directory view")
		}
	}
	if queries != 1 {
		t.Fatal("invalid credentials reached directory", queries)
	}
}
func TestDirectoryMembershipRejectsCrossNamespaceAndPartialSnapshots(t *testing.T) {
	now := time.Now().UTC()
	identity := quotaIdentityFixture(t, quotaIdentityClaims(now))
	for _, change := range []func(*membership.Snapshot){func(s *membership.Snapshot) { s.Query.TenantID = "other" }, func(s *membership.Snapshot) { s.Query.Role = "actor" }, func(s *membership.Snapshot) { s.Complete = false }, func(s *membership.Snapshot) { s.Expanded = false }, func(s *membership.Snapshot) { s.Groups = nil }, func(s *membership.Snapshot) { s.Groups = append(s.Groups, s.Groups[0]) }} {
		authority := &directoryFixtureAuthority{resolve: func(_ context.Context, q membership.Query, at time.Time) (membership.Snapshot, error) {
			s := directoryFixtureSnapshot(q, at)
			change(&s)
			return s, nil
		}}
		resolver, _ := NewDirectoryMembershipResolver(authority)
		if _, err := resolver.ResolveQuotaSubject(context.Background(), "tenant", identity, "principal", now); err == nil {
			t.Fatal("invalid directory proof accepted")
		}
	}
}
