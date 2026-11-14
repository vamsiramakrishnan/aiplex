package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// attachLedger gives a handler an in-memory ledger and returns a reader of
// everything recorded for the tenant (after background writes complete).
func attachLedger(t *testing.T, h *ConnectedHandler) func() []usageledger.Record {
	t.Helper()
	store := usageledger.NewMemoryStore()
	h.Ledger = usageledger.NewRecorder(store, usageledger.WithErrorHandler(func(_ usageledger.Record, err error) { t.Errorf("ledger write failed: %v", err) }))
	return func() []usageledger.Record {
		h.Ledger.Flush()
		page, err := store.List(context.Background(), "tenant", usageledger.Query{Limit: usageledger.MaxLimit})
		if err != nil {
			t.Fatal(err)
		}
		return page.Records
	}
}

func onlyRecord(t *testing.T, records []usageledger.Record) usageledger.Record {
	t.Helper()
	if len(records) != 1 {
		t.Fatalf("expected exactly one usage record, got %d: %+v", len(records), records)
	}
	return records[0]
}

// The record must never hold payloads or credentials.
func assertNoPayload(t *testing.T, r usageledger.Record, secrets ...string) {
	t.Helper()
	raw, _ := json.Marshal(r)
	for _, s := range append(secrets, "hello", "native answer", "caller-token-never-forwarded", "operator-owned-upstream-token") {
		if strings.Contains(string(raw), s) {
			t.Fatalf("usage record leaked %q: %s", s, raw)
		}
	}
}

func putModelPrice(t *testing.T, store resources.Store, spec string) {
	t.Helper()
	var price usageledger.Price
	if err := json.Unmarshal([]byte(spec), &price); err != nil {
		t.Fatal(err)
	}
	if _, err := usageledger.DecodePrice(price.ID, []byte(spec)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: usageledger.PriceKind, ID: price.ID, Spec: json.RawMessage(spec)}, 0); err != nil {
		t.Fatal(err)
	}
}

// Same token prices as the quota fixture's Gemini price book, as a ledger price:
// (3-1)*2 + 1*1 + 2*3 + 1*3 = 14 micro-USD.
const usageTestPrice = `{"id":"model-v1-usd","model_id":"model-v1","provider":"google","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":2,"denominator":1},"cache_read_tokens":{"numerator":1,"denominator":1},"output_tokens":{"numerator":3,"denominator":1},"reasoning_tokens":{"numerator":3,"denominator":1}}}`

func TestUsageLedgerRecordsUnmanagedModelWithEstimatedCost(t *testing.T) {
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, quotaHostModelTerminal)
	}, nil)
	f.handler.Quota = nil
	records := attachLedger(t, f.handler)
	putModelPrice(t, f.resources, usageTestPrice)
	w := httptest.NewRecorder()
	f.serve(w, f.request("generateContent", "", quotaHostModelBody, false), false)
	if w.Code != 200 || w.Body.String() != quotaHostModelTerminal {
		t.Fatalf("native response changed: %d %s", w.Code, w.Body.String())
	}
	r := onlyRecord(t, records())
	if r.Plane != usageledger.PlaneLLM || r.Status != usageledger.StatusOK || r.Source != usageledger.SourceManaged || r.HTTPStatus != 200 || r.Provider != "google" || r.ResourceID != "surface" || r.Operation != "models.generateContent" || r.ModelID != "model-v1" || r.ModelVersion != "model-v1" {
		t.Fatalf("record attribution: %+v", r)
	}
	if r.PrincipalID != f.identity.PrincipalID || r.RequestID != "transport-correlation" || r.QuotaAttemptID != "" || r.Requests != 1 {
		t.Fatalf("record identity: %+v", r)
	}
	u := r.Usage
	if u == nil || *u.InputTokens != 3 || *u.OutputTokens != 2 || *u.CacheReadTokens != 1 || *u.ReasoningTokens != 1 || *u.TotalTokens != 6 || u.CacheWriteTokens != nil {
		t.Fatalf("normalized usage: %+v", u)
	}
	if r.Cost != (usageledger.Cost{Basis: usageledger.BasisEstimated, Currency: "USD", Amount: "14", Scale: 6, PriceID: "model-v1-usd"}) {
		t.Fatalf("estimated cost: %+v", r.Cost)
	}
	assertNoPayload(t, r)
}

