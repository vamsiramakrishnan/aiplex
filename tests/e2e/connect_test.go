//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
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
		// The seeded surfaces install only their own chat/messages/responses/
		// generateContent operation; connect must not advertise image or audio
		// endpoints that answer "not supported".
		want := map[string]string{"mock-gpt": "openai-chat-completions", "mock-responses": "openai-responses", "mock-claude": "anthropic-messages", "mock-gemini": "google-genai"}
		got := map[string]bool{}
		for _, m := range c.Models {
			wire, ok := want[m.Name]
			if !ok {
				t.Fatalf("unexpected model %q", m.Name)
			}
			if len(m.Protocols) != 1 || m.Protocols[0] != wire {
				t.Fatalf("model %s protocols = %v, want only %s", m.Name, m.Protocols, wire)
			}
			got[m.Name] = true
		}
		if len(got) != len(want) {
			t.Fatalf("models = %+v, want %v", c.Models, want)
		}
		for _, ep := range c.Endpoints {
			t.Fatalf("connect advertises an endpoint the access point cannot serve: %+v", ep)
		}
	})

	// Every generic snippet that calls a model runs verbatim against the
	// real gateway path, and the answer is the mock's.
	t.Run("generic curl snippets run verbatim", func(t *testing.T) {
		run := func(title string) string {
			t.Helper()
			for _, s := range c.Snippets {
				if s.Title == title {
					cmd := exec.Command("bash", "-c", s.Content)
					cmd.Env = append(cleanEnv(), "AIPLEX_TOKEN="+e.stack.Token)
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("%s failed: %v\n%s\n%s", title, err, s.Content, out)
					}
					return string(out)
				}
			}
			t.Fatalf("no snippet %q in %+v", title, c.Snippets)
			return ""
		}
		for _, title := range []string{"curl: OpenAI chat completions", "curl: OpenAI responses", "curl: Anthropic messages", "curl: Google GenAI generateContent"} {
			contains(t, run(title), "Hello from the AIPlex mock model", title)
		}
		out := run("curl: list the MCP server catalog")
		contains(t, out, "local-tools", "MCP catalog snippet")
	})

	// Each wire also streams (SSE) through the gateway, and the ledger
	// records known usage and an estimated cost for every one.
	t.Run("streaming on every wire", func(t *testing.T) {
		cases := []struct{ name, path, body, want string }{
			{"chat", "/ap/local/v1/chat/completions", `{"model":"mock-gpt","stream":true,"messages":[{"role":"user","content":"hi"}]}`, "data: [DONE]"},
			{"responses", "/ap/local/v1/responses", `{"model":"mock-responses","stream":true,"input":"hi"}`, "response.completed"},
			{"messages", "/ap/local/v1/messages", `{"model":"mock-claude","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, "message_stop"},
			{"gemini", "/ap/local/v1beta/models/mock-gemini:streamGenerateContent?alt=sse", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, "usageMetadata"},
		}
		for _, tc := range cases {
			resp := e.post(tc.path, tc.body, "anthropic-version", "2023-06-01")
			if resp.Status != 200 {
				t.Fatalf("%s stream: %d %s", tc.name, resp.Status, resp.Body)
			}
			contains(t, resp.Body, tc.want, tc.name+" stream")
			contains(t, resp.Body, "AIPlex", tc.name+" stream text")
		}
		eventually(t, 10*time.Second, func() error {
			r := e.mustCLI("usage", "records", "-o", "json")
			var page struct {
				Records []struct {
					Status string `json:"status"`
					Cost   struct {
						Basis string `json:"basis"`
					} `json:"cost"`
					Usage struct {
						Input  *int `json:"input_tokens"`
						Output *int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"records"`
			}
			if err := json.Unmarshal([]byte(r.Out), &page); err != nil {
				return err
			}
			if len(page.Records) < 8 {
				return fmt.Errorf("%d usage records, want at least 8", len(page.Records))
			}
			for _, rec := range page.Records {
				if rec.Status != "ok" || rec.Cost.Basis != "estimated" || rec.Usage.Input == nil || rec.Usage.Output == nil {
					return fmt.Errorf("record without known usage and estimated cost: %+v", rec)
				}
			}
			return nil
		})
	})

	t.Run("every client profile", func(t *testing.T) {
		// Every profile connect offers renders a usable configuration for the
		// seeded access point, with the mock model of its wire.
		wantModel := map[string]string{"antigravity": "mock-gemini", "antigravity-acp": "mock-gemini", "codex": "mock-responses", "claude-code": "mock-claude"}
		for _, profile := range []string{"generic", "antigravity", "antigravity-acp", "codex", "claude-code"} {
			r := e.mustCLI("connect", "local", "--client", profile)
			contains(t, r.Out, "/ap/local", profile+" output")
			notContains(t, r.Out, e.stack.Token, profile+" output leaks the token")
			notContains(t, r.Out, "could not render", profile+" output falls back to generic")
			if m := wantModel[profile]; m != "" {
				contains(t, r.Out, m, profile+" output names its wire's model")
			}
		}
		// A profile that needs a Claude apps gateway server is refused with the
		// reason, and is not offered in the list of known profiles.
		r := e.cli("connect", "local", "--client", "claude-desktop")
		if r.Code == 0 || !strings.Contains(r.Err, "claude-code") {
			t.Fatalf("claude-desktop: %s", r)
		}
		r = e.cli("connect", "local", "--client", "nosuch")
		if r.Code == 0 || !strings.Contains(r.Err, "must be one of") || strings.Contains(r.Err, "claude-desktop") {
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
