package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// LocalSource serves templates from the store (Firestore or in-memory).
type LocalSource struct {
	store registry.Store
	plane models.Plane
}

// NewLocalSource creates a store-backed catalog source for the given plane.
func NewLocalSource(store registry.Store, plane models.Plane) *LocalSource {
	return &LocalSource{store: store, plane: plane}
}

func (l *LocalSource) Name() string       { return "local:" + string(l.plane) }
func (l *LocalSource) Plane() models.Plane { return l.plane }

func (l *LocalSource) Fetch(ctx context.Context) ([]models.Template, error) {
	templates, _, err := l.store.ListTemplates(ctx, l.plane, 0, 10000)
	return templates, err
}
