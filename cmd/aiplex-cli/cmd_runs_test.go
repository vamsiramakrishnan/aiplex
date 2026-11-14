package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const runsEffectsBody = `{"effects":[
{"run_id":"r-1","seq":1,"kind":"effect.begin","tool":"pay","scope":"payments:write","timestamp":"2026-05-01T10:00:00Z"},
{"run_id":"r-1","seq":2,"kind":"effect.confirmed","tool":"pay","scope":"payments:write","timestamp":"2026-05-01T10:00:01Z"},
{"run_id":"r-1","seq":3,"kind":"effect.unknown","tool":"wire","scope":"payments:write","timestamp":"2026-05-01T10:00:02Z"},
{"run_id":"r-1","seq":4,"kind":"effect.failed","tool":"","scope":"","timestamp":"2026-05-01T10:00:03Z"}]}`

func cellsOf(line string) []string { return strings.Fields(line) }

func TestRunsEffectsShowsUnknownNeverBlank(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/effects": runsEffectsBody})
	out, err := runSP(t, "", runsCmd(), "effects", "r-1")
	if err != nil {
		t.Fatal(out, err)
	}
	var unknownLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "wire") {
			unknownLine = l
		}
	}
	if !strings.Contains(unknownLine, "unknown") {
		t.Fatalf("effect.unknown must render as unknown: %q\n%s", unknownLine, out)
	}
	for _, want := range []string{"OUTCOME", "confirmed", "failed", "4 effect(s)", "1 with unknown outcome"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if len(calls) != 1 || calls[0].path != "/api/v1/runs/r-1/effects" {
		t.Errorf("calls %+v", calls)
	}
}

func TestRunsEffectsEmptyOutcomeKindIsUnknown(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/effects": `{"effects":[{"run_id":"r-1","seq":1,"kind":"","tool":"x"}]}`})
	out, err := runSP(t, "", runsCmd(), "effects", "r-1")
	if err != nil || !strings.Contains(out, "unknown") {
		t.Fatal(out, err)
	}
}

