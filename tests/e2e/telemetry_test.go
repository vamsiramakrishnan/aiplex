//go:build e2e

package e2e

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// otlpReceiver is a tiny in-process OTLP/HTTP receiver: it decodes
// protobuf trace exports and accepts (and ignores) metric exports.
type otlpReceiver struct {
	mu    sync.Mutex
	spans []receivedSpan
}

type receivedSpan struct {
	TraceID, SpanID, ParentID string
	Name                      string
	Kind                      tracev1.Span_SpanKind
	Attrs                     map[string]string
}

func (o *otlpReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "gzip", 400)
			return
		}
		body = zr
	}
	raw, err := io.ReadAll(io.LimitReader(body, 32<<20))
	if err != nil {
		http.Error(w, "read", 400)
		return
	}
	if r.URL.Path != "/v1/traces" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req collectortrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		http.Error(w, "decode", 400)
		return
	}
	o.mu.Lock()
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				attrs := map[string]string{}
				for _, kv := range s.Attributes {
					attrs[kv.Key] = anyValue(kv.Value)
				}
				o.spans = append(o.spans, receivedSpan{TraceID: hex.EncodeToString(s.TraceId), SpanID: hex.EncodeToString(s.SpanId), ParentID: hex.EncodeToString(s.ParentSpanId), Name: s.Name, Kind: s.Kind, Attrs: attrs})
			}
		}
		for _, kv := range rs.GetResource().GetAttributes() {
			o.spans = append(o.spans, receivedSpan{Name: "resource", Attrs: map[string]string{kv.Key: anyValue(kv.Value)}})
		}
	}
	o.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func anyValue(v *commonv1.AnyValue) string {
	switch x := v.GetValue().(type) {
	case *commonv1.AnyValue_StringValue:
		return x.StringValue
	case *commonv1.AnyValue_IntValue:
		return fmt.Sprint(x.IntValue)
	case *commonv1.AnyValue_BoolValue:
		return fmt.Sprint(x.BoolValue)
	case *commonv1.AnyValue_DoubleValue:
		return fmt.Sprint(x.DoubleValue)
	}
	return v.String()
}

func (o *otlpReceiver) all() []receivedSpan {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]receivedSpan(nil), o.spans...)
}

func (o *otlpReceiver) byID(id string) (receivedSpan, bool) {
	for _, s := range o.all() {
		if s.SpanID == id {
			return s, true
		}
	}
	return receivedSpan{}, false
}

type ledgerRecord struct {
	ID        string `json:"id"`
	Plane     string `json:"plane"`
	Operation string `json:"operation"`
	ModelID   string `json:"model_id"`
	ToolName  string `json:"tool_name"`
	TraceID   string `json:"trace_id"`
	SpanID    string `json:"span_id"`
	Streaming bool   `json:"streaming"`
	Usage     *struct {
		InputTokens  *int64 `json:"input_tokens"`
		OutputTokens *int64 `json:"output_tokens"`
	} `json:"usage"`
}

