//go:build e2e

package e2e

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// docBlock returns the first ```sh block after marker in a guide.
func docBlock(t *testing.T, doc, marker string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoDir, "docs", doc))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(raw), marker)
	if !ok {
		t.Fatalf("%q not found in docs/%s", marker, doc)
	}
	_, block, ok := strings.Cut(after, "```sh\n")
	if !ok {
		t.Fatalf("no sh block after %q in docs/%s", marker, doc)
	}
	block, _, _ = strings.Cut(block, "```")
	return block
}

// childEnv reads one variable from the environment of a direct child of pid.
func childEnv(t *testing.T, pid int, name string) string {
	t.Helper()
	entries, _ := os.ReadDir("/proc")
	for _, ent := range entries {
		child, err := strconv.Atoi(ent.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", ent.Name(), "stat"))
		if err != nil {
			continue
		}
		rest := string(stat[strings.LastIndexByte(string(stat), ')')+2:])
		if f := strings.Fields(rest); len(f) < 2 || f[1] != strconv.Itoa(pid) {
			continue
		}
		env, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(child), "environ"))
		if err != nil {
			continue
		}
		for _, kv := range strings.Split(string(env), "\x00") {
			if v, ok := strings.CutPrefix(kv, name+"="); ok {
				return v
			}
		}
	}
	return ""
}

// The commands of the guide's agent section run as written against the real
// `aiplex dev`, the banner and status name the agent, and the agent's token is in
// no file, output or log a user can reach.
func TestDevDocumentedAgentSteps(t *testing.T) {
	sh, run, home := realDev(t)
	out, code := sh(docBlock(t, "local-development.md", "Try it, in another terminal"))
	if code != 0 {
		t.Fatalf("documented agent steps failed (%d):\n%s", code, out)
	}
	for _, want := range []string{"local-agent", "mock-agent: you said hello", "local-agent (query)", "research-agent"} {
		contains(t, out, want, "output of the documented agent steps")
	}

	// Skills and deployed agent instances are empty, as the guide says.
	skills, skillsErr, c := run(nil, "skills", "servers")
	if c != 0 || !strings.Contains(skills, "0 skill server(s)") {
		t.Fatalf("skills servers: %d %s", c, skills)
	}
	contains(t, skillsErr, "need a cluster", "empty-list hint for a loopback API")
	if _, errJSON, _ := run(nil, "skills", "servers", "-o", "json"); strings.Contains(errJSON, "need a cluster") {
		t.Fatalf("hint printed for -o json: %s", errJSON)
	}
	inv, _, c := run(nil, "skills", "invocations", "-o", "json")
	if c != 0 || strings.TrimSpace(inv) != "[]" {
		t.Fatalf("skills invocations: %d %s", c, inv)
	}
	agents, agentsErr, c := run(nil, "a2a", "agents")
	if c != 0 || !strings.Contains(agents, "0 A2A agent(s)") {
		t.Fatalf("a2a agents: %d %s", c, agents)
	}
	contains(t, agentsErr, "need a cluster", "empty-list hint for a loopback API")

	statusJSON, _, _ := run(nil, "dev", "status", "-o", "json")
	var st struct {
		State struct {
			PID int `json:"pid"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(statusJSON), &st); err != nil || st.State.PID == 0 {
		t.Fatalf("status: %v %s", err, statusJSON)
	}
	token := childEnv(t, st.State.PID, "AIPLEX_DEV_AGENT_TOKEN")
	if token == "" {
		t.Skip("cannot read the API process's environment on this host")
	}
	if len(token) < 20 {
		t.Fatalf("agent token %q is not a generated token", token)
	}
	envOut, _, _ := run(nil, "dev", "env")
	statusText, _, _ := run(nil, "dev", "status")
	notContains(t, out+envOut+statusText+statusJSON, token, "output")
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), token) {
			t.Errorf("the agent token is written to %s", path)
		}
		return nil
	})
	// The connections file the stack generates is in the dev stack's own
	// directory, which the API process reads: it names the variable only.
	for _, dir := range []string{os.TempDir()} {
		matches, _ := filepath.Glob(filepath.Join(dir, "aiplex-dev-*", "*"))
		for _, m := range matches {
			if data, err := os.ReadFile(m); err == nil && strings.Contains(string(data), token) {
				t.Errorf("the agent token is written to %s", m)
			}
		}
	}
}
