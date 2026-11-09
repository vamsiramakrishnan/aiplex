package api

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// CatalogHandler serves the catalog browsing endpoints.
type CatalogHandler struct {
	aggregator *catalog.Aggregator
	store      registry.CatalogStore
	enabled    map[models.Plane]bool
}

// NewCatalogHandler creates a catalog API handler.
func NewCatalogHandler(agg *catalog.Aggregator, store registry.CatalogStore) *CatalogHandler {
	return &CatalogHandler{aggregator: agg, store: store}
}

// WithEnabledPlanes restricts discovery to active modules. An explicit empty
// list disables every plane; omitting this method preserves legacy behavior.
// Configure before serving requests.
func (h *CatalogHandler) WithEnabledPlanes(planes ...models.Plane) *CatalogHandler {
	h.enabled = make(map[models.Plane]bool, len(planes))
	for _, plane := range planes {
		h.enabled[plane] = true
	}
	return h
}

// List returns paginated catalog templates, optionally filtered by plane.
// GET /api/v1/catalog?plane=mcplex&page=0&page_size=20
func (h *CatalogHandler) List(w http.ResponseWriter, r *http.Request) {
	plane := models.Plane(r.URL.Query().Get("plane"))
	if h.enabled != nil && plane != "" && !h.enabled[plane] {
		Error(w, r, http.StatusNotFound, "MODULE_DISABLED", "catalog module is disabled")
		return
	}
	page := 0
	if raw := r.URL.Query().Get("page"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "page must be a non-negative integer")
			return
		}
		page = parsed
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	var result catalog.FetchResult
	if h.enabled == nil || plane != "" {
		result = h.aggregator.Fetch(r.Context(), plane)
	} else {
		for enabled := range h.enabled {
			fetched := h.aggregator.Fetch(r.Context(), enabled)
			result.Templates = append(result.Templates, fetched.Templates...)
			result.Errors = append(result.Errors, fetched.Errors...)
		}
	}
	// A source cannot make disabled entries visible by returning another plane.
	templates := make([]models.Template, 0, len(result.Templates))
	for _, template := range result.Templates {
		if (h.enabled == nil || h.enabled[template.Plane]) && (plane == "" || template.Plane == plane) {
			templates = append(templates, template)
		}
	}
	sort.SliceStable(templates, func(i, j int) bool {
		if templates[i].Plane != templates[j].Plane {
			return templates[i].Plane < templates[j].Plane
		}
		if templates[i].ID != templates[j].ID {
			return templates[i].ID < templates[j].ID
		}
		return templates[i].Source < templates[j].Source
	})
	total := len(templates)
	// Bound before multiplication to avoid overflowing int on hostile pages.
	start := total
	if page <= total/pageSize {
		start = page * pageSize
	}
	end := total
	if pageSize < total-start {
		end = start + pageSize
	}

	sort.SliceStable(result.Errors, func(i, j int) bool { return result.Errors[i].Source < result.Errors[j].Source })
	var sourceErrors []models.SourceError
	if len(result.Errors) > 0 {
		sourceErrors = result.Errors
	}

	JSON(w, http.StatusOK, models.CatalogPage{
		Templates:     templates[start:end],
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
	if h.enabled != nil && !h.enabled[t.Plane] {
		Error(w, r, http.StatusNotFound, "MODULE_DISABLED", "catalog module is disabled")
		return
	}
	JSON(w, http.StatusOK, t)
}
