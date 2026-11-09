package quotaplex

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// These measurements isolate the reference backend and accounting CPU/allocation
// cost. They do not include Firestore, identity lookups, provider I/O or queue wait.
func BenchmarkQuotaMemoryAdmission(b *testing.B) {
	for _, settle := range []bool{false, true} {
		name := "admit"
		if settle {
			name = "admit-claim-settle"
		}
		b.Run(name, func(b *testing.B) {
			now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
			policy := engineFixturePolicy(now)
			for i := range policy.Budgets {
				policy.Budgets[i].Limit = 1 << 60
			}
			policy.Rates[0].Rate = 1 << 60
			policy.Rates[0].Burst = 1 << 60
			policy.Concurrency[0].Limit = 1 << 60
			compiled, err := Compile(policy)
			if err != nil {
				b.Fatal(err)
			}
			engine, err := NewEngine(compiled, NewMemoryStore(), func() time.Time { return now })
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			if err = engine.Activate(ctx, ""); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				demand := engineFixtureDemand(now, "benchmark-"+strconv.Itoa(i), 10)
				decision, err := engine.Admit(ctx, demand)
				if err != nil || !decision.Allowed {
					b.Fatal(decision, err)
				}
				if settle {
					claim, err := engine.ClaimDispatch(ctx, "tenant", demand.AttemptID)
					if err != nil {
						b.Fatal(err)
					}
					if _, err = engine.Settle(ctx, Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "event-" + demand.AttemptID, Claim: claim, Usage: Vector{"requests": 1, "tokens": 7}}); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
func BenchmarkQuotaPolicyResolve(b *testing.B) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	compiled, err := Compile(engineFixturePolicy(now))
	if err != nil {
		b.Fatal(err)
	}
	demand := engineFixtureDemand(now, "benchmark", 10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		plan, err := compiled.Resolve(demand, now)
		if err != nil || plan.PayerID != "group-budget" {
			b.Fatal(plan, err)
		}
	}
}
