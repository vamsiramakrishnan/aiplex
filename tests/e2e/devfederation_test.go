//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// mintLocal asks the stack's local issuer for a token, as `aiplex dev token`
// does, with the developer's own token.
func (e *env) mintLocal(body string) (int, devstack.DevTokenResponse, string) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.stack.IssuerURL+devstack.DevTokenPath, bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+e.stack.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out devstack.DevTokenResponse
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

func (e *env) mustMint(body string) devstack.DevTokenResponse {
	e.t.Helper()
	status, out, raw := e.mintLocal(body)
	if status != 200 || out.Token == "" {
		e.t.Fatalf("mint %s: %d %s", body, status, raw)
	}
	return out
}

func bearer(token string) []string { return []string{"Authorization", "Bearer " + token} }

func (e *env) whoami(token string) httpResp {
	e.t.Helper()
	return e.do("GET", "/api/v1/iam/whoami", "", bearer(token)...)
}

const chatBody = `{"model":"mock-gpt","messages":[{"role":"user","content":"hi"}]}`

// Under the dev stack, a user from the stack's loopback workforce identity
// provider authenticates once a user identity provider names it, is not the
// developer, has no role, and reaches a model through an access point only with
// a grant. Tokens from an issuer nobody configured, for the wrong audience or
// signed with the wrong key are refused.
func TestDevFederationUser(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	idp := e.stack.Federation.IDP

	// Live authentication is on for both kinds (the gap this closes).
	for _, args := range [][]string{{"identity", "user-providers", "presets", "-o", "json"}, {"agents", "identity", "presets", "-o", "json"}} {
		contains(t, strings.Join(strings.Fields(e.mustCLI(args...).Out), ""), `"live_authentication":true`, strings.Join(args, " "))
	}

	alice := e.mustMint(`{"user":"alice","issuer":"idp","groups":["eng"]}`)
	if alice.Issuer != "idp" || alice.IssuerURL != idp.IssuerURL() || alice.JWKSURL != idp.JWKSURL || alice.Audience != idp.Audience || alice.PrincipalID == "" {
		t.Fatalf("mint answer does not describe the idp issuer: %+v", alice)
	}

	t.Run("refused until a provider names the issuer", func(t *testing.T) {
		if r := e.whoami(alice.Token); r.Status != 401 {
			t.Fatalf("idp token before any provider: %d %s", r.Status, r.Body)
		}
	})

	provider := map[string]any{"id": "local-idp", "preset": "oidc", "display_name": "Local IdP",
		"inputs": map[string]string{"issuer": idp.IssuerURL(), "jwks_url": idp.JWKSURL, "audience": idp.Audience, "groups_from": "claim", "group_claim": "groups"}}
	if r := e.cli("identity", "user-providers", "apply", "-f", writeFile(t, "idp.json", provider)); r.Code != 0 {
		t.Fatalf("apply the local idp: %s", r)
	}

	t.Run("a federated user is not the developer and has no role", func(t *testing.T) {
		r := e.whoami(alice.Token)
		if r.Status != 200 {
			t.Fatalf("whoami with the idp token: %d %s", r.Status, r.Body)
		}
		var who struct {
			Identity struct {
				PrincipalID string `json:"principal_id"`
			} `json:"identity"`
			Roles []string `json:"roles"`
		}
		_ = json.Unmarshal([]byte(r.Body), &who)
		if !strings.Contains(r.Body, "alice") || strings.Contains(r.Body, `"developer"`) || len(who.Roles) != 0 {
			t.Fatalf("whoami: %s", r.Body)
		}
		if who.Identity.PrincipalID != alice.PrincipalID {
			t.Fatalf("principal ID %q, the mint answer said %q", who.Identity.PrincipalID, alice.PrincipalID)
		}
		// An admin route: 403 ROLE_REQUIRED, not 401 and not served.
		r = e.do("PUT", "/api/v1/access-points/x", `{"id":"x"}`, append(bearer(alice.Token), "If-Match", "0")...)
		if r.Status != 403 || !strings.Contains(r.Body, "ROLE_REQUIRED") {
			t.Fatalf("federated user without a role on an admin route: %d %s", r.Status, r.Body)
		}
		// Development mode is unchanged: no credential is still served as the
		// local developer, and an unverifiable one is still refused.
		if r := e.do("GET", "/api/v1/dashboard/stats", ""); r.Status != 200 {
			t.Fatalf("anonymous request: %d %s", r.Status, r.Body)
		}
		if r := e.do("GET", "/api/v1/dashboard/stats", "", bearer("not-a-token")...); r.Status != 401 {
			t.Fatalf("garbage credential: %d %s", r.Status, r.Body)
		}
	})

	t.Run("reserved group names grant nothing", func(t *testing.T) {
		// A token that claims the developer's admin group, signed by the idp
		// (what an IdP could do), is a federated user: role bindings name the
		// local issuer's provider, so the group does not bind.
		forged, err := idp.Issuer.Mint(map[string]any{"sub": "mallory", "groups": []string{devstack.DevGroup, devstack.DevDeployerGroup}}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		r := e.whoami(forged)
		if r.Status != 200 {
			t.Fatalf("whoami: %d %s", r.Status, r.Body)
		}
		if strings.Contains(r.Body, `"admin"`) || strings.Contains(r.Body, `"deployer"`) {
			t.Fatalf("an idp group gave a role: %s", r.Body)
		}
		if r := e.do("PUT", "/api/v1/access-points/x", `{"id":"x"}`, append(bearer(forged), "If-Match", "0")...); r.Status != 403 {
			t.Fatalf("admin route with a group-claiming idp token: %d %s", r.Status, r.Body)
		}
		// The mint endpoint refuses the reserved groups and roles for it too.
		for _, body := range []string{`{"user":"m","issuer":"idp","groups":["` + devstack.DevGroup + `"]}`, `{"user":"m","issuer":"idp","role":"admin"}`, `{"user":"m","issuer":"workload","role":"deployer"}`, `{"user":"m","issuer":"nope"}`, `{"user":"developer","issuer":"idp"}`} {
			if status, _, raw := e.mintLocal(body); status != 400 {
				t.Fatalf("%s: %d %s", body, status, raw)
			}
		}
	})

	t.Run("through an access point, with and without a grant", func(t *testing.T) {
		chat := func(token string) httpResp {
			return e.do("POST", "/ap/local/v1/chat/completions", chatBody, append(bearer(token), "Content-Type", "application/json")...)
		}
		if r := chat(alice.Token); r.Status == 200 {
			t.Fatalf("federated user without grants reached the model: %s", r.Body)
		}
		e.mustCLI("agents", "user-scopes", "set", alice.PrincipalID, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*")
		r := chat(alice.Token)
		if r.Status != 200 {
			t.Fatalf("federated user with a grant: %d %s", r.Status, r.Body)
		}
		// The usage ledger names the federated principal, not the developer.
		recs := e.mustCLI("usage", "records", "-o", "json").Out
		contains(t, recs, alice.PrincipalID, "usage record principal")
	})

	t.Run("refused tokens", func(t *testing.T) {
		stray := e.mustMint(`{"user":"alice","issuer":"unconfigured"}`)
		wrongAud, _ := idp.Issuer.Mint(map[string]any{"sub": "alice", "aud": "somebody-else", "groups": []string{"eng"}}, time.Hour)
		otherKey, _ := devstack.NewIssuer(idp.IssuerURL(), idp.Audience)
		forged, _ := otherKey.Mint(map[string]any{"sub": "alice"}, time.Hour)
		expired, _ := idp.Issuer.Mint(map[string]any{"sub": "alice", "exp": time.Now().Add(-time.Hour).Unix()}, time.Hour)
		workload := e.mustMint(`{"user":"alice","issuer":"workload"}`)
		for name, token := range map[string]string{
			"issuer nobody configured": stray.Token,
			"right issuer, wrong aud":  wrongAud,
			"right issuer, wrong key":  forged,
			"expired":                  expired,
			"workload issuer, no bind": workload.Token,
		} {
			r := e.whoami(token)
			if r.Status != 401 {
				t.Fatalf("%s: %d %s", name, r.Status, r.Body)
			}
			notContains(t, r.Body, token, name+" echoes the token")
			if r := e.do("POST", "/ap/local/v1/chat/completions", chatBody, append(bearer(token), "Content-Type", "application/json")...); r.Status != 401 {
				t.Fatalf("%s through the access point: %d %s", name, r.Status, r.Body)
			}
		}
	})

	t.Run("the developer token still works and is still admin", func(t *testing.T) {
		r := e.whoami(e.stack.Token)
		if r.Status != 200 || !strings.Contains(r.Body, `"admin"`) {
			t.Fatalf("developer whoami: %d %s", r.Status, r.Body)
		}
	})
}

// A workload from the stack's loopback workload identity provider
// authenticates as the agent it is bound to, and as nobody else.
func TestDevFederationAgent(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	wl := e.stack.Federation.Workload
	good := e.mustMint(`{"user":"ci-1","issuer":"workload"}`)
	if good.Email != "" || good.Audience != wl.Audience {
		t.Fatalf("workload mint answer: %+v", good)
	}
	if r := e.whoami(good.Token); r.Status != 401 {
		t.Fatalf("workload token before a binding: %d %s", r.Status, r.Body)
	}

	e.mustCLI("agents", "register", "tutor", "--name", "Tutor", "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*")
	e.mustCLI("agents", "identity", "bind", "tutor", "ci", "--preset", "oidc",
		"--input", "issuer="+wl.IssuerURL(), "--input", "jwks_url="+wl.JWKSURL,
		"--input", "subject=ci-1", "--input", "audience="+wl.Audience)

	r := e.whoami(good.Token)
	if r.Status != 200 || !strings.Contains(r.Body, "tutor") || strings.Contains(r.Body, `"developer"`) || strings.Contains(r.Body, `"admin"`) {
		t.Fatalf("bound workload whoami: %d %s", r.Status, r.Body)
	}
	chat := func(token string) httpResp {
		return e.do("POST", "/ap/local/v1/chat/completions", chatBody, append(bearer(token), "Content-Type", "application/json")...)
	}
	// The binding grants nothing by itself: no grant, no model.
	if good.PrincipalID == "" {
		t.Fatalf("mint answer has no principal ID: %+v", good)
	}
	if r := chat(good.Token); r.Status != 403 || !strings.Contains(r.Body, good.PrincipalID) {
		t.Fatalf("bound agent without a grant: %d %s", r.Status, r.Body)
	}
	e.mustCLI("agents", "user-scopes", "set", good.PrincipalID, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*")
	if r := chat(good.Token); r.Status != 200 {
		t.Fatalf("bound agent with a grant through the access point: %d %s", r.Status, r.Body)
	}
	if r := e.do("PUT", "/api/v1/access-points/x", `{"id":"x"}`, append(bearer(good.Token), "If-Match", "0")...); r.Status != 403 {
		t.Fatalf("bound agent on an admin route: %d %s", r.Status, r.Body)
	}

	other := e.mustMint(`{"user":"ci-2","issuer":"workload"}`)
	wrongAud, _ := wl.Issuer.Mint(map[string]any{"sub": "ci-1", "aud": "somebody-else"}, time.Hour)
	stray := e.mustMint(`{"user":"ci-1","issuer":"unconfigured"}`)
	for name, token := range map[string]string{"unbound subject": other.Token, "wrong audience": wrongAud, "issuer nobody configured": stray.Token} {
		if r := e.whoami(token); r.Status != 401 {
			t.Fatalf("%s: %d %s", name, r.Status, r.Body)
		}
		if r := chat(token); r.Status != 401 {
			t.Fatalf("%s through the access point: %d %s", name, r.Status, r.Body)
		}
	}
	// Workload tokens carry a subject only.
	if status, _, raw := e.mintLocal(`{"user":"x","issuer":"workload","groups":["g"]}`); status != 400 {
		t.Fatalf("workload token with groups: %d %s", status, raw)
	}
}

// The seeded keyed tool connection holds its API key as a secret reference.
// The upstream refuses requests without the key; the gateway sends it; the key
// is not in the connection, the logs or any CLI output; and a connection whose
// reference does not resolve fails with an error that names the reference.
func TestDevKeyedToolConnection(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	seeded, err := e.stack.Seed(ctx, devstack.SeedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if seeded.KeyedConnection != devstack.SeedKeyedConnection {
		t.Fatalf("seeded %+v", seeded)
	}
	key := e.stack.KeyedMCP.APIKeyValue
	if len(key) < 20 {
		t.Fatalf("key %q is not a generated key", key)
	}

	// The upstream really refuses a caller without the key.
	resp, err := http.Post(e.stack.KeyedMCPURL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("keyed upstream without a key: %d", resp.StatusCode)
	}

	out := e.mustCLI("mcp", "connections", "test", devstack.SeedKeyedConnection)
	contains(t, out.Out, "echo", "tools listed through the keyed connection")
	call := e.mustCLI("mcp", "connections", "call", devstack.SeedKeyedConnection, "add", "--args", `{"a":2,"b":3}`)
	contains(t, call.Out, "5", "tool call through the keyed connection")

	got := e.mustCLI("resources", "get", "tool-connection", devstack.SeedKeyedConnection, "-o", "json").Out
	contains(t, got, `"secret_ref": "`+devstack.KeyedSecretRef+`"`, "connection holds the reference")
	for name, text := range map[string]string{"connection": got, "cli output": out.Out + call.Out, "api log": e.log.String()} {
		notContains(t, text, key, name+" shows the key")
	}
	// The connection list and the plain local-tools connection are unaffected.
	contains(t, e.mustCLI("mcp", "connections", "test", devstack.SeedConnection).Out, "echo", "local-tools")

	t.Run("a reference that does not resolve fails clearly", func(t *testing.T) {
		spec := devstack.ToolConnectionSpec("keyed-missing", e.stack.KeyedMCPURL, devstack.MockTools,
			map[string]any{"type": "api_key", "in": "header", "name": devstack.KeyedMCPHeader, "secret_ref": "no-such-ref"}, nil)
		e.mustCLI("resources", "put", "tool-connection", "keyed-missing", writeFile(t, "missing.json", spec), "0")
		r := e.cli("mcp", "connections", "test", "keyed-missing")
		if r.Code == 0 {
			t.Fatalf("connection with an unresolvable secret reference passed: %s", r)
		}
		contains(t, r.Err+r.Out, "credential", "error says the credential could not be used")
		contains(t, r.Err+r.Out, "aiplex mcp connections get keyed-missing", "error says where to look")
		for name, text := range map[string]string{"cli output": r.Out + r.Err, "api log": e.log.String()} {
			notContains(t, text, key, name+" shows the key")
		}
	})
}

// Everything the stack starts listens on loopback: the API, the mock
// upstreams, the issuers (including the federation listener) and the keyed
// MCP server. Checked against the kernel's table of listening sockets for this
// process and the API it started.
func TestDevStackListensOnLoopbackOnly(t *testing.T) {
	e := start(t)
	pids := []int{os.Getpid()}
	entries, _ := os.ReadDir("/proc")
	for _, ent := range entries {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", ent.Name(), "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...: comm may contain spaces, so cut after ')'.
		rest := string(stat[bytes.LastIndexByte(stat, ')')+2:])
		fields := strings.Fields(rest)
		if len(fields) > 1 && fields[1] == strconv.Itoa(os.Getpid()) {
			pids = append(pids, pid)
		}
	}
	inodes := map[string]bool{}
	for _, pid := range pids {
		fds, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "fd", fd.Name()))
			if err == nil && strings.HasPrefix(link, "socket:[") {
				inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
			}
		}
	}
	listening := 0
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		raw, err := os.ReadFile(table)
		if err != nil {
			if table == "/proc/net/tcp" {
				t.Skipf("cannot read %s: %v", table, err)
			}
			continue // no IPv6 table on this host
		}
		for _, line := range strings.Split(string(raw), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) < 10 || f[3] != "0A" || !inodes[f[9]] {
				continue
			}
			listening++
			addr := strings.Split(f[1], ":")[0]
			// 127.0.0.1 is 0100007F; ::1 is 00000000000000000000000001000000.
			if addr != "0100007F" && addr != "00000000000000000000000001000000" {
				t.Errorf("%s: listening on a non-loopback address %s (%s)", table, addr, line)
			}
		}
	}
	if listening < 9 { // API, mock models x2, MCP x2, mock agent, issuer, federation, branchyard stand-in
		t.Fatalf("found only %d listening sockets for the stack; the check is not seeing them", listening)
	}
	if !strings.HasPrefix(e.stack.Federation.URL, "http://127.0.0.1:") || !strings.HasPrefix(e.stack.KeyedMCPURL, "http://127.0.0.1:") || !strings.HasPrefix(e.stack.AgentURL, "http://127.0.0.1:") || !strings.HasPrefix(e.stack.Federation.Branchyard.URL, "http://127.0.0.1:") {
		t.Fatalf("federation %s, keyed MCP %s, branchyard %s", e.stack.Federation.URL, e.stack.KeyedMCPURL, e.stack.Federation.Branchyard.URL)
	}
}

