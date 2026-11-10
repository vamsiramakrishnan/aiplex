//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// The guided paths create working resources without hand-written files: a
// tool connection (tools discovered from the server) tested through the
// gateway, a model surface, an access point admitting a user by subject, a
// price set from per-million rates, and usage that names the caller.
func TestGuidedCreationWithoutFiles(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}

	t.Run("mcp connections", func(t *testing.T) {
		r := e.mustCLI("mcp", "connections", "add", "tools2", "--url", e.stack.MCPURL, "--mode", "json")
		contains(t, r.Out, "Discovered 3 tools", "tools discovered from the server")
		contains(t, r.Out, "tools/list through the gateway: 3 tools", "add tests through the gateway")
		r = e.mustCLI("mcp", "connections", "list")
		contains(t, r.Out, "tools2", "list")
		r = e.mustCLI("mcp", "connections", "test", "tools2", "-o", "json")
		contains(t, r.Out, `"echo"`, "test -o json")
		r = e.cli("mcp", "connections", "add", "tools2", "--url", e.stack.MCPURL)
		if r.Code != 5 {
			t.Fatalf("re-adding an existing connection: %s", r)
		}
		contains(t, r.Err, "--update", "conflict says how to replace")
		r = e.cli("mcp", "connections", "add", "keyed", "--url", e.stack.MCPURL, "--auth", "bearer", "--secret-ref", "sk-live-0123456789abcdef+")
		if r.Code != 2 || strings.Contains(r.Err, "sk-live") {
			t.Fatalf("secret value accepted or echoed: %s", r)
		}
		// With no terminal, a delete without --yes is refused (exit 2) and
		// nothing is deleted.
		r = e.cli("mcp", "connections", "delete", "tools2")
		if r.Code != 2 || !strings.Contains(r.Err, "--yes") {
			t.Fatalf("delete without --yes and without a terminal: %s", r)
		}
		e.mustCLI("mcp", "connections", "get", "tools2")
		e.mustCLI("mcp", "connections", "delete", "tools2", "--yes")
	})

	t.Run("surface, access point, price, usage", func(t *testing.T) {
		e.mustCLI("llm", "surfaces", "add", "compat2", "--provider", "openai-compatible", "--origin", e.stack.ModelsURL, "--models", "mock-gpt")
		bobID := auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, "bob")
		r := e.mustCLI("access-points", "create", "team", "--model", "team-model=compat2:mock-gpt", "--tool", "local-tools", "--allow-users", "bob,developer")
		contains(t, r.Out, "/ap/team", "access point URL")
		r = e.mustCLI("access-points", "get", "team", "-o", "json")
		contains(t, r.Out, bobID, "subject resolved to bob's principal ID")
		r = e.cli("access-points", "create", "bad", "--model", "m=nosuch")
		if r.Code != 2 {
			t.Fatalf("bad surface_ref: %s", r)
		}
		contains(t, r.Err, "models/0/surface_ref", "field path")
		contains(t, r.Err, "compat2", "valid choices listed")

		r = e.mustCLI("usage", "prices", "set", "team-model", "--input-per-mtok", "3", "--output-per-mtok", "15")
		contains(t, r.Out, "input USD", "") // rates are rendered with the currency
		r = e.mustCLI("usage", "prices", "get", "team-model", "-o", "json")
		contains(t, r.Out, `"numerator": 3`, "exact rational for $3/Mtok at scale 6")
		r = e.mustCLI("usage", "prices", "set", "team-model", "--output-per-mtok", "0.5")
		contains(t, r.Out, "revision 2", "read-modify-write")

		if resp := e.post("/ap/team/v1/chat/completions", `{"model":"team-model","messages":[{"role":"user","content":"hi"}]}`); resp.Status != 200 {
			t.Fatalf("chat through the created access point: %d %s", resp.Status, resp.Body)
		}
		r = e.mustCLI("usage", "records", "--access-point", "team")
		contains(t, r.Out, "developer@localhost", "caller shown by email, not the principal hash")
		notContains(t, r.Out, "principal-", "principal hash in the default table")
		r = e.mustCLI("usage", "records", "--access-point", "team", "-o", "json")
		contains(t, r.Out, `"principal_display": "developer@localhost"`, "display recorded server-side")
		contains(t, r.Out, `"principal_id": "principal-`, "canonical id kept")
		r = e.mustCLI("usage", "summary", "--by", "user")
		contains(t, r.Out, "developer@localhost", "summary label")
	})
}

