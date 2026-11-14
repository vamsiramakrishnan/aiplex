package quotaplex

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// unknownFixture admits and dispatches attempt id at the engine's current
// time and marks it unknown, as a lost upstream response does.
func unknownFixture(t *testing.T, engine *Engine, d Demand) {
	t.Helper()
	if decision := engineAdmit(t, engine, d); !decision.Allowed {
		t.Fatal(decision)
	}
	claim, err := engine.ClaimDispatch(context.Background(), "tenant", d.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.MarkUnknown(context.Background(), claim, "lost upstream response"); err != nil {
		t.Fatal(err)
	}
}

func exposureFor(t *testing.T, u *UnknownExposure, meter string) HeldExposure {
	t.Helper()
	for _, e := range u.Exposure {
		if e.MeterID == meter {
			return e
		}
	}
	t.Fatalf("no exposure for meter %q in %+v", meter, u.Exposure)
	return HeldExposure{}
}

func roomyPolicy(now time.Time) Policy {
	policy := engineFixturePolicy(now)
	policy.Rates[0].Burst, policy.Rates[0].Rate = 100, 100
	policy.Concurrency[0].Limit = 100
	return policy
}

func TestRecoveryReportsUnknownAttemptsAgeAndExposure(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }
	engine := engineFixture(t, roomyPolicy(base), clock, NewMemoryStore())
	engineActivate(t, engine, "")
	worker, err := NewRecovery(engine, nil)
	if err != nil {
		t.Fatal(err)
	}

	// None: the report says so explicitly (zero attempts, no oldest time).
	report, err := worker.RecoverAttempts(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
	if err != nil || report.Unknown == nil || report.Unknown.Attempts != 0 || report.Unknown.OldestDispatchAt != nil || len(report.Unknown.Exposure) != 0 {
		t.Fatalf("an empty tenant reports zero unknown attempts: %+v %v", report.Unknown, err)
	}

	unknownFixture(t, engine, engineFixtureDemand(base, "old", 10))
	now.Store(base.Add(50 * time.Minute).UnixNano())
	unknownFixture(t, engine, engineFixtureDemand(base, "young", 20))
	// An attempt still dispatching and a settled one are not unknown.
	if d := engineAdmit(t, engine, engineFixtureDemand(base, "pending", 5)); !d.Allowed {
		t.Fatal(d)
	}
	now.Store(base.Add(2 * time.Hour).UnixNano())

	report, err = worker.RecoverAttempts(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
	if err != nil {
		t.Fatal(err)
	}
	u := report.Unknown
	if u == nil || u.Attempts != 2 || u.Unreadable != 0 || u.UndatedAttempts != 0 {
		t.Fatalf("%+v", u)
	}
	if u.OldestDispatchAt == nil || !u.OldestDispatchAt.Equal(base) || !u.AsOf.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("oldest %v as of %v", u.OldestDispatchAt, u.AsOf)
	}
	if age, ok := u.OldestAge(); !ok || age != 2*time.Hour {
		t.Fatal(age, ok)
	}
	tokens := exposureFor(t, u, "tokens")
	if tokens.Unit != "tokens" || tokens.Quantity == nil || *tokens.Quantity != 30 || tokens.KnownPart != 30 || tokens.UncomputedAttempts != 0 || tokens.Attempts != 2 {
		t.Fatalf("tokens %+v", tokens)
	}
	// Money is reported per meter with its currency and scale, never folded
	// into another meter: 30 tokens at 3/2 micro-USD.
	usd := exposureFor(t, u, "usd")
	if usd.Unit != "money" || usd.Currency != "USD" || usd.Scale != 6 || usd.Quantity == nil || *usd.Quantity != 45 {
		t.Fatalf("usd %+v", usd)
	}
	if requests := exposureFor(t, u, "requests"); requests.Quantity == nil || *requests.Quantity != 2 {
		t.Fatalf("requests %+v", requests)
	}

	// Pages accumulate into the same summary as one scan.
	first, err := worker.RecoverAttempts(ctx, RecoveryPage{TenantID: "tenant", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	total := *first.Unknown
	after := first.NextAfterID
	for !first.Done {
		first, err = worker.RecoverAttempts(ctx, RecoveryPage{TenantID: "tenant", AfterID: after, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		after = first.NextAfterID
		total.Merge(*first.Unknown)
	}
	if total.Attempts != 2 || !total.OldestDispatchAt.Equal(base) || *exposureFor(t, &total, "tokens").Quantity != 30 {
		t.Fatalf("paged %+v", total)
	}
}

func TestRecoveryReportsUncomputableExposureAsUnknown(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	policy := costPolicy(base)
	policy.Meters["extra"] = Meter{ID: "extra", Unit: "tokens", Billable: true}
	engine := drawEngine(t, &now, policy)
	flag := BudgetDraw{ID: "cost-flag", Unit: DrawCost, Currency: "USD", Scale: 6, Limit: 1000, Period: drawPeriod(base), OnUnknownCost: UnknownCostAllowAndFlag}
	// A separate draw for the priced request: one allow_and_flag draw shared
	// by priced and unpriced requests is refused by loadBudget today.
	deny := BudgetDraw{ID: "cost-deny", Unit: DrawCost, Currency: "USD", Scale: 6, Limit: 1000, Period: drawPeriod(base)}
	priced := engineFixtureDemand(base, "priced", 100)
	priced.Budgets = []BudgetDraw{deny}
	unknownFixture(t, engine, priced)
	// A billable meter without a price: cost cannot be computed and is
	// admitted only because the budget says allow_and_flag.
	unpriced := engineFixtureDemand(base, "unpriced", 10)
	unpriced.Bounds["extra"] = 5
	unpriced.Budgets = []BudgetDraw{flag}
	unknownFixture(t, engine, unpriced)
	// A record the worker cannot read must not vanish from the summary.
	if err := engine.store.Update(ctx, "tenant", func(tx Tx) error {
		return tx.Put(attemptKind, "zz-corrupt", map[string]string{"fingerprint": "x"})
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := NewRecovery(engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := worker.RecoverAttempts(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
	if err != nil {
		t.Fatal(err)
	}
	u := report.Unknown
	if u.Attempts != 2 || u.Unreadable != 1 || u.Complete() {
		t.Fatalf("%+v", u)
	}
	usd := exposureFor(t, u, "usd")
	if usd.Quantity != nil || usd.UncomputedAttempts != 1 || usd.KnownPart != 150 || usd.Attempts != 2 {
		t.Fatalf("unpriced cost must be unknown, never 0 and never just the priced part: %+v", usd)
	}
	raw, _ := json.Marshal(usd)
	if !strings.Contains(string(raw), `"quantity":null`) {
		t.Fatalf("unknown quantity must serialize as null: %s", raw)
	}
	if tokens := exposureFor(t, u, "tokens"); tokens.Quantity == nil || *tokens.Quantity != 110 {
		t.Fatalf("known raw meters stay known: %+v", tokens)
	}
}

// Admission can flag a cost as unknown while the plan still holds a money
// bound: an unpriced billable meter bounded at 0 passes PriceUsage but not
// drawCostPriced. The bound is then reported as a known part, not exact.
func TestFlaggedCostWithAMoneyBoundIsNotExact(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	policy := costPolicy(base)
	policy.Meters["extra"] = Meter{ID: "extra", Unit: "tokens", Billable: true}
	engine := drawEngine(t, &now, policy)
	flag := BudgetDraw{ID: "cost-flag", Unit: DrawCost, Currency: "USD", Scale: 6, Limit: 1000, Period: drawPeriod(base), OnUnknownCost: UnknownCostAllowAndFlag}
	d := engineFixtureDemand(base, "zero-extra", 100)
	d.Bounds["extra"] = 0
	d.Budgets = []BudgetDraw{flag}
	unknownFixture(t, engine, d)
	info, err := engine.GetAttempt(ctx, "tenant", "zero-extra")
	if err != nil || info.Plan.Bounds["usd"] != 150 || len(info.Plan.Budgets) != 1 || !info.Plan.Budgets[0].UnknownCost {
		t.Fatalf("admission no longer produces the combination this test pins: %+v %v", info.Plan, err)
	}
	worker, err := NewRecovery(engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := worker.RecoverAttempts(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
	if err != nil {
		t.Fatal(err)
	}
	usd := exposureFor(t, report.Unknown, "usd")
	if usd.Quantity != nil || usd.KnownPart != 150 || usd.UncomputedAttempts != 1 || usd.Attempts != 1 {
		t.Fatalf("a flagged cost must not be reported exact: %+v", usd)
	}
}

func TestUnknownExposureMergeNeverWrapsOrDropsUnknowns(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	big := HeldExposure{MeterID: "tokens", Unit: "tokens", KnownPart: math.MaxInt64 - 1, Attempts: 1}
	a := UnknownExposure{Attempts: 1, OldestDispatchAt: &at, Exposure: []HeldExposure{big}}
	a.normalize()
	later := at.Add(time.Hour)
	b := UnknownExposure{Attempts: 1, OldestDispatchAt: &later, UndatedAttempts: 1, Exposure: []HeldExposure{{MeterID: "tokens", Unit: "tokens", KnownPart: 5, Attempts: 1}}}
	b.normalize()
	a.Merge(b)
	e := a.Exposure[0]
	if !e.Overflow || e.Quantity != nil || e.KnownPart != math.MaxInt64-1 || a.Attempts != 2 || !a.OldestDispatchAt.Equal(at) || a.UndatedAttempts != 1 {
		t.Fatalf("%+v %+v", a, e)
	}
	if _, ok := a.OldestAge(); ok {
		t.Fatal("an undated unknown attempt makes the oldest age unknown")
	}
	if a.Complete() {
		t.Fatal("overflowed or undated summaries are incomplete")
	}
}

func TestInventoryOlderThanFiltersUnknownAttempts(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }
	engine := engineFixture(t, roomyPolicy(base), clock, NewMemoryStore())
	engineActivate(t, engine, "")
	unknownFixture(t, engine, engineFixtureDemand(base, "old", 10))
	now.Store(base.Add(90 * time.Minute).UnixNano())
	unknownFixture(t, engine, engineFixtureDemand(base.Add(90*time.Minute), "young", 20))
	if d := engineAdmit(t, engine, engineFixtureDemand(base.Add(90*time.Minute), "admitted", 5)); !d.Allowed {
		t.Fatal(d)
	}
	now.Store(base.Add(2 * time.Hour).UnixNano())

	page, err := engine.InventoryUnknown(ctx, "tenant", time.Hour, "", 20)
	if err != nil || page.Kind != "attempts" || len(page.Records) != 1 || page.Records[0].ID != "old" || !page.Done {
		t.Fatal(len(page.Records), page.Done, err)
	}
	if page.OlderThan != "1h0m0s" || page.Unknown == nil || page.Unknown.Attempts != 1 || *exposureFor(t, page.Unknown, "tokens").Quantity != 10 {
		t.Fatalf("%+v", page.Unknown)
	}
	all, err := engine.InventoryUnknown(ctx, "tenant", 0, "", 20)
	if err != nil || len(all.Records) != 2 || all.Unknown.Attempts != 2 {
		t.Fatal(len(all.Records), err)
	}
	none, err := engine.InventoryUnknown(ctx, "tenant", 3*time.Hour, "", 20)
	if err != nil || len(none.Records) != 0 || none.Unknown.Attempts != 0 || none.Unknown.OldestDispatchAt != nil {
		t.Fatal(len(none.Records), err)
	}
	// Paging continues after the last scanned record, matched or not.
	first, err := engine.InventoryUnknown(ctx, "tenant", time.Hour, "", 1)
	if err != nil || first.Done || first.NextAfterID != "admitted" || len(first.Records) != 0 {
		t.Fatal(first.Done, first.NextAfterID, len(first.Records), err)
	}
	for _, bad := range []time.Duration{-time.Second} {
		if _, err := engine.InventoryUnknown(ctx, "tenant", bad, "", 20); err == nil {
			t.Fatal("negative age accepted")
		}
	}
	if _, err := engine.InventoryUnknown(ctx, "tenant", time.Hour, "", 65); err == nil {
		t.Fatal("unbounded page accepted")
	}
	if _, err := engine.InventoryUnknown(ctx, "other", time.Hour, "", 20); err == nil {
		t.Fatal("cross-tenant inventory accepted")
	}
}
