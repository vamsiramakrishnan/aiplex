package main

import (
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestModuleSelectionAndMount(t *testing.T) {
	available := builtinModules(nil, nil, nil)
	selected, e := selectModules([]string{"mcplex"}, available)
	if e != nil {
		t.Fatal(e)
	}
	r := chi.NewRouter()
	for _, m := range selected {
		m.mount(r)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/llm/providers", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	if _, e = selectModules([]string{"missing"}, available); e == nil {
		t.Fatal("unknown accepted")
	}
	if _, e = selectModules([]string{"mcplex", "mcplex"}, available); e == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestExternalModuleStartupAndReadiness(t *testing.T) {
	descriptor := modules.Descriptor{Name: "test-extension", Version: "1.0.0", ContractVersion: "v1", Capabilities: []string{"test.extension"}}
	var ready atomic.Bool
	ready.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/module/v1/descriptor":
			_ = json.NewEncoder(w).Encode(descriptor)
		case "/readyz":
			if !ready.Load() {
				w.WriteHeader(503)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	data, _ := json.Marshal(remoteModuleFile{Modules: []remoteModuleSpec{{Descriptor: descriptor, Endpoint: server.URL, AllowInsecureLocalhost: true}}})
	path := filepath.Join(t.TempDir(), "modules.json")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	registry := modules.NewRegistry()
	if e := registerExternalModules(registry, path, false); e == nil {
		t.Fatal("production accepted insecure endpoint")
	}
	if e := registerExternalModules(registry, path, true); e != nil {
		t.Fatal(e)
	}
	runtime := modules.NewRuntime(registry)
	if e := runtime.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	defer runtime.Stop(context.Background())
	ready.Store(false)
	if e := runtime.Ready(context.Background()); e == nil {
		t.Fatal("unhealthy external module considered ready")
	}
}
