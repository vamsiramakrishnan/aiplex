package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

type seenBody struct {
	mu          sync.Mutex
	bodies      []string
	traceparent []string
}

func (s *seenBody) add(body, traceparent string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, body)
	s.traceparent = append(s.traceparent, traceparent)
}

func (s *seenBody) last() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		return "", ""
	}
	return s.bodies[len(s.bodies)-1], s.traceparent[len(s.traceparent)-1]
}

func metaOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var req struct {
		Params struct {
			Meta map[string]any `json:"_meta"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("upstream body is not JSON: %v: %s", err, body)
	}
	return req.Params.Meta
}

// A managed tool call carries the exchange's span context in params._meta as
// well as in the HTTP header, replacing what the caller put there, and
// relays the rest of the request unchanged.
func TestManagedToolCallCarriesTraceContextInMeta(t *testing.T) {
	for _, propagate := range []bool{true, false} {
		exporter := installSpans(t, propagate)
		seen := &seenBody{}
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			seen.add(string(raw), r.Header.Get("traceparent"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, managedToolResponse)
		}))
		t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
		h, _, request := protectedToolFixture(t, upstream.URL+"/mcp")
		h.Quota = nil
		records := attachLedger(t, h)

		// Large integers, floats, unicode and key order must survive; the
		// caller's own trace context in _meta must not.
		body := `{"jsonrpc":"2.0","id":"rpc","method":"tools/call","params":{"name":"search","arguments":{"query":"héllo 😀","n":123456789012345678901234567890,"f":1.0e+100},"_meta":{"progressToken":7,"traceparent":"00-11111111111111111111111111111111-2222222222222222-01","baggage":"k=v"}}}`
		r := request("k")
		r.Body = io.NopCloser(strings.NewReader(body))
		w := httptest.NewRecorder()
		h.NativeTool(w, r)
		upstream.Close()
		if w.Code != 200 || w.Body.String() != managedToolResponse {
			t.Fatalf("propagate=%v: %d %s", propagate, w.Code, w.Body.String())
		}
		got, header := seen.last()
		meta := metaOf(t, got)
		record := onlyRecord(t, records())
		client := findSpan(t, exporter.GetSpans(), trace.SpanKindClient)
		if propagate {
			want := "00-" + client.SpanContext.TraceID().String() + "-" + client.SpanContext.SpanID().String() + "-01"
			if meta["traceparent"] != want || header != want {
				t.Fatalf("_meta %v header %q, want the exchange's CLIENT span %s", meta, header, want)
			}
			if record.SpanID != client.SpanContext.SpanID().String() || record.TraceID != client.SpanContext.TraceID().String() {
				t.Fatalf("ledger ids do not match the span in _meta: %+v", record)
			}
			if meta["progressToken"] != float64(7) || meta["baggage"] != nil {
				t.Fatalf("other _meta keys are kept, baggage is not forwarded: %v", meta)
			}
		} else {
			if meta["traceparent"] != nil || meta["tracestate"] != nil || meta["baggage"] != nil || header != "" || meta["progressToken"] != float64(7) {
				t.Fatalf("propagation off must strip trace context everywhere: _meta %v header %q", meta, header)
			}
		}
		// The arguments are relayed byte for byte.
		const arguments = `"arguments":{"query":"héllo 😀","n":123456789012345678901234567890,"f":1.0e+100}`
		prefix := `{"jsonrpc":"2.0","id":"rpc","method":"tools/call","params":{"name":"search",` + arguments + `,"_meta":{"progressToken":7`
		if !strings.Contains(got, arguments) || !strings.HasPrefix(got, prefix) {
			t.Fatalf("the request changed beyond _meta: %s", got)
		}
	}
}

// Through an access point the request is rebuilt from name and arguments; the
// upstream still receives the exchange's trace context in _meta.
func TestAccessPointToolCallCarriesTraceContextInMeta(t *testing.T) {
	exporter := installSpans(t, true)
	f := newAPFixture(t)
	records := attachLedger(t, f.h)
	w := mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"github__search","arguments":{"q":"x"},"_meta":{"progressToken":1,"traceparent":"00-11111111111111111111111111111111-2222222222222222-01"}}}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	calls := f.upstreamCalls()
	body := calls[len(calls)-1].Body
	meta := metaOf(t, body)
	records() // the span ends once the ledger write completes
	client := findSpan(t, exporter.GetSpans(), trace.SpanKindClient)
	want := "00-" + client.SpanContext.TraceID().String() + "-" + client.SpanContext.SpanID().String() + "-01"
	if meta["traceparent"] != want || strings.Contains(body, "1111111111") {
		t.Fatalf("_meta %v, want %s", meta, want)
	}
}

// Agent exchanges have their own CLIENT span named invoke_agent, a child of
// the request, whose attributes follow the GenAI agent conventions. Usage the
// runtime did not report is absent, not zero.
func TestAgentExchangeSpan(t *testing.T) {
	exporter := installSpans(t, true)
	seen := &seenBody{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.add("", r.Header.Get("traceparent"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"native\":true}\n\n")
	}))
	defer server.Close()
	t.Setenv("QUOTA_AGENT_TOKEN", "native-agent-token")
	h, _, _, request := protectedAgentFixture(t, server)
	h.Quota = nil
	records := attachLedger(t, h)
	w := httptest.NewRecorder()
	h.InvokeAgent(w, request("first"))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	record := onlyRecord(t, records())
	client := findSpan(t, exporter.GetSpans(), trace.SpanKindClient)
	attrs := spanKV(client)
	if client.Name != "invoke_agent" || attrs["gen_ai.operation.name"] != "invoke_agent" || attrs["gen_ai.agent.id"] != "surface" || attrs["aiplex.plane"] != "a2aplex" || attrs["aiplex.usage.record_id"] != record.ID {
		t.Fatalf("%q %v", client.Name, attrs)
	}
	if attrs["gen_ai.provider.name"] == "" || attrs["aiplex.status"] != "ok" {
		t.Fatalf("%v", attrs)
	}
	for _, k := range []string{"gen_ai.usage.input_tokens", "gen_ai.usage.output_tokens", "aiplex.cost.amount"} {
		if _, ok := attrs[k]; ok {
			t.Errorf("unknown %s recorded: %v", k, attrs)
		}
	}
	if record.SpanID != client.SpanContext.SpanID().String() {
		t.Fatalf("record span %s, client span %s", record.SpanID, client.SpanContext.SpanID())
	}
	if _, header := seen.last(); header != "00-"+client.SpanContext.TraceID().String()+"-"+client.SpanContext.SpanID().String()+"-01" {
		t.Fatalf("the agent runtime receives the CLIENT span's context: %q", header)
	}
	if record.Plane != usageledger.PlaneA2A || record.Usage != nil {
		t.Fatalf("%+v", record)
	}
}
