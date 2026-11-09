package main

import (
	"context"
	"testing"
	"time"
)

func TestQuotaScheduledAdmissionSelectsAccessPointFlow(t *testing.T) {
	controller, engine, _ := schedulingFixture(t, func(_ *quotaDeploymentFile, deployment *quotaSchedulingDeployment) {
		deployment.Routes = []quotaSchedulingRoute{{ResourceID: "tool", OperationID: "tools/call:search", FlowID: "shared"}, {ResourceID: "tool", OperationID: "tools/call:search", AccessPointID: "ide", FlowID: "protected"}}
	})
	for _, test := range []struct{ id, accessPoint, flow string }{{"direct", "", "shared"}, {"via-ide", "ide", "protected"}, {"via-other", "other", "shared"}} {
		demand := schedulingDemand(time.Now().UTC(), test.id)
		if test.accessPoint != "" {
			demand.Target.AccessPointID, demand.Target.CallerKind = test.accessPoint, "user"
		}
		if decision := schedulingAdmit(t, controller, engine, demand); !decision.Allowed {
			t.Fatal(test.id, decision)
		}
		status, err := controller.entries[schedulerCoordinate("tenant", "tool-capacity")].scheduler.Get(context.Background(), test.id)
		if err != nil || status.FlowID != test.flow {
			t.Fatalf("%s: flow %q, want %q (%v)", test.id, status.FlowID, test.flow, err)
		}
	}
	duplicate := schedulingDeployment(time.Now().UTC())
	duplicate.Routes = []quotaSchedulingRoute{{ResourceID: "tool", OperationID: "tools/call:search", AccessPointID: "ide", FlowID: "shared"}, {ResourceID: "tool", OperationID: "tools/call:search", AccessPointID: "ide", FlowID: "protected"}}
	if _, err := newQuotaScheduledAdmission([]quotaSchedulingDeployment{duplicate}); err == nil {
		t.Fatal("overlapping access-point routes accepted")
	}
}
