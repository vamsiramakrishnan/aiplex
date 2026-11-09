package quotaplex

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

func quotaPolicyFixture() (Policy, Demand, time.Time) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engineering := GroupRef{Source: "workforce", ID: "engineering"}
	research := GroupRef{Source: "workforce", ID: "research"}
	agents := GroupRef{Source: "workload", ID: "research-agents"}
	ref := contracts.SnapshotRef{ID: "human-membership", Version: "7"}
	demand := Demand{Version: Version, Context: contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: "correlation", PrincipalID: "human", ActorID: "agent", MembershipSnapshotRef: &ref, ResourceID: "models/flash"}, Principal: Subject{ID: "human", Kind: "human", Membership: Membership{Ref: ref, TenantID: "tenant", SubjectID: "human", Groups: []GroupRef{engineering, research}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}}, Actor: &Subject{ID: "agent", Kind: "agent", Membership: Membership{Ref: contracts.SnapshotRef{ID: "agent-membership", Version: "3"}, TenantID: "tenant", SubjectID: "agent", Groups: []GroupRef{agents}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}}, LogicalOperationID: "logical", AttemptID: "attempt", PayloadDigest: strings.Repeat("a", 64), Target: Target{ConfigurationDigest: strings.Repeat("c", 64), Plane: "llmplex", Provider: "google", ResourceID: "models/flash", Operation: "generate", PoolID: "capacity", BillingAccount: "project", Region: "us-central1", ModelVersion: "flash-001"}, Bounds: Vector{"input": 100, "output": 20, "requests": 1}}
	fixed := Period{Kind: "fixed", Start: now.Add(-24 * time.Hour), End: now.Add(24 * time.Hour)}
	policy := Policy{Version: Version, TenantID: "tenant", ID: "spending", Revision: 1, EffectiveAt: now.Add(-time.Hour), Meters: map[string]Meter{"input": {ID: "input", Unit: "tokens", Billable: true}, "output": {ID: "output", Unit: "tokens", Billable: true}, "requests": {ID: "requests", Unit: "requests"}, "usd": {ID: "usd", Unit: "money", Currency: "USD", Scale: 6}}, Accounts: []Account{{ID: "engineering", Priority: 20, Eligible: []Eligibility{{SubjectRole: "principal", Group: &engineering}}}, {ID: "research", Priority: 10, Eligible: []Eligibility{{SubjectRole: "principal", Group: &research}}}, {ID: "agent-funding", Priority: 5, Eligible: []Eligibility{{SubjectRole: "actor", Group: &agents}}}}, PriceBooks: []PriceBook{{ID: "google-flash", Revision: 8, Match: Match{Provider: "google", ResourceID: "models/flash", Operation: "generate", BillingAccount: "project", Region: "us-central1", ModelVersion: "flash-001"}, Prices: []UnitPrice{{MeterID: "input", MoneyMeterID: "usd", Numerator: 1, Denominator: 10}, {MeterID: "output", MoneyMeterID: "usd", Numerator: 3, Denominator: 10}}}}, Budgets: []BudgetRule{{ID: "engineering", Scope: Scope{Kind: "account", ID: "engineering"}, MeterID: "input", Limit: 1000, Period: Period{Kind: "anchored", Anchor: now.Add(-time.Hour), Duration: 7 * 24 * time.Hour}}, {ID: "research", Scope: Scope{Kind: "account", ID: "research"}, MeterID: "input", Limit: 500, Period: Period{Kind: "anchored", Anchor: now.Add(-time.Hour), Duration: 30 * 24 * time.Hour}}, {ID: "tenant", Scope: Scope{Kind: "tenant"}, MeterID: "input", Limit: 5000, Period: fixed}, {ID: "human", Scope: Scope{Kind: "principal"}, MeterID: "output", Limit: 100, Period: fixed}, {ID: "agent", Scope: Scope{Kind: "actor"}, MeterID: "output", Limit: 30, Period: fixed}, {ID: "engineering-group", Scope: Scope{Kind: "group", SubjectRole: "principal", Group: &engineering}, MeterID: "input", Limit: 2000, Period: fixed}, {ID: "money", Scope: Scope{Kind: "tenant"}, MeterID: "usd", Limit: 1000000, Period: fixed}}, Rates: []RateRule{{ID: "requests", Scope: Scope{Kind: "principal"}, MeterID: "requests", Rate: 5, Interval: time.Second, Burst: 10, Basis: "admitted"}}, Concurrency: []ConcurrencyRule{{ID: "tenant", Scope: Scope{Kind: "tenant"}, Limit: 3}, {ID: "agent", Scope: Scope{Kind: "actor"}, Limit: 1}}}
	return policy, demand, now
}

