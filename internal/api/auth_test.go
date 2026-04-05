package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func setupAuthRouter() (chi.Router, *registry.MemoryStore) {
	store := registry.NewMemoryStore()
	// Hydra client pointing to a non-existent server (tests won't call Hydra directly)
	hydraClient := auth.NewHydraClient("http://localhost:0")
	authH := api.NewAuthHandler(hydraClient, store)

	r := chi.NewRouter()
	r.Post("/auth/token-hook", authH.TokenHook)
	r.Get("/auth/users/{userId}/scopes", authH.GetUserScopes)
	r.Put("/auth/users/{userId}/scopes", authH.SetUserScopes)

	return r, store
}

func TestTokenHook_InjectsActClaim(t *testing.T) {
	r, store := setupAuthRouter()

	// Register an agent with SPIFFE ID
	store.PutAgent(context.Background(), &models.Agent{
		ClientID:    "tutor-agent",
		DisplayName: "Tutor",
		SpiffeID:    "spiffe://test.local/ns/a2aplex/sa/tutor-agent",
		Status:      "active",
	})

	body := `{
		"subject": "student@school.edu",
		"client": {"client_id": "tutor-agent"},
		"granted_scopes": ["mcp:tools:search"],
		"session": {"access_token": {}}
	}`
	req := httptest.NewRequest("POST", "/auth/token-hook", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)

	session := result["session"].(map[string]any)
	accessToken := session["access_token"].(map[string]any)
	act := accessToken["act"].(map[string]any)

	if act["sub"] != "spiffe://test.local/ns/a2aplex/sa/tutor-agent" {
		t.Errorf("expected SPIFFE ID in act claim, got %v", act["sub"])
	}
}

func TestTokenHook_UnknownAgent(t *testing.T) {
	r, _ := setupAuthRouter()

	body := `{
		"subject": "user@test.com",
		"client": {"client_id": "unknown-agent"},
		"granted_scopes": [],
		"session": {"access_token": {"existing": "claim"}}
	}`
	req := httptest.NewRequest("POST", "/auth/token-hook", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should still succeed — just without act claim
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestUserScopes_SetAndGet(t *testing.T) {
	r, _ := setupAuthRouter()

	// Set scopes
	body := `{"scopes": ["mcp:tools:search", "llm:model:gemini-2.5-flash", "a2a:task:research"]}`
	req := httptest.NewRequest("PUT", "/auth/users/admin@test.com/scopes", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 204 {
		t.Fatalf("set scopes: expected 204, got %d", w.Code)
	}

	// Get scopes
	req = httptest.NewRequest("GET", "/auth/users/admin@test.com/scopes", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("get scopes: expected 200, got %d", w.Code)
	}

	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)

	scopes := result["scopes"].([]any)
	if len(scopes) != 3 {
		t.Errorf("expected 3 scopes, got %d", len(scopes))
	}

	byPlane := result["by_plane"].(map[string]any)
	if _, ok := byPlane["mcplex"]; !ok {
		t.Error("expected mcplex in by_plane grouping")
	}
	if _, ok := byPlane["llmplex"]; !ok {
		t.Error("expected llmplex in by_plane grouping")
	}
	if _, ok := byPlane["a2aplex"]; !ok {
		t.Error("expected a2aplex in by_plane grouping")
	}
}
