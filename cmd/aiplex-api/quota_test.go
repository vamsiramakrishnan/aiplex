package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

type partialQuotaRecovery struct{ after []string }

func (p *partialQuotaRecovery) RunOnce(_ context.Context, page quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error) {
	p.after = append(p.after, page.AfterID)
	return quotaplex.RecoveryReport{Scanned: 1, NextAfterID: "progress"}, context.DeadlineExceeded
}
func (*partialQuotaRecovery) RecoverAttempts(context.Context, quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error) {
	return quotaplex.RecoveryReport{Done: true}, nil
}
func (*partialQuotaRecovery) ReconcileLoans(context.Context, quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error) {
	return quotaplex.RecoveryReport{}, errors.New("no scan progress")
}

func TestQuotaWorkerRetainsPartialProgressWithoutStarvingLaterRecords(t *testing.T) {
	fake := &partialQuotaRecovery{}
	m := &quotaRuntime{workers: []quotaWorker{{tenant: "tenant", recovery: fake, cursors: [3]string{"start", "previous-attempt", "previous-loan"}}}}
	m.pass(context.Background())
	m.pass(context.Background())
	if len(fake.after) != 2 || fake.after[0] != "start" || fake.after[1] != "progress" {
		t.Fatal("bounded failure discarded recovery checkpoint", fake.after)
	}
	if m.workers[0].cursors[1] != "" || m.workers[0].cursors[2] != "previous-loan" {
		t.Fatal("independent cursors were lost", m.workers[0].cursors)
	}
}

func quotaStartupSpec(now time.Time) quotaDeploymentFile {
	policy := quotaplex.Policy{Version: quotaplex.Version, TenantID: "tenant", ID: "quota", Revision: 1, EffectiveAt: now.Add(-time.Hour), Meters: map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}}, Accounts: []quotaplex.Account{{ID: "team", TenantWide: true}}, Budgets: []quotaplex.BudgetRule{{ID: "team-requests", Scope: quotaplex.Scope{Kind: "account", ID: "team"}, MeterID: "requests", Limit: 20, Period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}}}
	return quotaDeploymentFile{Version: quotaplex.Version, Host: api.QuotaHostConfig{Tenants: []api.QuotaTenantConfig{{Policy: policy, Activate: true}}, Bindings: []api.QuotaBinding{{ID: "tool", TenantID: "tenant", Plane: "mcplex", ResourceID: "tool", OperationID: "tools/call:search", Profile: "request-count", Provider: "mcp", Meters: map[string]string{"requests": "requests"}}}}, RecoveryInterval: "1s"}
}
func quotaStartupFile(t *testing.T, spec quotaDeploymentFile) string {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "quota.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestQuotaStartupRequiresExplicitModuleAndDurableProductionStore(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	h := api.NewConnectedHandler(nil, nil, nil, nil, true)
	file := quotaStartupFile(t, quotaStartupSpec(time.Now()))
	for _, cfg := range []config.Config{{QuotaFile: file}, {EnabledModules: []string{"quotaplex"}}, {QuotaFile: file, EnabledModules: []string{"quotaplex"}}} {
		if _, err := configureQuota(ctx, &cfg, store, h); err == nil {
			t.Fatalf("unsafe quota configuration accepted: %+v", cfg)
		}
		if h.Quota != nil {
			t.Fatal("failed startup installed quota hooks")
		}
	}
	if runtime, err := configureQuota(ctx, &config.Config{}, store, h); err != nil || runtime != nil || h.Quota != nil {
		t.Fatal("disabled quota changed legacy startup", runtime, err)
	}
}

