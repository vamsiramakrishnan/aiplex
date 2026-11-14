//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

func seeded(t *testing.T) *env {
	t.Helper()
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	return e
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertNoTokenAnywhere walks the isolated home: the live token must be in no
// file the command wrote, nor in any backup or manifest.
func assertNoTokenAnywhere(t *testing.T, e *env) {
	t.Helper()
	filepath.Walk(e.home, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.Contains(fileText(t, p), e.stack.Token) {
			t.Errorf("access token found in %s", p)
		}
		return nil
	})
}

func TestConnectWriteClaudeCode(t *testing.T) {
	e := seeded(t)
	settings := filepath.Join(e.home, ".claude", "settings.json")
	claudeJSON := filepath.Join(e.home, ".claude.json")
	orig := "{\n  \"theme\": \"dark\"\n}\n"
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(settings, []byte(orig), 0o600)

	// Without a terminal and without --yes the command refuses and writes nothing.
	r := e.cli("connect", "local", "--client", "claude-code", "--write")
	if r.Code != 2 || fileText(t, settings) != orig {
		t.Fatalf("expected a refusal that writes nothing:\n%s", r)
	}
	// --dry-run shows the diff and writes nothing.
	r = e.mustCLI("connect", "local", "--client", "claude-code", "--write", "--dry-run")
	contains(t, r.Out, "+    \"ANTHROPIC_BASE_URL\": \""+e.stack.APIURL+"/ap/local\"", "dry-run diff")
	contains(t, r.Out, "Dry run: nothing was written.", "dry-run message")
	if fileText(t, settings) != orig {
		t.Fatal("dry run changed the file")
	}

	r = e.mustCLI("connect", "local", "--client", "claude-code", "--write", "--yes")
	contains(t, r.Out, "Wrote 2 file(s)", "write summary")
	contains(t, r.Out, "ANTHROPIC_AUTH_TOKEN", "remaining step")
	got := fileText(t, settings)
	contains(t, got, `"theme": "dark"`, "existing key kept")
	contains(t, got, `"ANTHROPIC_BASE_URL": "`+e.stack.APIURL+`/ap/local"`, "base URL")
	contains(t, got, `"ANTHROPIC_MODEL": "mock-claude"`, "model")
	contains(t, fileText(t, claudeJSON), `"aiplex-local"`, "MCP server entry")
	contains(t, fileText(t, claudeJSON), "Bearer ${AIPLEX_TOKEN}", "MCP header references the env var")
	assertNoTokenAnywhere(t, e)

	// Idempotent: a second run has nothing to do and makes no new backup.
	backups, _ := os.ReadDir(filepath.Join(e.home, ".aiplex", "connect-backups"))
	r = e.mustCLI("connect", "local", "--client", "claude-code", "--write", "--yes")
	contains(t, r.Out, "Nothing to change.", "second run")
	if after, _ := os.ReadDir(filepath.Join(e.home, ".aiplex", "connect-backups")); len(after) != len(backups) {
		t.Error("an unchanged run must not create a backup")
	}

	// Undo restores the original and removes the file that did not exist.
	r = e.mustCLI("connect", "--undo", "--client", "claude-code", "--yes")
	contains(t, r.Out, "Restored 2 file(s).", "undo")
	if fileText(t, settings) != orig {
		t.Errorf("settings.json not restored:\n%s", fileText(t, settings))
	}
	if _, err := os.Stat(claudeJSON); !os.IsNotExist(err) {
		t.Error("~/.claude.json did not exist before and should be removed")
	}
	if r := e.cli("connect", "--undo", "--client", "claude-code", "--yes"); r.Code == 0 {
		t.Errorf("a second undo has nothing left to restore:\n%s", r)
	}
}

func TestConnectWriteCodex(t *testing.T) {
	e := seeded(t)
	cfg := filepath.Join(e.home, ".codex", "config.toml")
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	orig := "# keep me\nsandbox_mode = \"read-only\"\n\n[profiles.fast]\nmodel = \"gpt-fast\"\n"
	os.WriteFile(cfg, []byte(orig), 0o600)

	e.mustCLI("connect", "local", "--client", "codex", "--write", "--yes")
	got := fileText(t, cfg)
	for _, want := range []string{"# keep me", `sandbox_mode = "read-only"`, `model_provider = "aiplex"`, `model = "mock-responses"`,
		"[model_providers.aiplex]", `base_url = "` + e.stack.APIURL + `/ap/local/v1"`, `env_key = "AIPLEX_TOKEN"`, "[mcp_servers.aiplex-local]"} {
		contains(t, got, want, "codex config")
	}
	assertNoTokenAnywhere(t, e)

	r := e.mustCLI("connect", "local", "--client", "codex", "--write", "--yes")
	contains(t, r.Out, "Nothing to change.", "second run")
	if fileText(t, cfg) != got {
		t.Error("second run changed the file")
	}

	e.mustCLI("connect", "--undo", "--client", "codex", "--yes")
	if fileText(t, cfg) != orig {
		t.Errorf("config.toml not restored:\n%s", fileText(t, cfg))
	}
}

