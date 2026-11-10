package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func TestTableFitsWidthAndShortensIDsInTheMiddle(t *testing.T) {
	tb := newTable(keyCol("ID"), col("NOTE"), idCol("PRINCIPAL"), wideCol("EXTRA"))
	long := "principal-4c74072a02b4f07a6478544f4a98924fc565e64ec32edc1ddbcff14c95e8bea6"
	tb.add("ul_0123456789abcdef", "a fairly long free text note that will not fit", long, "hidden")
	var buf bytes.Buffer
	tb.renderWidth(&buf, 80, false)
	out := buf.String()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := ansi.StringWidth(line); w > 80 {
			t.Fatalf("line is %d wide (> 80): %q", w, line)
		}
	}
	if !strings.Contains(out, "ul_0123456789abcdef") {
		t.Fatalf("key column was shortened:\n%s", out)
	}
	if !strings.Contains(out, "principal-") || !strings.Contains(out, "…") || !strings.Contains(out, "bea6") {
		t.Fatalf("principal not shortened in the middle:\n%s", out)
	}
	if strings.Contains(out, "EXTRA") || strings.Contains(out, "hidden") {
		t.Fatalf("wide column shown by default:\n%s", out)
	}
	if strings.Contains(out, "─") {
		t.Fatalf("ruled line in table:\n%s", out)
	}

	buf.Reset()
	tb.renderWidth(&buf, 80, true)
	if !strings.Contains(buf.String(), long) || !strings.Contains(buf.String(), "hidden") {
		t.Fatalf("wide output must show every column untruncated:\n%s", buf.String())
	}
}

func TestMiddleEllipsis(t *testing.T) {
	if got := middleEllipsis("abcdefghij", 5); got != "ab…ij" {
		t.Fatalf("got %q", got)
	}
	if got := middleEllipsis("abc", 5); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

func TestColorPolicyHonoursNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if colorEnabled(os.Stdout) {
		t.Fatal("colour enabled with NO_COLOR set")
	}
}

func TestHumanValues(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for d, want := range map[time.Duration]string{2 * time.Second: "just now", 30 * time.Second: "30s ago", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago", 72 * time.Hour: "3d ago"} {
		if got := relTimeFrom(now.Add(-d), now); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
	if got := relTimeFrom(now.Add(90*time.Minute), now); got != "in 1h" {
		t.Errorf("future: %q", got)
	}
	for in, want := range map[[2]any]string{{"6", uint8(6)}: "0.000006", {"1250", uint8(2)}: "12.50", {"0", uint8(2)}: "0.00", {"5", uint8(0)}: "5"} {
		got, err := formatFixed(in[0].(string), in[1].(uint8))
		if err != nil || got != want {
			t.Errorf("formatFixed(%v) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got := shortMoney("USD", "6", 6, "estimated"); got != "$0.000006 est." {
		t.Errorf("shortMoney = %q", got)
	}
	if got := shortMoney("", "", 0, "unknown"); got != "unknown" {
		t.Errorf("unknown cost rendered as %q", got)
	}
	if got := humanDuration(850); got != "850ms" {
		t.Errorf("850ms -> %q", got)
	}
	if got := humanDuration(1250); got != "1.2s" && got != "1.3s" {
		t.Errorf("1250ms -> %q", got)
	}
}

func TestUsageRecordsShowDisplayNameNotHash(t *testing.T) {
	output = "table"
	defer func() { output = "table" }()
	in := int64(3)
	r := usageledger.Record{ID: "ul_1", StartedAt: time.Now().Add(-time.Minute), Plane: "llmplex", ModelID: "m", Status: "ok",
		PrincipalID: "principal-4c74072a02b4f07a6478544f4a98924fc565e64ec32edc1ddbcff14c95e8bea6", PrincipalDisplay: "dev@example.com",
		Usage: &usageledger.Usage{InputTokens: &in}, Cost: usageledger.UnknownCost()}
	var buf bytes.Buffer
	usageRecordTable([]usageledger.Record{r}).renderWidth(&buf, 100, false)
	out := buf.String()
	if !strings.Contains(out, "dev@example.com") || strings.Contains(out, "4c74072a02b4f07a6478") {
		t.Fatalf("caller should be the display name, not the hash:\n%s", out)
	}
	if !strings.Contains(out, "unknown") {
		t.Fatalf("unknown cost/usage must stay unknown:\n%s", out)
	}
	if !strings.Contains(out, "1m ago") {
		t.Fatalf("relative time missing:\n%s", out)
	}
}
