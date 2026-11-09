package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

const quotaHostModelBody = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":3}}`
const quotaHostModelTerminal = `{"responseId":"native-response","modelVersion":"model-v1","candidates":[{"index":0,"content":{"parts":[{"text":"native answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"cachedContentTokenCount":1,"candidatesTokenCount":2,"thoughtsTokenCount":1,"totalTokenCount":6}}`

type quotaHostModelFixture struct {
	handler   *ConnectedHandler
	host      *QuotaHost
	store     *quotaplex.MemoryStore
	resources resources.Store
	identity  *models.WIFIdentity
	now       time.Time
	calls     atomic.Int32
}

func quotaHostModelConfig(now time.Time, gemini bool) QuotaHostConfig {
	cfg := quotaHostTestConfig(now)
	cfg.Bindings[0].BillingAccount = "billing-project"
	cfg.Bindings[0].Region = "us-central1"
	stream := cfg.Bindings[0]
	stream.ID, stream.OperationID = "model-stream", "models.streamGenerateContent"
	stream.Meters = map[string]string{"requests": "requests"}
	cfg.Bindings = append(cfg.Bindings, stream)
	if !gemini {
		return cfg
	}
	policy := &cfg.Tenants[0].Policy
	policy.Meters["usd"] = quotaplex.Meter{ID: "usd", Unit: "money", Currency: "USD", Scale: 6}
	prices := []quotaplex.UnitPrice{}
	for native, price := range map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3, "thought_tokens": 3} {
		policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens", Billable: true}
		prices = append(prices, quotaplex.UnitPrice{MeterID: native, MoneyMeterID: "usd", Numerator: price, Denominator: 1})
		for i := range cfg.Bindings {
			cfg.Bindings[i].Profile = "gemini-text"
			cfg.Bindings[i].Meters[native] = native
		}
	}
	policy.PriceBooks = []quotaplex.PriceBook{{ID: "attested-native-model", Revision: 1, Match: quotaplex.Match{Provider: "google", BillingAccount: "billing-project", Region: "us-central1", ModelVersion: "model-v1"}, Prices: prices}}
	fixed := quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	policy.Budgets = append(policy.Budgets, quotaplex.BudgetRule{ID: "tenant-money", Scope: quotaplex.Scope{Kind: "tenant"}, MeterID: "usd", Limit: 1000, Period: fixed}, quotaplex.BudgetRule{ID: "tenant-input", Scope: quotaplex.Scope{Kind: "tenant"}, MeterID: "uncached_input_tokens", Limit: 200, Period: fixed})
	return cfg
}