func TestRunsEffectsJSONAndYAMLKeepKind(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/effects": runsEffectsBody})
	output = "json"
	out, err := runSP(t, "", runsCmd(), "effects", "r-1")
	if err != nil {
		t.Fatal(out, err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 4 || got[2]["kind"] != "effect.unknown" {
		t.Fatalf("%v\n%s", err, out)
	}
	output = "yaml"
	out, err = runSP(t, "", runsCmd(), "effects", "r-1")
	if err != nil || !strings.Contains(out, "kind: effect.unknown") || !strings.Contains(out, "run_id: r-1") {
		t.Fatal(out, err)
	}
}

func TestRunsEffectsEmptyJSONIsArray(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/effects": `{"effects":[]}`})
	output = "json"
	out, err := runSP(t, "", runsCmd(), "effects", "r-1")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("%v %q", err, out)
	}
}

func TestRunsObligations(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/obligations": `{"obligations":[{"run_id":"r-1","seq":9,"kind":"obligation.created","tool":"refund","scope":"payments:write","timestamp":"2026-05-01T10:00:00Z","payload_json":"{\"undo\":\"refund\"}"}]}`})
	out, err := runSP(t, "", runsCmd(), "obligations", "r-1")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"SEQ", "refund", "payments:write", "1 obligation(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestRunsBudgetsUnknownAmountIsNotZero(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/budgets": `{"budgets":[
{"run_id":"r-1","seq":1,"kind":"budget.charged","timestamp":"2026-05-01T10:00:00Z","payload_json":"{\"usd_charged\":1.25}"},
{"run_id":"r-1","seq":2,"kind":"budget.charged","timestamp":"2026-05-01T10:00:01Z","payload_json":"{\"usd_charged\":0.5}"},
{"run_id":"r-1","seq":3,"kind":"budget.charged","timestamp":"2026-05-01T10:00:02Z"},
{"run_id":"r-1","seq":4,"kind":"budget.charged","timestamp":"2026-05-01T10:00:03Z","payload_json":"not json"}]}`})
	out, err := runSP(t, "", runsCmd(), "budgets", "r-1")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, l := range strings.Split(out, "\n") {
		f := cellsOf(l)
		if len(f) > 0 && (f[0] == "3" || f[0] == "4") && strings.Contains(l, "2026-") && !strings.Contains(l, "unknown") {
			t.Errorf("missing amount must be unknown: %q", l)
		}
	}
	for _, want := range []string{"$1.25", "$0.50", "4 charge(s)", "Total charged: $1.75", "2 charge(s) with unknown amount"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestRunsHealth(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/_health": `{"last_ingest_at":"2026-05-01T10:00:00Z","has_runs":true,"tape_instances_count":2,"now":"2026-05-01T10:05:00Z"}`})
	out, err := runSP(t, "", runsCmd(), "health")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"2026-05-01T10:00:00Z", "Runs known:", "yes", "Tape instances:", "2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	output = "json"
	out, err = runSP(t, "", runsCmd(), "health")
	var h map[string]any
	if err != nil || json.Unmarshal([]byte(out), &h) != nil || h["has_runs"] != true || h["tape_instances_count"] != float64(2) {
		t.Fatal(out, err)
	}
}

func TestRunsHealthNoIngestIsNotATimestamp(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/_health": `{"last_ingest_at":"0001-01-01T00:00:00Z","has_runs":false,"tape_instances_count":0,"now":"2026-05-01T10:05:00Z"}`})
	out, err := runSP(t, "", runsCmd(), "health")
	if err != nil || strings.Contains(out, "0001") || !strings.Contains(out, "none recorded") || !strings.Contains(out, "no") {
		t.Fatal(out, err)
	}
}

// A caller confined to one tenant gets no deployment-wide values; the CLI
// prints them as unknown, never as zero, and the JSON output omits them.
func TestRunsHealthOmittedValuesAreUnknown(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/_health": `{"has_runs":true,"now":"2026-05-01T10:05:00Z"}`})
	out, err := runSP(t, "", runsCmd(), "health")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"Last ingest:     unknown", "Runs known:      yes", "Tape instances:  unknown"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "none recorded") || strings.Contains(out, "0001") || strings.Contains(out, "Tape instances:  0") {
		t.Errorf("an omitted value was shown as zero:\n%s", out)
	}
	output = "json"
	out, err = runSP(t, "", runsCmd(), "health")
	var h map[string]any
	if err != nil || json.Unmarshal([]byte(out), &h) != nil {
		t.Fatal(out, err)
	}
	if _, ok := h["tape_instances_count"]; ok {
		t.Errorf("json output invents tape_instances_count: %s", out)
	}
	if _, ok := h["last_ingest_at"]; ok {
		t.Errorf("json output invents last_ingest_at: %s", out)
	}
}

func runsEventsBody(from, n int) string {
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, `{"run_id":"r-1","seq":`+itoa(from+i)+`,"kind":"decision.recorded","timestamp":"2026-05-01T10:00:00Z"}`)
	}
	return `{"events":[` + strings.Join(parts, ",") + `]}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestRunsEventsPagingHint(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/events": runsEventsBody(5, 3)})
	out, err := runSP(t, "", runsCmd(), "events", "r-1", "--from-seq", "5", "--limit", "3")
	if err != nil {
		t.Fatal(out, err)
	}
	if len(calls) != 1 || calls[0].query != "from_seq=5&limit=3" {
		t.Fatalf("calls %+v", calls)
	}
	if !strings.Contains(out, "More: aiplex runs events r-1 --from-seq 8 --limit 3") {
		t.Fatalf("missing paging hint:\n%s", out)
	}
}

func TestRunsEventsNoHintOnShortPage(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/events": runsEventsBody(1, 2)})
	out, err := runSP(t, "", runsCmd(), "events", "r-1", "--limit", "3")
	if err != nil || strings.Contains(out, "More:") || !strings.Contains(out, "2 event(s)") {
		t.Fatal(out, err)
	}
}

func TestRunsEventsDefaultsAndValidation(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/events": runsEventsBody(1, 1)})
	if out, err := runSP(t, "", runsCmd(), "events", "r-1"); err != nil || strings.Contains(out, "More:") {
		t.Fatal(out, err)
	}
	if _, err := runSP(t, "", runsCmd(), "events", "r-1", "--limit", "0"); err == nil {
		t.Error("--limit 0 must be refused")
	}
	if _, err := runSP(t, "", runsCmd(), "events", "r-1", "--from-seq", "-1"); err == nil {
		t.Error("negative --from-seq must be refused")
	}
}

