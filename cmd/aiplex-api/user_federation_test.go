package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestUserFederationWiring(t *testing.T) {
	tenant := func(*http.Request) (string, error) { return "development", nil }
	wif := auth.NewWIFValidator(registry.NewMemoryStore(), auth.WIFConfig{})

	t.Setenv("AIPLEX_USER_FEDERATION", "")
	h, err := userFederation(&config.Config{PublicURL: "https://aiplex.example.com/"}, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif)
	if err != nil || h.LiveEnabled {
		t.Fatalf("default wiring must be off: %+v %v", h, err)
	}

	t.Setenv("AIPLEX_USER_FEDERATION", "enabled")
	if _, err = userFederation(&config.Config{}, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif); err == nil || !strings.Contains(err.Error(), "AIPLEX_TENANT_ID") {
		t.Fatalf("enabled without tenant must fail: %v", err)
	}
	h, err = userFederation(&config.Config{TenantID: "acme"}, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif)
	if err != nil || !h.LiveEnabled {
		t.Fatalf("enabled wiring: %v", err)
	}
	h, err = userFederation(&config.Config{DevelopmentMode: true}, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif)
	if err != nil || !h.LiveEnabled {
		t.Fatalf("development wiring: %v", err)
	}
	t.Setenv("AIPLEX_USER_FEDERATION", "yes")
	if _, err = userFederation(&config.Config{TenantID: "acme"}, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if _, err = userFederation(&config.Config{}, resources.NewMemoryStore(), endpointpolicy.Config{AllowLoopback: true}, tenant, wif); err == nil {
		t.Fatal("loopback JWKS outside development accepted")
	}

	// Routes coexist with the agent identity routes, as mounted in main.
	agents := registry.NewMemoryStore()
	agentH, err := agentFederation(&config.Config{}, agents, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIPLEX_USER_FEDERATION", "")
	userH, err := userFederation(&config.Config{}, resources.NewMemoryStore(), endpointpolicy.Config{}, tenant, wif)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		agentH.Mount(r)
		userH.Mount(r)
	})
	for path, want := range map[string]int{"/api/v1/agents/identity-presets": 200, "/api/v1/identity/user-provider-presets": 200} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("GET %s = %d %s", path, w.Code, w.Body)
		}
	}
}
