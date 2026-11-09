package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

type quotaHostTestVerifier struct{ claims map[string]any }

func (v quotaHostTestVerifier) Verify(context.Context, string) (map[string]any, error) {
	return v.claims, nil
}

func quotaHostTestIdentity(t *testing.T, now time.Time, mutate func(map[string]any)) *models.WIFIdentity {
	t.Helper()
	claims := map[string]any{"iss": "https://quota-identity.test", "sub": "immutable-human", "iat": json.Number(strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)), "exp": json.Number(strconv.FormatInt(now.Add(time.Hour).Unix(), 10)), "groups": []any{"engineering"}}
	if mutate != nil {
		mutate(claims)
	}
	validator := auth.NewWIFValidator(registry.NewMemoryStore(), auth.WIFConfig{WorkforcePoolID: "people", IsWorkforce: true, TrustedIssuers: []string{"https://quota-identity.test"}, Verifier: quotaHostTestVerifier{claims}})
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "Bearer independently-verified-by-fixture")
	identity, err := validator.ExtractIdentity(r)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

type quotaHostTestFixture struct {
	host   *QuotaHost
	config QuotaHostConfig
	store  *quotaplex.MemoryStore
	now    time.Time
}

func quotaHostTestConfig(now time.Time) QuotaHostConfig {
	group := quotaplex.GroupRef{Source: auth.MembershipSource("https://quota-identity.test", "people"), ID: "finance"}
	policy := quotaplex.Policy{Version: quotaplex.Version, TenantID: "tenant", ID: "host-policy", Revision: 1, EffectiveAt: now.Add(-time.Hour), Meters: map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}}, Accounts: []quotaplex.Account{{ID: "team", Priority: 10, TenantWide: true}, {ID: "finance", Priority: 20, Eligible: []quotaplex.Eligibility{{SubjectRole: "principal", Group: &group}}}}, Budgets: []quotaplex.BudgetRule{{ID: "account-requests", Scope: quotaplex.Scope{Kind: "account"}, MeterID: "requests", Limit: 10, Period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}, {ID: "human-requests", Scope: quotaplex.Scope{Kind: "principal"}, MeterID: "requests", Limit: 10, Period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}}, Rates: []quotaplex.RateRule{{ID: "request-rate", Scope: quotaplex.Scope{Kind: "tenant"}, MeterID: "requests", Rate: 100, Interval: time.Second, Burst: 100, Basis: "admitted"}}, Concurrency: []quotaplex.ConcurrencyRule{{ID: "active", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 1}}}
	return QuotaHostConfig{MaxMembershipAge: 5 * time.Minute, Tenants: []QuotaTenantConfig{{Policy: policy, Activate: true}}, Bindings: []QuotaBinding{{ID: "model-call", TenantID: "tenant", Plane: "llmplex", ResourceID: "surface", OperationID: "models.generateContent", Model: "model-v1", Profile: "request-count", Provider: "google", ModelVersion: "model-v1", Meters: map[string]string{"requests": "requests"}}}}
}

func quotaHostTestNew(t *testing.T) *quotaHostTestFixture {
	t.Helper()
	f := &quotaHostTestFixture{now: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), store: quotaplex.NewMemoryStore()}
	f.config = quotaHostTestConfig(f.now)
	var err error
	f.host, err = NewQuotaHost(context.Background(), f.config, f.store, func() time.Time { return f.now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func quotaHostTestInvocation(identity *models.WIFIdentity, key, body string) (*http.Request, QuotaInvocation) {
	r := httptest.NewRequest(http.MethodPost, "/native/surface/generate", strings.NewReader(body))
	ctx := context.WithValue(r.Context(), keyWIFIdentity, identity)
	ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: *identity, Scopes: []string{"llm:surface:surface:models.generateContent", "llm:model:model-v1"}})
	r = r.WithContext(ctx)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	invocation := QuotaInvocation{Context: contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: "transport-correlation", PrincipalID: identity.PrincipalID, ActorID: identity.ActorID, ResourceID: "surface"}, Plane: "llmplex", ResourceID: "surface", OperationID: "models.generateContent", Model: "model-v1", Target: quotaplex.Target{Provider: "google", ResourceID: "surface", Operation: "models.generateContent", BillingAccount: "project", Region: "us-central1"}, Body: []byte(body), Method: http.MethodPost, Path: "/v1/models/model-v1:generateContent", Headers: r.Header.Clone(), Configuration: map[string]any{"connection_revision": 7, "identity": "operator-credential"}, Reauthorize: func(context.Context) error { return nil }}
	return r, invocation
}

