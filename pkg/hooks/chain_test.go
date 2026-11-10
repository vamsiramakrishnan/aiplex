package hooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func redactHook(id string, rules ...RedactRule) Hook {
	return Hook{ID: id, Type: TypeRedact, Phases: []string{PhaseRequest, PhaseResponse}, Redact: &RedactConfig{Rules: rules}}
}

func mask(id, builtin string) RedactRule {
	return RedactRule{ID: id, Builtin: builtin, Action: ActionMask}
}

func resolvedOf(hooks ...Hook) Resolved {
	p := Policy{ID: "p", Scope: Scope{Kind: ScopeTenant}, Hooks: hooks}
	return Resolve([]Policy{p}, Target{Plane: PlaneModels})
}

func TestBuiltinRedactionPatterns(t *testing.T) {
	cases := []struct {
		builtin, in, want string
	}{
		{"email", "write to jane.doe+x@mail.example.co.uk now", "write to [EMAIL] now"},
		{"phone", "call (415) 555-0123 or +14155550123", "call [PHONE] or [PHONE]"},
		{"credit_card", "card 4111 1111 1111 1111 ok", "card [CREDIT_CARD] ok"},
		{"credit_card", "order 4111 1111 1111 1112 fails luhn", "order 4111 1111 1111 1112 fails luhn"},
		{"iban", "pay GB82 WEST 1234 5698 7654 32 today", "pay [IBAN] today"},
		{"iban", "not GB00 WEST 1234 5698 7654 32", "not GB00 WEST 1234 5698 7654 32"},
		{"us_ssn", "ssn 123-45-6789; 000-12-3456", "ssn [US_SSN]; 000-12-3456"},
		{"ipv4", "host 10.0.0.1 and 999.1.1.1", "host [IP_ADDRESS] and 999.1.1.1"},
		{"ipv6", "addr 2001:db8::1 at 12:30:45", "addr [IP_ADDRESS] at 12:30:45"},
		{"aws_access_key_id", "key AKIAIOSFODNN7EXAMPLE end", "key [AWS_ACCESS_KEY_ID] end"},
		{"github_token", "tok ghp_" + strings.Repeat("a", 36) + " x", "tok [GITHUB_TOKEN] x"},
		{"jwt", "jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl x", "jwt [JWT] x"},
		{"private_key", "k -----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY----- done", "k [PRIVATE_KEY] done"},
		{"bearer_token", "Authorization: Bearer abcdefghijklmnop1234", "Authorization: [BEARER_TOKEN]"},
		{"google_api_key", "AIza" + strings.Repeat("B", 35), "[GOOGLE_API_KEY]"},
	}
	for _, c := range cases {
		rules, err := compileRedact(RedactConfig{Rules: []RedactRule{mask("r", c.builtin)}})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := redactText(rules, c.in, map[string]int{})
		if got != c.want {
			t.Errorf("%s: %q -> %q, want %q", c.builtin, c.in, got, c.want)
		}
	}
}