func TestConnectWriteRefusesInvalidFilesAndAntigravity(t *testing.T) {
	e := seeded(t)
	settings := filepath.Join(e.home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	os.WriteFile(settings, []byte("{ not json"), 0o600)
	r := e.cli("connect", "local", "--client", "claude-code", "--write", "--yes")
	if r.Code == 0 || fileText(t, settings) != "{ not json" {
		t.Fatalf("an invalid file must be refused untouched:\n%s", r)
	}
	r = e.cli("connect", "local", "--client", "antigravity-acp", "--write", "--yes")
	if r.Code != 2 || !strings.Contains(r.Err, "print-only") {
		t.Fatalf("antigravity-acp is print-only:\n%s", r)
	}
	// antigravity: only the secret-free stdio MCP server is written.
	r = e.cli("connect", "local", "--client", "antigravity", "--write", "--yes")
	if r.Code != 0 {
		t.Fatalf("antigravity MCP write failed:\n%s", r)
	}
	got := fileText(t, filepath.Join(e.home, ".gemini", "config", "mcp_config.json"))
	if !strings.Contains(got, `"command": "aiplex"`) || strings.Contains(got, "AIPLEX_TOKEN") {
		t.Fatalf("unexpected antigravity file:\n%s", got)
	}
}

func TestMCPServeStdio(t *testing.T) {
	e := seeded(t)
	cmd := exec.Command(cliBin, "mcp", "serve")
	cmd.Env = append(cleanEnv(), "HOME="+e.home, "AIPLEX_URL="+e.stack.APIURL, "AIPLEX_TOKEN="+e.stack.Token)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Wait() })
	rd := bufio.NewReader(stdout)
	ask := func(line string) map[string]any {
		t.Helper()
		if _, err := stdin.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
		l, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout must carry only JSON-RPC, got %q", l)
		}
		return m
	}
	init := ask(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}`)
	if init["result"].(map[string]any)["serverInfo"].(map[string]any)["name"] != "aiplex" {
		t.Fatalf("initialize: %v", init)
	}
	stdin.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	list := ask(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if n := len(list["result"].(map[string]any)["tools"].([]any)); n < 6 {
		t.Fatalf("tools/list returned %d tools", n)
	}
	who := ask(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`)
	res := who["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] != false || !strings.Contains(text, "developer@localhost") {
		t.Fatalf("whoami: %v", who)
	}
	if strings.Contains(text, e.stack.Token) {
		t.Error("the token must never appear in a tool result")
	}
	conf := ask(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_connect_config","arguments":{"access_point":"local","client":"codex"}}}`)
	if conf["result"].(map[string]any)["isError"] != false {
		t.Fatalf("get_connect_config: %v", conf)
	}
}

