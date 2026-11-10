package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

func TestAccessPointQoSScopeValidatedAtSave(t *testing.T) {
	f := newAPFixture(t)
	for scope, valid := range map[string]bool{"": true, "principal": true, "access_point": true, "tenant": false, "Access_Point": false} {
		spec := apSpec()
		spec.QoS = &AccessPointQoS{Class: "interactive", Scope: scope, Limits: &AccessPointLimit{Concurrency: 2}}
		raw, _ := json.Marshal(spec)
		err := f.h.validate(context.Background(), "tenant", AccessPointKind, "ide", raw)
		if (err == nil) != valid {
			t.Errorf("scope %q: valid=%t, error %v", scope, valid, err)
		}
		if err != nil && !strings.Contains(err.Error(), "qos.scope") {
			t.Errorf("scope %q: %v", scope, err)
		}
	}
}

// qosDemand is an access-point demand from one principal.
func qosDemand(principal string, now time.Time) quotaplex.Demand {
	ref := contracts.SnapshotRef{ID: "m-" + principal, Version: "1"}
	return quotaplex.Demand{Version: quotaplex.Version,
		Context:            contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: "r-" + principal, PrincipalID: principal, MembershipSnapshotRef: &ref, ResourceID: "github"},
		Principal:          quotaplex.Subject{ID: principal, Kind: "human", Membership: quotaplex.Membership{Ref: ref, TenantID: "tenant", SubjectID: principal, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}},
		LogicalOperationID: "op-" + principal, AttemptID: "a-" + principal, PayloadDigest: strings.Repeat("a", 64),
		Target: quotaplex.Target{ConfigurationDigest: strings.Repeat("c", 64), Plane: "mcplex", Provider: "mcp", ResourceID: "github", Operation: "tools/call:search", AccessPointID: "ide", CallerKind: "user"},
		Bounds: quotaplex.Vector{"requests": 1}}
}

func TestAccessPointQuotaPlanScopeSharedOrPerPrincipal(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	render := func(scope string) AccessPointQuotaPlan {
		ap := apSpec()
		ap.QoS = &AccessPointQoS{Class: "interactive", Scope: scope, Limits: &AccessPointLimit{RequestsPerMinute: 60, Concurrency: 3}}
		plan, err := renderAccessPointQuota("tenant", ap, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	keys := func(plan AccessPointQuotaPlan) (rate, concurrency [2]string) {
		compiled, err := quotaplex.Compile(plan.Policy)
		if err != nil {
			t.Fatal("rendered policy rejected by the validator: ", err)
		}
		for i, principal := range []string{"alice", "bob"} {
			resolved, err := compiled.Resolve(qosDemand(principal, now), now)
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved.Rates) != 1 || len(resolved.Concurrency) != 1 {
				t.Fatalf("plan: %+v", resolved)
			}
			rate[i], concurrency[i] = resolved.Rates[0].Key, resolved.Concurrency[0].Key
		}
		return
	}

	for _, scope := range []string{"", "principal"} {
		plan := render(scope)
		if plan.Fragment.Rates[0].Scope.Kind != "principal" || plan.Fragment.Concurrency[0].Scope.Kind != "principal" {
			t.Fatalf("scope %q renders %+v", scope, plan.Fragment)
		}
		rate, concurrency := keys(plan)
		if rate[0] == rate[1] || concurrency[0] == concurrency[1] {
			t.Fatalf("scope %q: callers share a counter: %v %v", scope, rate, concurrency)
		}
	}
	plan := render("access_point")
	if plan.Fragment.Rates[0].Scope.Kind != "tenant" || plan.Fragment.Concurrency[0].Scope.Kind != "tenant" || plan.Fragment.Rates[0].Match.AccessPointID != "ide" {
		t.Fatalf("shared pool renders %+v", plan.Fragment)
	}
	rate, concurrency := keys(plan)
	if rate[0] != rate[1] || concurrency[0] != concurrency[1] {
		t.Fatalf("access_point scope: callers do not share one pool: %v %v", rate, concurrency)
	}
}
