package main

import (
	"os"
	"path/filepath"
	"testing"
)

// allow_loopback, which is what lets an agent connection name an http loopback
// origin, is accepted in development mode only.
func TestAllowLoopbackNeedsDevelopmentMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections.json")
	if err := os.WriteFile(path, []byte(`{"network":{"allowed_origins":["http://127.0.0.1:9000"],"allow_loopback":true},"identities":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConnectedConfig(path, false); err == nil {
		t.Fatal("allow_loopback accepted outside development mode")
	}
	cfg, err := loadConnectedConfig(path, true)
	if err != nil || !cfg.Network.AllowLoopback {
		t.Fatalf("development mode: %+v %v", cfg, err)
	}
}
