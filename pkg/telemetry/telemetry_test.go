package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func setup(t *testing.T, values map[string]string) *Telemetry {
	t.Helper()
	previous := current.Load()
	tel, err := Setup(context.Background(), Options{Getenv: env(values)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tel.Shutdown(context.Background())
		current.Store(previous)
	})
	return tel
}

func TestSetupIsOffWithoutAnEndpointButStillMintsIDs(t *testing.T) {
	tel := setup(t, nil)
	if tel.TracesExported || tel.MetricsExported || !tel.PropagateUpstream {
		t.Fatalf("defaults: %+v", tel)
	}
	ctx, span := Tracer().Start(context.Background(), "x")
	defer span.End()
	if TraceID(ctx) == "" || SpanID(ctx) == "" || span.SpanContext().IsSampled() {
		t.Fatalf("ids must exist without export, unsampled: %s %s", TraceID(ctx), SpanID(ctx))
	}
}

func TestSetupRefusesUnsupportedConfiguration(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"protocol":    {"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json"},
		"exporter":    {"OTEL_TRACES_EXPORTER": "zipkin"},
		"propagation": {EnvPropagateUpstream: "sometimes"},
	} {
		if _, err := Setup(context.Background(), Options{Getenv: env(values)}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSetupExportsWithAnEndpointAndHonoursNone(t *testing.T) {
	tel := setup(t, map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1", "OTEL_METRICS_EXPORTER": "none", EnvPropagateUpstream: "false"})
	if !tel.TracesExported || tel.MetricsExported || tel.PropagateUpstream || PropagateUpstream() {
		t.Fatalf("configured: %+v", tel)
	}
	legacy := setup(t, map[string]string{EnvLegacyEndpoint: "http://127.0.0.1:1"})
	if !legacy.TracesExported || !legacy.MetricsExported {
		t.Fatalf("OTEL_ENDPOINT alias: %+v", legacy)
	}
	disabled := setup(t, map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1", "OTEL_SDK_DISABLED": "true"})
	if disabled.TracesExported || disabled.MetricsExported {
		t.Fatalf("OTEL_SDK_DISABLED: %+v", disabled)
	}
}

func TestInjectReplacesAndPropagationOffRemoves(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tel, err := Setup(context.Background(), Options{Getenv: env(nil), SpanExporter: exporter})
	if err != nil {
		t.Fatal(err)
	}
	previous := current.Load()
	defer func() { current.Store(previous) }()
	ctx, span := Tracer().Start(context.Background(), "client", trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()
	h := http.Header{"Traceparent": {"00-11111111111111111111111111111111-2222222222222222-01"}, "Tracestate": {"caller=x"}}
	Inject(ctx, h)
	if got := h.Get("traceparent"); !strings.Contains(got, TraceID(ctx)+"-"+SpanID(ctx)) || h.Get("tracestate") != "" {
		t.Fatalf("inject: %v", h)
	}
	_ = tel
	off := *current.Load()
	off.propagate = false
	current.Store(&off)
	h = http.Header{"Traceparent": {"00-11111111111111111111111111111111-2222222222222222-01"}}
	Inject(ctx, h)
	if h.Get("traceparent") != "" {
		t.Fatalf("propagation off must strip: %v", h)
	}
}

func TestClientInjectsWithoutMutatingTheRequest(t *testing.T) {
	restore := UseTracerProviderForTest(idOnlyProvider(), true)
	defer restore()
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Get("traceparent") }))
	defer upstream.Close()
	ctx, span := Tracer().Start(context.Background(), "c")
	defer span.End()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, nil)
	client := Client(upstream.Client())
	if Client(client) != client {
		t.Fatal("wrapping twice")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(got, SpanID(ctx)) || req.Header.Get("traceparent") != "" {
		t.Fatalf("upstream got %q; caller request mutated: %v", got, req.Header)
	}
}

func TestMiddlewareNamesByRouteAndNeverRecordsTheQuery(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	if _, err := Setup(context.Background(), Options{Getenv: env(nil), SpanExporter: exporter}); err != nil {
		t.Fatal(err)
	}
	previous := current.Load()
	defer func() { current.Store(previous) }()
	h := Middleware(func(*http.Request) string { return "/ap/{apid}/*" })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }))
	r := httptest.NewRequest(http.MethodPost, "/ap/one/v1/chat?key=sk-planted", nil)
	h.ServeHTTP(httptest.NewRecorder(), r)
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "POST /ap/{apid}/*" || spans[0].SpanKind != trace.SpanKindServer {
		t.Fatalf("spans: %+v", spans)
	}
	for _, kv := range spans[0].Attributes {
		if strings.Contains(kv.Value.Emit(), "sk-planted") || strings.Contains(kv.Value.Emit(), "/ap/one") {
			t.Fatalf("raw path or query recorded: %s=%s", kv.Key, kv.Value.Emit())
		}
	}
}

