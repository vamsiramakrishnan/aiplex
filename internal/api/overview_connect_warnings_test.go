package api

import (
	"net/http"
	"strings"
	"testing"
)

// The Overview connect page renders ClientPlex plans for a synthetic
// registration. Its notes must not tell a developer that a field the
// registration needs does nothing, nor that the access point does not restrict
// models (it does: an unknown model is answered 404 MODEL_NOT_FOUND).
func TestOverviewConnectNotesCarryNoInertFieldOrAllowedModelsWarning(t *testing.T) {
	f := newOverviewFixture(t)
	rendered := 0
	for _, ap := range []string{"ide", "chat-only"} {
		for _, client := range []string{"claude-code", "codex", "gemini-cli", "antigravity", "antigravity-acp"} {
			w := f.get("/api/v1/overview/access-points/" + ap + "/connect?client=" + client)
			if w.Code == http.StatusUnprocessableEntity {
				continue // the access point offers no protocol this client speaks
			}
			if w.Code != http.StatusOK {
				t.Fatalf("%s/%s = %d %s", ap, client, w.Code, w.Body.String())
			}
			rendered++
			body := w.Body.String()
			if strings.Contains(body, "has no effect") {
				t.Errorf("%s/%s: a field is reported as inert: %s", ap, client, body)
			}
			if !strings.HasPrefix(client, "antigravity") && strings.Contains(body, "allowed_models") {
				t.Errorf("%s/%s: allowed_models warning on the Overview: %s", ap, client, body)
			}
		}
	}
	if rendered < 3 {
		t.Fatalf("only %d profile renderings were exercised", rendered)
	}
}
