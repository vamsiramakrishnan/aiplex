package main

import "testing"

func TestDevOTelEndpointValidation(t *testing.T) {
	env, err := devOTelEnv("http://127.0.0.1:4318")
	if err != nil || len(env) != 1 || env[0] != "OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318" {
		t.Fatalf("valid endpoint: %v %v", env, err)
	}
	if env, err := devOTelEnv(""); err != nil || env != nil {
		t.Fatalf("no flag sets nothing: %v %v", env, err)
	}
	for _, bad := range []string{"127.0.0.1:4318", "ftp://x", "http://user:secret@collector:4318", "http://collector:4318?token=x"} {
		if _, err := devOTelEnv(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