func TestQuotaStartupLifecycleOwnsWorkerBeyondStartupDeadline(t *testing.T) {
	ctx := context.Background()
	h := api.NewConnectedHandler(nil, nil, nil, nil, true)
	cfg := config.Config{DevelopmentMode: true, QuotaFile: quotaStartupFile(t, quotaStartupSpec(time.Now())), EnabledModules: []string{"mcplex", "quotaplex"}}
	runtime, err := configureQuota(ctx, &cfg, registry.NewMemoryStore(), h)
	if err != nil {
		t.Fatal(err)
	}
	if h.Quota == nil || len(runtime.workers) != 1 {
		t.Fatal("quota host/worker was not installed")
	}
	if err = runtime.Ready(ctx); err == nil {
		t.Fatal("not-started runtime ready")
	}
	startup, cancel := context.WithCancel(ctx)
	if err = runtime.Start(startup); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-runtime.done:
		t.Fatal("startup cancellation killed the lifecycle-owned worker")
	case <-time.After(10 * time.Millisecond):
	}
	if err = runtime.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Ready(ctx); err == nil {
		t.Fatal("stopped runtime ready")
	}
	if err = runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaConfigRejectsAmbiguityBeforeActivation(t *testing.T) {
	for _, raw := range []string{`{"version":"v1","Version":"v1","host":{}}`, `{"version":"v1","host":{},"secret":"not-supported"}`, `{"version":"v1","host":{}} {}`, `{"version":"v2","host":{}}`} {
		path := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadQuotaFile(path); err == nil {
			t.Fatal("ambiguous/unsupported configuration accepted", raw)
		}
	}
}

func TestDocumentedQuotaExampleLoadsAndActivates(t *testing.T) {
	spec, err := loadQuotaFile("../../examples/quota/request-count.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	host, err := api.NewQuotaHost(context.Background(), spec.Host, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = host.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(host.Engines()) != 1 || len(spec.Host.Bindings) != 3 {
		t.Fatal("documented model and tool bindings were lost")
	}
}

