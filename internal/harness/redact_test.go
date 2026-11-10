package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustNotContain(t *testing.T, what, text string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Errorf("%s leaks %q:\n%s", what, s, text)
		}
	}
}

func TestRedactJSONRules(t *testing.T) {
	in := `{"env":{"ANTHROPIC_AUTH_TOKEN":"abc-secret-1","ANTHROPIC_BASE_URL":"http://127.0.0.1:18590/ap/local","ANTHROPIC_MODEL":"mock-claude"},
"apiKeyHelper":"aiplex --context prod auth print-token",
"headers":{"Authorization":"Bearer ${AIPLEX_TOKEN}","X-Auth":"plain-secret-2","author":"Ada"},
"mcp":{"headers":{"Authorization":"Bearer literal-secret-3"}},
"auth":{"command":"aiplex","args":["auth","print-token"]},
"oauthAccount":{"accessToken":"t-4","email":"a@b.c"},
"note":"call with sk-ant-api03-ABCDEFGH12345 please",
"blob":"Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWprbG1ub3A",
"count":3}`
	out, ok := Redact("settings.json", []byte(in))
	if !ok {
		t.Fatal("should parse")
	}
	mustNotContain(t, "redacted JSON", out, "abc-secret-1", "plain-secret-2", "literal-secret-3", "t-4", "sk-ant-api03", "Zm9vYmFy")
	for _, keep := range []string{"http://127.0.0.1:18590/ap/local", "mock-claude", "Bearer ${AIPLEX_TOKEN}", "aiplex --context prod auth print-token", `"author": "Ada"`, `"command": "aiplex"`, `"count": 3`} {
		if !strings.Contains(out, keep) {
			t.Errorf("should keep %q:\n%s", keep, out)
		}
	}
	if strings.Count(out, "\n") < 10 {
		t.Errorf("compact input must be pretty-printed:\n%s", out)
	}
}

func TestRedactTOML(t *testing.T) {
	in := "# note\nname = \"x\"\nexperimental_bearer_token = \"tok-secret-1\"\nsecret_notes = \"\"\"\nline one\nmulti-secret-2\n\"\"\"\n[a]\nheaders = { \"X-Api-Key\" = \"hdr-secret-3\", \"X-Feature\" = \"on\" }\nbearer_token_env_var = \"AIPLEX_TOKEN\"\nenv_key = \"AIPLEX_TOKEN\"\n[a.auth]\ncommand = \"aiplex\"\nargs = [\"--context\", \"prod\"]\n"
	out, ok := Redact("config.toml", []byte(in))
	if !ok {
		t.Fatal("should be shown")
	}
	mustNotContain(t, "redacted TOML", out, "tok-secret-1", "multi-secret-2", "hdr-secret-3")
	for _, keep := range []string{"# note", `name = "x"`, `"X-Feature" = "on"`, `bearer_token_env_var = "AIPLEX_TOKEN"`, `command = "aiplex"`, } {
		if !strings.Contains(out, keep) {
			t.Errorf("should keep %q:\n%s", keep, out)
		}
	}
}

func TestRedactUnparseableFallsBackToLines(t *testing.T) {
	in := "{ // not json\n  \"password\": \"pw-secret-1\",\n  'x' : 'sk-ABCDEFGH1234'\n  token = ghp_ABCDEFGH1234\n"
	out, ok := Redact("f.json", []byte(in))
	if !ok {
		t.Fatal("line fallback should work")
	}
	mustNotContain(t, "fallback", out, "pw-secret-1", "sk-ABCDEFGH1234", "ghp_ABCDEFGH1234")

	long := "{\"token\":\"" + strings.Repeat("a", 5000) + "\" oops"
	if _, ok := Redact("f.json", []byte(long)); ok {
		t.Error("an unparseable one-line file must not be shown")
	}
	c := Change{Path: "/x/f.json", Before: []byte(long), Existed: true, After: []byte(long + "!"), Mode: 0o600}
	d := c.Diff()
	if !strings.Contains(d, "content not shown") || strings.Contains(d, "aaaa") {
		t.Errorf("summary expected:\n%s", d)
	}
	if _, ok := Redact("f.json", []byte{0xff, 0xfe, 0x00}); ok {
		t.Error("non-UTF-8 content must not be shown")
	}
}

func TestChangeOnlyInsideRedactedValueIsStillReported(t *testing.T) {
	c := Change{Path: "/x/settings.json", Existed: true, Mode: 0o600,
		Before: []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"old-secret-value"}}`),
		After:  []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"new-secret-value"}}`)}
	if !c.Changed() {
		t.Fatal("bytes differ")
	}
	d := c.Diff()
	mustNotContain(t, "diff", d, "old-secret-value", "new-secret-value")
	if !strings.Contains(d, "a value changed; content redacted") {
		t.Errorf("should report a redacted change:\n%s", d)
	}
}

func TestWrittenBytesAreTheUnredactedMerge(t *testing.T) {
	home := isolate(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	orig := `{"env":{"ANTHROPIC_AUTH_TOKEN":"keep-this-real-secret"},"apiKey":"another-real-secret"}`
	os.WriteFile(settings, []byte(orig), 0o600)
	p, err := PlanClaudeCode(connectFixture(), Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, "diff", p.Changes[0].Diff(), "keep-this-real-secret", "another-real-secret")
	dir := apply(t, home, p)
	got := read(t, settings)
	if got != string(p.Changes[0].After) {
		t.Error("the file on disk is not the planned merge")
	}
	if !strings.Contains(got, `"ANTHROPIC_AUTH_TOKEN": "keep-this-real-secret"`) || !strings.Contains(got, `"apiKey": "another-real-secret"`) || strings.Contains(got, "[redacted]") {
		t.Errorf("written file must keep the real values:\n%s", got)
	}
	if read(t, filepath.Join(dir, "files", "0.bak")) != orig {
		t.Error("the backup must be the original bytes")
	}
}

func TestTokenHelperUsesTheNamedContext(t *testing.T) {
	home := isolate(t)
	opts := Options{Home: home, TokenHelper: true, HelperContext: "prod"}
	p, err := PlanClaudeCode(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p.Changes[0].After), `"apiKeyHelper": "aiplex --context prod auth print-token"`) {
		t.Errorf("helper should name the context:\n%s", p.Changes[0].After)
	}
	apply(t, home, p)
	if again, _ := PlanClaudeCode(connectFixture(), opts); again.Changes[0].Changed() {
		t.Error("same context must be a no-op")
	}
	other, _ := PlanClaudeCode(connectFixture(), Options{Home: home, TokenHelper: true, HelperContext: "staging"})
	if !other.Changes[0].Changed() || !strings.Contains(other.Changes[0].Diff(), "staging") {
		t.Error("a different context must be a visible diff")
	}

	// Awkward names are quoted for the shell and kept as separate arguments for Codex.
	odd := Options{Home: home, TokenHelper: true, HelperContext: "it's a ctx"}
	cp, err := PlanClaudeCode(connectFixture(), odd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cp.Changes[0].After), `aiplex --context 'it'\\''s a ctx' auth print-token`) {
		t.Errorf("shell quoting wrong:\n%s", cp.Changes[0].After)
	}
	xp, err := PlanCodex(connectFixture(), odd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(xp.Changes[0].After), `args = ["--context", "it's a ctx", "auth", "print-token"]`) {
		t.Errorf("codex args wrong:\n%s", xp.Changes[0].After)
	}
}
