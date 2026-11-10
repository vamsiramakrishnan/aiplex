package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// spanMarker is planted in the prompt, the caller's credential, the query
// string and a provider-key-shaped header; no exported span may contain it.
const spanMarker = "planted-secret-7c1d"

type upstreamTrace struct {
	mu          sync.Mutex
	traceparent []string
}

func (u *upstreamTrace) seen() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.traceparent...)
}

func installSpans(t *testing.T, propagate bool) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(telemetry.UseTracerProviderForTest(tp, propagate))
	return exporter
}

func spanKV(s tracetest.SpanStub) map[string]string {
	out := map[string]string{}
	for _, kv := range s.Attributes {
		out[string(kv.Key)] = kv.Value.Emit()
	}
	return out
}

func findSpan(t *testing.T, spans tracetest.SpanStubs, kind trace.SpanKind) tracetest.SpanStub {
	t.Helper()
	for _, s := range spans {
		if s.SpanKind == kind {
			return s
		}
	}
	t.Fatalf("no %s span among %d", kind, len(spans))
	return tracetest.SpanStub{}
}

func assertNoMarker(t *testing.T, spans tracetest.SpanStubs, markers ...string) {
	t.Helper()
	for _, s := range spans {
		values := []string{s.Name, s.Status.Description}
		for _, kv := range s.Attributes {
			values = append(values, string(kv.Key), kv.Value.Emit())
		}
		for _, e := range s.Events {
			values = append(values, e.Name)
			for _, kv := range e.Attributes {
				values = append(values, kv.Value.Emit())
			}
		}
		for _, v := range values {
			for _, m := range markers {
				if strings.Contains(v, m) {
					t.Fatalf("span %q leaked %q in %q", s.Name, m, v)
				}
			}
		}
	}
}

func TestTelemetryModelExchangeSpans(t *testing.T) {
	exporter := installSpans(t, true)
	upstream := &upstreamTrace{}
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		upstream.mu.Lock()
		upstream.traceparent = append(upstream.traceparent, r.Header.Get("traceparent"))
		upstream.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, quotaHostModelTerminal)
	}, nil)
	f.handler.Quota = nil
	records := attachLedger(t, f.handler)
	putModelPrice(t, f.resources, usageTestPrice)

	const callerTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	body := strings.Replace(quotaHostModelBody, "hello", "hello "+spanMarker, 1)
	r := f.request("generateContent", "", body, false)
	r.URL.RawQuery = "key=" + spanMarker
	r.Header.Set("Authorization", "Bearer "+spanMarker)
	r.Header.Set("X-Goog-Api-Key", spanMarker)
	r.Header.Set("traceparent", "00-"+callerTrace+"-00f067aa0ba902b7-01")
	w := httptest.NewRecorder()
	telemetry.Middleware(func(*http.Request) string { return "/api/v1/llm/native/{id}/*" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.serve(w, r, false)
	})).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("exchange: %d %s", w.Code, w.Body.String())
	}
	record := onlyRecord(t, records())
	spans := exporter.GetSpans()
	server := findSpan(t, spans, trace.SpanKindServer)
	client := findSpan(t, spans, trace.SpanKindClient)

	if server.Name != "POST /api/v1/llm/native/{id}/*" || server.SpanContext.TraceID().String() != callerTrace || server.Parent.SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("server span must continue the caller's trace under the route name: %q %s parent %s", server.Name, server.SpanContext.TraceID(), server.Parent.SpanID())
	}
	if client.Parent.SpanID() != server.SpanContext.SpanID() || client.SpanContext.TraceID() != server.SpanContext.TraceID() {
		t.Fatal("client span must be the server span's child")
	}
	if record.TraceID != callerTrace || record.SpanID != client.SpanContext.SpanID().String() {
		t.Fatalf("ledger record ids %s/%s, client span %s", record.TraceID, record.SpanID, client.SpanContext.SpanID())
	}
	seen := upstream.seen()
	if len(seen) != 1 || seen[0] != "00-"+callerTrace+"-"+client.SpanContext.SpanID().String()+"-01" {
		t.Fatalf("upstream traceparent %q, want the client span's context", seen)
	}
	attrs := spanKV(client)
	want := map[string]string{
		"gen_ai.operation.name": "generate_content", "gen_ai.provider.name": "gcp.vertex_ai", "gen_ai.request.model": "model-v1",
		"gen_ai.response.model": "model-v1", "gen_ai.usage.input_tokens": "3", "gen_ai.usage.output_tokens": "2",
		"gen_ai.usage.cache_read.input_tokens": "1", "gen_ai.usage.reasoning.output_tokens": "1", "gen_ai.request.stream": "false",
		"aiplex.plane": "llmplex", "aiplex.resource.id": "surface", "aiplex.principal.id": record.PrincipalID,
		"aiplex.usage.record_id": record.ID, "aiplex.cost.amount": "0.000014", "aiplex.cost.currency": "USD", "aiplex.cost.basis": "estimated",
		"aiplex.status": "ok", "aiplex.request.id": "transport-correlation",
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Errorf("client span %s = %q, want %q", k, attrs[k], v)
		}
	}
	// Unknown is absent, never zero.
	for _, k := range []string{"gen_ai.usage.cache_write.input_tokens", "error.type", "aiplex.quota.attempt_id"} {
		if _, ok := attrs[k]; ok {
			t.Errorf("client span has %s=%q for an unknown or absent value", k, attrs[k])
		}
	}
	if client.Name != "generate_content model-v1" {
		t.Errorf("client span name %q", client.Name)
	}
	assertNoMarker(t, spans, spanMarker, "operator-owned-upstream-token", "native answer")
}

