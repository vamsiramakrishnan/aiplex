package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// toolUsageOptions configure a tool-usage fixture. The tool binding maps the
// bytes and/or duration meters; the connection sets maxBytes / maxSeconds
// (0: not configured). bytePrice prices tool_bytes in micro-USD per byte
// (0: unpriced).
type toolUsageOptions struct {
	bytes, duration      bool
	maxBytes, maxSeconds int64
	bytePrice            int64
	msPrice              [2]int64 // numerator, denominator in micro-USD per millisecond (0: unpriced)
	budgets              []budgets.Budget
	handler              http.HandlerFunc
	endpoint             string // overrides the fake server (a refused port)
}

type toolUsageFixture struct {
	h       *ConnectedHandler
	quota   *QuotaHost
	calls   *atomic.Int32
	records func() []usageledger.Record
	now     time.Time
	request func(string) *http.Request
}

func toolUsagePrincipal() string {
	return auth.CanonicalPrincipalID("https://identity.example", "people", "subject")
}

func newToolUsageFixture(t *testing.T, o toolUsageOptions) *toolUsageFixture {
	t.Helper()
	calls := &atomic.Int32{}
	handler := o.handler
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, managedToolResponse)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	endpoint := server.URL + "/mcp"
	if o.endpoint != "" {
		endpoint = o.endpoint
	}
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	policy := quotaplex.Policy{Version: quotaplex.Version, TenantID: "tenant", ID: "tool-usage-policy", Revision: 1, EffectiveAt: now.Add(-time.Hour),
		Meters:      map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}, "calls": {ID: "calls", Unit: "calls"}, "tool_bytes": {ID: "tool_bytes", Unit: "bytes", Billable: true}, "tool_ms": {ID: "tool_ms", Unit: "milliseconds"}, "usd": {ID: "usd", Unit: "money", Currency: "USD", Scale: 6}},
		Accounts:    []quotaplex.Account{{ID: "team", TenantWide: true}},
		Concurrency: []quotaplex.ConcurrencyRule{{ID: "active", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 100}}}
	window := quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	if o.bytes {
		policy.Budgets = append(policy.Budgets, quotaplex.BudgetRule{ID: "bytes-cap", Scope: quotaplex.Scope{Kind: "account", ID: "team"}, MeterID: "tool_bytes", Limit: 1 << 30, Period: window})
	}
	if o.duration {
		policy.Budgets = append(policy.Budgets, quotaplex.BudgetRule{ID: "ms-cap", Scope: quotaplex.Scope{Kind: "account", ID: "team"}, MeterID: "tool_ms", Limit: 1 << 30, Period: window})
	}
	var prices []quotaplex.UnitPrice
	if o.bytePrice > 0 {
		prices = append(prices, quotaplex.UnitPrice{MeterID: "tool_bytes", MoneyMeterID: "usd", Numerator: o.bytePrice, Denominator: 1})
	}
	if o.msPrice[0] > 0 {
		policy.Meters["tool_ms"] = quotaplex.Meter{ID: "tool_ms", Unit: "milliseconds", Billable: true}
		prices = append(prices, quotaplex.UnitPrice{MeterID: "tool_ms", MoneyMeterID: "usd", Numerator: o.msPrice[0], Denominator: o.msPrice[1]})
	}
	if prices != nil {
		policy.PriceBooks = []quotaplex.PriceBook{{ID: "tool-usage", Revision: 1, Match: quotaplex.Match{Plane: "mcplex"}, Prices: prices}}
	}
	meters := map[string]string{"requests": "requests", "tool_calls": "calls"}
	if o.bytes {
		meters[toolexec.CounterToolResponseBytes] = "tool_bytes"
	}
	if o.duration {
		meters[toolexec.CounterToolDurationMilliseconds] = "tool_ms"
	}
	config := QuotaHostConfig{Tenants: []QuotaTenantConfig{{Policy: policy, Activate: true}}, Bindings: []QuotaBinding{{ID: "tool-search", TenantID: "tenant", Plane: "mcplex", ResourceID: "surface", OperationID: "tools/call:search", Profile: quotaProfileToolUsage, FundingAccountID: "team", Provider: "mcp", Meters: meters}}}
	quota, err := NewQuotaHost(context.Background(), config, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.budgets != nil {
		tree, err := budgets.NewTree(o.budgets)
		if err != nil {
			t.Fatal(err)
		}
		quota.SetBudgets(&staticBudgets{tree: tree})
	}
	store := resources.NewMemoryStore()
	h := cohesionHandler(t, store, strings.TrimSuffix(endpoint, "/mcp"))
	h.Quota = quota
	records := attachLedger(t, h)
	connection := managedToolConnection(endpoint)
	connection.Execution.MaxResponseBytes, connection.Execution.MaxDurationSeconds = o.maxBytes, o.maxSeconds
	cohesionRecord(t, store, "tool-connection", "surface", connection)
	identity := models.WIFIdentity{PrincipalID: toolUsagePrincipal(), Subject: "subject", Provider: "https://identity.example", PoolID: "people", IsWorkforce: true, VerifiedEvidence: &models.IdentityEvidence{Issuer: "https://identity.example", PoolID: "people", Subject: "subject", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LifetimeValid: true, GroupsPresent: true, GroupsValid: true, Groups: []string{}}}
	h.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) {
		return &models.ResolvedAccess{Identity: identity, Scopes: []string{"mcp:tools:search"}}, nil
	}
	request := func(key string) *http.Request {
		r := trustedConnectedRequest("/api/v1/mcp/connections/surface/invoke", managedToolBody, []string{"mcp:tools:search"})
		ctx := context.WithValue(r.Context(), keyWIFIdentity, &identity)
		ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: identity, Scopes: []string{"mcp:tools:search"}})
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		return r
	}
	return &toolUsageFixture{h: h, quota: quota, calls: calls, records: records, now: now, request: request}
}

