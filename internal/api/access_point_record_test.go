package api

import (
	"net/http/httptest"
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
		{"provider error relayed", func(f *headerFixture) {
			f.model(AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m-fault-s429"})
		}, "/ap/r/v1/chat/completions", hello, "", true},
		{"missing grants", func(f *headerFixture) { f.as(apUser("alice"), "llm:model:*") }, "/ap/r/v1/chat/completions", hello, "ACTION_DENIED", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newHeaderFixture(t)
			f.model(AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m"})
			if c.prepare != nil {
				c.prepare(f)
			}
			tags, _ := NewTagPolicy([]string{"by_branch", "by_turn", "by_harness"}, 0)
			f.h.Tags = tags
			r := httptest.NewRequest("POST", c.path, strings.NewReader(c.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-AIPlex-Tags", "by_branch=smoke,by_turn=1,by_harness=gemini-cli")
			r.Header.Set("X-Client-Request-Id", "by-test-1")
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			if errCodeOf(w) != c.code {
				t.Fatalf("%s = %q, want %q (%d %s)", errorcode.Header, errCodeOf(w), c.code, w.Code, w.Body)
			}
			id := w.Header().Get("X-Aiplex-Request-Id")
			found := 0
			for _, rec := range f.records() {
				if rec.RequestID != id {
					continue
				}
				found++
				// The record of a call carries what a successful call's record does.
				want := map[string]string{"by_branch": "smoke", "by_turn": "1", "by_harness": "gemini-cli"}
				if len(rec.Tags) != 3 || rec.Tags["by_branch"] != want["by_branch"] || rec.Tags["by_turn"] != "1" || rec.Tags["by_harness"] != "gemini-cli" || rec.ClientRequestID != "by-test-1" || rec.AccessPointID != "r" || rec.CallerKind != CallerUser {
					t.Errorf("record of %s lacks the call's fields: tags=%v client=%q ap=%q kind=%q", c.name, rec.Tags, rec.ClientRequestID, rec.AccessPointID, rec.CallerKind)
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

// A refusal that is recorded after the model is chosen, by quota or on the
// managed tool path, carries the call's tags and client request id too.
func TestManagedPathRecordsCarryTheCallsTags(t *testing.T) {
	f := newAPFixture(t)
	tags, _ := NewTagPolicy([]string{"by_branch"}, 0)
	f.h.Tags = tags
	records := attachLedger(t, f.h)
	headers := []string{"X-AIPlex-Tags", "by_branch=smoke", "X-Client-Request-Id", "by-test-2"}
	if w := mcpPost(f, "/ap/ide/mcp", apSearchCall, headers...); !strings.Contains(w.Body.String(), "github:search") {
		t.Fatalf("tool call: %s", w.Body)
	}
	setQoS(f, &AccessPointQoS{Class: "interactive", Limits: &AccessPointLimit{Concurrency: 2}})
	withAPQuota(t, f, nil)
	records = attachLedger(t, f.h)
	if w := f.do("POST", "/ap/ide/v1/chat/completions", `{"model":"chat-model","messages":[{"role":"user","content":"x"}]}`, append([]string{"Content-Type", "application/json"}, headers...)...); w.Code < 400 {
		t.Fatalf("expected a quota refusal: %d %s", w.Code, w.Body)
	}
	got := records()
	if len(got) == 0 {
		t.Fatal("no records")
	}
	for _, rec := range got {
		if rec.Tags["by_branch"] != "smoke" || rec.ClientRequestID != "by-test-2" || rec.AccessPointID != "ide" || rec.CallerKind == "" {
			t.Errorf("record %s (%s %s) lacks the call's fields: %+v", rec.ID, rec.Plane, rec.ErrorClass, rec)
		}
	}
}
