package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

func schedulingDeployment(now time.Time) quotaSchedulingDeployment {
	return quotaSchedulingDeployment{Config: quotaplex.SchedulerConfig{ID: "managed-tools", Queue: quotaplex.QueuePolicy{TenantID: "tenant", PoolID: "tool-capacity", Generation: 1, MeterID: "requests", MaxEntries: 32, MaxReceipts: 64, MaxWait: time.Minute, MaxCost: 10, Quantum: 1, Weights: map[string]uint32{"shared": 1, "protected": 3}}, Capacity: quotaplex.CapacityAttestation{ID: "tool-service-capacity", Version: "1", SourceRef: "operator-verified-tool-capacity", TenantID: "tenant", PoolID: "tool-capacity", MeterID: "requests", Target: quotaplex.Match{Provider: "mcp", PoolID: "tool-capacity"}, Rate: 100, Burst: 100, Interval: time.Second, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}}, Routes: []quotaSchedulingRoute{{ResourceID: "tool", OperationID: "tools/call:search", PayerID: "team", FlowID: "shared"}}, PollInterval: time.Millisecond}
}
func schedulingDemand(now time.Time, id string) quotaplex.Demand {
	ref := contracts.SnapshotRef{ID: "verified-membership", Version: "1"}
	return quotaplex.Demand{Version: quotaplex.Version, Context: contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: id, PrincipalID: "human", ResourceID: "tool", MembershipSnapshotRef: &ref}, Principal: quotaplex.Subject{ID: "human", Kind: "human", Membership: quotaplex.Membership{Ref: ref, TenantID: "tenant", SubjectID: "human", Groups: []quotaplex.GroupRef{}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}}, AttemptID: id, LogicalOperationID: id, PayloadDigest: strings.Repeat("a", 64), Target: quotaplex.Target{ConfigurationDigest: strings.Repeat("b", 64), Plane: "mcplex", Provider: "mcp", ResourceID: "tool", Operation: "tools/call:search", PoolID: "tool-capacity"}, Bounds: quotaplex.Vector{"requests": 1}}
}
func schedulingFixture(t *testing.T, modify func(*quotaDeploymentFile, *quotaSchedulingDeployment)) (*quotaScheduledAdmission, *quotaplex.Engine, quotaDeploymentFile) {
	t.Helper()
	now := time.Now().UTC()
	spec := quotaStartupSpec(now)
	spec.Host.Bindings[0].PoolID = "tool-capacity"
	deployment := schedulingDeployment(now)
	if modify != nil {
		modify(&spec, &deployment)
	}
	spec.Scheduling = []quotaSchedulingDeployment{deployment}
	compiled, err := quotaplex.Compile(spec.Host.Tenants[0].Policy)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := quotaplex.NewEngine(compiled, quotaplex.NewMemoryStore(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Activate(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	controller, err := newQuotaScheduledAdmission(spec.Scheduling)
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.initialize(context.Background(), map[string]*quotaplex.Engine{"tenant": engine}, spec.Host.Tenants); err != nil {
		t.Fatal(err)
	}
	return controller, engine, spec
}
func schedulingAdmit(t *testing.T, a *quotaScheduledAdmission, e *quotaplex.Engine, d quotaplex.Demand) quotaplex.Decision {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	decision, err := a.Admit(ctx, e, d)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func TestQuotaScheduledAdmissionPreservesDeadlineAndCanonicalAttemptOnReplay(t *testing.T) {
	controller, engine, _ := schedulingFixture(t, nil)
	demand := schedulingDemand(time.Now().UTC(), "retry")
	first := schedulingAdmit(t, controller, engine, demand)
	demand.Context.RequestID = "new-transport-correlation"
	second := schedulingAdmit(t, controller, engine, demand)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("transport retry changed original admission", first, second)
	}
	info, err := engine.GetAttempt(context.Background(), "tenant", "retry")
	if err != nil || info.Decision.State != "admitted" {
		t.Fatal(info, err)
	}
	for _, budget := range info.Plan.Budgets {
		balance, err := engine.BudgetBalance(context.Background(), "tenant", budget.Key)
		if err != nil || balance.Held != 1 {
			t.Fatal("retry created another reservation", balance, err)
		}
	}
	claim, err := engine.ClaimDispatch(context.Background(), "tenant", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Settle(context.Background(), quotaplex.Settlement{TenantID: "tenant", AttemptID: "retry", EventID: "native-final", Claim: claim, Usage: quotaplex.Vector{"requests": 1}}); err != nil {
		t.Fatal(err)
	}
	settled := schedulingAdmit(t, controller, engine, demand)
	if settled.State != "settled" {
		t.Fatal("scheduler replay revived the original admission after terminal settlement", settled)
	}
}

func TestQuotaScheduledAdmissionResolvesPayerBeforeChoosingOperatorFlow(t *testing.T) {
	controller, engine, _ := schedulingFixture(t, func(spec *quotaDeploymentFile, deployment *quotaSchedulingDeployment) {
		spec.Host.Tenants[0].Policy.Accounts = []quotaplex.Account{{ID: "gold", Priority: 10, Eligible: []quotaplex.Eligibility{{SubjectRole: "principal", SubjectID: "gold-user"}}}, {ID: "bronze", Priority: 1, Eligible: []quotaplex.Eligibility{{SubjectRole: "principal", SubjectID: "bronze-user"}}}}
		spec.Host.Tenants[0].Policy.Budgets[0].Scope = quotaplex.Scope{Kind: "account"}
		deployment.Routes = []quotaSchedulingRoute{{ResourceID: "tool", OperationID: "tools/call:search", PayerID: "gold", FlowID: "protected"}, {ResourceID: "tool", OperationID: "tools/call:search", PayerID: "bronze", FlowID: "shared"}}
	})
	for _, test := range []struct{ subject, payer, flow string }{{"gold-user", "gold", "protected"}, {"bronze-user", "bronze", "shared"}} {
		demand := schedulingDemand(time.Now().UTC(), test.subject)
		demand.Context.PrincipalID = test.subject
		demand.Principal.ID = test.subject
		demand.Principal.Membership.SubjectID = test.subject
		decision := schedulingAdmit(t, controller, engine, demand)
		if !decision.Allowed || decision.PayerID != test.payer {
			t.Fatal(decision)
		}
		entry := controller.entries[schedulerCoordinate("tenant", "tool-capacity")]
		status, err := entry.scheduler.Get(context.Background(), test.subject)
		if err != nil || status.FlowID != test.flow {
			t.Fatal("caller chose a different allocation flow", status, err)
		}
	}
	unauthorized := schedulingDemand(time.Now().UTC(), "spoofed-gold")
	unauthorized.Context.PrincipalID = "bronze-user"
	unauthorized.Principal.ID = "bronze-user"
	unauthorized.Principal.Membership.SubjectID = "bronze-user"
	unauthorized.FundingAccountID = "gold"
	decision := schedulingAdmit(t, controller, engine, unauthorized)
	if decision.Allowed {
		t.Fatal("requested funding granted another flow", decision)
	}
}

func TestQuotaScheduledAdmissionCompetingCallersReceiveTheirOwnAtomicDecision(t *testing.T) {
	controller, engine, _ := schedulingFixture(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("concurrent-%02d", i)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			decision, err := controller.Admit(ctx, engine, schedulingDemand(time.Now().UTC(), id))
			if err != nil || decision.AttemptID != id || !decision.Allowed {
				t.Errorf("caller received another queued decision: %+v %v", decision, err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("concurrent-%02d", i)
		info, err := engine.GetAttempt(context.Background(), "tenant", id)
		if err != nil || info.Decision.State != "admitted" || info.Claim.Fence != 0 {
			t.Fatal(info, err)
		}
	}
}

func TestQuotaScheduledAdmissionCancelledQueuedWorkNeverCreatesHold(t *testing.T) {
	controller, engine, _ := schedulingFixture(t, func(_ *quotaDeploymentFile, deployment *quotaSchedulingDeployment) {
		deployment.Config.Capacity.Rate = 1
		deployment.Config.Capacity.Burst = 1
		deployment.Config.Capacity.Interval = time.Hour
	})
	now := time.Now().UTC()
	if decision := schedulingAdmit(t, controller, engine, schedulingDemand(now, "occupant")); !decision.Allowed {
		t.Fatal(decision)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := controller.Admit(ctx, engine, schedulingDemand(now, "cancel-queued")); done <- err }()
	entry := controller.entries[schedulerCoordinate("tenant", "tool-capacity")]
	deadline := time.Now().Add(time.Second)
	for {
		status, err := entry.scheduler.Get(context.Background(), "cancel-queued")
		if err == nil && status.State == "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request never queued", status, err)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled admission did not return")
	}
	status, err := entry.scheduler.Get(context.Background(), "cancel-queued")
	if err != nil || status.State != "cancelled" {
		t.Fatal(status, err)
	}
	if _, err = engine.GetAttempt(context.Background(), "tenant", "cancel-queued"); !errors.Is(err, quotaplex.ErrNotFound) {
		t.Fatal("queued cancellation created quota exposure", err)
	}
	info, err := engine.GetAttempt(context.Background(), "tenant", "occupant")
	if err != nil {
		t.Fatal(err)
	}
	balance, err := engine.BudgetBalance(context.Background(), "tenant", info.Plan.Budgets[0].Key)
	if err != nil || balance.Held != 1 {
		t.Fatal("cancelled work changed active reservation", balance, err)
	}
}

func TestQuotaScheduledConfigurationOwnsManifestSnapshots(t *testing.T) {
	now := time.Now().UTC()
	manifest := []quotaSchedulingDeployment{schedulingDeployment(now)}
	controller, err := newQuotaScheduledAdmission(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest := controller.ConfigurationDigest()
	manifest[0].Config.Queue.Weights["shared"] = 999
	manifest[0].Routes[0].FlowID = "protected"
	entry := controller.entries[schedulerCoordinate("tenant", "tool-capacity")]
	if entry.deployment.Config.Queue.Weights["shared"] != 1 || entry.deployment.Routes[0].FlowID != "shared" || controller.ConfigurationDigest() != digest {
		t.Fatal("operator input mutated controller behind pinned digest", entry.deployment)
	}
}

func TestQuotaScheduledHostCapacityExpiryIsRecheckedBeforeDispatch(t *testing.T) {
	now := time.Now().UTC()
	clock := now
	spec := quotaStartupSpec(now)
	spec.Host.Bindings[0].PoolID = "tool-capacity"
	deployment := schedulingDeployment(now)
	deployment.Config.Capacity.ExpiresAt = now.Add(time.Minute)
	controller, err := newQuotaScheduledAdmission([]quotaSchedulingDeployment{deployment})
	if err != nil {
		t.Fatal(err)
	}
	host, err := api.NewQuotaHost(context.Background(), spec.Host, quotaplex.NewMemoryStore(), func() time.Time { return clock }, nil, nil, api.WithQuotaAdmissionController(controller))
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.initialize(context.Background(), host.Engines(), spec.Host.Tenants); err != nil {
		t.Fatal(err)
	}
	validator := quotaRouteValidator(t, true)
	var verified *http.Request
	api.WIFAuth(validator)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { verified = r })).ServeHTTP(httptest.NewRecorder(), func() *http.Request {
		r := httptest.NewRequest("POST", "/api/v1/mcp/connections/tool/invoke", nil)
		r.Header.Set("Authorization", "Bearer owner")
		return r
	}())
	if verified == nil || api.GetWIFIdentity(verified.Context()) == nil {
		t.Fatal("verified caller missing")
	}
	identity := api.GetWIFIdentity(verified.Context())
	requestContext := contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: "native-request", PrincipalID: identity.PrincipalID, ResourceID: "tool"}
	ctx, cancel := context.WithTimeout(verified.Context(), time.Second)
	defer cancel()
	lease, err := host.Prepare(ctx, verified, api.QuotaInvocation{Context: requestContext, Plane: "mcplex", ResourceID: "tool", OperationID: "tools/call:search", Target: quotaplex.Target{Provider: "mcp"}, Body: []byte(`{"jsonrpc":"2.0","id":"rpc","method":"tools/call","params":{"name":"search","arguments":{}}}`), Headers: http.Header{"Idempotency-Key": []string{"expiring-capacity"}}, Method: http.MethodPost, Path: "/mcp", Configuration: struct{ Revision int }{1}, Reauthorize: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := lease.(execution.Receipt)
	if !ok {
		t.Fatal("managed quota lease lost attempt receipt")
	}
	clock = deployment.Config.Capacity.ExpiresAt
	if err = lease.BeforeDispatch(context.Background()); err == nil {
		t.Fatal("expired assigned capacity crossed actual host claim boundary")
	}
	if err = lease.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", receipt.AttemptID())
	if err != nil || info.Claim.Fence != 0 || info.Decision.State != "aborted" {
		t.Fatal("failed preclaim left dispatched exposure", info, err)
	}
	for _, budget := range info.Plan.Budgets {
		balance, err := host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
		if err != nil || balance.Held != 0 || balance.Consumed != 0 {
			t.Fatal(balance, err)
		}
	}
}

type schedulerRegistryStore struct {
	registry.Store
	quota quotaplex.Store
}

func (s schedulerRegistryStore) QuotaStore() quotaplex.Store { return s.quota }

func TestQuotaSchedulingStartupProofRenewalPreservesQuotaAndCapacity(t *testing.T) {
	now := time.Now().UTC()
	spec := quotaStartupSpec(now)
	spec.Host.Bindings[0].PoolID = "tool-capacity"
	deployment := schedulingDeployment(now)
	deployment.Config.Capacity.Rate = 1
	deployment.Config.Capacity.Burst = 1
	deployment.Config.Capacity.Interval = time.Hour
	spec.Scheduling = []quotaSchedulingDeployment{deployment}
	store := schedulerRegistryStore{Store: registry.NewMemoryStore(), quota: quotaplex.NewMemoryStore()}
	configure := func(spec quotaDeploymentFile) (*api.ConnectedHandler, *quotaRuntime) {
		h := api.NewConnectedHandler(nil, nil, nil, nil, true)
		cfg := &config.Config{DevelopmentMode: true, QuotaFile: quotaStartupFile(t, spec), EnabledModules: []string{"quotaplex"}}
		runtime, err := configureQuota(context.Background(), cfg, store, h)
		if err != nil {
			t.Fatal(err)
		}
		return h, runtime
	}
	first, _ := configure(spec)
	controller, err := newQuotaScheduledAdmission(spec.Scheduling)
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.initialize(context.Background(), first.Quota.Engines(), spec.Host.Tenants); err != nil {
		t.Fatal(err)
	}
	engine := first.Quota.Engines()["tenant"]
	if decision := schedulingAdmit(t, controller, engine, schedulingDemand(now, "before-renewal")); !decision.Allowed {
		t.Fatal(decision)
	}
	oldDigest := controller.entries[schedulerCoordinate("tenant", "tool-capacity")].scheduler.Digest()
	oldPolicy, err := quotaplex.Compile(spec.Host.Tenants[0].Policy)
	if err != nil {
		t.Fatal(err)
	}
	spec.Host.Tenants[0].Policy.Revision++
	spec.Host.Tenants[0].ExpectedDigest = oldPolicy.Reference().Digest
	spec.Scheduling[0].ExpectedDigest = oldDigest
	spec.Scheduling[0].Config.Capacity.Version = "2"
	spec.Scheduling[0].Config.Capacity.ExpiresAt = now.Add(2 * time.Hour)
	second, _ := configure(spec)
	renewed, err := newQuotaScheduledAdmission(spec.Scheduling)
	if err != nil {
		t.Fatal(err)
	}
	if err = renewed.initialize(context.Background(), second.Quota.Engines(), spec.Host.Tenants); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if _, err = renewed.Admit(ctx, second.Quota.Engines()["tenant"], schedulingDemand(time.Now().UTC(), "after-renewal")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("startup proof renewal reset consumed capacity", err)
	}
	if _, err = second.Quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", "after-renewal"); !errors.Is(err, quotaplex.ErrNotFound) {
		t.Fatal("blocked renewed attempt acquired a quota hold", err)
	}
	original, err := second.Quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", "before-renewal")
	if err != nil || original.Decision.State != "admitted" {
		t.Fatal(original, err)
	}
	balance, err := second.Quota.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", original.Plan.Budgets[0].Key)
	if err != nil || balance.Held != 1 {
		t.Fatal("renewal reset existing quota exposure", balance, err)
	}
}

func TestQuotaSchedulingChangedPayerFlowMappingRequiresExplicitMigration(t *testing.T) {
	controller, engine, spec := schedulingFixture(t, nil)
	entry := controller.entries[schedulerCoordinate("tenant", "tool-capacity")]
	demand := schedulingDemand(time.Now().UTC(), "waiting-for-mapping")
	if _, err := entry.scheduler.Enqueue(context.Background(), quotaplex.ScheduledRequest{Demand: demand, FlowID: "shared", Deadline: time.Now().Add(30 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	manifest := spec.Scheduling
	manifest[0].ExpectedDigest = entry.scheduler.Digest()
	manifest[0].Routes[0].FlowID = "protected"
	changed, err := newQuotaScheduledAdmission(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = changed.initialize(context.Background(), map[string]*quotaplex.Engine{"tenant": engine}, spec.Host.Tenants); !errors.Is(err, quotaplex.ErrConflict) {
		t.Fatal("new payer flow rules reused prior queued authority", err)
	}
	if _, err = engine.GetAttempt(context.Background(), "tenant", demand.AttemptID); !errors.Is(err, quotaplex.ErrNotFound) {
		t.Fatal("changed flow map admitted old queue work", err)
	}
}

func TestQuotaSchedulingManagedNativeToolSettlesAndBlocksSecondProviderDispatch(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer operator-scheduled-tool-token" {
			t.Error("operator credential missing")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"rpc","result":{"content":[{"type":"text","text":"managed native result"}]}}`)
	}))
	defer upstream.Close()
	t.Setenv("SCHEDULED_NATIVE_TOOL_TOKEN", "operator-scheduled-tool-token")
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{upstream.URL}, AllowLoopback: true}, Identities: []connections.Identity{{ID: "server", TenantID: "tenant", Type: "bearer-env", TokenEnv: "SCHEDULED_NATIVE_TOOL_TOKEN", AllowedOrigins: []string{upstream.URL}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	saved := resources.NewMemoryStore()
	specBytes, err := json.Marshal(api.ToolConnection{ID: "tool", ResourceURL: upstream.URL + "/mcp", Execution: &toolexec.Connection{ID: "tool", Endpoint: upstream.URL + "/mcp", IdentityRef: "server", AllowedTools: []string{"search"}, ProtocolVersion: "2025-03-26", Mode: toolexec.StatelessJSON}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = saved.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "tool-connection", ID: "tool", Spec: specBytes}, 0); err != nil {
		t.Fatal(err)
	}
	h := api.NewConnectedHandler(saved, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	now := time.Now().UTC()
	spec := quotaStartupSpec(now)
	spec.Host.Bindings[0].PoolID = "tool-capacity"
	deployment := schedulingDeployment(now)
	deployment.Config.Capacity.Rate = 1
	deployment.Config.Capacity.Burst = 1
	deployment.Config.Capacity.Interval = time.Hour
	spec.Scheduling = []quotaSchedulingDeployment{deployment}
	if _, err = configureQuota(context.Background(), &config.Config{DevelopmentMode: true, QuotaFile: quotaStartupFile(t, spec), EnabledModules: []string{"mcplex", "quotaplex"}}, registry.NewMemoryStore(), h); err != nil {
		t.Fatal(err)
	}
	router := mountedQuotaRouter(t, h, quotaRouteValidator(t, true), func(r chi.Router) { r.Post("/mcp/connections/{id}/invoke", h.NativeTool) })
	invoke := func(ctx context.Context, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/connections/tool/invoke", strings.NewReader(`{"jsonrpc":"2.0","id":"rpc","method":"tools/call","params":{"name":"search","arguments":{}}}`)).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer owner")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	first := invoke(context.Background(), "first")
	if first.Code != 200 || calls.Load() != 1 || first.Header().Get(execution.AttemptHeader) == "" {
		t.Fatal("scheduled native tool did not complete", first.Code, first.Body.String(), calls.Load())
	}
	info, err := h.Quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", first.Header().Get(execution.AttemptHeader))
	if err != nil || info.Decision.State != "settled" || info.Actual["requests"] != 1 {
		t.Fatal("managed scheduler bypassed actual settlement", info, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	blocked := invoke(ctx, "second")
	if blocked.Code == 200 || calls.Load() != 1 {
		t.Fatal("exhausted assigned capacity reached provider", blocked.Code, blocked.Body.String(), calls.Load())
	}
}