func (f *toolUsageFixture) call(key string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.h.NativeTool(w, f.request(key))
	return w
}

func (f *toolUsageFixture) attempt(t *testing.T, w *httptest.ResponseRecorder) quotaplex.AttemptInfo {
	t.Helper()
	info, err := f.quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get(execution.AttemptHeader))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func (f *toolUsageFixture) held(t *testing.T, info quotaplex.AttemptInfo) int64 {
	t.Helper()
	var held int64
	for _, b := range info.Plan.Budgets {
		bal, err := f.quota.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", b.Key)
		if err != nil {
			t.Fatal(err)
		}
		held += bal.Held
	}
	return held
}

// A complete call settles requests, calls, response bytes and duration
// together; the ledger record carries bytes and duration.
func TestToolUsageCompleteCallSettlesBytesAndDuration(t *testing.T) {
	f := newToolUsageFixture(t, toolUsageOptions{bytes: true, duration: true, maxBytes: 4096, maxSeconds: 30, handler: func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(15 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, managedToolResponse)
	}})
	w := f.call("complete")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	info := f.attempt(t, w)
	if info.Decision.State != "settled" || info.Plan.Bounds["tool_bytes"] != 4096 || info.Plan.Bounds["tool_ms"] != 30000 {
		t.Fatalf("admission must reserve the connection's bounds: %+v %+v", info.Decision, info.Plan.Bounds)
	}
	if info.Actual["requests"] != 1 || info.Actual["calls"] != 1 || info.Actual["tool_bytes"] != int64(len(managedToolResponse)) || info.Actual["tool_ms"] < 15 || info.Actual["tool_ms"] > 30000 {
		t.Fatalf("settlement: %+v", info.Actual)
	}
	record := onlyRecord(t, f.records())
	if v := record.Usage.UnitCounter(usageledger.CounterToolResponseBytes); v == nil || *v != int64(len(managedToolResponse)) {
		t.Fatalf("ledger bytes: %+v", record.Usage)
	}
	if v := record.Usage.UnitCounter(usageledger.CounterToolDurationMilliseconds); v == nil || *v < 15 {
		t.Fatalf("ledger duration: %+v", record.Usage)
	}
}

