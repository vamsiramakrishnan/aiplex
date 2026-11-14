package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func runComplete(t *testing.T, args ...string) string {
	t.Helper()
	root := newRootCmd()
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"__complete"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestCompletionOffersIDsFromTheServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/access-points/":
			w.Write([]byte(`[{"id":"local","revision":1,"spec":{"id":"local"}},{"id":"team","revision":1,"spec":{"id":"team"}}]`))
		case "/api/v1/mcp/connections/":
			w.Write([]byte(`[{"kind":"tool-connection","id":"local-tools","revision":1,"spec":{}}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("AIPLEX_URL", server.URL)
	t.Setenv("HOME", t.TempDir())
	if out := runComplete(t, "access-points", "get", "t"); !strings.Contains(out, "team") || strings.Contains(out, "local\n") {
		t.Fatalf("access point ids: %q", out)
	}
	if out := runComplete(t, "connect", ""); !strings.Contains(out, "local") || !strings.Contains(out, "team") {
		t.Fatalf("connect ids: %q", out)
	}
	if out := runComplete(t, "mcp", "connections", "test", ""); !strings.Contains(out, "local-tools") {
		t.Fatalf("connection ids: %q", out)
	}
	if out := runComplete(t, "-o", ""); !strings.Contains(out, "wide") {
		t.Fatalf("output formats: %q", out)
	}
}

// Completion never hangs on an API that does not answer.
func TestCompletionIsBounded(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer server.Close()
	defer close(block)
	t.Setenv("AIPLEX_URL", server.URL)
	t.Setenv("HOME", t.TempDir())
	start := time.Now()
	out := runComplete(t, "access-points", "get", "")
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("completion took %s", d)
	}
	if strings.Contains(out, "local") {
		t.Fatalf("unexpected ids: %q", out)
	}
}

// cannotCompleteIDs lists every command whose first argument is a
// <placeholder> but which has no row in completionTable, and says why. A
// command that takes an id the API can list belongs in completionTable, not
// here. TestEveryIDArgumentCompletesOrIsListedWithAReason fails on a command
// missing from both, and on an entry here that is stale.
var cannotCompleteIDs = map[string]string{
	// A new id the caller chooses: there is nothing to complete.
	"access-points create":   "creates an access point under a new id",
	"agents register":        "registers an agent under a new client id",
	"budgets create":         "creates a budget under a new id",
	"llm surfaces create":    "creates a surface under a new id",
	"mcp connections create": "creates a tool connection under a new id",
	"usage prices set":       "sets the price of a model the caller names; the id is new or free text",

	// Free text, a file, or a value that is not a stored id.
	"iam validate-principal":      "takes a principal string to check, not a stored id",
	"quota loan-grant":            "takes a JSON file",
	"quota policy-validate":       "takes a JSON file",
	"quota preview":               "takes a JSON file",
	"quota reconcile-plan":        "takes a JSON file",
	"validate":                    "takes a file",
	"mcp registry search":         "takes search text for an external registry",
	"usage prices catalog search": "takes search text for the bundled price catalogues",
	"usage prices catalog show":   "names a model in the bundled catalogues, which are local files, not an API list",
	"usage prices catalog import": "names models in the bundled catalogues, which are local files, not an API list",
	"mcp registry show":           "names a server in the external MCP Registry, not in this API",
	"mcp registry import":         "names a server in the external MCP Registry, not in this API",

	// Ids the API cannot list.
	"infra approve":              "plan ids are returned when a plan is saved; there is no list route",
	"infra execute":              "plan ids are returned when a plan is saved; there is no list route",
	"infra observation":          "plan ids are returned when a plan is saved; there is no list route",
	"infra observe":              "plan ids are returned when a plan is saved; there is no list route",
	"infra receipt":              "plan ids are returned when a plan is saved; there is no list route",
	"native governance apply":    "plan ids are returned when a plan is saved; there is no list route",
	"native governance get-plan": "plan ids are returned when a plan is saved; there is no list route",
	"native governance receipt":  "plan ids are returned when a plan is saved; there is no list route",
}

// positionalPlaceholder returns the first word after the command name in a Use
// line when it is a <placeholder> or an optional [placeholder] (not a [--flag]):
// the command takes a positional argument.
func positionalPlaceholder(use string) (string, bool) {
	words := strings.Fields(use)
	if len(words) < 2 {
		return "", false
	}
	w := words[1]
	if strings.HasPrefix(w, "<") || strings.HasPrefix(w, "[") && !strings.HasPrefix(w, "[-") {
		return w, true
	}
	return "", false
}

func commandKey(c *cobra.Command) string { return strings.TrimPrefix(c.CommandPath(), "aiplex ") }

// Every command that takes a positional argument either completes it (one row
// in completionTable) or is on cannotCompleteIDs with a reason. Completion is
// registered nowhere else.
func TestEveryIDArgumentCompletesOrIsListedWithAReason(t *testing.T) {
	root := newRootCmd()
	inTree := map[string]*cobra.Command{}
	walkCommands(root, func(c *cobra.Command) {
		key := commandKey(c)
		inTree[key] = c
		_, takesArg := positionalPlaceholder(c.Use)
		_, inTable := completionTable[key]
		local := slices.Contains(contextCompletions, key)
		reason, listed := cannotCompleteIDs[key]
		completes := c.ValidArgsFunction != nil || len(c.ValidArgs) > 0
		switch {
		case inTable || local:
			if !completes {
				t.Errorf("%s: has a completion table row but no completion is registered on the command", key)
			}
			if listed {
				t.Errorf("%s: is in completionTable and also in cannotCompleteIDs", key)
			}
		case len(c.ValidArgs) > 0:
			// A fixed list of words (the shells of `completion`), not ids.
		case completes:
			t.Errorf("%s: registers its own completion; add a row to completionTable instead", key)
		case takesArg && !listed:
			t.Errorf("%s (%s): takes an argument but completes nothing; add a row to completionTable, or an entry with a reason to cannotCompleteIDs", key, c.Use)
		case listed && !takesArg:
			t.Errorf("%s: is in cannotCompleteIDs but takes no <placeholder> argument", key)
		case listed && strings.TrimSpace(reason) == "":
			t.Errorf("%s: cannotCompleteIDs entry has no reason", key)
		}
	})
	for key := range completionTable {
		if _, ok := inTree[key]; !ok {
			t.Errorf("completionTable row %q names no command", key)
		}
	}
	for key := range cannotCompleteIDs {
		if _, ok := inTree[key]; !ok {
			t.Errorf("cannotCompleteIDs entry %q names no command", key)
		}
	}
	for _, key := range contextCompletions {
		if _, ok := inTree[key]; !ok {
			t.Errorf("contextCompletions entry %q names no command", key)
		}
	}
}

func TestEveryTableRowHasASourceForItsFirstArgument(t *testing.T) {
	root := newRootCmd()
	walkCommands(root, func(c *cobra.Command) {
		sources, ok := completionTable[commandKey(c)]
		if !ok {
			return
		}
		if _, takesArg := positionalPlaceholder(c.Use); !takesArg {
			t.Errorf("%s: completionTable row for a command with no positional <placeholder> (%s)", commandKey(c), c.Use)
		}
		if len(sources) == 0 || sources[0] == nil {
			t.Errorf("%s: row has no source for the first argument", commandKey(c))
		}
	})
}

func TestSafeCandidate(t *testing.T) {
	good := []string{"local", "team-a", "a.b_c", "openai/gpt-4o", "run_01HZ", "gpt:4", "a@b+c=d,e"}
	for _, id := range good {
		if !safeCandidate(id) {
			t.Errorf("%q should be offered", id)
		}
	}
	bad := []string{"", "-rf", "--flag", "a b", "a\tdescription", "a\nb", "a\rb", "a\x00b", "a\x1b[31m", "x;rm", "x&y", "x|y", "$(id)", "`id`", "a'b", `a"b`, "a\\b", "a*", "a?", "~root", "a>b", "a<b", "a#b", "a!b", "a(b)", "a{b}", "a[b]", "é", strings.Repeat("a", completionMaxLen+1), "a b"}
	for _, id := range bad {
		if safeCandidate(id) {
			t.Errorf("%q must not be offered", id)
		}
	}
}

// completionServer records every request it answers.
type completionServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
}

func (cs *completionServer) record(r *http.Request) {
	cs.mu.Lock()
	cs.requests = append(cs.requests, r.Method+" "+r.URL.Path)
	cs.mu.Unlock()
}

func (cs *completionServer) seen() []string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return slices.Clone(cs.requests)
}

