package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeGovernanceCLIRequiresReviewedApplyInputs(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]string{"state": "succeeded"})
	}))
	defer server.Close()
	oldURL, oldToken := apiURL, token
	apiURL, token = server.URL, "test-token"
	t.Cleanup(func() { apiURL, token = oldURL, oldToken })
	for _, args := range [][]string{{"governance", "apply", "plan-1"}, {"governance", "apply", "plan-1", "--plan-revision", "2"}, {"governance", "plan", "binding-1", "--binding-revision", "1"}} {
		cmd := nativeCmd()
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatal("incomplete review accepted", args)
		}
	}
	if calls != 0 {
		t.Fatalf("incomplete review made %d HTTP calls", calls)
	}
	cmd := nativeCmd()
	cmd.SetArgs([]string{"governance", "apply", "plan-1", "--plan-revision", "2", "--approval-digest", "sha256:reviewed"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("apply calls=%d", calls)
	}
}

func TestNativeOperationFileRejectsCredentialMaterialAndAmbiguousJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.json")
	for _, body := range []string{`{"name":"read","name":"other","resource":"r"}`, `{"name":"read","resource":"r","payload":{"clientSecret":"PRIVATE"}}`, `{"name":"read","resource":"r","unexpected":true}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readNativeOperation(path); err == nil || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal("unsafe file accepted or leaked", err)
		}
	}
	if err := os.WriteFile(path, []byte(`{"name":"services.get","resource":"projects/p/locations/l/services/s"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if operation, err := readNativeOperation(path); err != nil || operation.Name != "services.get" {
		t.Fatal(operation, err)
	}
}
