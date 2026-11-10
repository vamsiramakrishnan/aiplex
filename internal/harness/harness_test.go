package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

const planted = "tok-PLANTED-0123456789-secret"

func connectFixture() *aiplex.AccessPointConnect {
	return &aiplex.AccessPointConnect{
		AccessPointID: "local",
		BaseURLs: map[string]string{
			"anthropic": "http://127.0.0.1:18590/ap/local",
			"openai":    "http://127.0.0.1:18590/ap/local/v1",
			"genai":     "http://127.0.0.1:18590/ap/local",
		},
		Models: []aiplex.ConnectModel{
			{Name: "mock-gpt", Protocols: []string{"openai-chat-completions"}},
			{Name: "mock-claude", Protocols: []string{"anthropic-messages"}},
			{Name: "mock-responses", Protocols: []string{"openai-responses"}},
		},
		MCPURL: "http://127.0.0.1:18590/ap/local/mcp",
	}
}

// isolate points every home-like variable at a temp directory and plants a
// token in the environment so tests can prove it never reaches a file.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("AIPLEX_TOKEN", planted)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", planted)
	return home
}

func apply(t *testing.T, home string, p *Plan) string {
	t.Helper()
	dir, err := Apply(BackupRoot(home), p.Client, "local", p.Changes, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertNoToken(t *testing.T, root string) {
	t.Helper()
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), planted) {
			t.Errorf("token value found in %s", path)
		}
		return nil
	})
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClaudeCodeMergeIdempotentAndUndo(t *testing.T) {
	home := isolate(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	claudeJSON := filepath.Join(home, ".claude.json")
	orig := "{\n  \"theme\": \"dark\",\n  \"env\": {\n    \"FOO\": \"bar\"\n  },\n  \"permissions\": {\"allow\": [\"Bash(ls)\"]}\n}\n"
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeJSON, []byte(`{"numStartups": 12, "mcpServers": {"other": {"command": "x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := Options{Home: home, Cwd: t.TempDir(), Telemetry: "http://localhost:4318"}
	p, err := PlanClaudeCode(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	dir := apply(t, home, p)
	got := read(t, settings)
	for _, want := range []string{`"theme": "dark"`, `"FOO": "bar"`, `"Bash(ls)"`, `"ANTHROPIC_BASE_URL": "http://127.0.0.1:18590/ap/local"`, `"ANTHROPIC_MODEL": "mock-claude"`,
		`"CLAUDE_CODE_ENABLE_TELEMETRY": "1"`, `"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"`, `"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318"`} {
		if !strings.Contains(got, want) {
			t.Errorf("settings.json lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "OTEL_LOG_USER_PROMPTS") {
		t.Error("prompt logging must never be enabled")
	}
	if strings.Index(got, `"theme"`) > strings.Index(got, `"env"`) {
		t.Error("key order was not preserved")
	}
	st, _ := os.Stat(settings)
	if st.Mode().Perm() != 0o640 {
		t.Errorf("mode changed to %v", st.Mode().Perm())
	}
	cj := read(t, claudeJSON)
	if !strings.Contains(cj, `"numStartups": 12`) || !strings.Contains(cj, `"other"`) || !strings.Contains(cj, `"aiplex-local"`) || !strings.Contains(cj, "Bearer ${AIPLEX_TOKEN}") {
		t.Errorf("~/.claude.json merge wrong:\n%s", cj)
	}

	// Idempotent: a second plan has nothing to change.
	p2, err := PlanClaudeCode(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p2.Changes {
		if c.Changed() {
			t.Errorf("second run would change %s:\n%s", c.Path, c.Diff())
		}
	}
	if d, _ := Apply(BackupRoot(home), "claude-code", "local", p2.Changes, time.Now()); d != "" {
		t.Error("an unchanged plan must not create a backup")
	}

	assertNoToken(t, home)

	// Undo restores the originals.
	d, m, err := LatestBackup(BackupRoot(home), "claude-code")
	if err != nil || d != dir {
		t.Fatalf("latest backup: %v %s", err, d)
	}
	changes, err := PlanUndo(d, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyUndo(d, m, changes); err != nil {
		t.Fatal(err)
	}
	if read(t, settings) != orig {
		t.Errorf("settings.json not restored:\n%s", read(t, settings))
	}
	if _, _, err := LatestBackup(BackupRoot(home), "claude-code"); err == nil {
		t.Error("an undone backup must not be offered again")
	}
}

func TestUndoRemovesFileThatDidNotExist(t *testing.T) {
	home := isolate(t)
	p, err := PlanClaudeCode(connectFixture(), Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	settings := filepath.Join(home, ".claude", "settings.json")
	st, err := os.Stat(settings)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("new files must be 0600: %v %v", err, st)
	}
	d, m, _ := LatestBackup(BackupRoot(home), "")
	changes, _ := PlanUndo(d, m)
	if err := ApplyUndo(d, m, changes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Error("a file AIPlex created should be removed by undo")
	}
}

func TestInvalidJSONIsRefusedNotOverwritten(t *testing.T) {
	home := isolate(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	os.WriteFile(settings, []byte("{ // comment\n}"), 0o600)
	if _, err := PlanClaudeCode(connectFixture(), Options{Home: home}); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("want refusal, got %v", err)
	}
	if read(t, settings) != "{ // comment\n}" {
		t.Error("file was touched")
	}
}

func TestNonObjectEnvIsRefused(t *testing.T) {
	home := isolate(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	os.WriteFile(settings, []byte(`{"env": "oops"}`), 0o600)
	if _, err := PlanClaudeCode(connectFixture(), Options{Home: home}); err == nil {
		t.Fatal("a non-object env must be refused")
	}
}

func TestClaudeProjectScopeAndTelemetryRule(t *testing.T) {
	home := isolate(t)
	cwd := t.TempDir()
	if _, err := PlanClaudeCode(connectFixture(), Options{Home: home, Cwd: cwd, Scope: "project", Telemetry: "http://localhost:4317"}); err == nil {
		t.Error("telemetry in project scope must be refused")
	}
	p, err := PlanClaudeCode(connectFixture(), Options{Home: home, Cwd: cwd, Scope: "project", TokenHelper: true})
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	if !strings.Contains(read(t, filepath.Join(cwd, ".claude", "settings.local.json")), `"apiKeyHelper": "aiplex auth print-token"`) {
		t.Error("apiKeyHelper missing")
	}
	if !strings.Contains(read(t, filepath.Join(cwd, ".mcp.json")), `"aiplex-local"`) {
		t.Error(".mcp.json missing the server")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Error("project scope must not touch the user files")
	}
	assertNoToken(t, home)
	assertNoToken(t, cwd)
}

func TestCodexKeepsCommentsAndOtherKeys(t *testing.T) {
	home := isolate(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	orig := "# my codex config\nmodel = \"gpt-old\"   # favourite\nsandbox_mode = \"read-only\"\n\n[profiles.fast]\nmodel = \"gpt-fast\"\n\n# my notes\n[mcp_servers.docs]\ncommand = \"npx\"\n"
	os.WriteFile(cfg, []byte(orig), 0o600)

	opts := Options{Home: home, Telemetry: "http://localhost:4318"}
	p, err := PlanCodex(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	got := read(t, cfg)
	for _, want := range []string{"# my codex config", "# aiplex disabled: model = \"gpt-old\"   # favourite", "sandbox_mode = \"read-only\"", "[profiles.fast]\nmodel = \"gpt-fast\"", "# my notes",
		"model_provider = \"aiplex\"", "model = \"mock-responses\"", "[model_providers.aiplex]", `env_key = "AIPLEX_TOKEN"`, `wire_api = "responses"`,
		"[mcp_servers.aiplex-local]", `bearer_token_env_var = "AIPLEX_TOKEN"`, "[otel]", "log_user_prompt = false", `otlp-http`, "http://localhost:4318/v1/logs"} {
		if !strings.Contains(got, want) {
			t.Errorf("config.toml lacks %q:\n%s", want, got)
		}
	}
	p2, err := PlanCodex(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Changes[0].Changed() {
		t.Errorf("second run would change the file:\n%s", p2.Changes[0].Diff())
	}
	assertNoToken(t, home)

	// Rerunning without --with-telemetry drops the managed [otel] table and
	// keeps everything the user wrote.
	p3, err := PlanCodex(connectFixture(), Options{Home: home})
	if err != nil {
		t.Fatalf("rerun without telemetry: %v", err)
	}
	if after := string(p3.Changes[0].After); strings.Contains(after, "[otel]") || !strings.Contains(after, "# my notes") {
		t.Errorf("rerun without telemetry wrong:\n%s", after)
	}

	d, m, _ := LatestBackup(BackupRoot(home), "codex")
	ch, _ := PlanUndo(d, m)
	if err := ApplyUndo(d, m, ch); err != nil {
		t.Fatal(err)
	}
	if read(t, cfg) != orig {
		t.Errorf("not restored:\n%s", read(t, cfg))
	}
}

func TestCodexRefusals(t *testing.T) {
	home := isolate(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	os.MkdirAll(filepath.Dir(cfg), 0o755)

	os.WriteFile(cfg, []byte("model = \n"), 0o600)
	if _, err := PlanCodex(connectFixture(), Options{Home: home}); err == nil || !strings.Contains(err.Error(), "not valid TOML") {
		t.Errorf("invalid TOML must be refused, got %v", err)
	}
	os.WriteFile(cfg, []byte("[otel]\nenvironment = \"x\"\n"), 0o600)
	if _, err := PlanCodex(connectFixture(), Options{Home: home, Telemetry: "http://localhost:4318"}); err == nil {
		t.Error("an existing [otel] table must be refused, not duplicated")
	}
	os.WriteFile(cfg, []byte("[model_providers.aiplex]\nname = \"mine\"\n"), 0o600)
	if _, err := PlanCodex(connectFixture(), Options{Home: home}); err == nil {
		t.Error("a user-owned [model_providers.aiplex] must be refused")
	}
	if _, err := PlanCodex(connectFixture(), Options{Home: home, Scope: "project", Cwd: home}); err == nil {
		t.Error("codex project scope must be refused")
	}
}

func TestCodexTokenHelper(t *testing.T) {
	home := isolate(t)
	p, err := PlanCodex(connectFixture(), Options{Home: home, TokenHelper: true})
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	got := read(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(got, "[model_providers.aiplex.auth]") || !strings.Contains(got, `args = ["auth", "print-token"]`) || strings.Contains(got, "env_key") {
		t.Errorf("helper config wrong:\n%s", got)
	}
}

func TestGeminiCLI(t *testing.T) {
	home := isolate(t)
	p, err := PlanGeminiCLI(connectFixture(), Options{Home: home, Telemetry: "http://localhost:4317"})
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	got := read(t, filepath.Join(home, ".gemini", "settings.json"))
	for _, want := range []string{`"httpUrl": "http://127.0.0.1:18590/ap/local/mcp"`, "Bearer ${AIPLEX_TOKEN}", `"otlpProtocol": "grpc"`, `"logPrompts": false`} {
		if !strings.Contains(got, want) {
			t.Errorf("settings.json lacks %s:\n%s", want, got)
		}
	}
	joined := strings.Join(p.Remaining, "\n")
	if !strings.Contains(joined, "GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:18590/ap/local") {
		t.Errorf("remaining steps should name the base URL variable:\n%s", joined)
	}
	assertNoToken(t, home)
	if _, err := PlanGeminiCLI(connectFixture(), Options{Home: home, TokenHelper: true}); err == nil {
		t.Error("gemini has no credential helper")
	}
}

func TestBackupDirIsPrivate(t *testing.T) {
	home := isolate(t)
	p, _ := PlanClaudeCode(connectFixture(), Options{Home: home})
	dir := apply(t, home, p)
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("backup dir mode: %v %v", err, st)
	}
	st, err = os.Stat(filepath.Join(dir, "manifest.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("manifest mode: %v %v", err, st)
	}
}

func TestUnifiedDiff(t *testing.T) {
	d := UnifiedDiff("f", []byte("a\nb\nc\n"), []byte("a\nB\nc\n"), true)
	want := "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"
	if d != want {
		t.Errorf("diff:\n%s", d)
	}
	if UnifiedDiff("f", []byte("x"), []byte("x"), true) != "" {
		t.Error("identical files must produce no diff")
	}
	if !strings.HasPrefix(UnifiedDiff("f", nil, []byte("x\n"), false), "--- /dev/null\n+++ b/f\n@@ -0,0 +1,1 @@\n+x\n") {
		t.Error("new file diff wrong")
	}
}
