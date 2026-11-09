package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestAgentFederationWiring(t *testing.T) {
	tenant := func(*http.Request) (string, error) { return "development", nil }
	store := registry.NewMemoryStore()
	wif := auth.NewWIFValidator(store, auth.WIFConfig{})

	t.Setenv("AIPLEX_AGENT_FEDERATION", "")
	h, err := agentFederation(&config.Config{PublicURL: "https://aiplex.example.com/"}, store, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif)
	if err != nil || h.LiveEnabled || h.DefaultAudience != "https://aiplex.example.com" {
		t.Fatalf("default wiring: %+v %v", h, err)
	}

	t.Setenv("AIPLEX_AGENT_FEDERATION", "enabled")
	if _, err = agentFederation(&config.Config{}, store, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif); err == nil || !strings.Contains(err.Error(), "AIPLEX_TENANT_ID") {
		t.Fatalf("enabled without tenant must fail: %v", err)
	}
	h, err = agentFederation(&config.Config{TenantID: "acme"}, store, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif)
	if err != nil || !h.LiveEnabled {
		t.Fatalf("enabled wiring: %v", err)
	}
	t.Setenv("AIPLEX_AGENT_FEDERATION", "yes")
	if _, err = agentFederation(&config.Config{TenantID: "acme"}, store, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if _, err = agentFederation(&config.Config{}, store, resources.NewMemoryStore(), endpointpolicy.Config{AllowLoopback: true}, tenant, wif); err == nil {
		t.Fatal("loopback JWKS outside development accepted")
	}

	// Routes coexist with the existing /agents/{clientId} routes, as mounted in main.
	agentH := api.NewAgentHandler(store)
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/agents", agentH.List)
		r.Post("/agents", agentH.Register)
		r.Get("/agents/{clientId}", agentH.Get)
		r.Delete("/agents/{clientId}", agentH.Delete)
		r.Get("/agents/{clientId}/permissions", agentH.GetPermissions)
		h.Mount(r)
	})
	for path, want := range map[string]int{"/api/v1/agents/identity-presets": 200, "/api/v1/agents/missing": 404} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("GET %s = %d %s", path, w.Code, w.Body)
		}
	}
}
