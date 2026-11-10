package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// countingSecrets counts credential resolutions so tests can prove a rejected
// request never acquired the upstream credential.
type countingSecrets struct{ resolves *atomic.Int64 }

func (s countingSecrets) Resolve(_ context.Context, tenant, ref string) (string, error) {
	if tenant == "tenant" && ref == "provider-key" {
		s.resolves.Add(1)
		return providerTestSecret, nil
	}
	return "", fmt.Errorf("secret unavailable")
}
func (s countingSecrets) Exists(_ context.Context, tenant, ref string) (bool, error) {
	return tenant == "tenant" && ref == "provider-key", nil
}

const capacityChatBody = `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3}`

var capacityClock atomic.Int64 // seconds the gate clock runs ahead of real time

func skewedClock() time.Time {
	return time.Now().Add(time.Duration(capacityClock.Load()) * time.Second)
}

// capacityQuotaFixture is a quota-protected OpenAI chat surface whose upstream
// answers with the given handler.
func capacityQuotaFixture(t *testing.T, policy *providerlimits.Policy, upstream http.HandlerFunc) (*quotaHostModelFixture, *atomic.Int64, func() []usageledger.Record) {
	t.Helper()
	capacityClock.Store(0)
	// Request-count metering: one admitted exchange, settled even when the
	// provider answers with an error.
	f := quotaHostModelNew(t, false, upstream, func(cfg *QuotaHostConfig) {
		cfg.Bindings = cfg.Bindings[:1]
		binding := &cfg.Bindings[0]
		binding.OperationID, binding.Provider, binding.BillingAccount, binding.Region = "openai.v1.chat-completions", modelapi.ProviderOpenAI, "", ""
		// A control for the "ap" access point, so QoS callers are covered.
		policy := &cfg.Tenants[0].Policy
		policy.Concurrency = append(policy.Concurrency, quotaplex.ConcurrencyRule{ID: "ap", Scope: quotaplex.Scope{Kind: "principal"}, Match: quotaplex.Match{AccessPointID: "ap"}, Limit: 5})
	})
	resolves := &atomic.Int64{}
	provider, err := toolcredential.NewProvider(toolcredential.Config{HTTPClient: f.handler.Runtime.HTTP, Policy: f.handler.Runtime.Policy, Secrets: countingSecrets{resolves}})
	if err != nil {
		t.Fatal(err)
	}
	f.handler.ToolCredentials = provider
	record, _ := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	var google ModelConnection
	_ = json.Unmarshal(record.Spec, &google)
	connection := providerConnection(t, google.Surface.Origin, modelapi.Provider{ID: modelapi.ProviderOpenAI}, "openai-chat", bearerRef, "openai.v1.chat-completions")
	connection.CapacityPolicy = policy
	if err = f.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal("surface rejected:", err)
	}
	putProviderSurface(t, f.resources, connection)
	f.handler.CapacityGate = providerlimits.NewGateWithClock(f.handler.capacityStore(), skewedClock)
	return f, resolves, usageLedgerFor(t, f.handler)
}

func (f *quotaHostModelFixture) chat(key string, extra ...func(*http.Request)) *httptest.ResponseRecorder {
	r := f.request("generateContent", key, capacityChatBody, false)
	r.URL.Path, r.URL.RawQuery = "/api/v1/llm/native/surface/v1/chat/completions", ""
	for _, fn := range extra {
		fn(r)
	}
	w := httptest.NewRecorder()
	f.handler.NativeModel(w, r)
	return w
}

func quotaAttempts(t *testing.T, f *quotaHostModelFixture) int {
	t.Helper()
	page, err := f.host.Engines()["tenant"].Inventory(context.Background(), "tenant", "attempts", "", 64)
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Records)
}