func quotaHostModelNew(t *testing.T, gemini bool, upstreamHandler http.HandlerFunc, mutate func(*QuotaHostConfig)) *quotaHostModelFixture {
	t.Helper()
	f := &quotaHostModelFixture{now: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), store: quotaplex.NewMemoryStore(), resources: resources.NewMemoryStore()}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		upstreamHandler(w, r)
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("QUOTA_HOST_MODEL_TOKEN", "operator-owned-upstream-token")
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{upstream.URL}, AllowLoopback: true}, Identities: []connections.Identity{{ID: "host", TenantID: "tenant", Type: "bearer-env", TokenEnv: "QUOTA_HOST_MODEL_TOKEN", AllowedOrigins: []string{upstream.URL}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := modelapi.SelectOperations("models.generateContent", "models.streamGenerateContent", "models.countTokens")
	if err != nil {
		t.Fatal(err)
	}
	surface := modelapi.Surface{ID: "surface", Protocol: "google-genai", Origin: upstream.URL, APIVersion: "v1", Project: "project", Location: "us-central1", Publisher: "google", Models: []string{"model-v1"}, Operations: ops}
	spec, err := json.Marshal(ModelConnection{Surface: surface, IdentityRef: "host", BillingProject: "billing-project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.resources.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "model-surface", ID: "surface", Spec: spec}, 0); err != nil {
		t.Fatal(err)
	}
	cfg := quotaHostModelConfig(f.now, gemini)
	if mutate != nil {
		mutate(&cfg)
	}
	var bounds TrustedBoundsAuthority
	if gemini {
		bounds = &quotaHostTestBounds{input: 100, expiry: f.now.Add(time.Hour)}
	}
	f.host, err = NewQuotaHost(context.Background(), cfg, f.store, func() time.Time { return f.now }, nil, bounds)
	if err != nil {
		t.Fatal(err)
	}
	f.identity = quotaHostTestIdentity(t, f.now, nil)
	f.handler = NewConnectedHandler(f.resources, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	f.handler.Quota = f.host
	f.handler.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) { return f.access(), nil }
	return f
}

func (f *quotaHostModelFixture) access() *models.ResolvedAccess {
	return &models.ResolvedAccess{Identity: *f.identity, Scopes: []string{"llm:surface:surface:*", "llm:model:model-v1"}}
}

func (f *quotaHostModelFixture) request(operation, key, body string, facade bool) *http.Request {
	path := "/api/v1/llm/native/surface/v1/projects/project/locations/us-central1/publishers/google/models/model-v1:" + operation
	if facade {
		path = "/api/v1/llm/gateway/surface/v1beta/models/model-v1:" + operation
	}
	if operation == "streamGenerateContent" {
		path += "?alt=sse"
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.SetPathValue("id", "surface")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer caller-token-never-forwarded")
	r.Header.Set("X-Goog-User-Project", "caller-billing-project-never-trusted")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	ctx := context.WithValue(r.Context(), keyWIFIdentity, f.identity)
	ctx = context.WithValue(ctx, keyWIFAccess, f.access())
	ctx = context.WithValue(ctx, keyRequestID, "transport-correlation")
	return r.WithContext(ctx)
}

func (f *quotaHostModelFixture) serve(w http.ResponseWriter, r *http.Request, facade bool) {
	if facade {
		f.handler.ClientGateway(w, r)
	} else {
		f.handler.NativeModel(w, r)
	}
}

func quotaHostModelAttempt(t *testing.T, f *quotaHostModelFixture, id string) quotaplex.AttemptInfo {
	t.Helper()
	if id == "" {
		t.Fatal("managed model response omitted canonical quota recovery receipt")
	}
	info, err := f.host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func quotaHostModelAck(t *testing.T, f *quotaHostModelFixture, id string) quotaplex.UsageAcknowledgement {
	t.Helper()
	eventID, err := quotaDigest([]string{"tenant", id, "native-model", "terminal"})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := f.host.Engines()["tenant"].GetUsageAcknowledgement(context.Background(), "tenant", eventID)
	if err != nil {
		t.Fatal(err)
	}
	return ack
}

type quotaHostModelInspectWriter struct {
	*httptest.ResponseRecorder
	beforeWrite func([]byte)
}

func (w *quotaHostModelInspectWriter) Write(body []byte) (int, error) {
	if w.beforeWrite != nil {
		w.beforeWrite(body)
	}
	return w.ResponseRecorder.Write(body)
}

func TestQuotaHostModelCountSettlesBeforeNativeBodyAndDeniesFurtherCalls(t *testing.T) {
	for _, facade := range []bool{false, true} {
		t.Run(fmt.Sprintf("facade=%t", facade), func(t *testing.T) {
			seen := make(chan string, 1)
			const native = "{\"native\": true, \"extension\": [1,2]}\n"
			f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				seen <- r.URL.Path + "|" + r.Header.Get("Authorization") + "|" + r.Header.Get("X-Goog-User-Project") + "|" + string(body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, native)
			}, func(cfg *QuotaHostConfig) {
				for i := range cfg.Tenants[0].Policy.Budgets {
					cfg.Tenants[0].Policy.Budgets[i].Limit = 1
				}
			})
			writer := &quotaHostModelInspectWriter{ResponseRecorder: httptest.NewRecorder()}
			bodyObserved := false
			writer.beforeWrite = func([]byte) {
				if writer.Code != 200 || bodyObserved {
					return
				}
				bodyObserved = true
				info := quotaHostModelAttempt(t, f, writer.Header().Get(execution.AttemptHeader))
				if info.Decision.State != "settled" || info.Actual["requests"] != 1 || quotaHostModelAck(t, f, info.Demand.AttemptID).Status != quotaplex.UsageSettled {
					t.Fatal("native body escaped before durable quota settlement", info)
				}
			}
			f.serve(writer, f.request("generateContent", "one-logical-call", quotaHostModelBody, facade), facade)
			if writer.Code != 200 || writer.Body.String() != native || !bodyObserved || f.calls.Load() != 1 {
				t.Fatalf("native count call failed: status=%d body=%q calls=%d", writer.Code, writer.Body.String(), f.calls.Load())
			}
			want := "/v1/projects/project/locations/us-central1/publishers/google/models/model-v1:generateContent|Bearer operator-owned-upstream-token|billing-project|" + quotaHostModelBody
			if actual := <-seen; actual != want {
				t.Fatalf("wrong native dispatch/billing/identity: %s", actual)
			}
			replay := httptest.NewRecorder()
			f.serve(replay, f.request("generateContent", "one-logical-call", quotaHostModelBody, facade), facade)
			if replay.Code != 409 || f.calls.Load() != 1 {
				t.Fatal("settled replay dispatched another native request", replay.Code, replay.Body.String(), f.calls.Load())
			}
			denied := httptest.NewRecorder()
			f.serve(denied, f.request("generateContent", "next-call", quotaHostModelBody, facade), facade)
			retry, err := strconv.Atoi(denied.Header().Get("Retry-After"))
			if denied.Code != 429 || err != nil || retry <= 0 || f.calls.Load() != 1 {
				t.Fatal("exhausted budget did not reject before dispatch", denied.Code, denied.Body.String(), denied.Header(), f.calls.Load())
			}
		})
	}
}

func TestQuotaHostModelUnboundOperationRejectsBeforeCredentialLookup(t *testing.T) {
	f := quotaHostModelNew(t, false, func(http.ResponseWriter, *http.Request) { t.Error("unbound model operation reached provider") }, nil)
	t.Setenv("QUOTA_HOST_MODEL_TOKEN", "")
	rec := httptest.NewRecorder()
	f.handler.NativeModel(rec, f.request("countTokens", "unsupported", `{"contents":[]}`, false))
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "QUOTA_COVERAGE_UNAVAILABLE") || f.calls.Load() != 0 || len(quotaHostTestAttempts(t, f.store)) != 0 {
		t.Fatal("unsupported operation reached credentials or reserved capacity", rec.Code, rec.Body.String(), f.calls.Load())
	}
}

func TestQuotaHostModelStreamingShapeRejectsBeforeCredentialLookup(t *testing.T) {
	f := quotaHostModelNew(t, true, func(http.ResponseWriter, *http.Request) { t.Error("unsupported stream shape reached provider") }, nil)
	t.Setenv("QUOTA_HOST_MODEL_TOKEN", "")
	for _, query := range []string{"", "alt=json", "alt=sse&alt=sse"} {
		t.Run(query, func(t *testing.T) {
			r := f.request("streamGenerateContent", "unsupported-stream", quotaHostModelBody, false)
			r.URL.RawQuery = query
			rec := httptest.NewRecorder()
			f.handler.NativeModel(rec, r)
			if rec.Code != 400 || !strings.Contains(rec.Body.String(), "QUOTA_STREAM_SHAPE_UNSUPPORTED") || f.calls.Load() != 0 || len(quotaHostTestAttempts(t, f.store)) != 0 {
				t.Fatal("unmeterable native stream acquired capacity or credentials", rec.Code, rec.Body.String(), f.calls.Load())
			}
		})
	}
}

func TestQuotaHostModelRateAndConcurrencyDenyBeforeProviderDispatch(t *testing.T) {
	t.Run("rate after settled call", func(t *testing.T) {
		f := quotaHostModelNew(t, false, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"native":true}`)
		}, func(cfg *QuotaHostConfig) {
			cfg.Tenants[0].Policy.Rates[0].Rate = 1
			cfg.Tenants[0].Policy.Rates[0].Burst = 1
		})
		first := httptest.NewRecorder()
		f.handler.NativeModel(first, f.request("generateContent", "rate-first", quotaHostModelBody, false))
		if first.Code != 200 || quotaHostModelAttempt(t, f, first.Header().Get(execution.AttemptHeader)).Decision.State != "settled" {
			t.Fatal("first rate permit did not complete", first.Code, first.Body.String())
		}
		next := httptest.NewRecorder()
		f.handler.NativeModel(next, f.request("generateContent", "rate-next", quotaHostModelBody, false))
		if next.Code != 429 || next.Header().Get("Retry-After") != "1" || f.calls.Load() != 1 || !strings.Contains(next.Body.String(), "RATE") {
			t.Fatal("rate limit did not gate actual dispatch", next.Code, next.Body.String(), next.Header(), f.calls.Load())
		}
	})
	t.Run("active provider execution", func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			select {
			case <-release:
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"native":true}`)
			case <-r.Context().Done():
			}
		}, nil)
		first := httptest.NewRecorder()
		r := f.request("generateContent", "concurrent-first", quotaHostModelBody, false)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() { defer close(done); f.handler.NativeModel(first, r.WithContext(ctx)) }()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("first native dispatch never started", first.Code, first.Body.String())
		}
		next := httptest.NewRecorder()
		f.handler.NativeModel(next, f.request("generateContent", "concurrent-next", quotaHostModelBody, false))
		if next.Code != 429 || !strings.Contains(next.Body.String(), "CONCURRENCY") || f.calls.Load() != 1 {
			cancel()
			<-done
			t.Fatal("active provider execution lost concurrency permit", next.Code, next.Body.String(), f.calls.Load())
		}
		close(release)
		<-done
		if first.Code != 200 || quotaHostModelAttempt(t, f, first.Header().Get(execution.AttemptHeader)).Decision.State != "settled" {
			t.Fatal("completed native dispatch failed", first.Code, first.Body.String())
		}
	})
}

