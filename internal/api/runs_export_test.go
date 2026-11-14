package api

import (
	"context"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// RunsTestCaller is test-only middleware that stands in for WIFAuthMode: it
// attaches a verified access (nil for none) and the development flag, so
// handler tests in package api_test run as a defined caller.
func RunsTestCaller(access *models.ResolvedAccess, development bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), keyDevelopment, development)
			if access != nil {
				ctx = context.WithValue(ctx, keyWIFAccess, access)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