// A mapped meter without the connection's bound is refused before the tool
// server sees anything; the save-time check refuses the connection too.
func TestToolUsageWithoutABoundIsRefused(t *testing.T) {
	for name, o := range map[string]toolUsageOptions{"bytes without max_response_bytes": {bytes: true, maxSeconds: 30}, "duration without max_duration_seconds": {duration: true, maxBytes: 4096}} {
		f := newToolUsageFixture(t, o)
		w := f.call("unbounded")
		if w.Code != 503 || f.calls.Load() != 0 {
			t.Fatalf("%s: %d calls=%d %s", name, w.Code, f.calls.Load(), w.Body.String())
		}
		connection := managedToolConnection("http://127.0.0.1:9/mcp")
		connection.ID, connection.Execution.ID = "surface", "surface"
		connection.Execution.MaxResponseBytes, connection.Execution.MaxDurationSeconds = o.maxBytes, o.maxSeconds
		if err := f.quota.checkToolUsageConnection("tenant", connection); err == nil {
			t.Fatalf("%s: the save-time check accepted the connection", name)
		}
		connection.Execution.MaxResponseBytes, connection.Execution.MaxDurationSeconds = 4096, 30
		if err := f.quota.checkToolUsageConnection("tenant", connection); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// Every ending without a complete response keeps the whole reservation as
// unknown, and the ledger records neither bytes nor duration.
func TestToolUsageIncompleteEndingsStayUnknown(t *testing.T) {
	big := strings.Repeat("x", 8192)
	cases := map[string]toolUsageOptions{
		"cut at the size bound": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"rpc","result":{"content":[{"type":"text","text":"`+big+`"}]}}`)
		}},
		"connection reset": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "1000")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"rpc","result":`)
			w.(http.Flusher).Flush()
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				if tcp, ok := conn.(*net.TCPConn); ok {
					_ = tcp.SetLinger(0)
				}
				_ = conn.Close()
			}
		}},
		"timeout at the duration bound": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"rpc","result":`)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
		}, maxSeconds: 1},
		"server error with a body": {handler: func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"failure"}`, http.StatusInternalServerError)
		}},
	}
	for name, o := range cases {
		o.bytes, o.duration = true, true
		if o.maxBytes == 0 {
			o.maxBytes = 4096
		}
		if o.maxSeconds == 0 {
			o.maxSeconds = 30
		}
		f := newToolUsageFixture(t, o)
		w := f.call("incomplete")
		if w.Code == 200 {
			t.Fatalf("%s: delivered", name)
		}
		info := f.attempt(t, w)
		if info.Decision.State != "unknown" || len(info.Actual) != 0 {
			t.Fatalf("%s: must stay unknown: %+v %+v", name, info.Decision, info.Actual)
		}
		if held := f.held(t, info); held == 0 {
			t.Fatalf("%s: the hold was released", name)
		}
		record := onlyRecord(t, f.records())
		if record.Usage.UnitCounter(usageledger.CounterToolResponseBytes) != nil || record.Usage.UnitCounter(usageledger.CounterToolDurationMilliseconds) != nil {
			t.Fatalf("%s: the ledger recorded bytes or duration for an incomplete call: %+v", name, record.Usage)
		}
		recovery, err := quotaplex.NewRecovery(f.quota.Engines()["tenant"], nil)
		if err != nil {
			t.Fatal(err)
		}
		report, err := recovery.RecoverAttempts(context.Background(), quotaplex.RecoveryPage{TenantID: "tenant", Limit: 100})
		if err != nil || report.Unknown == nil {
			t.Fatal(report, err)
		}
		units := map[string]string{}
		for _, e := range report.Unknown.Exposure {
			if e.Quantity != nil {
				units[e.MeterID] = e.Unit
				if e.MeterID == "tool_bytes" && *e.Quantity != o.maxBytes || e.MeterID == "tool_ms" && *e.Quantity != o.maxSeconds*1000 {
					t.Fatalf("%s: held exposure %+v", name, e)
				}
			}
		}
		if units["tool_bytes"] != "bytes" || units["tool_ms"] != "milliseconds" {
			t.Fatalf("%s: the unknown report must show the meters with their units: %+v", name, report.Unknown.Exposure)
		}
	}
}