// TestTelemetryEndToEnd runs a JSON chat, a streamed chat and an MCP tool call
// through the seeded access point with OTLP export to a local receiver, and
// checks spans, parent links, upstream trace context, ledger correlation and
// that no prompt or credential reaches telemetry.
func TestTelemetryEndToEnd(t *testing.T) {
	receiver := &otlpReceiver{}
	srv, receiverURL, err := devstack.Listen(receiver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	e := start(t, func(o *devstack.Options) {
		o.Env = append(o.Env, "OTEL_EXPORTER_OTLP_ENDPOINT="+receiverURL, "OTEL_BSP_SCHEDULE_DELAY=100", "OTEL_METRIC_EXPORT_INTERVAL=500")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	seeded, err := e.stack.Seed(ctx, devstack.SeedOptions{})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	contains(t, e.log.String(), "telemetry configured", "API startup log")

	const marker = "telemetry-planted-prompt-5e3a"
	const callerTrace = "0af7651916cd43dd8448eb211c80319c"
	tp := func(span string) string { return "00-" + callerTrace + "-" + span + "-01" }
	path := "/ap/" + seeded.AccessPoint + "/v1/chat/completions"

	chat := e.post(path, `{"model":"mock-gpt","messages":[{"role":"user","content":"hello `+marker+`"}]}`, "traceparent", tp("b7ad6b7169203331"))
	if chat.Status != 200 {
		t.Fatalf("chat: %d %s", chat.Status, chat.Body)
	}
	stream := e.post(path, `{"model":"mock-gpt","stream":true,"messages":[{"role":"user","content":"stream `+marker+`"}]}`)
	if stream.Status != 200 || len(sseData(stream.Body)) == 0 {
		t.Fatalf("stream: %d %s", stream.Status, stream.Body)
	}
	mcp := &mcpClient{e: e, path: "/ap/" + seeded.AccessPoint + "/mcp", extraHdr: []string{"traceparent", tp("c7ad6b7169203332")}}
	mcp.initialize("2025-06-18")
	if got := callText(t, mcp.call(seeded.Connection+"__echo", map[string]any{"text": marker})); got != marker {
		t.Fatalf("echo = %q", got)
	}

	// Ledger records carry the ids of the exchange's CLIENT span.
	var records []ledgerRecord
	eventually(t, 10*time.Second, func() error {
		r := e.do("GET", "/api/v1/usage/records?limit=50", "", e.auth()...)
		var page struct {
			Records []ledgerRecord `json:"records"`
		}
		if err := json.Unmarshal([]byte(r.Body), &page); err != nil {
			return err
		}
		records = page.Records
		chats, tools := 0, 0
		for _, rec := range records {
			switch {
			case rec.Plane == "llmplex":
				chats++
			case rec.Plane == "mcplex" && rec.ToolName == "echo":
				tools++
			}
		}
		if chats < 2 || tools < 1 {
			return fmt.Errorf("records so far: %+v", records)
		}
		return nil
	})
	var chatRec, streamRec, toolRec ledgerRecord
	for _, rec := range records {
		if rec.TraceID == "" || rec.SpanID == "" {
			t.Fatalf("record without trace or span id: %+v", rec)
		}
		switch {
		case rec.Plane == "llmplex" && rec.Streaming:
			streamRec = rec
		case rec.Plane == "llmplex":
			chatRec = rec
		case rec.Plane == "mcplex" && rec.ToolName == "echo":
			toolRec = rec
		}
	}
	if chatRec.TraceID != callerTrace || toolRec.TraceID != callerTrace {
		t.Fatalf("records must continue the caller's trace: chat %s tool %s", chatRec.TraceID, toolRec.TraceID)
	}
	if streamRec.TraceID == callerTrace || streamRec.ID == "" {
		t.Fatalf("a request without traceparent starts its own trace: %+v", streamRec)
	}

	// Spans arrive at the receiver with the expected names, kinds, parents
	// and attributes.
	var chatSpan, streamSpan, toolSpan receivedSpan
	eventually(t, 15*time.Second, func() error {
		var ok1, ok2, ok3 bool
		chatSpan, ok1 = receiver.byID(chatRec.SpanID)
		streamSpan, ok2 = receiver.byID(streamRec.SpanID)
		toolSpan, ok3 = receiver.byID(toolRec.SpanID)
		if !ok1 || !ok2 || !ok3 {
			return fmt.Errorf("spans not yet exported (%d received)", len(receiver.all()))
		}
		for _, s := range []receivedSpan{chatSpan, streamSpan, toolSpan} {
			if _, ok := receiver.byID(s.ParentID); !ok {
				return fmt.Errorf("parent of %q not yet exported", s.Name)
			}
		}
		return nil
	})
	for _, s := range []receivedSpan{chatSpan, streamSpan, toolSpan} {
		if s.Kind != tracev1.Span_SPAN_KIND_CLIENT {
			t.Errorf("%q kind %v, want CLIENT", s.Name, s.Kind)
		}
		parent, _ := receiver.byID(s.ParentID)
		if parent.Kind != tracev1.Span_SPAN_KIND_SERVER || parent.TraceID != s.TraceID || !strings.HasPrefix(parent.Name, "POST /ap/{apid}") {
			t.Errorf("%q parent %q (kind %v); want the request's SERVER span named by route", s.Name, parent.Name, parent.Kind)
		}
		if parent.Attrs["http.route"] == "" || parent.Attrs["http.response.status_code"] != "200" {
			t.Errorf("server span attributes: %v", parent.Attrs)
		}
	}
	if chatSpan.Name != "chat "+chatRec.ModelID || chatSpan.Attrs["gen_ai.operation.name"] != "chat" || chatSpan.Attrs["gen_ai.request.model"] != chatRec.ModelID || chatSpan.Attrs["gen_ai.provider.name"] == "" {
		t.Errorf("chat span %q: %v", chatSpan.Name, chatSpan.Attrs)
	}
	if chatRec.Usage != nil && chatRec.Usage.InputTokens != nil && chatSpan.Attrs["gen_ai.usage.input_tokens"] != fmt.Sprint(*chatRec.Usage.InputTokens) {
		t.Errorf("chat span input tokens %q, record %d", chatSpan.Attrs["gen_ai.usage.input_tokens"], *chatRec.Usage.InputTokens)
	}
	if chatSpan.Attrs["aiplex.access_point.id"] != seeded.AccessPoint || chatSpan.Attrs["aiplex.usage.record_id"] != chatRec.ID || chatSpan.Attrs["aiplex.cost.basis"] == "" {
		t.Errorf("chat span AIPlex attributes: %v", chatSpan.Attrs)
	}
	if streamSpan.Attrs["gen_ai.request.stream"] != "true" || streamSpan.Attrs["gen_ai.usage.output_tokens"] == "" {
		t.Errorf("stream span must end with the final usage: %v", streamSpan.Attrs)
	}
	if toolSpan.Name != "tools/call echo" || toolSpan.Attrs["mcp.method.name"] != "tools/call" || toolSpan.Attrs["gen_ai.tool.name"] != "echo" || toolSpan.Attrs["gen_ai.operation.name"] != "execute_tool" {
		t.Errorf("tool span %q: %v", toolSpan.Name, toolSpan.Attrs)
	}

	t.Logf("chat span %q %v", chatSpan.Name, chatSpan.Attrs)
	t.Logf("tool span %q %v", toolSpan.Name, toolSpan.Attrs)
	// The upstreams received the CLIENT span's context.
	var upstreamTP []string
	for _, req := range e.stack.Models.Requests() {
		upstreamTP = append(upstreamTP, req.Traceparent)
	}
	for _, want := range []string{tp(chatRec.SpanID), "-" + streamRec.SpanID + "-"} {
		if !containsSub(upstreamTP, want) {
			t.Errorf("model upstream did not receive %q: %v", want, upstreamTP)
		}
	}
	if !containsSub(e.stack.MCP.Traceparents(), tp(toolRec.SpanID)) {
		t.Errorf("MCP upstream did not receive the tool span context: %v", e.stack.MCP.Traceparents())
	}

	// No prompt, tool argument or credential in any exported span.
	for _, s := range receiver.all() {
		for k, v := range s.Attrs {
			for _, secret := range []string{marker, e.stack.Token, "mock-credential-not-a-secret"} {
				if strings.Contains(v, secret) || strings.Contains(s.Name, secret) {
					t.Fatalf("span %q attribute %s leaked a planted secret", s.Name, k)
				}
			}
		}
	}

	// /metrics serves Prometheus text (development mode, no metrics token).
	m := e.do("GET", "/metrics", "")
	if m.Status != 200 || !strings.Contains(m.Body, "gen_ai_client_inference_duration_seconds") || !strings.Contains(m.Body, "mcp_client_operation_duration_seconds") {
		t.Fatalf("metrics: %d\n%s", m.Status, m.Body)
	}
	if strings.Contains(m.Body, chatRec.TraceID) || strings.Contains(m.Body, chatRec.ID) {
		t.Fatal("metrics carry per-request identifiers")
	}
}

func containsSub(list []string, sub string) bool {
	for _, v := range list {
		if strings.Contains(v, sub) {
			return true
		}
	}
	return false
}
