package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
)

// Claude Code with ANTHROPIC_API_KEY in its settings sends the provider key in
// x-api-key beside the AIPlex token in Authorization. The request stays
// refused (fail closed), and the message names the header to remove. The
// provider key value reaches no log, response, or span, and a caller whose
// native header alone carries the AIPlex token keeps working.
func TestForeignProviderKeyBesideBearerIsRefusedAndNeverRecorded(t *testing.T) {
	spans := installSpans(t, false)
	f := newAPFixture(t)
	chain, upstream, logs := nativeKeyChain(t, f)
	handler := telemetry.Middleware(nil)(chain)
	const marker = "sk-ant-api03-PLANTED-FOREIGN-KEY-9d2e"
	path := "/ap/ide/v1/messages"

	w := nativeKeyDo(handler, "POST", path, nativeAnthropicBody, "Authorization", "Bearer "+nativeKeyToken, "X-Api-Key", marker)
	if w.Code != 401 {
		t.Fatalf("both credentials must be refused, got %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"conflicting credentials", "remove the X-Api-Key header", "never logged, stored or forwarded", "in transit"} {
		if !strings.Contains(body, want) {
			t.Errorf("message lacks %q: %s", want, body)
		}
	}
	// The message does not claim the key is safe or compromised beyond transit.
	if strings.Contains(body, "compromised") || strings.Contains(body, "leaked") {
		t.Errorf("message overstates: %s", body)
	}
	// A repeated header with different values names itself too.
	req := httptest.NewRequest("POST", path, strings.NewReader(nativeAnthropicBody))
	req.Header.Add("X-Api-Key", nativeKeyToken)
	req.Header.Add("X-Api-Key", marker)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 401 || !strings.Contains(rr.Body.String(), "X-Api-Key header was repeated") {
		t.Fatalf("repeated header: %d %s", rr.Code, rr.Body.String())
	}
	// The native header alone with the AIPlex token still works.
	if ok := nativeKeyDo(handler, "POST", path, nativeAnthropicBody, "X-Api-Key", nativeKeyToken); ok.Code != 200 {
		t.Fatalf("native key header alone must keep working: %d %s", ok.Code, ok.Body.String())
	}

	for name, text := range map[string]string{"logs": logs.String(), "responses": body + rr.Body.String(), "upstream": strings.Join(upstream.seen, "\n")} {
		if strings.Contains(text, marker) {
			t.Errorf("the foreign key reached %s", name)
		}
	}
	assertNoMarker(t, spans.GetSpans(), marker)
}
