package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// voiceQuotaFixture is an OpenAI Live voice surface behind the connected
// handler, metered by session seconds. The voice_seconds meter is priced at
// secondPrice micro-USD per second (0: unpriced); maxSeconds is the surface's
// max_session_seconds (0: the surface does not meter by seconds).
type voiceFixtureOptions struct {
	secondPrice int64
	maxSeconds  int64
	budgets     []budgets.Budget
	upstream    func(*websocket.Conn)
	// handshake answers the upstream handshake with this status instead of
	// upgrading.
	handshake int
	// requestCount binds the operation with the request-count profile, and
	// the surface is stored without the save-time check.
	requestCount bool
}

func voiceQuotaFixture(t *testing.T, o voiceFixtureOptions) *quotaHostModelFixture {
	t.Helper()
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		if o.handshake != 0 {
			http.Error(w, `{"error":{"type":"upstream"}}`, o.handshake)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		o.upstream(conn)
	}, func(cfg *QuotaHostConfig) {
		cfg.Bindings = cfg.Bindings[:1]
		binding := &cfg.Bindings[0]
		binding.OperationID, binding.Profile, binding.Provider = "openai.v1.live", quotaProfileVoiceSeconds, modelapi.ProviderOpenAI
		binding.BillingAccount, binding.Region = "", ""
		binding.Meters = map[string]string{"requests": "requests", quotaVoiceSecondsCounter: "voice_seconds"}
		if o.requestCount {
			binding.Profile, binding.Meters = quotaProfileRequests, map[string]string{"requests": "requests"}
		}
		policy := &cfg.Tenants[0].Policy
		policy.Meters["voice_seconds"] = quotaplex.Meter{ID: "voice_seconds", Unit: "seconds", Billable: true}
		policy.Meters["usd"] = quotaplex.Meter{ID: "usd", Unit: "money", Currency: "USD", Scale: 6}
		if o.secondPrice > 0 {
			policy.PriceBooks = []quotaplex.PriceBook{{ID: "voice-tariff", Revision: 1, Match: quotaplex.Match{Provider: modelapi.ProviderOpenAI}, Prices: []quotaplex.UnitPrice{{MeterID: "voice_seconds", MoneyMeterID: "usd", Numerator: o.secondPrice, Denominator: 1}}}}
		}
	})
	if o.budgets != nil {
		tree, err := budgets.NewTree(o.budgets)
		if err != nil {
			t.Fatal(err)
		}
		f.host.SetBudgets(&staticBudgets{tree: tree})
	}
	installProviderCredentials(t, f.handler)
	connection := providerConnection(t, "", modelapi.Provider{ID: modelapi.ProviderOpenAI}, modelapi.ProtocolOpenAILive, bearerRef, "openai.v1.live")
	record, _ := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	var prior ModelConnection
	_ = json.Unmarshal(record.Spec, &prior)
	connection.Surface.Origin = prior.Surface.Origin
	connection.Surface.WebSocket = &modelapi.WebSocketSessions{MaxSessionSeconds: 600}
	if o.maxSeconds > 0 {
		connection.Surface.WebSocket = &modelapi.WebSocketSessions{Metering: modelapi.SessionMeteringSeconds, MaxSessionSeconds: o.maxSeconds}
	}
	err := f.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection))
	if o.requestCount && o.maxSeconds > 0 {
		if err == nil || !strings.Contains(err.Error(), quotaProfileVoiceSeconds) {
			t.Fatalf("a seconds surface over a request-count binding must be refused at save: %v", err)
		}
	} else if err != nil {
		t.Fatal("surface rejected:", err)
	}
	putProviderSurface(t, f.resources, connection)
	return f
}

// voiceUpstream replays a recorded OpenAI Live fixture after session.start.
// With closedSeconds it instead plays a minimal session that closes with that
// length (snapshots are cumulative, so the recorded ones would exceed it).
func voiceUpstream(t *testing.T, file string, closedSeconds string) func(*websocket.Conn) {
	t.Helper()
	data, err := os.ReadFile("../../pkg/modelapi/testdata/usage-formats/openai/" + file)
	if err != nil {
		t.Fatal(err)
	}
	if closedSeconds != "" {
		data = []byte(`{"type":"session.started","event_id":"evt_s0"}` + "\n" + `{"type":"session.closed","event_id":"evt_s1","reason":"close_requested","usage":{"seconds":` + closedSeconds + `}}` + "\n")
	}
	interrupted := strings.Contains(file, "interrupted")
	return func(conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
				if conn.WriteMessage(websocket.TextMessage, line) != nil {
					return
				}
			}
		}
		if interrupted {
			_ = conn.UnderlyingConn().Close()
			return
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}
}

