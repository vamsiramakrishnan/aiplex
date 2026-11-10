package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// unitBounds is a trusted exposure authority attesting unit bounds.
type unitBounds struct{ native map[string]int64 }

func (b *unitBounds) Bounds(_ context.Context, in QuotaBoundsRequest) (QuotaBoundsProof, error) {
	copy := map[string]int64{}
	for k, v := range b.native {
		copy[k] = v
	}
	return QuotaBoundsProof{NativeBounds: copy, EvidenceRef: "operator-unit-bound-v1", ExpiresAt: in.At.Add(time.Hour), ConfigurationDigest: in.Target.ConfigurationDigest, ModelVersion: in.Target.ModelVersion}, nil
}

type mediaRoute struct {
	status int
	media  string
	body   []byte
	cut    bool
}

// mediaFixture is a managed provider surface with a quota host whose
// bindings and bounds the test supplies.
type mediaFixture struct {
	*quotaHostModelFixture
	ledger func() []usageledger.Record
	mu     sync.Mutex
	routes map[string]mediaRoute
	seen   []http.Header
	bodies [][]byte
}

func newMediaFixture(t *testing.T, provider modelapi.Provider, protocol string, auth *toolcredential.Auth, ops []string, bindings []QuotaBinding, meters map[string]quotaplex.Meter, prices []quotaplex.UnitPrice, bounds TrustedBoundsAuthority) *mediaFixture {
	t.Helper()
	m := &mediaFixture{routes: map[string]mediaRoute{}}
	m.quotaHostModelFixture = quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.seen = append(m.seen, r.Header.Clone())
		m.bodies = append(m.bodies, body)
		route, ok := m.routes[r.Method+" "+r.URL.Path]
		m.mu.Unlock()
		if !ok {
			http.Error(w, "no route", 404)
			return
		}
		w.Header().Set("Content-Type", route.media)
		if route.cut {
			w.Header().Set("Content-Length", fmt.Sprint(len(route.body)+50))
		}
		w.WriteHeader(route.status)
		_, _ = w.Write(route.body)
	}, nil)
	cfg := quotaHostTestConfig(m.now)
	policy := &cfg.Tenants[0].Policy
	policy.Concurrency[0].Limit = 10
	policy.Meters["usd"] = quotaplex.Meter{ID: "usd", Unit: "money", Currency: "USD", Scale: 6}
	for id, meter := range meters {
		policy.Meters[id] = meter
	}
	policy.PriceBooks = []quotaplex.PriceBook{{ID: "media", Revision: 1, Match: quotaplex.Match{Provider: provider.ID, Operation: bindings[0].OperationID}, Prices: prices}}
	for i := range bindings {
		bindings[i].TenantID, bindings[i].Plane, bindings[i].ResourceID, bindings[i].Provider = "tenant", "llmplex", "surface", provider.ID
	}
	cfg.Bindings = bindings
	m.store = quotaplex.NewMemoryStore()
	host, err := NewQuotaHost(context.Background(), cfg, m.store, func() time.Time { return m.now }, nil, bounds)
	if err != nil {
		t.Fatal(err)
	}
	m.host, m.handler.Quota = host, host
	installProviderCredentials(t, m.handler)
	record, _ := m.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	var google ModelConnection
	_ = json.Unmarshal(record.Spec, &google)
	connection := providerConnection(t, google.Surface.Origin, provider, protocol, auth, ops...)
	if err := m.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal("provider surface rejected:", err)
	}
	putProviderSurface(t, m.resources, connection)
	m.ledger = usageLedgerFor(t, m.handler)
	return m
}

func (m *mediaFixture) route(method, path string, value mediaRoute) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes[method+" "+path] = value
}

func (m *mediaFixture) call(t *testing.T, method, path, key, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	r := m.request("generateContent", key, "", false)
	r.Method = method
	r.URL.Path, r.URL.RawQuery = "/api/v1/llm/gateway/surface"+path, ""
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r.Header.Set("X-Api-Key", "caller-key-never-forwarded")
	w := httptest.NewRecorder()
	m.handler.ClientGateway(w, r)
	return w
}

