package aiplex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientRegistrationSDKRevisionAndEscaping(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("If-Match")
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dev","revision":3,"spec":{}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL)
	client.SetToken("test-token")
	_, e := client.PutClientRegistration(context.Background(), ClientRegistration{ID: "dev"}, 2)
	if e != nil || seen != `"2"` {
		t.Fatal(e, seen)
	}
	if _, e = client.GetClientRegistration(context.Background(), "../other"); e == nil {
		t.Fatal("path traversal accepted")
	}
	if e = client.DeleteClientRegistration(context.Background(), "dev", 0); e == nil || !strings.Contains(e.Error(), "revision") {
		t.Fatal(e)
	}
}
