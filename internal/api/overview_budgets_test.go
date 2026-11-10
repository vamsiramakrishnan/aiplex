package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
)

func TestOverviewShowsBudgetsOfAccessPointsAndSubjects(t *testing.T) {
	f := newOverviewFixture(t)
	spec := apSpec()
	spec.Project = "apollo"
	setSpec(f.apFixture, spec)
	b := budgets.Budget{ID: "apollo", DisplayName: "Apollo", Limit: budgets.Limit{Unit: budgets.UnitRequests, Amount: "100"}, Window: budgets.Window{Kind: budgets.WindowCalendar, Unit: "month", TimeZone: "UTC"}, Members: budgets.Members{Projects: []string{"apollo"}}}
	f.put(BudgetKind, "apollo", b)
	alice := apUser("alice").PrincipalID
	solo := b
	solo.ID, solo.DisplayName, solo.Members = "alice-only", "Alice", budgets.Members{Principals: []string{alice}}
	f.put(BudgetKind, "alice-only", solo)

	// Without a quota host there are no counters: unavailable, never zero.
	w := f.get("/api/v1/overview/access-points/ide")
	var ap OverviewAccessPoint
	if err := json.Unmarshal(w.Body.Bytes(), &ap); err != nil || w.Code != 200 || len(ap.Budgets.Applicable) != 1 || ap.Budgets.Applicable[0].ID != "apollo" || ap.Budgets.Applicable[0].Counters != "unavailable" || ap.Budgets.Applicable[0].Spent != "" {
		t.Fatalf("access point budgets: %d %s", w.Code, w.Body.String())
	}
	f.h.Quota = budgetTestHost(t)
	w = f.get("/api/v1/overview/access-points/ide")
	if err := json.Unmarshal(w.Body.Bytes(), &ap); err != nil || ap.Budgets.Applicable[0].Counters != "available" || ap.Budgets.Applicable[0].Spent != "0" || ap.Budgets.Applicable[0].Remaining != "100" || ap.Budgets.Applicable[0].Window == nil {
		t.Fatalf("counters: %s", w.Body.String())
	}
	if !strings.Contains(ap.Budgets.Note, "group") {
		t.Fatalf("the note must say group budgets apply per request: %q", ap.Budgets.Note)
	}
	w = f.get("/api/v1/overview/subjects/user/" + alice)
	var detail OverviewSubjectDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got := map[string]OverviewBudget{}
	for _, item := range detail.Budgets.Applicable {
		got[item.ID] = item
	}
	if !got["alice-only"].Direct || len(got["alice-only"].MatchedBy) != 1 || len(got["apollo"].ViaAccessPoints) == 0 || got["apollo"].ViaAccessPoints[0] != "ide" {
		t.Fatalf("subject budgets: %s", w.Body.String())
	}
}
