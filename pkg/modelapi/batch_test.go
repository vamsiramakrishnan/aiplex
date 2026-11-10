package modelapi

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

type fakeBatchSettler struct {
	mu      sync.Mutex
	settles []BatchSettlement
	records []BatchSettlement
	err     error
}

func (f *fakeBatchSettler) SettleBatch(_ context.Context, _ Owner, s BatchSettlement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settles = append(f.settles, s)
	return f.err
}
func (f *fakeBatchSettler) RecordBatch(_ context.Context, _ Owner, s BatchSettlement) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, s)
}
func (f *fakeBatchSettler) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.settles), len(f.records)
}

func fixture(t *testing.T, provider, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage-formats", provider, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type batchHarness struct {
	t         *testing.T
	upstream  *httptest.Server
	store     *MemoryBatchStore
	ownership *memoryOwnership
	settler   *fakeBatchSettler
	surface   Surface
	auth      atomic.Value
	routes    map[string]func(http.ResponseWriter, *http.Request)
	mu        sync.Mutex
	clock     func() time.Time
}

func newBatchHarness(t *testing.T, provider Provider, protocol string, models []string, ids ...string) *batchHarness {
	h := &batchHarness{t: t, store: NewMemoryBatchStore(), ownership: newOwnership(), settler: &fakeBatchSettler{}, routes: map[string]func(http.ResponseWriter, *http.Request){}}
	h.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.auth.Store(r.Header.Get("Authorization") + "|" + r.Header.Get("X-Api-Key"))
		h.mu.Lock()
		route := h.routes[r.Method+" "+r.URL.Path]
		h.mu.Unlock()
		if route == nil {
			http.Error(w, "no route "+r.Method+" "+r.URL.Path, 404)
			return
		}
		route(w, r)
	}))
	t.Cleanup(h.upstream.Close)
	h.surface = providerSurfaceFor(provider, protocol, models, ids...)(h.upstream.URL)
	return h
}

func (h *batchHarness) route(method, path string, status int, media string, body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.routes[method+" "+path] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", media)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

func (h *batchHarness) gateway(lease *collectingLease) *Gateway {
	opts := options(h.t, h.upstream.URL, h.ownership)
	opts.LiveDialer, opts.TokenSource = nil, nil
	opts.Credential = CredentialFunc(func(_ context.Context, r *http.Request) error {
		if h.surface.Provider.ID == ProviderAnthropic {
			r.Header.Set("X-Api-Key", "surface-key")
		} else {
			r.Header.Set("Authorization", "Bearer surface-key")
		}
		return nil
	})
	opts.Batches, opts.BatchSettler, opts.Clock = h.store, h.settler, h.clock
	opts.AllowShared = func(context.Context, contracts.RequestContext, Surface, Operation) bool { return true }
	if lease != nil {
		opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	}
	g, err := NewGateway(h.surface, opts)
	if err != nil {
		h.t.Fatal(err)
	}
	return g
}

func (h *batchHarness) do(g *Gateway, method, path, contentType string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r.Header.Set("Authorization", "Bearer forged-client-token")
	r.Header.Set("X-Api-Key", "forged-client-key")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if got, _ := h.auth.Load().(string); got != "" && strings.Contains(got, "forged") {
		h.t.Fatalf("caller credential crossed upstream: %s", got)
	}
	return w
}

const anthropicBatchModel = "claude-opus-5-5"

func anthropicBatchBody(n int) []byte {
	parts := []string{}
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf(`{"custom_id":"req-%c","params":{"model":%q,"max_tokens":8,"messages":[]}}`, 'a'+i, anthropicBatchModel))
	}
	return []byte(`{"requests":[` + strings.Join(parts, ",") + `]}`)
}

