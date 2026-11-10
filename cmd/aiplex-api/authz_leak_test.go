package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// lockedBuffer collects log output written from request goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Credentials presented by callers (verified tokens, unverifiable tokens,
// provider-native key headers, token query parameters and secrets in request
// bodies) never appear in logs, audit events, responses or the usage ledger.
func TestCallerSecretsNeverReachLogsResponsesOrTheLedger(t *testing.T) {
	const marker = "MARKERSECRET7f3a9c"
	captured := &lockedBuffer{}
	previous, level := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(captured)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	t.Cleanup(func() { log.Logger = previous; zerolog.SetGlobalLevel(level) })

	s := newAuthzServer(t)
	s.put(t, authzHomeTenant, "access-point", "open", `{"id":"open","caller_kinds":["user","agent"],"models":[{"name":"m","surface_ref":"s","native_model":"n"}]}`)
	responses := &strings.Builder{}
	send := func(method, path string, headers map[string]string, body string) {
		req := authzRequest(method, path, "", body)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := s.do2(req)
		responses.WriteString(rec.Header().Get("Www-Authenticate"))
		responses.WriteString(rec.Body.String())
	}
	verified := "Bearer no-role:" + marker
	for _, path := range []string{"/ap/open/v1/chat/completions", "/ap/open/openai/v1/chat/completions", "/ap/open/v1/messages", "/ap/open/v1beta/models/m:generateContent", "/api/v1/llm/native/s/v1/chat/completions", "/api/v1/mcp/connections/c/invoke"} {
		body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"client_secret":"` + marker + `"}`
		for _, headers := range []map[string]string{
			{"Authorization": verified},
			{"Authorization": "Bearer unverifiable-" + marker},
			{"x-api-key": "no-role:" + marker},
			{"api-key": "no-role:" + marker},
			{"x-goog-api-key": "no-role:" + marker},
		} {
			send("POST", path, headers, body)
		}
	}
	for _, path := range []string{"/api/v1/iam/whoami", "/api/v1/usage/records", "/events/stream?token=" + marker + "&run_id=x", "/api/v1/access-points?token=" + marker} {
		send("GET", path, map[string]string{"Authorization": verified}, "")
	}
	send("POST", "/api/v1/agents/identities/verify", map[string]string{"Authorization": "Bearer admin"}, `{"token":"`+marker+`"}`)
	send("POST", "/api/v1/identity/user-providers/verify", map[string]string{"Authorization": "Bearer admin"}, `{"token":"`+marker+`"}`)
	send("PUT", "/api/v1/mcp/connections/c", map[string]string{"Authorization": "Bearer admin", "If-Match": `"0"`}, `{"id":"c","resource_url":"https://`+marker+`.invalid/mcp","client":{"client_secret":"`+marker+`"}}`)
	send("POST", "/auth/token-hook", map[string]string{"Authorization": "Bearer " + marker}, `{}`)
	send("POST", "/internal/tape/events", map[string]string{"Authorization": "Bearer " + marker}, `{}`)
	ledger := s.do(t, "GET", "/api/v1/usage/records", string(callerAdmin), "")

	for name, text := range map[string]string{"logs": captured.String(), "responses": responses.String(), "usage ledger": ledger.Body.String()} {
		if strings.Contains(text, marker) {
			i := strings.Index(text, marker)
			t.Errorf("caller secret reached %s: …%s…", name, text[max(0, i-200):min(len(text), i+60)])
		}
	}
	if !strings.Contains(captured.String(), "request started") {
		t.Fatal("log capture is not wired; the test would pass vacuously")
	}
}
