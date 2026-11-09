package api

import (
	"encoding/json"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuotaAdminJSONRejectsNestedStructAliasesWithoutFoldingMeterIDs(t *testing.T) {
	for _, raw := range []string{`{"policy":{"revision":1,"Revision":2}}`, `{"policy":{"budgets":[{"limit":1,"LIMIT":2}]}}`, `{"policy":{"price_books":[{"prices":[{"meter_id":"a","Meter_Id":"b"}]}]}}`} {
		var out struct {
			Policy quotaplex.Policy `json:"policy"`
		}
		r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
		if quotaAdminDecode(r, &out) == nil {
			t.Fatal("ambiguous nested fields accepted", raw)
		}
	}
	var out struct {
		Usage map[string]int64 `json:"usage"`
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"usage":{"TOKENS":1,"tokens":2}}`))
	if err := quotaAdminDecode(r, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Usage) != 2 {
		t.Fatal(out)
	}
	var typed map[string]json.RawMessage
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{"meter":{"id":"x"}}`))
	if err := quotaAdminDecode(r, &typed); err != nil {
		t.Fatal(err)
	}
}