// Never-started calls: provably not sent settles at zero; a refusal before
// processing settles one request and nothing else.
func TestToolUsageNeverStartedCalls(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	endpoint := closed.URL + "/mcp"
	closed.Close()
	f := newToolUsageFixture(t, toolUsageOptions{bytes: true, duration: true, maxBytes: 4096, maxSeconds: 30, endpoint: endpoint})
	w := f.call("refused")
	info := f.attempt(t, w)
	if w.Code == 200 || info.Decision.State != "settled" || info.Actual["requests"] != 0 || info.Actual["calls"] != 0 || info.Actual["tool_bytes"] != 0 || f.held(t, info) != 0 {
		t.Fatalf("not sent: %d %+v %+v", w.Code, info.Decision, info.Actual)
	}
	f = newToolUsageFixture(t, toolUsageOptions{bytes: true, duration: true, maxBytes: 4096, maxSeconds: 30, handler: func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}})
	w = f.call("rejected")
	info = f.attempt(t, w)
	if info.Decision.State != "settled" || info.Actual["requests"] != 1 || info.Actual["calls"] != 0 || info.Actual["tool_bytes"] != 0 || info.Actual["tool_ms"] != 0 || f.held(t, info) != 0 {
		t.Fatalf("rejected: %+v %+v", info.Decision, info.Actual)
	}
}

