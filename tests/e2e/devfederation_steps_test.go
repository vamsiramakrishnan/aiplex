//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// realDev starts the real `aiplex dev --detach` with an isolated HOME and
// returns a runner for shell scripts that see the test's aiplex first on PATH.
func realDev(t *testing.T) (sh func(script string) (string, int), aiplexRun func(env []string, args ...string) (string, string, int)) {
	t.Helper()
	home := t.TempDir()
	port, err := devstack.FreePort()
	if err != nil {
		t.Fatal(err)
	}
	base := append(cleanEnv(), "HOME="+home, "AIPLEX_API_BIN="+apiBin, "NO_COLOR=1", "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	aiplexRun = func(env []string, args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command(cliBin, args...)
		cmd.Env = append(append([]string{}, base...), env...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		code := 0
		if err := cmd.Run(); err != nil {
			x, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = x.ExitCode()
		}
		return out.String(), errb.String(), code
	}
	sh = func(script string) (string, int) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "steps.sh")
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-e", path)
		cmd.Env = base
		out, err := cmd.CombinedOutput()
		code := 0
		if x, ok := err.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	if out, errOut, code := aiplexRun(nil, "dev", "--detach", "--port", strconv.Itoa(port), "--no-console"); code != 0 {
		t.Fatalf("dev --detach: %s %s", out, errOut)
	}
	t.Cleanup(func() { aiplexRun(nil, "dev", "stop") })
	return sh, aiplexRun
}

// The steps a reader follows work as written: the provider block that
// `aiplex dev token --issuer idp` prints is valid shell when pasted unmodified,
// and the workload block of docs/local-development.md runs as it stands.
func TestDevDocumentedFederationSteps(t *testing.T) {
	sh, run := realDev(t)

	t.Run("printed provider block runs unmodified", func(t *testing.T) {
		tok, hint, code := run(nil, "dev", "token", "--user", "alice", "--issuer", "idp")
		tok = strings.TrimSpace(tok)
		if code != 0 || tok == "" {
			t.Fatalf("dev token: %d %s", code, hint)
		}
		lines := strings.Split(hint, "\n")
		start, end := -1, -1
		for i, l := range lines {
			if start < 0 && strings.Contains(l, "aiplex identity user-providers apply") {
				start = i
			}
			if start >= 0 && l == "EOF" {
				end = i
				break
			}
		}
		if start < 0 || end < 0 {
			t.Fatalf("no here-document ending at column 0 in:\n%s", hint)
		}
		if out, code := sh(strings.Join(lines[start:end+1], "\n") + "\n"); code != 0 {
			t.Fatalf("printed block failed as shell (%d):\n%s", code, out)
		}
		out, _, code := run([]string{"AIPLEX_TOKEN=" + tok}, "whoami")
		if code != 0 || !strings.Contains(out, "alice") {
			t.Fatalf("whoami after applying the printed provider: %d %s", code, out)
		}
	})

	t.Run("documented workload steps run as written", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(repoDir, "docs", "local-development.md"))
		if err != nil {
			t.Fatal(err)
		}
		_, after, ok := strings.Cut(string(raw), "A workload token is bound to an agent the same way:")
		_, block, ok2 := strings.Cut(after, "```sh\n")
		if !ok || !ok2 {
			t.Fatal("workload steps not found in docs/local-development.md")
		}
		block, _, _ = strings.Cut(block, "```")
		for _, want := range []string{"aiplex agents register tutor", "aiplex dev token --user ci-1 --issuer workload", "whoami"} {
			if !strings.Contains(block, want) {
				t.Fatalf("documented block lacks %q:\n%s", want, block)
			}
		}
		out, code := sh(block)
		if code != 0 {
			t.Fatalf("documented workload steps failed (%d):\n%s\n--- block ---\n%s", code, out, block)
		}
		if !strings.Contains(out, "tutor") {
			t.Fatalf("whoami after the documented steps does not show the agent:\n%s", out)
		}
	})
}

// Federation can be switched off through Options.Env: the entry replaces the
// default and the presets then report live authentication as off.
func TestDevStackFederationCanBeDisabled(t *testing.T) {
	e := start(t, func(o *devstack.Options) {
		o.Env = append(o.Env, "AIPLEX_USER_FEDERATION=disabled", "AIPLEX_AGENT_FEDERATION=disabled")
	})
	for _, args := range [][]string{{"identity", "user-providers", "presets", "-o", "json"}, {"agents", "identity", "presets", "-o", "json"}} {
		contains(t, strings.Join(strings.Fields(e.mustCLI(args...).Out), ""), `"live_authentication":false`, strings.Join(args, " "))
	}
}

// A keyed connection of the user's own resolves its secret reference from an
// AIPLEX_TOOL_SECRET_<NAME> variable exported before the stack starts, as the
// guide says.
func TestDevKeyedConnectionOfYourOwnReadsExportedSecret(t *testing.T) {
	const value = "own-upstream-key-not-in-any-file"
	t.Setenv("AIPLEX_TOOL_SECRET_MY_OWN_KEY", value)
	own := &devstack.MockMCP{APIKeyHeader: "X-Api-Key", APIKeyValue: value}
	srv, ownURL, err := devstack.Listen(own)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	e := start(t, func(o *devstack.Options) { o.ExtraOrigins = append(o.ExtraOrigins, ownURL) })
	spec := devstack.ToolConnectionSpec("own", ownURL, devstack.MockTools,
		map[string]any{"type": "api_key", "in": "header", "name": "X-Api-Key", "secret_ref": "my-own-key"}, nil)
	e.mustCLI("resources", "put", "tool-connection", "own", writeFile(t, "own.json", spec), "0")
	r := e.mustCLI("mcp", "connections", "test", "own")
	contains(t, r.Out, "echo", "tools through the connection that uses the exported secret")
	notContains(t, e.log.String()+r.Out+r.Err, value, "output shows the key")
	// The same shape with an unexported name fails, with the server's text.
	spec = devstack.ToolConnectionSpec("own2", ownURL, devstack.MockTools,
		map[string]any{"type": "api_key", "in": "header", "name": "X-Api-Key", "secret_ref": "not-exported"}, nil)
	e.mustCLI("resources", "put", "tool-connection", "own2", writeFile(t, "own2.json", spec), "0")
	r = e.cli("mcp", "connections", "test", "own2")
	if r.Code == 0 {
		t.Fatalf("unexported reference resolved: %s", r)
	}
	contains(t, r.Err, "operator tool credential unavailable", "the server's own text is shown")
	contains(t, r.Err, "AIPLEX_TOOL_SECRET_<NAME>", "dev hint on a loopback API")
	_ = time.Second
}
