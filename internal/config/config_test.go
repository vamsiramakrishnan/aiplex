package config

import (
	"context"
	"testing"
)

func TestDevelopmentRequiresLoopbackBind(t *testing.T) {
	t.Setenv("AIPLEX_DEV_MODE", "true")
	for _, host := range []string{"0.0.0.0", "example.com", "::"} {
		t.Setenv("HOST", host)
		if _, e := Load(context.Background()); e == nil {
			t.Fatalf("accepted insecure bind %s", host)
		}
	}
	for _, host := range []string{"127.0.0.1", "::1", "localhost"} {
		t.Setenv("HOST", host)
		if _, e := Load(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
}
