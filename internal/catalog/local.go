package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// LocalSource serves templates from the store (Firestore or in-memory),
// filtered to a single capability kind.
type LocalSource struct {
	store registry.Store
	kind  capability.Kind
}

// NewLocalSource creates a store-backed catalog source for the given kind.
func NewLocalSource(store registry.Store, kind capability.Kind) *LocalSource {
	return &LocalSource{store: store, kind: kind}
}

func (l *LocalSource) Name() string             { return "local:" + string(l.kind) }
func (l *LocalSource) Kind() capability.Kind    { return l.kind }

func (l *LocalSource) Fetch(ctx context.Context) ([]models.Template, error) {
	templates, _, err := l.store.ListTemplates(ctx, l.kind, 0, 10000)
	return templates, err
}