// voiceDial opens a session and returns the HTTP status of a refused upgrade
// (0 when the session ran to its end).
func (f *quotaHostModelFixture) voiceSession(t *testing.T) int {
	t.Helper()
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		ctx := context.WithValue(r.Context(), keyWIFIdentity, f.identity)
		ctx = context.WithValue(ctx, keyWIFAccess, f.access())
		ctx = context.WithValue(ctx, keyRequestID, "session-correlation")
		r = r.WithContext(ctx)
		r.SetPathValue("id", "surface")
		r.URL.Path = "/api/v1/llm/native/surface" + r.URL.Path
		f.handler.NativeModel(w, r)
	}))
	defer server.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/live/sessions?model=model-v1", nil)
	if err != nil {
		<-done
		if response == nil {
			t.Fatal(err)
		}
		return response.StatusCode
	}
	defer conn.Close()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.start","session":{"model":"model-v1"}}`))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	conn.Close()
	<-done
	return 0
}

func (f *quotaHostModelFixture) voiceAttempts(t *testing.T) []quotaplex.AttemptInfo {
	t.Helper()
	var out []quotaplex.AttemptInfo
	for _, record := range quotaHostTestAttempts(t, f.store) {
		info, err := f.host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", record.ID)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, info)
	}
	return out
}

// Admission reserves the configured maximum; settlement uses session.closed,
// rounded up to whole seconds (45.8 s -> 46, 0.1 s -> 1, never 0).
func TestVoiceSecondsReserveTheMaximumAndSettleRoundedUp(t *testing.T) {
	for closed, want := range map[string]int64{"": 46, "0.1": 1, "0.0004": 1, "0.0000000001": 1, "0": 0, "12": 12} {
		name := closed
		if name == "" {
			name = "recorded fixture (45.8 s)"
		}
		t.Run(name, func(t *testing.T) {
			f := voiceQuotaFixture(t, voiceFixtureOptions{secondPrice: 10, maxSeconds: 120, upstream: voiceUpstream(t, "live.ws.jsonl", closed)})
			if status := f.voiceSession(t); status != 0 {
				t.Fatalf("session refused: %d", status)
			}
			attempts := f.voiceAttempts(t)
			if len(attempts) == 1 && (attempts[0].Plan.Bounds["voice_seconds"] != 120 || attempts[0].Plan.Bounds["usd"] != 1200) {
				t.Fatalf("admission must reserve the configured maximum: %+v", attempts[0].Plan.Bounds)
			}
			if len(attempts) != 1 || attempts[0].Decision.State != "settled" || attempts[0].Actual["voice_seconds"] != want || attempts[0].Actual["requests"] != 1 || attempts[0].Actual["usd"] != want*10 {
				t.Fatalf("settlement: %+v", attempts)
			}
		})
	}
}

// No configured maximum (the surface does not meter by seconds): refused before
// the upstream connection is made.
func TestVoiceSecondsWithoutAMaximumIsRefusedBeforeTheDial(t *testing.T) {
	f := voiceQuotaFixture(t, voiceFixtureOptions{secondPrice: 10, upstream: voiceUpstream(t, "live.ws.jsonl", "")})
	if status := f.voiceSession(t); status != http.StatusServiceUnavailable {
		t.Fatalf("status %d", status)
	}
	if f.calls.Load() != 0 || len(f.voiceAttempts(t)) != 0 {
		t.Fatalf("a session with no maximum reached the provider or was admitted: calls=%d", f.calls.Load())
	}
}

