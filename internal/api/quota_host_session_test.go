package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// sessionQuotaFixture builds a token-metered WebSocket surface behind the
// connected handler. upstream scripts the provider socket.
func sessionQuotaFixture(t *testing.T, profile, operation, provider string, meters map[string]int64, moneyLimit int64, google bool, allowance int64, upstream func(*websocket.Conn)) (*quotaHostModelFixture, string) {
	t.Helper()
	f := quotaHostModelNew(t, true, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		upstream(conn)
	}, func(cfg *QuotaHostConfig) {
		cfg.Bindings = cfg.Bindings[:1]
		binding := &cfg.Bindings[0]
		binding.OperationID, binding.Profile, binding.Provider = operation, profile, provider
		if !google {
			binding.BillingAccount, binding.Region = "", ""
		}
		policy := &cfg.Tenants[0].Policy
		for _, native := range []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"} {
			delete(binding.Meters, native)
			delete(policy.Meters, native)
		}
		budgets := policy.Budgets[:0]
		for _, budget := range policy.Budgets {
			if budget.MeterID == "usd" {
				budget.Limit = moneyLimit
				budgets = append(budgets, budget)
			} else if budget.MeterID == "requests" {
				budgets = append(budgets, budget)
			}
		}
		policy.Budgets = budgets
		book := &policy.PriceBooks[0]
		book.Prices = nil
		for native, price := range meters {
			policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens", Billable: true}
			binding.Meters[native] = native
			book.Prices = append(book.Prices, quotaplex.UnitPrice{MeterID: native, MoneyMeterID: "usd", Numerator: price, Denominator: 1})
		}
		book.Match.Provider = provider
		if !google {
			book.Match.BillingAccount, book.Match.Region = "", ""
		}
	})
	record, _ := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	var connection ModelConnection
	_ = json.Unmarshal(record.Spec, &connection)
	path := ""
	if google {
		ops, err := modelapi.SelectOperations(operation)
		if err != nil {
			t.Fatal(err)
		}
		connection.Surface.Operations = ops
		connection.Surface.WebSocket = &modelapi.WebSocketSessions{Metering: modelapi.SessionMeteringToken, TokenAllowance: allowance}
		path = "/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent"
	} else {
		installProviderCredentials(t, f.handler)
		connection = providerConnection(t, connection.Surface.Origin, modelapi.Provider{ID: provider}, modelapi.ProtocolOpenAIRealtime, bearerRef, operation)
		connection.Surface.WebSocket = &modelapi.WebSocketSessions{Metering: modelapi.SessionMeteringToken}
		path = "/v1/realtime?model=model-v1"
	}
	if err := f.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal("surface rejected:", err)
	}
	putProviderSurface(t, f.resources, connection)
	return f, path
}

func (f *quotaHostModelFixture) dialSession(t *testing.T, path string) (*websocket.Conn, chan struct{}) {
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
	t.Cleanup(server.Close)
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path, nil)
	if err != nil {
		body := ""
		if response != nil {
			raw := make([]byte, 512)
			n, _ := response.Body.Read(raw)
			body = fmt.Sprint(response.StatusCode, " ", string(raw[:n]))
		}
		t.Fatal(err, body)
	}
	return conn, done
}

func sessionBudgets(t *testing.T, f *quotaHostModelFixture) (held, consumed int64) {
	t.Helper()
	records := quotaHostTestAttempts(t, f.store)
	seen := map[string]bool{}
	for _, record := range records {
		info, err := f.host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", record.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, budget := range info.Plan.Budgets {
			if budget.MeterID != "usd" || seen[budget.Key] {
				continue
			}
			seen[budget.Key] = true
			balance, err := f.host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
			if err != nil {
				t.Fatal(err)
			}
			held += balance.Held
			consumed += balance.Consumed
		}
	}
	return held, consumed
}

const realtimeDone = `{"type":"response.done","response":{"object":"realtime.response","id":"resp_1","status":"completed","usage":{"total_tokens":15,"input_tokens":10,"output_tokens":5,"input_token_details":{"text_tokens":6,"audio_tokens":4,"image_tokens":0,"cached_tokens":2,"cached_tokens_details":{"text_tokens":2,"audio_tokens":0,"image_tokens":0}},"output_token_details":{"text_tokens":1,"audio_tokens":4}}}}`

