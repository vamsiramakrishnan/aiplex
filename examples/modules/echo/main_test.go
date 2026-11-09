package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
)

func TestStandaloneModuleHTTPContract(t *testing.T) {
	server := httptest.NewServer(handler())
	defer server.Close()
	remote, err := modules.NewRemote(modules.RemoteConfig{Descriptor: echoDescriptor(), Endpoint: server.URL, AllowInsecureLocalhost: true})
	if err != nil {
		t.Fatal(err)
	}
	registry := modules.NewRegistry()
	if err := registry.Register(remote); err != nil {
		t.Fatal(err)
	}
	runtime := modules.NewRuntime(registry)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer runtime.Stop(context.Background())
	if err := runtime.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(server.URL+"/echo", "text/plain", strings.NewReader("standalone module"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "standalone module" {
		t.Fatalf("echo status=%d body=%s err=%v", response.StatusCode, body, err)
	}
}

func TestEchoRejectsOversizeAndWrongMethods(t *testing.T) {
	h := handler()
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, "/echo", strings.Repeat("x", (64<<10)+1), http.StatusRequestEntityTooLarge},
		{http.MethodGet, "/echo", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/readyz", "", http.StatusMethodNotAllowed},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
		if w.Code != test.status {
			t.Fatalf("%s %s: got %d, want %d", test.method, test.path, w.Code, test.status)
		}
	}
}
