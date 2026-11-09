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

func TestCLIAuthMethodDoesNotSendConflictingDefaultGrant(t *testing.T) {
	var grantCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid registration")
		}
		if payload["auth_method"] != "authorization_code" {
			t.Error("auth method not forwarded")
		}
		if grants, ok := payload["grant_types"].([]any); ok {
			grantCount = len(grants)
		}
		json.NewEncoder(w).Encode(map[string]any{"client_id": "agent", "auth_method": "authorization_code"})
	}))
	defer server.Close()
	oldURL, oldToken := apiURL, token
	apiURL, token = server.URL, "session"
	t.Cleanup(func() { apiURL, token = oldURL, oldToken })
	cmd := registerAgentCmd()
	cmd.SetArgs([]string{"agent", "--auth", "authorization_code"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if grantCount != 0 {
		t.Fatal("CLI overrode auth method with default grant")
	}
}

func TestCLIResolvedIdentityAndUsageFilters(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/api/v1/iam/whoami":
			json.NewEncoder(w).Encode(map[string]any{"identity": map[string]any{"principal_id": "principal-" + strings.Repeat("a", 64)}, "scopes": []string{}})
		case "/api/v1/llm/usage/summary":
			if r.URL.Query().Get("model_id") != "model" || r.URL.Query().Get("agent_id") != "agent" {
				t.Error("CLI filters lost")
			}
			json.NewEncoder(w).Encode(map[string]string{"period": "week"})
		default:
			t.Error("unexpected CLI request", r.URL.Path)
		}
	}))
	defer server.Close()
	oldURL, oldToken := apiURL, token
	apiURL, token = server.URL, "session"
	t.Cleanup(func() { apiURL, token = oldURL, oldToken })
	who := whoamiCmd()
	who.SetArgs([]string{"--server"})
	if err := who.Execute(); err != nil {
		t.Fatal(err)
	}
	costs := llmCostsCmd()
	costs.SetArgs([]string{"--model", "model", "--agent", "agent", "--period", "week"})
	if err := costs.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("CLI calls=%d", calls)
	}
}

func TestPlatformApplyRequiresDeployableIdentityConfiguration(t *testing.T) {
	for _, body := range []string{"api: {}", "api:\n  env:\n    JWT_ISSUER: https://issuer.example\n", "api:\n  env:\n    AIPLEX_DEV_MODE: true\n", "api:\n  env:\n    JWT_ISSUER: https://issuer.example\n    JWT_AUDIENCE: aiplex\n    JWT_JWKS_URL: http://remote.example/keys\n"} {
		path := filepath.Join(t.TempDir(), "values.yaml")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validatePlatformIdentityValues(path); err == nil {
			t.Fatal("invalid deployment identity accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, []byte("api:\n  env:\n    JWT_ISSUER: https://issuer.example\n    JWT_AUDIENCE: aiplex\n    JWT_JWKS_URL: https://issuer.example/keys\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validatePlatformIdentityValues(path); err != nil {
		t.Fatal(err)
	}
	if err := validatePlatformIdentityValues(""); err == nil {
		t.Fatal("implicit unauthenticated chart deployment accepted")
	}
}

func TestPlatformFileConfigurationRequiresReadOnlySourceMount(t *testing.T) {
	valid := `api:
  env:
    AIPLEX_IDENTITY_PROVIDERS_FILE: /etc/aiplex/identity/providers.json
    AIPLEX_CONNECTIONS_FILE: /etc/aiplex/connections/providers.json
  extraVolumes:
    - name: identity
      configMap:
        name: identity-config
    - name: connections
      secret:
        secretName: connection-config
  extraVolumeMounts:
    - name: identity
      mountPath: /etc/aiplex/identity
    - name: connections
      mountPath: /etc/aiplex/connections
      readOnly: true
`
	filename := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(filename, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validatePlatformIdentityValues(filename); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(valid, "readOnly: true", "readOnly: false", 1),
		strings.Replace(valid, "mountPath: /etc/aiplex/identity", "mountPath: /etc/aiplex/elsewhere", 1),
		strings.Replace(valid, "configMap:\n        name: identity-config", "hostPath:\n        path: /etc", 1),
		strings.Replace(valid, "mountPath: /etc/aiplex/identity", "mountPath: /etc/../identity", 1),
		strings.Replace(valid, "name: identity\n      mountPath:", "name: absent\n      mountPath:", 1),
	} {
		if err := os.WriteFile(filename, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validatePlatformIdentityValues(filename); err == nil {
			t.Fatal("invalid config source mount accepted")
		}
	}
}
