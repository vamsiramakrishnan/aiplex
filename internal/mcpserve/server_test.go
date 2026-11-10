package mcpserve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

const secret = "tok-SECRET-0123456789"

func stubServer(t *testing.T) *Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/iam/whoami", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, `{"code":"unauthenticated","message":"bad token `+secret+`"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"identity":{"principal_id":"principal-abc","email":"dev@localhost"},"roles":["admin"],"scopes":[]}`))
	})
	mux.HandleFunc("/api/v1/overview/access-points/local/connect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_point_id":"local","client":"generic","base_urls":{"openai":"http://x/v1"},"models":[{"name":"mock-gpt","protocols":["openai-chat-completions"]}],"endpoints":[]}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c := aiplex.NewClient(ts.URL)
	c.SetToken(secret)
	return &Server{Client: c, Version: "test", Secrets: []string{secret}}
}

// roundTrip sends newline-delimited requests and returns the decoded responses.
func roundTrip(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		res = append(res, m)
	}
	return res
}

func TestLegacyHandshakeListAndCall(t *testing.T) {
	s := stubServer(t)
	res := roundTrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`,
	)
	if len(res) != 3 { // the notification gets no response
		t.Fatalf("want 3 responses, got %d: %v", len(res), res)
	}
	init := res[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" {
		t.Errorf("version negotiation: %v", init["protocolVersion"])
	}
	if _, ok := init["capabilities"].(map[string]any)["tools"]; !ok {
		t.Error("tools capability missing")
	}
	listed := res[1]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, raw := range listed {
		tl := raw.(map[string]any)
		names[tl["name"].(string)] = true
		ann := tl["annotations"].(map[string]any)
		if ann["readOnlyHint"] != true || ann["destructiveHint"] != false {
			t.Errorf("%v must be annotated read-only", tl["name"])
		}
		if tl["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("%v: inputSchema must be an object schema", tl["name"])
		}
	}
	for _, want := range []string{"whoami", "list_access_points", "get_connect_config", "list_models", "usage_summary", "usage_records"} {
		if !names[want] {
			t.Errorf("tool %s not listed", want)
		}
	}
	call := res[2]["result"].(map[string]any)
	if call["isError"] != false {
		t.Fatalf("whoami failed: %v", call)
	}
	text := call["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "principal-abc") || strings.Contains(text, secret) {
		t.Errorf("unexpected whoami text: %s", text)
	}
}

func TestModernStatelessRequests(t *testing.T) {
	s := stubServer(t)
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`
	res := roundTrip(t, s,
		`{"jsonrpc":"2.0","id":"d","method":"server/discover","params":{`+meta+`}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_models","arguments":{"access_point":"local"},`+meta+`}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"1900-01-01"}}}`,
	)
	d := res[0]["result"].(map[string]any)
	if d["resultType"] != "complete" || d["supportedVersions"].([]any)[0] != "2026-07-28" {
		t.Errorf("discover: %v", d)
	}
	c := res[1]["result"].(map[string]any)
	if c["resultType"] != "complete" || c["isError"] != false {
		t.Errorf("list_models: %v", c)
	}
	if !strings.Contains(c["content"].([]any)[0].(map[string]any)["text"].(string), "mock-gpt") {
		t.Error("model missing from result")
	}
	e := res[2]["error"].(map[string]any)
	if e["code"].(float64) != -32022 {
		t.Errorf("unsupported version error: %v", e)
	}
}

func TestToolErrorCarriesAPIMessageWithoutToken(t *testing.T) {
	s := stubServer(t)
	s.Client.SetToken("a-different-token-value")
	res := roundTrip(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami"}}`)
	r := res[0]["result"].(map[string]any)
	if r["isError"] != true {
		t.Fatalf("want a tool error: %v", r)
	}
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "bad token") || strings.Contains(text, secret) || !strings.Contains(text, "[redacted]") {
		t.Errorf("error text should carry the API message with the secret removed: %s", text)
	}
}

func TestBadCalls(t *testing.T) {
	s := stubServer(t)
	res := roundTrip(t, s,
		`not json`,
		`{"jsonrpc":"2.0","id":1,"method":"nope"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"delete_everything"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"usage_records","arguments":{"limit":5000}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_connect_config","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"ping"}`,
	)
	if res[0]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Error("parse error expected")
	}
	if res[1]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Error("method not found expected")
	}
	if res[2]["error"].(map[string]any)["code"].(float64) != -32602 {
		t.Error("unknown tool is a protocol error")
	}
	if res[3]["result"].(map[string]any)["isError"] != true {
		t.Error("an oversized page must be refused as a tool error")
	}
	if res[4]["result"].(map[string]any)["isError"] != true {
		t.Error("missing access_point must be a tool error")
	}
	if _, ok := res[5]["result"]; !ok {
		t.Error("ping must answer")
	}
}

func TestParseSince(t *testing.T) {
	for _, v := range []string{"", "24h", "7d", "2026-01-01T00:00:00Z"} {
		if _, err := parseSince(v, time.Now()); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	if _, err := parseSince("yesterday", time.Now()); err == nil {
		t.Error("garbage must be rejected")
	}
}
