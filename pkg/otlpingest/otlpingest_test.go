package otlpingest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// 2026-11-10T12:00:00Z, the timestamp of the first record in the testdata.
var testNow = time.Unix(0, 1794312000000000000).UTC().Add(time.Minute)

var testSender = Sender{TenantID: "tenant-a", PrincipalID: "alice@example.com", PrincipalDisplay: "alice@example.com", ClientID: "claude-code-client", CallerKind: "user"}

func load(t *testing.T, name string) *collogs.ExportLogsServiceRequest {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var req collogs.ExportLogsServiceRequest
	if err := Unmarshal(KindJSON, raw, &req); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &req
}

func TestClaudeCodeEventsMapToRecords(t *testing.T) {
	res := MapLogs(load(t, "claude_code_logs.json"), testSender, testNow, 0)
	if len(res.Records) != 2 || res.Rejected != 0 || res.Unmapped != 2 {
		t.Fatalf("records=%d rejected=%d unmapped=%d errors=%v", len(res.Records), res.Rejected, res.Unmapped, res.Errors)
	}
	r := res.Records[0]
	if r.Source != usageledger.SourceReported || r.Plane != usageledger.PlaneLLM || r.Harness != HarnessClaudeCode || r.HarnessVersion != "2.1.312" || r.SessionID != "session-aaa" || r.ModelID != "claude-sonnet-4-5" || r.Status != usageledger.StatusOK {
		t.Fatalf("record: %+v", r)
	}
	// Identity is the verified sender, never the payload.
	if r.TenantID != "tenant-a" || r.PrincipalID != "alice@example.com" || r.ClientID != "claude-code-client" || r.CallerKind != "user" || r.AccessPointID != "" {
		t.Fatalf("identity: %+v", r)
	}
	u := r.Usage
	// input_tokens excludes cache tokens in Claude Code; the ledger's is total input.
	if u == nil || *u.InputTokens != 1750 || *u.OutputTokens != 300 || *u.CacheReadTokens != 200 || *u.CacheWriteTokens != 50 || *u.TotalTokens != 2050 || u.ReasoningTokens != nil {
		t.Fatalf("usage: %+v", u)
	}
	if r.Cost != (usageledger.Cost{Basis: usageledger.BasisReported, Currency: "USD", Amount: "12346", Scale: 6}) {
		t.Fatalf("cost: %+v", r.Cost)
	}
	if r.LatencyMS != 2500 || !r.FinishedAt.Equal(time.Unix(0, 1794312000000000000)) || r.FinishedAt.Sub(r.StartedAt) != 2500*time.Millisecond {
		t.Fatalf("timing: %+v", r)
	}
	if r.TraceID != "0af7651916cd43dd8448eb211c80319c" || r.SpanID != "b7ad6b7169203331" || r.ClientRequestID != "c0ffee00-0000-4000-8000-000000000001" {
		t.Fatalf("correlation ids: %+v", r)
	}
	want := map[string]string{"event": "api_request", "prompt_id": "prompt-111", "request_id": "req_011CUabc", "query_source": "main", "speed": "normal", "effort": "medium"}
	if len(r.ReportedAttributes) != len(want) {
		t.Fatalf("reported attributes: %+v", r.ReportedAttributes)
	}
	for k, v := range want {
		if r.ReportedAttributes[k] != v {
			t.Fatalf("reported attribute %s: %+v", k, r.ReportedAttributes)
		}
	}
	raw, _ := json.Marshal(res.Records)
	for _, leak := range []string{"someone@example.com", "org-untrusted", "acct-untrusted", "spoofed-principal", "REDACTED", "secret error text"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("untrusted or sensitive value %q reached a record: %s", leak, raw)
		}
	}
	e := res.Records[1]
	if e.Status != usageledger.StatusError || e.ErrorClass != "rate_limited" || e.HTTPStatus != 429 || e.Usage != nil || e.Cost.Basis != usageledger.BasisUnknown || e.LatencyMS != 120 {
		t.Fatalf("api_error record: %+v", e)
	}
	// Unmapped events are kept for forwarding, with their resource.
	if res.UnmappedByHarness[HarnessClaudeCode] != 2 || res.Forward == nil || len(res.Forward.ResourceLogs[0].ScopeLogs[0].LogRecords) != 2 {
		t.Fatalf("unmapped: %+v forward=%v", res.UnmappedByHarness, res.Forward)
	}
}