func TestUsageLedgerUnknownModelUsageIsNotZero(t *testing.T) {
	const noUsage = `{"responseId":"native-response","candidates":[{"index":0,"content":{"parts":[{"text":"native answer"}]},"finishReason":"STOP"}]}`
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, noUsage)
	}, nil)
	f.handler.Quota = nil
	records := attachLedger(t, f.handler)
	putModelPrice(t, f.resources, usageTestPrice)
	w := httptest.NewRecorder()
	f.serve(w, f.request("generateContent", "", quotaHostModelBody, false), false)
	if w.Code != 200 || w.Body.String() != noUsage {
		t.Fatalf("native response changed: %d", w.Code)
	}
	r := onlyRecord(t, records())
	if r.Usage != nil || r.Cost.Basis != usageledger.BasisUnknown || r.Status != usageledger.StatusOK {
		t.Fatalf("unknown usage must stay unknown and unpriced: %+v", r)
	}
	ledger := usageledger.NewMemoryStore()
	if err := ledger.Append(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	h := NewUsageHandler(ledger, func(*http.Request) (string, error) { return "tenant", nil }, false)
	summary := usageGet(t, h.Summary, "/api/v1/usage/summary?group_by=model_id", adminAccess("admin"), 200)
	var s usageledger.Summary
	_ = json.Unmarshal(summary, &s)
	g := s.Groups[0]
	if g.Records != 1 || g.UnknownUsageRecords != 1 || g.TokensUnknown["input_tokens"] != 1 || g.Tokens["input_tokens"] != 0 || g.UnknownCostRecords != 1 || len(g.Cost) != 0 {
		t.Fatalf("summary reported unknown usage as zero usage: %s", summary)
	}
}

func TestUsageLedgerSettledModelCostAndUnchangedResponse(t *testing.T) {
	f := quotaHostModelNew(t, true, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, quotaHostModelTerminal)
	}, nil)
	records := attachLedger(t, f.handler)
	w := httptest.NewRecorder()
	f.serve(w, f.request("generateContent", "ledger-settled", quotaHostModelBody, false), false)
	if w.Code != 200 || w.Body.String() != quotaHostModelTerminal || f.calls.Load() != 1 {
		t.Fatalf("protected response changed: %d %s", w.Code, w.Body.String())
	}
	attempt := w.Header().Get(execution.AttemptHeader)
	quotaHostModelAssertSettled(t, f, attempt)
	r := onlyRecord(t, records())
	if r.QuotaAttemptID != attempt || r.Status != usageledger.StatusOK {
		t.Fatalf("attempt linkage: %+v", r)
	}
	if r.Cost != (usageledger.Cost{Basis: usageledger.BasisSettled, Currency: "USD", Amount: "14", Scale: 6}) {
		t.Fatalf("settled cost: %+v", r.Cost)
	}
	if r.Usage == nil || *r.Usage.InputTokens != 3 || *r.Usage.CacheReadTokens != 1 || *r.Usage.ReasoningTokens != 1 {
		t.Fatalf("normalized lease usage: %+v", r.Usage)
	}
	// The ledger only reads the attempt: the settlement is the single charge.
	info := quotaHostModelAttempt(t, f, attempt)
	if info.Actual["usd"] != 14 {
		t.Fatalf("settlement changed by ledger: %+v", info.Actual)
	}
	assertNoPayload(t, r)
}

