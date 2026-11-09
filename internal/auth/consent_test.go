package auth

import (
	"context"
	"encoding/json"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestConsentHonorsSelectionAndCeilings(t *testing.T) {
	store := registry.NewMemoryStore()
	ctx := context.Background()
	store.PutAgent(ctx, &models.Agent{ClientID: "agent", Status: "active", AllowedScopes: []string{"llm:model:*"}})
	store.SetUserScopes(ctx, "user", []string{"llm:model:a", "llm:model:b"})
	var granted []string
	accepts := 0
	challengeSubject := "user"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"subject": challengeSubject, "client": map[string]string{"client_id": "agent"}, "requested_scope": []string{"llm:model:a", "llm:model:b"}})
			return
		}
		accepts++
		var body struct {
			Scopes []string `json:"grant_scope"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		granted = body.Scopes
		w.Write([]byte(`{"redirect_to":"https://callback"}`))
	}))
	defer server.Close()
	ch := NewConsentHandler(NewHydraClient(server.URL), store)
	if _, err := ch.HandleConsentScopes(ctx, "challenge", []string{"llm:model:a"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(granted, []string{"llm:model:a"}) {
		t.Fatalf("unchecked scopes granted: %v", granted)
	}
	if _, err := ch.HandleConsentScopes(ctx, "challenge", []string{"llm:model:secret"}); err == nil {
		t.Fatal("unrequested scope granted")
	}
	if accepts != 1 {
		t.Fatal("invalid selection accepted by Hydra")
	}
	access := &models.ResolvedAccess{Identity: models.WIFIdentity{Subject: "different", Provider: "issuer"}, Scopes: []string{"llm:model:a"}}
	if _, err := ch.HandleConsentForAccess(ctx, "challenge", []string{"llm:model:a"}, access); err == nil {
		t.Fatal("another user's challenge accepted")
	}
	access.Identity.Subject = "external-user"
	access.Identity.PrincipalID = CanonicalPrincipalID("issuer", "", "external-user")
	challengeSubject = access.Identity.Subject
	if _, err := ch.HandleConsentForAccess(ctx, "challenge", []string{"llm:model:a"}, access); err == nil {
		t.Fatal("issuer-local subject accepted as global principal")
	}
	challengeSubject = access.Identity.PrincipalID
	store.SetUserScopes(ctx, "user", nil)
	if _, err := ch.HandleConsentForAccess(ctx, "challenge", []string{"llm:model:a"}, access); err != nil {
		t.Fatalf("fresh derived grants not usable: %v", err)
	}
	challengeSubject = "user"
	if _, err := ch.HandleConsentScopes(ctx, "challenge", nil); err != nil {
		t.Fatalf("empty selection: %v", err)
	}
	if len(granted) != 0 {
		t.Fatalf("empty selection granted: %v", granted)
	}
}
func TestConsentRejectsMissingSubject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"subject":"","client":{"client_id":"agent"}}`))
	}))
	defer server.Close()
	_, err := NewConsentHandler(NewHydraClient(server.URL), registry.NewMemoryStore()).HandleConsentScopes(context.Background(), "challenge", nil)
	if err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatalf("missing subject accepted: %v", err)
	}
}

func TestHydraConsentErrorsAndChallengeEncoding(t *testing.T) {
	challenge := "challenge&other=injected"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("consent_challenge") != challenge || r.URL.Query().Get("other") != "" {
			t.Errorf("challenge query injection: %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"redirect_to":"https://must-not-accept"}`))
	}))
	defer server.Close()
	h := NewHydraClient(server.URL)
	ctx := context.Background()
	if _, err := h.GetConsentRequest(ctx, challenge); err == nil {
		t.Fatal("failed get reported success")
	}
	if _, err := h.AcceptConsent(ctx, challenge, nil, nil); err == nil {
		t.Fatal("failed acceptance reported success")
	}
	if _, err := h.RejectConsent(ctx, challenge, "denied", "denied"); err == nil {
		t.Fatal("failed rejection reported success")
	}
	if h.httpClient.Timeout == 0 {
		t.Fatal("Hydra HTTP timeout unbounded")
	}
}
