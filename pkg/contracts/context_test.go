package contracts

import (
	"testing"
	"time"
)

func TestMembershipBindingAndExpiry(t *testing.T) {
	now := time.Now()
	ref := SnapshotRef{ID: "membership", Version: "7"}
	r := RequestContext{Version: Version, TenantID: "tenant-a", RequestID: "request", PrincipalID: "user", ResourceID: "model", MembershipSnapshotRef: &ref}
	s := MembershipSnapshot{Version: Version, Ref: ref, TenantID: r.TenantID, PrincipalID: r.PrincipalID, Groups: []string{"entra:engineering", "entra:research"}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute)}
	if err := s.ValidateFor(r, now); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*MembershipSnapshot){func(s *MembershipSnapshot) { s.TenantID = "tenant-b" }, func(s *MembershipSnapshot) { s.PrincipalID = "other" }, func(s *MembershipSnapshot) { s.Ref.Version = "6" }, func(s *MembershipSnapshot) { s.ExpiresAt = now }, func(s *MembershipSnapshot) { s.IssuedAt = now.Add(time.Minute) }, func(s *MembershipSnapshot) { s.Groups = []string{"group", "group"} }} {
		copy := s
		mutate(&copy)
		if err := copy.ValidateFor(r, now); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
}

func TestRequestContextValidation(t *testing.T) {
	r := RequestContext{Version: Version, TenantID: "tenant", RequestID: "req", PrincipalID: "user", ResourceID: "resource"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.MembershipSnapshotRef = &SnapshotRef{ID: "missing-version"}
	if err := r.Validate(); err == nil {
		t.Fatal("incomplete reference accepted")
	}
	r.MembershipSnapshotRef = nil
	r.Version = "v2"
	if err := r.Validate(); err == nil {
		t.Fatal("unknown contract accepted")
	}
}