func TestCooldownRejectsBeforeCredentialUpstreamAndQuota(t *testing.T) {
	var retryAfter atomic.Value
	retryAfter.Store("30")
	policy := &providerlimits.Policy{CooldownEnabled: true}
	f, resolves, ledger := capacityQuotaFixture(t, policy, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if value := retryAfter.Load().(string); value != "" {
			w.Header().Set("Retry-After", value)
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"slow down"}}`)
			return
		}
		w.Header().Set("X-Ratelimit-Remaining-Requests", "50")
		fmt.Fprint(w, `{"id":"chatcmpl-1","model":"model-v1","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})

	// First request reaches the provider, which throttles it.
	first := f.chat("k1")
	if first.Code != 429 || f.calls.Load() != 1 || resolves.Load() != 1 {
		t.Fatalf("setup: %d %s calls=%d credentials=%d", first.Code, first.Body.String(), f.calls.Load(), resolves.Load())
	}
	attempts := quotaAttempts(t, f)
	if attempts == 0 {
		t.Fatal("the dispatched request left no quota attempt")
	}

	// Within the cooldown: native 429, the provider's own retry hint, and no
	// upstream call, credential acquisition or quota attempt.
	second := f.chat("k2")
	if second.Code != 429 {
		t.Fatalf("cooldown not enforced: %d %s", second.Code, second.Body.String())
	}
	seconds, err := strconv.Atoi(second.Header().Get("Retry-After"))
	if err != nil || seconds < 28 || seconds > 30 {
		t.Fatalf("Retry-After %q is not derived from the provider's 30s", second.Header().Get("Retry-After"))
	}
	if f.calls.Load() != 1 || resolves.Load() != 1 || quotaAttempts(t, f) != attempts || second.Header().Get("X-Aiplex-Attempt-ID") != "" {
		t.Fatalf("rejection touched upstream/credential/quota: calls=%d credentials=%d attempts=%d->%d", f.calls.Load(), resolves.Load(), attempts, quotaAttempts(t, f))
	}
	if strings.Contains(second.Body.String(), "slow down") {
		t.Fatal("provider body leaked into a gate rejection")
	}

	// The ledger shows a denied exchange with a distinct class and no limits.
	records := ledger()
	if len(records) != 2 {
		t.Fatalf("ledger: %+v", records)
	}
	var provider, gated *usageledger.Record
	for i := range records {
		if records[i].ErrorClass == "capacity_cooldown" {
			gated = &records[i]
		} else {
			provider = &records[i]
		}
	}
	if provider == nil || provider.ProviderLimits == nil || provider.ProviderLimits.Classification != "rate_limited" {
		t.Fatalf("provider 429 record: %+v", provider)
	}
	if gated == nil || gated.Status != usageledger.StatusDenied || gated.HTTPStatus != 429 || gated.ProviderLimits != nil || gated.QuotaAttemptID != "" || gated.Operation != "openai.v1.chat-completions" {
		t.Fatalf("gate rejection record: %+v", gated)
	}

	// Another tenant's surface of the same id is untouched.
	if st := f.handler.capacityGate().State("other-tenant", "surface", *policy); st.Phase != providerlimits.PhaseNone {
		t.Fatalf("cooldown visible to another tenant: %+v", st)
	}

	// The capacity endpoint exposes the policy and the cooldown state.
	capacity := httptest.NewRecorder()
	cr := httptest.NewRequest("GET", "/api/v1/llm/surfaces/surface/capacity", nil)
	cr.SetPathValue("id", "surface")
	cr = cr.WithContext(context.WithValue(context.WithValue(cr.Context(), keyWIFIdentity, f.identity), keyWIFAccess, &models.ResolvedAccess{Identity: *f.identity, Roles: []models.IAMRole{models.RoleAdmin}}))
	f.handler.SurfaceCapacity(capacity, cr)
	var report struct {
		Observed       bool `json:"observed"`
		CapacityPolicy struct {
			Enabled bool `json:"enabled"`
			Policy  struct {
				CooldownEnabled bool `json:"cooldown_enabled"`
				MaxCooldown     int  `json:"max_cooldown_seconds"`
				SignalMaxAge    int  `json:"signal_max_age_seconds"`
			} `json:"policy"`
			Cooldown struct {
				Phase        string `json:"phase"`
				RetryAfterMS int64  `json:"retry_after_ms"`
				Until        string `json:"until"`
			} `json:"cooldown"`
		} `json:"capacity_policy"`
	}
	if capacity.Code != 200 || json.Unmarshal(capacity.Body.Bytes(), &report) != nil || !report.CapacityPolicy.Enabled || !report.CapacityPolicy.Policy.CooldownEnabled || report.CapacityPolicy.Policy.MaxCooldown != 60 || report.CapacityPolicy.Policy.SignalMaxAge != 60 || report.CapacityPolicy.Cooldown.Phase != "cooling" || report.CapacityPolicy.Cooldown.RetryAfterMS <= 0 || report.CapacityPolicy.Cooldown.Until == "" {
		t.Fatalf("capacity report: %d %s", capacity.Code, capacity.Body.String())
	}

	// When the cooldown ends exactly one request probes the provider. The
	// provider recovers, so the probe closes the cooldown.
	retryAfter.Store("")
	capacityClock.Store(31)
	probe := f.chat("k3")
	if probe.Code != 200 || f.calls.Load() != 2 || resolves.Load() != 2 {
		t.Fatalf("probe: %d calls=%d credentials=%d body=%s", probe.Code, f.calls.Load(), resolves.Load(), probe.Body.String())
	}
	if after := f.chat("k4"); after.Code != 200 || f.calls.Load() != 3 {
		t.Fatalf("cooldown did not close after a good probe: %d calls=%d", after.Code, f.calls.Load())
	}
}

func TestCooldownProbeThatIsThrottledAgainRestartsTheCooldown(t *testing.T) {
	f, _, _ := capacityQuotaFixture(t, &providerlimits.Policy{CooldownEnabled: true, MaxCooldownSeconds: 20}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"type":"rate_limit_error"}}`)
	})
	if w := f.chat("k1"); w.Code != 429 || f.calls.Load() != 1 {
		t.Fatalf("setup %d", w.Code)
	}
	if w := f.chat("k1b"); w.Code != 429 || f.calls.Load() != 1 {
		t.Fatalf("not gated during the cooldown: calls=%d", f.calls.Load())
	}
	time.Sleep(1100 * time.Millisecond)
	probe := f.chat("k2") // the probe, throttled again
	if probe.Code != 429 || f.calls.Load() != 2 {
		t.Fatalf("probe not dispatched: %d calls=%d", probe.Code, f.calls.Load())
	}
	// The new observation starts a new cooldown: rejected without a call.
	next := f.chat("k3")
	if next.Code != 429 || f.calls.Load() != 2 || next.Header().Get("Retry-After") == "" {
		t.Fatalf("second cooldown missing: %d calls=%d", next.Code, f.calls.Load())
	}
}

func TestHeadroomShedsLowWeightCallersAndKeepsQuotaAdditive(t *testing.T) {
	policy := &providerlimits.Policy{HeadroomFraction: 0.2, MinWeight: 10}
	f, resolves, ledger := capacityQuotaFixture(t, policy, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Ratelimit-Limit-Requests", "100")
		w.Header().Set("X-Ratelimit-Remaining-Requests", "5")
		w.Header().Set("X-Ratelimit-Reset-Requests", "40s")
		fmt.Fprint(w, `{"id":"chatcmpl-1","model":"model-v1","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})
	// No observation yet: an absent signal is never zero capacity.
	if w := f.chat("k1"); w.Code != 200 || f.calls.Load() != 1 {
		t.Fatalf("first request: %d", w.Code)
	}
	attempts := quotaAttempts(t, f)
	// Capacity is now below the reserve. An unclassified caller (default
	// weight 1) is shed with a reset-derived hint.
	shed := f.chat("k2")
	seconds, err := strconv.Atoi(shed.Header().Get("Retry-After"))
	if shed.Code != 429 || err != nil || seconds < 38 || seconds > 40 {
		t.Fatalf("not shed: %d retry-after=%q", shed.Code, shed.Header().Get("Retry-After"))
	}
	if f.calls.Load() != 1 || resolves.Load() != 1 || quotaAttempts(t, f) != attempts {
		t.Fatal("shed request reached upstream, credential or quota")
	}
	withWeight := func(weight uint32) func(*http.Request) {
		return func(r *http.Request) {
			*r = *r.WithContext(withAccessPointCall(r.Context(), &accessPointCall{ID: "ap", CallerKind: CallerUser, QoS: true, Weight: weight}))
		}
	}
	if w := f.chat("k3", withWeight(9)); w.Code != 429 || f.calls.Load() != 1 {
		t.Fatalf("weight below the threshold admitted: %d", w.Code)
	}
	// A weight at the threshold is admitted by the gate and still goes through
	// the normal quota admission and settlement.
	high := f.chat("k4", withWeight(10))
	if high.Code != 200 || f.calls.Load() != 2 || quotaAttempts(t, f) != attempts+1 || high.Header().Get("X-Aiplex-Attempt-ID") == "" {
		t.Fatalf("high weight: %d %s calls=%d attempts=%d", high.Code, high.Body.String(), f.calls.Load(), quotaAttempts(t, f))
	}
	// QoS without a weight is treated as the default weight.
	if w := f.chat("k5", withWeight(0)); w.Code != 429 {
		t.Fatalf("QoS without weight not treated as default weight: %d", w.Code)
	}
	// A stale signal is ignored.
	capacityClock.Store(61)
	if w := f.chat("k6"); w.Code != 200 {
		t.Fatalf("stale signal still shed: %d", w.Code)
	}
	var shedRecords int
	for _, r := range ledger() {
		if r.ErrorClass == "capacity_shed" {
			shedRecords++
			if r.Status != usageledger.StatusDenied || r.HTTPStatus != 429 || r.ProviderLimits != nil {
				t.Fatalf("shed record: %+v", r)
			}
		}
	}
	if shedRecords != 3 {
		t.Fatalf("want three shed ledger records, got %d", shedRecords)
	}
}

func TestPolicyIsOffByDefault(t *testing.T) {
	for name, policy := range map[string]*providerlimits.Policy{"absent": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := capacityQuotaFixture(t, policy, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(429)
				fmt.Fprint(w, `{"error":{"type":"rate_limit_error"}}`)
			})
			for i := 0; i < 3; i++ {
				if w := f.chat("k" + strconv.Itoa(i)); w.Code != 429 {
					t.Fatal(w.Code)
				}
			}
			if f.calls.Load() != 3 {
				t.Fatalf("a surface without a policy was gated: calls=%d", f.calls.Load())
			}
		})
	}
}

