//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// 1. Health, readiness, doctor, whoami and CLI config/context commands.
func TestBasicsHealthDoctorWhoamiAndContexts(t *testing.T) {
	e := start(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		if r := e.do("GET", path, ""); r.Status != 200 {
			t.Fatalf("%s: %d %s", path, r.Status, r.Body)
		}
	}

	t.Run("contexts", func(t *testing.T) {
		r := e.cliEnv(nil, "config", "set-context", "local", "--url", e.stack.APIURL)
		if r.Code != 0 {
			t.Fatal(r)
		}
		contains(t, r.Out, `Context "local" set and activated.`, "set-context")
		r = e.cliEnv(nil, "config", "current-context")
		contains(t, r.Out, "local", "current-context")
		r = e.cliEnv(nil, "config", "set-context", "other", "--url", "http://127.0.0.1:1")
		if r.Code != 0 {
			t.Fatal(r)
		}
		r = e.cliEnv(nil, "ctx", "local")
		if r.Code != 0 {
			t.Fatal(r)
		}
		r = e.cliEnv(nil, "config", "show")
		if r.Code != 0 {
			t.Fatal(r)
		}
		contains(t, r.Out, e.stack.APIURL, "config show")
		r = e.cliEnv(nil, "config", "use-context", "missing")
		if r.Code == 0 {
			t.Fatalf("use-context of a missing context succeeded: %s", r)
		}
		contains(t, r.Err, `context "missing" not found`, "use-context missing")
		// Destructive commands need --yes when there is no terminal to ask on.
		r = e.cliEnv(nil, "config", "delete-context", "other")
		if r.Code != 2 || !strings.Contains(r.Err, "--yes") {
			t.Fatalf("delete-context without --yes and without a terminal: %s", r)
		}
		r = e.cliEnv(nil, "config", "delete-context", "other", "--yes")
		if r.Code != 0 {
			t.Fatal(r)
		}
	})

	t.Run("whoami", func(t *testing.T) {
		r := e.cliEnv(nil, "whoami")
		if r.Code != 0 {
			t.Fatal(r)
		}
		contains(t, r.Out, "Context:  local", "whoami")
		// The verified identity, through the server.
		r = e.cli("whoami", "--server", "-o", "json")
		if r.Code != 0 {
			t.Fatal(r)
		}
		contains(t, r.Out, `"admin"`, "whoami --server roles")
		contains(t, r.Out, "developer@localhost", "whoami --server email")
	})

	t.Run("doctor", func(t *testing.T) {
		// A local context with a token: doctor must say the API is healthy and
		// authenticated, and must not fail the run on cloud tooling that a
		// local setup does not use.
		r := e.cli("doctor")
		if r.Code != 0 {
			t.Fatal(r)
		}
		contains(t, r.Out, "API healthy", "doctor")
		contains(t, r.Out, "Authenticated access working", "doctor")
		notContains(t, r.Out, "[FAIL]", "doctor against a local API")
	})

	t.Run("health", func(t *testing.T) {
		r := e.cli("health")
		if r.Code != 0 {
			t.Fatal(r)
		}
		contains(t, r.Out, "API Server", "health")
	})
}

// The development-mode loopback guard, against the real binary.
func TestDevelopmentModeRefusesNonLoopbackBind(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "::", "192.0.2.1"} {
		cmd := exec.Command(apiBin)
		cmd.Env = append(cleanEnv(), "AIPLEX_DEV_MODE=true", "HOST="+host, "PORT=0")
		done := make(chan struct{})
		var out []byte
		var err error
		go func() { out, err = cmd.CombinedOutput(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatalf("HOST=%s: development API kept running on a non-loopback bind", host)
		}
		if err == nil {
			t.Fatalf("HOST=%s: development API exited cleanly instead of refusing", host)
		}
		contains(t, string(out), "AIPLEX_DEV_MODE requires a loopback HOST", "refusal for HOST="+host)
	}
	// With HOST unset, development mode binds loopback.
	cmd := exec.Command(apiBin)
	cmd.Env = append(cleanEnvWithout("HOST"), "AIPLEX_DEV_MODE=true", "PORT=0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(1500 * time.Millisecond)
	if cmd.ProcessState != nil {
		t.Fatal("development API with HOST unset exited")
	}
}

func cleanEnvWithout(names ...string) []string {
	var out []string
	for _, kv := range cleanEnv() {
		name, _, _ := strings.Cut(kv, "=")
		skip := false
		for _, n := range names {
			if n == name {
				skip = true
			}
		}
		if !skip {
			out = append(out, kv)
		}
	}
	return out
}

var _ = os.Getenv