func quotaPolicyCompile(t *testing.T, policy Policy) *CompiledPolicy {
	t.Helper()
	compiled, err := Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func quotaBudgetByRule(t *testing.T, plan Plan, rule string) BudgetLimit {
	t.Helper()
	for _, budget := range plan.Budgets {
		if budget.RuleID == rule {
			return budget
		}
	}
	t.Fatalf("missing budget %s", rule)
	return BudgetLimit{}
}

func quotaExpectResolveCode(t *testing.T, compiled *CompiledPolicy, demand Demand, now time.Time, code string) {
	t.Helper()
	_, err := compiled.Resolve(demand, now)
	var denied *ResolveError
	if !errors.As(err, &denied) || denied.Code != code {
		t.Fatalf("wanted %s; got %v", code, err)
	}
}

func TestPolicyUnequalAccountsPeriodsAndSimultaneousSubjectCaps(t *testing.T) {
	policy, demand, now := quotaPolicyFixture()
	compiled := quotaPolicyCompile(t, policy)
	plan, err := compiled.Resolve(demand, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PayerID != "engineering" || plan.Bounds["usd"] != 16 || len(plan.Budgets) != 6 || len(plan.Rates) != 1 || len(plan.Concurrency) != 2 {
		t.Fatalf("unexpected funding/control plan: %+v", plan)
	}
	engineering := quotaBudgetByRule(t, plan, "engineering")
	if engineering.Limit != 1000 || engineering.Demand != 100 || engineering.Window.End.Sub(engineering.Window.Start) != 7*24*time.Hour {
		t.Fatal("engineering allocation changed", engineering)
	}
	for _, rule := range []string{"human", "agent"} {
		if quotaBudgetByRule(t, plan, rule).Demand != 20 {
			t.Fatalf("%s constraint missing", rule)
		}
	}
	for _, budget := range plan.Budgets {
		if budget.RuleID == "research" {
			t.Fatal("all group funding was implicitly combined")
		}
	}
	demand.FundingAccountID = "research"
	researchPlan, err := compiled.Resolve(demand, now)
	if err != nil {
		t.Fatal(err)
	}
	research := quotaBudgetByRule(t, researchPlan, "research")
	if researchPlan.PayerID != "research" || research.Limit != 500 || research.Window.End.Sub(research.Window.Start) != 30*24*time.Hour {
		t.Fatalf("research allocation lost its own period: %+v", research)
	}
	if quotaBudgetByRule(t, researchPlan, "human").Key != quotaBudgetByRule(t, plan, "human").Key {
		t.Fatal("selecting another payer bypassed the human cap")
	}
}

func TestPolicyFundingRequiresExactSourceAndSeparateActorMembership(t *testing.T) {
	policy, demand, now := quotaPolicyFixture()
	compiled := quotaPolicyCompile(t, policy)
	demand.FundingAccountID = "research"
	demand.Principal.Membership.Groups = []GroupRef{{Source: "another-directory", ID: "research"}}
	demand.Actor.Membership.Groups = append(demand.Actor.Membership.Groups, GroupRef{Source: "workforce", ID: "research"})
	quotaExpectResolveCode(t, compiled, demand, now, "funding_unavailable")
	demand.FundingAccountID = "agent-funding"
	if _, err := compiled.Resolve(demand, now); err != nil {
		t.Fatal("verified actor eligibility rejected", err)
	}
	demand.Actor.Membership = demand.Principal.Membership
	if _, err := compiled.Resolve(demand, now); err == nil {
		t.Fatal("principal snapshot substituted for actor")
	}
}

func TestPolicyFundingTiesRequireSelectionAndCannotUseUnknownAccount(t *testing.T) {
	policy, demand, now := quotaPolicyFixture()
	policy.Accounts[1].Priority = 20
	compiled := quotaPolicyCompile(t, policy)
	quotaExpectResolveCode(t, compiled, demand, now, "funding_ambiguous")
	demand.FundingAccountID = "engineering"
	if _, err := compiled.Resolve(demand, now); err != nil {
		t.Fatal(err)
	}
	demand.FundingAccountID = "other-tenant-account"
	quotaExpectResolveCode(t, compiled, demand, now, "funding_unavailable")
}

func TestPolicyFreshnessAndTrustedContextBinding(t *testing.T) {
	for _, role := range []string{"principal", "actor"} {
		t.Run(role, func(t *testing.T) {
			policy, demand, now := quotaPolicyFixture()
			if role == "principal" {
				demand.Principal.Membership.ExpiresAt = now
			} else {
				demand.Actor.Membership.ExpiresAt = now
			}
			quotaExpectResolveCode(t, quotaPolicyCompile(t, policy), demand, now, "membership_invalid")
		})
	}
	for _, mutate := range []func(*Demand){func(d *Demand) { d.Context.PrincipalID = "another" }, func(d *Demand) { d.Context.ResourceID = "another" }, func(d *Demand) { d.Context.MembershipSnapshotRef = nil }, func(d *Demand) { d.Actor = nil }, func(d *Demand) { d.Principal.Membership.TenantID = "another" }, func(d *Demand) { d.PayloadDigest = "caller-label" }} {
		_, demand, _ := quotaPolicyFixture()
		mutate(&demand)
		if err := ValidateDemand(demand); err == nil {
			t.Fatal("unbound demand accepted", demand)
		}
	}
}

func TestPolicyImmutableSnapshotAndDeterministicDeclarationOrder(t *testing.T) {
	policy, demand, now := quotaPolicyFixture()
	compiled := quotaPolicyCompile(t, policy)
	reference := compiled.Reference()
	reordered := policyCopy(policy)
	for i, j := 0, len(reordered.Accounts)-1; i < j; i, j = i+1, j-1 {
		reordered.Accounts[i], reordered.Accounts[j] = reordered.Accounts[j], reordered.Accounts[i]
	}
	for i, j := 0, len(reordered.Budgets)-1; i < j; i, j = i+1, j-1 {
		reordered.Budgets[i], reordered.Budgets[j] = reordered.Budgets[j], reordered.Budgets[i]
	}
	reordered.PriceBooks[0].Prices[0], reordered.PriceBooks[0].Prices[1] = reordered.PriceBooks[0].Prices[1], reordered.PriceBooks[0].Prices[0]
	if quotaPolicyCompile(t, reordered).Reference() != reference {
		t.Fatal("declaration order changed the immutable policy identity")
	}
	policy.Accounts[0].Eligible[0].Group.ID = "mutated"
	policy.PriceBooks[0].Prices[0].Numerator = 999
	policy.Meters["input"] = Meter{ID: "input", Unit: "bytes"}
	copy := compiled.Policy()
	copy.Budgets[0].Limit = 0
	copy.Accounts[0].Eligible[0].Group.ID = "mutated-again"
	lineages := compiled.Lineages()
	for key := range lineages {
		lineages[key] = "mutated"
	}
	plan, err := compiled.Resolve(demand, now)
	if err != nil {
		t.Fatal(err)
	}
	plan.Bounds["input"] = 999
	plan.Meters["input"] = Meter{ID: "input", Unit: "bytes"}
	plan.Prices[0].Numerator = 999
	again, err := compiled.Resolve(demand, now)
	if err != nil || again.Bounds["usd"] != 16 || again.Bounds["input"] != 100 || again.Meters["input"].Unit != "tokens" || compiled.Reference() != reference {
		t.Fatalf("compiled snapshot was mutated: %+v %v", again, err)
	}
}

func TestPolicyRevisionsPreserveStateKeysAndGuardStructuralChanges(t *testing.T) {
	policy, demand, now := quotaPolicyFixture()
	first := quotaPolicyCompile(t, policy)
	original, err := first.Resolve(demand, now)
	if err != nil {
		t.Fatal(err)
	}
	policy.Revision++
	policy.Budgets[0].Limit = 99
	policy.Concurrency[0].Limit = 1
	second := quotaPolicyCompile(t, policy)
	changed, err := second.Resolve(demand, now)
	if err != nil {
		t.Fatal(err)
	}
	oldBudget, newBudget := quotaBudgetByRule(t, original, "engineering"), quotaBudgetByRule(t, changed, "engineering")
	if oldBudget.Key != newBudget.Key || oldBudget.LineageKey != newBudget.LineageKey || oldBudget.Shape != newBudget.Shape || newBudget.Limit != 99 {
		t.Fatal("quantity revision resets or reinterprets budget", oldBudget, newBudget)
	}
	policy.Revision++
	policy.Budgets[0].Period.Duration = 8 * 24 * time.Hour
	third := quotaPolicyCompile(t, policy)
	next, err := third.Resolve(demand, now)
	if err != nil {
		t.Fatal(err)
	}
	nextBudget := quotaBudgetByRule(t, next, "engineering")
	if oldBudget.LineageKey != nextBudget.LineageKey || oldBudget.Shape == nextBudget.Shape {
		t.Fatal("period change is not caught by a stable lineage guard")
	}
	policy.Rates[0].Burst++
	if third.Lineages()[policyLineage(policy.ID, "rate", "requests")] == quotaPolicyCompile(t, policy).Lineages()[policyLineage(policy.ID, "rate", "requests")] {
		t.Fatal("rate parameter migration was silently permitted")
	}
}

func TestPolicyExactPriceCoordinatesAndMissingExposure(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Policy, *Demand)
		code string
	}{
		{"billing account", func(_ *Policy, d *Demand) { d.Target.BillingAccount = "other-project" }, "unpriced_meter"},
		{"region", func(_ *Policy, d *Demand) { d.Target.Region = "europe-west1" }, "unpriced_meter"},
		{"model version", func(_ *Policy, d *Demand) { d.Target.ModelVersion = "flash-002" }, "unpriced_meter"},
		{"overlapping books", func(p *Policy, _ *Demand) {
			book := policyCopy(p.PriceBooks[0])
			book.ID = "generic"
			book.Match = Match{}
			p.PriceBooks = append(p.PriceBooks, book)
		}, "pricebook_ambiguous"},
		{"missing billable price even at zero bound", func(p *Policy, d *Demand) {
			p.PriceBooks[0].Prices = p.PriceBooks[0].Prices[:1]
			d.Bounds["output"] = 0
		}, "unpriced_meter"},
		{"missing bounded input", func(_ *Policy, d *Demand) { delete(d.Bounds, "input") }, "unsupported_meter"},
		{"missing request meter", func(_ *Policy, d *Demand) { delete(d.Bounds, "requests") }, "unsupported_meter"},
		{"zero request meter", func(_ *Policy, d *Demand) { d.Bounds["requests"] = 0 }, "unsupported_meter"},
		{"untrusted money input", func(_ *Policy, d *Demand) { d.Bounds["usd"] = 0 }, "unsupported_meter"},
		{"unknown raw meter", func(_ *Policy, d *Demand) { d.Bounds["another"] = 1 }, "unsupported_meter"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, demand, now := quotaPolicyFixture()
			test.edit(&policy, &demand)
			quotaExpectResolveCode(t, quotaPolicyCompile(t, policy), demand, now, test.code)
		})
	}
	policy, demand, now := quotaPolicyFixture()
	demand.Bounds["output"] = 0
	plan, err := quotaPolicyCompile(t, policy).Resolve(demand, now)
	if err != nil {
		t.Fatal(err)
	}
	if quotaBudgetByRule(t, plan, "human").Demand != 0 || quotaBudgetByRule(t, plan, "agent").Demand != 0 {
		t.Fatal("zero exposure lost matched settlement projections")
	}
}