func TestCapacityPolicyValidationAndQuotaDigest(t *testing.T) {
	f, _, _ := capacityQuotaFixture(t, nil, func(w http.ResponseWriter, r *http.Request) {})
	base := providerConnection(t, f.handler.mustSurfaceOrigin(t), modelapi.Provider{ID: modelapi.ProviderOpenAI}, "openai-chat", bearerRef, "openai.v1.chat-completions")
	validate := func(raw string) error {
		spec := providerSpecJSON(t, base)
		var generic map[string]json.RawMessage
		_ = json.Unmarshal(spec, &generic)
		generic["capacity_policy"] = json.RawMessage(raw)
		out, _ := json.Marshal(generic)
		return f.handler.validate(context.Background(), "tenant", "model-surface", "surface", out)
	}
	for _, raw := range []string{
		`{"cooldown_enabled":true}`,
		`{"cooldown_enabled":true,"max_cooldown_seconds":120,"headroom_fraction":0.15,"min_weight":10,"default_weight":2,"signal_max_age_seconds":30}`,
		`{}`,
	} {
		if err := validate(raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"headroom_fraction":0.2}`,
		`{"headroom_fraction":2,"min_weight":1}`,
		`{"cooldown_enabled":true,"max_cooldown_seconds":-1}`,
		`{"cooldown_enabled":true,"unknown":1}`,
		`{"cooldown_enabled":"yes"}`,
		`{"cooldown_enabled":true,"min_weight":-3}`,
		`{"min_weight":5}`,
		`[]`,
	} {
		if err := validate(raw); err == nil {
			t.Fatalf("%s accepted", raw)
		}
	}
	// Google surfaces report no limit signals and cannot carry a policy.
	ops, _ := modelapi.SelectOperations("models.generateContent")
	google := ModelConnection{Surface: modelapi.Surface{ID: "surface", Protocol: "google-genai", Origin: base.Surface.Origin, APIVersion: "v1", Project: "p", Location: "us-central1", Publisher: "google", Models: []string{"m"}, Operations: ops}, IdentityRef: "host", CapacityPolicy: &providerlimits.Policy{CooldownEnabled: true}}
	if err := f.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, google)); err == nil {
		t.Fatal("capacity_policy accepted on a Google surface")
	}

	// A policy change is a configuration change for the quota host's pin.
	putProviderSurface(t, f.resources, base)
	rec, err := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	if err != nil {
		t.Fatal(err)
	}
	digest := func(policy *providerlimits.Policy) string {
		connection := base
		connection.CapacityPolicy = policy
		configuration, err := f.handler.quotaModelConfiguration(context.Background(), "tenant", rec, connection)
		if err != nil {
			t.Fatal(err)
		}
		d, err := quotaDigest(configuration)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	none, cooldown, headroom := digest(nil), digest(&providerlimits.Policy{CooldownEnabled: true}), digest(&providerlimits.Policy{CooldownEnabled: true, HeadroomFraction: 0.1, MinWeight: 1})
	if none == cooldown || cooldown == headroom || none == headroom {
		t.Fatal("capacity_policy is not part of the pinned model configuration digest")
	}
	if digest(nil) != none {
		t.Fatal("digest not stable")
	}
}

// mustSurfaceOrigin returns the origin of the fixture's saved surface.
func (h *ConnectedHandler) mustSurfaceOrigin(t *testing.T) string {
	t.Helper()
	record, err := h.Store.Get(context.Background(), "tenant", "model-surface", "surface")
	if err != nil {
		t.Fatal(err)
	}
	var c ModelConnection
	if err = json.Unmarshal(record.Spec, &c); err != nil {
		t.Fatal(err)
	}
	return c.Surface.Origin
}

// ── access-point protocols, weights and sessions ─────────────────────────

func addCapacitySurfaces(t *testing.T, f *apFixture, policy *providerlimits.Policy) {
	t.Helper()
	addProviderAccessPointSurfaces(t, f)
	chatOps, err := modelapi.SelectProviderOperations(modelapi.ProviderOpenAI, "openai.v1.chat-completions")
	if err != nil {
		t.Fatal(err)
	}
	claudeOps, err := modelapi.SelectProviderOperations(modelapi.ProviderAnthropic, "anthropic.messages")
	if err != nil {
		t.Fatal(err)
	}
	gpt := modelapi.Surface{ID: "gpt", Protocol: "openai-chat", Origin: f.upstream.URL, Models: []string{"gpt-p"}, Operations: chatOps, Provider: &modelapi.Provider{ID: modelapi.ProviderOpenAI}}
	claude := modelapi.Surface{ID: "claude-p", Protocol: "anthropic-messages", Origin: f.upstream.URL, Models: []string{"claude-p"}, Operations: claudeOps, Provider: &modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}}
	for id, connection := range map[string]ModelConnection{
		"gpt":      {Surface: gpt, IdentityRef: "host", CapacityPolicy: policy},
		"claude-p": {Surface: claude, Auth: anthropicRef, CapacityPolicy: policy},
	} {
		f.put("model-surface", id, connection)
	}
	// The bedrock and realtime surfaces saved by the shared helper get the
	// policy too.
	for _, id := range []string{"bedrock", "realtime"} {
		record, err := f.store.Get(context.Background(), "tenant", "model-surface", id)
		if err != nil {
			t.Fatal(err)
		}
		var c ModelConnection
		_ = json.Unmarshal(record.Spec, &c)
		c.CapacityPolicy = policy
		raw, _ := json.Marshal(c)
		if _, err = f.store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "model-surface", ID: id, Spec: raw}, record.Revision); err != nil {
			t.Fatal(err)
		}
	}
	spec := apSpec()
	spec.ID = "prov"
	spec.Models = append(spec.Models,
		AccessPointModel{Name: "claude-bedrock", SurfaceRef: "bedrock", NativeModel: "anthropic.claude-v1:0"},
		AccessPointModel{Name: "voice", SurfaceRef: "realtime", NativeModel: "fixture-rt"},
		AccessPointModel{Name: "gpt-p", SurfaceRef: "gpt", NativeModel: "gpt-p"},
		AccessPointModel{Name: "claude-p", SurfaceRef: "claude-p", NativeModel: "claude-p"})
	setSpec(f, spec)
	f.access.Scopes = append(f.access.Scopes, "llm:surface:gpt:openai.v1.chat-completions", "llm:model:gpt-p", "llm:surface:claude-p:anthropic.messages", "llm:model:claude-p")
}

func seedLimits(f *apFixture, surface, provider string, status int, headers map[string]string) {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	f.h.capacityStore().Record("tenant", surface, providerlimits.Parse(provider, status, h, time.Now()))
}

func TestGateRejectionsUseEachProtocolsNativeRateLimitError(t *testing.T) {
	f := newAPFixture(t)
	addCapacitySurfaces(t, f, &providerlimits.Policy{CooldownEnabled: true})
	records := attachLedger(t, f.h)
	seedLimits(f, "gpt", modelapi.ProviderOpenAI, 429, map[string]string{"Retry-After": "12"})
	seedLimits(f, "claude-p", modelapi.ProviderAnthropic, 529, map[string]string{"Retry-After": "7"})
	seedLimits(f, "bedrock", modelapi.ProviderBedrock, 400, map[string]string{"X-Amzn-Errortype": "ThrottlingException", "Retry-After-Ms": "4500"})
	seedLimits(f, "realtime", modelapi.ProviderOpenAI, 429, map[string]string{"Retry-After": "3"})
	before := len(f.upstreamCalls())

	w := f.do("POST", "/ap/prov/v1/chat/completions", capacityChatBody[:0]+`{"model":"gpt-p","messages":[{"role":"user","content":"hi"}],"max_completion_tokens":3}`, "Content-Type", "application/json")
	var openai struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if w.Code != 429 || w.Header().Get("Retry-After") != "12" || json.Unmarshal(w.Body.Bytes(), &openai) != nil || openai.Error.Type != "rate_limit_error" || openai.Error.Message == "" {
		t.Fatalf("OpenAI shape: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	w = f.do("POST", "/ap/prov/v1/messages", `{"model":"claude-p","max_tokens":3,"messages":[{"role":"user","content":"hi"}]}`, "Content-Type", "application/json", "Anthropic-Version", "2023-06-01")
	var anthropic struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if w.Code != 429 || w.Header().Get("Retry-After") != "7" || json.Unmarshal(w.Body.Bytes(), &anthropic) != nil || anthropic.Type != "error" || anthropic.Error.Type != "rate_limit_error" {
		t.Fatalf("Anthropic shape: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	w = f.do("POST", "/ap/prov/model/claude-bedrock/converse", `{"messages":[{"role":"user","content":[{"text":"hi"}]}]}`, "Content-Type", "application/json")
	if w.Code != 429 || w.Header().Get("X-Amzn-ErrorType") != "ThrottlingException" || w.Header().Get("Retry-After") != "5" || !strings.HasPrefix(w.Body.String(), `{"message":`) {
		t.Fatalf("Bedrock shape: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	// A WebSocket session is gated at admission, before the upgrade.
	w = f.do("GET", "/ap/prov/v1/realtime?model=voice", "")
	if w.Code != 429 || w.Header().Get("Retry-After") != "3" || !strings.Contains(w.Body.String(), "rate_limit_error") {
		t.Fatalf("session admission: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if len(f.upstreamCalls()) != before {
		t.Fatal("a gate rejection reached the upstream")
	}
	got := records()
	if len(got) != 4 {
		t.Fatalf("ledger: %d records", len(got))
	}
	for _, r := range got {
		if r.ErrorClass != "capacity_cooldown" || r.Status != usageledger.StatusDenied || r.AccessPointID != "prov" || r.HTTPStatus != 429 {
			t.Fatalf("ledger record: %+v", r)
		}
	}
}

func TestAccessPointQoSWeightDecidesHeadroomShedding(t *testing.T) {
	f := newAPFixture(t)
	addCapacitySurfaces(t, f, &providerlimits.Policy{HeadroomFraction: 0.5, MinWeight: 10})
	records := attachLedger(t, f.h)
	withAPQuota(t, f, []quotaplex.ConcurrencyRule{{ID: "ap-prov", Scope: quotaplex.Scope{Kind: "principal"}, Match: quotaplex.Match{AccessPointID: "prov"}, Limit: 5}})
	f.access.Scopes = append(f.access.Scopes, "llm:surface:gpt:openai.v1.chat-completions", "llm:model:gpt-p")
	setWeight := func(weight uint32) {
		spec := apSpec()
		spec.ID = "prov"
		spec.Models = append(spec.Models, AccessPointModel{Name: "gpt-p", SurfaceRef: "gpt", NativeModel: "gpt-p"})
		spec.QoS = &AccessPointQoS{Class: "interactive", Weight: weight, Limits: &AccessPointLimit{Concurrency: 5}}
		record, _ := f.store.Get(context.Background(), "tenant", AccessPointKind, "prov")
		raw, _ := json.Marshal(spec)
		if _, err := f.store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: AccessPointKind, ID: "prov", Spec: raw}, record.Revision); err != nil {
			t.Fatal(err)
		}
	}
	seedLimits(f, "gpt", modelapi.ProviderOpenAI, 200, map[string]string{"X-Ratelimit-Limit-Requests": "100", "X-Ratelimit-Remaining-Requests": "10", "X-Ratelimit-Reset-Requests": "25s"})
	body := `{"model":"gpt-p","messages":[{"role":"user","content":"hi"}],"max_completion_tokens":3}`

	setWeight(5)
	w := f.do("POST", "/ap/prov/v1/chat/completions", body, "Content-Type", "application/json")
	seconds, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if w.Code != 429 || err != nil || seconds < 23 || seconds > 25 || !strings.Contains(w.Body.String(), "reserved for higher-priority") {
		t.Fatalf("low-weight access point not shed: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	setWeight(50)
	w = f.do("POST", "/ap/prov/v1/chat/completions", body, "Content-Type", "application/json")
	// The gate admits it; QuotaPlex (which has no binding for this operation in
	// this fixture) still decides, so the gate did not bypass quota.
	if strings.Contains(w.Body.String(), "reserved for higher-priority") || w.Code == 200 {
		t.Fatalf("high-weight access point: %d %s", w.Code, w.Body.String())
	}
	if len(f.upstreamCalls()) != 0 {
		t.Fatal("quota was bypassed or the shed request dispatched")
	}
	classes := map[string]int{}
	for _, r := range records() {
		classes[r.ErrorClass]++
	}
	if classes["capacity_shed"] != 1 {
		t.Fatalf("ledger error classes: %v", classes)
	}
}

func TestHalfOpenLetsOneConcurrentRequestProbeTheProvider(t *testing.T) {
	var throttle atomic.Bool
	throttle.Store(true)
	release := make(chan struct{})
	f, _, _ := capacityQuotaFixture(t, &providerlimits.Policy{CooldownEnabled: true}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if throttle.Load() {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"type":"rate_limit_error"}}`)
			return
		}
		<-release // the probe is held in flight
		fmt.Fprint(w, `{"id":"chatcmpl-1","model":"model-v1","choices":[]}`)
	})
	if w := f.chat("k0"); w.Code != 429 {
		t.Fatalf("setup %d", w.Code)
	}
	throttle.Store(false)
	capacityClock.Store(6) // cooldown over
	const callers = 12
	type result struct {
		code  int
		retry string
	}
	results := make(chan result, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			w := f.chat("c" + strconv.Itoa(i))
			results <- result{w.Code, w.Header().Get("Retry-After")}
		}(i)
	}
	rejected := 0
	for rejected < callers-1 {
		r := <-results
		if r.code != 429 || r.retry == "" {
			t.Fatalf("non-probe caller: %+v", r)
		}
		rejected++
	}
	for deadline := time.Now().Add(2 * time.Second); f.calls.Load() < 2 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if f.calls.Load() != 2 { // the original throttled call plus exactly one probe
		t.Fatalf("thundering herd: %d upstream calls", f.calls.Load())
	}
	close(release)
	if r := <-results; r.code != 200 {
		t.Fatalf("probe result %+v", r)
	}
}