func TestCodexResponseCompletedMapsAndOtherEventsAreUnmapped(t *testing.T) {
	res := MapLogs(load(t, "codex_logs.json"), testSender, testNow, 0)
	if len(res.Records) != 1 || res.Unmapped != 2 {
		t.Fatalf("records=%d unmapped=%d", len(res.Records), res.Unmapped)
	}
	r := res.Records[0]
	u := r.Usage
	if r.Harness != HarnessCodex || r.HarnessVersion != "0.99.0" || r.SessionID != "conv-1" || r.ModelID != "gpt-5-codex" || r.Cost.Basis != usageledger.BasisUnknown {
		t.Fatalf("record: %+v", r)
	}
	if *u.InputTokens != 1200 || *u.OutputTokens != 400 || *u.CacheReadTokens != 1000 || *u.ReasoningTokens != 128 || *u.TotalTokens != 1600 || u.CacheWriteTokens != nil {
		t.Fatalf("usage: %+v", u)
	}
	// The sse_event duration is not a request latency and is not used as one.
	if r.LatencyMS != 0 || !r.StartedAt.Equal(r.FinishedAt) {
		t.Fatalf("timing: %+v", r)
	}
}

func TestGeminiAPIResponseMapsAndToolCallsAreUnmapped(t *testing.T) {
	res := MapLogs(load(t, "gemini_cli_logs.json"), testSender, testNow, 0)
	if len(res.Records) != 1 || res.Unmapped != 1 {
		t.Fatalf("records=%d unmapped=%d", len(res.Records), res.Unmapped)
	}
	r := res.Records[0]
	u := r.Usage
	if r.Harness != HarnessGeminiCLI || r.ModelID != "gemini-2.5-pro" || r.SessionID != "gem-session-1" || r.HTTPStatus != 200 || r.LatencyMS != 1800 {
		t.Fatalf("record: %+v", r)
	}
	if *u.InputTokens != 900 || *u.OutputTokens != 250 || *u.CacheReadTokens != 300 || *u.ReasoningTokens != 75 || *u.TotalTokens != 1225 {
		t.Fatalf("usage: %+v", u)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "the model said this") || strings.Contains(string(raw), "gem@example.com") || strings.Contains(string(raw), "install-untrusted") {
		t.Fatalf("leak: %s", raw)
	}
}

func TestProtobufAndJSONDecodeToTheSameRecords(t *testing.T) {
	req := load(t, "claude_code_logs.json")
	wire, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var back collogs.ExportLogsServiceRequest
	if err := Unmarshal(KindProtobuf, wire, &back); err != nil {
		t.Fatal(err)
	}
	a, b := MapLogs(req, testSender, testNow, 0), MapLogs(&back, testSender, testNow, 0)
	if len(a.Records) != len(b.Records) {
		t.Fatal("record counts differ")
	}
	for i := range a.Records {
		ja, _ := json.Marshal(a.Records[i])
		jb, _ := json.Marshal(b.Records[i])
		if !bytes.Equal(ja, jb) {
			t.Fatalf("record %d differs:\n%s\n%s", i, ja, jb)
		}
	}
}

func TestMissingCacheCountersLeaveInputUnknown(t *testing.T) {
	req := load(t, "claude_code_logs.json")
	rec := req.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	kept := rec.Attributes[:0]
	for _, kv := range rec.Attributes {
		if kv.Key != "cache_creation_tokens" && kv.Key != "cost_usd_micros" && kv.Key != "cost_usd" {
			kept = append(kept, kv)
		}
	}
	rec.Attributes = kept
	r := MapLogs(req, testSender, testNow, 0).Records[0]
	if r.Usage.InputTokens != nil || r.Usage.TotalTokens != nil || *r.Usage.OutputTokens != 300 || *r.Usage.CacheReadTokens != 200 || r.Usage.CacheWriteTokens != nil {
		t.Fatalf("unknown counters must stay unknown: %+v", r.Usage)
	}
	if r.Cost.Basis != usageledger.BasisUnknown {
		t.Fatalf("cost must be unknown without a reported cost: %+v", r.Cost)
	}
}

func TestNegativeCountsAndBadTimestampsAreRejected(t *testing.T) {
	req := load(t, "claude_code_logs.json")
	for _, kv := range req.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Attributes {
		if kv.Key == "output_tokens" {
			kv.Value.Value = &commonpb.AnyValue_IntValue{IntValue: -5}
		}
	}
	res := MapLogs(req, testSender, testNow, 0)
	if res.Rejected != 1 || len(res.Records) != 1 {
		t.Fatalf("negative count: rejected=%d records=%d %v", res.Rejected, len(res.Records), res.Errors)
	}
	old := MapLogs(load(t, "claude_code_logs.json"), testSender, testNow.Add(200*24*time.Hour), 0)
	if old.Rejected != 2 || len(old.Records) != 0 {
		t.Fatalf("outside the retention window: rejected=%d records=%d", old.Rejected, len(old.Records))
	}
	future := MapLogs(load(t, "claude_code_logs.json"), testSender, testNow.Add(-2*time.Hour), 0)
	if future.Rejected != 2 {
		t.Fatalf("from the future: rejected=%d", future.Rejected)
	}
}