// Anthropic Message Batches: admitted once at create, settled exactly once
// from complete results (succeeded lines only), never from partial results,
// never twice on a repeated fetch.
func TestAnthropicMessageBatchSettlesOnceFromCompleteResults(t *testing.T) {
	h := newBatchHarness(t, Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01"}, ProtocolAnthropicBatches, []string{anthropicBatchModel},
		"anthropic.message-batches-create", "anthropic.message-batches-get", "anthropic.message-batches-results", "anthropic.message-batches-list")
	h.route("POST", "/v1/messages/batches", 200, "application/json", []byte(`{"id":"msgbatch_1","type":"message_batch","processing_status":"in_progress","request_counts":{"processing":5,"succeeded":0,"errored":0,"canceled":0,"expired":0},"results_url":null}`))
	createLease := &collectingLease{profile: "batch-usage"}
	w := h.do(h.gateway(createLease), "POST", "/v1/messages/batches", "application/json", anthropicBatchBody(5))
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if createLease.isSettled() {
		t.Fatal("batch create settled the job")
	}
	found := false
	for _, call := range createLease.calls {
		found = found || call == "resource:msgbatch_1"
	}
	if !found {
		t.Fatalf("batch id not pinned on the attempt: %v", createLease.calls)
	}
	record, _, err := h.store.LoadBatch(context.Background(), Owner{"tenant", "alice", "native"}, ProviderAnthropic, "msgbatch_1")
	if err != nil || record.Expected != 5 || record.AttemptID != "attempt-1" || record.Settled {
		t.Fatalf("record: %+v %v", record, err)
	}
	// Another principal cannot read the batch.
	r := httptest.NewRequest("GET", "/v1/messages/batches/msgbatch_1/results", nil)
	r.Header.Set("Test-Principal", "mallory")
	other := httptest.NewRecorder()
	h.gateway(nil).ServeHTTP(other, r)
	if other.Code != 403 {
		t.Fatalf("foreign results fetch: %d", other.Code)
	}

	// Partial results (one of five lines): delivered, nothing settles.
	h.route("GET", "/v1/messages/batches/msgbatch_1/results", 200, "application/binary", fixture(t, "anthropic", "batch-results-partial.jsonl"))
	w = h.do(h.gateway(&collectingLease{profile: "request-count"}), "GET", "/v1/messages/batches/msgbatch_1/results", "", nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), fixture(t, "anthropic", "batch-results-partial.jsonl")) {
		t.Fatalf("partial body: %d %q", w.Code, w.Body)
	}
	if settles, records := h.settler.counts(); settles != 0 || records != 0 {
		t.Fatal("partial results settled")
	}

	// Complete results: settle once; two concurrent and one later fetch.
	full := fixture(t, "anthropic", "batch-results.jsonl")
	h.route("GET", "/v1/messages/batches/msgbatch_1/results", 200, "application/binary", full)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := h.do(h.gateway(&collectingLease{profile: "request-count"}), "GET", "/v1/messages/batches/msgbatch_1/results", "", nil)
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), full) {
				t.Errorf("full body: %d", w.Code)
			}
		}()
	}
	wg.Wait()
	_, records := h.settler.counts()
	if records != 1 {
		t.Fatalf("batch recorded %d times", records)
	}
	s := h.settler.records[0]
	want := map[string]int64{"input_tokens": 5590, "uncached_input_tokens": 1590, "output_tokens": 910, "cached_tokens": 2000, "thought_tokens": 370, "batch_requests": 2}
	for key, value := range want {
		if s.Counters[key] != value {
			t.Fatalf("settlement %s=%d want %d (%v)", key, s.Counters[key], value, s.Counters)
		}
	}
	if _, known := s.Counters["cache_write_tokens"]; known {
		t.Fatal("cache writes absent on one succeeded line must stay unknown")
	}
	if s.Items != 5 || s.Succeeded != 2 || s.Model != anthropicBatchModel || s.AttemptID != "attempt-1" {
		t.Fatalf("settlement: %+v", s)
	}
	for _, settlement := range h.settler.settles {
		if fmt.Sprint(settlement.Counters) != fmt.Sprint(s.Counters) {
			t.Fatal("repeated settlement carried different usage")
		}
	}
	w = h.do(h.gateway(nil), "GET", "/v1/messages/batches/msgbatch_1/results", "", nil)
	if settles, records := h.settler.counts(); records != 1 || w.Code != 200 {
		t.Fatalf("second fetch settled again: settles=%d records=%d", settles, records)
	}
}