func TestTelemetryUnknownUsageAndCostAreAbsent(t *testing.T) {
	exporter := installSpans(t, false)
	upstream := &upstreamTrace{}
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		upstream.mu.Lock()
		upstream.traceparent = append(upstream.traceparent, r.Header.Get("traceparent"))
		upstream.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"index":0,"content":{"parts":[{"text":"x"}]}}]}`)
	}, nil)
	f.handler.Quota = nil
	records := attachLedger(t, f.handler)
	r := f.request("generateContent", "", quotaHostModelBody, false)
	// With upstream propagation off a caller's traceparent is not relayed.
	r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	w := httptest.NewRecorder()
	f.serve(w, r, false)
	record := onlyRecord(t, records())
	if record.TraceID == "" || record.SpanID == "" {
		t.Fatalf("every record gets trace and span ids, even without a server span: %+v", record)
	}
	if seen := upstream.seen(); len(seen) != 1 || seen[0] != "" {
		t.Fatalf("propagation off must send no traceparent, got %q", seen)
	}
	client := findSpan(t, exporter.GetSpans(), trace.SpanKindClient)
	attrs := spanKV(client)
	for k := range attrs {
		if strings.HasPrefix(k, "gen_ai.usage.") || k == "aiplex.cost.amount" || k == "aiplex.cost.currency" {
			t.Errorf("unknown usage or cost recorded as %s=%q", k, attrs[k])
		}
	}
	if attrs["aiplex.cost.basis"] != usageledger.BasisUnknown {
		t.Errorf("cost basis %q", attrs["aiplex.cost.basis"])
	}
}

func TestTelemetryStreamSpanEndsWithFinalUsage(t *testing.T) {
	exporter := installSpans(t, true)
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"a\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3}}\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: {\"modelVersion\":\"model-v1\",\"candidates\":[{\"index\":0,\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"totalTokenCount\":5}}\n\n")
	}, nil)
	f.handler.Quota = nil
	records := attachLedger(t, f.handler)
	w := httptest.NewRecorder()
	f.serve(w, f.request("streamGenerateContent", "", quotaHostModelBody, false), false)
	record := onlyRecord(t, records())
	if !record.Streaming {
		t.Fatalf("stream record: %+v", record)
	}
	client := findSpan(t, exporter.GetSpans(), trace.SpanKindClient)
	attrs := spanKV(client)
	if attrs["gen_ai.request.stream"] != "true" || attrs["gen_ai.usage.output_tokens"] != "2" || attrs["gen_ai.usage.input_tokens"] != "3" {
		t.Fatalf("stream span attributes: %v", attrs)
	}
	if _, ok := attrs["gen_ai.response.time_to_first_chunk"]; !ok {
		t.Errorf("streamed exchange should record time to first chunk: %v", attrs)
	}
	if !client.EndTime.Equal(record.FinishedAt) {
		t.Errorf("span end %s, record finish %s", client.EndTime, record.FinishedAt)
	}
}
