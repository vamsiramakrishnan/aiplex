package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// Source provides templates from a single catalog origin.
type Source interface {
	// Name returns a human-readable identifier for this source.
	Name() string
	// Plane returns which plane this source serves.
	Plane() models.Plane
	// Fetch retrieves all templates from this source.
	Fetch(ctx context.Context) ([]models.Template, error)
}

// Aggregator merges templates from multiple sources per plane.
type Aggregator struct {
	sources map[models.Plane][]Source
}

// NewAggregator creates a catalog aggregator with the given sources grouped by plane.
func NewAggregator(sources []Source) *Aggregator {
	m := make(map[models.Plane][]Source)
	for _, s := range sources {
		m[s.Plane()] = append(m[s.Plane()], s)
	}
	return &Aggregator{sources: m}
}

// FetchResult holds templates and any per-source errors.
type FetchResult struct {
	Templates []models.Template
	Errors    []models.SourceError
}

// Fetch retrieves templates from all sources for the given plane.
// If plane is empty, all planes are fetched. Partial failures are reported in Errors.
func (a *Aggregator) Fetch(ctx context.Context, plane models.Plane) FetchResult {
	var result FetchResult
	for p, srcs := range a.sources {
		if plane != "" && p != plane {
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
