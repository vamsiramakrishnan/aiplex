package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/hooks"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// hookMarker is planted in prompts and tool arguments: inside an email
// address (masked) and inside a private key block (denied). It must reach
// no upstream after masking, and no ledger record, span, log line, error
// body or metric label in either case.
const hookMarker = "planted-pii-5e9a"

const hookEmail = hookMarker + "@example.com"

const hookPrivateKey = `-----BEGIN PRIVATE KEY-----\n` + hookMarker + `\n-----END PRIVATE KEY-----`

const hookPolicySpec = `{"id":"pii","scope":{"kind":"tenant"},"hooks":[
 {"id":"mask","type":"redact","phases":["request","response"],"redact":{"rules":[
   {"id":"email","builtin":"email","action":"mask"},
   {"id":"private_key","builtin":"private_key","action":"deny"}]}}]}`

func putHookPolicy(t *testing.T, store resources.Store, spec string) {
	t.Helper()
	var probe struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(spec), &probe)
	if _, err := hooks.DecodePolicy(probe.ID, []byte(spec)); err != nil {
		t.Fatal(err)
	}
	current, err := store.Get(context.Background(), "tenant", hooks.Kind, probe.ID)
	revision := uint64(0)
	if err == nil {
		revision = current.Revision
	}
	if _, err := store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: hooks.Kind, ID: probe.ID, Spec: json.RawMessage(spec)}, revision); err != nil {
		t.Fatal(err)
	}
}

// leakProbe captures every place content could escape to.
type leakProbe struct {
	logs    *bytes.Buffer
	metrics *sdkmetric.ManualReader
}

func newLeakProbe(t *testing.T) *leakProbe {
	t.Helper()
	p := &leakProbe{logs: &bytes.Buffer{}, metrics: sdkmetric.NewManualReader()}
	previous := log.Logger
	log.Logger = zerolog.New(&syncWriter{w: p.logs})
	t.Cleanup(func() { log.Logger = previous })
	t.Cleanup(telemetry.UseMeterProviderForTest(sdkmetric.NewMeterProvider(sdkmetric.WithReader(p.metrics))))
	return p
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// metricText renders every collected metric name and label.
func (p *leakProbe) metricText(t *testing.T) string {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := p.metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			b.WriteString(m.Name + "\n")
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					for _, kv := range dp.Attributes.ToSlice() {
						b.WriteString(string(kv.Key) + "=" + kv.Value.Emit() + "\n")
					}
				}
			case metricdata.Histogram[float64]:
				for _, dp := range data.DataPoints {
					for _, kv := range dp.Attributes.ToSlice() {
						b.WriteString(string(kv.Key) + "=" + kv.Value.Emit() + "\n")
					}
				}
			}
		}
	}
	return b.String()
}

func (p *leakProbe) assertClean(t *testing.T, records []usageledger.Record, bodies ...string) {
	t.Helper()
	raw, _ := json.Marshal(records)
	for where, text := range map[string]string{"ledger": string(raw), "logs": p.logs.String(), "metrics": p.metricText(t), "responses": strings.Join(bodies, "\n")} {
		if strings.Contains(text, hookMarker) {
			t.Fatalf("%s leaked the planted marker: %s", where, text)
		}
	}
}

