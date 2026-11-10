//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

type connectOut struct {
	BaseURLs  map[string]string `json:"base_urls"`
	Endpoints []struct {
		Protocol string   `json:"protocol"`
		URL      string   `json:"url"`
		Models   []string `json:"models"`
	} `json:"endpoints"`
	Models []struct {
		Name      string   `json:"name"`
		Protocols []string `json:"protocols"`
	} `json:"models"`
	Snippets []struct {
		Title    string `json:"title"`
		Language string `json:"language"`
		Content  string `json:"content"`
	} `json:"snippets"`
}

// 5. Overview and connect: who, connect for every client profile, and the
// generic snippet's curl run verbatim.
func TestWhoAndConnect(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}

	r := e.mustCLI("connect", "local", "-o", "json")
	var c connectOut
	if err := json.Unmarshal([]byte(r.Out), &c); err != nil {
		t.Fatalf("connect json: %v\n%s", err, r.Out)
	}

	t.Run("endpoints are servable", func(t *testing.T) {
		// The seeded surface installs only chat completions; connect must not
		// advertise image or audio endpoints that answer "not supported".
		for _, m := range c.Models {
			for _, p := range m.Protocols {
				if p != "openai-chat-completions" {
					t.Fatalf("model %s advertised on %s, which its surface does not install: %+v", m.Name, p, c.Models)
				}
			}
		}
		for _, ep := range c.Endpoints {
			t.Fatalf("connect advertises an endpoint the access point cannot serve: %+v", ep)
		}
	})

	t.Run("generic curl snippet runs verbatim", func(t *testing.T) {
		var snippet string
		for _, s := range c.Snippets {
			if s.Title == "curl: OpenAI chat completions" {
				snippet = s.Content
			}
		}
		if snippet == "" {
			t.Fatalf("no chat curl snippet: %+v", c.Snippets)
		}
		cmd := exec.Command("bash", "-c", snippet)
		cmd.Env = append(cleanEnv(), "AIPLEX_TOKEN="+e.stack.Token)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("snippet failed: %v\n%s\n%s", err, snippet, out)
		}
		contains(t, string(out), "Hello from the AIPlex mock model", "snippet response")
		// The MCP catalog snippet too.
		for _, s := range c.Snippets {
			if strings.HasPrefix(s.Title, "curl: list the MCP server catalog") {
				cmd := exec.Command("bash", "-c", s.Content)
				cmd.Env = append(cleanEnv(), "AIPLEX_TOKEN="+e.stack.Token)
				out, err := cmd.CombinedOutput()
				if err != nil || !strings.Contains(string(out), "local-tools") {
					t.Fatalf("MCP catalog snippet: %v\n%s", err, out)
				}
			}
		}
	})

	t.Run("every client profile", func(t *testing.T) {
		for _, profile := range []string{"generic", "antigravity", "antigravity-acp", "codex", "claude-code", "claude-desktop"} {
			r := e.cli("connect", "local", "--client", profile)
			switch {
			case r.Code == 0:
				contains(t, r.Out, "/ap/local", profile+" output")
				notContains(t, r.Out, e.stack.Token, profile+" output leaks the token")
			case strings.Contains(r.Err, "CLIENT_NOT_APPLICABLE") || strings.Contains(r.Err, "does not serve") || strings.Contains(r.Err, "this access point offers"):
				contains(t, r.Err, "openai-chat", profile+" refusal names what the access point offers")
			default:
				t.Fatalf("connect --client %s: %s", profile, r)
			}
		}
		r := e.cli("connect", "local", "--client", "nosuch")
		if r.Code == 0 || !strings.Contains(r.Err, "must be one of") {
			t.Fatalf("unknown profile: %s", r)
		}
	})

	t.Run("who", func(t *testing.T) {
		r := e.mustCLI("who", "--kind", "user")
		contains(t, r.Out, "principal-", "who --kind user lists the developer after the snippet call")
		r = e.mustCLI("who", "--kind", "user", "-o", "json")
		var page struct {
			Subjects []struct {
				ID string `json:"id"`
			} `json:"subjects"`
		}
		if err := json.Unmarshal([]byte(r.Out), &page); err != nil || len(page.Subjects) == 0 {
			t.Fatalf("who json: %v %s", err, r.Out)
		}
		r = e.mustCLI("who", "user", page.Subjects[0].ID)
		contains(t, r.Out, "local", "who user <id> shows the access point")
		r = e.cli("who", "agent", "nobody")
		if r.Code == 0 {
			t.Fatalf("unknown agent: %s", r)
		}
	})
}
