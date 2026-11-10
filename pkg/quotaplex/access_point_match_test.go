package quotaplex

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPolicyAccessPointCallerKindAndClientDimensions(t *testing.T) {
	policy, demand, now := quotaPolicyFixture()
	policy.Budgets, policy.PriceBooks = nil, nil
	policy.Rates = []RateRule{{ID: "ap-rpm", Scope: Scope{Kind: "principal"}, Match: Match{AccessPointID: "ide", CallerKind: "user"}, MeterID: "requests", Rate: 2, Interval: time.Minute, Burst: 2, Basis: "admitted"}}
	policy.Concurrency = []ConcurrencyRule{{ID: "ap-client", Scope: Scope{Kind: "tenant"}, Match: Match{AccessPointID: "ide", ClientID: "registered-ide"}, Limit: 1}}
	compiled := quotaPolicyCompile(t, policy)

	quotaExpectResolveCode(t, compiled, demand, now, "no_matching_controls")
	demand.Target.AccessPointID, demand.Target.CallerKind = "ide", "user"
	plan, err := compiled.Resolve(demand, now)
	if err != nil || len(plan.Rates) != 1 || len(plan.Concurrency) != 0 {
		t.Fatalf("access point and caller kind did not select the rate: %+v %v", plan, err)
	}
	demand.Target.ClientID = "registered-ide"
	if plan, err = compiled.Resolve(demand, now); err != nil || len(plan.Rates) != 1 || len(plan.Concurrency) != 1 {
		t.Fatalf("client dimension did not select the concurrency rule: %+v %v", plan, err)
	}
	demand.Target.CallerKind = "agent"
	if plan, err = compiled.Resolve(demand, now); err != nil || len(plan.Rates) != 0 || len(plan.Concurrency) != 1 {
		t.Fatalf("caller kind selector ignored: %+v %v", plan, err)
	}
	demand.Target.AccessPointID = "other"
	quotaExpectResolveCode(t, compiled, demand, now, "no_matching_controls")

	for name, match := range map[string]Match{"caller kind": {CallerKind: "robot"}, "wildcard access point": {AccessPointID: "ide*"}, "wildcard client": {ClientID: "*"}} {
		bad := policy
		bad.Concurrency = []ConcurrencyRule{{ID: "bad", Scope: Scope{Kind: "tenant"}, Match: match, Limit: 1}}
		if _, err := Compile(bad); err == nil {
			t.Errorf("%s selector accepted", name)
		}
	}
	unbound := demand
	unbound.Target.CallerKind = ""
	if ValidateDemand(unbound) == nil {
		t.Error("access point without caller kind accepted")
	}
	unbound.Target.CallerKind = "robot"
	if ValidateDemand(unbound) == nil {
		t.Error("unknown caller kind accepted")
	}
}

// Existing policies and demands serialize exactly as before, so their
// digests, lineage shapes and attempt identities are unchanged.
func TestPolicyAccessPointDimensionsAreOmittedWhenEmpty(t *testing.T) {
	raw, _ := json.Marshal(Match{Plane: "llmplex"})
	if string(raw) != `{"plane":"llmplex"}` {
		t.Fatalf("match serialization changed: %s", raw)
	}
	raw, _ = json.Marshal(Target{Plane: "llmplex", Provider: "google", ResourceID: "r", Operation: "o", ConfigurationDigest: "d"})
	if string(raw) != `{"plane":"llmplex","provider":"google","resource_id":"r","operation":"o","configuration_digest":"d"}` {
		t.Fatalf("target serialization changed: %s", raw)
	}
	policy, _, _ := quotaPolicyFixture()
	first := quotaPolicyCompile(t, policy).Reference()
	second := quotaPolicyCompile(t, policy).Reference()
	if first != second || !MatchesTarget(Match{}, Target{AccessPointID: "ide", CallerKind: "user"}) {
		t.Fatal("empty selectors must keep matching every target")
	}
}
