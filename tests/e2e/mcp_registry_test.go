//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// fakeRegistry serves the MCP Registry API (v0.1) from memory: the entries are
// raw server objects so the test knows the exact bytes whose hash the CLI
// stores as provenance.
type fakeRegistry struct {
	servers map[string]string // name -> raw server object
}

func (f *fakeRegistry) entry(name string) string {
	return `{"server":` + f.servers[name] + `,"_meta":{"io.modelcontextprotocol.registry/official":{"status":"active","statusChangedAt":"2026-11-01T00:00:00Z","publishedAt":"2026-11-01T00:00:00Z","isLatest":true}}}`
}

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.EscapedPath()
	if path == "/v0.1/servers" {
		var parts []string
		for name := range f.servers {
			if q := r.URL.Query().Get("search"); q == "" || strings.Contains(name, q) {
				parts = append(parts, f.entry(name))
			}
		}
		fmt.Fprintf(w, `{"servers":[%s],"metadata":{"count":%d}}`, strings.Join(parts, ","), len(parts))
		return
	}
	const prefix = "/v0.1/servers/"
	if rest, ok := strings.CutPrefix(path, prefix); ok {
		name, _, _ := strings.Cut(rest, "/versions/")
		name = strings.ReplaceAll(name, "%2F", "/")
		if _, ok := f.servers[name]; ok {
			fmt.Fprint(w, f.entry(name))
			return
		}
	}
	http.NotFound(w, r)
}