type quotaHostModelChangingStore struct {
	resources.Store
	gets atomic.Int32
}

func (s *quotaHostModelChangingStore) Get(ctx context.Context, tenant, kind, id string) (resources.Record, error) {
	record, err := s.Store.Get(ctx, tenant, kind, id)
	if err == nil && kind == "model-surface" && s.gets.Add(1) == 2 {
		return s.Store.Put(ctx, record, record.Revision)
	}
	return record, err
}

func TestQuotaHostModelRefreshesConfigurationAndAccessBeforeDispatch(t *testing.T) {
	for _, changed := range []string{"surface revision", "model access revoked"} {
		t.Run(changed, func(t *testing.T) {
			f := quotaHostModelNew(t, false, func(http.ResponseWriter, *http.Request) { t.Error("revoked configuration reached provider") }, nil)
			if changed == "surface revision" {
				f.handler.Store = &quotaHostModelChangingStore{Store: f.resources}
			} else {
				f.handler.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) {
					return &models.ResolvedAccess{Identity: *f.identity, Scopes: []string{"llm:surface:surface:*"}}, nil
				}
			}
			rec := httptest.NewRecorder()
			f.handler.NativeModel(rec, f.request("generateContent", "stale-permission", quotaHostModelBody, false))
			if rec.Code < 400 || f.calls.Load() != 0 {
				t.Fatal("stale authority dispatched", rec.Code, rec.Body.String(), f.calls.Load())
			}
			info := quotaHostModelAttempt(t, f, rec.Header().Get(execution.AttemptHeader))
			if info.Decision.State != "aborted" || info.Claim.Fence != 0 {
				t.Fatal("known-undispatched work kept exposure or claimed provider execution", info)
			}
			for _, budget := range info.Plan.Budgets {
				balance, err := f.host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
				if err != nil || balance.Held != 0 || balance.Consumed != 0 {
					t.Fatal("pre-dispatch revocation consumed quota", balance, err)
				}
			}
		})
	}
}