func serveCompletion(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *completionServer {
	t.Helper()
	cs := &completionServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.record(r)
		handler(w, r)
	}))
	t.Cleanup(cs.Close)
	t.Setenv("AIPLEX_URL", cs.URL)
	t.Setenv("AIPLEX_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	return cs
}

func serveStatus(t *testing.T, status int, body string) *completionServer {
	return serveCompletion(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
}

// candidates parses the output of `__complete`: one candidate per line, then
// a ":<directive>" line and a note.
func candidates(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "Completion ended") {
			continue
		}
		ids = append(ids, line)
	}
	return ids
}

// Completion offers nothing, and prints nothing else, whenever the answer
// cannot be trusted: the API refuses, the caller lacks the role, the list is
// empty, the answer is not a list.
func TestCompletionIsSilentWhenTheAnswerIsNotAList(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"not signed in":  {401, `{"error":"unauthorized"}`},
		"lacks the role": {403, `{"error":"forbidden"}`},
		"server error":   {500, `boom`},
		"empty list":     {200, `[]`},
		"not json":       {200, `<html>`},
		"wrong shape":    {200, `{"unexpected":true}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cs := serveStatus(t, tc.status, tc.body)
			for _, args := range [][]string{
				{"access-points", "get", ""}, {"hooks", "policies", "get", ""}, {"clients", "get", ""}, {"runs", "inspect", ""},
				{"agents", "identity", "get", "a1", ""}, {"resources", "get", "tool-connection", ""}, {"catalog", "get", ""},
			} {
				out := runComplete(t, args...)
				if got := candidates(out); len(got) != 0 {
					t.Errorf("%v: offered %q", args, got)
				}
				if strings.Contains(strings.ToLower(out), "error") {
					t.Errorf("%v: printed an error: %q", args, out)
				}
			}
			for _, r := range cs.seen() {
				if !strings.HasPrefix(r, "GET ") {
					t.Errorf("completion sent %s", r)
				}
			}
		})
	}
}

func TestCompletionIsSilentWhenTheAPIIsDown(t *testing.T) {
	cs := serveStatus(t, 200, `[]`)
	cs.Close()
	for _, args := range [][]string{{"access-points", "get", ""}, {"runs", "inspect", ""}, {"clients", "delete", ""}} {
		if out := runComplete(t, args...); len(candidates(out)) != 0 || strings.Contains(strings.ToLower(out), "error") {
			t.Errorf("%v: %q", args, out)
		}
	}
}

// Table rows are exercised end to end: the one GET route, the ids offered,
// prefix filtering, and later arguments depending on earlier ones.
func TestCompletionRoutesAndIDs(t *testing.T) {
	routes := map[string]string{
		"/api/v1/hooks/policies/":           `[{"id":"pii","revision":1,"spec":{}},{"id":"toxic","revision":1,"spec":{}}]`,
		"/api/v1/clients/registrations":     `[{"id":"claude-code","revision":1,"spec":{}}]`,
		"/api/v1/agents":                    `[{"client_id":"agent-1"},{"client_id":"agent-2"}]`,
		"/api/v1/agents/agent-1/identities": `[{"id":"aws-prod","client_id":"agent-1"}]`,
		"/api/v1/iam/role-bindings":         `[{"id":"eng-deployers","group":"eng","role":"deployer"}]`,
		"/api/v1/a2a/delegations":           `[{"id":"del-1"}]`,
		"/api/v1/a2a/connections/":          `[{"id":"bedrock","revision":1,"spec":{}}]`,
		"/api/v1/runs":                      `{"runs":[{"run_id":"run-1"},{"run_id":"run-2"}]}`,
		"/api/v1/instances":                 `[{"id":"inst-1"}]`,
		"/api/v1/catalog":                   `{"templates":[{"id":"tpl-1"}],"total":1}`,
		"/api/v1/modules":                   `[{"name":"llmplex"}]`,
		"/api/v1/budgets":                   `[{"id":"team-a","revision":1,"spec":{}}]`,
		"/api/v1/mcp/connections/":          `[{"id":"docs","revision":1,"spec":{}}]`,
	}
	cs := serveCompletion(t, func(w http.ResponseWriter, r *http.Request) {
		if body, ok := routes[r.URL.Path]; ok && r.Method == http.MethodGet {
			w.Write([]byte(body))
			return
		}
		w.WriteHeader(404)
	})
	cases := []struct {
		args  []string
		want  string
		route string
	}{
		{[]string{"hooks", "policies", "get", "p"}, "pii", "GET /api/v1/hooks/policies/"},
		{[]string{"clients", "plan", ""}, "claude-code", "GET /api/v1/clients/registrations"},
		{[]string{"agents", "delete", "agent-2"}, "agent-2", "GET /api/v1/agents"},
		{[]string{"agents", "identity", "get", "agent-1", ""}, "aws-prod", "GET /api/v1/agents/agent-1/identities"},
		{[]string{"iam", "role-bindings", "rm", "e"}, "eng-deployers", "GET /api/v1/iam/role-bindings"},
		{[]string{"a2a", "delegations", "show", ""}, "del-1", "GET /api/v1/a2a/delegations"},
		{[]string{"a2a", "invoke", ""}, "bedrock", "GET /api/v1/a2a/connections/"},
		{[]string{"runs", "cancel", "run-"}, "run-2", "GET /api/v1/runs"},
		{[]string{"logs", ""}, "inst-1", "GET /api/v1/instances"},
		{[]string{"deploy", ""}, "tpl-1", "GET /api/v1/catalog"},
		{[]string{"modules", "inspect", ""}, "llmplex", "GET /api/v1/modules"},
		{[]string{"budgets", "status", ""}, "team-a", "GET /api/v1/budgets"},
		{[]string{"mcp", "connections", "rules", "list", ""}, "docs", "GET /api/v1/mcp/connections/"},
		{[]string{"resources", "get", "tool-connection", ""}, "docs", "GET /api/v1/mcp/connections/"},
		{[]string{"resources", "get", ""}, "tool-connection", ""},
	}
	for _, tc := range cases {
		before := len(cs.seen())
		out := runComplete(t, tc.args...)
		if !slices.Contains(candidates(out), tc.want) {
			t.Errorf("%v: want %q, got %q", tc.args, tc.want, out)
		}
		got := cs.seen()[before:]
		if tc.route == "" && len(got) != 0 || tc.route != "" && (len(got) != 1 || got[0] != tc.route) {
			t.Errorf("%v: requests %v, want exactly %q", tc.args, got, tc.route)
		}
	}
	// An argument beyond the table row completes nothing and sends nothing.
	before := len(cs.seen())
	if out := runComplete(t, "runs", "cancel", "run-1", ""); len(candidates(out)) != 0 || len(cs.seen()) != before {
		t.Errorf("second argument of runs cancel: %q", out)
	}
}

// Ids are data from a server. Nothing a shell could misread is offered, and a
// tab or newline cannot forge a description or a second candidate.
func TestCompletionDropsHostileIDs(t *testing.T) {
	body := `[` + strings.Join([]string{
		`{"id":"good","revision":1,"spec":{}}`,
		`{"id":"has space","revision":1,"spec":{}}`,
		`{"id":"tab\tforged description","revision":1,"spec":{}}`,
		`{"id":"line\nforged-second","revision":1,"spec":{}}`,
		`{"id":"-rf","revision":1,"spec":{}}`,
		`{"id":"--url=http://evil","revision":1,"spec":{}}`,
		`{"id":"$(touch pwned)","revision":1,"spec":{}}`,
		"{\"id\":\"`id`\",\"revision\":1,\"spec\":{}}",
		`{"id":"a;b","revision":1,"spec":{}}`,
		`{"id":"esc\u001b[2J","revision":1,"spec":{}}`,
		`{"id":"nul\u0000","revision":1,"spec":{}}`,
		`{"id":"fine-too","revision":1,"spec":{}}`,
	}, ",") + `]`
	serveStatus(t, 200, body)
	for _, args := range [][]string{{"mcp", "connections", "get", ""}, {"a2a", "invoke", ""}, {"resources", "delete", "tool-connection", ""}} {
		out := runComplete(t, args...)
		got := candidates(out)
		slices.Sort(got)
		if !slices.Equal(got, []string{"fine-too", "good"}) {
			t.Errorf("%v: offered %q", args, got)
		}
		if strings.ContainsAny(out, "\t\x1b\x00") {
			t.Errorf("%v: control characters in output %q", args, out)
		}
	}
}

// Completion never refreshes a stored token (that contacts the API and writes
// the credentials file) and never creates or changes anything under ~/.aiplex.
func TestCompletionWritesNothing(t *testing.T) {
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			refreshes.Add(1)
		}
		w.Write([]byte(`[]`))
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AIPLEX_URL", "")
	t.Setenv("AIPLEX_TOKEN", "")
	dir := filepath.Join(home, ".aiplex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"current_context":"c","contexts":{"c":{"name":"c","url":"` + server.URL + `"}}}`
	creds := `{"tokens":{"c":{"access_token":"old","refresh_token":"r","expires_at":"2001-01-01T00:00:00Z"}}}`
	for name, body := range map[string]string{"config.json": config, "credentials.json": creds} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runComplete(t, "access-points", "get", "")
	runComplete(t, "ctx", "")
	if n := refreshes.Load(); n != 0 {
		t.Fatalf("completion refreshed the token %d times", n)
	}
	for name, want := range map[string]string{"config.json": config, "credentials.json": creds} {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != want {
			t.Errorf("%s changed: %s", name, got)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("files appeared under ~/.aiplex: %v", entries)
	}
	// With no ~/.aiplex at all, none is created.
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	runComplete(t, "access-points", "get", "")
	runComplete(t, "ctx", "")
	if _, err := os.Stat(filepath.Join(empty, ".aiplex")); err == nil {
		t.Error("completion created ~/.aiplex")
	}
}

// Whatever a table row names, the client completion uses refuses to send
// anything but a GET.
func TestCompletionClientOnlySendsGET(t *testing.T) {
	cs := serveStatus(t, 200, `{}`)
	c := newCompletionClient()
	if err := c.DeleteAgent(context.Background(), "a1"); err == nil {
		t.Fatal("a DELETE went through the completion client")
	}
	// DeleteAgent reads the current revision first; that GET is allowed.
	for _, r := range cs.seen() {
		if !strings.HasPrefix(r, "GET ") {
			t.Fatalf("the server saw %s", r)
		}
	}
}

func TestPositionalPlaceholderSeesOptionalArguments(t *testing.T) {
	for use, want := range map[string]bool{
		"get <id>":                         true,
		"ctx [name]":                       true,
		"who [agent|user|client] [id]":     true,
		"providers [provider]":             true,
		"list [--agent <id>] [--limit N]":  false,
		"verify [--agent <agent>] --x <y>": false,
		"apply -f <file>":                  false,
		"status":                           false,
	} {
		if _, got := positionalPlaceholder(use); got != want {
			t.Errorf("%q: positional = %v, want %v", use, got, want)
		}
	}
}

// The new rows: caller kinds then caller ids, canonical principals only for
// user scopes, quota and usage ids from one bounded page, fixed lists.
func TestCompletionOfCallersQuotaAndUsageIDs(t *testing.T) {
	cs := serveCompletion(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		principal := "principal-" + strings.Repeat("a", 64)
		switch r.URL.Path {
		case "/api/v1/overview/subjects":
			if r.URL.Query().Get("kind") == "user" {
				w.Write([]byte(`{"kind":"user","subjects":[{"kind":"user","id":"` + principal + `"},{"kind":"user","id":"alice@example.com"}]}`))
				return
			}
			w.Write([]byte(`{"kind":"` + r.URL.Query().Get("kind") + `","subjects":[{"kind":"agent","id":"tutor-agent"}]}`))
		case "/api/v1/quota/inventory":
			w.Write([]byte(`{"kind":"` + r.URL.Query().Get("kind") + `","records":[{"id":"rec-` + r.URL.Query().Get("kind") + `"}],"done":true}`))
		case "/api/v1/usage/records":
			w.Write([]byte(`{"records":[{"id":"ul_0123"}]}`))
		case "/api/v1/iam/native/bindings/":
			w.Write([]byte(`[{"id":"bind-1","revision":1,"spec":{}}]`))
		default:
			w.WriteHeader(404)
		}
	})
	principal := "principal-" + strings.Repeat("a", 64)
	cases := []struct {
		args  []string
		want  []string
		route string
	}{
		{[]string{"who", ""}, []string{"agent", "user", "client"}, ""},
		{[]string{"who", "agent", ""}, []string{"tutor-agent"}, "GET /api/v1/overview/subjects"},
		{[]string{"agents", "user-scopes", "set", ""}, []string{principal}, "GET /api/v1/overview/subjects"},
		{[]string{"quota", "loan", ""}, []string{"rec-loans"}, "GET /api/v1/quota/inventory"},
		{[]string{"quota", "evidence", ""}, []string{"rec-terminal-work"}, "GET /api/v1/quota/inventory"},
		{[]string{"quota", "inventory", ""}, []string{"loans", "terminal-work"}, ""},
		{[]string{"usage", "get", ""}, []string{"ul_0123"}, "GET /api/v1/usage/records"},
		{[]string{"native", "governance", "plan", ""}, []string{"bind-1"}, "GET /api/v1/iam/native/bindings/"},
		{[]string{"llm", "providers", ""}, []string{"bedrock"}, ""},
	}
	for _, tc := range cases {
		before := len(cs.seen())
		got := candidates(runComplete(t, tc.args...))
		for _, w := range tc.want {
			if !slices.Contains(got, w) {
				t.Errorf("%v: want %q in %q", tc.args, w, got)
			}
		}
		if slices.Contains(got, "alice@example.com") {
			t.Errorf("%v: offered a non-canonical principal", tc.args)
		}
		reqs := cs.seen()[before:]
		if tc.route == "" && len(reqs) != 0 || tc.route != "" && (len(reqs) != 1 || reqs[0] != tc.route) {
			t.Errorf("%v: requests %v, want %q", tc.args, reqs, tc.route)
		}
	}
}

// A stored token that is known to be expired is not sent: nothing is offered
// and no request is made, even from a server that would accept it. Flag values
// go through the same client, and nothing is written.
func TestCompletionWithAnExpiredTokenSendsNothing(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte(`[{"id":"leaked","revision":1,"spec":{}}]`))
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AIPLEX_URL", "")
	t.Setenv("AIPLEX_TOKEN", "")
	dir := filepath.Join(home, ".aiplex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"current_context":"c","contexts":{"c":{"name":"c","url":"` + server.URL + `"}}}`
	creds := `{"tokens":{"c":{"access_token":"old","refresh_token":"r","expires_at":"2001-01-01T00:00:00Z"}}}`
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600)
	os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(creds), 0o600)
	for _, args := range [][]string{
		{"access-points", "get", ""},
		{"access-points", "create", "x", "--tool", ""},
		{"budgets", "preview", "--access-point", ""},
		{"mcp", "connections", "get", ""},
	} {
		if got := candidates(runComplete(t, args...)); len(got) != 0 {
			t.Errorf("%v: offered %q for an expired login", args, got)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests were made with an expired token", n)
	}
	for name, want := range map[string]string{"config.json": config, "credentials.json": creds} {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != want {
			t.Errorf("%s changed: %s", name, got)
		}
	}
	// A token with no known expiry is still sent.
	os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(`{"tokens":{"c":{"access_token":"fresh"}}}`), 0o600)
	if got := candidates(runComplete(t, "access-points", "get", "")); requests.Load() != 1 || len(got) != 0 && !slices.Contains(got, "leaked") {
		t.Errorf("a token without expiry: requests=%d got=%q", requests.Load(), got)
	}
}

// Flag values complete through the same read-only client: no refresh, no
// ~/.aiplex created.
func TestFlagCompletionWritesNothing(t *testing.T) {
	cs := serveStatus(t, 200, `[{"id":"local","revision":1,"spec":{}}]`)
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, args := range [][]string{
		{"access-points", "create", "x", "--tool", ""},
		{"budgets", "preview", "--access-point", ""},
		{"--context", ""},
		{"ctx", ""},
	} {
		runComplete(t, args...)
	}
	if _, err := os.Stat(filepath.Join(home, ".aiplex")); err == nil {
		t.Error("flag completion created ~/.aiplex")
	}
	for _, r := range cs.seen() {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("flag completion sent %s", r)
		}
	}
}
