//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// Azure SDK path shapes with the api-key header, model aliases on OpenAI and
// Anthropic JSON bodies, usage filters (--error-class) and details.field on
// access-point validation errors, through the real binaries.
func TestAzureShapesAliasesFiltersAndFieldErrors(t *testing.T) {
	e := modelEnv(t)
	tok := e.stack.Token

	t.Run("azure shapes with api-key", func(t *testing.T) {
		r := e.do("POST", "/ap/models/openai/deployments/gpt-e2e/chat/completions?api-version=2024-10-21", `{"messages":[{"role":"user","content":"azure deployment"}]}`, "api-key", tok, "Content-Type", "application/json")
		if r.Status != 200 {
			t.Fatalf("deployment shape: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "azure deployment", "reply")
		if up := lastUpstream(t, e); up.Model != "gpt-e2e" || up.Authorization != "Bearer "+openAIKey {
			t.Fatalf("upstream model %q auth %q", up.Model, up.Authorization)
		}
		r = e.do("POST", "/ap/models/openai/v1/chat/completions", `{"model":"gpt-e2e","messages":[{"role":"user","content":"azure v1"}]}`, "api-key", tok, "Content-Type", "application/json")
		if r.Status != 200 {
			t.Fatalf("v1 shape: %d %s", r.Status, r.Body)
		}
		r = e.do("POST", "/ap/models/openai/deployments/embed-e2e/embeddings", `{"input":"vec"}`, "api-key", tok, "Content-Type", "application/json")
		if r.Status != 200 {
			t.Fatalf("deployment embeddings: %d %s", r.Status, r.Body)
		}
		r = e.do("POST", "/ap/models/openai/deployments/gpt-e2e/chat/completions", `{"messages":[]}`, "api-key", "not-a-token", "Content-Type", "application/json")
		if r.Status != 401 {
			t.Fatalf("bad api-key: %d %s", r.Status, r.Body)
		}
	})

	t.Run("aliases", func(t *testing.T) {
		ap := `id: aliases
caller_kinds: [user]
models:
  - {name: fast, surface_ref: oai, native_model: gpt-e2e}
  - {name: smart, surface_ref: ant, native_model: claude-e2e}
`
		e.mustCLI("access-points", "apply", "-f", writeFile(t, "aliases.yaml", ap))
		r := e.post("/ap/aliases/v1/chat/completions", `{"model":"fast","messages":[{"role":"user","content":"alias"}]}`)
		if r.Status != 200 {
			t.Fatalf("openai alias: %d %s", r.Status, r.Body)
		}
		if up := lastUpstream(t, e); up.Model != "gpt-e2e" {
			t.Fatalf("upstream saw model %q, want the native gpt-e2e", up.Model)
		}
		r = e.post("/ap/aliases/v1/messages", `{"model":"smart","max_tokens":8,"messages":[{"role":"user","content":"alias"}]}`, "anthropic-version", "2023-06-01")
		if r.Status != 200 {
			t.Fatalf("anthropic alias: %d %s", r.Status, r.Body)
		}
		if up := lastUpstream(t, e); up.Model != "claude-e2e" {
			t.Fatalf("upstream saw model %q, want claude-e2e", up.Model)
		}
	})

	t.Run("usage --error-class", func(t *testing.T) {
		if r := e.post("/ap/models/v1/chat/completions", `{"model":"gpt-ratelimit","messages":[{"role":"user","content":"x"}]}`); r.Status != 429 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		r := e.mustCLI("usage", "records", "--error-class", "rate_limited", "-o", "json")
		var page struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.Unmarshal([]byte(r.Out), &page); err != nil || len(page.Records) == 0 {
			t.Fatalf("filtered records: %v %s", err, r.Out)
		}
		for _, rec := range page.Records {
			if rec["error_class"] != "rate_limited" {
				t.Fatalf("filter returned %v", rec)
			}
		}
	})

	t.Run("details.field", func(t *testing.T) {
		bad := `{"id":"bad","caller_kinds":["user"],"subjects":{},"models":[{"name":"x","surface_ref":"missing","native_model":"x"}]}`
		r := e.do("PUT", "/api/v1/access-points/bad", bad, append(e.auth(), "If-Match", "0", "Content-Type", "application/json")...)
		if r.Status != 400 && r.Status != 422 {
			t.Fatalf("invalid access point: %d %s", r.Status, r.Body)
		}
		if !strings.Contains(r.Body, `"field":"models/0/surface_ref"`) {
			t.Fatalf("validation error lacks details.field: %s", r.Body)
		}
		c := e.cli("access-points", "apply", "-f", writeFile(t, "bad.json", bad))
		if c.Code == 0 {
			t.Fatalf("CLI applied an invalid access point: %s", c)
		}
		contains(t, c.Err, "missing", "CLI names the unresolved reference")
	})
}
