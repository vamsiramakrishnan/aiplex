package errorcode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	for code, want := range map[string]bool{
		"UNAUTHENTICATED": true, "QUOTA_BUDGET_EXHAUSTED": true, "A": true, "X9_Z": true, strings.Repeat("A", 64): true,
		"": false, "lower": false, "Mixed": false, "HAS-DASH": false, "HAS SPACE": false, "NEW\nLINE": false, "É": false, strings.Repeat("A", 65): false,
	} {
		if got := Valid(code); got != want {
			t.Errorf("Valid(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestSetNeverWritesAMalformedValue(t *testing.T) {
	h := http.Header{}
	Set(h, "NOT_ALLOWED")
	if h.Get(Header) != "NOT_ALLOWED" {
		t.Fatalf("valid code changed: %q", h.Get(Header))
	}
	Set(h, "hook_denied")
	if h.Get(Header) != "HOOK_DENIED" {
		t.Fatalf("upper-casing: %q", h.Get(Header))
	}
	for _, bad := range []string{"", strings.Repeat("a", 65), "has-dash", "é"} {
		h2 := http.Header{}
		Set(h2, bad)
		if h2.Get(Header) != Unspecified {
			t.Fatalf("%q gave %q", bad, h2.Get(Header))
		}
	}
	Set(h, "bad code\r\nX-Injected: 1")
	if h.Get(Header) != Unspecified || h.Get("X-Injected") != "" {
		t.Fatalf("malformed code was written: %q", h.Get(Header))
	}
}

func TestRefuseKeepsHTTPErrorBody(t *testing.T) {
	w := httptest.NewRecorder()
	Refuse(w, 403, "FORBIDDEN", "operation denied")
	want := httptest.NewRecorder()
	http.Error(want, "operation denied", 403)
	if w.Code != 403 || w.Body.String() != want.Body.String() || w.Header().Get("Content-Type") != want.Header().Get("Content-Type") || w.Header().Get(Header) != "FORBIDDEN" {
		t.Fatalf("got %d %q %v", w.Code, w.Body.String(), w.Header())
	}
}