// The real `aiplex dev`: its summary and status say federation and the keyed
// connection are there, `aiplex dev token --issuer idp` prints a token plus the
// exact provider to apply, applying what it printed makes that token work as a
// user who is not the developer, and the unconfigured issuer is refused.
func TestDevCommandFederationAndKeyedTools(t *testing.T) {
	home := t.TempDir()
	port, err := devstack.FreePort()
	if err != nil {
		t.Fatal(err)
	}
	base := append(cleanEnv(), "HOME="+home, "AIPLEX_API_BIN="+apiBin, "NO_COLOR=1")
	run := func(extra []string, args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command(cliBin, args...)
		cmd.Env = append(append([]string{}, base...), extra...)
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
	out, errOut, code := run(nil, "dev", "--detach", "--port", strconv.Itoa(port), "--no-console")
	if code != 0 {
		t.Fatalf("dev --detach: %s %s", out, errOut)
	}
	t.Cleanup(func() { run(nil, "dev", "stop") })
	contains(t, out, "Keyed tools    local-tools-keyed  (API key from a secret reference; try: aiplex mcp connections test local-tools-keyed)", "summary keyed tools row")
	contains(t, out, "Identity       federation on; stand-in providers on loopback: aiplex dev token --issuer idp|workload|unconfigured|branchyard", "summary identity row")
	t.Logf("new summary rows:\n%s", strings.Join(grepLines(out, "Keyed tools", "Identity  "), "\n"))
	status, _, _ := run(nil, "dev", "status")
	contains(t, status, "Identity       federation on; stand-in providers on loopback: aiplex dev token --issuer idp|workload|unconfigured|branchyard", "status identity row")

	// The keyed connection through the CLI, with the stored developer token.
	tools, e2, code := run(nil, "mcp", "connections", "test", "local-tools-keyed")
	if code != 0 || !strings.Contains(tools, "echo") {
		t.Fatalf("test keyed connection: %d %s %s", code, tools, e2)
	}

	token := func(args ...string) (string, string, int) {
		return run(nil, append([]string{"dev", "token"}, args...)...)
	}
	// Roles are not available outside the local issuer.
	if _, e3, code := token("--user", "x", "--issuer", "idp", "--role", "admin"); code == 0 || !strings.Contains(e3, "local issuer only") {
		t.Fatalf("--role with idp: %d %s", code, e3)
	}
	if _, e3, code := token("--user", "x", "--issuer", "bogus"); code == 0 || !strings.Contains(e3, "issuer must be one of") {
		t.Fatalf("--issuer bogus: %d %s", code, e3)
	}

	tok, hint, code := token("--user", "alice", "--issuer", "idp", "--groups", "eng")
	tok = strings.TrimSpace(tok)
	if code != 0 || tok == "" || strings.Count(tok, ".") != 2 {
		t.Fatalf("dev token --issuer idp: %d %q %s", code, tok, hint)
	}
	notContains(t, hint, tok, "token help prints the token")
	whoami := func(token string) (string, int) {
		o, e, c := run([]string{"AIPLEX_TOKEN=" + token}, "whoami")
		return o + e, c
	}
	if o, c := whoami(tok); c == 0 {
		t.Fatalf("idp token accepted before a provider was applied: %s", o)
	}
	// The printed block is applied, unmodified, by TestDevDocumentedFederationSteps;
	// here the same provider is applied from a file built from the stack's values.
	idp := map[string]any{"id": "local-idp", "preset": "oidc", "inputs": map[string]string{
		"issuer": "https://idp.aiplex-dev.invalid", "jwks_url": jwksFromHint(t, hint), "audience": "aiplex-dev-users", "groups_from": "claim", "group_claim": "groups"}}
	if o, e, c := run(nil, "identity", "user-providers", "apply", "-f", writeFile(t, "idp.json", idp)); c != 0 {
		t.Fatalf("apply the provider: %s %s", o, e)
	}
	o, c := whoami(tok)
	if c != 0 || !strings.Contains(o, "alice") || strings.Contains(o, "developer") {
		t.Fatalf("whoami with the federated token: %d %s", c, o)
	}
	stray, _, _ := token("--user", "alice", "--issuer", "unconfigured")
	if o, c := whoami(strings.TrimSpace(stray)); c == 0 {
		t.Fatalf("unconfigured issuer accepted: %s", o)
	}
	if o, c := whoami("garbage"); c == 0 {
		t.Fatalf("garbage accepted: %s", o)
	}
}

func grepLines(s string, needles ...string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		for _, n := range needles {
			if strings.Contains(l, n) {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

func jwksFromHint(t *testing.T, hint string) string {
	t.Helper()
	for _, l := range strings.Split(hint, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "jwks" {
			return f[1]
		}
	}
	t.Fatalf("no jwks line in:\n%s", hint)
	return ""
}
