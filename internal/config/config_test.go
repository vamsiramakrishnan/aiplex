package config

import (
	"context"
	"os"
	"testing"
)

func TestDevelopmentRequiresLoopbackBind(t *testing.T) {
	t.Setenv("AIPLEX_DEV_MODE", "true")
	for _, host := range []string{"0.0.0.0", "example.com", "::", ""} {
		t.Setenv("HOST", host)
		if _, e := Load(context.Background()); e == nil {
			t.Fatalf("accepted insecure bind %q", host)
		}
	}
	for _, host := range []string{"127.0.0.1", "::1", "localhost"} {
		t.Setenv("HOST", host)
		if _, e := Load(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
}

func TestDevelopmentDefaultsToLoopbackOnlyWhenHostUnset(t *testing.T) {
	t.Setenv("HOST", "")
	os.Unsetenv("HOST")
	t.Setenv("AIPLEX_DEV_MODE", "true")
	cfg, err := Load(context.Background())
	if err != nil || cfg.Host != "127.0.0.1" {
		t.Fatalf("development default bind: %+v %v", cfg, err)
	}
	t.Setenv("AIPLEX_DEV_MODE", "false")
	if cfg, err = Load(context.Background()); err != nil || cfg.Host != "0.0.0.0" {
		t.Fatalf("production default bind changed: %+v %v", cfg, err)
	}
}
