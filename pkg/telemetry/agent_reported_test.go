package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func prometheusText(t *testing.T, tel *Telemetry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	tel.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	return string(body)
}

// The GenAI token counters are told apart by gen_ai.operation.name, so an
// agent exchange with known final usage is counted under invoke_agent, and an
// agent exchange without usage adds nothing (unknown is not zero).
func TestAgentExchangeTokensAreCountedOnlyWhenKnown(t *testing.T) {
	tel := setup(t, nil)
	known := Exchange{Plane: usageledger.PlaneA2A, OperationName: OpInvokeAgent, ProviderName: ProviderGCPVertexAI, ResourceID: "agent-1", Status: "ok", Duration: time.Second,
		Usage: &usageledger.Usage{InputTokens: usageledger.Int64(11), OutputTokens: usageledger.Int64(7)}, Cost: usageledger.UnknownCost()}
	Metrics().RecordExchange(context.Background(), known)
	unknown := Exchange{Plane: usageledger.PlaneA2A, OperationName: OpInvokeAgent, ProviderName: ProviderGCPVertexAI, ResourceID: "agent-2", Status: "ok", Cost: usageledger.UnknownCost()}
	Metrics().RecordExchange(context.Background(), unknown)
	text := prometheusText(t, tel)
	var input, unknownUsage []string
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "gen_ai_client_inference_usage_input_tokens_total"):
			input = append(input, line)
		case strings.HasPrefix(line, "aiplex_usage_unknown_usage_total"):
			unknownUsage = append(unknownUsage, line)
		}
	}
	if len(input) != 1 || !strings.Contains(input[0], `gen_ai_operation_name="invoke_agent"`) || !strings.HasSuffix(input[0], " 11") {
		t.Fatalf("agent input tokens: %v\n%s", input, text)
	}
	if len(unknownUsage) != 1 || !strings.HasSuffix(unknownUsage[0], " 1") {
		t.Fatalf("the exchange without usage is counted as unknown usage, not as zero tokens: %v", unknownUsage)
	}
	if !strings.Contains(text, "gen_ai_client_invoke_agent_duration_seconds") {
		t.Fatal("the invoke_agent duration metric is missing")
	}
	// The agent span names the agent by its id and keeps the operation name.
	attrs := map[string]string{}
	for _, kv := range known.Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	if known.SpanName() != "invoke_agent" || attrs[AttrAgentID] != "agent-1" || attrs[AttrGenAIOperationName] != "invoke_agent" || attrs[AttrGenAIUsageInput] != "11" {
		t.Fatalf("%q %v", known.SpanName(), attrs)
	}
	// An MCP exchange never counts tokens.
	mcp := Exchange{Plane: usageledger.PlaneMCP, MCPMethod: "tools/call", ToolName: "echo", Status: "ok", Usage: &usageledger.Usage{InputTokens: usageledger.Int64(99)}, Cost: usageledger.UnknownCost()}
	Metrics().RecordExchange(context.Background(), mcp)
	if strings.Contains(prometheusText(t, tel), "} 110") {
		t.Fatal("MCP tokens were counted")
	}
}

func TestOTLPAndTagCountersAreBounded(t *testing.T) {
	tel := setup(t, nil)
	Metrics().RecordOTLP(context.Background(), "logs", "claude-code", OTLPRecorded, 3)
	Metrics().RecordOTLP(context.Background(), "logs", "claude-code", OTLPDuplicate, 0)
	Metrics().RecordTagRejected(context.Background(), TagOverflow, 2)
	text := prometheusText(t, tel)
	for _, want := range []string{"aiplex_otlp_ingest_total", `aiplex_otlp_outcome="recorded"`, `aiplex_harness="claude-code"`, "aiplex_usage_tags_rejected_total", `aiplex_tag_reason="overflow"`} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics lack %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, `outcome="duplicate"`) {
		t.Error("a zero count must not create a series")
	}
}

func TestRemoteParentAndTraceContextFields(t *testing.T) {
	h := http.Header{}
	h.Set("Traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	if trace, span := RemoteParent(h); trace != "0af7651916cd43dd8448eb211c80319c" || span != "b7ad6b7169203331" {
		t.Fatalf("%s %s", trace, span)
	}
	h.Set("Traceparent", "garbage")
	if trace, span := RemoteParent(h); trace != "" || span != "" {
		t.Fatalf("an invalid traceparent is no parent: %s %s", trace, span)
	}
	tid, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	sid, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	if tp, ts, ok := TraceContextFields(ctx); !ok || tp != "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01" || ts != "" {
		t.Fatalf("%s %s %v", tp, ts, ok)
	}
	if _, _, ok := TraceContextFields(context.Background()); ok {
		t.Fatal("no span, no trace context")
	}
	defer UseTracerProviderForTest(trace.NewNoopTracerProvider(), false)()
	if _, _, ok := TraceContextFields(ctx); ok {
		t.Fatal("with upstream propagation off no trace context is produced")
	}
}