func (m *mediaFixture) attempt(t *testing.T, w *httptest.ResponseRecorder) quotaplex.AttemptInfo {
	return quotaHostModelAttempt(t, m.quotaHostModelFixture, w.Header().Get("X-Aiplex-Attempt-ID"))
}

// Image generation under a media-units binding: admitted with the request's
// own image count, settled from the returned images, priced per image.
func TestManagedImageGenerationSettlesGeneratedImages(t *testing.T) {
	m := newMediaFixture(t, modelapi.Provider{ID: modelapi.ProviderOpenAI}, modelapi.ProtocolOpenAIImages, bearerRef, []string{"openai.v1.images-generations"},
		[]QuotaBinding{{ID: "images", OperationID: "openai.v1.images-generations", Model: "model-v1", Profile: quotaProfileMedia, ModelVersion: "model-v1", Meters: map[string]string{"requests": "requests", "generated_images": "images"}}},
		map[string]quotaplex.Meter{"images": {ID: "images", Unit: "images", Billable: true}},
		[]quotaplex.UnitPrice{{MeterID: "images", MoneyMeterID: "usd", Numerator: 40000, Denominator: 1}, {MeterID: "requests", MoneyMeterID: "usd", Numerator: 0, Denominator: 1}}, nil)
	response := `{"created":1,"size":"1024x1024","quality":"high","data":[{"b64_json":"AAAA"},{"b64_json":"BBBB"}],"usage":{"input_tokens":50,"input_tokens_details":{"text_tokens":10,"image_tokens":40},"output_tokens":50,"total_tokens":100}}`
	m.route("POST", "/v1/images/generations", mediaRoute{200, "application/json", []byte(response), false})
	w := m.call(t, "POST", "/v1/images/generations", "images-1", "application/json", []byte(`{"model":"model-v1","prompt":"x","n":3}`))
	if w.Code != 200 || w.Body.String() != response {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := m.seen[0].Get("Authorization"); got != "Bearer "+providerTestSecret || m.seen[0].Get("X-Api-Key") != "" {
		t.Fatalf("credential handling: %v", m.seen[0])
	}
	info := m.attempt(t, w)
	if info.Demand.Bounds["images"] != 3 || info.Decision.State != "settled" || info.Actual["images"] != 2 || info.Actual["usd"] != 80000 {
		t.Fatalf("image settlement: bounds=%v state=%s actual=%v", info.Demand.Bounds, info.Decision.State, info.Actual)
	}
	records := m.ledger()
	if len(records) != 1 {
		t.Fatal(len(records))
	}
	rec := records[0]
	if rec.UsageBound != "" || *rec.Usage.GeneratedImages != 2 || *rec.Usage.InputImageTokens != 40 || rec.ImageSize != "1024x1024" || rec.ImageQuality != "high" || rec.Cost.Basis != usageledger.BasisSettled || rec.Cost.Amount != "80000" {
		t.Fatalf("ledger: %+v usage=%+v", rec, rec.Usage)
	}
	// An unbounded image count is refused before reservation or dispatch.
	if w := m.call(t, "POST", "/v1/images/generations", "images-2", "application/json", []byte(`{"model":"model-v1","prompt":"x","n":11}`)); w.Code != 400 || len(m.seen) != 1 {
		t.Fatalf("unbounded n: %d calls=%d", w.Code, len(m.seen))
	}
}

func transcriptionForm(t *testing.T, model string) ([]byte, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "audio.wav")
	_, _ = part.Write(bytes.Repeat([]byte{0x52, 0x49, 0x46, 0x46}, 4096))
	_ = mw.WriteField("model", model)
	_ = mw.Close()
	return buf.Bytes(), mw.FormDataContentType()
}