func TestExchangeAttributesOmitUnknowns(t *testing.T) {
	e := Exchange{Plane: usageledger.PlaneLLM, OperationName: OpChat, RequestModel: "m", Status: "ok", Cost: usageledger.UnknownCost(), Usage: &usageledger.Usage{InputTokens: usageledger.Int64(0)}}
	got := map[string]string{}
	for _, kv := range e.Attributes() {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	if got[AttrGenAIUsageInput] != "0" {
		t.Fatalf("a reported zero stays zero: %v", got)
	}
	for _, k := range []string{AttrGenAIUsageOutput, AttrCostAmount, AttrCostCurrency, AttrErrorType, AttrPrincipalID} {
		if _, ok := got[k]; ok {
			t.Fatalf("unknown %s recorded: %v", k, got)
		}
	}
	if e.SpanName() != "chat m" {
		t.Fatalf("span name %q", e.SpanName())
	}
	tool := Exchange{Plane: usageledger.PlaneMCP, MCPMethod: "tools/call", ToolName: "echo"}
	if tool.SpanName() != "tools/call echo" {
		t.Fatalf("mcp span name %q", tool.SpanName())
	}
}

func TestMetricsAreBoundedAndServedAsPrometheusText(t *testing.T) {
	tel := setup(t, nil)
	cost := usageledger.Cost{Basis: usageledger.BasisEstimated, Currency: "USD", Amount: "14", Scale: 6}
	Metrics().RecordExchange(context.Background(), Exchange{Plane: usageledger.PlaneLLM, OperationName: OpChat, ProviderName: ProviderOpenAI, RequestModel: "m", AccessPointID: "ap", CallerKind: "user", PrincipalID: "alice@example.com", RequestID: "req-1", RecordID: "ul_x", Status: "ok", Duration: time.Second, Usage: &usageledger.Usage{InputTokens: usageledger.Int64(3), OutputTokens: usageledger.Int64(2)}, Cost: cost})
	Metrics().RecordExchange(context.Background(), Exchange{Plane: usageledger.PlaneLLM, OperationName: OpChat, RequestModel: "m", Status: "denied", ErrorType: "quota_budget_exhausted", QuotaDenied: true, Cost: usageledger.UnknownCost()})
	Metrics().RecordHTTP(context.Background(), "GET", "/healthz", 200, 0.01)
	rec := httptest.NewRecorder()
	tel.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	text := string(body)
	for _, want := range []string{"gen_ai_client_inference_usage_input_tokens", "gen_ai_client_inference_duration_seconds", "aiplex_usage_cost", `aiplex_cost_currency="USD"`, "aiplex_usage_unknown_cost", "aiplex_quota_denials", "http_server_request_duration_seconds"} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics lack %s:\n%s", want, text)
		}
	}
	for _, leak := range []string{"alice@example.com", "req-1", "ul_x", "trace_id"} {
		if strings.Contains(text, leak) {
			t.Errorf("metrics carry a per-request identifier %q", leak)
		}
	}
}

func TestModelOperationNames(t *testing.T) {
	for id, want := range map[string]string{
		"openai.v1.chat-completions": OpChat, "anthropic.messages": OpChat, "bedrock.converse-stream": OpChat, "openai.v1.responses": OpChat,
		"models.generateContent": OpGenerateContent, "gemini.models.streamGenerateContent": OpGenerateContent, "openai.v1.embeddings": OpEmbeddings,
		"openai.v1.realtime": OpRealtime, "live.connect": OpRealtime, "gemini.live.connect": OpRealtime, "openai.v1.responses-retrieve": OpFetchResponse, "models.countTokens": "models.countTokens",
	} {
		if got := ModelOperationName(id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
}

func TestRedact(t *testing.T) {
	if got := Redact("post failed: Authorization=Bearer abc123", []string{"Bearer abc123"}); strings.Contains(got, "abc123") {
		t.Fatal(got)
	}
}