func TestBudgetsCostExampleLoadsAndActivates(t *testing.T) {
	spec, err := loadQuotaFile("../../examples/budgets/quota-cost.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	host, err := api.NewQuotaHost(context.Background(), spec.Host, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = host.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	meters := host.Engines()["development"].PolicyMeters()
	if meters["usd"].Unit != "money" || meters["usd"].Currency != "USD" || !meters["requests"].Billable {
		t.Fatalf("the example must define a USD money meter and a billable request meter: %+v", meters)
	}
}

func TestDeclaredProviderLimitsAreVersionAccountAndExpiryScoped(t *testing.T) {
	now := time.Now().UTC()
	e := providerLimitEvidence{ID: "verified-context-cap", TenantID: "tenant", BindingID: "model-binding", Provider: "google", Model: "model", ModelVersion: "model-001", BillingAccount: "project", Region: "global", InputCeiling: 1000, CombinedOutputCeiling: 100, SourceRef: "operator-verified-native-model-metadata", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	a, err := newDeclaredProviderLimits([]providerLimitEvidence{e}, now)
	if err != nil {
		t.Fatal(err)
	}
	r := api.QuotaBoundsRequest{TenantID: "tenant", BindingID: "model-binding", Model: "model", Target: quotaplex.Target{Provider: "google", ModelVersion: "model-001", BillingAccount: "project", Region: "global", ConfigurationDigest: strings.Repeat("a", 64)}, RequestedOutput: 20, At: now}
	proof, err := a.Bounds(context.Background(), r)
	if err != nil || proof.NativeBounds["uncached_input_tokens"] != 1000 || proof.NativeBounds["cached_tokens"] != 1000 || proof.NativeBounds["output_tokens"] != 20 || proof.NativeBounds["thought_tokens"] != 20 {
		t.Fatal(proof, err)
	}
	proof.NativeBounds["cached_tokens"] = 0
	if again, err := a.Bounds(context.Background(), r); err != nil || again.NativeBounds["cached_tokens"] != 1000 {
		t.Fatal("proof mutation changed authority", again, err)
	}
	for _, change := range []func(*api.QuotaBoundsRequest){func(r *api.QuotaBoundsRequest) { r.Target.ModelVersion = "other" }, func(r *api.QuotaBoundsRequest) { r.Target.BillingAccount = "other" }, func(r *api.QuotaBoundsRequest) { r.Target.Region = "other" }, func(r *api.QuotaBoundsRequest) { r.RequestedOutput = 101 }, func(r *api.QuotaBoundsRequest) { r.At = e.ExpiresAt }} {
		altered := r
		change(&altered)
		if _, err := a.Bounds(context.Background(), altered); err == nil {
			t.Fatal("unproven native exposure accepted", altered)
		}
	}
	if _, err := newDeclaredProviderLimits([]providerLimitEvidence{e, e}, now); err == nil {
		t.Fatal("duplicate limit authority accepted")
	}
}

func TestConfiguredActorEvidenceRemainsIndependentAndExpires(t *testing.T) {
	now := time.Now().UTC()
	subject := quotaplex.Subject{ID: "agent", Kind: "agent", Membership: quotaplex.Membership{Ref: contracts.SnapshotRef{ID: "directory-view", Version: "1"}, TenantID: "tenant", SubjectID: "agent", Groups: []quotaplex.GroupRef{{Source: "agent-directory", ID: "research-agents"}}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute)}}
	a, err := newConfiguredActorMembership([]quotaplex.Subject{subject}, now)
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.ResolveActor(context.Background(), "tenant", "agent", now)
	if err != nil {
		t.Fatal(err)
	}
	s.Membership.Groups[0].ID = "human-group"
	if fresh, err := a.ResolveActor(context.Background(), "tenant", "agent", now); err != nil || fresh.Membership.Groups[0].ID != "research-agents" {
		t.Fatal(fresh, err)
	}
	for _, args := range []struct {
		tenant, id string
		at         time.Time
	}{{"other", "agent", now}, {"tenant", "human", now}, {"tenant", "agent", subject.Membership.ExpiresAt}} {
		if _, err := a.ResolveActor(context.Background(), args.tenant, args.id, args.at); err == nil {
			t.Fatal("actor evidence was inferred or expired", args)
		}
	}
	for _, change := range []func(*quotaplex.Subject){
		func(s *quotaplex.Subject) { s.Membership.Groups = nil },
		func(s *quotaplex.Subject) { s.Membership.Ref.Version = "bad\x00version" },
		func(s *quotaplex.Subject) {
			s.Membership.Groups = []quotaplex.GroupRef{{Source: "bad\nsource", ID: "group"}}
		},
	} {
		invalid := subject
		change(&invalid)
		if _, err := newConfiguredActorMembership([]quotaplex.Subject{invalid}, now); err == nil {
			t.Fatal("incomplete or malformed actor evidence accepted", invalid)
		}
	}
	empty := subject
	empty.Membership.Groups = []quotaplex.GroupRef{}
	if _, err := newConfiguredActorMembership([]quotaplex.Subject{empty}, now); err != nil {
		t.Fatal("explicit complete empty membership rejected", err)
	}
}

func TestManagedToolWorkloadRouteDoesNotGrantAdministrativeAccess(t *testing.T) {
	for _, test := range []struct {
		method, path string
		want         bool
	}{{"POST", "/api/v1/mcp/connections/tool/invoke", true}, {"PUT", "/api/v1/mcp/connections/tool/invoke", false}, {"POST", "/api/v1/mcp/connections/tool/registration", false}, {"POST", "/api/v1/mcp/connections/a/b/invoke", false}, {"POST", "/api/v1/mcp/connections//invoke", false}} {
		r := httptest.NewRequest(test.method, test.path, nil)
		if got := nativeInvocation(r); got != test.want {
			t.Fatalf("%s %s workload=%t", test.method, test.path, got)
		}
	}
}

func TestCanonicalFirestoreEnvironmentIsUsedByQuotaStorage(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "legacy-project")
	t.Setenv("FIRESTORE_PROJECT_ID", "older-project")
	t.Setenv("FIRESTORE_DATABASE_ID", "legacy-db")
	project, db := firestoreConfiguration(&config.Config{GCPProject: "canonical-project", FirestoreDB: "canonical-db"})
	if project != "canonical-project" || db != "canonical-db" {
		t.Fatal(project, db)
	}
	t.Setenv("FIRESTORE_DATABASE", "(default)")
	if project, db = firestoreConfiguration(&config.Config{GCPProject: "canonical-project", FirestoreDB: "(default)"}); project != "canonical-project" || db != "(default)" {
		t.Fatal("canonical default was overridden", project, db)
	}
}
