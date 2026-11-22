//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// `aiplex dev --provider X --api-key-env NAME` wires a real provider surface
// whose key reaches only the API process: it is never printed and never
// written under HOME or the temporary directory.
func TestDevCommandRealProviderKeyHandling(t *testing.T) {
	const secret = "sk-e2e-never-print-this-0123456789"
	home := t.TempDir()
	tmp := t.TempDir()
	port, err := devstack.FreePort()
	if err != nil {
		t.Fatal(err)
	}
	env := append(cleanEnv(), "HOME="+home, "TMPDIR="+tmp, "E2E_PROVIDER_KEY="+secret, "AIPLEX_API_BIN="+apiBin)

	// Refusals that need no server.
	for _, args := range [][]string{
		{"dev", "--provider", "anthropic"},
		{"dev", "--provider", "anthropic", "--api-key-env", secret + "=x"},
		{"dev", "--api-key-env", "E2E_PROVIDER_KEY"},
		{"dev", "--provider", "anthropic", "--api-key-env", "E2E_UNSET_VARIABLE"},
	} {
		cmd := exec.Command(cliBin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("aiplex %v succeeded", args)
		}
		if bytes.Contains(out, []byte(secret)) {
			t.Fatalf("aiplex %v printed the key", args)
		}
	}

	out := &syncBuffer{}
	cmd := exec.Command(cliBin, "dev", "--provider", "anthropic", "--api-key-env", "E2E_PROVIDER_KEY", "--port", strconv.Itoa(port), "--no-console")
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	e := &env2{t: t, home: home}
	eventually(t, 60*time.Second, func() error {
		if !strings.Contains(out.String(), "Press Ctrl+C") {
			return &timeoutErr{out.String()}
		}
		return nil
	})
	contains(t, out.String(), "via anthropic (key from $E2E_PROVIDER_KEY)", "summary")
	contains(t, out.String(), "x-api-key: $AIPLEX_TOKEN", "anthropic try line")
	r := e.run(env, "llm", "surfaces", "get", "local-models", "-o", "json")
	contains(t, r, `"secret_ref": "dev-provider-key"`, "surface references the secret by name")
	notContains(t, r, secret, "surface output")
	envOut := e.run(env, "dev", "env")
	contains(t, envOut, "ANTHROPIC_BASE_URL", "dev env for anthropic")

	try := exec.Command(cliBin, "dev", "try")
	try.Env = env
	if got, err := try.CombinedOutput(); err == nil || !strings.Contains(string(got), "mock provider") {
		t.Fatalf("dev try on a real-provider stack: %v, %s", err, got)
	}

	_ = cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("aiplex dev exit on Ctrl+C: %v\n%s", err, out.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("aiplex dev did not stop on Ctrl+C")
	}
	if strings.Contains(out.String(), secret) {
		t.Fatal("aiplex dev printed the provider key")
	}
	for _, dir := range []string{home, tmp} {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, _ := os.ReadFile(path)
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("provider key written to %s", path)
			}
			return nil
		})
	}
}

type env2 struct {
	t    *testing.T
	home string
}

func (e *env2) run(env []string, args ...string) string {
	e.t.Helper()
	cmd := exec.Command(cliBin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("aiplex %v: %v\n%s", args, err, out)
	}
	return string(out)
}

type timeoutErr struct{ out string }

func (e *timeoutErr) Error() string { return "aiplex dev not ready:\n" + e.out }

// A shell may already point its SDKs and CLI at another deployment. dev try
// must use the saved local stack, and must show the record for this call.
func TestDevTryJoinsOneLocalCallToItsLedgerRecord(t *testing.T) {
	_, run, _ := realDev(t)
	stale := []string{"AIPLEX_URL=http://127.0.0.1:1", "AIPLEX_TOKEN=unrelated"}
	out, errOut, code := run(stale, "dev", "try")
	if code != 0 {
		t.Fatalf("first local request: %d %s", code, errOut)
	}
	for _, want := range []string{"Hello from the AIPlex mock model", "developer@localhost", "(estimated)", "aiplex usage get "} {
		contains(t, out, want, "dev try")
	}
	out, errOut, code = run(stale, "dev", "try", "-o", "json")
	var result struct {
		Reply  string             `json:"reply"`
		Record usageledger.Record `json:"record"`
	}
	if err := json.Unmarshal([]byte(out), &result); code != 0 || err != nil {
		t.Fatalf("structured local request: %v, %d %s %s", err, code, out, errOut)
	}
	r := result.Record
	if !strings.Contains(result.Reply, "Hello") || r.ID == "" || r.RequestID == "" || r.ModelID != devstack.MockModel || r.AccessPointID != devstack.SeedAccessPoint || r.Status != usageledger.StatusOK {
		t.Fatalf("unexpected local reply or record: %+v", result)
	}
	out, errOut, code = run(nil, "usage", "records", "-o", "json")
	var saved usageledger.Page
	if err := json.Unmarshal([]byte(out), &saved); code != 0 || err != nil || len(saved.Records) != 2 || saved.Records[0].RequestID != r.RequestID || saved.Records[1].RequestID == r.RequestID {
		t.Fatalf("expected two distinct calls and a join to the latest one: %v, %d %s %s", err, code, out, errOut)
	}
}
