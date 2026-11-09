package aiplex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModuleDiscoveryAndDisabledLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/modules" || r.Header.Get("Authorization") != "Bearer session" {
			t.Errorf("unexpected request %s", r.URL)
		}
		json.NewEncoder(w).Encode([]ModuleDescriptor{{Name: "llmplex", Version: "1.0.0", ContractVersion: "v1"}, {Name: "future-extension", Version: "2.0.0", ContractVersion: "v2"}})
	}))
	defer server.Close()
	c := NewClient(server.URL)
	c.SetToken("session")
	all, err := c.ListModules(context.Background())
	if err != nil || len(all) != 2 {
		t.Fatalf("discovery: %v %#v", err, all)
	}
	if _, err := c.GetModule(context.Background(), "llmplex"); err != nil {
		t.Fatal(err)
	}
	_, err = c.GetModule(context.Background(), "quotaplex")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "MODULE_NOT_ENABLED" {
		t.Fatalf("missing module must fail: %v", err)
	}
}

func TestModuleDiscoveryFailureIsNotEmptySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if _, err := NewClient(server.URL).ListModules(context.Background()); err == nil {
		t.Fatal("expected discovery error")
	}
}
