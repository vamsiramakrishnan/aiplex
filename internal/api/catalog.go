package api

import (
	"net/http"
	"strconv"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// CatalogHandler serves the catalog browsing endpoints.
type CatalogHandler struct {
	aggregator *catalog.Aggregator
	store      registry.Store
}

// NewCatalogHandler creates a catalog API handler.
func NewCatalogHandler(agg *catalog.Aggregator, store registry.Store) *CatalogHandler {
	return &CatalogHandler{aggregator: agg, store: store}
}

// List returns paginated catalog templates, optionally filtered by capability kind.
// GET /api/v1/catalog?kind=tool&page=0&page_size=20
func (h *CatalogHandler) List(w http.ResponseWriter, r *http.Request) {
	kind := capability.Kind(r.URL.Query().Get("kind"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	result := h.aggregator.Fetch(r.Context(), kind)

	total := len(result.Templates)
	start := page * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	var sourceErrors []models.SourceError
	if len(result.Errors) > 0 {
		sourceErrors = result.Errors
	}

	JSON(w, http.StatusOK, models.CatalogPage{
		Templates:     result.Templates[start:end],
		Total:         total,
		Page:          page,
		PageSize:      pageSize,
		SourcesFailed: sourceErrors,
	})
}

// Get returns a single template by ID.
// GET /api/v1/catalog/{id}
func (h *CatalogHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := h.store.GetTemplate(r.Context(), id)
	if err != nil {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "template not found")
		return
	}
	JSON(w, http.StatusOK, t)
}