func quotaHostModelAssertSettled(t *testing.T, f *quotaHostModelFixture, attempt string) {
	t.Helper()
	info := quotaHostModelAttempt(t, f, attempt)
	want := quotaplex.Vector{"requests": 1, "uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 2, "thought_tokens": 1, "usd": 14}
	if info.Decision.State != "settled" || info.ProviderResource != "native-response" || info.Demand.Target.BillingAccount != "billing-project" || info.Demand.Target.Region != "us-central1" || !info.Demand.RequireModelVersion {
		t.Fatal("native terminal lost pinned provider authority", info)
	}
	for meter, value := range want {
		if info.Actual[meter] != value {
			t.Fatalf("native %s usage = %d, want %d; all=%v", meter, info.Actual[meter], value, info.Actual)
		}
	}
	for _, budget := range info.Plan.Budgets {
		balance, err := f.host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
		if err != nil || balance.Held != 0 || balance.Consumed != want[budget.MeterID] {
			t.Fatal("raw/money budget did not settle from disjoint native meters", balance, err)
		}
	}
	if ack := quotaHostModelAck(t, f, attempt); ack.Status != quotaplex.UsageSettled {
		t.Fatal("model response lacks durable settled outbox", ack)
	}
}

func TestQuotaHostModelGeminiJSONSettlesDisjointTokenAndMoneyUsage(t *testing.T) {
	for _, facade := range []bool{false, true} {
		t.Run(fmt.Sprintf("facade=%t", facade), func(t *testing.T) {
			f := quotaHostModelNew(t, true, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, quotaHostModelTerminal)
			}, nil)
			rec := &quotaHostModelInspectWriter{ResponseRecorder: httptest.NewRecorder()}
			rec.beforeWrite = func([]byte) {
				if rec.Code == 200 {
					quotaHostModelAssertSettled(t, f, rec.Header().Get(execution.AttemptHeader))
				}
			}
			f.serve(rec, f.request("generateContent", "token-money", quotaHostModelBody, facade), facade)
			if rec.Code != 200 || rec.Body.String() != quotaHostModelTerminal || f.calls.Load() != 1 {
				t.Fatal("protected native JSON changed or failed", rec.Code, rec.Body.String(), f.calls.Load())
			}
			quotaHostModelAssertSettled(t, f, rec.Header().Get(execution.AttemptHeader))
		})
	}
}

