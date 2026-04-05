package cliconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	// Use a temp dir as home
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Load empty config
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Contexts) != 0 {
		t.Errorf("expected empty contexts, got %d", len(cfg.Contexts))
	}

	// Set context
	cfg.SetContext("dev", "http://localhost:8080", "my-project", "us-central1", "dev.example.com")
	cfg.SetContext("prod", "https://aiplex.example.com", "prod-project", "europe-west1", "aiplex.example.com")

	if cfg.CurrentContext != "prod" {
		t.Errorf("expected current=prod, got %s", cfg.CurrentContext)
	}

	// Save
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	// Verify file exists with correct permissions
	path := filepath.Join(tmp, ".aiplex", "config.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
	}

	// Reload
	cfg2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.CurrentContext != "prod" {
		t.Errorf("expected prod, got %s", cfg2.CurrentContext)
	}
	if len(cfg2.Contexts) != 2 {
		t.Errorf("expected 2 contexts, got %d", len(cfg2.Contexts))
	}
	if cfg2.Contexts["dev"].URL != "http://localhost:8080" {
		t.Errorf("dev URL mismatch: %s", cfg2.Contexts["dev"].URL)
	}
}

func TestConfigCurrent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, _ := Load()

	// No context set
	_, err := cfg.Current()
	if err != ErrNoContext {
		t.Errorf("expected ErrNoContext, got %v", err)
	}

	// Set and get
	cfg.SetContext("test", "http://localhost", "", "", "")
	ctx, err := cfg.Current()
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Name != "test" {
		t.Errorf("expected test, got %s", ctx.Name)
	}
}

func TestCredentialsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Ensure dir exists
	Dir()

	creds, err := LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}

	creds.SetToken("dev", &TokenEntry{
		AccessToken: "test-token-123",
		TokenType:   "bearer",
	})

	if err := creds.Save(); err != nil {
		t.Fatal(err)
	}

	// Verify file permissions
	path := filepath.Join(tmp, ".aiplex", "credentials.json")
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600, got %o", info.Mode().Perm())
	}

	// Reload
	creds2, err := LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	entry := creds2.GetToken("dev")
	if entry == nil {
		t.Fatal("expected token for dev")
	}
	if entry.AccessToken != "test-token-123" {
		t.Errorf("token mismatch: %s", entry.AccessToken)
	}

	// Missing context returns nil
	if creds2.GetToken("nonexistent") != nil {
		t.Error("expected nil for nonexistent context")
	}
}
