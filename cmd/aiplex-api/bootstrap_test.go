package main

import (
	"context"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapValidatesWholeDocumentBeforeWriting(t *testing.T) {
	s := registry.NewMemoryStore()
	path := filepath.Join(t.TempDir(), "bindings.json")
	_ = os.WriteFile(path, []byte(`{"role_bindings":[{"id":"admin","group":"admins","provider":"issuer","pool_id":"users","role":"admin"},{"id":"bad","role":"admin"}]}`), 0600)
	if e := bootstrapRoleBindings(context.Background(), s, path); e == nil {
		t.Fatal("invalid binding accepted")
	}
	b, _ := s.ListRoleBindings(context.Background())
	if len(b) != 0 {
		t.Fatal("partially bootstrapped invalid document")
	}
}
