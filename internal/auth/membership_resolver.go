package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/membership"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// DirectoryMembershipResolver bridges authenticated identity to an explicitly
// configured directory. Authentication and group authority remain separate;
// missing JWT groups are permitted only because this resolver supplies evidence.
// It never falls back to claims when the configured directory is unavailable.
type DirectoryMembershipResolver struct {
	authority membership.Authority
	clock     func() time.Time
}

func NewDirectoryMembershipResolver(authority membership.Authority, clocks ...func() time.Time) (*DirectoryMembershipResolver, error) {
	if !membership.ValidAuthority(authority) {
		return nil, fmt.Errorf("membership authority with immutable manifest required")
	}
	clock := time.Now
	if len(clocks) > 1 || (len(clocks) == 1 && clocks[0] == nil) {
		return nil, membership.ErrInvalidEvidence
	}
	if len(clocks) == 1 {
		clock = clocks[0]
	}
	return &DirectoryMembershipResolver{authority: authority, clock: clock}, nil
}
func (r *DirectoryMembershipResolver) ConfigurationDigest() string {
	return r.authority.ConfigurationDigest()
}

// VerifiedQuotaIdentity checks credential proof without requiring group claims.
func VerifiedQuotaIdentity(identity *models.WIFIdentity, now time.Time) error {
	if identity == nil || identity.VerifiedEvidence == nil {
		return membership.ErrInvalidEvidence
	}
	e := identity.VerifiedEvidence
	if !e.LifetimeValid || e.Issuer == "" || e.PoolID == "" || e.Subject == "" || identity.Provider != e.Issuer || identity.PoolID != e.PoolID || identity.Subject != e.Subject || identity.PrincipalID != CanonicalPrincipalID(e.Issuer, e.PoolID, e.Subject) || e.IssuedAt.IsZero() || e.IssuedAt.After(now) || !e.ExpiresAt.After(now) || !e.ExpiresAt.After(e.IssuedAt) || (!e.CredentialExpiresAt.IsZero() && !e.CredentialExpiresAt.After(now)) {
		return membership.ErrInvalidEvidence
	}
	if identity.ActorSubject != e.ActorSubject {
		return membership.ErrInvalidEvidence
	}
	if identity.ActorID != "" && (e.ActorSubject == "" || identity.ActorID != CanonicalPrincipalID(e.Issuer, e.PoolID, e.ActorSubject)) {
		return membership.ErrInvalidEvidence
	}
	if identity.ActorID == "" && e.ActorSubject != "" {
		return membership.ErrInvalidEvidence
	}
	return nil
}
func (r *DirectoryMembershipResolver) ResolveQuotaSubject(ctx context.Context, tenant string, identity *models.WIFIdentity, role string, at time.Time) (quotaplex.Subject, error) {
	if err := VerifiedQuotaIdentity(identity, at); err != nil {
		return quotaplex.Subject{}, err
	}
	id, native, kind := identity.PrincipalID, identity.Subject, "agent"
	if identity.IsWorkforce {
		kind = "human"
	}
	if role == "actor" {
		id, native, kind = identity.ActorID, identity.ActorSubject, "agent"
	}
	query := membership.Query{TenantID: tenant, SubjectID: id, Kind: kind, Issuer: identity.Provider, PoolID: identity.PoolID, NativeSubject: native, Role: role}
	if query.Validate() != nil {
		return quotaplex.Subject{}, membership.ErrInvalidEvidence
	}
	snapshot, err := r.authority.Resolve(ctx, query, at)
	if err != nil {
		return quotaplex.Subject{}, err
	}
	if checkedAt := r.clock().UTC(); checkedAt.After(at) {
		at = checkedAt
	}
	if err := VerifiedQuotaIdentity(identity, at); err != nil {
		return quotaplex.Subject{}, err
	}
	// Authority implementations must validate complete proofs; keep identity,
	// freshness and shape checks here too for custom injected implementations.
	if snapshot.Query != query || snapshot.Version != membership.Version || !snapshot.Complete || !snapshot.Expanded || snapshot.Groups == nil || len(snapshot.Groups) > 1024 || !membership.Text(snapshot.Authority) || !membership.Text(snapshot.Ref.ID) || !membership.Text(snapshot.Ref.Version) || snapshot.IssuedAt.IsZero() || snapshot.IssuedAt.After(at) || !snapshot.ExpiresAt.After(at) || !snapshot.ExpiresAt.After(snapshot.IssuedAt) {
		return quotaplex.Subject{}, membership.ErrInvalidEvidence
	}
	expiry := snapshot.ExpiresAt
	e := identity.VerifiedEvidence
	if e.ExpiresAt.Before(expiry) {
		expiry = e.ExpiresAt
	}
	if !e.CredentialExpiresAt.IsZero() && e.CredentialExpiresAt.Before(expiry) {
		expiry = e.CredentialExpiresAt
	}
	groups := make([]quotaplex.GroupRef, 0, len(snapshot.Groups))
	seen := map[membership.Group]bool{}
	for _, g := range snapshot.Groups {
		if !membership.Text(g.Source) || !membership.Text(g.ID) || seen[g] {
			return quotaplex.Subject{}, membership.ErrInvalidEvidence
		}
		seen[g] = true
		groups = append(groups, quotaplex.GroupRef{Source: g.Source, ID: g.ID})
	}
	return quotaplex.Subject{ID: id, Kind: kind, Membership: quotaplex.Membership{Ref: snapshot.Ref, TenantID: tenant, SubjectID: id, Groups: groups, IssuedAt: snapshot.IssuedAt, ExpiresAt: expiry}}, nil
}
