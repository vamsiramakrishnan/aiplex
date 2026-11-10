//go:build e2e

// Package e2e drives the real aiplex-api binary, as a separate process, with
// the real aiplex CLI and plain HTTP. Every upstream (model providers, MCP
// servers, the token issuer, JWKS servers) is a local mock on loopback, so the
// suite needs no network, Docker or cloud account.
//
// Run with:
//
//	go test -tags e2e ./tests/e2e/ -count=1
//
// E2E_RACE=1 builds the API with the race detector (used for the concurrency
// tests; slower). E2E_KEEP_LOGS=1 keeps API logs in the test's temp dir.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

var (
	binDir  string
	apiBin  string
	cliBin  string
	repoDir string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aiplex-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binDir = dir
	_, file, _, _ := runtime.Caller(0)
	repoDir = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	apiBin = filepath.Join(dir, "aiplex-api")
	cliBin = filepath.Join(dir, "aiplex")
	build := func(out, pkg string, race bool) error {
		args := []string{"build", "-o", out}
		if race {
			args = append(args, "-race")
		}
		args = append(args, pkg)
		cmd := exec.Command("go", args...)
		cmd.Dir = repoDir
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		return cmd.Run()
	}
	race := os.Getenv("E2E_RACE") == "1"
	if err := build(apiBin, "./cmd/aiplex-api", race); err != nil {
		fmt.Fprintln(os.Stderr, "build aiplex-api:", err)
		os.Exit(1)
	}
	if err := build(cliBin, "./cmd/aiplex-cli", false); err != nil {
		fmt.Fprintln(os.Stderr, "build aiplex:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// env is one running stack plus an isolated CLI home.
type env struct {
	t     *testing.T
	stack *devstack.Stack
	home  string
	log   *syncBuffer
	api   devstack.API
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type stackOption func(*devstack.Options)

func start(t *testing.T, opts ...stackOption) *env {
	t.Helper()
	log := &syncBuffer{}
	o := devstack.Options{APIBinary: apiBin, Log: log, UpstreamTimeout: 3 * time.Second}
	for _, f := range opts {
		f(&o)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, err := devstack.Start(ctx, o)
	if err != nil {
		t.Fatalf("start stack: %v\n--- API log ---\n%s", err, log.String())
	}
	e := &env{t: t, stack: s, home: t.TempDir(), log: log, api: devstack.API{URL: s.APIURL, Token: s.Token}}
	t.Cleanup(func() {
		s.Stop()
		if t.Failed() || os.Getenv("E2E_KEEP_LOGS") == "1" {
			path := filepath.Join(os.TempDir(), "aiplex-e2e-"+strings.ReplaceAll(t.Name(), "/", "_")+".log")
			_ = os.WriteFile(path, []byte(log.String()), 0o600)
			t.Logf("API log: %s", path)
		}
		if strings.Contains(log.String(), "WARNING: DATA RACE") {
			t.Errorf("data race reported by aiplex-api; see log")
		}
		if strings.Contains(log.String(), "panic") && strings.Contains(log.String(), "goroutine ") {
			t.Errorf("aiplex-api panicked; see log")
		}
	})
	return e
}

// result of a CLI run.
type result struct {
	Out, Err string
	Code     int
}

func (r result) String() string {
	return fmt.Sprintf("exit %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Out, r.Err)
}

// cliEnv runs the CLI with an isolated HOME and the given extra environment.
func (e *env) cliEnv(extra []string, args ...string) result {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBin, args...)
	cmd.Env = append(cleanEnv(), "HOME="+e.home, "NO_COLOR=1", "TERM=dumb")
	cmd.Env = append(cmd.Env, extra...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		if x, ok := err.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			e.t.Fatalf("run cli: %v", err)
		}
	}
	return result{Out: out.String(), Err: errb.String(), Code: code}
}

// cli runs the CLI against the stack as the local admin developer.
func (e *env) cli(args ...string) result {
	e.t.Helper()
	return e.cliEnv([]string{"AIPLEX_URL=" + e.stack.APIURL, "AIPLEX_TOKEN=" + e.stack.Token}, args...)
}

// mustCLI fails the test unless the CLI exits 0.
func (e *env) mustCLI(args ...string) result {
	e.t.Helper()
	r := e.cli(args...)
	if r.Code != 0 {
		e.t.Fatalf("aiplex %s: %s", strings.Join(args, " "), r)
	}
	return r
}

func cleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "AIPLEX_") || name == "HOME" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// writeFile writes content into the test's temp dir and returns the path.
func writeFile(t *testing.T, name string, v any) string {
	t.Helper()
	var data []byte
	switch x := v.(type) {
	case string:
		data = []byte(x)
	case []byte:
		data = x
	default:
		var err error
		data, err = json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// httpResp is a captured HTTP response.
type httpResp struct {
	Status int
	Header http.Header
	Body   string
}

func (r httpResp) JSON(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.Body), &out); err != nil {
		t.Fatalf("response is not a JSON object (%d): %s", r.Status, r.Body)
	}
	return out
}

// do sends a request to the API. headers alternate name, value.
func (e *env) do(method, path, body string, headers ...string) httpResp {
	e.t.Helper()
	req, err := http.NewRequest(method, e.stack.APIURL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
			continue
		}
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return httpResp{Status: resp.StatusCode, Header: resp.Header, Body: string(raw)}
}

// auth returns the Authorization header pair for the developer token.
func (e *env) auth() []string { return []string{"Authorization", "Bearer " + e.stack.Token} }

func (e *env) post(path, body string, headers ...string) httpResp {
	e.t.Helper()
	return e.do("POST", path, body, append(e.auth(), headers...)...)
}

// sseEvents splits an SSE body into data payloads.
func sseData(body string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "data:") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return out
}

func contains(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("%s: want %q in:\n%s", what, needle, haystack)
	}
}

func notContains(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("%s: did not want %q in:\n%s", what, needle, haystack)
	}
}

// eventually retries f until it returns nil or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, f func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := f()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