func TestModelHooksMaskDenyAndRecordWithoutContent(t *testing.T) {
	probe := newLeakProbe(t)
	exporter := installSpans(t, false)
	var mu sync.Mutex
	var upstreamBodies []string
	f := quotaHostModelNew(t, true, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		upstreamBodies = append(upstreamBodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, quotaHostModelTerminal)
	}, nil)
	records := attachLedger(t, f.handler)
	putHookPolicy(t, f.resources, hookPolicySpec)

	// Masked: the upstream receives the placeholder, nothing else changes.
	body := strings.Replace(quotaHostModelBody, "hello", "hello "+hookEmail, 1)
	w := httptest.NewRecorder()
	f.serve(w, f.request("generateContent", "hook-mask", body, false), false)
	if w.Code != 200 || f.calls.Load() != 1 {
		t.Fatalf("masked exchange: %d %s", w.Code, w.Body.String())
	}
	want := strings.Replace(quotaHostModelBody, "hello", "hello [EMAIL]", 1)
	if len(upstreamBodies) != 1 || upstreamBodies[0] != want {
		t.Fatalf("upstream body %q, want %q", upstreamBodies, want)
	}
	maskedAttempt := w.Header().Get(execution.AttemptHeader)
	quotaHostModelAssertSettled(t, f, maskedAttempt)

	// Denied: refused before the quota lease and the upstream.
	body = strings.Replace(quotaHostModelBody, "hello", "key "+hookPrivateKey, 1)
	denied := httptest.NewRecorder()
	f.serve(denied, f.request("generateContent", "hook-deny", body, false), false)
	if denied.Code != 403 || f.calls.Load() != 1 || denied.Header().Get(execution.AttemptHeader) != "" {
		t.Fatalf("denied exchange: %d calls=%d attempt=%q %s", denied.Code, f.calls.Load(), denied.Header().Get(execution.AttemptHeader), denied.Body.String())
	}
	var refusal struct{ Code, Message string }
	if json.Unmarshal(denied.Body.Bytes(), &refusal) != nil || refusal.Code != hooks.CodeDenied || refusal.Message != hooks.MessageDenied {
		t.Fatalf("refusal body %s", denied.Body.String())
	}

	got := records()
	if len(got) != 2 {
		t.Fatalf("records %+v", got)
	}
	byStatus := map[string]usageledger.Record{}
	for _, r := range got {
		byStatus[r.Status] = r
	}
	ok, refused := byStatus[usageledger.StatusOK], byStatus[usageledger.StatusDenied]
	if ok.Hooks == nil || ok.Hooks.Decision != usageledger.HookTransform || ok.Hooks.Results[0].Rules[0] != (usageledger.HookRuleCount{ID: "email", Count: 1}) || ok.QuotaAttemptID != maskedAttempt {
		t.Fatalf("masked record hooks %+v", ok.Hooks)
	}
	if refused.Hooks == nil || refused.Hooks.Decision != usageledger.HookDeny || refused.Hooks.Results[0].Rules[0].ID != "private_key" || refused.ErrorClass != "hook_denied" || refused.QuotaAttemptID != "" {
		t.Fatalf("denied record %+v hooks %+v", refused, refused.Hooks)
	}
	// Two response-phase results on the masked exchange: the response was
	// inspected too (allow).
	phases := []string{}
	for _, r := range ok.Hooks.Results {
		phases = append(phases, r.Phase+":"+r.Decision)
	}
	if strings.Join(phases, ",") != "request:transform,response:allow" {
		t.Fatalf("phases %v", phases)
	}

	spans := exporter.GetSpans()
	assertNoMarker(t, spans, hookMarker)
	var sawDecision bool
	for _, s := range spans {
		kv := spanKV(s)
		if kv[telemetry.AttrHooksDecision] == "deny" && kv[telemetry.AttrHooksDeniedRule] == "private_key" {
			sawDecision = true
		}
	}
	if !sawDecision {
		t.Fatal("no span carries the hook decision and denied rule")
	}
	if metrics := probe.metricText(t); !strings.Contains(metrics, telemetry.MetricHookDecisions) || !strings.Contains(metrics, "aiplex.hook.decision=deny") {
		t.Fatalf("hook metrics missing: %s", metrics)
	}
	probe.assertClean(t, got, w.Body.String(), denied.Body.String())
}

func TestModelHooksStreamingAndFailClosedConfiguration(t *testing.T) {
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, quotaHostModelTerminal)
	}, nil)
	f.handler.Quota = nil
	records := attachLedger(t, f.handler)
	putHookPolicy(t, f.resources, hookPolicySpec)

	// A response hook applies and the request streams: refused by default.
	w := httptest.NewRecorder()
	f.serve(w, f.request("streamGenerateContent", "", quotaHostModelBody, false), false)
	if w.Code != 403 || f.calls.Load() != 0 || !strings.Contains(w.Body.String(), hooks.CodeDenied) {
		t.Fatalf("stream with response hook: %d %s", w.Code, w.Body.String())
	}
	// Buffer is not available for Gemini streams: still refused.
	putHookPolicy(t, f.resources, strings.Replace(hookPolicySpec, `"scope"`, `"streaming":"buffer","scope"`, 1))
	w = httptest.NewRecorder()
	f.serve(w, f.request("streamGenerateContent", "", quotaHostModelBody, false), false)
	if w.Code != 403 || f.calls.Load() != 0 {
		t.Fatalf("gemini buffer: %d %s", w.Code, w.Body.String())
	}
	// A broken stored policy fails closed.
	if _, err := f.resources.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: hooks.Kind, ID: "broken", Spec: json.RawMessage(`{"id":"broken","hooks":"x"}`)}, 0); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	f.serve(w, f.request("generateContent", "", quotaHostModelBody, false), false)
	if w.Code != 503 || f.calls.Load() != 0 || !strings.Contains(w.Body.String(), hooks.CodeUnavailableConfig) {
		t.Fatalf("broken policy: %d %s", w.Code, w.Body.String())
	}
	for _, r := range records() {
		if r.Status == usageledger.StatusOK {
			t.Fatalf("a refused exchange was recorded ok: %+v", r)
		}
	}
}

