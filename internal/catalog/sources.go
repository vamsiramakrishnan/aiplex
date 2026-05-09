package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// Source provides templates from a single catalog origin.
type Source interface {
	// Name returns a human-readable identifier for this source.
	Name() string
	// Kind returns the capability kind this source serves.
	Kind() capability.Kind
	// Fetch retrieves all templates from this source.
	Fetch(ctx context.Context) ([]models.Template, error)
}

// Aggregator merges templates from multiple sources per kind.
type Aggregator struct {
	sources map[capability.Kind][]Source
}

// NewAggregator creates a catalog aggregator with the given sources grouped by kind.
func NewAggregator(sources []Source) *Aggregator {
	m := make(map[capability.Kind][]Source)
	for _, s := range sources {
		m[s.Kind()] = append(m[s.Kind()], s)
	}
	return &Aggregator{sources: m}
}

// FetchResult holds templates and any per-source errors.
type FetchResult struct {
	Templates []models.Template
	Errors    []models.SourceError
}

// Fetch retrieves templates from all sources for the given kind.
// If kind is empty, all kinds are fetched. Partial failures are reported in Errors.
func (a *Aggregator) Fetch(ctx context.Context, kind capability.Kind) FetchResult {
	var result FetchResult
	for k, srcs := range a.sources {
		if kind != "" && k != kind {
			continue
		}
		for _, src := range srcs {
			templates, err := src.Fetch(ctx)
			if err != nil {
				result.Errors = append(result.Errors, models.SourceError{
					Source: src.Name(),
					Error:  err.Error(),
				})
				continue
			}
			result.Templates = append(result.Templates, templates...)
		}
	}
	return result
}
