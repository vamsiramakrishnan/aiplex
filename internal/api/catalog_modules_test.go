package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

type catalogModuleSource struct {
	plane   models.Plane
	calls   *int
	entries []models.Template
}

func (s catalogModuleSource) Name() string        { return string(s.plane) }
func (s catalogModuleSource) Plane() models.Plane { return s.plane }
func (s catalogModuleSource) Fetch(context.Context) ([]models.Template, error) {
	*s.calls++
	return s.entries, nil
}

func TestCatalogModuleIsolationAndPagination(t *testing.T) {
	store := registry.NewMemoryStore()
	llmCalls, mcpCalls := 0, 0
	llm := models.Template{ID: "model", Plane: models.PlaneLLMPlex}
	mcp := models.Template{ID: "tool", Plane: models.PlaneMCPlex}
	if err := store.PutTemplate(context.Background(), &llm); err != nil {
		t.Fatal(err)
	}
	if err := store.PutTemplate(context.Background(), &mcp); err != nil {
		t.Fatal(err)
	}
	agg := catalog.NewAggregator([]catalog.Source{
		catalogModuleSource{models.PlaneLLMPlex, &llmCalls, []models.Template{llm}},
		// Source accidentally returns a template owned by a disabled module.
		catalogModuleSource{models.PlaneMCPlex, &mcpCalls, []models.Template{mcp, llm}},
	})
	h := api.NewCatalogHandler(agg, store).WithEnabledPlanes(models.PlaneMCPlex)
	r := chi.NewRouter()
	r.Get("/catalog", h.List)
	r.Get("/catalog/{id}", h.Get)
	for _, path := range []string{"/catalog", "/catalog?page=9223372036854775807", "/catalog?page=" + strconv.Itoa(int(^uint(0)>>1))} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var page models.CatalogPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 {
			t.Fatalf("disabled templates counted: %+v", page)
		}
		for _, template := range page.Templates {
			if template.Plane != models.PlaneMCPlex {
				t.Fatal("disabled template leaked")
			}
		}
	}
	if llmCalls != 0 || mcpCalls != 3 {
		t.Fatalf("disabled sources fetched: llm=%d mcp=%d", llmCalls, mcpCalls)
	}
	for _, path := range []string{"/catalog?plane=llmplex", "/catalog/model"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	for _, page := range []string{"-1", "18446744073709551616", "not-a-number"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/catalog?page="+page, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("page %q: %d", page, w.Code)
		}
	}
}

func TestCatalogExplicitEmptyModuleSelection(t *testing.T) {
	calls := 0
	agg := catalog.NewAggregator([]catalog.Source{catalogModuleSource{models.PlaneLLMPlex, &calls, []models.Template{{ID: "model", Plane: models.PlaneLLMPlex}}}})
	h := api.NewCatalogHandler(agg, registry.NewMemoryStore()).WithEnabledPlanes()
	w := httptest.NewRecorder()
	h.List(w, httptest.NewRequest(http.MethodGet, "/catalog", nil))
	var page models.CatalogPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || page.Total != 0 || calls != 0 {
		t.Fatalf("empty module selection not honored: %d %+v calls=%d", w.Code, page, calls)
	}
}

func TestUnsafeRouteConfigIsRejectedBeforePersistence(t *testing.T) {
	r, store := setupLLMRouter()
	body := `{"backends":[{"provider":"google","model_id":"model\n---\nkind: Secret","weight":100,"enabled":true}]}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/llm/routes/model", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unsafe route returned %d: %s", w.Code, w.Body.String())
	}
	if _, err := store.GetRouteConfig(context.Background(), "model"); err == nil {
		t.Fatal("unsafe route persisted")
	}
}