func quotaHostTestAttempt(t *testing.T, lease execution.Lease) string {
	t.Helper()
	receipt, ok := lease.(execution.Receipt)
	if !ok || receipt.AttemptID() == "" {
		t.Fatal("quota lease does not expose its opaque recovery attempt")
	}
	return receipt.AttemptID()
}

func quotaHostTestStatus(t *testing.T, err error, status int) *execution.StatusError {
	t.Helper()
	var failure *execution.StatusError
	if !errors.As(err, &failure) || failure.Status != status {
		t.Fatalf("wanted HTTP%d quota failure, got %v", status, err)
	}
	return failure
}

func quotaHostTestAttempts(t *testing.T, store quotaplex.Store) []quotaplex.Record {
	t.Helper()
	var records []quotaplex.Record
	err := store.View(context.Background(), "tenant", func(tx quotaplex.Tx) error { var err error; records, err = tx.Scan("attempt", "", 100); return err })
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestQuotaHostKnownTerminalUsagePersistsOnceAndReleasesPermits(t *testing.T) {
	f := quotaHostTestNew(t)
	identity := quotaHostTestIdentity(t, f.now, nil)
	r, in := quotaHostTestInvocation(identity, "first", `{"caller_cost":0,"groups":["finance"]}`)
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := quotaHostTestAttempt(t, lease)
	if err = lease.BeforeDispatch(r.Context()); err != nil {
		t.Fatal(err)
	}
	if err = lease.Resource(r.Context(), execution.Resource{Kind: "response", ID: "native-response"}); err != nil {
		t.Fatal(err)
	}
	if err = lease.Observe(r.Context(), execution.Observation{Source: "upstream", ResourceID: "native-response", Counters: map[string]int64{"requests": 1}, Cumulative: true, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(r.Context(), execution.Result{StatusCode: 200, ResponseComplete: true, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	engine := f.host.Engines()["tenant"]
	info, err := engine.GetAttempt(context.Background(), "tenant", attemptID)
	if err != nil || info.Decision.State != "settled" || info.Actual["requests"] != 1 || info.Plan.PayerID != "team" || info.ProviderResource != "native-response" {
		t.Fatalf("native terminal facts were lost: %+v %v", info, err)
	}
	for _, budget := range info.Plan.Budgets {
		balance, err := engine.BudgetBalance(context.Background(), "tenant", budget.Key)
		if err != nil || balance.Consumed != 1 || balance.Held != 0 || balance.Available != 9 {
			t.Fatalf("compound control did not settle %+v %v", balance, err)
		}
	}
	if len(info.Demand.Principal.Membership.Groups) != 1 || info.Demand.Principal.Membership.Groups[0].ID != "engineering" {
		t.Fatal("body groups became funding authority", info.Demand.Principal)
	}
	if _, err = f.host.Prepare(r.Context(), r, in); err == nil {
		t.Fatal("settled replay obtained another execution lease")
	} else {
		quotaHostTestStatus(t, err, 409)
	}
	_, secondIn := quotaHostTestInvocation(identity, "second", `{}`)
	second, err := f.host.Prepare(r.Context(), r, secondIn)
	if err != nil {
		t.Fatal("terminal work retained a concurrency permit", err)
	}
	if err = second.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaHostUnknownWorkRetainsExposureAndBlocksNextDispatch(t *testing.T) {
	f := quotaHostTestNew(t)
	identity := quotaHostTestIdentity(t, f.now, nil)
	r, in := quotaHostTestInvocation(identity, "uncertain", `{}`)
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	attempt := quotaHostTestAttempt(t, lease)
	if err = lease.BeforeDispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = lease.Finish(context.Background(), execution.Result{StatusCode: 502, Err: errors.New("connection reset")})
	// An erroneous cleanup call cannot undo possibly executed provider work.
	_ = lease.Abort(context.Background())
	engine := f.host.Engines()["tenant"]
	info, err := engine.GetAttempt(context.Background(), "tenant", attempt)
	if err != nil || info.Decision.State != "unknown" {
		t.Fatal("uncertain execution lost recovery state", info, err)
	}
	for _, budget := range info.Plan.Budgets {
		balance, err := engine.BudgetBalance(context.Background(), "tenant", budget.Key)
		if err != nil || balance.Held != 1 || balance.Consumed != 0 {
			t.Fatal(balance, err)
		}
	}
	r2, in2 := quotaHostTestInvocation(identity, "other", `{}`)
	if _, err = f.host.Prepare(r2.Context(), r2, in2); err == nil {
		t.Fatal("unknown work lost its concurrency constraint")
	} else {
		quotaHostTestStatus(t, err, 429)
	}
}

func TestQuotaHostRejectsUnprovenIdentityAndActorBeforeReservation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing lifetime", func(c map[string]any) { delete(c, "iat") }},
		{"missing groups", func(c map[string]any) { delete(c, "groups") }},
		{"incomplete groups", func(c map[string]any) { c["groups"] = nil }},
		{"delegated actor has no independent snapshot", func(c map[string]any) { c["act"] = map[string]any{"sub": "agent", "groups": []any{"finance"}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := quotaHostTestNew(t)
			identity := quotaHostTestIdentity(t, f.now, tc.mutate)
			r, in := quotaHostTestInvocation(identity, "no-proof", `{}`)
			if _, err := f.host.Prepare(r.Context(), r, in); err == nil {
				t.Fatal("missing membership evidence admitted")
			} else {
				quotaHostTestStatus(t, err, 503)
			}
			if records := quotaHostTestAttempts(t, f.store); len(records) != 0 {
				t.Fatal("invalid identity reserved quota", records)
			}
		})
	}
}

func TestQuotaHostCallerCannotChooseAnIneligibleAccountOrUnboundAlias(t *testing.T) {
	for _, tc := range []string{"funding", "model alias", "operation"} {
		t.Run(tc, func(t *testing.T) {
			f := quotaHostTestNew(t)
			identity := quotaHostTestIdentity(t, f.now, nil)
			r, in := quotaHostTestInvocation(identity, "invalid-target", `{"funding_account_id":"finance"}`)
			status := 503
			switch tc {
			case "funding":
				in.Headers.Set("X-Aiplex-Funding-Account", "finance")
				status = 403
			case "model alias":
				in.Model = "projects/project/locations/us-central1/models/model-v1"
			case "operation":
				in.OperationID = "models.unboundBillableCall"
				in.Target.Operation = in.OperationID
			}
			if _, err := f.host.Prepare(r.Context(), r, in); err == nil {
				t.Fatal("caller selected unbound funding/coverage")
			} else {
				quotaHostTestStatus(t, err, status)
			}
			for _, record := range quotaHostTestAttempts(t, f.store) {
				var info struct{ Decision quotaplex.Decision }
				if json.Unmarshal(record.Value, &info) != nil || info.Decision.Allowed {
					t.Fatal("invalid request created capacity", record)
				}
			}
		})
	}
}

func TestQuotaHostIdempotencyBindsPayloadAndSeparatesTransportCorrelation(t *testing.T) {
	f := quotaHostTestNew(t)
	identity := quotaHostTestIdentity(t, f.now, nil)
	r, in := quotaHostTestInvocation(identity, "same-key", `{"text":"original"}`)
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	changed := in
	changed.Body = []byte(`{"text":"changed"}`)
	if _, err = f.host.Prepare(r.Context(), r, changed); err == nil {
		t.Fatal("one idempotency key retargeted its held request")
	} else {
		quotaHostTestStatus(t, err, 409)
	}
	retry := in
	retry.Context.RequestID = "different-transport-id"
	replayed, err := f.host.Prepare(r.Context(), r, retry)
	if err != nil {
		t.Fatal("correlation change invalidated accounting identity", err)
	}
	if quotaHostTestAttempt(t, replayed) != quotaHostTestAttempt(t, lease) || len(quotaHostTestAttempts(t, f.store)) != 1 {
		t.Fatal("transport retry created another admission")
	}
	if err = replayed.BeforeDispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = lease.BeforeDispatch(context.Background()); err == nil {
		t.Fatal("duplicate lease gained a second dispatch")
	}
}

func TestQuotaHostSignedMembershipAgeCannotBeRenewedByAnotherRequest(t *testing.T) {
	f := quotaHostTestNew(t)
	identity := quotaHostTestIdentity(t, f.now, func(c map[string]any) {
		c["iat"] = json.Number(strconv.FormatInt(f.now.Add(-4*time.Minute).Unix(), 10))
	})
	r, in := quotaHostTestInvocation(identity, "old-assertion", `{}`)
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", quotaHostTestAttempt(t, lease))
	if err != nil || !info.Demand.Principal.Membership.ExpiresAt.Equal(f.now.Add(time.Minute)) {
		t.Fatal("request time fabricated fresh group validity", info, err)
	}
	f.now = f.now.Add(time.Minute)
	if err = lease.BeforeDispatch(context.Background()); err == nil {
		t.Fatal("expired membership passed the dispatch boundary")
	}
	if err = lease.Abort(context.Background()); err != nil {
		t.Fatal("undispatched stale request cannot release its hold", err)
	}
	r2, in2 := quotaHostTestInvocation(identity, "same-old-assertion", `{}`)
	if _, err = f.host.Prepare(r2.Context(), r2, in2); err == nil {
		t.Fatal("new request renewed signed membership age")
	} else {
		quotaHostTestStatus(t, err, 503)
	}
}

func TestQuotaHostBindingRevisionsFenceOldReplicasAndHeldDispatch(t *testing.T) {
	f := quotaHostTestNew(t)
	identity := quotaHostTestIdentity(t, f.now, nil)
	r, in := quotaHostTestInvocation(identity, "before-upgrade", `{}`)
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	// Caller-owned constructor input cannot mutate the published binding.
	f.config.Bindings[0].PoolID = "revised-pool"
	if _, err = NewQuotaHost(context.Background(), f.config, f.store, func() time.Time { return f.now }, nil, nil); !errors.Is(err, quotaplex.ErrConflict) {
		t.Fatal("same policy revision silently changed quota bindings", err)
	}
	if err = f.host.Ready(context.Background()); err != nil {
		t.Fatal("failed host change displaced healthy replica", err)
	}
	f.config.Tenants[0].Policy.Revision = 2
	f.config.Tenants[0].ExpectedDigest = f.host.tenants["tenant"].policy.Reference().Digest
	newHost, err := NewQuotaHost(context.Background(), f.config, f.store, func() time.Time { return f.now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = newHost.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = f.host.Ready(context.Background()); err == nil {
		t.Fatal("old replica still advertises the prior host configuration")
	}
	if err = lease.BeforeDispatch(context.Background()); err == nil {
		t.Fatal("old held request dispatched after binding activation")
	}
	if err = lease.Abort(context.Background()); err != nil {
		t.Fatal("old known-undispatched reservation was stranded", err)
	}
}

type quotaHostTestActor struct{ subject quotaplex.Subject }

func (a quotaHostTestActor) ResolveActor(context.Context, string, string, time.Time) (quotaplex.Subject, error) {
	return a.subject, nil
}

func TestQuotaHostActorMembershipStaysIndependentOfHumanMembership(t *testing.T) {
	f := quotaHostTestNew(t)
	identity := quotaHostTestIdentity(t, f.now, func(c map[string]any) {
		c["act"] = map[string]any{"sub": "agent", "groups": []any{"must-not-be-trusted"}}
	})
	actor := quotaplex.Subject{ID: identity.ActorID, Kind: "agent", Membership: quotaplex.Membership{Ref: contracts.SnapshotRef{ID: "independent-agent-view", Version: "5"}, TenantID: "tenant", SubjectID: identity.ActorID, Groups: []quotaplex.GroupRef{{Source: "workload-directory", ID: "automation"}}, IssuedAt: f.now.Add(-time.Minute), ExpiresAt: f.now.Add(time.Minute)}}
	f.host.actor = quotaHostTestActor{actor}
	r, in := quotaHostTestInvocation(identity, "delegated", `{}`)
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", quotaHostTestAttempt(t, lease))
	if err != nil || info.Demand.Actor == nil || len(info.Demand.Actor.Membership.Groups) != 1 || info.Demand.Actor.Membership.Groups[0].Source != "workload-directory" || info.Demand.Actor.Membership.Groups[0].ID != "automation" {
		t.Fatalf("actor facts did not come from independent authority: %+v %v", info.Demand.Actor, err)
	}
	if info.Demand.Actor.Membership.Ref == info.Demand.Principal.Membership.Ref {
		t.Fatal("actor reused principal membership reference")
	}
}

type quotaHostTestBounds struct {
	input  int64
	expiry time.Time
}

func (*quotaHostTestBounds) ConfigurationDigest() string { return strings.Repeat("f", 64) }
func (b *quotaHostTestBounds) Bounds(_ context.Context, in QuotaBoundsRequest) (QuotaBoundsProof, error) {
	return QuotaBoundsProof{NativeBounds: map[string]int64{"uncached_input_tokens": b.input, "cached_tokens": b.input, "output_tokens": in.RequestedOutput, "thought_tokens": in.RequestedOutput}, EvidenceRef: "verified-provider-limit-v1", ExpiresAt: b.expiry, ConfigurationDigest: in.Target.ConfigurationDigest, ModelVersion: in.Target.ModelVersion}, nil
}

func TestQuotaHostReprovesTokenExposureBeforeDispatch(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	cfg := quotaHostTestConfig(now)
	for _, native := range []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"} {
		cfg.Tenants[0].Policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens"}
		cfg.Bindings[0].Meters[native] = native
	}
	cfg.Bindings[0].Profile = "gemini-text"
	authority := &quotaHostTestBounds{input: 100, expiry: now.Add(time.Hour)}
	store := quotaplex.NewMemoryStore()
	host, err := NewQuotaHost(context.Background(), cfg, store, func() time.Time { return now }, nil, authority)
	if err != nil {
		t.Fatal(err)
	}
	identity := quotaHostTestIdentity(t, now, nil)
	r, in := quotaHostTestInvocation(identity, "bound-proof", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2}}`)
	lease, err := host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	info, err := host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", quotaHostTestAttempt(t, lease))
	if err != nil {
		t.Fatal(err)
	}
	if info.Demand.Bounds["uncached_input_tokens"] != 100 || info.Demand.Bounds["cached_tokens"] != 100 || info.Demand.Bounds["thought_tokens"] != 2 {
		t.Fatal("host substituted client estimates for bound authority", info.Demand.Bounds)
	}
	authority.input = 50
	if err = lease.BeforeDispatch(context.Background()); err == nil {
		t.Fatal("provider limit proof changed without a dispatch fence")
	} else {
		quotaHostTestStatus(t, err, 409)
	}
	if err = lease.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err = host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", quotaHostTestAttempt(t, lease))
	if err != nil || info.Decision.State != "aborted" {
		t.Fatal("changed bound left a known-undispatched hold", info, err)
	}
}

func TestQuotaHostRejectsInvalidMetadataBeforeBinding(t *testing.T) {
	f := quotaHostTestNew(t)
	identity := quotaHostTestIdentity(t, f.now, nil)
	for _, name := range []string{"Idempotency-Key", "X-Aiplex-Funding-Account"} {
		t.Run(name, func(t *testing.T) {
			r, in := quotaHostTestInvocation(identity, "first", `{}`)
			in.Headers[name] = []string{"one", "two"}
			if _, err := f.host.Prepare(r.Context(), r, in); err == nil {
				t.Fatal("multiple quota metadata values accepted")
			} else {
				quotaHostTestStatus(t, err, 400)
			}
		})
	}
	if records := quotaHostTestAttempts(t, f.store); len(records) != 0 {
		t.Fatalf("bad metadata created %d attempts", len(records))
	}
}

func TestQuotaHostTokenProfileRejectsUnboundedNativeFeaturesBeforeReservation(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	cfg := quotaHostTestConfig(now)
	for _, native := range []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"} {
		cfg.Tenants[0].Policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens"}
		cfg.Bindings[0].Meters[native] = native
	}
	cfg.Bindings[0].Profile = "gemini-text"
	if _, err := NewQuotaHost(context.Background(), cfg, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil); err == nil {
		t.Fatal("an operator maximum without a provider authority enabled token enforcement")
	}
	authority := &quotaHostTestBounds{input: 100, expiry: now.Add(time.Hour)}
	store := quotaplex.NewMemoryStore()
	host, err := NewQuotaHost(context.Background(), cfg, store, func() time.Time { return now }, nil, authority)
	if err != nil {
		t.Fatal(err)
	}
	identity := quotaHostTestIdentity(t, now, nil)
	bodies := map[string]string{
		"media":                  `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"abcd"}}]}],"generationConfig":{"maxOutputTokens":2}}`,
		"tools":                  `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2},"tools":[{"googleSearch":{}}]}`,
		"cache":                  `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2},"cachedContent":"external-cache"}`,
		"multiple candidates":    `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2,"candidateCount":2}}`,
		"missing output limit":   `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{}}`,
		"duplicate output limit": `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2,"maxOutputTokens":200}}`,
		"history":                `{"contents":[{"role":"user","parts":[{"text":"question"}]},{"role":"model","parts":[{"text":"answer"}]}],"generationConfig":{"maxOutputTokens":2}}`,
		"alternative routing":    `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2,"routingConfig":{"autoMode":{}}}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			r, in := quotaHostTestInvocation(identity, name, body)
			if _, err := host.Prepare(r.Context(), r, in); err == nil {
				t.Fatal("unsupported native feature gained token coverage")
			} else {
				quotaHostTestStatus(t, err, 400)
			}
		})
	}
	if attempts := quotaHostTestAttempts(t, store); len(attempts) != 0 {
		t.Fatal("unbounded native request reserved quota", attempts)
	}
}