func TestManagedRealtimeAdmitsEachResponseAndRefusesNatively(t *testing.T) {
	meters := map[string]int64{}
	for _, native := range append(append([]string(nil), quotaRealtimeInputCounters...), quotaRealtimeOutputCounters...) {
		meters[native] = 1
	}
	// One hold = 6 input subsets x 100 + 2 output subsets x 3 = 606 usd.
	// After the first settles (15), 605 remain: the second is refused.
	f, path := sessionQuotaFixture(t, quotaProfileRealtime, "openai.v1.realtime", modelapi.ProviderOpenAI, meters, 620, false, 0, func(conn *websocket.Conn) {
		_, _, _ = conn.ReadMessage() // session.update
		_, _, _ = conn.ReadMessage() // admitted response.create
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(realtimeDone))
		_, payload, _ := conn.ReadMessage() // the next forwarded event
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"echo","of":`+string(payload)+`}`))
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_, _, _ = conn.ReadMessage()
	})
	conn, done := f.dialSession(t, path)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	create := `{"type":"response.create","event_id":"c1","response":{"max_output_tokens":3,"output_modalities":["audio"]}}`
	// Automatic responses must be disabled before anything is admitted.
	_ = conn.WriteMessage(websocket.TextMessage, []byte(create))
	_, refused, _ := conn.ReadMessage()
	if !strings.Contains(string(refused), "AUTOMATIC_RESPONSES_ENABLED") {
		t.Fatalf("unarmed session not refused natively: %s", refused)
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{"type":"realtime","audio":{"input":{"turn_detection":{"type":"server_vad","create_response":false}}}}}`))
	_ = conn.WriteMessage(websocket.TextMessage, []byte(create))
	var settledBefore int64
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), `"response.done"`) {
			// Settlement is acknowledged before the terminal event is relayed.
			_, settledBefore = sessionBudgets(t, f)
			break
		}
	}
	if settledBefore != 15 {
		t.Fatalf("response.done relayed before exact settlement: consumed %d", settledBefore)
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(strings.Replace(create, "c1", "c2", 1)))
	_, denied, _ := conn.ReadMessage()
	var event struct {
		Type  string `json:"type"`
		Error struct {
			Type, Code, EventID string
		} `json:"error"`
	}
	if json.Unmarshal(denied, &event) != nil || event.Type != "error" || !strings.HasPrefix(event.Error.Code, "QUOTA_") {
		t.Fatalf("budget denial not a native error event: %s", denied)
	}
	// The session stays open: the next event is relayed.
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"conversation.item.create"}`))
	_, echo, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(echo), "conversation.item.create") {
		t.Fatal("session closed after a refused response", err, string(echo))
	}
	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}
	conn.Close()
	<-done
	held, consumed := sessionBudgets(t, f)
	if held != 0 || consumed != 15 || f.calls.Load() != 1 {
		t.Fatal("per-response settlement not exact", held, consumed, f.calls.Load())
	}
	records := quotaHostTestAttempts(t, f.store)
	settled := 0
	for _, record := range records {
		info, _ := f.host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", record.ID)
		if info.Decision.State == "settled" {
			settled++
			if info.Actual["uncached_text_input_tokens"] != 4 || info.Actual["cached_text_input_tokens"] != 2 || info.Actual["uncached_audio_input_tokens"] != 4 || info.Actual["audio_output_tokens"] != 4 || info.Actual["text_output_tokens"] != 1 {
				t.Fatal("modality meters wrong", info.Actual)
			}
		}
	}
	if settled != 1 {
		t.Fatal("settled attempts", settled)
	}
}

func TestManagedRealtimeDisconnectRetainsResponseHold(t *testing.T) {
	meters := map[string]int64{}
	for _, native := range append(append([]string(nil), quotaRealtimeInputCounters...), quotaRealtimeOutputCounters...) {
		meters[native] = 1
	}
	f, path := sessionQuotaFixture(t, quotaProfileRealtime, "openai.v1.realtime", modelapi.ProviderOpenAI, meters, 10000, false, 0, func(conn *websocket.Conn) {
		_, _, _ = conn.ReadMessage()
		_, _, _ = conn.ReadMessage()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`))
		_ = conn.UnderlyingConn().Close()
	})
	conn, done := f.dialSession(t, path)
	defer conn.Close()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{"turn_detection":null}}`))
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","response":{"max_output_tokens":3}}`))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			break
		}
		t.Logf("relayed %s", payload)
	}
	conn.Close()
	<-done
	held, consumed := sessionBudgets(t, f)
	if held != 606 || consumed != 0 {
		t.Fatal("interrupted response released or charged", held, consumed)
	}
}

func TestManagedLiveAllowanceRenewsSettlesAndClosesOnDenial(t *testing.T) {
	setup := `{"setup":{"model":"projects/project/locations/us-central1/publishers/google/models/model-v1"}}`
	snapshot := func(total int) []byte {
		return []byte(`{"usageMetadata":{"totalTokenCount":` + strconv.Itoa(total) + `}}`)
	}
	for _, test := range []struct {
		name      string
		limit     int64
		wantClose int
		consumed  int64
		held      int64
	}{
		// Two windows of 1024: the first settles at renewal (600), the second
		// at clean close (900 - 600 = 300).
		{"renewed", 4096, websocket.CloseNormalClosure, 900, 0},
		// The first window settles 600 at renewal; the second 1024 hold does
		// not fit the remaining budget: goAway and a policy close.
		{"denied", 1500, websocket.ClosePolicyViolation, 600, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, path := sessionQuotaFixture(t, quotaProfileLiveAllowance, "live.connect", "google", map[string]int64{"total_tokens": 1}, test.limit, true, 1024, func(conn *websocket.Conn) {
				_, _, _ = conn.ReadMessage() // setup
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"setupComplete":{}}`))
				_ = conn.WriteMessage(websocket.TextMessage, snapshot(600))
				_ = conn.WriteMessage(websocket.TextMessage, snapshot(900))
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				_, _, _ = conn.ReadMessage()
			})
			conn, done := f.dialSession(t, path)
			defer conn.Close()
			_ = conn.WriteMessage(websocket.TextMessage, []byte(setup))
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			sawGoAway := false
			var err error
			for {
				var payload []byte
				if _, payload, err = conn.ReadMessage(); err != nil {
					break
				}
				t.Logf("relayed %s", payload)
				sawGoAway = sawGoAway || strings.Contains(string(payload), "goAway")
			}
			conn.Close()
			<-done
			var closeErr *websocket.CloseError
			if !asCloseError(err, &closeErr) || closeErr.Code != test.wantClose || sawGoAway != (test.wantClose == websocket.ClosePolicyViolation) {
				t.Fatalf("session end %v goAway=%t", err, sawGoAway)
			}
			held, consumed := sessionBudgets(t, f)
			if held != test.held || consumed != test.consumed {
				t.Fatal("allowance windows wrong", held, consumed)
			}
		})
	}
}

func asCloseError(err error, target **websocket.CloseError) bool {
	value, ok := err.(*websocket.CloseError)
	if ok {
		*target = value
	}
	return ok
}

var _ = models.WIFIdentity{}
