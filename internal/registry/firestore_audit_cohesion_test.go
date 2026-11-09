package registry

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func TestFirestoreAuditQueriesUseSDKFieldsAndFilterBeforeLimit(t *testing.T) {
	s, _ := queryWireFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	trace := strings.Repeat("a", 32)
	for i := 0; i < 10; i++ {
		agent := "unrelated"
		skill := "other"
		caller := "unrelated"
		callee := "other"
		tid := strings.Repeat("b", 32)
		if i == 0 {
			agent = "target-agent"
			skill = "target-skill"
			caller = "target-agent"
			tid = trace
		}
		if i == 1 {
			callee = "target-agent"
			tid = trace
		}
		if e := s.AppendSkillInvocation(ctx, &models.SkillInvocation{ID: fmt.Sprintf("skill-%d", i), AgentID: agent, SkillName: skill, StartedAt: now.Add(time.Duration(i) * time.Minute)}); e != nil {
			t.Fatal(e)
		}
		if e := s.AppendDelegation(ctx, &models.Delegation{ID: fmt.Sprintf("delegation-%d", i), CallerAgentID: caller, CalleeAgentID: callee, TraceID: tid, StartedAt: now.Add(time.Duration(i) * time.Minute)}); e != nil {
			t.Fatal(e)
		}
	}
	inv, e := s.ListSkillInvocations(ctx, "target-agent", "target-skill", 1)
	if e != nil || len(inv) != 1 || inv[0].ID != "skill-0" {
		t.Fatalf("skill filter applied after limit or queried wrong persisted fields: %+v %v", inv, e)
	}
	delegations, e := s.ListDelegations(ctx, "target-agent", 1)
	if e != nil || len(delegations) != 1 || delegations[0].ID != "delegation-1" {
		t.Fatalf("delegation OR filter/limit inconsistent: %+v %v", delegations, e)
	}
	chain, e := s.ListDelegationTrace(ctx, trace, 3)
	if e != nil || len(chain) != 2 || chain[0].ID != "delegation-1" || chain[1].ID != "delegation-0" {
		t.Fatalf("trace query excludes older matching spans: %+v %v", chain, e)
	}
}
