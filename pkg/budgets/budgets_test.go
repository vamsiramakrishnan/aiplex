package budgets

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

func cost(id, parent, currency, amount string) Budget {
	return Budget{ID: id, Parent: parent, Limit: Limit{Unit: UnitCost, Currency: currency, Amount: amount}, Window: Window{Kind: WindowCalendar, Unit: "month", TimeZone: "UTC"}}
}

func TestDecodeRefusesUnknownFieldsAndIDMismatch(t *testing.T) {
	if _, err := Decode("a", []byte(`{"id":"a","limit":{"unit":"tokens","amount":"1"},"window":{"kind":"calendar","unit":"day","time_zone":"UTC"},"surprise":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Decode("a", []byte(`{"id":"b"}`)); err == nil {
		t.Fatal("id mismatch accepted")
	}
	if _, err := Decode("a", []byte(`{"id":"a"} {"id":"a"}`)); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestValidateRefusesBadSpecifications(t *testing.T) {
	ok := cost("a", "", "USD", "10.00")
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Budget){
		"id":         func(b *Budget) { b.ID = "bad id" },
		"unit":       func(b *Budget) { b.Limit.Unit = "bytes" },
		"currency":   func(b *Budget) { b.Limit.Currency = "usd" },
		"amount":     func(b *Budget) { b.Limit.Amount = "1e3" },
		"float":      func(b *Budget) { b.Limit.Amount = "-1" },
		"tokens-dec": func(b *Budget) { b.Limit = Limit{Unit: UnitTokens, Amount: "1.5"} },
		"rolling":    func(b *Budget) { b.Window = Window{Kind: "rolling", Hours: 24} },
		"zone":       func(b *Budget) { b.Window.TimeZone = "Mars/Base" },
		"no-zone":    func(b *Budget) { b.Window.TimeZone = "" },
		"hours-cal":  func(b *Budget) { b.Window.Hours = 3 },
		"tumbling0":  func(b *Budget) { b.Window = Window{Kind: WindowTumbling} },
		"unknown":    func(b *Budget) { b.OnUnknownCost = "guess" },
		"cost-only":  func(b *Budget) { b.Limit = Limit{Unit: UnitTokens, Amount: "5"}; b.OnUnknownCost = OnUnknownCostDeny },
		"threshold":  func(b *Budget) { b.Thresholds = []int{0} },
		"dup-group": func(b *Budget) {
			g := quotaplex.GroupRef{Source: "s", ID: "g"}
			b.Members.Groups = []quotaplex.GroupRef{g, g}
		},
		"project": func(b *Budget) { b.Members.Projects = []string{"has space"} },
		"self":    func(b *Budget) { b.Parent = "a" },
	}
	for name, mutate := range cases {
		b := ok
		mutate(&b)
		if err := b.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		} else {
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field == "" {
				t.Errorf("%s: error does not name a field: %v", name, err)
			}
		}
	}
}

func TestTreeRejectsCyclesDepthAndCrossCurrency(t *testing.T) {
	if _, err := NewTree([]Budget{cost("a", "b", "USD", "1"), cost("b", "a", "USD", "1")}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle: %v", err)
	}
	if _, err := NewTree([]Budget{cost("a", "missing", "USD", "1")}); err == nil {
		t.Fatal("missing parent accepted")
	}
	chain := []Budget{cost("l1", "", "USD", "1"), cost("l2", "l1", "USD", "1"), cost("l3", "l2", "USD", "1"), cost("l4", "l3", "USD", "1")}
	if _, err := NewTree(chain); err != nil {
		t.Fatalf("depth %d must be allowed: %v", MaxDepth, err)
	}
	if _, err := NewTree(append(chain, cost("l5", "l4", "USD", "1"))); err == nil || !strings.Contains(err.Error(), "levels deep") {
		t.Fatalf("depth: %v", err)
	}
	if _, err := NewTree([]Budget{cost("p", "", "USD", "10"), cost("c", "p", "EUR", "1")}); err == nil || !strings.Contains(err.Error(), "currenc") {
		t.Fatalf("cross-currency roll-up: %v", err)
	}
	// A tokens budget between two cost budgets does not hide a currency clash.
	mid := Budget{ID: "m", Parent: "p", Limit: Limit{Unit: UnitTokens, Amount: "5"}, Window: Window{Kind: WindowTumbling, Hours: 1}}
	if _, err := NewTree([]Budget{cost("p", "", "USD", "10"), mid, cost("c", "m", "EUR", "1")}); err == nil {
		t.Fatal("currency clash across a non-cost level accepted")
	}
	// Different units under one parent are fine.
	if _, err := NewTree([]Budget{cost("p", "", "USD", "10"), mid}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDrawsUpTheTreeAndExplainsWhy(t *testing.T) {
	grp := quotaplex.GroupRef{Source: "s", ID: "platform"}
	org := cost("org", "", "USD", "100")
	team := cost("team", "org", "USD", "40")
	team.Members.Groups = []quotaplex.GroupRef{grp}
	other := cost("other", "org", "USD", "40")
	other.Members.Principals = []string{"someone-else"}
	proj := cost("proj", "team", "USD", "10")
	proj.Members.Projects = []string{"apollo"}
	tree, err := NewTree([]Budget{org, team, other, proj})
	if err != nil {
		t.Fatal(err)
	}
	ids := func(ms []Match) string {
		var out []string
		for _, m := range ms {
			out = append(out, m.ID)
		}
		return strings.Join(out, ",")
	}
	got := tree.Resolve(Facts{Groups: []quotaplex.GroupRef{grp}, PrincipalID: "me"})
	if ids(got) != "team,org" || !got[0].Direct || got[1].Direct || got[1].ViaChild != "team" {
		t.Fatalf("%+v", got)
	}
	got = tree.Resolve(Facts{Groups: []quotaplex.GroupRef{grp}, Project: "apollo"})
	if ids(got) != "proj,team,org" || got[0].Reasons[0] != "project:apollo" {
		t.Fatalf("%+v", got)
	}
	if got = tree.Resolve(Facts{PrincipalID: "stranger"}); len(got) != 0 {
		t.Fatalf("a non-member draws from nothing: %+v", got)
	}
	// A group with the same id from another source is a different group.
	if got = tree.Resolve(Facts{Groups: []quotaplex.GroupRef{{Source: "other-source", ID: "platform"}}}); len(got) != 0 {
		t.Fatalf("group ids are source-qualified: %+v", got)
	}
	if !tree.DirectMember("team", Facts{Groups: []quotaplex.GroupRef{grp}}) || tree.DirectMember("org", Facts{Groups: []quotaplex.GroupRef{grp}}) {
		t.Fatal("direct membership")
	}
}

func TestEmptyMembersMatchNobodyDirectly(t *testing.T) {
	tree, _ := NewTree([]Budget{cost("lonely", "", "USD", "1")})
	if got := tree.Resolve(Facts{PrincipalID: "x", AccessPointID: "y"}); len(got) != 0 {
		t.Fatal("a budget with no members must not match everyone")
	}
}

func TestAmountsAreExact(t *testing.T) {
	cases := []struct {
		in    string
		scale uint8
		want  int64
		bad   bool
	}{
		{"250.00", 6, 250000000, false},
		{"0.000001", 6, 1, false},
		{"0.0000001", 6, 0, true}, // finer than the meter
		{"100", 0, 100, false},
		{"1.5", 0, 0, true},
		{"9223372036854775807", 0, 9223372036854775807, false},
		{"9223372036854775808", 0, 0, true},
		{"00.5", 6, 0, true},
		{"", 0, 0, true},
	}
	for _, c := range cases {
		got, err := ParseAmount(c.in, c.scale)
		if (err != nil) != c.bad || (!c.bad && got != c.want) {
			t.Errorf("%q/%d: %d %v", c.in, c.scale, got, err)
		}
	}
	if FormatAmount(250000000, 6) != "250.000000" || FormatAmount(1, 6) != "0.000001" || FormatAmount(5, 0) != "5" {
		t.Fatal(FormatAmount(1, 6))
	}
}

func TestCalendarWindowsFollowTheConfiguredZone(t *testing.T) {
	b := cost("a", "", "USD", "1")
	b.Window = Window{Kind: WindowCalendar, Unit: "month", TimeZone: "Australia/Melbourne"}
	p, err := b.Period()
	if err != nil {
		t.Fatal(err)
	}
	// 2026-02-28 20:00 UTC is 2026-03-01 07:00 in Melbourne (UTC+11): March.
	w, err := p.Resolve(time.Date(2026, 2, 28, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 2, 28, 13, 0, 0, 0, time.UTC); !w.Start.Equal(want) {
		t.Fatalf("month starts at local midnight: %v", w.Start)
	}
	w2, _ := p.Resolve(time.Date(2026, 2, 28, 12, 59, 0, 0, time.UTC)) // still February locally
	if !w2.End.Equal(w.Start) {
		t.Fatalf("windows are adjacent at the zone boundary: %v %v", w2.End, w.Start)
	}
	// Weeks start Monday.
	b.Window = Window{Kind: WindowCalendar, Unit: "week", TimeZone: "UTC"}
	p, _ = b.Period()
	w, _ = p.Resolve(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)) // a Wednesday
	if w.Start.Weekday() != time.Monday || w.Start.Day() != 9 {
		t.Fatalf("week: %v", w.Start)
	}
	// Tumbling windows are consecutive N-hour buckets from the UTC anchor.
	b.Window = Window{Kind: WindowTumbling, Hours: 6}
	p, _ = b.Period()
	w, _ = p.Resolve(time.Date(2026, 11, 11, 7, 30, 0, 0, time.UTC))
	if w.Start.Hour() != 6 || w.End.Sub(w.Start) != 6*time.Hour {
		t.Fatalf("tumbling: %v", w)
	}
}

func TestDrawConversion(t *testing.T) {
	b := cost("a", "", "USD", "2.50")
	d, err := b.Draw(6)
	if err != nil || d.Limit != 2500000 || d.Currency != "USD" || d.Scale != 6 || d.OnUnknownCost != "deny" || len(d.Thresholds) != 3 {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err = b.Draw(1); err == nil {
		t.Fatal("an amount finer than the meter scale must be refused, not rounded")
	}
	b.OnUnknownCost = OnUnknownCostAllowAndFlag
	if d, _ = b.Draw(6); d.OnUnknownCost != "allow_and_flag" {
		t.Fatal(d)
	}
	tok := Budget{ID: "t", Limit: Limit{Unit: UnitTokens, Amount: "1000"}, Window: Window{Kind: WindowTumbling, Hours: 1}}
	if d, err = tok.Draw(0); err != nil || d.Limit != 1000 || d.Unit != "tokens" {
		t.Fatalf("%+v %v", d, err)
	}
	if err = quotaplex.ValidateDraw(d); err != nil {
		t.Fatal(err)
	}
}

func TestAppliesByPlane(t *testing.T) {
	req := Budget{Limit: Limit{Unit: UnitRequests}}
	tok := Budget{Limit: Limit{Unit: UnitTokens}}
	if !req.Applies("mcplex") || tok.Applies("mcplex") || !tok.Applies("llmplex") {
		t.Fatal("token and cost budgets count model traffic only")
	}
}
