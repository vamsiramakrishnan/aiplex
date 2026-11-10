package catalog

import (
	"context"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

type fixedSource struct {
	name      string
	templates []models.Template
}

func (f fixedSource) Name() string        { return f.name }
func (f fixedSource) Plane() models.Plane { return models.PlaneLLMPlex }
func (f fixedSource) Fetch(context.Context) ([]models.Template, error) {
	return f.templates, nil
}

// The built-in providers are stored locally and also served by their own
// source; the catalog must list each template once.
func TestAggregatorListsATemplateOnce(t *testing.T) {
	tpl := models.Template{ID: "gemini-2.5-flash", Plane: models.PlaneLLMPlex, Source: "builtin"}
	agg := NewAggregator([]Source{
		fixedSource{"local", []models.Template{tpl}},
		fixedSource{"builtin", []models.Template{tpl, {ID: "other", Plane: models.PlaneLLMPlex}}},
	})
	got := agg.Fetch(context.Background(), models.PlaneLLMPlex).Templates
	if len(got) != 2 {
		t.Fatalf("got %d templates, want 2: %+v", len(got), got)
	}
}