func TestPolicyPopulationAllocationIsExplicitFrozenAndGroupBound(t *testing.T) {
	policy, demand, now := quotaPolicyFixture()
	rule := &policy.Budgets[0]
	rule.Limit = 0
	rule.Population = &PopulationAllocation{Ref: contracts.SnapshotRef{ID: "engineering-population", Version: "42"}, Group: *policy.Accounts[0].Eligible[0].Group, Count: 3, PerMember: 17, ObservedAt: now.Add(-time.Hour)}
	compiled := quotaPolicyCompile(t, policy)
	plan, err := compiled.Resolve(demand, now)
	if err != nil {
		t.Fatal(err)
	}
	if quotaBudgetByRule(t, plan, "engineering").Limit != 51 {
		t.Fatal("explicit population allocation not applied")
	}
	demand.Principal.Membership.Groups = append(demand.Principal.Membership.Groups, GroupRef{Source: "unrelated", ID: "large-group"})
	plan, err = compiled.Resolve(demand, now)
	if err != nil || quotaBudgetByRule(t, plan, "engineering").Limit != 51 {
		t.Fatal("request group count changed a frozen allocation", err)
	}
	policy.Budgets[0].Population.Group.ID = "research"
	if _, err := Compile(policy); err == nil {
		t.Fatal("unrelated population group funded an account")
	}
	policy.Budgets[0].Population.Group.ID = "engineering"
	policy.Budgets[0].Population.Count = math.MaxInt64
	policy.Budgets[0].Population.PerMember = 2
	if _, err := Compile(policy); !errors.Is(err, ErrQuantityOverflow) {
		t.Fatal("population overflow accepted", err)
	}
}