// A session without session.closed keeps its whole reservation as unknown:
// not settled at the lower-bound snapshot (12 s) and not at zero. The unknown
// report shows the held seconds in their unit.
func TestVoiceSecondsInterruptedSessionStaysUnknown(t *testing.T) {
	f := voiceQuotaFixture(t, voiceFixtureOptions{secondPrice: 10, maxSeconds: 120, upstream: voiceUpstream(t, "live.interrupted.ws.jsonl", "")})
	if status := f.voiceSession(t); status != 0 {
		t.Fatalf("session refused: %d", status)
	}
	attempts := f.voiceAttempts(t)
	if len(attempts) != 1 || attempts[0].Decision.State != "unknown" || len(attempts[0].Actual) != 0 {
		t.Fatalf("an interrupted session must stay unknown: %+v", attempts)
	}
	recovery, err := quotaplex.NewRecovery(f.host.Engines()["tenant"], nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := recovery.RecoverAttempts(context.Background(), quotaplex.RecoveryPage{TenantID: "tenant", Limit: 100})
	if err != nil || report.Unknown == nil || report.Unknown.Attempts != 1 {
		t.Fatalf("unknown report: %+v %v", report, err)
	}
	found := false
	for _, e := range report.Unknown.Exposure {
		if e.MeterID == "voice_seconds" {
			found = true
			if e.Unit != "seconds" || e.Quantity == nil || *e.Quantity != 120 {
				t.Fatalf("held seconds in the unknown report: %+v", e)
			}
		}
		if e.MeterID == "usd" && (e.Quantity == nil || *e.Quantity != 1200) {
			t.Fatalf("held cost in the unknown report: %+v", e)
		}
	}
	if !found {
		t.Fatalf("the seconds meter is missing from the unknown report: %+v", report.Unknown.Exposure)
	}
}

// A cost budget draws the priced seconds like any priced meter: it reserves
// the maximum, refuses before the dial when it cannot cover it, and settles
// the rounded length. Unpriced seconds follow on_unknown_cost.
func TestVoiceSecondsDrawFromCostBudgets(t *testing.T) {
	// 120 s x 10 micro-USD = 1200 reserved; 0.001500 USD covers one session.
	spend := costBudget("voice", "Voice spend", "0.001500", "", "engineering")
	f := voiceQuotaFixture(t, voiceFixtureOptions{secondPrice: 10, maxSeconds: 120, budgets: []budgets.Budget{spend}, upstream: voiceUpstream(t, "live.ws.jsonl", "")})
	if status := f.voiceSession(t); status != 0 {
		t.Fatalf("first session: %d", status)
	}
	bal := voiceBudgetBalance(t, f, spend)
	if bal.Consumed != 460 || bal.Held != 0 {
		t.Fatalf("46 s x 10 must be drawn: %+v", bal)
	}
	// 1040 left, 1200 needed for the maximum: refused before the dial.
	calls := f.calls.Load()
	if status := f.voiceSession(t); status != http.StatusTooManyRequests {
		t.Fatalf("a budget that cannot cover the maximum must refuse: %d", status)
	}
	if f.calls.Load() != calls {
		t.Fatal("the refused session reached the provider")
	}

	for policy, want := range map[string]int{"": http.StatusForbidden, budgets.OnUnknownCostAllowAndFlag: 0} {
		b := costBudget("voice", "Voice spend", "1.00", policy, "engineering")
		f = voiceQuotaFixture(t, voiceFixtureOptions{maxSeconds: 120, budgets: []budgets.Budget{b}, upstream: voiceUpstream(t, "live.ws.jsonl", "")})
		if status := f.voiceSession(t); status != want {
			t.Fatalf("unpriced seconds under on_unknown_cost %q: %d", policy, status)
		}
		bal = voiceBudgetBalance(t, f, b)
		if policy == "" && (f.calls.Load() != 0 || bal.UnknownCost != 0 || bal.Consumed != 0) {
			t.Fatalf("deny: %+v calls=%d", bal, f.calls.Load())
		}
		if policy != "" && (bal.UnknownCost != 1 || bal.Consumed != 0 || bal.Held != 0) {
			t.Fatalf("allow_and_flag: %+v", bal)
		}
	}
}

func voiceBudgetBalance(t *testing.T, f *quotaHostModelFixture, b budgets.Budget) quotaplex.DrawBalance {
	t.Helper()
	engine := f.host.Engines()["tenant"]
	scale, err := budgetDrawScale(b, engine.PolicyMeters())
	if err != nil {
		t.Fatal(err)
	}
	draw, err := b.Draw(scale)
	if err != nil {
		t.Fatal(err)
	}
	bal, err := engine.DrawBalance(context.Background(), "tenant", draw, f.now)
	if err != nil {
		t.Fatal(err)
	}
	return bal
}

// The profile is checked at host start: only the Live voice operation, with
// the seconds counter mapped to a seconds meter.
func TestVoiceSecondsBindingValidation(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	base := func() QuotaHostConfig {
		cfg := quotaHostTestConfig(now)
		cfg.Tenants[0].Policy.Meters["voice_seconds"] = quotaplex.Meter{ID: "voice_seconds", Unit: "seconds"}
		cfg.Tenants[0].Policy.Meters["voice_ms"] = quotaplex.Meter{ID: "voice_ms", Unit: "milliseconds"}
		b := &cfg.Bindings[0]
		b.OperationID, b.Profile, b.Provider, b.BillingAccount, b.Region = "openai.v1.live", quotaProfileVoiceSeconds, modelapi.ProviderOpenAI, "", ""
		b.Meters = map[string]string{"requests": "requests", quotaVoiceSecondsCounter: "voice_seconds"}
		return cfg
	}
	if _, err := NewQuotaHost(context.Background(), base(), quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*QuotaBinding){
		"other operation": func(b *QuotaBinding) { b.OperationID = "openai.v1.realtime" },
		"wrong unit":      func(b *QuotaBinding) { b.Meters[quotaVoiceSecondsCounter] = "voice_ms" },
		"no seconds":      func(b *QuotaBinding) { delete(b.Meters, quotaVoiceSecondsCounter) },
		"token counter":   func(b *QuotaBinding) { b.Meters["input_tokens"] = "requests" },
	} {
		cfg := base()
		mutate(&cfg.Bindings[0])
		if _, err := NewQuotaHost(context.Background(), cfg, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestVoiceSecondsRoundingIsUpward(t *testing.T) {
	for ms, want := range map[int64]int64{0: 0, 1: 1, 100: 1, 999: 1, 1000: 1, 1001: 2, 45800: 46} {
		native := quotaVoiceSecondsNative(map[string]int64{modelapi.CounterVoiceSessionMilliseconds: ms})
		if native[quotaVoiceSecondsCounter] != want || native[modelapi.CounterVoiceSessionMilliseconds] != ms {
			t.Fatalf("%d ms -> %v, want %d s", ms, native, want)
		}
	}
	if _, present := quotaVoiceSecondsNative(map[string]int64{"requests": 1})[quotaVoiceSecondsCounter]; present {
		t.Fatal("seconds derived from a missing length")
	}
}

// A surface that meters by seconds over a request-count binding: refused at
// save (fixture), and if stored anyway, refused before the dial with the
// lease released.
func TestVoiceSecondsSurfaceOverARequestCountBindingIsRefused(t *testing.T) {
	f := voiceQuotaFixture(t, voiceFixtureOptions{maxSeconds: 120, requestCount: true, upstream: voiceUpstream(t, "live.ws.jsonl", "")})
	if status := f.voiceSession(t); status != http.StatusNotImplemented {
		t.Fatalf("status %d", status)
	}
	if f.calls.Load() != 0 {
		t.Fatal("the session reached the provider")
	}
	for _, info := range f.voiceAttempts(t) {
		if info.Decision.State != "aborted" || len(info.Actual) != 0 {
			t.Fatalf("the request-count lease must be released, not settled: %+v", info.Decision)
		}
	}
}

// A handshake answered with a documented not-processed status settles one
// request and zero seconds, releasing the reservation; any other answer keeps
// the hold as unknown.
func TestVoiceSecondsRejectedHandshakeReleasesTheReservation(t *testing.T) {
	f := voiceQuotaFixture(t, voiceFixtureOptions{secondPrice: 10, maxSeconds: 120, handshake: http.StatusForbidden})
	_ = f.voiceSession(t)
	attempts := f.voiceAttempts(t)
	if len(attempts) != 1 || attempts[0].Decision.State != "settled" || attempts[0].Actual["requests"] != 1 || attempts[0].Actual["voice_seconds"] != 0 || attempts[0].Actual["usd"] != 0 {
		t.Fatalf("403 handshake: %+v", attempts)
	}
	f = voiceQuotaFixture(t, voiceFixtureOptions{secondPrice: 10, maxSeconds: 120, handshake: http.StatusBadGateway})
	_ = f.voiceSession(t)
	attempts = f.voiceAttempts(t)
	if len(attempts) != 1 || attempts[0].Decision.State != "unknown" || attempts[0].Plan.Bounds["voice_seconds"] != 120 {
		t.Fatalf("502 handshake must stay unknown holding the maximum: %+v", attempts)
	}
}

// A session closed before its deadline lasted at most N seconds, so its
// confirmed length in milliseconds is at most N*1000; rounding up to whole
// seconds then never exceeds the N-second hold.
func TestVoiceSecondsRoundingNeverExceedsTheHold(t *testing.T) {
	for _, n := range []int64{60, 120, 600, 86400} {
		for ms := int64(0); ms <= n*1000; ms += 7 {
			if s := quotaVoiceSecondsNative(map[string]int64{modelapi.CounterVoiceSessionMilliseconds: ms})[quotaVoiceSecondsCounter]; s > n {
				t.Fatalf("%d ms -> %d s exceeds %d", ms, s, n)
			}
		}
		for _, ms := range []int64{n*1000 - 1, n * 1000} {
			if s := quotaVoiceSecondsNative(map[string]int64{modelapi.CounterVoiceSessionMilliseconds: ms})[quotaVoiceSecondsCounter]; s != n {
				t.Fatalf("%d ms -> %d s, want %d", ms, s, n)
			}
		}
	}
}

// An unknown voice session is settled by a reviewed reconciliation with its
// confirmed length: the seconds are settled at the pinned price and the rest
// of the hold is released. Computed meters (usd) are not part of the usage.
func TestVoiceSecondsUnknownSessionReconciles(t *testing.T) {
	f := voiceQuotaFixture(t, voiceFixtureOptions{secondPrice: 10, maxSeconds: 120, upstream: voiceUpstream(t, "live.interrupted.ws.jsonl", "")})
	_ = f.voiceSession(t)
	attempts := f.voiceAttempts(t)
	if len(attempts) != 1 || attempts[0].Decision.State != "unknown" {
		t.Fatalf("%+v", attempts)
	}
	engine := f.host.Engines()["tenant"]
	ctx := context.Background()
	author := quotaplex.Administrator{PrincipalID: "investigator", RequestID: "plan"}
	request := quotaplex.ReconciliationRequest{TenantID: "tenant", ID: "voice-correction", AttemptID: attempts[0].Demand.AttemptID, Reason: "Provider confirms the session length", EvidenceRef: "case/voice-42", Usage: quotaplex.Vector{"requests": 1, "voice_seconds": 46, "usd": 460}, ExpiresAt: f.now.Add(30 * time.Minute)}
	if _, err := engine.PrepareReconciliation(ctx, request, author, false); err == nil {
		t.Fatal("a reconciliation carrying the computed usd meter was accepted")
	}
	request.ID, request.Usage = "voice-correction-2", quotaplex.Vector{"requests": 1, "voice_seconds": 46}
	plan, err := engine.PrepareReconciliation(ctx, request, author, false)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := engine.ApplyReconciliation(ctx, "tenant", plan.ID, plan.Digest, quotaplex.Administrator{PrincipalID: "reviewer", RequestID: "approve"})
	if err != nil || receipt.Decision.State != "settled" {
		t.Fatal(receipt, err)
	}
	info, err := engine.GetAttempt(ctx, "tenant", attempts[0].Demand.AttemptID)
	if err != nil || info.Actual["voice_seconds"] != 46 || info.Actual["usd"] != 460 {
		t.Fatalf("reconciled: %+v %v", info.Actual, err)
	}
	for _, budget := range info.Plan.Budgets {
		balance, err := engine.BudgetBalance(ctx, "tenant", budget.Key)
		if err != nil || balance.Held != 0 {
			t.Fatalf("the rest of the hold was not released: %+v %v", balance, err)
		}
	}
}
