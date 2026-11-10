package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every secret marker below sits in a file the user already has. None may
// reach stdout, stderr or an error message from --dry-run, --write or --undo,
// while the files, the backups and the restore stay byte-correct.

var secretMarkers = []string{
	"MARK-claude-auth-token-0001", "MARK-claude-apikey-0002", "MARK-oauth-access-0003", "MARK-mcp-bearer-0004",
	"MARK-codex-bearer-0005", "MARK-codex-multiline-0006", "MARK-gemini-apikey-0007", "MARK-gemini-bearer-0008",
	"sk-ant-api03-MARKERLITERAL0009abcdef", "ghp_MARKERLITERAL0010abcdefgh", "MARK-compact-0011",
	"MARK-session-0012", "MARK-codex-header-0013",
}

const (
	claudePretty = `{
  "theme": "dark",
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "MARK-claude-auth-token-0001",
    "ANTHROPIC_API_KEY": "MARK-claude-apikey-0002",
    "NOTE": "sk-ant-api03-MARKERLITERAL0009abcdef"
  }
}
`
	claudeCompact = `{"theme":"dark","env":{"ANTHROPIC_AUTH_TOKEN":"MARK-compact-0011"},"sessionKey":"MARK-session-0012"}`

	claudeJSON = `{"numStartups": 3, "oauthAccount": {"accessToken": "MARK-oauth-access-0003", "emailAddress": "dev@example.com"},
 "mcpServers": {"other": {"type": "http", "url": "https://x.example/mcp", "headers": {"Authorization": "Bearer MARK-mcp-bearer-0004"}}}}
`

	codexTOML = `# keep this comment
sandbox_mode = "read-only"
experimental_bearer_token = "MARK-codex-bearer-0005"
notes = """
first line
MARK-codex-multiline-0006
"""
[model_providers.other]
name = "other"
base_url = "https://o.example/v1"
env_http_headers = { "X-Feature" = "FEATURES" }
http_headers = { "X-Api-Key" = "MARK-codex-header-0013" }
`

	geminiJSON = `{"mcpServers":{"mine":{"httpUrl":"https://m.example/mcp","headers":{"Authorization":"Bearer ghp_MARKERLITERAL0010abcdefgh"}}},"apiKey":"MARK-gemini-apikey-0007","auth":{"password":"MARK-gemini-bearer-0008"}}`
)

func connectStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_point_id":"local","client":"generic","base_urls":{"anthropic":"http://x/ap/local","openai":"http://x/ap/local/v1","genai":"http://x/ap/local"},
"models":[{"name":"mock-claude","protocols":["anthropic-messages"]},{"name":"mock-responses","protocols":["openai-responses"]}],"endpoints":[],"mcp_url":"http://x/ap/local/mcp"}`))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err = root.Execute()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return out.String(), errb.String() + msg, err
}

func assertNoMarkers(t *testing.T, what, text string) {
	t.Helper()
	for _, m := range secretMarkers {
		if strings.Contains(text, m) {
			t.Errorf("%s leaks %q:\n%s", what, m, text)
		}
	}
}

func TestConnectDisplayNeverPrintsExistingSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("AIPLEX_URL", connectStub(t))
	t.Setenv("AIPLEX_TOKEN", "live-token-must-not-appear-0000000000")

	cases := []struct {
		client string
		files  map[string]string
	}{
		{"claude-code", map[string]string{".claude/settings.json": claudePretty, ".claude.json": claudeJSON}},
		{"claude-code", map[string]string{".claude/settings.json": claudeCompact, ".claude.json": claudeJSON}},
		{"codex", map[string]string{".codex/config.toml": codexTOML}},
		{"gemini-cli", map[string]string{".gemini/settings.json": geminiJSON}},
	}
	for i, c := range cases {
		t.Run(c.client+"-"+string(rune('a'+i)), func(t *testing.T) {
			os.RemoveAll(filepath.Join(home, ".aiplex"))
			for rel, content := range c.files {
				p := filepath.Join(home, rel)
				os.MkdirAll(filepath.Dir(p), 0o755)
				if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := func() map[string]string {
				m := map[string]string{}
				for rel := range c.files {
					b, _ := os.ReadFile(filepath.Join(home, rel))
					m[rel] = string(b)
				}
				return m
			}

			out, errs, err := runCLI(t, "connect", "local", "--client", c.client, "--write", "--dry-run")
			if err != nil {
				t.Fatalf("dry-run: %v", err)
			}
			assertNoMarkers(t, "dry-run output", out+errs)
			if !strings.Contains(out, "Dry run: nothing was written.") {
				t.Errorf("dry-run output:\n%s", out)
			}
			for rel, content := range c.files {
				if b, _ := os.ReadFile(filepath.Join(home, rel)); string(b) != content {
					t.Fatalf("dry run changed %s", rel)
				}
			}

			out, errs, err = runCLI(t, "connect", "local", "--client", c.client, "--write", "--yes")
			if err != nil {
				t.Fatalf("write: %v\n%s", err, errs)
			}
			assertNoMarkers(t, "write output", out+errs)
			if strings.Contains(out+errs, "live-token-must-not-appear") {
				t.Error("the live token was printed")
			}
			written := snapshot()
			for rel, content := range c.files {
				// What is on disk is the real merge: the user's own values are intact.
				for _, m := range secretMarkers {
					if strings.Contains(content, m) && !strings.Contains(written[rel], m) {
						t.Errorf("%s lost the user's value %q", rel, m)
					}
				}
				if strings.Contains(written[rel], "[redacted]") {
					t.Errorf("the redacted form was written to %s:\n%s", rel, written[rel])
				}
			}
			// Backups and manifest hold the originals, privately; the manifest holds no secret.
			manifests, _ := filepath.Glob(filepath.Join(home, ".aiplex", "connect-backups", "*", "manifest.json"))
			if len(manifests) != 1 {
				t.Fatalf("want one manifest, got %v", manifests)
			}
			mb, _ := os.ReadFile(manifests[0])
			assertNoMarkers(t, "manifest", string(mb))

			// A second run changes nothing and prints nothing sensitive.
			out, errs, err = runCLI(t, "connect", "local", "--client", c.client, "--write", "--yes")
			if err != nil {
				t.Fatal(err)
			}
			assertNoMarkers(t, "rerun output", out+errs)
			if !strings.Contains(out, "Nothing to change.") {
				t.Errorf("rerun should be a no-op:\n%s", out)
			}

			for _, args := range [][]string{{"connect", "--undo", "--client", c.client, "--dry-run"}, {"connect", "--undo", "--client", c.client, "--yes"}} {
				out, errs, err = runCLI(t, args...)
				if err != nil {
					t.Fatalf("%v: %v", args, err)
				}
				assertNoMarkers(t, strings.Join(args, " ")+" output", out+errs)
			}
			for rel, content := range snapshot() {
				if want, ok := c.files[rel]; ok && content != want {
					t.Errorf("%s not restored byte for byte:\n%s", rel, content)
				}
			}
		})
	}
}

func TestConnectErrorsDoNotLeakFileContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("AIPLEX_URL", connectStub(t))
	t.Setenv("AIPLEX_TOKEN", "live-token-must-not-appear-0000000000")
	bad := map[string]string{
		".claude/settings.json": `{"env": {"ANTHROPIC_AUTH_TOKEN": "MARK-claude-auth-token-0001",}`,
		".codex/config.toml":    "experimental_bearer_token = \"MARK-codex-bearer-0005\"\nbroken = \n",
	}
	for rel, content := range bad {
		p := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o600)
	}
	for _, client := range []string{"claude-code", "codex"} {
		out, errs, err := runCLI(t, "connect", "local", "--client", client, "--write", "--yes")
		if err == nil {
			t.Fatalf("%s: an invalid file must be refused", client)
		}
		assertNoMarkers(t, client+" refusal", out+errs)
	}
}

func TestTokenHelperRefusesWithoutAStoredContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AIPLEX_URL", connectStub(t))
	t.Setenv("AIPLEX_TOKEN", "live-token-must-not-appear-0000000000")
	_, errs, err := runCLI(t, "connect", "local", "--client", "claude-code", "--write", "--token-helper", "--yes")
	if err == nil || !strings.Contains(errs, "stored") {
		t.Fatalf("want a refusal naming the stored-context requirement, got %v %q", err, errs)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".claude", "settings.json")); statErr == nil {
		t.Error("nothing may be written when the helper cannot work")
	}
}