func TestQuotaHostModelVersionDriftIsDurableAndQuarantinesNextCall(t *testing.T) {
	for _, version := range []string{"", "unexpected-provider-version"} {
		t.Run("observed="+version, func(t *testing.T) {
			payload := strings.Replace(quotaHostModelTerminal, `"modelVersion":"model-v1",`, "", 1)
			if version != "" {
				payload = strings.Replace(quotaHostModelTerminal, `"modelVersion":"model-v1"`, `"modelVersion":"`+version+`"`, 1)
			}
			f := quotaHostModelNew(t, true, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, payload)
			}, nil)
			rec := httptest.NewRecorder()
			f.handler.NativeModel(rec, f.request("generateContent", "model-drift", quotaHostModelBody, false))
			if rec.Code != 502 || strings.Contains(rec.Body.String(), "native answer") || f.calls.Load() != 1 {
				t.Fatal("unpriced model drift escaped as success", rec.Code, rec.Body.String(), f.calls.Load())
			}
			attempt := rec.Header().Get(execution.AttemptHeader)
			info := quotaHostModelAttempt(t, f, attempt)
			ack := quotaHostModelAck(t, f, attempt)
			if info.Decision.State == "settled" || ack.Status != quotaplex.UsagePending {
				t.Fatal("model drift released its exposure", info, ack)
			}
			var evidence struct{ Evidence quotaplex.TerminalEvidence }
			if err := f.store.View(context.Background(), "tenant", func(tx quotaplex.Tx) error { return tx.Get("terminal-evidence", ack.EventID, &evidence) }); err != nil || evidence.Evidence.ObservedModelVersion != version || !evidence.Evidence.RequireModelVersion {
				t.Fatal("durable evidence lost actual model identity", evidence, err)
			}
			for _, budget := range info.Plan.Budgets {
				balance, err := f.host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
				if err != nil || balance.Held <= 0 || balance.Consumed != 0 {
					t.Fatal("version mismatch refunded unsettled usage", balance, err)
				}
			}
			next := httptest.NewRecorder()
			f.handler.NativeModel(next, f.request("generateContent", "after-model-drift", quotaHostModelBody, false))
			if next.Code != 503 || !strings.Contains(next.Body.String(), "QUOTA_CONFIGURATION_INACTIVE") || f.calls.Load() != 1 {
				t.Fatal("quarantined model was dispatched again", next.Code, next.Body.String(), f.calls.Load())
			}
		})
	}
}