func TestUsageLedgerRecordsDeniedAndErroredModelExchanges(t *testing.T) {
	status := http.StatusInternalServerError
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"message":"provider detail that must not be stored"}}`)
	}, nil)
	f.handler.Quota = nil
	records := attachLedger(t, f.handler)
	w := httptest.NewRecorder()
	f.serve(w, f.request("generateContent", "", quotaHostModelBody, false), false)
	if w.Code != 500 {
		t.Fatalf("upstream error not passed through: %d", w.Code)
	}
	errored := onlyRecord(t, records())
	if errored.Status != usageledger.StatusError || errored.HTTPStatus != 500 || errored.ErrorClass != "server_error" || errored.Usage != nil {
		t.Fatalf("errored exchange: %+v", errored)
	}
	assertNoPayload(t, errored, "provider detail")

	// Denied before dispatch: the caller lacks the model grant.
	before := f.calls.Load()
	r := f.request("generateContent", "", quotaHostModelBody, false)
	access := &models.ResolvedAccess{Identity: *f.identity, Scopes: []string{"llm:surface:surface:*"}}
	r = r.WithContext(context.WithValue(r.Context(), keyWIFAccess, access))
	w = httptest.NewRecorder()
	f.serve(w, r, false)
	if w.Code != 403 || f.calls.Load() != before {
		t.Fatalf("denial changed: %d calls=%d", w.Code, f.calls.Load())
	}
	all := records()
	if len(all) != 2 {
		t.Fatalf("denied exchange not recorded: %+v", all)
	}
	denied := all[0]
	if denied.Status != usageledger.StatusDenied || denied.HTTPStatus != 403 || denied.ErrorClass != "forbidden" || denied.ModelID != "model-v1" {
		t.Fatalf("denied exchange: %+v", denied)
	}
}

func TestUsageLedgerQuotaDenialKeepsQuotaOutcome(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, managedToolResponse)
	}))
	defer upstream.Close()
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	h, quota, request := protectedToolFixture(t, upstream.URL+"/mcp")
	records := attachLedger(t, h)
	w := httptest.NewRecorder()
	h.NativeTool(w, request("first"))
	if w.Code != 200 || w.Body.String() != managedToolResponse {
		t.Fatalf("tool response changed: %d %s", w.Code, w.Body.String())
	}
	w2 := httptest.NewRecorder()
	h.NativeTool(w2, request("second"))
	if w2.Code != 429 || calls.Load() != 1 {
		t.Fatalf("quota outcome changed: %d calls=%d", w2.Code, calls.Load())
	}
	all := records()
	if len(all) != 2 {
		t.Fatalf("records %+v", all)
	}
	denied, ok := all[0], all[1]
	if ok.Plane != usageledger.PlaneMCP || ok.Status != usageledger.StatusOK || ok.ToolName != "search" || ok.Operation != "tools/call" || ok.Provider != "mcp" || ok.QuotaAttemptID != w.Header().Get("X-Aiplex-Attempt-ID") || ok.Cost.Basis != usageledger.BasisUnknown {
		t.Fatalf("tool record: %+v", ok)
	}
	// A tool call's record carries its response size and duration (and no
	// token counters).
	if bytes := ok.Usage.UnitCounter(usageledger.CounterToolResponseBytes); bytes == nil || *bytes != int64(len(managedToolResponse)) || ok.Usage.UnitCounter(usageledger.CounterToolDurationMilliseconds) == nil || ok.Usage.InputTokens != nil {
		t.Fatalf("tool record usage: %+v", ok.Usage)
	}
	if denied.Status != usageledger.StatusDenied || denied.HTTPStatus != 429 || denied.ErrorClass == "" || denied.ToolName != "search" {
		t.Fatalf("quota denial record: %+v", denied)
	}
	info, err := quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", ok.QuotaAttemptID)
	if err != nil || info.Decision.State != "settled" || info.Actual["calls"] != 1 {
		t.Fatalf("settlement changed: %+v %v", info, err)
	}
	assertNoPayload(t, ok, "native tool result", "operator-tool-token")
}

func TestUsageLedgerUnmanagedToolAndLedgerFailureIsolation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, managedToolResponse)
	}))
	defer upstream.Close()
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	h, _, request := protectedToolFixture(t, upstream.URL+"/mcp")
	h.Quota = nil
	var mu sync.Mutex
	failures := 0
	h.Ledger = usageledger.NewRecorder(usageFailingStore{}, usageledger.WithErrorHandler(func(usageledger.Record, error) { mu.Lock(); failures++; mu.Unlock() }))
	w := httptest.NewRecorder()
	h.NativeTool(w, request("first"))
	h.Ledger.Flush()
	if w.Code != 200 || w.Body.String() != managedToolResponse || failures != 1 {
		t.Fatalf("ledger failure leaked into the exchange: %d %s failures=%d", w.Code, w.Body.String(), failures)
	}
}

type usageFailingStore struct{ usageledger.Store }

func (usageFailingStore) Append(context.Context, usageledger.Record) error {
	return io.ErrUnexpectedEOF
}

func TestUsageLedgerAgentCompletedAndTruncated(t *testing.T) {
	var truncate atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if truncate.Load() {
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, "data: partial")
			return
		}
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
	truncate.Store(true)
	w = httptest.NewRecorder()
	h.InvokeAgent(w, request("second"))
	all := records()
	if len(all) != 2 {
		t.Fatalf("records %+v", all)
	}
	aborted, complete := all[0], all[1]
	if complete.Plane != usageledger.PlaneA2A || complete.Status != usageledger.StatusOK || complete.Operation != "stream-query" || complete.Provider == "" || !complete.Streaming {
		t.Fatalf("agent record: %+v", complete)
	}
	if aborted.Status != usageledger.StatusUnknown || aborted.ErrorClass != "stream_aborted" || aborted.HTTPStatus != 200 {
		t.Fatalf("truncated agent stream: %+v", aborted)
	}
	assertNoPayload(t, complete, "native-agent-token", "\"input\"")
}

func TestUsageLedgerModelStreamClientDisconnect(t *testing.T) {
	release := make(chan struct{})
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":3}}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}, nil)
	defer close(release)
	f.handler.Quota = nil
	records := attachLedger(t, f.handler)
	r := f.request("streamGenerateContent", "", quotaHostModelBody, false)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	w := &cancelOnWrite{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	f.serve(w, r.WithContext(ctx), false)
	rec := onlyRecord(t, records())
	if rec.Status != usageledger.StatusUnknown || rec.ErrorClass != "client_disconnected" || !rec.Streaming {
		t.Fatalf("aborted stream: %+v", rec)
	}
	if rec.Usage == nil || *rec.Usage.InputTokens != 3 || rec.Usage.OutputTokens != nil {
		t.Fatalf("partial usage must keep missing counters unknown: %+v", rec.Usage)
	}
}

type cancelOnWrite struct {
	*httptest.ResponseRecorder
	cancel func()
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	w.cancel()
	return n, err
}

func TestUsageLedgerReportedSource(t *testing.T) {
	store := registry.NewMemoryStore()
	resourcesStore := resources.NewMemoryStore()
	putModelPrice(t, resourcesStore, `{"id":"flash","model_id":"gemini-2.5-flash","currency":"USD","scale":9,"meters":{"input_tokens":{"numerator":150,"denominator":1},"output_tokens":{"numerator":600,"denominator":1}}}`)
	ledger := usageledger.NewMemoryStore()
	recorder := usageledger.NewRecorder(ledger)
	h := NewLLMHandler(store, nil, "gateway", nil).WithUsageLedger(recorder, func(*http.Request) (string, error) { return "tenant", nil }, func(ctx context.Context, tenant string) ([]usageledger.Price, error) {
		return modelPricesFrom(ctx, resourcesStore, tenant)
	})
	body := `{"model_id":"gemini-2.5-flash","provider":"google","agent_id":"tutor","user_id":"student","input_tokens":1000,"output_tokens":10,"latency_ms":250}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/llm/usage", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.RecordUsage(w, r)
	if w.Code != 201 {
		t.Fatalf("legacy report changed: %d %s", w.Code, w.Body.String())
	}
	legacy, _ := store.ListUsageRecords(context.Background(), "gemini-2.5-flash", "", time.Time{}, 10)
	if len(legacy) != 1 {
		t.Fatal("legacy usage store no longer receives reports")
	}
	recorder.Flush()
	page, _ := ledger.List(context.Background(), "tenant", usageledger.Query{})
	rec := onlyRecord(t, page.Records)
	if rec.Source != usageledger.SourceReported || rec.ActorID != "tutor" || rec.PrincipalID != "student" || rec.LatencyMS != 250 || rec.FinishedAt.Sub(rec.StartedAt) != 250*time.Millisecond {
		t.Fatalf("reported record: %+v", rec)
	}
	if *rec.Usage.InputTokens != 1000 || *rec.Usage.OutputTokens != 10 || *rec.Usage.TotalTokens != 1010 {
		t.Fatalf("reported usage: %+v", rec.Usage)
	}
	if rec.Cost != (usageledger.Cost{Basis: usageledger.BasisEstimated, Currency: "USD", Amount: "156000", Scale: 9, PriceID: "flash"}) {
		t.Fatalf("reported cost must come from a price resource, not the float report: %+v", rec.Cost)
	}
}