func TestToolHooksMaskArgumentsDenyResultsAndKeepQuota(t *testing.T) {
	probe := newLeakProbe(t)
	var mu sync.Mutex
	var upstreamBodies []string
	result := managedToolResponse
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		upstreamBodies = append(upstreamBodies, string(body))
		reply := result
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	defer upstream.Close()
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	h, _, request := protectedToolFixture(t, upstream.URL+"/mcp")
	records := attachLedger(t, h)
	store := h.Store
	putHookPolicy(t, store, `{"id":"tools","scope":{"kind":"tool_connection","id":"surface"},"hooks":[
	 {"id":"mask","type":"redact","phases":["request","response"],"redact":{"rules":[
	   {"id":"email","builtin":"email","action":"mask"},{"id":"private_key","builtin":"private_key","action":"deny"}]}}]}`)
	call := func(key, arguments string) *httptest.ResponseRecorder {
		r := request(key)
		body := strings.Replace(managedToolBody, `{"query":"hello"}`, arguments, 1)
		r.Body = io.NopCloser(strings.NewReader(body))
		r.ContentLength = int64(len(body))
		w := httptest.NewRecorder()
		h.NativeTool(w, r)
		return w
	}
	// Request deny: refused before the quota lease (the budget is one call).
	denied := call("deny", `{"query":"`+hookPrivateKey+`"}`)
	if denied.Code != 403 || len(upstreamBodies) != 0 || !strings.Contains(denied.Body.String(), `"reason":"hook_denied"`) || !strings.Contains(denied.Body.String(), `"id":"rpc"`) {
		t.Fatalf("tool request deny: %d %s", denied.Code, denied.Body.String())
	}
	// The one-call budget is still available, and the argument is masked.
	mu.Lock()
	result = strings.Replace(managedToolResponse, "native tool result", "result for "+hookEmail, 1)
	mu.Unlock()
	masked := call("mask", `{"query":"`+hookEmail+`"}`)
	if masked.Code != 200 || len(upstreamBodies) != 1 || strings.Contains(upstreamBodies[0], hookMarker) || !strings.Contains(upstreamBodies[0], `"query":"[EMAIL]"`) {
		t.Fatalf("tool mask: %d %s upstream=%q", masked.Code, masked.Body.String(), upstreamBodies)
	}
	if !strings.Contains(masked.Body.String(), "result for [EMAIL]") {
		t.Fatalf("tool result not masked: %s", masked.Body.String())
	}
	// A result that must be denied is withheld (the call was dispatched).
	putHookPolicy(t, store, `{"id":"tools","scope":{"kind":"tool_connection","id":"surface"},"hooks":[
	 {"id":"result","type":"redact","phases":["response"],"redact":{"rules":[{"id":"marker","pattern":"`+hookMarker+`","action":"deny"}]}}]}`)
	putHookPolicy(t, store, `{"id":"budget","scope":{"kind":"tenant"},"planes":["llmplex"],"hooks":[{"id":"s","type":"size_limit","phases":["request"],"size_limit":{"max_chars":100000}}]}`)
	// The ledger writes asynchronously and reads h.Quota; let it finish first.
	h.Ledger.Flush()
	h.Quota = nil
	withheld := call("result", `{"query":"q"}`)
	if withheld.Code != 403 || strings.Contains(withheld.Body.String(), hookMarker) || !strings.Contains(withheld.Body.String(), "hook_denied") {
		t.Fatalf("tool result deny: %d %s", withheld.Code, withheld.Body.String())
	}
	got := records()
	probe.assertClean(t, got, denied.Body.String(), masked.Body.String(), withheld.Body.String())
	decisions := map[string]int{}
	for _, r := range got {
		if r.Hooks != nil {
			decisions[r.Hooks.Decision]++
		}
	}
	if decisions[usageledger.HookDeny] != 2 || decisions[usageledger.HookTransform] != 1 {
		t.Fatalf("decisions %v", decisions)
	}
}

