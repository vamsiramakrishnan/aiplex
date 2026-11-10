package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/otlpingest"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// The payloads are built by hand from the harnesses' documentation, not
// captured from real clients (see the files' _comment).
func otlpFixture(t *testing.T, file string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../pkg/otlpingest/testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type otlpHarness struct {
	h     *OTLPHandler
	store *usageledger.MemoryStore
}

func newOTLPHarness(t *testing.T) *otlpHarness {
	t.Helper()
	store := usageledger.NewMemoryStore()
	h, err := NewOTLPHandler(store, func(*http.Request) (string, error) { return "tenant", nil }, false, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	h.Caller = func(context.Context, string, *models.WIFIdentity) (accessPointCaller, error) {
		return accessPointCaller{Kind: CallerUser}, nil
	}
	// The fixtures are dated 2026-11-10T12:00Z.
	h.Now = func() time.Time { return time.Date(2026, 11, 10, 12, 1, 0, 0, time.UTC) }
	return &otlpHarness{h: h, store: store}
}

func (o *otlpHarness) post(t *testing.T, handler http.HandlerFunc, contentType string, body []byte, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/otlp/v1/logs", bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	identity := &models.WIFIdentity{PrincipalID: "alice", AuthorizedParty: "claude-code-client", IsWorkforce: true}
	r = r.WithContext(context.WithValue(r.Context(), keyWIFIdentity, identity))
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func (o *otlpHarness) records(t *testing.T) []usageledger.Record {
	t.Helper()
	page, err := o.store.List(context.Background(), "tenant", usageledger.Query{Limit: usageledger.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	return page.Records
}

func TestOTLPLogsRecordUnderTheVerifiedIdentityOnly(t *testing.T) {
	o := newOTLPHarness(t)
	w := o.post(t, o.h.Logs, "application/json", otlpFixture(t, "claude_code_logs.json"), nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	recs := o.records(t)
	if len(recs) != 2 {
		t.Fatalf("records: %+v", recs)
	}
	for _, r := range recs {
		if r.TenantID != "tenant" || r.PrincipalID != "alice" || r.ClientID != "claude-code-client" || r.CallerKind != "user" || r.Source != usageledger.SourceReported || r.AccessPointID != "" {
			t.Fatalf("identity must come from the token: %+v", r)
		}
	}
	raw, _ := json.Marshal(recs)
	for _, leak := range []string{"someone@example.com", "spoofed-principal", "org-untrusted"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("payload identity %q reached the ledger: %s", leak, raw)
		}
	}
	// The records are visible to their principal, and only to them and operators.
	usage := NewUsageHandler(o.store, o.h.Tenant, false)
	if body := usageGet(t, usage.ListRecords, "/api/v1/usage/records?source=reported&harness=claude-code", viewerAccess("alice"), 200); len(recordIDs(t, body)) != 2 {
		t.Fatalf("the principal sees its reported records: %s", body)
	}
	if body := usageGet(t, usage.ListRecords, "/api/v1/usage/records?source=reported", viewerAccess("mallory"), 200); len(recordIDs(t, body)) != 0 {
		t.Fatalf("another principal must not see them: %s", body)
	}
}

func TestOTLPRetriesAreIdempotent(t *testing.T) {
	o := newOTLPHarness(t)
	body := otlpFixture(t, "claude_code_logs.json")
	for i := 0; i < 3; i++ {
		if w := o.post(t, o.h.Logs, "application/json", body, nil); w.Code != 200 {
			t.Fatalf("attempt %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if n := len(o.records(t)); n != 2 {
		t.Fatalf("a retried export must not duplicate records: %d", n)
	}
	// A second replica (an empty in-process window) still dedupes through the store.
	other, err := NewOTLPHandler(o.store, o.h.Tenant, false, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	other.Caller, other.Now = o.h.Caller, o.h.Now
	w := o.post(t, other.Logs, "application/json", body, nil)
	if w.Code != 200 || len(o.records(t)) != 2 {
		t.Fatalf("store-level dedupe: %d %d", w.Code, len(o.records(t)))
	}
	// The same events from another principal are that principal's own records.
	w = o.post(t, o.h.Logs, "application/json", body, func(r *http.Request) {
		id := &models.WIFIdentity{PrincipalID: "bob", IsWorkforce: true}
		*r = *r.WithContext(context.WithValue(r.Context(), keyWIFIdentity, id))
	})
	if w.Code != 200 || len(o.records(t)) != 4 {
		t.Fatalf("another principal: %d %d", w.Code, len(o.records(t)))
	}
}

func TestOTLPProtobufGzipAndPartialSuccess(t *testing.T) {
	o := newOTLPHarness(t)
	var req collogs.ExportLogsServiceRequest
	if err := otlpingest.Unmarshal(otlpingest.KindJSON, otlpFixture(t, "claude_code_logs.json"), &req); err != nil {
		t.Fatal(err)
	}
	// One usage-bearing event with a negative count is rejected, not stored.
	for _, kv := range req.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Attributes {
		if kv.Key == "output_tokens" {
			kv.Value.Value = &commonpb.AnyValue_IntValue{IntValue: -1}
		}
	}
	wire, err := proto.Marshal(&req)
	if err != nil {
		t.Fatal(err)
	}
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write(wire)
	_ = zw.Close()
	w := o.post(t, o.h.Logs, "application/x-protobuf", zipped.Bytes(), func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") })
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("%d %s", w.Code, w.Header())
	}
	var resp collogs.ExportLogsServiceResponse
	if err := proto.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.PartialSuccess.GetRejectedLogRecords() != 1 || resp.PartialSuccess.GetErrorMessage() == "" {
		t.Fatalf("partial success: %v %v", resp.PartialSuccess, err)
	}
	if n := len(o.records(t)); n != 1 {
		t.Fatalf("only the valid event is stored: %d", n)
	}
	if strings.Contains(resp.PartialSuccess.GetErrorMessage(), "secret") {
		t.Fatal("rejection reasons must not echo payload content")
	}
}

func TestOTLPBoundsAndStatusShapes(t *testing.T) {
	o := newOTLPHarness(t)
	body := otlpFixture(t, "claude_code_logs.json")

	o.h.MaxRecords = 2
	w := o.post(t, o.h.Logs, "application/json", body, nil)
	var status struct {
		Code    int
		Message string
	}
	if w.Code != http.StatusRequestEntityTooLarge || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Code != otlpingest.CodeInvalidArgument || status.Message == "" {
		t.Fatalf("too many records: %d %s", w.Code, w.Body.String())
	}
	o.h.MaxRecords = 1000

	o.h.MaxWireBytes = 100
	if w := o.post(t, o.h.Logs, "application/json", body, nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body too large: %d", w.Code)
	}
	o.h.MaxWireBytes = 4 << 20

	if w := o.post(t, o.h.Logs, "text/plain", body, nil); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type: %d", w.Code)
	}
	if w := o.post(t, o.h.Logs, "application/json", []byte("{nope"), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed: %d", w.Code)
	}
	if w := o.post(t, o.h.Logs, "application/json", body, func(r *http.Request) { r.Header.Set("Content-Encoding", "br") }); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("encoding: %d", w.Code)
	}
	if n := len(o.records(t)); n != 0 {
		t.Fatalf("refused exports store nothing: %d", n)
	}

	// Rate limit: 429 with Retry-After in an OTLP status body.
	o.h.Limiter = otlpingest.NewLimiter(otlpingest.LimiterConfig{RequestsPerSecond: 0.001, RequestBurst: 1}, 0)
	if w := o.post(t, o.h.Logs, "application/json", body, nil); w.Code != 200 {
		t.Fatalf("first request: %d", w.Code)
	}
	w = o.post(t, o.h.Logs, "application/json", body, nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Code != otlpingest.CodeResourceExhausted {
		t.Fatalf("rate limit: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	// The limit is per principal.
	w = o.post(t, o.h.Logs, "application/json", body, func(r *http.Request) {
		*r = *r.WithContext(context.WithValue(r.Context(), keyWIFIdentity, &models.WIFIdentity{PrincipalID: "carol", IsWorkforce: true}))
	})
	if w.Code != 200 {
		t.Fatalf("another principal must not be limited: %d", w.Code)
	}
}

func TestOTLPRequiresIdentityOutsideDevelopment(t *testing.T) {
	o := newOTLPHarness(t)
	body := otlpFixture(t, "claude_code_logs.json")
	anonymous := func(r *http.Request) { *r = *r.WithContext(context.Background()) }
	if w := o.post(t, o.h.Logs, "application/json", body, anonymous); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", w.Code)
	}
	o.h.Development = true
	if w := o.post(t, o.h.Logs, "application/json", body, anonymous); w.Code != 200 {
		t.Fatalf("development mode is unauthenticated by design: %d", w.Code)
	}
	for _, r := range o.records(t) {
		if r.PrincipalID != "" {
			t.Fatalf("an anonymous development export has no principal: %+v", r)
		}
	}
	o.h.Development = false
	o.h.Tenant = func(*http.Request) (string, error) { return "", context.Canceled }
	if w := o.post(t, o.h.Logs, "application/json", body, nil); w.Code != http.StatusForbidden {
		t.Fatalf("no verified tenant: %d", w.Code)
	}
}

func TestOTLPLedgerFailureAsksForARetry(t *testing.T) {
	o := newOTLPHarness(t)
	o.h.Ledger = usageFailingStore{}
	w := o.post(t, o.h.Logs, "application/json", otlpFixture(t, "claude_code_logs.json"), nil)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

func TestOTLPMetricsAndTracesAreAcceptedAndNotRecorded(t *testing.T) {
	o := newOTLPHarness(t)
	metrics := `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","unit":"tokens","sum":{"aggregationTemporality":1,"isMonotonic":true,"dataPoints":[{"asInt":"1500","timeUnixNano":"1794312000000000000","attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"model","value":{"stringValue":"claude-sonnet-4-5"}}]}]}}]}]}]}`
	w := o.post(t, o.h.Metrics, "application/json", []byte(metrics), nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	traces := `{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"b7ad6b7169203331","name":"claude_code.llm_request","startTimeUnixNano":"1","endTimeUnixNano":"2"}]}]}]}`
	if w := o.post(t, o.h.Traces, "application/json", []byte(traces), nil); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if n := len(o.records(t)); n != 0 {
		t.Fatalf("metrics and spans must not become records (events are the source of truth): %d", n)
	}
	// A metrics request in protobuf with an empty response body.
	wire, _ := proto.Marshal(&colmetrics.ExportMetricsServiceRequest{})
	if w := o.post(t, o.h.Metrics, "application/x-protobuf", wire, nil); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
}

func TestOTLPForwardsUnrecordedSignalsWithTheVerifiedIdentity(t *testing.T) {
	received := make(chan *collogs.ExportLogsServiceRequest, 1)
	gotAuth := make(chan string, 4)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth <- r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/logs" {
			var req collogs.ExportLogsServiceRequest
			_ = proto.Unmarshal(raw, &req)
			received <- &req
		}
	}))
	defer collector.Close()
	env := map[string]string{otlpingest.EnvForward: "true", "OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL}
	f, err := otlpingest.NewForwarder(func(k string) string { return env[k] }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	o := newOTLPHarness(t)
	o.h.Forwarder = f
	w := o.post(t, o.h.Logs, "application/json", otlpFixture(t, "claude_code_logs.json"), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer caller-token-never-forwarded")
	})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	select {
	case req := <-received:
		attrs := map[string]string{}
		for _, kv := range req.ResourceLogs[0].Resource.Attributes {
			attrs[kv.Key] = kv.Value.GetStringValue()
		}
		if attrs["aiplex.principal.id"] != "alice" || len(req.ResourceLogs[0].ScopeLogs[0].LogRecords) != 2 {
			t.Fatalf("forwarded: %v %d", attrs, len(req.ResourceLogs[0].ScopeLogs[0].LogRecords))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing forwarded")
	}
	if auth := <-gotAuth; auth != "" {
		t.Fatalf("the caller's Authorization header was forwarded: %q", auth)
	}
}

func TestOTLPPricesEventsThatReportedNoCostAndKeepsReportedCost(t *testing.T) {
	o := newOTLPHarness(t)
	o.h.Prices = func(context.Context, string) ([]usageledger.Price, error) {
		return []usageledger.Price{
			{ID: "codex", ModelID: "gpt-5-codex", Currency: "USD", Scale: 6, Meters: map[string]usageledger.Rational{
				usageledger.MeterInput: {Numerator: 2, Denominator: 1}, usageledger.MeterOutput: {Numerator: 8, Denominator: 1},
				usageledger.MeterCacheRead: {Numerator: 1, Denominator: 1}, usageledger.MeterReasoning: {Numerator: 0, Denominator: 1}}},
			{ID: "claude", ModelID: "claude-sonnet-4-5", Currency: "USD", Scale: 6, Meters: map[string]usageledger.Rational{usageledger.MeterInput: {Numerator: 99, Denominator: 1}}},
		}, nil
	}
	o.post(t, o.h.Logs, "application/json", otlpFixture(t, "codex_logs.json"), nil)
	o.post(t, o.h.Logs, "application/json", otlpFixture(t, "claude_code_logs.json"), nil)
	for _, r := range o.records(t) {
		switch r.Harness {
		case "codex":
			// (1200-1000)*2 + 1000*1 + 400*8 = 4600 micro-USD, on an AIPlex price: estimated.
			if r.Cost != (usageledger.Cost{Basis: usageledger.BasisEstimated, Currency: "USD", Amount: "4600", Scale: 6, PriceID: "codex"}) {
				t.Fatalf("codex cost: %+v", r.Cost)
			}
		case "claude-code":
			if r.Status == usageledger.StatusOK && r.Cost.Basis != usageledger.BasisReported {
				t.Fatalf("a harness-reported cost is kept as reported, not repriced: %+v", r.Cost)
			}
			if r.Status == usageledger.StatusError && r.Cost.Basis != usageledger.BasisUnknown {
				t.Fatalf("an error with no usage stays unpriced: %+v", r.Cost)
			}
		}
	}
}

func TestOTLPTagsFromTheAllowlistedHeader(t *testing.T) {
	o := newOTLPHarness(t)
	tags, err := NewTagPolicy([]string{"experiment"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	o.h.Tags = tags
	header := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set(TagsHeader, v) }
	}
	o.post(t, o.h.Logs, "application/json", otlpFixture(t, "gemini_cli_logs.json"), header("experiment=arm-a,team=x"))
	recs := o.records(t)
	if len(recs) != 1 || len(recs[0].Tags) != 1 || recs[0].Tags["experiment"] != "arm-a" {
		t.Fatalf("only allowlisted keys are stored: %+v", recs)
	}
}

// usageFailingStore is defined in usage_test.go.
var _ usageledger.Store = usageFailingStore{}