// Duration-billed transcription: the authority bounds input audio
// milliseconds; the reported seconds settle exactly; a token-type response
// cannot settle the duration meter and stays unknown with its exposure.
func TestManagedTranscriptionSettlesAudioDuration(t *testing.T) {
	m := newMediaFixture(t, modelapi.Provider{ID: modelapi.ProviderOpenAI}, modelapi.ProtocolOpenAIAudio, bearerRef, []string{"openai.v1.audio-transcriptions"},
		[]QuotaBinding{{ID: "asr", OperationID: "openai.v1.audio-transcriptions", Model: "model-v1", Profile: quotaProfileMedia, ModelVersion: "model-v1", Meters: map[string]string{"requests": "requests", "audio_input_milliseconds": "audio_ms"}}},
		map[string]quotaplex.Meter{"audio_ms": {ID: "audio_ms", Unit: "milliseconds", Billable: true}},
		[]quotaplex.UnitPrice{{MeterID: "audio_ms", MoneyMeterID: "usd", Numerator: 1, Denominator: 10}, {MeterID: "requests", MoneyMeterID: "usd", Numerator: 0, Denominator: 1}},
		&unitBounds{native: map[string]int64{"audio_input_milliseconds": 600000}})
	duration, _ := os.ReadFile("../../pkg/modelapi/testdata/usage-formats/openai/transcription.duration.json")
	m.route("POST", "/v1/audio/transcriptions", mediaRoute{200, "application/json", duration, false})
	form, contentType := transcriptionForm(t, "model-v1")
	w := m.call(t, "POST", "/v1/audio/transcriptions", "asr-1", contentType, form)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), duration) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !bytes.Equal(m.bodies[0], form) {
		t.Fatal("multipart audio not relayed byte-for-byte")
	}
	info := m.attempt(t, w)
	if info.Demand.Bounds["audio_ms"] != 600000 || info.Decision.State != "settled" || info.Actual["audio_ms"] != 3000 || info.Actual["usd"] != 300 {
		t.Fatalf("duration settlement: %v %s %v", info.Demand.Bounds, info.Decision.State, info.Actual)
	}
	rec := m.ledger()[0]
	if rec.UsageBound != "" || *rec.Usage.AudioInputMilliseconds != 3000 || rec.Usage.InputTokens != nil || rec.Cost.Amount != "300" {
		t.Fatalf("ledger: %+v usage=%+v", rec, rec.Usage)
	}
	tokens, _ := os.ReadFile("../../pkg/modelapi/testdata/usage-formats/openai/transcription.json")
	m.route("POST", "/v1/audio/transcriptions", mediaRoute{200, "application/json", tokens, false})
	w = m.call(t, "POST", "/v1/audio/transcriptions", "asr-2", contentType, form)
	if w.Code == 200 {
		t.Fatal("response without the protected duration was delivered as settled")
	}
	if info := m.attempt(t, w); info.Decision.State != "unknown" || info.Actual != nil {
		t.Fatalf("token-billed response settled a duration binding: %s %v", info.Decision.State, info.Actual)
	}
}

