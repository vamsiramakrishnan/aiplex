package registry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func quotaEmulatorStores(t *testing.T) (*FirestoreStore, *FirestoreStore, string) {
	t.Helper()
	skipWithoutEmulator(t)
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	project := "aq-" + hex.EncodeToString(nonce[:])
	first, err := NewFirestoreStore(project, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFirestoreStore(project, "")
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	return first, second, project
}

// The native SDK retries conflicts itself. A still-aborted transaction is a
// fail-closed, retryable result; retry the identical operation/attempt identity.
func retryQuotaEmulator(ctx context.Context, fn func() error) error {
	for i := 0; i < 8; i++ {
		err := fn()
		if status.Code(err) != codes.Aborted {
			return err
		}
		timer := time.NewTimer(time.Duration(i+1) * 10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return fmt.Errorf("emulator contention retries exhausted")
}
func quotaEmulatorPolicy(now time.Time) quotaplex.Policy {
	return quotaplex.Policy{Version: quotaplex.Version, TenantID: "qualified-tenant", ID: "qualified-controls", Revision: 1, EffectiveAt: now.Add(-time.Hour),
		Meters:     map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}, "tokens": {ID: "tokens", Unit: "tokens", Billable: true}, "usd": {ID: "usd", Unit: "money", Currency: "USD", Scale: 6}},
		PriceBooks: []quotaplex.PriceBook{{ID: "native-prices", Revision: 1, Prices: []quotaplex.UnitPrice{{MeterID: "tokens", MoneyMeterID: "usd", Numerator: 3, Denominator: 2}}}},
		Accounts:   []quotaplex.Account{{ID: "engineering", TenantWide: true}},
		Budgets:    []quotaplex.BudgetRule{{ID: "account-tokens", Scope: quotaplex.Scope{Kind: "account"}, MeterID: "tokens", Limit: 60, Period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}, {ID: "tenant-usd", Scope: quotaplex.Scope{Kind: "tenant"}, MeterID: "usd", Limit: 90, Period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}},
		Rates:      []quotaplex.RateRule{{ID: "arrivals", Scope: quotaplex.Scope{Kind: "tenant"}, MeterID: "requests", Rate: 2, Interval: time.Second, Burst: 6, Basis: "admitted"}}, Concurrency: []quotaplex.ConcurrencyRule{{ID: "active", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 6}}}
}
func quotaEmulatorDemand(now time.Time, id string) quotaplex.Demand {
	ref := contracts.SnapshotRef{ID: "directory-membership", Version: "1"}
	return quotaplex.Demand{Version: quotaplex.Version, Context: contracts.RequestContext{Version: contracts.Version, TenantID: "qualified-tenant", RequestID: "transport-" + id, PrincipalID: "qualified-human", ResourceID: "qualified-model", MembershipSnapshotRef: &ref}, Principal: quotaplex.Subject{ID: "qualified-human", Kind: "human", Membership: quotaplex.Membership{Ref: ref, TenantID: "qualified-tenant", SubjectID: "qualified-human", Groups: []quotaplex.GroupRef{{Source: "directory", ID: "engineering"}}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}}, LogicalOperationID: "logical-" + id, AttemptID: id, PayloadDigest: strings.Repeat("a", 64), Target: quotaplex.Target{ConfigurationDigest: strings.Repeat("b", 64), Plane: "llmplex", Provider: "qualified", ResourceID: "qualified-model", Operation: "generate", PoolID: "qualified-pool", BillingAccount: "qualified-project", Region: "global", ModelVersion: "qualified-model-v1"}, RequireModelVersion: true, Bounds: quotaplex.Vector{"requests": 1, "tokens": 10}}
}

func TestQuotaEmulatorReplicasOutboxRestartAndOriginalPrices(t *testing.T) {
	first, second, project := quotaEmulatorStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	policy := quotaEmulatorPolicy(now)
	compiled, err := quotaplex.Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	makeEngine := func(store *FirestoreStore, c *quotaplex.CompiledPolicy) *quotaplex.Engine {
		e, err := quotaplex.NewEngine(c, store.QuotaStore(), clock)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	engines := []*quotaplex.Engine{makeEngine(first, compiled), makeEngine(second, compiled)}
	if err = engines[0].Activate(ctx, ""); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	var mu sync.Mutex
	var admitted []string
	jobs := make(chan int, 16)
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := range jobs {
				id := fmt.Sprintf("native-%02d", i)
				var decision quotaplex.Decision
				err := retryQuotaEmulator(ctx, func() error {
					var e error
					decision, e = engines[worker%2].Admit(ctx, quotaEmulatorDemand(now, id))
					return e
				})
				if err != nil {
					t.Error(err)
					continue
				}
				if decision.Allowed {
					accepted.Add(1)
					mu.Lock()
					admitted = append(admitted, id)
					mu.Unlock()
				}
			}
		}(worker)
	}
	for i := 0; i < 16; i++ {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	if accepted.Load() != 6 {
		t.Fatalf("atomic shared controls admitted %d, want 6", accepted.Load())
	}
	if len(admitted) == 0 {
		t.Fatal("no admitted attempt")
	}
	id := admitted[0]
	attempt, err := engines[0].GetAttempt(ctx, policy.TenantID, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range attempt.Plan.Budgets {
		balance, err := engines[1].BudgetBalance(ctx, policy.TenantID, budget.Key)
		want := int64(60)
		if budget.MeterID == "usd" {
			want = 90
		}
		if err != nil || balance.Held != want || balance.Consumed != 0 || balance.Available != 0 {
			t.Fatalf("shared balance %+v, error %v", balance, err)
		}
	}
	var claims atomic.Int64
	var claimMu sync.Mutex
	var claim quotaplex.DispatchClaim
	workers = sync.WaitGroup{}
	for _, engine := range engines {
		workers.Add(1)
		go func(e *quotaplex.Engine) {
			defer workers.Done()
			var result quotaplex.DispatchClaim
			err := retryQuotaEmulator(ctx, func() error { var err error; result, err = e.ClaimDispatch(ctx, policy.TenantID, id); return err })
			if err == nil {
				claims.Add(1)
				claimMu.Lock()
				claim = result
				claimMu.Unlock()
			} else if !errors.Is(err, quotaplex.ErrDispatchClaimed) {
				t.Error(err)
			}
		}(engine)
	}
	workers.Wait()
	if claims.Load() != 1 {
		t.Fatalf("claimed provider dispatch %d times", claims.Load())
	}
	if err = engines[0].MarkUnknown(ctx, claim, "provider response unavailable"); err != nil {
		t.Fatal(err)
	}
	if err = engines[1].Abort(ctx, policy.TenantID, id); !errors.Is(err, quotaplex.ErrInvalidTransition) {
		t.Fatal("uncertain dispatched work was released", err)
	}
	evidence := quotaplex.TerminalEvidence{Version: quotaplex.Version, ProducerID: "qualified-native-adapter", ConfigurationDigest: attempt.Demand.Target.ConfigurationDigest, ObservedModelVersion: "qualified-model-v1", RequireModelVersion: true, Settlement: quotaplex.Settlement{TenantID: policy.TenantID, AttemptID: id, EventID: "terminal-" + id, Claim: claim, Usage: quotaplex.Vector{"requests": 1, "tokens": 7}, ModelVersion: "qualified-model-v1"}}
	ack, err := engines[0].EnqueueTerminal(ctx, evidence)
	if err != nil || ack.Status != quotaplex.UsagePending {
		t.Fatal(ack, err)
	}
	// A policy upgrade and replacement client/engine must retain original billing
	// and durable terminal facts. No provider submission is part of recovery.
	policy.Revision = 2
	policy.PriceBooks[0].Revision = 2
	policy.PriceBooks[0].Prices[0].Numerator = 30
	upgraded, err := quotaplex.Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = engines[1].Ready(ctx); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	_ = second.Close()
	restarted, err := NewFirestoreStore(project, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	engine := makeEngine(restarted, upgraded)
	if err = engine.Activate(ctx, compiled.Reference().Digest); err != nil {
		t.Fatal(err)
	}
	recovery, err := quotaplex.NewRecovery(engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := recovery.RunOnce(ctx, quotaplex.RecoveryPage{TenantID: policy.TenantID, Limit: 32})
	if err != nil || report.Settled != 1 || report.Failed != 0 {
		t.Fatal(report, err)
	}
	actual, err := engine.GetAttempt(ctx, policy.TenantID, id)
	if err != nil || actual.Decision.State != "settled" || actual.Actual["tokens"] != 7 || actual.Actual["usd"] != 11 {
		t.Fatalf("original-price settlement %+v, error %v", actual, err)
	}
	if _, err = engine.ProcessTerminal(ctx, policy.TenantID, evidence.Settlement.EventID); err != nil {
		t.Fatal("settlement replay", err)
	}
	for _, budget := range actual.Plan.Budgets {
		balance, err := engine.BudgetBalance(ctx, policy.TenantID, budget.Key)
		held, consumed := int64(50), int64(7)
		if budget.MeterID == "usd" {
			held, consumed = 75, 11
		}
		if err != nil || balance.Held != held || balance.Consumed != consumed {
			t.Fatal("recovery charged twice or lost another hold", balance, err)
		}
	}
	if _, err = engine.GetAttempt(ctx, "another-tenant", id); err == nil {
		t.Fatal("tenant isolation failed")
	}
}

func TestQuotaEmulatorConfigurationCASAndAtomicRollback(t *testing.T) {
	first, second, _ := quotaEmulatorStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	raw, _ := json.Marshal(models.ProviderConfig{Provider: "qualified-provider"})
	initial, err := first.PutConfiguration(ctx, ConfigurationRecord{Kind: ConfigProvider, ID: "qualified-provider", Spec: raw}, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := first
			if i%2 == 1 {
				store = second
			}
			err := retryQuotaEmulator(ctx, func() error { _, err := store.PutConfiguration(ctx, initial, initial.Revision, -1); return err })
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrConfigurationConflict) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("CAS had %d winners", winners.Load())
	}
	current, err := second.GetConfiguration(ctx, ConfigProvider, initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.DeleteConfiguration(ctx, ConfigProvider, current.ID, initial.Revision, -1); !errors.Is(err, ErrConfigurationConflict) {
		t.Fatal("stale deletion accepted", err)
	}
	if err = second.DeleteConfiguration(ctx, ConfigProvider, current.ID, current.Revision, -1); err != nil {
		t.Fatal(err)
	}
	recreated, err := first.PutConfiguration(ctx, initial, 0, -1)
	if err != nil || recreated.Revision <= current.Revision {
		t.Fatal("tombstone revision reused", recreated, err)
	}
	generation, err := first.ConfigurationGeneration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = second.ModifyUserScopes(ctx, "qualified-human", func(existing []string) ([]string, error) { return append(existing, "llm:model:qualified"), nil }); err != nil {
		t.Fatal(err)
	}
	route, _ := json.Marshal(models.LLMRouteConfig{ModelID: "qualified-route"})
	if _, err = first.PutConfiguration(ctx, ConfigurationRecord{Kind: ConfigRoute, ID: "qualified-route", Spec: route}, 0, generation); !errors.Is(err, ErrConfigurationConflict) {
		t.Fatal("dependency generation changed without rejection", err)
	}
	rollback := errors.New("qualification rollback")
	if err = first.QuotaStore().Update(ctx, "rollback-tenant", func(tx quotaplex.Tx) error {
		if err := tx.Create("proof", "must-not-exist", map[string]int{"value": 1}); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if err = second.QuotaStore().View(ctx, "rollback-tenant", func(tx quotaplex.Tx) error {
		var result map[string]int
		return tx.Get("proof", "must-not-exist", &result)
	}); !errors.Is(err, quotaplex.ErrNotFound) {
		t.Fatal("failed transaction committed staged write", err)
	}
}