func TestRunsEventsYAML(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/events": runsEventsBody(1, 1)})
	output = "yaml"
	out, err := runSP(t, "", runsCmd(), "events", "r-1")
	if err != nil || !strings.Contains(out, "kind: decision.recorded") {
		t.Fatal(out, err)
	}
}

func TestRunsBudgetsTotalCases(t *testing.T) {
	charge := func(seq int, payload string) string {
		p := ""
		if payload != "" {
			p = `,"payload_json":"` + strings.ReplaceAll(payload, `"`, `\"`) + `"`
		}
		return `{"run_id":"r-1","seq":` + itoa(seq) + `,"kind":"budget.charged","timestamp":"2026-05-01T10:00:00Z"` + p + `}`
	}
	cases := []struct {
		name    string
		events  []string
		want    string
		notWant string
	}{
		{"none", nil, "No budget charges recorded; no total.", "$0.00"},
		{"all unknown", []string{charge(1, ""), charge(2, "not json")}, "Total charged: unknown (2 charge(s) with unknown amount)", "$0.00"},
		{"known zero", []string{charge(1, `{"usd_charged":0}`)}, "Total charged: $0.00\n", "unknown amount"},
		{"mix", []string{charge(1, `{"usd_charged":2}`), charge(2, "")}, "Total charged: $2.00 (excludes 1 charge(s) with unknown amount)", "Total charged: unknown"},
	}
	for _, c := range cases {
		var calls []spCall
		spServer(t, &calls, map[string]string{"GET /api/v1/runs/r-1/budgets": `{"budgets":[` + strings.Join(c.events, ",") + `]}`})
		out, err := runSP(t, "", runsCmd(), "budgets", "r-1")
		if err != nil || !strings.Contains(out, c.want) || strings.Contains(out, c.notWant) {
			t.Errorf("%s: %v\n%s", c.name, err, out)
		}
	}
}

func TestRunsEventsHintForDashLeadingRunIDParsesBack(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/runs/-run-1/events": runsEventsBody(1, 2)})
	out, err := runSP(t, "", runsCmd(), "events", "--limit", "2", "--", "-run-1")
	if err != nil {
		t.Fatal(out, err)
	}
	var more string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "More: ") {
			more = strings.TrimPrefix(l, "More: ")
		}
	}
	f := strings.Fields(more)
	if len(f) < 4 || f[0] != "aiplex" || f[1] != "runs" {
		t.Fatalf("hint %q", more)
	}
	calls = nil
	if out, err = runSP(t, "", runsCmd(), f[2:]...); err != nil {
		t.Fatalf("printed command %q does not parse: %v\n%s", more, err, out)
	}
	if len(calls) != 1 || calls[0].path != "/api/v1/runs/-run-1/events" || calls[0].query != "from_seq=3&limit=2" {
		t.Fatalf("%q -> %+v", more, calls)
	}
}

func TestRunsListTruncatesMultibyteRunIDSafely(t *testing.T) {
	var calls []spCall
	id := "run-日本語日本語日本語日本語日本語日本語"
	spServer(t, &calls, map[string]string{"GET /api/v1/runs": `{"runs":[{"run_id":"` + id + `","status":"running","tenant_id":"t","agent_id":"a"}]}`})
	out, err := runSP(t, "", runsCmd(), "list")
	if err != nil || !utf8.ValidString(out) {
		t.Fatalf("%v invalid utf-8 in %q", err, out)
	}
}

func TestRunsRunIDShorteningIsRuneSafe(t *testing.T) {
	for _, s := range []string{"héllo wörld ünïcode", "日本語日本語日本語", "plain ascii text"} {
		for _, n := range []int{0, 1, 2} {
			got := shorten(s, n, truncEnd, false)
			if !utf8.ValidString(got) {
				t.Errorf("shorten(%q,%d) = %q is not valid UTF-8", s, n, got)
			}
			if ansi.StringWidth(got) > n {
				t.Errorf("shorten(%q,%d) = %q is wider than %d", s, n, got, n)
			}
		}
	}
}