func TestFloatCostIsExactAndNeverUnderstated(t *testing.T) {
	cases := []struct {
		in     float64
		amount string
		scale  uint8
	}{{0.0123456, "123456", 7}, {1.5, "15", 1}, {0, "0", 0}, {2, "2", 0}, {0.30000000000000004, "", 0}}
	for _, c := range cases {
		got, ok := floatCost(c.in, "USD")
		if !ok {
			t.Fatalf("%v unusable", c.in)
		}
		if c.amount == "" {
			// 0.30000000000000004 has 17 places; it is capped at 12 and rounded up.
			if got.Scale != 12 || got.Amount != "300000000001" {
				t.Fatalf("%v: %+v", c.in, got)
			}
			continue
		}
		if got.Amount != c.amount || got.Scale != c.scale || got.Basis != usageledger.BasisReported {
			t.Fatalf("%v: %+v", c.in, got)
		}
		if err := got.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []float64{-1, 1e12} {
		if _, ok := floatCost(bad, "USD"); ok {
			t.Fatalf("%v accepted", bad)
		}
	}
}

func TestRecordIDIsStableAndScopedToTheSender(t *testing.T) {
	a := MapLogs(load(t, "claude_code_logs.json"), testSender, testNow, 0)
	b := MapLogs(load(t, "claude_code_logs.json"), testSender, testNow, 0)
	if a.Records[0].ID != b.Records[0].ID || a.Records[0].ID == a.Records[1].ID {
		t.Fatal("ids must be deterministic per event")
	}
	other := testSender
	other.PrincipalID = "bob@example.com"
	if c := MapLogs(load(t, "claude_code_logs.json"), other, testNow, 0); c.Records[0].ID == a.Records[0].ID {
		t.Fatal("the same event from another principal must not collide")
	}
}

func TestReadBodyBounds(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 10_000)
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write(bytes.Repeat([]byte("0"), 1<<20))
	_ = zw.Close()
	if _, err := ReadBody(bytes.NewReader(big), "", 5_000, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("wire bound: %v", err)
	}
	if _, err := ReadBody(bytes.NewReader(zipped.Bytes()), "gzip", 1<<20, 64<<10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a small gzip body that inflates past the bound must be refused: %v", err)
	}
	if out, err := ReadBody(bytes.NewReader(zipped.Bytes()), "gzip", 1<<20, 2<<20); err != nil || len(out) != 1<<20 {
		t.Fatalf("gzip within bounds: %d %v", len(out), err)
	}
	if _, err := ReadBody(bytes.NewReader(big), "br", 1<<20, 1<<20); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("encoding: %v", err)
	}
	if _, err := ReadBody(bytes.NewReader([]byte("not gzip")), "gzip", 1<<20, 1<<20); !errors.Is(err, ErrMalformed) {
		t.Fatalf("bad gzip: %v", err)
	}
}

func TestContentKindAndMalformedBodies(t *testing.T) {
	for ct, want := range map[string]string{"application/x-protobuf": KindProtobuf, "application/json; charset=utf-8": KindJSON, "Application/JSON": KindJSON} {
		if got, err := ContentKind(ct); err != nil || got != want {
			t.Fatalf("%s: %q %v", ct, got, err)
		}
	}
	if _, err := ContentKind("text/plain"); !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatal("text/plain accepted")
	}
	var req collogs.ExportLogsServiceRequest
	for _, bad := range []string{"{", `[]`, `{"resourceLogs": 5}`} {
		if err := Unmarshal(KindJSON, []byte(bad), &req); !errors.Is(err, ErrMalformed) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if err := Unmarshal(KindProtobuf, []byte{0xff, 0xff, 0xff}, &req); !errors.Is(err, ErrMalformed) {
		t.Fatalf("bad protobuf: %v", err)
	}
}

func TestLimiterPerPrincipal(t *testing.T) {
	l := NewLimiter(LimiterConfig{RequestsPerSecond: 1, RequestBurst: 2, RecordsPerSecond: 10, RecordBurst: 20}, 2)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	if ok, _ := l.AllowRequest("a"); !ok {
		t.Fatal("first")
	}
	if ok, _ := l.AllowRequest("a"); !ok {
		t.Fatal("second (burst)")
	}
	if ok, wait := l.AllowRequest("a"); ok || wait <= 0 {
		t.Fatalf("third must be refused with a wait: %v %v", ok, wait)
	}
	if ok, _ := l.AllowRequest("b"); !ok {
		t.Fatal("another principal has its own allowance")
	}
	if ok, _ := l.AllowRecords("a", 20); !ok {
		t.Fatal("records within burst")
	}
	if ok, wait := l.AllowRecords("a", 5); ok || wait <= 0 {
		t.Fatal("records beyond the allowance")
	}
	now = now.Add(time.Second)
	if ok, _ := l.AllowRequest("a"); !ok {
		t.Fatal("tokens refill")
	}
}

