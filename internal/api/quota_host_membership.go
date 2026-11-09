package api

import (
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"time"
)

func quotaResolvedSubjectValid(s quotaplex.Subject, tenant, id, kind string, now time.Time, maxAge time.Duration) bool {
	m := s.Membership
	if s.ID != id || s.Kind != kind || m.TenantID != tenant || m.SubjectID != id || quotaplex.ValidateStoreCoordinate(tenant, m.Ref.ID, m.Ref.Version) != nil || m.Groups == nil || len(m.Groups) > 1024 || m.IssuedAt.IsZero() || m.IssuedAt.After(now) || !m.ExpiresAt.After(now) || !m.ExpiresAt.After(m.IssuedAt) || !m.IssuedAt.Add(maxAge).After(now) {
		return false
	}
	seen := map[quotaplex.GroupRef]bool{}
	for _, g := range m.Groups {
		if quotaplex.ValidateStoreCoordinate(tenant, g.Source, g.ID) != nil || seen[g] {
			return false
		}
		seen[g] = true
	}
	return true
}
func quotaClampSubjectExpiry(s *quotaplex.Subject, maxAge time.Duration) {
	expiry := s.Membership.IssuedAt.Add(maxAge)
	if expiry.Before(s.Membership.ExpiresAt) {
		s.Membership.ExpiresAt = expiry
	}
	s.Membership.Groups = append([]quotaplex.GroupRef{}, s.Membership.Groups...)
}

// New authoritative observations may renew observation refs/times without
// changing eligibility. Preserve the admission's original evidence and expiry;
// a fresh complete proof may confirm the same exact source-qualified group set.
func quotaSameSubjectEligibility(a, b quotaplex.Subject) bool {
	if a.ID != b.ID || a.Kind != b.Kind || a.Membership.TenantID != b.Membership.TenantID || a.Membership.SubjectID != b.Membership.SubjectID || len(a.Membership.Groups) != len(b.Membership.Groups) {
		return false
	}
	groups := map[quotaplex.GroupRef]bool{}
	for _, group := range a.Membership.Groups {
		groups[group] = true
	}
	for _, group := range b.Membership.Groups {
		if !groups[group] {
			return false
		}
	}
	return true
}

func quotaSameActorEligibility(a, b *quotaplex.Subject) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return quotaSameSubjectEligibility(*a, *b)
}