type quotaHostTestRecordingBounds struct {
	quotaHostTestBounds
	bodies []string
}

func (b *quotaHostTestRecordingBounds) Bounds(ctx context.Context, in QuotaBoundsRequest) (QuotaBoundsProof, error) {
	b.bodies = append(b.bodies, string(in.Body))
	return b.quotaHostTestBounds.Bounds(ctx, in)
}

func TestQuotaHostOwnsAdmittedBodyAcrossExposureReproof(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	cfg := quotaHostTestConfig(now)
	for _, native := range []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"} {
		cfg.Tenants[0].Policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens"}
		cfg.Bindings[0].Meters[native] = native
	}
	cfg.Bindings[0].Profile = "gemini-text"
	authority := &quotaHostTestRecordingBounds{quotaHostTestBounds: quotaHostTestBounds{input: 100, expiry: now.Add(time.Hour)}}
	store := quotaplex.NewMemoryStore()
	host, err := NewQuotaHost(context.Background(), cfg, store, func() time.Time { return now }, nil, authority)
	if err != nil {
		t.Fatal(err)
	}
	const admitted = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":3}}`
	r, in := quotaHostTestInvocation(quotaHostTestIdentity(t, now, nil), "owned-body", admitted)
	lease, err := host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the same caller-owned backing array rather than replace its slice.
	copy(in.Body, strings.Replace(admitted, `"maxOutputTokens":3`, `"maxOutputTokens":9`, 1))
	if err = lease.BeforeDispatch(r.Context()); err != nil {
		t.Fatal("caller body mutation changed the admitted exposure proof", err)
	}
	if len(authority.bodies) < 2 {
		t.Fatal("provider authority was not reproved at dispatch", authority.bodies)
	}
	for _, observed := range authority.bodies {
		if observed != admitted {
			t.Fatal("bounds authority saw caller mutation after admission", observed)
		}
	}
	if err = lease.Observe(r.Context(), execution.Observation{Source: "upstream", ModelVersion: "model-v1", Counters: map[string]int64{"requests": 1, "uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 2, "thought_tokens": 1}, Cumulative: true, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(r.Context(), execution.Result{StatusCode: 200, ResponseComplete: true, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	info, err := host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", quotaHostTestAttempt(t, lease))
	if err != nil || info.Decision.State != "settled" || info.Demand.Bounds["output_tokens"] != 3 {
		t.Fatal("mutation changed admitted accounting", info, err)
	}
}

func TestQuotaHostRejectsBindingLimitsAsExposureAuthority(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for _, profile := range []string{"request-count", "gemini-text"} {
		t.Run(profile, func(t *testing.T) {
			cfg := quotaHostTestConfig(now)
			cfg.Bindings[0].Profile = profile
			cfg.Bindings[0].Limits = map[string]int64{"uncached_input_tokens": 100}
			if profile == "gemini-text" {
				for _, native := range []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"} {
					cfg.Tenants[0].Policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens"}
					cfg.Bindings[0].Meters[native] = native
				}
			}
			authority := &quotaHostTestBounds{input: 100, expiry: now.Add(time.Hour)}
			if _, err := NewQuotaHost(context.Background(), cfg, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, authority); err == nil {
				t.Fatal("an unproved operator limit became native exposure authority")
			}
		})
	}
}

