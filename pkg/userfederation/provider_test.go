package userfederation

import (
	"context"
	"errors"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestDuplicateIssuerAudienceRejected(t *testing.T) {
	ctx := context.Background()
	s := NewStore(resources.NewMemoryStore())
	if _, err := s.Put(ctx, "tenant-a", entraProvider(t, "corp", "claim"), 0); err != nil {
		t.Fatal(err)
	}
	// Same issuer+audience under a different ID and a different group source.
	_, err := s.Put(ctx, "tenant-a", entraProvider(t, "corp-copy", "membership-authority"), 0)
	var dup *DuplicateSourceError
	if !errors.As(err, &dup) || dup.ProviderID != "corp" || !errors.Is(err, ErrDuplicateSource) {
		t.Fatalf("duplicate = %v", err)
	}
	if _, err := s.Get(ctx, "tenant-a", "corp-copy"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected provider persisted: %v", err)
	}
	// Different audience on the same issuer is allowed.
	other, _ := NewProvider("corp-b", ProviderRequest{Preset: PresetEntraID, Inputs: map[string]string{"tenant_id": testTenant, "audience": "b", "groups_from": "claim"}})
	if _, err := s.Put(ctx, "tenant-a", other, 0); err != nil {
		t.Fatal(err)
	}
	// Re-putting the same provider (replace) is not a duplicate.
	cur, _ := s.Get(ctx, "tenant-a", "corp")
	cur.DisplayName = "Corporate"
	if _, err := s.Put(ctx, "tenant-a", cur, cur.Revision); err != nil {
		t.Fatal(err)
	}
	// Moving a provider onto another's issuer+audience is rejected and keeps the old source reserved.
	moved := other
	moved.Audience = testAudience
	cur, _ = s.Get(ctx, "tenant-a", "corp-b")
	if _, err := s.Put(ctx, "tenant-a", moved, cur.Revision); !errors.As(err, &dup) {
		t.Fatalf("move onto taken source = %v", err)
	}
	// Deleting releases the source.
	cur, _ = s.Get(ctx, "tenant-a", "corp")
	if err := s.Delete(ctx, "tenant-a", "corp", cur.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "tenant-a", entraProvider(t, "corp-copy", "claim"), 0); err != nil {
		t.Fatalf("source not released: %v", err)
	}
}

func TestTenantIsolationAndRevisions(t *testing.T) {
	ctx := context.Background()
	s := NewStore(resources.NewMemoryStore())
	p := entraProvider(t, "corp", "claim")
	if _, err := s.Put(ctx, "tenant-a", p, 0); err != nil {
		t.Fatal(err)
	}
	// The same issuer+audience in another tenant is independent.
	if _, err := s.Put(ctx, "tenant-b", p, 0); err != nil {
		t.Fatalf("tenant-b: %v", err)
	}
	if _, err := s.Get(ctx, "tenant-c", "corp"); !errors.Is(err, ErrNotFound) {
		t.Fatal("tenant-c sees providers")
	}
	if list, _ := s.List(ctx, "tenant-c"); len(list) != 0 {
		t.Fatal("tenant-c lists providers")
	}
	if list, _ := s.Tenant("tenant-c").ByIssuer(ctx, p.Issuer); len(list) != 0 {
		t.Fatal("tenant-c finds providers by issuer")
	}
	if list, _ := s.Tenant("tenant-a").ByIssuer(ctx, p.Issuer); len(list) != 1 {
		t.Fatalf("tenant-a by issuer = %d", len(list))
	}
	// Create over existing and stale-revision writes conflict.
	if _, err := s.Put(ctx, "tenant-a", p, 0); err == nil {
		t.Fatal("create over existing accepted")
	}
	cur, _ := s.Get(ctx, "tenant-a", "corp")
	if _, err := s.Put(ctx, "tenant-a", cur, cur.Revision+7); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v", err)
	}
	if err := s.Delete(ctx, "tenant-a", "corp", cur.Revision+7); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete = %v", err)
	}
	// Deleting in one tenant leaves the other.
	if err := s.Delete(ctx, "tenant-a", "corp", cur.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "tenant-b", "corp"); err != nil {
		t.Fatal("tenant-b provider deleted with tenant-a's")
	}
}