func TestForwarderAnnotatesVerifiedIdentityAndNeverSendsAuthorization(t *testing.T) {
	var mu sync.Mutex
	var got *collogs.ExportLogsServiceRequest
	var header http.Header
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req collogs.ExportLogsServiceRequest
		_ = proto.Unmarshal(raw, &req)
		mu.Lock()
		got, header = &req, r.Header.Clone()
		mu.Unlock()
		close(done)
	}))
	defer srv.Close()
	env := map[string]string{EnvForward: "true", "OTEL_EXPORTER_OTLP_ENDPOINT": srv.URL, "OTEL_EXPORTER_OTLP_HEADERS": "x-collector-key=operator%20secret"}
	f, err := NewForwarder(func(k string) string { return env[k] }, nil, nil)
	if err != nil || f == nil {
		t.Fatal(f, err)
	}
	defer f.Close()
	res := MapLogs(load(t, "claude_code_logs.json"), testSender, testNow, 0)
	if !f.Forward(testSender, SignalLogs, res.Forward) {
		t.Fatal("not queued")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("not delivered")
	}
	mu.Lock()
	defer mu.Unlock()
	if header.Get("Authorization") != "" || header.Get("X-Collector-Key") != "operator secret" {
		t.Fatalf("headers: %v", header)
	}
	attrs := map[string]string{}
	for _, kv := range got.ResourceLogs[0].Resource.Attributes {
		attrs[kv.Key] = kv.Value.GetStringValue()
	}
	if attrs[ResourcePrincipal] != "alice@example.com" || attrs[ResourceTenant] != "tenant-a" || attrs[ResourceCaller] != "user" || attrs["service.name"] != "claude-code" {
		t.Fatalf("resource: %v", attrs)
	}
	if strings.Contains(attrs[ResourcePrincipal], "spoofed") {
		t.Fatal("a sender-supplied aiplex.* attribute survived")
	}
	// The caller's own resource was not modified.
	for _, kv := range res.Forward.ResourceLogs[0].Resource.Attributes {
		if kv.Key == ResourcePrincipal && kv.Value.GetStringValue() != "spoofed-principal" {
			t.Fatal("forwarding mutated the source request")
		}
	}
}

func TestForwarderConfiguration(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if f, err := NewForwarder(get(nil), nil, nil); f != nil || err != nil {
		t.Fatalf("off by default: %v %v", f, err)
	}
	for name, env := range map[string]map[string]string{
		"no endpoint": {EnvForward: "true"},
		"grpc":        {EnvForward: "true", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4317", "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"},
		"bad bool":    {EnvForward: "maybe"},
		"bad scheme":  {EnvForward: "true", "OTEL_EXPORTER_OTLP_ENDPOINT": "ftp://c"},
	} {
		if _, err := NewForwarder(get(env), nil, nil); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestEncodeStatusAndSuccess(t *testing.T) {
	ct, body := EncodeStatus(KindJSON, CodeResourceExhausted, "slow down")
	var s struct {
		Code    int
		Message string
	}
	if ct != "application/json" || json.Unmarshal(body, &s) != nil || s.Code != 8 || s.Message != "slow down" {
		t.Fatalf("%s %s", ct, body)
	}
	ct, body = EncodeStatus(KindProtobuf, CodeInvalidArgument, "bad")
	if ct != "application/x-protobuf" || len(body) == 0 {
		t.Fatal("protobuf status")
	}
	_, ok, err := EncodeSuccess(KindJSON, SignalLogs, 3, "three rejected")
	if err != nil || !strings.Contains(string(ok), "rejectedLogRecords") {
		t.Fatalf("%s %v", ok, err)
	}
	_, none, _ := EncodeSuccess(KindProtobuf, SignalMetrics, 0, "")
	if len(none) != 0 {
		t.Fatalf("a full success has an empty response: %x", none)
	}
}

func TestJSONHexIDsAreAccepted(t *testing.T) {
	raw := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"b7ad6b7169203331","timeUnixNano":"1794312000000000000"}]}]}]}`
	var req collogs.ExportLogsServiceRequest
	if err := Unmarshal(KindJSON, []byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	lr := req.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	trace, span := recordIDs(lr)
	if trace != "0af7651916cd43dd8448eb211c80319c" || span != "b7ad6b7169203331" {
		t.Fatalf("%s %s", trace, span)
	}
}