// A binding is checked at host start: a tool-usage binding must be a managed
// tools/call (never a deployment action), map a request meter and a bytes or
// duration meter, and give each the right unit.
func TestToolUsageBindingValidation(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	base := func() QuotaHostConfig {
		policy := quotaplex.Policy{Version: quotaplex.Version, TenantID: "tenant", ID: "p", Revision: 1, EffectiveAt: now.Add(-time.Hour),
			Meters:      map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}, "calls": {ID: "calls", Unit: "calls"}, "tool_bytes": {ID: "tool_bytes", Unit: "bytes"}, "tool_ms": {ID: "tool_ms", Unit: "milliseconds"}, "secs": {ID: "secs", Unit: "seconds"}},
			Accounts:    []quotaplex.Account{{ID: "team", TenantWide: true}},
			Concurrency: []quotaplex.ConcurrencyRule{{ID: "active", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 1}}}
		return QuotaHostConfig{Tenants: []QuotaTenantConfig{{Policy: policy, Activate: true}}, Bindings: []QuotaBinding{{ID: "b", TenantID: "tenant", Plane: "mcplex", ResourceID: "surface", OperationID: "tools/call:search", Profile: quotaProfileToolUsage, FundingAccountID: "team", Provider: "mcp", Meters: map[string]string{"requests": "requests", "tool_calls": "calls", toolexec.CounterToolResponseBytes: "tool_bytes", toolexec.CounterToolDurationMilliseconds: "tool_ms"}}}}
	}
	if _, err := NewQuotaHost(context.Background(), base(), quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*QuotaBinding){
		"duration in seconds":   func(b *QuotaBinding) { b.Meters[toolexec.CounterToolDurationMilliseconds] = "secs" },
		"bytes on a calls unit": func(b *QuotaBinding) { b.Meters[toolexec.CounterToolResponseBytes] = "calls" },
		"neither new meter": func(b *QuotaBinding) {
			delete(b.Meters, toolexec.CounterToolResponseBytes)
			delete(b.Meters, toolexec.CounterToolDurationMilliseconds)
		},
		"deployment action": func(b *QuotaBinding) {
			b.Provider, b.OperationID = "anvil", "tools/call:anvil.execute:infra.networks.create"
		},
		"model plane": func(b *QuotaBinding) { b.Plane = "llmplex" },
		"agent plane": func(b *QuotaBinding) { b.Plane = "a2aplex" },
		"bytes on request-count": func(b *QuotaBinding) {
			b.Profile = quotaProfileRequests
		},
	} {
		cfg := base()
		mutate(&cfg.Bindings[0])
		if _, err := NewQuotaHost(context.Background(), cfg, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// A cost budget that counts tool calls draws the priced bytes as money;
// unpriced bytes follow on_unknown_cost.
func TestToolUsageDrawsFromCostBudgets(t *testing.T) {
	spend := budgets.Budget{ID: "spend", DisplayName: "Tool spend", Limit: budgets.Limit{Unit: budgets.UnitCost, Currency: "USD", Amount: "1.00"}, Window: budgets.Window{Kind: budgets.WindowCalendar, Unit: "month", TimeZone: "UTC"}, Members: budgets.Members{Principals: []string{toolUsagePrincipal()}}, IncludeToolCalls: true}
	f := newToolUsageFixture(t, toolUsageOptions{bytes: true, maxBytes: 4096, bytePrice: 10, budgets: []budgets.Budget{spend}})
	w := f.call("priced")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	engine := f.quota.Engines()["tenant"]
	draw, _ := spend.Draw(6)
	bal, err := engine.DrawBalance(context.Background(), "tenant", draw, f.now)
	if err != nil || bal.Consumed != int64(len(managedToolResponse))*10 || bal.Held != 0 {
		t.Fatalf("priced bytes must draw as money: %+v %v", bal, err)
	}
	// 4096 bytes x 10 = 40960 reserved; a budget below that refuses before
	// the tool server is contacted.
	small := spend
	small.Limit.Amount = "0.040000"
	f = newToolUsageFixture(t, toolUsageOptions{bytes: true, maxBytes: 4096, bytePrice: 10, budgets: []budgets.Budget{small}})
	if w = f.call("too-small"); w.Code != 429 || f.calls.Load() != 0 {
		t.Fatalf("%d calls=%d", w.Code, f.calls.Load())
	}
	for policy, want := range map[string]int{"": 403, budgets.OnUnknownCostAllowAndFlag: 200} {
		b := spend
		b.OnUnknownCost = policy
		f = newToolUsageFixture(t, toolUsageOptions{bytes: true, maxBytes: 4096, budgets: []budgets.Budget{b}})
		if w = f.call("unpriced"); w.Code != want {
			t.Fatalf("unpriced bytes under %q: %d %s", policy, w.Code, w.Body.String())
		}
	}
}

// Priced milliseconds draw too, and with both meters priced into one money
// meter the draw is their exact sum rounded up once (PriceUsage: one ceiling
// per money meter).
func TestToolUsagePricedDurationAndBothMetersDrawTheirSum(t *testing.T) {
	spend := budgets.Budget{ID: "spend", DisplayName: "Tool spend", Limit: budgets.Limit{Unit: budgets.UnitCost, Currency: "USD", Amount: "100.00"}, Window: budgets.Window{Kind: budgets.WindowCalendar, Unit: "month", TimeZone: "UTC"}, Members: budgets.Members{Principals: []string{toolUsagePrincipal()}}, IncludeToolCalls: true}
	draw, _ := spend.Draw(6)
	slow := func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, managedToolResponse)
	}
	for name, o := range map[string]toolUsageOptions{
		"duration only": {duration: true, maxSeconds: 30, msPrice: [2]int64{3, 2}},
		"both":          {bytes: true, duration: true, maxBytes: 4096, maxSeconds: 30, bytePrice: 10, msPrice: [2]int64{3, 2}},
	} {
		o.budgets, o.handler = []budgets.Budget{spend}, slow
		f := newToolUsageFixture(t, o)
		w := f.call("priced")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
		info := f.attempt(t, w)
		ms, bytes := info.Actual["tool_ms"], info.Actual["tool_bytes"]
		// bytes x 10 + ceil(ms x 3/2): exact sum, one ceiling.
		want := bytes*10 + (ms*3+1)/2
		if ms < 5 || info.Actual["usd"] != want {
			t.Fatalf("%s: actual %+v, want usd %d", name, info.Actual, want)
		}
		bal, err := f.quota.Engines()["tenant"].DrawBalance(context.Background(), "tenant", draw, f.now)
		if err != nil || bal.Consumed != want || bal.Held != 0 {
			t.Fatalf("%s: budget draw %+v %v, want %d", name, bal, err, want)
		}
	}
}