// ── /api/v1/usage handlers ──

func adminAccess(principal string) *models.ResolvedAccess {
	return &models.ResolvedAccess{Identity: models.WIFIdentity{PrincipalID: principal}, Roles: []models.IAMRole{models.RoleAdmin}}
}

func viewerAccess(principal string) *models.ResolvedAccess {
	return &models.ResolvedAccess{Identity: models.WIFIdentity{PrincipalID: principal}, Roles: []models.IAMRole{models.RoleViewer}}
}

func usageGet(t *testing.T, handler http.HandlerFunc, target string, access *models.ResolvedAccess, want int) []byte {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := r.Context()
	if access != nil {
		identity := access.Identity
		ctx = context.WithValue(ctx, keyWIFIdentity, &identity)
		ctx = context.WithValue(ctx, keyWIFAccess, access)
	}
	if strings.HasPrefix(target, "/api/v1/usage/records/") {
		r.SetPathValue("id", strings.TrimPrefix(strings.SplitN(target, "?", 2)[0], "/api/v1/usage/records/"))
	}
	w := httptest.NewRecorder()
	handler(w, r.WithContext(ctx))
	if w.Code != want {
		t.Fatalf("GET %s: expected %d, got %d: %s", target, want, w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func usageFixture(t *testing.T) *UsageHandler {
	t.Helper()
	store := usageledger.NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	add := func(tenant, id, principal, actor string, at time.Time, status string) {
		r := usageledger.Record{ID: id, TenantID: tenant, StartedAt: at, FinishedAt: at, Plane: usageledger.PlaneLLM, Provider: "google", ModelID: "m", PrincipalID: principal, ActorID: actor, Status: status, Source: usageledger.SourceManaged, Requests: 1, Usage: &usageledger.Usage{InputTokens: usageledger.Int64(10)}, Cost: usageledger.UnknownCost()}
		if err := store.Append(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	add("tenant", "r1", "alice", "agent-x", now.Add(-3*time.Minute), usageledger.StatusOK)
	add("tenant", "r2", "bob", "agent-x", now.Add(-2*time.Minute), usageledger.StatusError)
	add("tenant", "r3", "carol", "alice", now.Add(-time.Minute), usageledger.StatusDenied)
	add("other", "o1", "alice", "agent-x", now, usageledger.StatusOK)
	// The tenant comes from the verified binding; a header stands in for it here.
	return NewUsageHandler(store, func(r *http.Request) (string, error) {
		if t := r.Header.Get("X-Test-Tenant"); t != "" {
			return t, nil
		}
		return "tenant", nil
	}, false)
}

func recordIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var page usageledger.Page
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, r := range page.Records {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestUsageAPITenantIsolationAndAdminVisibility(t *testing.T) {
	h := usageFixture(t)
	ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records", adminAccess("root"), 200))
	if strings.Join(ids, ",") != "r3,r2,r1" {
		t.Fatalf("admin must read the whole tenant and only it: %v", ids)
	}
	usageGet(t, h.GetRecord, "/api/v1/usage/records/o1", adminAccess("root"), 404)
	usageGet(t, h.GetRecord, "/api/v1/usage/records/r2", adminAccess("root"), 200)
	var s usageledger.Summary
	_ = json.Unmarshal(usageGet(t, h.Summary, "/api/v1/usage/summary?group_by=status,principal_id", adminAccess("root"), 200), &s)
	if s.RecordsScanned != 3 || len(s.Groups) != 3 {
		t.Fatalf("admin summary: %+v", s)
	}
	// Pagination over the handler.
	page := usageGet(t, h.ListRecords, "/api/v1/usage/records?limit=2", adminAccess("root"), 200)
	var first usageledger.Page
	_ = json.Unmarshal(page, &first)
	if len(first.Records) != 2 || first.NextCursor == "" {
		t.Fatalf("first page: %s", page)
	}
	rest := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?limit=2&cursor="+url.QueryEscape(first.NextCursor), adminAccess("root"), 200))
	if strings.Join(rest, ",") != "r1" {
		t.Fatalf("second page: %v", rest)
	}
}

func TestUsageAPINonAdminSeesOnlyOwnRecords(t *testing.T) {
	h := usageFixture(t)
	ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records", viewerAccess("alice"), 200))
	if strings.Join(ids, ",") != "r3,r1" {
		t.Fatalf("alice is principal of r1 and actor of r3 only: %v", ids)
	}
	// An explicit filter cannot widen visibility.
	if ids = recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?principal_id=bob", viewerAccess("alice"), 200)); len(ids) != 0 {
		t.Fatalf("filter widened visibility: %v", ids)
	}
	usageGet(t, h.GetRecord, "/api/v1/usage/records/r2", viewerAccess("alice"), 404)
	usageGet(t, h.GetRecord, "/api/v1/usage/records/r3", viewerAccess("alice"), 200)
	var s usageledger.Summary
	_ = json.Unmarshal(usageGet(t, h.Summary, "/api/v1/usage/summary", viewerAccess("alice"), 200), &s)
	if s.RecordsScanned != 2 || s.Groups[0].Tokens["input_tokens"] != 20 {
		t.Fatalf("non-admin summary must cover only visible records: %+v", s)
	}
	usageGet(t, h.ListRecords, "/api/v1/usage/records", nil, 401)
}

func TestUsageAPIValidatesQueries(t *testing.T) {
	h := usageFixture(t)
	admin := adminAccess("root")
	for _, target := range []string{
		"/api/v1/usage/summary?group_by=secret",
		"/api/v1/usage/summary?group_by=plane,plane",
		"/api/v1/usage/summary?bucket=week",
		"/api/v1/usage/summary?limit=10",
		"/api/v1/usage/records?group_by=plane",
		"/api/v1/usage/records?limit=0",
		"/api/v1/usage/records?limit=1001",
		"/api/v1/usage/records?limit=x",
		"/api/v1/usage/records?cursor=not-a-cursor",
		"/api/v1/usage/records?since=yesterday",
		"/api/v1/usage/records?since=2026-11-09T00:00:00Z&until=2026-11-08T00:00:00Z",
		"/api/v1/usage/records?plane=other",
		"/api/v1/usage/records?status=fine",
		"/api/v1/usage/records?source=guess",
		"/api/v1/usage/records?model_id=a&model_id=b",
		"/api/v1/usage/records?unknown=1",
	} {
		handler := h.ListRecords
		if strings.HasPrefix(target, "/api/v1/usage/summary") {
			handler = h.Summary
		}
		usageGet(t, handler, target, admin, 400)
	}
	usageGet(t, h.Summary, "/api/v1/usage/summary?group_by=plane,provider,model_id,tool_name,actor_id,principal_id,client_id,status&bucket=day&status=ok&source=managed", admin, 200)
	usageGet(t, h.ListRecords, "/api/v1/usage/records?plane=llmplex&status=error&since=2000-01-01T00:00:00Z", admin, 200)
}

func TestUsagePriceResourceValidation(t *testing.T) {
	store := resources.NewMemoryStore()
	h := cohesionHandler(t, store, "https://example.com")
	put := func(id, spec string) int {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/usage/prices/"+id, strings.NewReader(spec))
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		h.Resources(usageledger.PriceKind)(w, r)
		return w.Code
	}
	if code := put("flash", `{"id":"flash","model_id":"gemini-2.5-flash","currency":"USD","scale":9,"meters":{"input_tokens":{"numerator":150,"denominator":1}}}`); code != 200 {
		t.Fatalf("valid price rejected: %d", code)
	}
	for _, spec := range []string{
		`{"id":"other","model_id":"m","currency":"USD","meters":{"input_tokens":{"numerator":1,"denominator":1}}}`,
		`{"id":"flash","model_id":"m","currency":"USD","meters":{"input_tokens":{"numerator":1,"denominator":0}}}`,
		`{"id":"flash","model_id":"m","currency":"USD","meters":{"input_tokens":0.15}}`,
	} {
		if code := put("flash", spec); code != 400 {
			t.Fatalf("invalid price accepted (%d): %s", code, spec)
		}
	}
	prices, err := h.ModelPrices(context.Background(), "tenant")
	if err != nil || len(prices) != 1 || prices[0].ID != "flash" {
		t.Fatalf("stored prices %+v %v", prices, err)
	}
}

func TestUsageAPIBatchErrorClassAndBoundFilters(t *testing.T) {
	store := usageledger.NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	add := func(id string, mutate func(*usageledger.Record)) {
		r := usageledger.Record{ID: id, TenantID: "tenant", StartedAt: now, FinishedAt: now, Plane: usageledger.PlaneLLM, Status: usageledger.StatusOK, Source: usageledger.SourceManaged, Requests: 1, Cost: usageledger.UnknownCost()}
		mutate(&r)
		if err := store.Append(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	add("plain", func(*usageledger.Record) {})
	add("batch", func(r *usageledger.Record) { r.Batch = true })
	add("limited", func(r *usageledger.Record) { r.Status, r.ErrorClass = usageledger.StatusError, "rate_limited" })
	add("bounded", func(r *usageledger.Record) { r.UsageBound = usageledger.UsageAmbiguous })
	h := NewUsageHandler(store, func(*http.Request) (string, error) { return "tenant", nil }, false)
	admin := adminAccess("root")
	for query, want := range map[string]string{
		"batch=true":               "batch",
		"batch=false":              "bounded,limited,plain",
		"error_class=rate_limited": "limited",
		"usage_bound=ambiguous":    "bounded",
		"usage_bound=none":         "batch,limited,plain",
	} {
		got := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?"+query, admin, 200))
		sort.Strings(got)
		if strings.Join(got, ",") != want {
			t.Errorf("%s: got %v want %s", query, got, want)
		}
	}
	for _, bad := range []string{"batch=1", "batch=yes", "usage_bound=final"} {
		usageGet(t, h.ListRecords, "/api/v1/usage/records?"+bad, admin, 400)
		usageGet(t, h.Summary, "/api/v1/usage/summary?"+bad, admin, 400)
	}
	var s usageledger.Summary
	_ = json.Unmarshal(usageGet(t, h.Summary, "/api/v1/usage/summary?batch=false&group_by=error_class,source", admin, 200), &s)
	if s.RecordsScanned != 3 || len(s.Groups) != 2 {
		t.Fatalf("%+v", s)
	}
}