// Errors say what happened and what to run next, with documented exit codes.
func TestErrorsExplainAndExitCodes(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	r := e.cli("access-points", "get", "locl")
	if r.Code != 4 {
		t.Fatalf("404 exit: %s", r)
	}
	contains(t, r.Err, `access point "locl" not found`, "names kind and id")
	contains(t, r.Err, "Did you mean: local", "near miss")
	contains(t, r.Err, "aiplex access-points list", "list command")

	r = e.cli("access-points", "lst")
	if r.Code != 2 {
		t.Fatalf("unknown subcommand exit: %s", r)
	}
	contains(t, r.Err, "Did you mean: aiplex access-points list", "subcommand suggestion")
	r = e.cli("access-points", "get")
	if r.Code != 2 {
		t.Fatalf("missing arg exit: %s", r)
	}
	contains(t, r.Err, "missing <id>", "missing argument named")
	r = e.cli("-o", "tabel", "access-points", "list")
	if r.Code != 2 {
		t.Fatalf("bad -o: %s", r)
	}

	bob, err := e.stack.Issuer.Mint(map[string]any{"sub": "bob", "email": "bob@localhost", "groups": []string{"eng"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r = e.cliEnv([]string{"AIPLEX_URL=" + e.stack.APIURL, "AIPLEX_TOKEN=" + bob}, "llm", "surfaces", "list")
	if r.Code != 3 {
		t.Fatalf("403 exit: %s", r)
	}
	contains(t, r.Err, "needs the admin or deployer role", "role named")
	contains(t, r.Err, "You are bob@localhost with no role", "caller named")
	contains(t, r.Err, "aiplex whoami", "next command")
	notContains(t, r.Err, "ROLE_REQUIRED (403)", "raw API error")

	r = e.cliEnv([]string{"AIPLEX_URL=" + e.stack.APIURL, "AIPLEX_TOKEN=" + bob}, "whoami")
	if r.Code != 0 {
		t.Fatal(r)
	}
	contains(t, r.Out, "Identity: bob@localhost", "whoami from env")
	contains(t, r.Out, "Token:    from AIPLEX_TOKEN", "token source")
	contains(t, r.Out, "Roles:    none", "roles")

	r = e.cliEnv([]string{"AIPLEX_URL=http://127.0.0.1:1"}, "access-points", "list")
	if r.Code != 6 {
		t.Fatalf("connection refused exit: %s", r)
	}
	contains(t, r.Err, "aiplex dev", "start hint")

	r = e.cliEnv(nil, "--context", "nope", "access-points", "list")
	if r.Code != 2 {
		t.Fatalf("unknown --context: %s", r)
	}

	// Development mode refuses an unauthenticated request that came through
	// a proxy; a direct one is served.
	if resp := e.do("GET", "/api/v1/access-points", "", "X-Forwarded-For", "203.0.113.9"); resp.Status != 403 || !strings.Contains(resp.Body, "PROXIED_DEVELOPMENT_REQUEST") {
		t.Fatalf("proxied anonymous dev request: %d %s", resp.Status, resp.Body)
	}
	if resp := e.do("GET", "/api/v1/access-points", ""); resp.Status != 200 {
		t.Fatalf("direct anonymous dev request: %d %s", resp.Status, resp.Body)
	}
	if resp := e.do("GET", "/api/v1/access-points", "", append(e.auth(), "X-Forwarded-For", "203.0.113.9")...); resp.Status != 200 {
		t.Fatalf("authenticated request through a proxy: %d %s", resp.Status, resp.Body)
	}
}

// The file example in `identity user-providers apply --help` is accepted by
// the server's validation.
func TestUserProviderHelpExampleIsValid(t *testing.T) {
	e := start(t)
	r := e.mustCLI("identity", "user-providers", "apply", "--help")
	m := regexp.MustCompile(`(?s)\n(  id: .*?)\n\n`).FindStringSubmatch(r.Out)
	if m == nil {
		t.Fatalf("no YAML example in help:\n%s", r.Out)
	}
	lines := strings.Split(m[1], "\n")
	for i := range lines {
		lines[i] = strings.TrimPrefix(lines[i], "  ")
	}
	path := writeFile(t, "okta.yaml", nil)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = e.cli("identity", "user-providers", "apply", "-f", path)
	if r.Code != 0 {
		t.Fatalf("help example rejected by the server: %s\n%s", r, strings.Join(lines, "\n"))
	}
	e.mustCLI("identity", "user-providers", "get", "okta")
}

// aiplex dev --detach / status / stop, and a killed aiplex dev does not leave
// its local API running.
func TestDevDetachStatusStopAndNoOrphan(t *testing.T) {
	home := t.TempDir()
	port, err := devstack.FreePort()
	if err != nil {
		t.Fatal(err)
	}
	env := append(cleanEnv(), "HOME="+home, "AIPLEX_API_BIN="+apiBin, "NO_COLOR=1")
	run := func(args ...string) (string, int) {
		cmd := exec.Command(cliBin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		code := 0
		if x, ok := err.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	if out, code := run("dev", "status"); code == 0 {
		t.Fatalf("status with nothing running: %s", out)
	}
	out, code := run("dev", "--detach", "--port", strconv.Itoa(port), "--no-console")
	if code != 0 {
		t.Fatalf("dev --detach: %s", out)
	}
	contains(t, out, "Running in the background", "detach summary")
	contains(t, out, "# 2. See what it cost", "next steps")
	out, code = run("dev", "status", "-o", "json")
	if code != 0 {
		t.Fatalf("status: %s", out)
	}
	var st struct {
		Running bool `json:"running"`
		State   struct {
			PID int    `json:"pid"`
			URL string `json:"url"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || !st.Running || st.State.PID == 0 {
		t.Fatalf("status json: %v %s", err, out)
	}
	if out, code = run("dev", "--port", strconv.Itoa(port+1)); code != 5 || !strings.Contains(out, "already running") {
		t.Fatalf("second aiplex dev: %d %s", code, out)
	}
	out, _ = run("usage", "records")
	contains(t, out, "No usage records", "the detached API answers through the local context")
	if out, code = run("dev", "stop"); code != 0 || !strings.Contains(out, "Stopped") {
		t.Fatalf("stop: %s", out)
	}
	if devstackReachable(st.State.URL) {
		t.Fatal("local API still answering after dev stop")
	}

	// SIGKILL the aiplex dev process: its API must exit on its own.
	if out, code = run("dev", "--detach", "--port", strconv.Itoa(port), "--no-console"); code != 0 {
		t.Fatalf("dev --detach again: %s", out)
	}
	out, _ = run("dev", "status", "-o", "json")
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err, out)
	}
	if err := syscall.Kill(st.State.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() error {
		if devstackReachable(st.State.URL) {
			return &timeoutErr{"local API still running after aiplex dev was killed"}
		}
		return nil
	})
	if out, code = run("dev", "status"); code == 0 {
		t.Fatalf("status after kill: %s", out)
	}
	if out, _ = run("dev", "stop"); !strings.Contains(out, "not running") {
		t.Fatalf("stop after kill: %s", out)
	}
}

func devstackReachable(url string) bool {
	cmd := exec.Command("curl", "--noproxy", "*", "-fsS", "-m", "1", url+"/healthz")
	return cmd.Run() == nil
}