// Refusals before dispatch release the reservation (or never make one): an
// unresolvable credential, a dispatch grant revoked after admission, and a
// connection bound changed between admission and dispatch. None reaches the
// tool server, holds anything, or appears in the unknown report.
func TestToolUsageRefusalsBeforeDispatchHoldNothing(t *testing.T) {
	check := func(name string, f *toolUsageFixture, w *httptest.ResponseRecorder, wantCode int) {
		t.Helper()
		if w.Code != wantCode || f.calls.Load() != 0 {
			t.Fatalf("%s: %d calls=%d %s", name, w.Code, f.calls.Load(), w.Body.String())
		}
		if id := w.Header().Get(execution.AttemptHeader); id != "" {
			info := f.attempt(t, w)
			if info.Decision.State != "aborted" || f.held(t, info) != 0 {
				t.Fatalf("%s: %+v held=%d", name, info.Decision, f.held(t, info))
			}
		}
		recovery, err := quotaplex.NewRecovery(f.quota.Engines()["tenant"], nil)
		if err != nil {
			t.Fatal(err)
		}
		report, err := recovery.RecoverAttempts(context.Background(), quotaplex.RecoveryPage{TenantID: "tenant", Limit: 100})
		if err != nil || report.Unknown == nil || report.Unknown.Attempts != 0 {
			t.Fatalf("%s: unknown report %+v %v", name, report.Unknown, err)
		}
	}
	opts := toolUsageOptions{bytes: true, duration: true, maxBytes: 4096, maxSeconds: 30}

	f := newToolUsageFixture(t, opts)
	t.Setenv("COHESION_HOST_CREDENTIAL", "")
	check("unresolvable credential", f, f.call("no-credential"), http.StatusServiceUnavailable)

	f = newToolUsageFixture(t, opts)
	initial, checks := f.h.ResolveAccess, 0
	f.h.ResolveAccess = func(ctx context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
		checks++
		access, err := initial(ctx, identity)
		if checks > 1 {
			access.Scopes = nil
		}
		return access, err
	}
	check("grant revoked before dispatch", f, f.call("revoked"), http.StatusForbidden)

	for name, change := range map[string]func(*ToolConnection){
		"max_response_bytes lowered":   func(c *ToolConnection) { c.Execution.MaxResponseBytes = 1024 },
		"max_duration_seconds removed": func(c *ToolConnection) { c.Execution.MaxDurationSeconds = 0 },
	} {
		f = newToolUsageFixture(t, opts)
		initial, checks := f.h.ResolveAccess, 0
		f.h.ResolveAccess = func(ctx context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
			checks++
			if checks == 2 { // after admission, before the dispatch recheck
				record, err := f.h.Store.Get(ctx, "tenant", "tool-connection", "surface")
				if err != nil {
					return nil, err
				}
				var connection ToolConnection
				if err = json.Unmarshal(record.Spec, &connection); err != nil {
					return nil, err
				}
				change(&connection)
				record.Spec, _ = json.Marshal(connection)
				if _, err = f.h.Store.Put(ctx, record, record.Revision); err != nil {
					return nil, err
				}
			}
			return initial(ctx, identity)
		}
		// The dispatch recheck finds the pinned configuration changed
		// (quota_tool.go Reauthorize) and the quota host refuses with
		// QUOTA_EXECUTION_REVOKED.
		w := f.call("changed")
		if !strings.Contains(w.Body.String(), "configuration changed") {
			t.Fatalf("%s: %s", name, w.Body.String())
		}
		check(name, f, w, http.StatusForbidden)
	}
}

// A tool-usage binding names one exact connection: an empty or wildcard
// resource id is refused at start (the coordinates check every binding
// passes), so the save-time check sees every connection a binding covers.
func TestToolUsageBindingNeedsAnExactConnection(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for _, resource := range []string{"", "*", "surf*"} {
		policy := quotaplex.Policy{Version: quotaplex.Version, TenantID: "tenant", ID: "p", Revision: 1, EffectiveAt: now.Add(-time.Hour),
			Meters:      map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}, "tool_bytes": {ID: "tool_bytes", Unit: "bytes"}},
			Accounts:    []quotaplex.Account{{ID: "team", TenantWide: true}},
			Concurrency: []quotaplex.ConcurrencyRule{{ID: "active", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 1}}}
		config := QuotaHostConfig{Tenants: []QuotaTenantConfig{{Policy: policy, Activate: true}}, Bindings: []QuotaBinding{{ID: "b", TenantID: "tenant", Plane: "mcplex", ResourceID: resource, OperationID: "tools/call:search", Profile: quotaProfileToolUsage, FundingAccountID: "team", Provider: "mcp", Meters: map[string]string{"requests": "requests", toolexec.CounterToolResponseBytes: "tool_bytes"}}}}
		if _, err := NewQuotaHost(context.Background(), config, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil); err == nil {
			t.Fatalf("resource %q accepted", resource)
		}
	}
}
