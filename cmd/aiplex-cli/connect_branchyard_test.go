package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const byPlanted = "tok-PLANTED-branchyard-0123456789"

// byStub is an access point `local` with an Anthropic and an OpenAI model. The
// planted token sits where a token could plausibly leak from a response.
func byStub(t *testing.T) (url string, requests *atomic.Int64) {
	t.Helper()
	requests = &atomic.Int64{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_point_id":"local","client":"generic","base_urls":{"anthropic":"https://aiplex.example.com/ap/local","openai":"https://aiplex.example.com/ap/local/v1"},
"models":[{"name":"mock-claude","protocols":["anthropic-messages"]},{"name":"mock-gpt","protocols":["openai-chat-completions"]},{"name":"mock-responses","protocols":["openai-responses"]}],
"endpoints":[],"auth":{"scheme":"bearer","header":"Authorization: Bearer ` + byPlanted + `","token_env":"AIPLEX_TOKEN","steps":["export AIPLEX_TOKEN=` + byPlanted + `"]},
"snippets":[{"title":"x","body":"` + byPlanted + `"}],"notes":["` + byPlanted + `"]}`))
	}))
	t.Cleanup(ts.Close)
	return ts.URL, requests
}

func byEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AIPLEX_TOKEN", byPlanted)
	return home
}

// Item 12: the print path.
func TestConnectBranchyardPrintsTheTables(t *testing.T) {
	byEnv(t)
	url, _ := byStub(t)
	t.Setenv("AIPLEX_URL", url)
	out, errs, err := runCLI(t, "connect", "local", "--client", "branchyard")
	if err != nil {
		t.Fatalf("%v\n%s", err, errs)
	}
	for _, want := range []string{
		"[secrets]\naiplex = \"AIPLEX_TOKEN\"\n",
		"[models.backends.aiplex-local-anthropic]\napi = \"anthropic\"\nurl = \"https://aiplex.example.com/ap/local\"\nkey = \"aiplex\"\ncontrol_plane = \"aiplex\"\n",
		"[models.backends.aiplex-local-openai]\napi = \"openai\"\nurl = \"https://aiplex.example.com/ap/local\"\nkey = \"aiplex\"\ncontrol_plane = \"aiplex\"\n",
		"[[models.routes]]\nmodel = \"mock-claude\"\nbackends = [\"aiplex-local-anthropic\"]\n",
		"[[models.routes]]\nmodel = \"mock-gpt\"\nbackends = [\"aiplex-local-openai\"]\n",
		"[[models.routes]]\nmodel = \"mock-responses\"\nbackends = [\"aiplex-local-openai\"]\n",
		`export AIPLEX_TOKEN="$(aiplex auth print-token)"`,
		"branchyard.toml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, byPlanted) || strings.Contains(errs, byPlanted) {
		t.Errorf("the token is in the output:\n%s", out)
	}
	// Item 10: the hint names the file and what --write does to it.
	if !strings.Contains(out, "aiplex connect local --client branchyard --write") || strings.Contains(out, "client's own config") {
		t.Errorf("hint:\n%s", out)
	}
}

// Item 9: flags this client refuses are usage errors, before any request.
func TestConnectBranchyardRefusedFlagsAreUsageErrorsBeforeTheServer(t *testing.T) {
	home := byEnv(t)
	url, requests := byStub(t)
	t.Setenv("AIPLEX_URL", url)
	yard := filepath.Join(home, "branchyard.toml")
	os.WriteFile(yard, []byte("# yard\n"), 0o600)
	for _, flags := range [][]string{{"--model", "m"}, {"--token-helper"}, {"--with-telemetry", "http://localhost:4318"}} {
		args := append([]string{"connect", "local", "--client", "branchyard", "--write", "--dry-run", "--file", yard}, flags...)
		_, _, err := runCLI(t, args...)
		if err == nil || exitCodeOf(err) != 2 {
			t.Errorf("%v: want exit 2, got %v (code %d)", flags, err, exitCodeOf(err))
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d request(s) reached the server before the flags were refused", n)
	}
}

// Item 2 at the command: the note about an existing reference comes before the
// diff, and the diff does not hide it.
func TestConnectBranchyardNamesAnExistingSecretReferenceBeforeTheDiff(t *testing.T) {
	home := byEnv(t)
	url, _ := byStub(t)
	t.Setenv("AIPLEX_URL", url)
	yard := filepath.Join(home, "branchyard.toml")
	os.WriteFile(yard, []byte("[secrets]\naiplex = \"@/run/secrets/aiplex\"\n"), 0o600)
	out, errs, err := runCLI(t, "connect", "local", "--client", "branchyard", "--write", "--dry-run", "--file", yard)
	if err != nil {
		t.Fatalf("%v\n%s", err, errs)
	}
	note, diff := strings.Index(out, "@/run/secrets/aiplex"), strings.Index(out, "--- a/")
	if note < 0 || diff < 0 || note > diff {
		t.Errorf("the note must name the reference before the diff:\n%s", out)
	}
	if strings.Contains(out, "AIPLEX_TOKEN\"") && !strings.Contains(out, "put the token in that file") {
		t.Errorf("remaining step:\n%s", out)
	}
}

// Round 2 item 1: an existing [secrets] aiplex value is printed only when it is
// a reference that cannot be a pasted token.
func TestConnectBranchyardNeverPrintsAnUnsafeSecretValue(t *testing.T) {
	home := byEnv(t)
	url, _ := byStub(t)
	t.Setenv("AIPLEX_URL", url)
	yard := filepath.Join(home, "branchyard.toml")
	type tc struct {
		name, line, value string
		refuse            bool
	}
	for _, c := range []tc{
		{"hyphenated token", `aiplex = "sk-live-abc123def456ghi789"`, "sk-live-abc123def456ghi789", true},
		{"token shaped like a name", `aiplex = "sk_live_abc123def456ghi789"`, "sk_live_abc123def456ghi789", false},
		{"shell text", `aiplex = "A; rm -rf ~"`, "rm -rf", true},
		{"empty", `aiplex = ""`, "", true},
		{"multi-line", "aiplex = \"\"\"\nline-one-secret\nline-two\"\"\"", "line-one-secret", true},
		{"bare @", `aiplex = "@"`, "", true},
		{"path", `aiplex = "@/run/secrets/aiplex"`, "", false},
		{"conventional name", `aiplex = "MY_AIPLEX_TOKEN"`, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			file := "[secrets]\n" + c.line + "\n"
			os.WriteFile(yard, []byte(file), 0o600)
			out, errs, err := runCLI(t, "connect", "local", "--client", "branchyard", "--write", "--dry-run", "--file", yard)
			all := out + "\n" + errs
			if c.value != "" && strings.Contains(all, c.value) {
				t.Errorf("the value is printed:\n%s", all)
			}
			if c.refuse {
				if err == nil || exitCodeOf(err) == 0 {
					t.Errorf("must be refused:\n%s", all)
				}
				if !strings.Contains(all, "neither a variable name nor an @path") {
					t.Errorf("message:\n%s", all)
				}
			} else if err != nil {
				t.Fatalf("%v\n%s", err, all)
			}
			if b, _ := os.ReadFile(yard); string(b) != file {
				t.Errorf("file changed:\n%s", b)
			}
			if c.name == "token shaped like a name" && !strings.Contains(all, "not shown") {
				t.Errorf("a hidden reference must say so:\n%s", all)
			}
			if c.name == "conventional name" && !strings.Contains(all, `export MY_AIPLEX_TOKEN=`) {
				t.Errorf("a conventional name may be printed:\n%s", all)
			}
		})
	}
}