func TestQuotaHostModelProviderBoundViolationSettlesActualAndQuarantines(t *testing.T) {
	for _, bound := range []string{"combined output", "input context"} {
		t.Run(bound, func(t *testing.T) {
			payload, body := quotaHostModelTerminal, quotaHostModelBody
			if bound == "combined output" {
				body = strings.Replace(body, `"maxOutputTokens":3`, `"maxOutputTokens":2`, 1)
			} else {
				payload = strings.Replace(payload, `"promptTokenCount":3`, `"promptTokenCount":102`, 1)
				payload = strings.Replace(payload, `"totalTokenCount":6`, `"totalTokenCount":105`, 1)
			}
			f := quotaHostModelNew(t, true, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, payload)
			}, func(cfg *QuotaHostConfig) {
				for i := range cfg.Tenants[0].Policy.Budgets {
					if cfg.Tenants[0].Policy.Budgets[i].MeterID == "uncached_input_tokens" {
						cfg.Tenants[0].Policy.Budgets[i].Limit = 100
					}
				}
			})
			rec := httptest.NewRecorder()
			f.handler.NativeModel(rec, f.request("generateContent", "provider-bound-violation", body, false))
			if rec.Code != 200 || rec.Body.String() != payload || f.calls.Load() != 1 {
				t.Fatal("known provider overrun was not charged and delivered", rec.Code, rec.Body.String(), f.calls.Load())
			}
			attempt := rec.Header().Get(execution.AttemptHeader)
			info := quotaHostModelAttempt(t, f, attempt)
			if bound == "combined output" {
				quotaHostModelAssertSettled(t, f, attempt)
			} else {
				if info.Decision.State != "settled" || !info.Decision.Overrun || info.Actual["uncached_input_tokens"] != 101 || info.Actual["usd"] != 212 || quotaHostModelAck(t, f, attempt).Status != quotaplex.UsageSettled {
					t.Fatal("known usage above provider bound was capped or lost", info)
				}
				found := false
				for _, budget := range info.Plan.Budgets {
					if budget.MeterID == "uncached_input_tokens" {
						found = true
						balance, err := f.host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
						if err != nil || balance.Held != 0 || balance.Consumed != 101 || balance.Available != -1 {
							t.Fatal("provider overrun disappeared instead of becoming debt", balance, err)
						}
					}
				}
				if !found {
					t.Fatal("fixture lacks protected input budget")
				}
			}
			next := httptest.NewRecorder()
			f.handler.NativeModel(next, f.request("generateContent", "after-provider-bound-violation", quotaHostModelBody, false))
			if next.Code != 503 || !strings.Contains(next.Body.String(), "QUOTA_CONFIGURATION_INACTIVE") || f.calls.Load() != 1 {
				t.Fatal("violated provider limit remained active", next.Code, next.Body.String(), f.calls.Load())
			}
		})
	}
}

