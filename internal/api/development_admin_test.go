package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// Administrative reads follow the same development rule as RequireRole: an
// unverified request in explicit development mode is treated as an
// administrator, and a verified non-admin is refused in every mode.
func TestAdministrativeReadsHonourDevelopmentMode(t *testing.T) {
	for _, development := range []bool{true, false} {
		h := NewConnectedHandler(resources.NewMemoryStore(), nil, func(*http.Request) (string, error) { return "tenant", nil }, nil, development)
		router := chi.NewRouter()
		router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Test-Role") != "" {
					identity := &models.WIFIdentity{PrincipalID: "p", Subject: "p", Provider: "i"}
					ctx := context.WithValue(r.Context(), keyWIFIdentity, identity)
					ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: *identity, Roles: []models.IAMRole{models.IAMRole(r.Header.Get("X-Test-Role"))}})
					r = r.WithContext(ctx)
				}
				next.ServeHTTP(w, r)
			})
		})
		router.Get("/llm/batches", h.ListModelBatches)
		router.Get("/llm/surfaces/{id}/capacity", h.SurfaceCapacity)
		for _, test := range []struct {
			path, role string
			want       int
		}{
			{"/llm/batches", "", map[bool]int{true: 200, false: 403}[development]},
			{"/llm/surfaces/missing/capacity", "", map[bool]int{true: 404, false: 403}[development]},
			{"/llm/batches", "deployer", 403},
			{"/llm/surfaces/missing/capacity", "deployer", 403},
			{"/llm/batches", "admin", 200},
			{"/llm/surfaces/missing/capacity", "admin", 404},
		} {
			r := httptest.NewRequest("GET", test.path, nil)
			if test.role != "" {
				r.Header.Set("X-Test-Role", test.role)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Errorf("development=%v %s role=%q: got %d want %d %s", development, test.path, test.role, w.Code, test.want, w.Body)
			}
		}
	}
}