func TestRedactValidation(t *testing.T) {
	for name, cfg := range map[string]RedactConfig{
		"no rules":        {},
		"bad regex":       {Rules: []RedactRule{{ID: "r", Pattern: "(unclosed", Action: ActionMask}}},
		"backreference":   {Rules: []RedactRule{{ID: "r", Pattern: `(a)\1`, Action: ActionMask}}},
		"empty match":     {Rules: []RedactRule{{ID: "r", Pattern: `a*`, Action: ActionMask}}},
		"unknown builtin": {Rules: []RedactRule{{ID: "r", Builtin: "dna", Action: ActionMask}}},
		"both":            {Rules: []RedactRule{{ID: "r", Builtin: "email", Pattern: "x", Action: ActionMask}}},
		"bad action":      {Rules: []RedactRule{{ID: "r", Builtin: "email", Action: "log"}}},
		"long pattern":    {Rules: []RedactRule{{ID: "r", Pattern: strings.Repeat("a", MaxPatternLength+1), Action: ActionMask}}},
		"duplicate ids":   {Rules: []RedactRule{mask("r", "email"), mask("r", "ipv4")}},
	} {
		if _, err := compileRedact(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	many := RedactConfig{}
	for i := 0; i <= MaxRulesPerHook; i++ {
		many.Rules = append(many.Rules, RedactRule{ID: "r" + strings.Repeat("x", i%50) + string(rune('a'+i%26)) + string(rune('a'+i/26)), Pattern: "zz", Action: ActionMask})
	}
	if _, err := compileRedact(many); err == nil {
		t.Error("rule count cap not enforced")
	}
}

func TestDenyRuleCannotBeHiddenByAMask(t *testing.T) {
	rules, _ := compileRedact(RedactConfig{Rules: []RedactRule{
		{ID: "wide", Pattern: `x+KEY[a-z]*`, Action: ActionMask},
		{ID: "key", Pattern: `KEY`, Action: ActionDeny},
	}})
	if _, denied := redactText(rules, "xxxKEYabc", map[string]int{}); denied != "key" {
		t.Fatalf("deny hidden by overlapping mask: %q", denied)
	}
}

func run(t *testing.T, rt Runtime, resolved Resolved, phase string, texts ...string) Outcome {
	t.Helper()
	return Run(context.Background(), rt, resolved, Meta{Plane: PlaneModels}, phase, PlainText(texts), nil)
}

func TestChainOrderTransformThenDeny(t *testing.T) {
	// The first hook masks the email; the second (deny on "[EMAIL]") sees
	// the transformed text and denies. A third hook never runs.
	resolved := resolvedOf(
		redactHook("mask", mask("email", "email")),
		redactHook("deny-masked", RedactRule{ID: "masked", Pattern: `\[EMAIL\]`, Action: ActionDeny}),
		Hook{ID: "never", Type: TypeHTTP, Phases: []string{PhaseRequest}, HTTP: &HTTPConfig{Endpoint: "http://127.0.0.1:1"}},
	)
	out := run(t, Runtime{}, resolved, PhaseRequest, "hi a@b.example")
	if !out.Refused() || out.Code != CodeDenied || out.Status != 403 || len(out.Results) != 2 {
		t.Fatalf("%+v", out)
	}
	if out.Results[0].Decision != Transform || out.Results[1].Decision != Deny || out.Results[1].Rules[0].ID != "masked" {
		t.Fatalf("results %+v", out.Results)
	}
	if strings.Contains(out.Message, "a@b") || strings.Contains(out.Message, "EMAIL") {
		t.Fatalf("message leaks: %q", out.Message)
	}
}

func TestChainTransformAndCounts(t *testing.T) {
	out := run(t, Runtime{}, resolvedOf(redactHook("pii", mask("email", "email"), mask("ip", "ipv4"))), PhaseRequest, "a@b.example and c@d.example", "10.0.0.1", "clean")
	if out.Action != Transform {
		t.Fatalf("%+v", out)
	}
	texts, _ := PlainTexts(out.Body)
	if strings.Join(texts, "|") != "[EMAIL] and [EMAIL]|[IP_ADDRESS]|clean" {
		t.Fatalf("texts %q", texts)
	}
	rules := out.Results[0].Rules
	if len(rules) != 2 || rules[0] != (usageledger.HookRuleCount{ID: "email", Count: 2}) || rules[1] != (usageledger.HookRuleCount{ID: "ip", Count: 1}) {
		t.Fatalf("rules %+v", rules)
	}
}

func TestPhaseSelection(t *testing.T) {
	h := redactHook("pii", mask("email", "email"))
	h.Phases = []string{PhaseResponse}
	if out := run(t, Runtime{}, resolvedOf(h), PhaseRequest, "a@b.example"); out.Action != Allow || len(out.Results) != 0 {
		t.Fatalf("response-only hook ran on request: %+v", out)
	}
}

func TestUninspectableAndNonText(t *testing.T) {
	strict := resolvedOf(redactHook("pii", mask("email", "email")))
	out := Run(context.Background(), Runtime{}, strict, Meta{}, PhaseRequest, nil, ErrUninspectable)
	if !out.Refused() || out.Results[0].Rules[0].ID != "uninspectable" {
		t.Fatalf("uninspectable allowed by default: %+v", out)
	}
	lenient := redactHook("pii", mask("email", "email"))
	lenient.OnUninspectable = "allow"
	out = Run(context.Background(), Runtime{}, resolvedOf(lenient), Meta{}, PhaseRequest, nil, ErrUninspectable)
	if out.Refused() || out.Results[0].Decision != usageledger.HookSkipped {
		t.Fatalf("on_uninspectable allow: %+v", out)
	}
	content, _ := ExtractRequest(FormatOpenAIChat, []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"u"}}]}]}`))
	out = Run(context.Background(), Runtime{}, strict, Meta{}, PhaseRequest, content, nil)
	if out.Refused() || out.Uninspected != 1 {
		t.Fatalf("non-text default: %+v", out)
	}
	strict.NonText = "deny"
	if out = Run(context.Background(), Runtime{}, strict, Meta{}, PhaseRequest, content, nil); !out.Refused() {
		t.Fatalf("non_text deny: %+v", out)
	}
}

func TestSizeLimit(t *testing.T) {
	h := Hook{ID: "size", Type: TypeSizeLimit, Phases: []string{PhaseRequest}, SizeLimit: &SizeLimitConfig{MaxChars: 5}}
	if out := run(t, Runtime{}, resolvedOf(h), PhaseRequest, "ééé", "éé"); out.Refused() {
		t.Fatalf("5 code points refused: %+v", out)
	}
	if out := run(t, Runtime{}, resolvedOf(h), PhaseRequest, "abc", "def"); !out.Refused() || out.Results[0].Rules[0].ID != "max_chars" {
		t.Fatalf("%+v", out)
	}
}

func TestScopeMergeNarrowerAddsOnly(t *testing.T) {
	tenant := Policy{ID: "tenant-pii", Scope: Scope{Kind: ScopeTenant}, Hooks: []Hook{redactHook("pii", mask("email", "email"))}}
	ap := Policy{ID: "ap-extra", Scope: Scope{Kind: ScopeAccessPoint, ID: "ap1"}, Streaming: StreamingPassthrough, Hooks: []Hook{redactHook("ip", mask("ip", "ipv4"))}}
	surface := Policy{ID: "surface", Scope: Scope{Kind: ScopeModelSurface, ID: "s1"}, Streaming: StreamingBuffer, Hooks: []Hook{redactHook("ssn", mask("ssn", "us_ssn"))}}
	other := Policy{ID: "other-ap", Scope: Scope{Kind: ScopeAccessPoint, ID: "ap2"}, Hooks: []Hook{redactHook("x", mask("x", "jwt"))}}
	tools := Policy{ID: "tools-only", Scope: Scope{Kind: ScopeTenant}, Planes: []string{PlaneTools}, Hooks: []Hook{redactHook("t", mask("t", "jwt"))}}
	resolved := Resolve([]Policy{surface, other, ap, tenant, tools}, Target{Plane: PlaneModels, AccessPointID: "ap1", ModelSurface: "s1"})
	var names []string
	for _, h := range resolved.Hooks {
		names = append(names, h.Name())
	}
	if strings.Join(names, ",") != "tenant-pii/pii,ap-extra/ip,surface/ssn" {
		t.Fatalf("hooks %v", names)
	}
	// The tenant policy (default streaming deny) cannot be loosened by the
	// access point's passthrough or the surface's buffer.
	if resolved.Streaming != StreamingDeny {
		t.Fatalf("streaming %q", resolved.Streaming)
	}
	// A narrower policy with no hooks of its own cannot remove the tenant
	// hook: there is no removal syntax, and an empty hook list is invalid.
	empty := Policy{ID: "remove", Scope: Scope{Kind: ScopeAccessPoint, ID: "ap1"}, Hooks: []Hook{}}
	if empty.Validate() == nil {
		t.Fatal("empty hook list accepted")
	}
	if got := Resolve([]Policy{tenant}, Target{Plane: PlaneModels, AccessPointID: "ap1"}); len(got.Hooks) != 1 {
		t.Fatalf("tenant hook missing: %+v", got)
	}
}

func TestDecodePolicyStrict(t *testing.T) {
	good := `{"id":"pii","scope":{"kind":"tenant"},"hooks":[{"id":"r","type":"redact","phases":["request"],"redact":{"rules":[{"id":"email","builtin":"email","action":"mask"}]}}]}`
	if _, err := DecodePolicy("pii", []byte(good)); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"unknown top field":   strings.Replace(good, `"id":"pii",`, `"id":"pii","extra":1,`, 1),
		"unknown hook field":  strings.Replace(good, `"type":"redact",`, `"type":"redact","weight":2,`, 1),
		"unknown rule field":  strings.Replace(good, `"action":"mask"`, `"action":"mask","log":true`, 1),
		"unknown type":        strings.Replace(good, `"type":"redact"`, `"type":"moderate"`, 1),
		"id mismatch":         strings.Replace(good, `"id":"pii"`, `"id":"other"`, 1),
		"no phases":           strings.Replace(good, `"phases":["request"],`, ``, 1),
		"bad scope":           strings.Replace(good, `"kind":"tenant"`, `"kind":"team"`, 1),
		"scope id on tenant":  strings.Replace(good, `"kind":"tenant"`, `"kind":"tenant","id":"x"`, 1),
		"two configs":         strings.Replace(good, `"redact":{`, `"size_limit":{"max_chars":1},"redact":{`, 1),
		"fail_open on redact": strings.Replace(good, `"type":"redact",`, `"type":"redact","http":{"endpoint":"https://x","fail_open":true},`, 1),
		"trailing":            good + "{}",
	} {
		if _, err := DecodePolicy("pii", []byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// ── HTTP sidecar ───────────────────────────────────────────────────────────

func httpHook(endpoint string, mutate ...func(*HTTPConfig)) Resolved {
	cfg := &HTTPConfig{Endpoint: endpoint}
	for _, m := range mutate {
		m(cfg)
	}
	return resolvedOf(Hook{ID: "side", Type: TypeHTTP, Phases: []string{PhaseRequest, PhaseResponse}, TimeoutMS: 500, HTTP: cfg})
}

func sidecarServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}

func answer(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestSidecarContract(t *testing.T) {
	var seen SidecarRequest
	var auth string
	s := sidecarServer(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&seen)
		answer(200, `{"decision":"transform","parts":[{"index":0,"text":"[NAME]"}],"rules":[{"id":"person","count":1}]}`)(w, r)
	})
	rt := Runtime{Client: s.Client(), Secret: func(_ context.Context, ref string) (string, error) { return "sidecar-token-" + ref, nil }}
	out := run(t, rt, httpHook(s.URL, func(c *HTTPConfig) { c.AuthSecretRef = "hook-key" }), PhaseRequest, "Jane")
	if out.Action != Transform || auth != "Bearer sidecar-token-hook-key" || seen.Version != ContractVersion || seen.Parts[0].Text != "Jane" || seen.Plane != PlaneModels {
		t.Fatalf("%+v auth=%q seen=%+v", out, auth, seen)
	}
	texts, _ := PlainTexts(out.Body)
	if texts[0] != "[NAME]" || out.Results[0].Rules[0].ID != "person" {
		t.Fatalf("%q %+v", texts, out.Results)
	}

	deny := sidecarServer(t, answer(200, `{"decision":"deny","reason":"Contains: Jane Doe!","rules":[]}`))
	out = run(t, Runtime{Client: deny.Client()}, httpHook(deny.URL), PhaseRequest, "Jane Doe")
	if !out.Refused() || out.Results[0].Rules[0].ID != "sidecar" || strings.Contains(out.Message, "Jane") {
		t.Fatalf("sidecar reason must be sanitised and never shown: %+v", out)
	}
}

func TestSidecarFailsClosed(t *testing.T) {
	slow := sidecarServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	})
	redirect := sidecarServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest", http.StatusTemporaryRedirect)
	})
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	cases := map[string]string{
		"5xx":              sidecarServer(t, answer(500, `{"decision":"allow"}`)).URL,
		"invalid json":     sidecarServer(t, answer(200, `{"decision":`)).URL,
		"unknown decision": sidecarServer(t, answer(200, `{"decision":"maybe"}`)).URL,
		"unknown field":    sidecarServer(t, answer(200, `{"decision":"allow","extra":1}`)).URL,
		"bad part index":   sidecarServer(t, answer(200, `{"decision":"transform","parts":[{"index":9,"text":"x"}]}`)).URL,
		"duplicate key":    sidecarServer(t, answer(200, `{"decision":"allow","decision":"deny"}`)).URL,
		"not json":         sidecarServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }).URL,
		"timeout":          slow.URL,
		"redirect":         redirect.URL,
		"unreachable":      closedURL,
	}
	for name, url := range cases {
		out := run(t, Runtime{Client: http.DefaultClient}, httpHook(url), PhaseRequest, "x")
		if !out.Refused() || out.Code != CodeUnavailable || out.Status != 503 || out.Results[0].Decision != usageledger.HookError {
			t.Errorf("%s: %+v", name, out)
		}
		open := run(t, Runtime{Client: http.DefaultClient}, httpHook(url, func(c *HTTPConfig) { c.FailOpen = true }), PhaseRequest, "x")
		if open.Refused() || !open.Results[0].FailOpenUsed {
			t.Errorf("%s fail_open: %+v", name, open)
		}
	}
	// A missing secret is an error too, and never a call without auth.
	var called atomic.Bool
	s := sidecarServer(t, func(w http.ResponseWriter, r *http.Request) { called.Store(true) })
	out := run(t, Runtime{Client: s.Client()}, httpHook(s.URL, func(c *HTTPConfig) { c.AuthSecretRef = "missing" }), PhaseRequest, "x")
	if !out.Refused() || called.Load() {
		t.Fatalf("missing secret: %+v called=%v", out, called.Load())
	}
}

func testdata(t *testing.T, name string) string {
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPresidioAdapter(t *testing.T) {
	var analyzed map[string]any
	var path string
	s := sidecarServer(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&analyzed)
		answer(200, testdata(t, "presidio-analyze-response.json"))(w, r)
	})
	text := "Hi, I am Jöne Smith, mail jane@example.org today"
	threshold := 0.5
	out := run(t, Runtime{Client: s.Client()}, httpHook(s.URL, func(c *HTTPConfig) {
		c.Format = SidecarPresidio
		c.Presidio = &PresidioConfig{Entities: []string{"PERSON", "EMAIL_ADDRESS"}, ScoreThreshold: &threshold}
	}), PhaseRequest, text)
	if path != "/analyze" || analyzed["text"] != text || analyzed["language"] != "en" || analyzed["score_threshold"] != 0.5 {
		t.Fatalf("analyze request %s %+v", path, analyzed)
	}
	texts, _ := PlainTexts(out.Body)
	if out.Action != Transform || texts[0] != "Hi, I am [PERSON], mail [EMAIL_ADDRESS] today" {
		t.Fatalf("%+v %q", out, texts)
	}
	out = run(t, Runtime{Client: s.Client()}, httpHook(s.URL, func(c *HTTPConfig) {
		c.Format = SidecarPresidio
		c.Presidio = &PresidioConfig{Action: ActionDeny}
	}), PhaseRequest, text)
	if !out.Refused() || out.Results[0].Rules[0].ID != "person" {
		t.Fatalf("presidio deny %+v", out)
	}
	anonymizer := sidecarServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/anonymize" || req["analyzer_results"] == nil {
			w.WriteHeader(400)
			return
		}
		answer(200, testdata(t, "presidio-anonymize-response.json"))(w, r)
	})
	out = run(t, Runtime{Client: http.DefaultClient}, httpHook(s.URL, func(c *HTTPConfig) {
		c.Format = SidecarPresidio
		c.Presidio = &PresidioConfig{AnonymizerEndpoint: anonymizer.URL}
	}), PhaseRequest, text)
	texts, _ = PlainTexts(out.Body)
	if out.Action != Transform || texts[0] != "Hi, I am <PERSON>, mail <EMAIL_ADDRESS> today" {
		t.Fatalf("anonymizer %+v %q", out, texts)
	}
	bad := sidecarServer(t, answer(200, `[{"entity_type":"PERSON","start":5,"end":500,"score":1}]`))
	if out = run(t, Runtime{Client: bad.Client()}, httpHook(bad.URL, func(c *HTTPConfig) { c.Format = SidecarPresidio }), PhaseRequest, "short"); out.Code != CodeUnavailable {
		t.Fatalf("out-of-range span accepted: %+v", out)
	}
}

func TestLLMGuardAdapter(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	s := sidecarServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if r.URL.Path == "/analyze/prompt" {
			answer(200, testdata(t, "llm-guard-analyze-prompt-response.json"))(w, r)
			return
		}
		answer(200, testdata(t, "llm-guard-analyze-output-invalid.json"))(w, r)
	})
	hook := httpHook(s.URL, func(c *HTTPConfig) { c.Format = SidecarLLMGuard })
	out := run(t, Runtime{Client: s.Client()}, hook, PhaseRequest, "Hi, I am Jane, mail jane@example.org today")
	texts, _ := PlainTexts(out.Body)
	if out.Action != Transform || !strings.Contains(texts[0], "[REDACTED_EMAIL_ADDRESS_1]") || paths[0] != "/analyze/prompt" || bodies[0]["prompt"] == nil {
		t.Fatalf("%+v %q %v", out, texts, paths)
	}
	if out.Results[0].Rules[0].ID != "anonymize" {
		t.Fatalf("scanner rules %+v", out.Results[0].Rules)
	}
	out = run(t, Runtime{Client: s.Client()}, hook, PhaseResponse, "model output")
	if !out.Refused() || paths[1] != "/analyze/output" || bodies[1]["output"] != "model output" || out.Results[0].Rules[0].ID != "sensitive" {
		t.Fatalf("output: %+v %v", out, paths)
	}
}