func TestHookPolicyValidationRefusesUnapprovedSidecars(t *testing.T) {
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"decision":"allow"}`)
	}))
	defer sidecar.Close()
	store := resources.NewMemoryStore()
	h := cohesionHandler(t, store, sidecar.URL)
	put := func(endpoint string) *httptest.ResponseRecorder {
		spec := `{"id":"side","scope":{"kind":"tenant"},"hooks":[{"id":"s","type":"http","phases":["request"],"http":{"endpoint":"` + endpoint + `"}}]}`
		r := trustedConnectedRequest("/api/v1/hooks/policies/side", spec, nil)
		r.Method = http.MethodPut
		r.SetPathValue("id", "side")
		w := httptest.NewRecorder()
		h.Resources(HookPolicyKind)(w, r)
		return w
	}
	for _, endpoint := range []string{"http://169.254.169.254/latest/meta-data", "http://10.0.0.5:8080/hook", "https://sidecar.example.com/hook", "http://127.0.0.1:1/x"} {
		if w := put(endpoint); w.Code != 400 || !strings.Contains(w.Body.String(), "outbound endpoint policy") {
			t.Errorf("%s accepted: %d %s", endpoint, w.Code, w.Body.String())
		}
	}
	if w := put(sidecar.URL + "/hook"); w.Code != 200 {
		t.Fatalf("approved sidecar refused: %d %s", w.Code, w.Body.String())
	}
	// Unknown hook types and fields are refused at save time.
	r := trustedConnectedRequest("/api/v1/hooks/policies/x", `{"id":"x","scope":{"kind":"tenant"},"hooks":[{"id":"m","type":"moderate","phases":["request"]}]}`, nil)
	r.Method = http.MethodPut
	r.SetPathValue("id", "x")
	w := httptest.NewRecorder()
	h.Resources(HookPolicyKind)(w, r)
	if w.Code != 400 {
		t.Fatalf("unknown hook type: %d %s", w.Code, w.Body.String())
	}
}

func TestHookRefusalsRenderInEachProtocol(t *testing.T) {
	for protocol, want := range map[string]string{
		WireOpenAIChat:      `"code":"hook_denied"`,
		WireOpenAIResponses: `"type":"permission_error"`,
		WireAnthropic:       `"code":"hook_denied"`,
		WireGenAI:           `"reason":"HOOK_DENIED"`,
		WireBedrockConverse: `"code":"hook_denied"`,
	} {
		w := httptest.NewRecorder()
		writeNativeError(w, protocol, http.StatusForbidden, hooks.CodeDenied, hooks.MessageDenied)
		if w.Code != 403 || !strings.Contains(w.Body.String(), want) || w.Header().Get("X-Aiplex-Error-Code") != "hook_denied" {
			t.Errorf("%s: %d %s %v", protocol, w.Code, w.Body.String(), w.Header())
		}
	}
}

func TestHookPolicyDryRunNeverRecords(t *testing.T) {
	store := resources.NewMemoryStore()
	h := cohesionHandler(t, store, "https://models.example")
	records := attachLedger(t, h)
	putHookPolicy(t, store, hookPolicySpec)
	r := trustedConnectedRequest("/api/v1/hooks/policies/pii/test", `{"texts":["mail `+hookEmail+`","clean"]}`, nil)
	r.SetPathValue("id", "pii")
	w := httptest.NewRecorder()
	h.TestHookPolicy(w, r)
	var out HookPolicyTestResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Decision != hooks.Transform || out.Texts[0] != "mail [EMAIL]" || out.Texts[1] != "clean" {
		t.Fatalf("dry run: %d %s", w.Code, w.Body.String())
	}
	r = trustedConnectedRequest("/api/v1/hooks/policies/pii/test", `{"texts":["`+hookPrivateKey+`"]}`, nil)
	r.SetPathValue("id", "pii")
	w = httptest.NewRecorder()
	h.TestHookPolicy(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"decision":"deny"`) || strings.Contains(w.Body.String(), hookMarker) {
		t.Fatalf("dry run deny: %d %s", w.Code, w.Body.String())
	}
	if got := records(); len(got) != 0 {
		t.Fatalf("dry run recorded %d records", len(got))
	}
}
