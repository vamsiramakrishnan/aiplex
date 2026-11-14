package api

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
)

// Which responses have a ledger record under their X-Aiplex-Request-Id. A
// refusal before admission has no verified tenant, so it writes none; a refusal
// after admission that reaches a managed handler, or the model lookup, writes
// one (docs/access-points.md, "Response headers").
func TestWhichRefusalsHaveALedgerRecord(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(f *headerFixture)
		path    string
		body    string
		code    string
		record  bool
	}{
		{"success", nil, "/ap/r/v1/chat/completions", hello, "", true},
		{"unknown model", nil, "/ap/r/v1/chat/completions", `{"model":"nope","messages":[]}`, "MODEL_NOT_FOUND", true},
		{"not authenticated", func(f *headerFixture) { f.as(nil) }, "/ap/r/v1/chat/completions", hello, "UNAUTHENTICATED", false},
		{"access point not found", nil, "/ap/missing/v1/chat/completions", hello, "NOT_FOUND", false},
		{"caller not admitted", func(f *headerFixture) {
			agent := apUser("robot")
			agent.IsWorkforce = false
			f.as(agent, "llm:model:*")
		}, "/ap/r/v1/chat/completions", hello, "CALLER_NOT_ALLOWED", false},
		{"body is not JSON", nil, "/ap/r/v1/chat/completions", `nope`, "INVALID_REQUEST", false},
		{"wrong wire for an unrouted model", nil, "/ap/r/v1/messages", `{"model":"m"}`, "PROTOCOL_MISMATCH", false},
		{"body too large", nil, "/ap/r/v1/chat/completions", `{"model":"m","x":"` + strings.Repeat("a", 8<<20) + `"}`, "REQUEST_TOO_LARGE", false},
		{"route not served", nil, "/ap/r/v1/nothing", `{}`, "NOT_FOUND", false},
		{"routed model, wrong wire", func(f *headerFixture) { f.model(AccessPointModel{Name: "m", Backends: backends("a/m")}) }, "/ap/r/v1/messages", `{"model":"m"}`, "PROTOCOL_MISMATCH", true},
		{"missing grants", func(f *headerFixture) { f.as(apUser("alice"), "llm:model:*") }, "/ap/r/v1/chat/completions", hello, "ACTION_DENIED", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newHeaderFixture(t)
			f.model(AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m"})
			if c.prepare != nil {
				c.prepare(f)
			}
			w := f.do(c.path, c.body)
			if errCodeOf(w) != c.code {
				t.Fatalf("%s = %q, want %q (%d %s)", errorcode.Header, errCodeOf(w), c.code, w.Code, w.Body)
			}
			id := w.Header().Get("X-Aiplex-Request-Id")
			found := 0
			for _, r := range f.records() {
				if r.RequestID == id {
					found++
				}
			}
			if (found > 0) != c.record {
				t.Fatalf("records for %s: %d, want record=%v", id, found, c.record)
			}
			if strings.TrimSpace(id) == "" {
				t.Fatal("no request id")
			}
		})
	}
}