// A settler that cannot acknowledge withholds the final line; an
// interrupted body aggregates nothing.
func TestBatchResultsWithholdFinalLineUntilAcknowledged(t *testing.T) {
	h := newBatchHarness(t, Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01"}, ProtocolAnthropicBatches, []string{anthropicBatchModel},
		"anthropic.message-batches-create", "anthropic.message-batches-results")
	h.route("POST", "/v1/messages/batches", 200, "application/json", []byte(`{"id":"msgbatch_2","processing_status":"in_progress"}`))
	if w := h.do(h.gateway(nil), "POST", "/v1/messages/batches", "application/json", anthropicBatchBody(5)); w.Code != 200 {
		t.Fatal(w.Body)
	}
	full := fixture(t, "anthropic", "batch-results.jsonl")
	h.mu.Lock()
	h.routes["GET /v1/messages/batches/msgbatch_2/results"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(full)+100))
		_, _ = w.Write(full)
	}
	h.mu.Unlock()
	w := h.do(h.gateway(nil), "GET", "/v1/messages/batches/msgbatch_2/results", "", nil)
	if bytes.Equal(w.Body.Bytes(), full) {
		t.Fatal("interrupted body delivered its final line")
	}
	if settles, _ := h.settler.counts(); settles != 0 {
		t.Fatal("interrupted body settled")
	}
	h.route("GET", "/v1/messages/batches/msgbatch_2/results", 200, "application/binary", full)
	h.settler.err = fmt.Errorf("quota store unavailable")
	w = h.do(h.gateway(nil), "GET", "/v1/messages/batches/msgbatch_2/results", "", nil)
	if bytes.Equal(w.Body.Bytes(), full) {
		t.Fatal("unacknowledged settlement released the final line")
	}
	if _, records := h.settler.counts(); records != 0 {
		t.Fatal("unacknowledged settlement was recorded")
	}
	h.settler.err = ErrBatchUsageIncomplete
	if w = h.do(h.gateway(nil), "GET", "/v1/messages/batches/msgbatch_2/results", "", nil); !bytes.Equal(w.Body.Bytes(), full) {
		t.Fatal("results withheld when only the quota meters were incomplete")
	}
	record, _, _ := h.store.LoadBatch(context.Background(), Owner{"tenant", "alice", "native"}, ProviderAnthropic, "msgbatch_2")
	if record.Settled {
		t.Fatal("batch with incomplete protected meters marked settled")
	}
}

func multipartBody(t *testing.T, fields map[string]string, file string, content []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for key, value := range fields {
		_ = mw.WriteField(key, value)
	}
	if file != "" {
		part, _ := mw.CreateFormFile("file", file)
		_, _ = part.Write(content)
	}
	_ = mw.Close()
	return buf.Bytes(), mw.FormDataContentType()
}

