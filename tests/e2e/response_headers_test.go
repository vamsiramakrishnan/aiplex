//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// A proxy in front of an access point (Branchyard's model gateway) joins its
// own record of a call to AIPlex's ledger record with X-Client-Request-Id, and
// tells AIPlex's own refusal from a provider's relayed error with
// X-Aiplex-Error-Code (docs/access-points.md, "Response headers").

type correlatedRecord struct {
	ID              string            `json:"id"`
	RequestID       string            `json:"request_id"`
	ClientRequestID string            `json:"client_request_id"`
	TraceID         string            `json:"trace_id"`
	Tags            map[string]string `json:"tags"`
	AccessPointID   string            `json:"access_point_id"`
}

// findRecords runs "aiplex usage records" with filters and waits until the
// ledger holds a match (the ledger is written after the response).
func findRecords(t *testing.T, e *env, want int, args ...string) []correlatedRecord {
	t.Helper()
	var out []correlatedRecord
	eventually(t, 10*time.Second, func() error {
		r := e.mustCLI(append([]string{"usage", "records", "-o", "json"}, args...)...)
		var page struct {
			Records []correlatedRecord `json:"records"`
		}
		if err := json.Unmarshal([]byte(r.Out), &page); err != nil {
			t.Fatalf("records json: %v\n%s", err, r.Out)
		}
		if len(page.Records) != want {
			return fmt.Errorf("ledger holds %d matching records, want %d", len(page.Records), want)
		}
		out = page.Records
		return nil
	})
	return out
}

func TestResponseHeadersFindTheLedgerRecordOfACall(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatalf("seed: %v\n%s", err, e.log.String())
	}
	chat := `{"model":"mock-gpt","messages":[{"role":"user","content":"hello there"}]}`
	r := e.post("/ap/local/v1/chat/completions", chat,
		"X-Client-Request-Id", "by-test-123",
		"traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"X-AIPlex-Tags", "by_branch=repo/feat-x,by_turn=turn-1,by_harness=claude-code")
	if r.Status != 200 {
		t.Fatalf("chat: %d %s", r.Status, r.Body)
	}
	requestID := r.Header.Get("X-Aiplex-Request-Id")
	if requestID == "" || r.Header.Get("X-Aiplex-Error-Code") != "" {
		t.Fatalf("headers of a success: %v", r.Header)
	}
	byClient := findRecords(t, e, 1, "--client-request-id", "by-test-123")
	byRequest := findRecords(t, e, 1, "--request-id", requestID)
	if byClient[0].ID != byRequest[0].ID || byClient[0].RequestID != requestID || byClient[0].ClientRequestID != "by-test-123" {
		t.Fatalf("the two filters must find the same record: %+v %+v (X-Aiplex-Request-Id %s)", byClient[0], byRequest[0], requestID)
	}
	// The three tag keys of the contract are kept, because the dev API allows them.
	want := map[string]string{"by_branch": "repo/feat-x", "by_turn": "turn-1", "by_harness": "claude-code"}
	for k, v := range want {
		if byClient[0].Tags[k] != v {
			t.Fatalf("tag %s = %q, want %q (tags %v)", k, byClient[0].Tags[k], v, byClient[0].Tags)
		}
	}
	const trace = "0af7651916cd43dd8448eb211c80319c"
	if byClient[0].TraceID != trace {
		t.Fatalf("the record continues the caller's trace: %q, want %s", byClient[0].TraceID, trace)
	}
	if got := findRecords(t, e, 1, "--trace-id", trace); got[0].ID != byClient[0].ID {
		t.Fatalf("--trace-id found %+v", got)
	}
	// The same record through the API, and a malformed value is refused.
	api := e.do("GET", "/api/v1/usage/records?client_request_id=by-test-123", "", e.auth()...)
	if api.Status != 200 || !strings.Contains(api.Body, byClient[0].ID) {
		t.Fatalf("api: %d %s", api.Status, api.Body)
	}
	if bad := e.do("GET", "/api/v1/usage/records?client_request_id=has%20space", "", e.auth()...); bad.Status != 400 || !strings.Contains(bad.Body, "INVALID_QUERY") {
		t.Fatalf("malformed filter: %d %s", bad.Status, bad.Body)
	}

	// A request id the caller chose is the request id of the record, and a
	// streamed response carries it too.
	r = e.post("/ap/local/v1/chat/completions", `{"model":"mock-gpt","stream":true,"messages":[{"role":"user","content":"stream"}]}`, "X-Request-Id", "caller-rid-77")
	if r.Status != 200 || r.Header.Get("X-Aiplex-Request-Id") != "caller-rid-77" || !strings.Contains(r.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("streamed: %d %v", r.Status, r.Header)
	}
	if got := findRecords(t, e, 1, "--request-id", "caller-rid-77"); got[0].AccessPointID != "local" {
		t.Fatalf("%+v", got)
	}

	// A call refused before any model is called has its own record and id.
	r = e.post("/ap/local/v1/chat/completions", `{"model":"no-such-model","messages":[]}`, "X-Client-Request-Id", "by-test-404", "X-AIPlex-Tags", "by_branch=repo/feat-x,by_turn=turn-2,by_harness=claude-code")
	if r.Status != 404 || r.Header.Get("X-Aiplex-Error-Code") != "MODEL_NOT_FOUND" || r.Header.Get("X-Aiplex-Request-Id") == "" {
		t.Fatalf("unknown model: %d %v %s", r.Status, r.Header, r.Body)
	}
	refused := findRecords(t, e, 1, "--request-id", r.Header.Get("X-Aiplex-Request-Id"))
	if refused[0].ClientRequestID != "by-test-404" || refused[0].Tags["by_branch"] != "repo/feat-x" || refused[0].Tags["by_turn"] != "turn-2" || refused[0].Tags["by_harness"] != "claude-code" {
		t.Fatalf("the refused call's record lacks its client request id or tags: %+v", refused[0])
	}

	// A refusal of the gate has the code, and no record.
	r = e.do("POST", "/ap/local/v1/chat/completions", chat)
	if r.Status != 401 || r.Header.Get("X-Aiplex-Error-Code") != "UNAUTHENTICATED" || r.Header.Get("X-Aiplex-Request-Id") == "" {
		t.Fatalf("no credential: %d %v", r.Status, r.Header)
	}
	if r = e.post("/ap/local/v1/nothing", `{}`); r.Status != 404 || r.Header.Get("X-Aiplex-Error-Code") != "NOT_FOUND" {
		t.Fatalf("unserved route: %d %v", r.Status, r.Header)
	}
}

