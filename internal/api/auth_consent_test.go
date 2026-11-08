package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func TestConsentAcceptUsesSelectedScopes(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutAgent(ctx, &models.Agent{ClientID: "agent", AllowedScopes: []string{"llm:model:*"}})
	store.SetUserScopes(ctx, "user", []string{"llm:model:a", "llm:model:b"})
	var scopes []string
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Write([]byte(`{"subject":"user","client":{"client_id":"agent"},"requested_scope":["llm:model:a","llm:model:b"]}`))
			return
		}
		var body struct {
			Scopes []string `json:"grant_scope"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		scopes = body.Scopes
		w.Write([]byte(`{"redirect_to":"https://callback"}`))
	}))
	defer hydra.Close()
	h := api.NewAuthHandler(auth.NewHydraClient(hydra.URL), store)
	rec := httptest.NewRecorder()
	h.ConsentAccept(rec, httptest.NewRequest("POST", "/auth/consent", strings.NewReader(`{"challenge":"c","granted_scopes":["llm:model:a"]}`)))
	if rec.Code != 200 {
		t.Fatalf("response %d: %s", rec.Code, rec.Body.String())
	}
	if !reflect.DeepEqual(scopes, []string{"llm:model:a"}) {
		t.Fatalf("user selection ignored: %v", scopes)
	}
	rec = httptest.NewRecorder()
	h.ConsentAccept(rec, httptest.NewRequest("POST", "/auth/consent", strings.NewReader(`{"challenge":"c"}`)))
	if rec.Code != 400 {
		t.Fatalf("missing selection accepted: %d", rec.Code)
	}
}