func TestQuotaHostRejectsIncompatibleGeminiBindingBeforeActivation(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for _, incompatible := range []string{"provider", "operation"} {
		t.Run(incompatible, func(t *testing.T) {
			cfg := quotaHostTestConfig(now)
			cfg.Bindings[0].Profile = "gemini-text"
			for _, native := range []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"} {
				cfg.Tenants[0].Policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens"}
				cfg.Bindings[0].Meters[native] = native
			}
			if incompatible == "provider" {
				cfg.Bindings[0].Provider = "anthropic"
			} else {
				cfg.Bindings[0].OperationID = "models.countTokens"
			}
			authority := &quotaHostTestBounds{input: 100, expiry: now.Add(time.Hour)}
			store := quotaplex.NewMemoryStore()
			if _, err := NewQuotaHost(context.Background(), cfg, store, func() time.Time { return now }, nil, authority); err == nil {
				t.Fatal("incompatible Gemini provider/operation binding activated")
			}
			var published []quotaplex.Record
			if err := store.View(context.Background(), "tenant", func(tx quotaplex.Tx) error {
				var err error
				published, err = tx.Scan("quota-host-config", "", 10)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if len(published) != 0 {
				t.Fatal("invalid provider profile published an active host configuration", published)
			}
		})
	}
}

func TestQuotaHostGeminiRejectsUsageAndVersionSuppressionBeforeAdmission(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	cfg := quotaHostTestConfig(now)
	for _, native := range []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"} {
		cfg.Tenants[0].Policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens"}
		cfg.Bindings[0].Meters[native] = native
	}
	cfg.Bindings[0].Profile = "gemini-text"
	authority := &quotaHostTestRecordingBounds{quotaHostTestBounds: quotaHostTestBounds{input: 100, expiry: now.Add(time.Hour)}}
	store := quotaplex.NewMemoryStore()
	host, err := NewQuotaHost(context.Background(), cfg, store, func() time.Time { return now }, nil, authority)
	if err != nil {
		t.Fatal(err)
	}
	identity := quotaHostTestIdentity(t, now, nil)
	for _, query := range []string{"fields=candidates", "fields=candidates,usageMetadata", "%24fields=candidates%2CusageMetadata", "modelVersion=false", "alt=json&fields=responseId", "alt=sse"} {
		t.Run(query, func(t *testing.T) {
			r, in := quotaHostTestInvocation(identity, query, `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":3}}`)
			in.RawQuery = query
			if _, err := host.Prepare(r.Context(), r, in); err == nil {
				t.Fatal("partial response query bypassed mandatory native accounting facts")
			} else {
				status := quotaHostTestStatus(t, err, 400)
				if status.Code != "QUOTA_QUERY_UNSUPPORTED" {
					t.Fatal("query failed after entering a later admission phase", status)
				}
			}
		})
	}
	if len(authority.bodies) != 0 || len(quotaHostTestAttempts(t, store)) != 0 {
		t.Fatal("unsupported native response shape reached exposure authority or reservation", authority.bodies)
	}
}