// A provider's own error is relayed without the code; AIPlex's quota refusal
// has it.
func TestRelayedProviderErrorHasNoCodeAndQuotaRefusalHasOne(t *testing.T) {
	e := modelEnv(t)
	r := e.post("/ap/models/v1/chat/completions", `{"model":"gpt-ratelimit","messages":[{"role":"user","content":"x"}]}`)
	if r.Status != 429 || r.Header.Get("X-Aiplex-Error-Code") != "" || r.Header.Get("X-Aiplex-Request-Id") == "" {
		t.Fatalf("relayed provider 429: %d %v %s", r.Status, r.Header, r.Body)
	}
}

func TestQuotaRefusalCarriesAIPlexErrorCode(t *testing.T) {
	e, caller := budgetEnv(t)
	token := caller("dana", "eng")
	id := auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, "dana")
	e.mustCLI("agents", "user-scopes", "set", id, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*")
	e.mustCLI("budgets", "create", "eng", "--unit", "requests", "--amount", "1", "--window", "day", "--group", "eng")
	chat := func() httpResp {
		return e.do("POST", "/ap/local/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"hi"}]}`, "Authorization", "Bearer "+token, "Content-Type", "application/json")
	}
	if r := chat(); r.Status != 200 || r.Header.Get("X-Aiplex-Error-Code") != "" {
		t.Fatalf("within the budget: %d %v", r.Status, r.Header)
	}
	r := chat()
	code := r.Header.Get("X-Aiplex-Error-Code")
	if r.Status != 429 || !strings.HasPrefix(code, "QUOTA_") || r.Header.Get("X-Aiplex-Request-Id") == "" {
		t.Fatalf("budget refusal: %d %v %s", r.Status, r.Header, r.Body)
	}
	t.Logf("quota refusal code: %s", code)
}