const quotaHostModelSSEFirst = ": native-comment\r\ndata: {\"responseId\":\"native-response\",\"modelVersion\":\"model-v1\",\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"hel\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":1,\"totalTokenCount\":4}}\r\n\r\n"
const quotaHostModelSSEFinal = "data: {\"responseId\":\"native-response\",\"modelVersion\":\"model-v1\",\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"lo\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"cachedContentTokenCount\":1,\"candidatesTokenCount\":2,\"thoughtsTokenCount\":1,\"totalTokenCount\":6}}\n\n"

func TestQuotaHostModelGeminiSSEForwardsExactNativeBytesAfterDurableFinality(t *testing.T) {
	release := make(chan struct{})
	f := quotaHostModelNew(t, true, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, quotaHostModelSSEFirst)
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, quotaHostModelSSEFinal)
		case <-r.Context().Done():
		}
	}, nil)
	rec := &quotaHostModelInspectWriter{ResponseRecorder: httptest.NewRecorder()}
	first := make(chan string, 1)
	var once sync.Once
	rec.beforeWrite = func(body []byte) {
		if strings.Contains(string(body), `"hel"`) {
			once.Do(func() { first <- rec.Header().Get(execution.AttemptHeader) })
		}
		if strings.Contains(string(body), `"finishReason":"STOP"`) {
			quotaHostModelAssertSettled(t, f, rec.Header().Get(execution.AttemptHeader))
		}
	}
	done := make(chan struct{})
	r := f.request("streamGenerateContent", "stream-token-money", quotaHostModelBody, true)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	go func() { defer close(done); f.serve(rec, r.WithContext(ctx), true) }()
	var attempt string
	select {
	case attempt = <-first:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("nonterminal native frame was buffered until finality", rec.Code, rec.Body.String())
	}
	info := quotaHostModelAttempt(t, f, attempt)
	if info.Decision.State == "settled" {
		t.Fatal("nonterminal SSE usage released exposure", info)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("completed native SSE did not terminate")
	}
	if rec.Code != 200 || rec.Body.String() != quotaHostModelSSEFirst+quotaHostModelSSEFinal || f.calls.Load() != 1 {
		t.Fatal("managed SSE changed native framing", rec.Code, rec.Body.String(), f.calls.Load())
	}
	quotaHostModelAssertSettled(t, f, attempt)
}

func TestQuotaHostModelIncompleteSSEAndCancellationRetainExposure(t *testing.T) {
	for _, ending := range []string{"clean EOF without finish", "truncated final frame", "client cancelled"} {
		t.Run(ending, func(t *testing.T) {
			f := quotaHostModelNew(t, true, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, quotaHostModelSSEFirst)
				w.(http.Flusher).Flush()
				if ending == "truncated final frame" {
					_, _ = io.WriteString(w, `data: {"responseId":"native-response"`)
				}
				if ending == "client cancelled" {
					<-r.Context().Done()
				}
			}, nil)
			r := f.request("streamGenerateContent", "incomplete-stream", quotaHostModelBody, false)
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			rec := &quotaHostModelInspectWriter{ResponseRecorder: httptest.NewRecorder()}
			rec.beforeWrite = func(body []byte) {
				if ending == "client cancelled" && strings.Contains(string(body), `"hel"`) {
					cancel()
				}
			}
			f.handler.NativeModel(rec, r.WithContext(ctx))
			info := quotaHostModelAttempt(t, f, rec.Header().Get(execution.AttemptHeader))
			if info.Decision.State != "unknown" || info.Claim.Fence == 0 || f.calls.Load() != 1 {
				t.Fatal("possibly executed native stream lost uncertainty", info, rec.Code, rec.Body.String())
			}
			for _, budget := range info.Plan.Budgets {
				balance, err := f.host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
				if err != nil || balance.Held <= 0 || balance.Consumed != 0 {
					t.Fatal("incomplete stream refunded possible exposure", balance, err)
				}
			}
		})
	}
}