func TestQuotaHostPartialInputOverrunQuarantinesWithoutInventingTerminalUsage(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	cfg := quotaHostTestConfig(now)
	for _, native := range []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"} {
		cfg.Tenants[0].Policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens"}
		cfg.Bindings[0].Meters[native] = native
	}
	cfg.Bindings[0].Profile = "gemini-text"
	cfg.Tenants[0].Policy.Budgets = append(cfg.Tenants[0].Policy.Budgets, quotaplex.BudgetRule{ID: "native-input", Scope: quotaplex.Scope{Kind: "tenant"}, MeterID: "uncached_input_tokens", Limit: 200, Period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}})
	authority := &quotaHostTestBounds{input: 100, expiry: now.Add(time.Hour)}
	store := quotaplex.NewMemoryStore()
	host, err := NewQuotaHost(context.Background(), cfg, store, func() time.Time { return now }, nil, authority)
	if err != nil {
		t.Fatal(err)
	}
	identity := quotaHostTestIdentity(t, now, nil)
	const body = `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":3}}`
	r, in := quotaHostTestInvocation(identity, "partial-overrun", body)
	lease, err := host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := quotaHostTestAttempt(t, lease)
	if err = lease.BeforeDispatch(r.Context()); err != nil {
		t.Fatal(err)
	}
	// A partial prompt count proves the context ceiling was exceeded, but
	// establishes neither the cache partition nor any final output quantity.
	if err = lease.Observe(r.Context(), execution.Observation{Source: "upstream", ModelVersion: "model-v1", Counters: map[string]int64{"input_tokens": 101}, Cumulative: true}); err != nil {
		t.Fatal("quarantining a provider-bound violation prevented recovery of ongoing execution", err)
	}
	nextRequest, next := quotaHostTestInvocation(identity, "after-partial-overrun", body)
	if _, err = host.Prepare(nextRequest.Context(), nextRequest, next); err == nil {
		t.Fatal("partial usage above the trusted context ceiling left the binding active")
	} else if status := quotaHostTestStatus(t, err, 503); status.Code != "QUOTA_CONFIGURATION_INACTIVE" {
		t.Fatal("new execution reached admission after a partial provider-bound violation", status)
	}
	if err = lease.Finish(r.Context(), execution.Result{StatusCode: 200, Err: errors.New("native stream truncated before final usage")}); !errors.Is(err, quotaplex.ErrTerminalEvidenceRequired) {
		t.Fatal("truncation fabricated terminal evidence", err)
	}
	engine := host.Engines()["tenant"]
	info, err := engine.GetAttempt(context.Background(), "tenant", attemptID)
	if err != nil || info.Decision.State != "unknown" || info.Claim.Fence == 0 || len(info.Actual) != 0 {
		t.Fatal("partial overrun lost unsettled execution state", info, err)
	}
	for _, budget := range info.Plan.Budgets {
		balance, err := engine.BudgetBalance(context.Background(), "tenant", budget.Key)
		if err != nil || balance.Held != info.Demand.Bounds[budget.MeterID] || balance.Consumed != 0 {
			t.Fatal("partial counts refunded exposure or became assumed final usage", balance, err)
		}
	}
	for _, kind := range []string{"terminal-evidence", "terminal-work", "terminal-attempt"} {
		var records []quotaplex.Record
		if err := store.View(context.Background(), "tenant", func(tx quotaplex.Tx) error {
			var err error
			records, err = tx.Scan(kind, "", 10)
			return err
		}); err != nil || len(records) != 0 {
			t.Fatal("partial usage created terminal outbox evidence", kind, records, err)
		}
	}
}
