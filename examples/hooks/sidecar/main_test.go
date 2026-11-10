package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	out := Decide(request{Parts: []part{{Index: 0, Text: "ask EMP-123456 and EMP-654321"}, {Index: 1, Text: "fine"}}})
	if out.Decision != "transform" || out.Parts[0].Text != "ask [EMPLOYEE_ID] and [EMPLOYEE_ID]" || out.Rules[0].Count != 2 {
		t.Fatalf("%+v", out)
	}
	if out = Decide(request{Parts: []part{{Text: "CONFIDENTIAL plan"}}}); out.Decision != "deny" {
		t.Fatalf("%+v", out)
	}
	if out = Decide(request{Parts: []part{{Text: "hello"}}}); out.Decision != "allow" || out.Parts != nil {
		t.Fatalf("%+v", out)
	}
}

func TestHandlerChecksTokenAndVersion(t *testing.T) {
	h := handler("s3cret")
	for _, c := range []struct {
		auth, body string
		want       int
	}{
		{"", `{"version":"aiplex.hooks.v1","parts":[]}`, 401},
		{"Bearer s3cret", `{"version":"other"}`, 400},
		{"Bearer s3cret", `{"version":"aiplex.hooks.v1","phase":"request","parts":[{"index":0,"text":"EMP-000001"}]}`, 200},
	} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.body))
		r.Header.Set("Authorization", c.auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Fatalf("%q: %d %s", c.body, w.Code, w.Body.String())
		}
	}
}