// OpenAI Batch: input upload, create, retrieve (binds output/error files),
// then the output and error files settle the batch once; batch-object usage is
// only a cross-check.
func TestOpenAIBatchSettlesFromOutputAndErrorFiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		usage     string
		wantSettl bool
	}{
		{"cross-check agrees", `{"input_tokens":510,"input_tokens_details":{"cached_tokens":128},"output_tokens":70,"output_tokens_details":{"reasoning_tokens":5},"total_tokens":580}`, true},
		{"cross-check disagrees", `{"input_tokens":999,"output_tokens":70,"total_tokens":1069}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newBatchHarness(t, Provider{ID: ProviderOpenAI}, ProtocolOpenAIBatches, []string{"fixture-model-a"},
				"openai.v1.files-create", "openai.v1.files-content", "openai.v1.batches-create", "openai.v1.batches-get", "openai.v1.batches-cancel")
			var uploaded []byte
			h.mu.Lock()
			h.routes["POST /v1/files"] = func(w http.ResponseWriter, r *http.Request) {
				uploaded = readAll(r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"file-in1","object":"file","purpose":"batch"}`))
			}
			h.mu.Unlock()
			input := []byte(`{"custom_id":"request-1","method":"POST","url":"/v1/chat/completions","body":{}}` + "\n")
			body, contentType := multipartBody(t, map[string]string{"purpose": "batch"}, "input.jsonl", input)
			if w := h.do(h.gateway(nil), "POST", "/v1/files", contentType, body); w.Code != 200 {
				t.Fatalf("upload: %d %s", w.Code, w.Body)
			}
			if !bytes.Equal(uploaded, body) {
				t.Fatal("multipart upload not relayed byte-for-byte")
			}
			h.route("POST", "/v1/batches", 200, "application/json", []byte(`{"id":"batch_fix1","object":"batch","status":"validating","request_counts":{"total":0,"completed":0,"failed":0}}`))
			if w := h.do(h.gateway(nil), "POST", "/v1/batches", "application/json", []byte(`{"input_file_id":"file-in1","endpoint":"/v1/chat/completions","completion_window":"24h"}`)); w.Code != 200 {
				t.Fatalf("create: %d %s", w.Code, w.Body)
			}
			if w := h.do(h.gateway(nil), "POST", "/v1/batches", "application/json", []byte(`{"input_file_id":"file-other","endpoint":"/v1/chat/completions","completion_window":"24h"}`)); w.Code != 403 {
				t.Fatalf("foreign input file accepted: %d", w.Code)
			}
			// Output file before the batch is retrieved: not yet bound, refused.
			if w := h.do(h.gateway(nil), "GET", "/v1/files/file-out1/content", "", nil); w.Code != 403 {
				t.Fatalf("unbound output file readable: %d", w.Code)
			}
			h.route("GET", "/v1/batches/batch_fix1", 200, "application/json", []byte(`{"id":"batch_fix1","object":"batch","status":"completed","output_file_id":"file-out1","error_file_id":"file-err1","request_counts":{"total":3,"completed":2,"failed":1},"usage":`+test.usage+`}`))
			if w := h.do(h.gateway(nil), "GET", "/v1/batches/batch_fix1", "", nil); w.Code != 200 {
				t.Fatalf("get: %d %s", w.Code, w.Body)
			}
			h.route("GET", "/v1/files/file-out1/content", 200, "application/octet-stream", fixture(t, "openai", "batch.output.jsonl"))
			h.route("GET", "/v1/files/file-err1/content", 200, "application/octet-stream", fixture(t, "openai", "batch.errors.jsonl"))
			if w := h.do(h.gateway(nil), "GET", "/v1/files/file-out1/content", "", nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), fixture(t, "openai", "batch.output.jsonl")) {
				t.Fatalf("output: %d", w.Code)
			}
			if settles, _ := h.settler.counts(); settles != 0 {
				t.Fatal("settled before the error file accounted for every item")
			}
			for i := 0; i < 2; i++ {
				if w := h.do(h.gateway(nil), "GET", "/v1/files/file-err1/content", "", nil); w.Code != 200 {
					t.Fatalf("errors: %d", w.Code)
				}
			}
			_, records := h.settler.counts()
			record, _, _ := h.store.LoadBatch(context.Background(), Owner{"tenant", "alice", "native"}, ProviderOpenAI, "batch_fix1")
			if !test.wantSettl {
				if records != 0 || record.Settled || !record.CrossCheckMismatch {
					t.Fatalf("mismatching cross-check settled: %+v", record)
				}
				return
			}
			if records != 1 {
				t.Fatalf("recorded %d times", records)
			}
			s := h.settler.records[0]
			if s.Counters["input_tokens"] != 510 || s.Counters["output_tokens"] != 70 || s.Counters["cached_tokens"] != 128 || s.Counters["thought_tokens"] != 5 || s.Counters["batch_requests"] != 2 || s.Items != 3 {
				t.Fatalf("settlement adds the batch object or misses lines: %+v", s)
			}
		})
	}
}

func readAll(r *http.Request) []byte {
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r.Body)
	return buf.Bytes()
}