func TestPolicyRejectsMalformedConfigurationBeforeAdmission(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Policy)
	}{
		{"unknown meter", func(p *Policy) { p.Budgets[0].MeterID = "unknown" }},
		{"duplicate rule", func(p *Policy) { p.Budgets = append(p.Budgets, p.Budgets[0]) }},
		{"implicit account access", func(p *Policy) { p.Accounts[0].Eligible = nil }},
		{"mixed tenant access", func(p *Policy) { p.Accounts[0].TenantWide = true }},
		{"unqualified group", func(p *Policy) { p.Accounts[0].Eligible[0].Group.Source = "" }},
		{"ambiguous eligibility", func(p *Policy) { p.Accounts[0].Eligible[0].SubjectID = "human" }},
		{"unsupported rate basis", func(p *Policy) { p.Rates[0].Basis = "observed" }},
		{"wildcard selector", func(p *Policy) { p.Budgets[0].Match.ResourceID = "models/*" }},
		{"unknown scoped account", func(p *Policy) { p.Budgets[0].Scope.ID = "missing" }},
		{"unsafe time", func(p *Policy) { p.EffectiveAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"rolling unsupported", func(p *Policy) { p.Budgets[0].Period = Period{Kind: "rolling", Duration: 24 * time.Hour} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy, _, _ := quotaPolicyFixture()
			test.edit(&policy)
			if _, err := Compile(policy); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestPolicySharingRequiresExplicitCompatibleAccountsAndStableLineage(t *testing.T) {
	base, _, _ := quotaPolicyFixture()
	base.Sharing = []LoanRule{{ID: "engineering-to-research", LenderRuleID: "engineering", BorrowerRuleID: "research", MaxAmount: 100, ProtectedFloor: 20, MaxDuration: time.Hour}}
	first := quotaPolicyCompile(t, base)
	key := policyLineage(base.ID, "loan", base.Sharing[0].ID)
	if first.Lineages()[key] == "" {
		t.Fatal("sharing lacks activation lineage")
	}
	changed := policyCopy(base)
	changed.Revision++
	changed.Sharing[0].ProtectedFloor++
	if first.Lineages()[key] == quotaPolicyCompile(t, changed).Lineages()[key] {
		t.Fatal("sharing policy changed without a migration guard")
	}
	for _, test := range []struct {
		name string
		edit func(*Policy)
	}{
		{"unknown rule", func(p *Policy) { p.Sharing[0].LenderRuleID = "missing" }},
		{"same rule", func(p *Policy) { p.Sharing[0].BorrowerRuleID = "engineering" }},
		{"same account", func(p *Policy) { p.Budgets[1].Scope.ID = "engineering" }},
		{"dynamic payer", func(p *Policy) { p.Budgets[0].Scope.ID = "" }},
		{"group constraint", func(p *Policy) {
			p.Budgets[0].Scope = Scope{Kind: "group", SubjectRole: "principal", Group: &GroupRef{Source: "workforce", ID: "engineering"}}
		}},
		{"different meter", func(p *Policy) { p.Budgets[1].MeterID = "output" }},
		{"different target", func(p *Policy) { p.Budgets[1].Match.Provider = "another" }},
		{"zero quantity", func(p *Policy) { p.Sharing[0].MaxAmount = 0 }},
		{"negative floor", func(p *Policy) { p.Sharing[0].ProtectedFloor = -1 }},
		{"zero duration", func(p *Policy) { p.Sharing[0].MaxDuration = 0 }},
		{"duplicate identity", func(p *Policy) { p.Sharing = append(p.Sharing, p.Sharing[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := policyCopy(base)
			test.edit(&policy)
			if _, err := Compile(policy); err == nil {
				t.Fatal("incompatible loan policy accepted")
			}
		})
	}
}