func serverObject(name, description string, remotes, packages string) string {
	s := `{"$schema":"https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json","name":"` + name + `","description":` + jsonString(description) + `,"version":"1.2.3"`
	if remotes != "" {
		s += `,"remotes":` + remotes
	}
	if packages != "" {
		s += `,"packages":` + packages
	}
	return s + "}"
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestMCPRegistryImport(t *testing.T) {
	keyed := &devstack.MockMCP{APIKeyHeader: "X-Api-Key", APIKeyValue: "mcp-upstream-key"}
	srv, keyedURL, err := devstack.Listen(keyed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	e := start(t, func(o *devstack.Options) {
		o.ExtraOrigins = append(o.ExtraOrigins, keyedURL)
		o.Env = append(o.Env, "AIPLEX_TOOL_SECRET_MCP_KEY=mcp-upstream-key")
	})
	const bidi = "\u202e"
	reg := &fakeRegistry{servers: map[string]string{
		"e2e.example/open":   serverObject("e2e.example/open", "Open mock tools "+bidi+"reversed", `[{"type":"streamable-http","url":"`+e.stack.MCPURL+`"}]`, ""),
		"e2e.example/keyed":  serverObject("e2e.example/keyed", "Keyed mock tools", `[{"type":"streamable-http","url":"`+keyedURL+`","headers":[{"name":"X-Api-Key","isRequired":true,"isSecret":true,"description":"mock key"}]}]`, ""),
		"e2e.example/far":    serverObject("e2e.example/far", "A server on an origin nobody approved", `[{"type":"streamable-http","url":"https://mcp.unapproved.example/mcp"}]`, ""),
		"e2e.example/local":  serverObject("e2e.example/local", "Runs on your machine", "", `[{"registryType":"npm","identifier":"@e2e/local","version":"1.0.0","transport":{"type":"stdio"}}]`),
		"e2e.example/escape": serverObject("e2e.example/escape", "terminal \x1b[31mred", `[{"type":"streamable-http","url":"https://escape.example/mcp"}]`, ""),
		"e2e.example/sse":    serverObject("e2e.example/sse", "Legacy transport", `[{"type":"sse","url":"https://sse.example/sse"}]`, ""),
	}}
	registry, registryURL, err := devstack.Listen(reg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	run := func(args ...string) result {
		return e.cli(append([]string{"mcp", "registry"}, append(args, "--registry", registryURL)...)...)
	}
	must := func(args ...string) result {
		r := run(args...)
		if r.Code != 0 {
			t.Fatalf("aiplex mcp registry %s: %s", strings.Join(args, " "), r)
		}
		return r
	}

	t.Run("search and show classify what can be imported", func(t *testing.T) {
		out := must("search", "e2e.example").Out
		for _, want := range []string{"e2e.example/open", "remote", "not importable (local package)", "not importable (legacy SSE transport)"} {
			contains(t, out, want, "search")
		}
		notContains(t, out, bidi, "bidi override from registry content")
		contains(t, out, "1 entries failed validation", "an entry with a terminal escape is skipped")
		if r := run("show", "e2e.example/escape"); r.Code == 0 {
			t.Fatalf("an entry with a terminal escape was shown: %s", r)
		}
		shown := must("show", "e2e.example/local").Out
		contains(t, shown, "not importable (local package)", "show package")
		contains(t, shown, "sha256", "show hash")
		if r := run("show", "e2e.example/missing"); r.Code != 4 {
			t.Fatalf("missing server: %s", r)
		}
		if r := e.cli("mcp", "registry", "search", "x", "--registry", "http://registry.example"); r.Code != 2 {
			t.Fatalf("plain http to a public host accepted: %s", r)
		}
		var listing struct {
			Servers []struct {
				Name       string `json:"name"`
				Importable string `json:"importable"`
			} `json:"servers"`
		}
		if err := json.Unmarshal([]byte(must("search", "e2e.example", "-o", "json").Out), &listing); err != nil || len(listing.Servers) != 5 {
			t.Fatalf("json search: %v %d", err, len(listing.Servers))
		}
	})

	t.Run("dry run shows origin and secrets and creates nothing", func(t *testing.T) {
		out := must("import", "e2e.example/keyed", "--id", "keyed", "--allow-tools", "echo,add", "--dry-run").Out
		for _, want := range []string{"Origin:    " + keyedURL, "Not approved by this command", "network.allowed_origins", "api_key (header X-Api-Key), secret reference keyed-key", "Secrets to create", "AIPLEX_TOOL_SECRET_KEYED_KEY", "Dry run: nothing was created"} {
			contains(t, out, want, "dry run")
		}
		if r := e.cli("mcp", "connections", "get", "keyed"); r.Code != 4 {
			t.Fatalf("dry run created the connection: %s", r)
		}
		dry := must("import", "e2e.example/far", "--allow-tools", "x", "--dry-run").Out
		contains(t, dry, "dev --allow-origin https://mcp.unapproved.example", "how to approve the origin")
		notContains(t, must("import", "e2e.example/open", "--allow-tools", "echo", "--dry-run").Out, bidi, "bidi override in dry run")
	})

	t.Run("unsafe or incomplete imports are refused", func(t *testing.T) {
		if r := run("import", "e2e.example/open", "--yes"); r.Code != 2 || !strings.Contains(r.Err, "--allow-tools") {
			t.Fatalf("import without a tool choice: %s", r)
		}
		if r := run("import", "e2e.example/local", "--allow-tools", "x", "--yes"); r.Code == 0 || !strings.Contains(r.Err, "local package") {
			t.Fatalf("package imported: %s", r)
		}
		if r := run("import", "e2e.example/sse", "--allow-tools", "x", "--yes"); r.Code == 0 || !strings.Contains(r.Err, "legacy SSE") {
			t.Fatalf("sse imported: %s", r)
		}
		// A required secret reference is not guessed non-interactively.
		if r := run("import", "e2e.example/keyed", "--id", "keyed", "--allow-tools", "echo", "--yes"); r.Code != 2 || !strings.Contains(r.Err, "--secret-ref keyed-key") {
			t.Fatalf("missing secret reference: %s", r)
		}
		// Without --yes and without a terminal, nothing is created.
		if r := run("import", "e2e.example/open", "--allow-tools", "echo"); r.Code != 2 {
			t.Fatalf("import without --yes and without a terminal: %s", r)
		}
		// The origin is not approved by import.
		r := run("import", "e2e.example/far", "--allow-tools", "x", "--yes")
		if r.Code == 0 || !strings.Contains(r.Err, "does not approve the origin https://mcp.unapproved.example") || !strings.Contains(r.Err, "--allow-origin") {
			t.Fatalf("unapproved origin: %s", r)
		}
		if r := e.cli("mcp", "connections", "get", "far"); r.Code != 4 {
			t.Fatalf("a connection to an unapproved origin was created: %s", r)
		}
	})

	t.Run("import creates an ordinary read-only connection with provenance", func(t *testing.T) {
		out := must("import", "e2e.example/open", "--id", "imported", "--allow-tools", "echo,add,reset_counter", "--yes").Out
		contains(t, out, "Created tool connection imported", "import")
		spec := e.mustCLI("mcp", "connections", "get", "imported", "-o", "json").Out
		var record struct {
			Spec struct {
				Auth       map[string]any `json:"auth"`
				Tools      map[string]any `json:"tools"`
				Provenance map[string]any `json:"provenance"`
				Execution  struct {
					Tools []string `json:"allowed_tools"`
				} `json:"execution"`
			} `json:"spec"`
		}
		if err := json.Unmarshal([]byte(spec), &record); err != nil {
			t.Fatalf("%v\n%s", err, spec)
		}
		raw := reg.servers["e2e.example/open"]
		sum := sha256.Sum256([]byte(raw))
		p := record.Spec.Provenance
		if record.Spec.Auth["type"] != "none" || record.Spec.Tools["read_only"] != true || p["name"] != "e2e.example/open" || p["version"] != "1.2.3" || p["sha256"] != hex.EncodeToString(sum[:]) || p["registry"] != registryURL || p["retrieved_at"] == "" {
			t.Fatalf("saved connection: %s", spec)
		}
		// Read-only: nothing is callable until the tools are listed through
		// the gateway, and then only the annotated read-only ones.
		e.mustCLI("access-points", "apply", "-f", writeFile(t, "ap.json", map[string]any{"id": "imp", "caller_kinds": []string{"user"}, "subjects": map[string]any{}, "tools": []map[string]any{{"connection_ref": "imported"}}}))
		c := &mcpClient{e: e, path: "/ap/imp/mcp"}
		c.initialize("2025-06-18")
		if d := c.rpcData(c.call("imported__echo", map[string]any{"text": "x"})); d["reason"] != "read_only_unknown" {
			t.Fatalf("a call before listing: %v", d)
		}
		names := c.tools()
		if !has(names, "imported__echo") || has(names, "imported__reset_counter") {
			t.Fatalf("read-only listing: %v", names)
		}
		if got := callText(t, c.call("imported__echo", map[string]any{"text": "hello"})); got != "hello" {
			t.Fatalf("echo = %q", got)
		}
		if d := c.rpcData(c.call("imported__reset_counter", map[string]any{})); d["reason"] != "not_read_only" {
			t.Fatalf("destructive tool callable: %v", d)
		}
		if r := e.cli("mcp", "connections", "get", "imported"); r.Code != 0 || !strings.Contains(r.Out, "Read-only") {
			t.Fatalf("%s", r)
		}
		// A second import of the same id needs --update.
		if r := run("import", "e2e.example/open", "--id", "imported", "--allow-tools", "echo", "--yes"); r.Code != 5 {
			t.Fatalf("existing id: %s", r)
		}
	})

	t.Run("a secret credential is a reference the operator created", func(t *testing.T) {
		out := must("import", "e2e.example/keyed", "--id", "keyed", "--allow-tools", "echo", "--secret-ref", "mcp-key", "--yes").Out
		contains(t, out, "Created tool connection keyed", "import")
		notContains(t, out, "mcp-upstream-key", "secret value in output")
		e.mustCLI("access-points", "apply", "-f", writeFile(t, "ap2.json", map[string]any{"id": "kimp", "caller_kinds": []string{"user"}, "subjects": map[string]any{}, "tools": []map[string]any{{"connection_ref": "keyed"}}}))
		c := &mcpClient{e: e, path: "/ap/kimp/mcp"}
		c.initialize("2025-06-18")
		c.tools()
		if got := callText(t, c.call("keyed__echo", map[string]any{"text": "through the key"})); got != "through the key" {
			t.Fatalf("echo = %q", got)
		}
	})

	t.Run("probe discovers tools from a server that needs no credential", func(t *testing.T) {
		out := must("import", "e2e.example/open", "--id", "probed", "--probe", "--yes").Out
		contains(t, out, "Created tool connection probed", "probe import")
		spec := e.mustCLI("mcp", "connections", "get", "probed").Out
		contains(t, spec, "echo", "probed tools")
		if r := run("import", "e2e.example/keyed", "--id", "probed2", "--probe", "--secret-ref", "mcp-key", "--yes"); r.Code != 2 {
			t.Fatalf("--probe accepted for a server that needs a credential: %s", r)
		}
	})
}
