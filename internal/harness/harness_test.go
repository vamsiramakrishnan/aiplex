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

// Gemini CLI 0.67 (observed 2026-11-10) stops a headless run with "Invalid auth
// method selected" unless security.auth.selectedType is set, and asks its own
// default model unless model.name names one the access point serves.
func TestGeminiCLISelectsAPIKeyAuthAndModel(t *testing.T) {
	home := isolate(t)
	c := connectFixture()
	c.Models = append(c.Models, aiplex.ConnectModel{Name: "mock-gemini", Protocols: []string{"google-genai"}})
	p, err := PlanGeminiCLI(c, Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	got := read(t, filepath.Join(home, ".gemini", "settings.json"))
	for _, want := range []string{`"selectedType": "gemini-api-key"`, `"name": "mock-gemini"`} {
		if !strings.Contains(got, want) {
			t.Errorf("settings.json lacks %s:\n%s", want, got)
		}
	}
}

// Claude Code sends ANTHROPIC_API_KEY from settings as x-api-key beside the
// AIPlex token, which the access point refuses; the plan has to say so.
func TestClaudeCodeWarnsAboutExistingAnthropicAPIKey(t *testing.T) {
	home := isolate(t)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"env":{"ANTHROPIC_API_KEY":"placeholder-FAKEFAKEFAKEFAKEFAKEFAKE"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := PlanClaudeCode(connectFixture(), Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Notes, "\n"), "ANTHROPIC_API_KEY") {
		t.Errorf("no warning about the existing key: %v", p.Notes)
	}
	for _, n := range p.Notes {
		if strings.Contains(n, "FAKEFAKE") {
			t.Errorf("note leaks the key: %s", n)
		}
	}
}

// Antigravity CLI 1.5.4 (Linux, 2026-11-10): `agy mcp add` writes
// ~/.gemini/config/mcp_config.json, and it sent "Bearer ${AIPLEX_TOKEN}" to an
// MCP server literally, so the file may only hold the secret-free stdio entry.
func TestAntigravityWritesOnlyTheStdioServerAndKeepsOtherServers(t *testing.T) {
	home := isolate(t)
	path := filepath.Join(home, ".gemini", "config", "mcp_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"serverUrl":"https://example.com/mcp","headers":{"Authorization":"Bearer FAKEBEARER0123456789abcdef"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := PlanAntigravity(connectFixture(), Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if d := p.Changes[0].Diff(); strings.Contains(d, "FAKEBEARER") {
		t.Errorf("diff shows an existing secret:\n%s", d)
	}
	apply(t, home, p)
	got := read(t, path)
	for _, want := range []string{`"serverUrl": "https://example.com/mcp"`, `"command": "aiplex"`, `"serve"`, "FAKEBEARER0123456789abcdef"} {
		if !strings.Contains(got, want) {
			t.Errorf("mcp_config.json lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "${AIPLEX_TOKEN}") || strings.Contains(got, "serverUrl\": \"http://127.0.0.1") {
		t.Errorf("the access point endpoint must not be written:\n%s", got)
	}
	p2, _ := PlanAntigravity(connectFixture(), Options{Home: home})
	if p2.Changes[0].Diff() != "" {
		t.Error("second run is not a no-op")
	}
	if _, err := PlanAntigravity(connectFixture(), Options{Home: home, TokenHelper: true}); err == nil {
		t.Error("--token-helper should be refused")
	}
	assertNoToken(t, home)
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

// ---- branchyard ----

func byOpts(home string) Options { return Options{Home: home, Cwd: home, Scope: "project"} }

func byRoutes(t *testing.T, text string) []map[string]any {
	t.Helper()
	m, err := parseTOML(text)
	if err != nil {
		t.Fatalf("not valid TOML: %v\n%s", err, text)
	}
	var out []map[string]any
	models, _ := m["models"].(map[string]any)
	l, _ := models["routes"].([]any)
	for _, r := range l {
		out = append(out, r.(map[string]any))
	}
	return out
}

func byBackends(t *testing.T, text string) map[string]any {
	t.Helper()
	m, _ := parseTOML(text)
	models, _ := m["models"].(map[string]any)
	b, _ := models["backends"].(map[string]any)
	return b
}

func TestBranchyardFreshFileIdempotentAndNoToken(t *testing.T) {
	home := isolate(t)
	cfg := filepath.Join(home, "branchyard.toml")
	opts := byOpts(home)
	opts.File = cfg
	p, err := PlanBranchyard(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	got := read(t, cfg)
	m, _ := parseTOML(got)
	if s, _ := m["secrets"].(map[string]any); s["aiplex"] != "AIPLEX_TOKEN" {
		t.Errorf("secrets: %v", m["secrets"])
	}
	b := byBackends(t, got)
	if len(b) != 2 {
		t.Fatalf("backends: %v", b)
	}
	for name, api := range map[string]string{"aiplex-local-anthropic": "anthropic", "aiplex-local-openai": "openai"} {
		e, _ := b[name].(map[string]any)
		if e["api"] != api || e["url"] != "http://127.0.0.1:18590/ap/local" || e["key"] != "aiplex" || e["control_plane"] != "aiplex" || len(e) != 4 {
			t.Errorf("backend %s = %v", name, e)
		}
	}
	want := map[string]string{"mock-claude": "aiplex-local-anthropic", "mock-gpt": "aiplex-local-openai", "mock-responses": "aiplex-local-openai"}
	rs := byRoutes(t, got)
	if len(rs) != 3 {
		t.Fatalf("routes: %v", rs)
	}
	for _, r := range rs {
		bs := strs(r["backends"])
		if len(bs) != 1 || want[r["model"].(string)] != bs[0] || len(r) != 2 {
			t.Errorf("route %v", r)
		}
	}
	p2, err := PlanBranchyard(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Changes[0].Changed() {
		t.Errorf("second run would change the file:\n%s", p2.Changes[0].Diff())
	}
	for _, r := range append(append([]string{}, p.Remaining...), p.Notes...) {
		if strings.Contains(r, planted) {
			t.Errorf("token in output: %s", r)
		}
	}
	steps := strings.Join(p.Remaining, "\n")
	if !strings.Contains(steps, `export AIPLEX_TOKEN="$(aiplex auth print-token)"`) || !strings.Contains(steps, "expires") {
		t.Errorf("remaining steps: %v", p.Remaining)
	}
	assertNoToken(t, home)
	if strings.Contains(p.Changes[0].Diff(), planted) {
		t.Error("token in diff")
	}
	d, mf, _ := LatestBackup(BackupRoot(home), "branchyard")
	ch, _ := PlanUndo(d, mf)
	if err := ApplyUndo(d, mf, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Error("undo should remove a file that did not exist")
	}
}

func TestBranchyardMissingDefaultFilePrintsInstead(t *testing.T) {
	home := isolate(t)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 0 || !strings.Contains(p.Print, "[models.backends.aiplex-local-anthropic]") || !strings.Contains(strings.Join(p.Notes, "\n"), "nothing was written") {
		t.Errorf("plan: changes=%d print=%q notes=%v", len(p.Changes), p.Print, p.Notes)
	}
	if _, err := os.Stat(filepath.Join(home, "branchyard.toml")); !os.IsNotExist(err) {
		t.Error("the default file must not be created")
	}
}

const byExisting = `# my yard
version = 1

[defaults]
harness = "claude-code"   # keep

[secrets]
# provider key
ANTHROPIC_API_KEY = "ANTHROPIC_API_KEY"

# models
[models]
allow = ["claude-*"]   # gateway on
seed = 7

[models.backends.anthropic]
api = "anthropic"
key = "ANTHROPIC_API_KEY"

# my route
[[models.routes]]
model = "claude-sonnet-*"
backends = ["anthropic"]

# between tables
[[models.routes]]
model = "o3"
backends = ["anthropic"]
weights = [2]

[usage]
note = "kept"
`

func TestBranchyardMergeKeepsCommentsAndOtherTables(t *testing.T) {
	home := isolate(t)
	cfg := filepath.Join(home, "branchyard.toml")
	os.WriteFile(cfg, []byte(byExisting), 0o640)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	got := read(t, cfg)
	// Everything the person wrote is still there, line for line and in order,
	// except the one line inserted into [secrets].
	rest := strings.Replace(got, "aiplex = \"AIPLEX_TOKEN\"\n", "", 1)
	if !strings.HasPrefix(rest, byExisting) {
		t.Errorf("the original is not a prefix of the result:\n%s", got)
	}
	if st, _ := os.Stat(cfg); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	m, _ := parseTOML(got)
	if s := m["secrets"].(map[string]any); s["aiplex"] != "AIPLEX_TOKEN" || s["ANTHROPIC_API_KEY"] != "ANTHROPIC_API_KEY" {
		t.Errorf("secrets %v", s)
	}
	models := m["models"].(map[string]any)
	if models["seed"] != int64(7) || len(models["allow"].([]any)) != 1 {
		t.Errorf("models keys lost: %v", models)
	}
	if rs := byRoutes(t, got); len(rs) != 5 || rs[0]["model"] != "claude-sonnet-*" || rs[1]["model"] != "o3" || rs[2]["model"] != "mock-gpt" {
		t.Errorf("routes: %v", rs)
	}
	if len(byBackends(t, got)) != 3 {
		t.Errorf("backends: %v", byBackends(t, got))
	}
	p2, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	if p2.Changes[0].Changed() {
		t.Errorf("second run would change the file:\n%s", p2.Changes[0].Diff())
	}
	d, mf, _ := LatestBackup(BackupRoot(home), "branchyard")
	ch, _ := PlanUndo(d, mf)
	ApplyUndo(d, mf, ch)
	if read(t, cfg) != byExisting {
		t.Error("undo did not restore the original")
	}
}

func TestBranchyardReplacesSameNameWithDifferentValues(t *testing.T) {
	home := isolate(t)
	cfg := filepath.Join(home, "branchyard.toml")
	orig := "# top\n[secrets]\naiplex = \"AIPLEX_TOKEN\"   # mine\nother = \"X\"\n\n# b\n[models.backends.aiplex-local-anthropic]\napi = \"anthropic\"\nurl = \"https://old.example/ap/local\"\nkey = \"aiplex\"\ncontrol_plane = \"aiplex\"\n\n# r\n[models.backends.elsewhere]\napi = \"anthropic\"\nurl = \"https://e/ap/x\"\nkey = \"aiplex\"\ncontrol_plane = \"aiplex\"\n\n[[models.routes]]\nmodel = \"mock-claude\"\nbackends = [\"elsewhere\"]\nrequests_per_minute = 5\n\n[usage]\nx = 1\n"
	os.WriteFile(cfg, []byte(orig), 0o600)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	d := p.Changes[0].Diff()
	for _, w := range []string{"url", "requests_per_minute"} {
		if !strings.Contains(d, w) {
			t.Errorf("diff does not show the replaced %s:\n%s", w, d)
		}
	}
	notes := strings.Join(p.Notes, "\n")
	for _, w := range []string{"[models.backends.aiplex-local-anthropic]", "models.routes[0]"} {
		if !strings.Contains(notes, w) {
			t.Errorf("notes do not name %s: %v", w, p.Notes)
		}
	}
	apply(t, home, p)
	got := read(t, cfg)
	for _, w := range []string{"# top", "# b", "# r", "other = \"X\"", "[usage]\nx = 1"} {
		if !strings.Contains(got, w) {
			t.Errorf("lost %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "old.example") || strings.Contains(got, "requests_per_minute") {
		t.Errorf("old values remain:\n%s", got)
	}
	if rs := byRoutes(t, got); len(rs) != 3 || rs[0]["backends"].([]any)[0] != "aiplex-local-anthropic" {
		t.Errorf("routes: %v", rs)
	}
	p2, _ := PlanBranchyard(connectFixture(), byOpts(home))
	if p2.Changes[0].Changed() {
		t.Errorf("second run would change the file:\n%s", p2.Changes[0].Diff())
	}
}

func TestBranchyardRefusesToMixAndNamesTheRoute(t *testing.T) {
	home := isolate(t)
	cfg := filepath.Join(home, "branchyard.toml")
	cases := map[string]struct{ file, want string }{
		"same glob, plain backend":    {"[models.backends.anthropic]\napi = \"anthropic\"\n\n[[models.routes]]\nmodel = \"x\"\nbackends = [\"anthropic\"]\n\n[[models.routes]]\nmodel = \"mock-claude\"\nbackends = [\"anthropic\"]\n", "models.routes[1]"},
		"broader glob, plain backend": {"[models.backends.anthropic]\napi = \"anthropic\"\n\n[[models.routes]]\nmodel = \"mock-*\"\nbackends = [\"anthropic\"]\n", "models.routes[0]"},
		"already mixed":               {"[models.backends.anthropic]\napi = \"anthropic\"\n\n[models.backends.mine]\napi = \"anthropic\"\ncontrol_plane = \"aiplex\"\nurl = \"u\"\n\n[[models.routes]]\nmodel = \"other\"\nbackends = [\"mine\"]\nfallbacks = [\"anthropic\"]\n", "models.routes[0]"},
		"plain backend with our name": {"[models.backends.aiplex-local-anthropic]\napi = \"anthropic\"\n\n[models.backends.p]\napi = \"anthropic\"\n\n[[models.routes]]\nmodel = \"z\"\nbackends = [\"aiplex-local-anthropic\", \"p\"]\n", "models.routes[0]"},
	}
	for name, c := range cases {
		os.WriteFile(cfg, []byte(c.file), 0o600)
		_, err := PlanBranchyard(connectFixture(), byOpts(home))
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "Nothing was changed") {
			t.Errorf("%s: want a refusal naming %s, got %v", name, c.want, err)
		}
		if read(t, cfg) != c.file {
			t.Errorf("%s: file changed", name)
		}
	}
	// A plain route for a model the access point does not offer is fine.
	os.WriteFile(cfg, []byte("[models.backends.anthropic]\napi = \"anthropic\"\n\n[[models.routes]]\nmodel = \"claude-*\"\nbackends = [\"anthropic\"]\n"), 0o600)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range byRoutes(t, string(p.Changes[0].After)) {
		for _, b := range strs(r["backends"]) {
			if strings.HasPrefix(b, "aiplex-") != strings.HasPrefix(r["model"].(string), "mock-") {
				t.Errorf("mixed route written: %v", r)
			}
		}
	}
}

func TestBranchyardRefusalsLeaveTheFileAlone(t *testing.T) {
	home := isolate(t)
	cfg := filepath.Join(home, "branchyard.toml")
	for name, text := range map[string]string{
		"invalid TOML":       "models = \n",
		"inline routes":      "[models]\nroutes = [ { model = \"a\", backends = [\"b\"] } ]\n",
		"inline backends":    "[models]\nbackends = { a = { api = \"anthropic\" } }\n",
		"inline secrets":     "secrets = { a = \"B\" }\n",
		"models not a table": "models = 3\n",
	} {
		os.WriteFile(cfg, []byte(text), 0o600)
		if _, err := PlanBranchyard(connectFixture(), byOpts(home)); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	o := byOpts(home)
	o.Scope = "user"
	if _, err := PlanBranchyard(connectFixture(), o); err == nil {
		t.Error("user scope must be refused")
	}
	for _, o := range []Options{{Model: "m"}, {TokenHelper: true}, {Telemetry: "http://x"}} {
		o.Home, o.Cwd, o.Scope = home, home, "project"
		if _, err := PlanBranchyard(connectFixture(), o); err == nil {
			t.Errorf("%+v must be refused", o)
		}
	}
}

func TestBranchyardUnsupportedWiresAreListedNotAnError(t *testing.T) {
	home := isolate(t)
	c := connectFixture()
	c.Models = append(c.Models, aiplex.ConnectModel{Name: "gem", Protocols: []string{"google-genai"}}, aiplex.ConnectModel{Name: "br", Protocols: []string{"bedrock-converse", "anthropic-messages"}})
	p, err := BranchyardFragment(c, Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(p.Notes, "\n")
	if !strings.Contains(notes, "not written: google-genai (models: gem): Branchyard's gateway speaks Anthropic's and OpenAI's APIs") || !strings.Contains(notes, "not written: bedrock-converse (models: br)") {
		t.Errorf("notes: %v", p.Notes)
	}
	if strings.Contains(p.Print, `"gem"`) || !strings.Contains(p.Print, `model = "br"`) {
		t.Errorf("fragment:\n%s", p.Print)
	}
	// Only unsupported wires: nothing to write.
	c.Models = []aiplex.ConnectModel{{Name: "gem", Protocols: []string{"google-genai"}}}
	if _, err := BranchyardFragment(c, Options{Home: home}); err == nil || !strings.Contains(err.Error(), "not written: google-genai") {
		t.Errorf("want an error naming the skipped wire, got %v", err)
	}
	// One wire only: one backend.
	c.Models = []aiplex.ConnectModel{{Name: "mock-claude", Protocols: []string{"anthropic-messages"}}}
	p, _ = BranchyardFragment(c, Options{Home: home})
	if strings.Contains(p.Print, "-openai") {
		t.Errorf("an openai backend was written without a model:\n%s", p.Print)
	}
}

func TestBranchyardURLFromOpenAIBaseAndFragmentParses(t *testing.T) {
	c := connectFixture()
	delete(c.BaseURLs, "anthropic")
	p, err := BranchyardFragment(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Print, `url = "http://127.0.0.1:18590/ap/local"`) || strings.Contains(p.Print, "/v1") {
		t.Errorf("url must be the access point base, without /v1:\n%s", p.Print)
	}
	if _, err := parseTOML(p.Print); err != nil {
		t.Errorf("fragment is not valid TOML: %v", err)
	}
	c.BaseURLs = map[string]string{"anthropic": "http://x/other"}
	if _, err := BranchyardFragment(c, Options{}); err == nil {
		t.Error("a base URL not ending in /ap/<id> must be refused")
	}
}

// ---- branchyard, round 1 fixes ----

func byPlanFile(t *testing.T, text string) (home, cfg string) {
	t.Helper()
	home = isolate(t)
	cfg = filepath.Join(home, "branchyard.toml")
	if err := os.WriteFile(cfg, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, cfg
}

// Item 1: Branchyard's matcher knows ? as exactly one character.
func TestBranchyardGlobMirrorsBranchyard(t *testing.T) {
	for _, c := range []struct {
		g, s string
		want bool
	}{
		{"mock-?laude", "mock-claude", true}, {"mock-?laude", "mock-laude", false}, {"?", "", false},
		{"*", "", true}, {"a*b*c", "aXXbYYc", true}, {"a*b*c", "aXXbYY", false}, {"mock-*", "mock-claude", true},
		{"claude-*", "mock-claude", false}, {"**", "x", true}, {"a.b", "a.b", true}, {"a.b", "axb", false}, {"", "", true},
	} {
		if got := globMatch(c.g, c.s); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.g, c.s, got, c.want)
		}
	}
	file := "[models.backends.direct]\napi = \"anthropic\"\n\n[[models.routes]]\nmodel = \"mock-?laude\"\nbackends = [\"direct\"]\n"
	home, cfg := byPlanFile(t, file)
	_, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err == nil || !strings.Contains(err.Error(), "models.routes[0]") || !strings.Contains(err.Error(), "mock-?laude") {
		t.Errorf("want a refusal naming the route, got %v", err)
	}
	if read(t, cfg) != file {
		t.Error("file changed")
	}
}

// Item 2: an existing [secrets] aiplex reference is used as it is.
func TestBranchyardKeepsExistingSecretReference(t *testing.T) {
	file := "[secrets]\naiplex = \"@/run/secrets/aiplex\"   # mine\n"
	home, cfg := byPlanFile(t, file)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	after := string(p.Changes[0].After)
	if !strings.HasPrefix(after, file) || strings.Contains(after, "AIPLEX_TOKEN") {
		t.Errorf("the secrets line must stay byte for byte:\n%s", after)
	}
	pre := strings.Join(p.Preamble, "\n")
	if !strings.Contains(pre, "@/run/secrets/aiplex") {
		t.Errorf("a note before the diff must name the existing reference: %v", p.Preamble)
	}
	steps := strings.Join(p.Remaining, "\n")
	if !strings.Contains(steps, "put the token in that file") || !strings.Contains(steps, "/run/secrets/aiplex") || strings.Contains(steps, "export AIPLEX_TOKEN") {
		t.Errorf("remaining steps must name the reference: %v", p.Remaining)
	}
	// A variable reference.
	home, _ = byPlanFile(t, "[secrets]\naiplex = \"MY_AIPLEX\"\n")
	p, err = PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	if steps := strings.Join(p.Remaining, "\n"); !strings.Contains(steps, `export MY_AIPLEX="$(aiplex auth print-token)"`) {
		t.Errorf("remaining steps: %v", p.Remaining)
	}
	// Not a string: refused, file untouched.
	bad := "[secrets]\naiplex = 3\n"
	home, cfg = byPlanFile(t, bad)
	if _, err := PlanBranchyard(connectFixture(), byOpts(home)); err == nil {
		t.Error("a secrets.aiplex that is not a string must be refused")
	}
	if read(t, cfg) != bad {
		t.Error("file changed")
	}
}

// Item 3: a header directly after another table's header, no blank line.
func TestBranchyardAdjacentEditsMerge(t *testing.T) {
	file := "[secrets]\n[models.backends.aiplex-local-anthropic]\napi = \"anthropic\"\nurl = \"https://old.example/ap/local\"\nkey = \"aiplex\"\ncontrol_plane = \"aiplex\"\n"
	home, cfg := byPlanFile(t, file)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	got := read(t, cfg)
	if b := byBackends(t, got)["aiplex-local-anthropic"].(map[string]any); b["url"] != "http://127.0.0.1:18590/ap/local" {
		t.Errorf("backend not replaced: %v", b)
	}
	if m, _ := parseTOML(got); m["secrets"].(map[string]any)["aiplex"] != "AIPLEX_TOKEN" {
		t.Errorf("secrets: %v", m["secrets"])
	}
}

// Item 4: what is already right is left alone, with its comments and extra keys.
func TestBranchyardLeavesRightEntriesAlone(t *testing.T) {
	file := "[secrets]\naiplex = \"AIPLEX_TOKEN\"\n\n# mine\n[models.backends.aiplex-local-anthropic]\napi = \"anthropic\"   # c1\nurl = \"http://127.0.0.1:18590/ap/local\"\nkey = \"aiplex\"\ncontrol_plane = \"aiplex\"\ncompat = \"none\"\n\n# rt\n[[models.routes]]\nmodel = \"mock-claude\"   # c2\nbackends = [\"aiplex-local-anthropic\"]\nrequests_per_minute = 5\n"
	home, _ := byPlanFile(t, file)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	after := string(p.Changes[0].After)
	if !strings.HasPrefix(after, file) {
		t.Errorf("the right entries must stay byte for byte:\n%s", after)
	}
	if strings.Count(after, "model = \"mock-claude\"") != 1 || strings.Count(after, "[models.backends.aiplex-local-anthropic]") != 1 {
		t.Errorf("duplicated:\n%s", after)
	}
	if len(byRoutes(t, after)) != 3 {
		t.Errorf("routes: %v", byRoutes(t, after))
	}
}

// Item 5: inserted lines use the file's line ending.
func TestBranchyardKeepsLineEndings(t *testing.T) {
	file := "# yard\r\n[secrets]\r\nx = \"Y\"\r\n\r\n[models.backends.aiplex-local-anthropic]\r\napi = \"anthropic\"\r\nurl = \"old\"\r\n"
	home, cfg := byPlanFile(t, file)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, p)
	got := read(t, cfg)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("a bare LF was written into a CRLF file:\n%q", got)
	}
	if _, err := parseTOML(got); err != nil {
		t.Fatal(err)
	}
	p2, _ := PlanBranchyard(connectFixture(), byOpts(home))
	if p2.Changes[0].Changed() {
		t.Error("second run would change the file")
	}
	// An LF file stays LF.
	home, cfg = byPlanFile(t, "# yard\n")
	p, _ = PlanBranchyard(connectFixture(), byOpts(home))
	if strings.Contains(string(p.Changes[0].After), "\r") {
		t.Error("CR written into an LF file")
	}
}

// Item 6 and the "not accepted" rule: an earlier AIPlex route that shadows a new one.
func TestBranchyardNotesAnEarlierAIPlexRouteThatShadows(t *testing.T) {
	file := "[models.backends.other]\napi = \"anthropic\"\nurl = \"https://x/ap/other\"\nkey = \"aiplex\"\ncontrol_plane = \"aiplex\"\n\n[models.backends.otheropenai]\napi = \"openai\"\nurl = \"https://x/ap/other\"\nkey = \"aiplex\"\ncontrol_plane = \"aiplex\"\n\n[[models.routes]]\nmodel = \"mock-*\"\nbackends = [\"other\"]\n\n[[models.routes]]\nmodel = \"mock-gpt\"\nbackends = [\"otheropenai\"]\n"
	home, _ := byPlanFile(t, file)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatalf("an AIPlex-only shadow must not be refused: %v", err)
	}
	notes := strings.Join(p.Notes, "\n")
	// mock-claude (anthropic) is shadowed by "mock-*" -> anthropic backend.
	if !strings.Contains(notes, "models.routes[0]") || !strings.Contains(notes, "mock-claude") || !strings.Contains(notes, "will not take effect") {
		t.Errorf("want a note naming the earlier route: %v", p.Notes)
	}
	// mock-responses (openai) is shadowed only by routes[1]? No: "mock-gpt" does not match it;
	// "mock-*" names an anthropic backend, which a request on the OpenAI API skips.
	if strings.Contains(notes, "for \"mock-responses\"") {
		t.Errorf("a route for the other API does not shadow: %v", p.Notes)
	}
	// An earlier route to a backend of the other API with backend_model does.
	file2 := strings.Replace(file, "backends = [\"other\"]\n", "backends = [\"other\"]\nbackend_model = \"m\"\n", 1)
	home, _ = byPlanFile(t, file2)
	p, _ = PlanBranchyard(connectFixture(), byOpts(home))
	if !strings.Contains(strings.Join(p.Notes, "\n"), "for \"mock-responses\"") {
		t.Errorf("backend_model makes the earlier route apply to every API: %v", p.Notes)
	}
}

// Item 7: the refusal says what to do.
func TestBranchyardShadowRefusalGivesTheWayForward(t *testing.T) {
	file := "[models.backends.direct]\napi = \"anthropic\"\n\n[[models.routes]]\nmodel = \"*\"\nbackends = [\"direct\"]\n"
	home, _ := byPlanFile(t, file)
	_, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err == nil {
		t.Fatal("must be refused")
	}
	for _, w := range []string{`"*"`, `["direct"]`, "move that route below", "without --write", "above it"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("message lacks %q: %v", w, err)
		}
	}
}

// Item 8: dotted keys are merged when they do not collide, refused when they do.
func TestBranchyardDottedKeys(t *testing.T) {
	file := "version = 1\nmodels.backends.mine.api = \"openai\"\nmodels.backends.mine.url = \"https://u\"\n"
	home, cfg := byPlanFile(t, file)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatalf("dotted keys that do not collide are merged: %v", err)
	}
	apply(t, home, p)
	got := read(t, cfg)
	if !strings.HasPrefix(got, file) {
		t.Errorf("the dotted keys must be kept:\n%s", got)
	}
	if b := byBackends(t, got); len(b) != 3 || b["mine"].(map[string]any)["url"] != "https://u" {
		t.Errorf("backends: %v", b)
	}
	// The same name defined by dotted keys cannot be edited: refused.
	clash := "models.backends.aiplex-local-anthropic.api = \"anthropic\"\n"
	home, cfg = byPlanFile(t, clash)
	if _, err := PlanBranchyard(connectFixture(), byOpts(home)); err == nil {
		t.Error("a backend of ours defined by dotted keys must be refused")
	}
	if read(t, cfg) != clash {
		t.Error("file changed")
	}
}

// Item 14f: the two rules about calls no route matches do not affect what is written:
// every model the access point lists gets a route.
func TestBranchyardWritesARouteForEveryModel(t *testing.T) {
	c := connectFixture()
	p, err := BranchyardFragment(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range c.Models {
		if !strings.Contains(p.Print, "model = \""+m.Name+"\"") {
			t.Errorf("no route for %s", m.Name)
		}
	}
	if strings.Contains(strings.Join(p.Notes, "\n"), "goes to every backend") {
		t.Error("the note about unrouted models is out of date")
	}
}

// ---- branchyard, round 2 fixes ----

// Round 2 item 2: CRLF file with no final newline whose last table is replaced.
func TestBranchyardCRLFWithoutFinalNewline(t *testing.T) {
	file := "[models.backends.aiplex-local-anthropic]\r\napi = \"anthropic\"\r\nurl = \"old\""
	home, cfg := byPlanFile(t, file)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatalf("must be merged: %v", err)
	}
	apply(t, home, p)
	got := read(t, cfg)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\r") || strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("lone CR or LF in the result:\n%q", got)
	}
	if _, err := parseTOML(got); err != nil {
		t.Fatal(err)
	}
	if b := byBackends(t, got)["aiplex-local-anthropic"].(map[string]any); b["url"] != "http://127.0.0.1:18590/ap/local" {
		t.Errorf("not replaced: %v", b)
	}
	p2, _ := PlanBranchyard(connectFixture(), byOpts(home))
	if p2.Changes[0].Changed() {
		t.Error("second run would change the file")
	}
}

const byTwoAIPlex = "[models.backends.elsewhere]\napi = \"anthropic\"\nurl = \"https://e/ap/x\"\nkey = \"aiplex\"\ncontrol_plane = \"aiplex\"\n\n[models.backends.plain]\napi = \"anthropic\"\n\n"

// Round 2 item 3: a route that already includes our backend keeps its failover.
func TestBranchyardKeepsARouteThatIncludesOurBackend(t *testing.T) {
	file := byTwoAIPlex + "[[models.routes]]\nmodel = \"mock-claude\"\nbackends = [\"aiplex-local-anthropic\"]\nfallbacks = [\"elsewhere\"]\n"
	home, _ := byPlanFile(t, file)
	p, err := PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	after := string(p.Changes[0].After)
	if !strings.HasPrefix(after, file) {
		t.Errorf("the route must stay byte for byte:\n%s", after)
	}
	if strings.Count(after, "model = \"mock-claude\"") != 1 || strings.Contains(strings.Join(p.Notes, "\n"), "replaced your models.routes") {
		t.Errorf("route rewritten or duplicated:\n%s\n%v", after, p.Notes)
	}
	// Ours plus a plain or undefined backend is the mixed-route refusal.
	for name, names := range map[string]string{"plain": `["aiplex-local-anthropic", "plain"]`, "undefined": `["aiplex-local-anthropic", "nowhere"]`} {
		bad := byTwoAIPlex + "[[models.routes]]\nmodel = \"mock-claude\"\nbackends = " + names + "\n"
		home, cfg := byPlanFile(t, bad)
		if _, err := PlanBranchyard(connectFixture(), byOpts(home)); err == nil || !strings.Contains(err.Error(), "models.routes[0]") {
			t.Errorf("%s: want the mixed-route refusal, got %v", name, err)
		}
		if read(t, cfg) != bad {
			t.Errorf("%s: file changed", name)
		}
	}
	// A route for the same model that names other AIPlex backends but not ours is replaced.
	other := byTwoAIPlex + "[[models.routes]]\nmodel = \"mock-claude\"\nbackends = [\"elsewhere\"]\n"
	home, _ = byPlanFile(t, other)
	p, err = PlanBranchyard(connectFixture(), byOpts(home))
	if err != nil {
		t.Fatal(err)
	}
	rs := byRoutes(t, string(p.Changes[0].After))
	if len(rs) != 3 || strs(rs[0]["backends"])[0] != "aiplex-local-anthropic" || !strings.Contains(strings.Join(p.Notes, "\n"), "replaced your models.routes[0]") {
		t.Errorf("routes %v, notes %v", rs, p.Notes)
	}
}
