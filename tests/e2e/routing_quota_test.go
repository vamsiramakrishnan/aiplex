//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// A quota (budget) denial on backend A is final by default: quota limits the
// caller, so B is not tried. With fallback.on_quota_denied: next_backend the
// router moves on to B, which takes its own admission.
func TestRoutingQuotaDenialIsFinalByDefault(t *testing.T) {
	e, caller := budgetEnv(t)
	token := caller("dana", "eng")
	id := auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, "dana")
	e.mustCLI("agents", "user-scopes", "set", id, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*,llm:surface:"+devstack.SeedSurfaceB+":*")
	e.mustCLI("budgets", "create", "eng", "--unit", "requests", "--amount", "1", "--window", "day", "--group", "eng")
	apply := func(onQuota string) {
		routing := ""
		if onQuota != "" {
			routing = "\n    routing: {fallback: {on_quota_denied: " + onQuota + "}}"
		}
		e.mustCLI("access-points", "apply", "-f", writeFile(t, "rq.yaml", `id: rq
caller_kinds: [user]
models:
  - name: mock-gpt
    backends: [{surface_ref: local-models, native_model: mock-gpt}, {surface_ref: `+devstack.SeedSurfaceB+`, native_model: mock-gpt}]`+routing+"\n"))
	}
	chat := func() httpResp {
		return e.do("POST", "/ap/rq/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"hi"}]}`, "Authorization", "Bearer "+token, "Content-Type", "application/json")
	}
	apply("")
	if r := chat(); r.Status != 200 {
		t.Fatalf("first request within the budget: %d %s", r.Status, r.Body)
	}
	beforeB := len(e.stack.ModelsB.Requests())
	r := chat()
	if r.Status != 429 || len(e.stack.ModelsB.Requests()) != beforeB || r.Header.Get("X-Aiplex-Attempts") != "0" {
		t.Fatalf("budget denial is final by default: %d %s (B=%d attempts=%q)", r.Status, r.Body, len(e.stack.ModelsB.Requests())-beforeB, r.Header.Get("X-Aiplex-Attempts"))
	}
	notContains(t, r.Body, devstack.SeedSurfaceB, "denial names no backend")
	apply("next_backend")
	r = chat()
	if r.Status != 429 {
		t.Fatalf("the caller's budget also refuses backend B: %d %s", r.Status, r.Body)
	}
	recs := waitRecords(t, e, "rq", 3)
	got := map[string]bool{}
	for _, rec := range recs {
		got[attemptOutcomes(rec)] = true
		if rec.Status == usageledger.StatusDenied && !strings.HasPrefix(rec.ErrorClass, "quota_") {
			t.Fatalf("denied record error class %q", rec.ErrorClass)
		}
	}
	if !got["local-models:quota_denied"] || !got["local-models:quota_denied,"+devstack.SeedSurfaceB+":quota_denied"] {
		t.Fatalf("attempt summaries: %v", got)
	}
}
