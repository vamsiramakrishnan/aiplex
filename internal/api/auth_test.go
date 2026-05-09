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
	hydraClient := auth.NewHydraClient("http://localhost:0")
	authH := api.NewAuthHandler(hydraClient, store)

	r := chi.NewRouter()
	r.Post("/auth/token-hook", authH.TokenHook)
	r.Get("/auth/users/{userId}/caps", authH.GetUserCaps)
	r.Put("/auth/users/{userId}/caps", authH.SetUserCaps)

	return r, store
}

func TestTokenHook_InjectsActClaim(t *testing.T) {
	r, store := setupAuthRouter()

	store.PutAgent(context.Background(), &models.Agent{
		ClientID:    "tutor-agent",
		DisplayName: "Tutor",
		SpiffeID:    "spiffe://test.local/ns/a2aplex/sa/tutor-agent",
		Status:      "active",
	})

	body := `{
		"subject": "student@school.edu",
		"client": {"client_id": "tutor-agent"},
		"granted_scopes": ["cap://tool/search@v1"],
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

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestUserCaps_SetAndGet(t *testing.T) {
	r, _ := setupAuthRouter()

	body := `{"caps": [
		{"uri": "cap://tool/search@v1", "actions": ["call"]},
		{"uri": "cap://model/gemini-2.5-flash@v1", "actions": ["complete"]},
		{"uri": "cap://task/research@v1", "actions": ["invoke"]}
	]}`
	req := httptest.NewRequest("PUT", "/auth/users/admin@test.com/caps", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 204 {
		t.Fatalf("set caps: expected 204, got %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/auth/users/admin@test.com/caps", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("get caps: expected 200, got %d", w.Code)
	}

	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)

	caps := result["caps"].([]any)
	if len(caps) != 3 {
		t.Errorf("expected 3 caps, got %d", len(caps))
	}

	byKind := result["by_kind"].(map[string]any)
	if _, ok := byKind["tool"]; !ok {
		t.Error("expected tool in by_kind grouping")
	}
	if _, ok := byKind["model"]; !ok {
		t.Error("expected model in by_kind grouping")
	}
	if _, ok := byKind["task"]; !ok {
		t.Error("expected task in by_kind grouping")
	}
}