// Message Batches under a batch-usage binding: admitted at create, open until
// complete results are fetched, settled once from succeeded lines, never on a
// partial or repeated fetch, recorded once on the ledger.
func TestManagedMessageBatchSettlesOnceFromResults(t *testing.T) {
	anthropic := modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}
	m := newMediaFixture(t, anthropic, modelapi.ProtocolAnthropicBatches, anthropicRef,
		[]string{"anthropic.message-batches-create", "anthropic.message-batches-get", "anthropic.message-batches-results"},
		[]QuotaBinding{
			{ID: "batch", OperationID: "anthropic.message-batches-create", Model: "model-v1", Profile: quotaProfileBatch, Meters: map[string]string{"requests": "requests", "batch_requests": "batch_items", "output_tokens": "out"}},
			{ID: "batch-results", OperationID: "anthropic.message-batches-results", Profile: quotaProfileRequests, Meters: map[string]string{"requests": "requests"}},
			{ID: "batch-get", OperationID: "anthropic.message-batches-get", Profile: quotaProfileRequests, Meters: map[string]string{"requests": "requests"}},
		},
		map[string]quotaplex.Meter{"batch_items": {ID: "batch_items", Unit: "requests"}, "out": {ID: "out", Unit: "tokens", Billable: true}},
		[]quotaplex.UnitPrice{{MeterID: "out", MoneyMeterID: "usd", Numerator: 2, Denominator: 1}, {MeterID: "requests", MoneyMeterID: "usd", Numerator: 0, Denominator: 1}},
		&unitBounds{native: map[string]int64{"output_tokens": 5 * 8000}})
	m.route("POST", "/v1/messages/batches", mediaRoute{200, "application/json", []byte(`{"id":"msgbatch_q1","type":"message_batch","processing_status":"in_progress"}`), false})
	requests := []string{}
	for _, id := range []string{"req-a", "req-b", "req-c", "req-d", "req-e"} {
		requests = append(requests, fmt.Sprintf(`{"custom_id":%q,"params":{"model":"model-v1","max_tokens":8000,"messages":[{"role":"user","content":"x"}]}}`, id))
	}
	w := m.call(t, "POST", "/v1/messages/batches", "batch-1", "application/json", []byte(`{"requests":[`+strings.Join(requests, ",")+`]}`))
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if m.seen[0].Get("X-Api-Key") != providerTestSecret {
		t.Fatal("surface credential not injected")
	}
	batchAttempt := w.Header().Get("X-Aiplex-Attempt-ID")
	info := m.attempt(t, w)
	if info.Decision.State != "unknown" || info.UnknownReason != "batch_results_pending" || info.Demand.Bounds["batch_items"] != 5 || info.Demand.Bounds["out"] != 40000 || info.ProviderResource != "msgbatch_q1" {
		t.Fatalf("open batch attempt: %s %q %v %q", info.Decision.State, info.UnknownReason, info.Demand.Bounds, info.ProviderResource)
	}
	full, _ := os.ReadFile("../../pkg/modelapi/testdata/usage-formats/anthropic/batch-results.jsonl")
	full = bytes.ReplaceAll(full, []byte("claude-opus-5-5"), []byte("model-v1"))
	partial, _ := os.ReadFile("../../pkg/modelapi/testdata/usage-formats/anthropic/batch-results-partial.jsonl")
	m.route("GET", "/v1/messages/batches/msgbatch_q1/results", mediaRoute{200, "application/binary", partial, false})
	if w := m.call(t, "GET", "/v1/messages/batches/msgbatch_q1/results", "", "", nil); w.Code != 200 {
		t.Fatalf("partial: %d %s", w.Code, w.Body)
	}
	m.route("GET", "/v1/messages/batches/msgbatch_q1/results", mediaRoute{200, "application/binary", full, true})
	m.call(t, "GET", "/v1/messages/batches/msgbatch_q1/results", "", "", nil)
	if info := quotaHostModelAttempt(t, m.quotaHostModelFixture, batchAttempt); info.Decision.State != "unknown" {
		t.Fatalf("partial or interrupted results settled the batch: %s", info.Decision.State)
	}
	m.route("GET", "/v1/messages/batches/msgbatch_q1/results", mediaRoute{200, "application/binary", full, false})
	for i := 0; i < 2; i++ {
		if w := m.call(t, "GET", "/v1/messages/batches/msgbatch_q1/results", "", "", nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), full) {
			t.Fatalf("results fetch %d: %d %s", i, w.Code, w.Body)
		}
	}
	info = quotaHostModelAttempt(t, m.quotaHostModelFixture, batchAttempt)
	if info.Decision.State != "settled" || info.Actual["batch_items"] != 2 || info.Actual["out"] != 910 || info.Actual["usd"] != 1820 || info.Actual["requests"] != 1 {
		t.Fatalf("batch settlement: %s %v", info.Decision.State, info.Actual)
	}
	var usageRecords []usageledger.Record
	for _, rec := range m.ledger() {
		if !rec.Batch {
			t.Fatalf("batch exchange not marked batch: %+v", rec)
		}
		if rec.Usage != nil {
			usageRecords = append(usageRecords, rec)
		}
	}
	if len(usageRecords) != 1 {
		t.Fatalf("batch usage recorded %d times", len(usageRecords))
	}
	rec := usageRecords[0]
	if *rec.Usage.OutputTokens != 910 || *rec.Usage.InputTokens != 5590 || rec.Usage.CacheWriteTokens != nil || rec.QuotaAttemptID != batchAttempt || rec.ModelID != "model-v1" || rec.Cost.Basis != usageledger.BasisSettled || rec.Cost.Amount != "1820" || rec.UsageBound != "" {
		t.Fatalf("batch ledger record: %+v usage=%+v", rec, rec.Usage)
	}
}
