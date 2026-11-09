package api

import (
	"context"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagedModelRequiresAllDispatchGrantsInOneAuthoritySnapshot(t *testing.T) {
	f := quotaHostModelNew(t, false, func(http.ResponseWriter, *http.Request) { t.Error("mixed grant snapshots reached provider") }, nil)
	calls := 0
	f.handler.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) {
		calls++
		scopes := []string{"llm:surface:surface:models.generateContent"}
		if calls%2 == 0 {
			scopes = []string{"llm:model:model-v1"}
		}
		return &models.ResolvedAccess{Identity: *f.identity, Scopes: scopes}, nil
	}
	w := httptest.NewRecorder()
	f.handler.NativeModel(w, f.request("generateContent", "mixed-grants", quotaHostModelBody, false))
	if w.Code < 400 || f.calls.Load() != 0 || calls != 1 {
		t.Fatal("grant intersection was not resolved once", w.Code, calls, f.calls.Load(), w.Body.String())
	}
}
