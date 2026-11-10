package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// A caller with no role, scope or group gets empty arrays, not null.
func TestWhoamiListsAreEmptyArraysNotNull(t *testing.T) {
	raw, err := json.Marshal(nonNullAccess(&models.ResolvedAccess{Identity: models.WIFIdentity{PrincipalID: "p"}}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{`"roles":[]`, `"scopes":[]`, `"groups":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("want %s in %s", want, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("null in %s", body)
	}
	if nonNullAccess(nil) != nil {
		t.Error("nil access must stay nil")
	}
}