func TestConnectWriteBranchyard(t *testing.T) {
	e := seeded(t)

	// Without --write the tables are printed, with no token in them.
	r := e.mustCLI("connect", "local", "--client", "branchyard")
	contains(t, r.Out, "[models.backends.aiplex-local-anthropic]", "printed backend")
	contains(t, r.Out, `control_plane = "aiplex"`, "printed marker")
	contains(t, r.Out, "aiplex auth print-token", "token step")
	if strings.Contains(r.Out, e.stack.Token) {
		t.Fatal("the printed tables hold the token")
	}

	// The default file is ./branchyard.toml of the working directory (the
	// package directory here); it does not exist, so nothing is created.
	r = e.mustCLI("connect", "local", "--client", "branchyard", "--write", "--yes")
	contains(t, r.Out, "nothing was written", "missing default file")
	if _, err := os.Stat("branchyard.toml"); !os.IsNotExist(err) {
		os.Remove("branchyard.toml")
		t.Fatal("the default file was created in a directory that is not a Branchyard project")
	}

	yard := filepath.Join(e.home, "yard", "branchyard.toml")
	os.MkdirAll(filepath.Dir(yard), 0o755)
	orig := "# my yard\n[defaults]\nharness = \"claude-code\"   # keep\n\n[models]\nallow = [\"*\"]\n\n[models.backends.anthropic]\napi = \"anthropic\"\n\n# my route\n[[models.routes]]\nmodel = \"claude-*\"\nbackends = [\"anthropic\"]\n"
	os.WriteFile(yard, []byte(orig), 0o600)

	r = e.mustCLI("connect", "local", "--client", "branchyard", "--write", "--dry-run", "--file", yard)
	contains(t, r.Out, "Dry run: nothing was written.", "dry-run message")
	if fileText(t, yard) != orig {
		t.Fatal("dry run changed the file")
	}
	r = e.mustCLI("connect", "local", "--client", "branchyard", "--write", "--yes", "--file", yard)
	contains(t, r.Out, "Wrote 1 file(s)", "write summary")
	contains(t, r.Out, "AIPLEX_TOKEN", "remaining step")
	got := fileText(t, yard)
	if !strings.HasPrefix(strings.Replace(got, "aiplex = \"AIPLEX_TOKEN\"\n", "", 1), orig) && !strings.HasPrefix(got, orig) {
		t.Errorf("the person's text was not kept:\n%s", got)
	}
	type parsed struct {
		Secrets map[string]string `toml:"secrets"`
		Models  struct {
			Backends map[string]map[string]any `toml:"backends"`
			Routes   []map[string]any          `toml:"routes"`
		} `toml:"models"`
	}
	var cfg, was parsed
	if err := toml.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatalf("the written file is not TOML: %v\n%s", err, got)
	}
	if err := toml.Unmarshal([]byte(orig), &was); err != nil {
		t.Fatal(err)
	}
	if cfg.Secrets["aiplex"] != "AIPLEX_TOKEN" {
		t.Errorf("secrets: %v", cfg.Secrets)
	}
	// The AIPlex backends are exactly the two expected, with exactly these keys.
	aiplexBackends := map[string]bool{}
	for name, b := range cfg.Models.Backends {
		if b["control_plane"] == "aiplex" {
			aiplexBackends[name] = true
		}
	}
	wantBackends := map[string]string{"aiplex-local-anthropic": "anthropic", "aiplex-local-openai": "openai"}
	if len(aiplexBackends) != len(wantBackends) {
		t.Errorf("AIPlex backends: %v", aiplexBackends)
	}
	for name, api := range wantBackends {
		b := cfg.Models.Backends[name]
		if !aiplexBackends[name] || len(b) != 4 || b["api"] != api || b["url"] != e.stack.APIURL+"/ap/local" || b["key"] != "aiplex" {
			t.Errorf("backend %s: %v", name, b)
		}
	}
	// The routes that name them are exactly the expected (model, backend) pairs.
	gotPairs := map[string]string{}
	for _, r := range cfg.Models.Routes {
		for _, b := range r["backends"].([]any) {
			if aiplexBackends[b.(string)] {
				if len(r) != 2 || len(r["backends"].([]any)) != 1 {
					t.Errorf("AIPlex route has more than a model and one backend: %v", r)
				}
				gotPairs[r["model"].(string)] = b.(string)
			}
		}
	}
	wantPairs := map[string]string{"mock-claude": "aiplex-local-anthropic", "mock-gpt": "aiplex-local-openai", "mock-responses": "aiplex-local-openai"}
	if len(gotPairs) != len(wantPairs) {
		t.Errorf("AIPlex routes: %v", gotPairs)
	}
	for m, b := range wantPairs {
		if gotPairs[m] != b {
			t.Errorf("route %s -> %q, want %q", m, gotPairs[m], b)
		}
	}
	// Everything that was in the file before is still there, equal.
	for name, b := range was.Models.Backends {
		if !reflect.DeepEqual(cfg.Models.Backends[name], b) {
			t.Errorf("backend %s changed: %v -> %v", name, b, cfg.Models.Backends[name])
		}
	}
	for i, r := range was.Models.Routes {
		if i >= len(cfg.Models.Routes) || !reflect.DeepEqual(cfg.Models.Routes[i], r) {
			t.Errorf("route %d changed", i)
		}
	}
	assertNoTokenAnywhere(t, e)

	r = e.mustCLI("connect", "local", "--client", "branchyard", "--write", "--yes", "--file", yard)
	contains(t, r.Out, "Nothing to change.", "second run")
	if fileText(t, yard) != got {
		t.Error("second run changed the file")
	}

	e.mustCLI("connect", "--undo", "--client", "branchyard", "--yes")
	if fileText(t, yard) != orig {
		t.Errorf("branchyard.toml not restored:\n%s", fileText(t, yard))
	}

	// A route that mixes in a plain backend is refused and named.
	mixed := orig + "\n[[models.routes]]\nmodel = \"mock-*\"\nbackends = [\"anthropic\"]\n"
	os.WriteFile(yard, []byte(mixed), 0o600)
	r = e.cli("connect", "local", "--client", "branchyard", "--write", "--yes", "--file", yard)
	if r.Code == 0 || !strings.Contains(r.Err+r.Out, "models.routes[1]") || fileText(t, yard) != mixed {
		t.Errorf("a mixed route must be refused by name and the file kept:\n%s", r)
	}
}
