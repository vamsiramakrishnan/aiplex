package main

// One table policy for the whole CLI.
//
//   - Tables fit the terminal: the width comes from $COLUMNS, then the
//     terminal stdout is attached to. When stdout is not a terminal (a pipe or
//     a file) and $COLUMNS is unset, cells are never shortened, so grep and
//     awk see whole values.
//   - Each table has a default column set chosen for the task; columns marked
//     wide appear only with -o wide, which also turns truncation off.
//   - When a row is too wide, the widest truncatable columns shrink first.
//     Identifiers are shortened in the middle (head…tail) so both ends stay
//     recognisable; prose is shortened at the end. The first column (the key
//     you pass to the next command) is never shortened.
//   - No ruled lines: a header row, then one line per row.
//   - Colour only when stdout is a terminal and NO_COLOR is unset.
//   - -o json and -o yaml are never truncated and never coloured.

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// truncMode says how a column's cells are shortened to fit.
type truncMode int

const (
	truncAuto   truncMode = iota // middle for identifier-like cells, end for prose
	truncEnd                     // "long text…"
	truncMiddle                  // "principal-4c74…bea6"
	truncNever                   // keys you copy into the next command
)

const (
	tableGap       = 2
	minColumnWidth = 6
	// unlimitedWidth means "do not shorten": stdout is not a terminal.
	unlimitedWidth = 1 << 30
)

// column is one table column.
type column struct {
	Header string
	Wide   bool // shown only with -o wide
	Trunc  truncMode
}

func col(header string) column            { return column{Header: header} }
func keyCol(header string) column         { return column{Header: header, Trunc: truncNever} }
func idCol(header string) column          { return column{Header: header, Trunc: truncMiddle} }
func wideCol(header string) column        { return column{Header: header, Wide: true} }
func (c column) wide() column             { c.Wide = true; return c }
func (c column) trunc(m truncMode) column { c.Trunc = m; return c }

// table collects rows and renders them under the CLI's table policy.
type outTable struct {
	cols []column
	rows [][]string
}

func newTable(cols ...column) *outTable { return &outTable{cols: cols} }

// add appends a row; missing cells render empty.
func (t *outTable) add(cells ...string) { t.rows = append(t.rows, cells) }

// wideOutput reports whether -o wide was asked for.
func wideOutput() bool { return output == "wide" }

// machineOutput reports whether -o asks for JSON or YAML.
func machineOutput() bool { return output == "json" || output == "yaml" }

// terminalWidth is the width tables fit into.
func terminalWidth() int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && v > 20 {
		return v
	}
	if term.IsTerminal(os.Stdout.Fd()) {
		if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 20 {
			return w
		}
		return 80
	}
	return unlimitedWidth
}

// stdoutIsTerminal reports whether stdout is attached to a terminal.
func stdoutIsTerminal() bool { return term.IsTerminal(os.Stdout.Fd()) }

// stdinIsTerminal reports whether stdin is attached to a terminal.
func stdinIsTerminal() bool { return term.IsTerminal(os.Stdin.Fd()) }

// colorEnabled is true only on a terminal, with NO_COLOR unset and TERM not
// "dumb".
func colorEnabled(f *os.File) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" || noColorFlag {
		return false
	}
	return term.IsTerminal(f.Fd())
}

var noColorFlag bool

// applyColorPolicy turns lipgloss styling off unless stdout may be coloured.
func applyColorPolicy() {
	if !colorEnabled(os.Stdout) {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

var headerCellStyle = lipgloss.NewStyle().Bold(true)

// render writes the table to w.
func (t *outTable) render(w io.Writer) {
	t.renderWidth(w, terminalWidth(), wideOutput())
}

func (t *outTable) renderWidth(w io.Writer, width int, wide bool) {
	var idx []int
	for i, c := range t.cols {
		if wide || !c.Wide {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return
	}
	cell := func(row []string, i int) string {
		if i < len(row) {
			return strings.ReplaceAll(row[i], "\n", " ")
		}
		return ""
	}
	widths := make([]int, len(idx))
	for j, i := range idx {
		widths[j] = ansi.StringWidth(t.cols[i].Header)
		for _, row := range t.rows {
			if n := ansi.StringWidth(cell(row, i)); n > widths[j] {
				widths[j] = n
			}
		}
	}
	if !wide {
		fitWidths(widths, idx, t.cols, width)
	}
	line := func(cells []string, header bool) {
		var b strings.Builder
		for j := range idx {
			v := cells[j]
			if !wide {
				v = shorten(v, widths[j], t.cols[idx[j]].Trunc, j == 0)
			}
			pad := widths[j] - ansi.StringWidth(v)
			if header {
				v = headerCellStyle.Render(v)
			}
			b.WriteString(v)
			if j < len(idx)-1 {
				b.WriteString(strings.Repeat(" ", pad+tableGap))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	headers := make([]string, len(idx))
	for j, i := range idx {
		headers[j] = t.cols[i].Header
	}
	line(headers, true)
	for _, row := range t.rows {
		cells := make([]string, len(idx))
		for j, i := range idx {
			cells[j] = cell(row, i)
		}
		line(cells, false)
	}
}

// fitWidths shrinks the widest shrinkable column one cell at a time until the
// row fits or nothing can shrink further.
func fitWidths(widths, idx []int, cols []column, width int) {
	total := func() int {
		n := tableGap * (len(widths) - 1)
		for _, w := range widths {
			n += w
		}
		return n
	}
	floor := func(j int) int {
		c := cols[idx[j]]
		if c.Trunc == truncNever || (j == 0 && c.Trunc == truncAuto) {
			return widths[j]
		}
		m := ansi.StringWidth(c.Header)
		floor := minColumnWidth
		if c.Trunc == truncMiddle {
			// Keep enough of both ends of an identifier to recognise it.
			floor = 12
		}
		if m < floor {
			m = floor
		}
		if m > widths[j] {
			m = widths[j]
		}
		return m
	}
	for total() > width {
		best := -1
		for j := range widths {
			if widths[j] > floor(j) && (best < 0 || widths[j] > widths[best]) {
				best = j
			}
		}
		if best < 0 {
			return
		}
		widths[best]--
	}
}

// shorten fits v into n display cells.
func shorten(v string, n int, mode truncMode, first bool) string {
	if ansi.StringWidth(v) <= n || mode == truncNever || (first && mode == truncAuto) {
		return v
	}
	if mode == truncAuto {
		mode = truncEnd
		if looksLikeID(v) {
			mode = truncMiddle
		}
	}
	if mode == truncMiddle {
		return middleEllipsis(v, n)
	}
	return ansi.Truncate(v, n, "…")
}

// looksLikeID is true for a single token with no spaces, long enough to be an
// identifier, hash, URL or path.
func looksLikeID(v string) bool {
	if len(v) < 12 {
		return false
	}
	for _, r := range v {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// middleEllipsis keeps the head and tail of v: "principal-4c74…e8bea6".
func middleEllipsis(v string, n int) string {
	r := []rune(v)
	if len(r) <= n {
		return v
	}
	if n <= 1 {
		return "…"
	}
	keep := n - 1
	head := (keep + 1) / 2
	tail := keep - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// ---- human-friendly values ----------------------------------------------

// relTime renders t relative to now in tables ("3m ago"), or the exact
// RFC3339 time with -o wide. The zero time is "—".
func relTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	if wideOutput() {
		return t.UTC().Format(time.RFC3339)
	}
	return relTimeFrom(t, time.Now())
}

func relTimeFrom(t, now time.Time) string {
	d := now.Sub(t)
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		s = fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		s = fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		s = fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		s = fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.UTC().Format("2006-01-02")
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}

// humanDuration renders milliseconds compactly: 850ms, 1.2s, 3m4s.
func humanDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", ms)
	case d < time.Minute:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	}
	return d.Round(time.Second).String()
}

// shortMoney renders a fixed-point amount for a table: "USD 0.000006
// (estimated)", "USD 12.40 (settled)": trailing zeros beyond cents trimmed,
// basis always shown. Unknown stays "unknown"; it is never shown as 0.
func shortMoney(currency, amount string, scale uint8, basis string) string {
	if basis != "settled" && basis != "estimated" {
		return usageUnknown
	}
	value, err := formatFixed(amount, scale)
	if err != nil {
		return "invalid"
	}
	return currency + " " + value + " (" + basis + ")"
}

// formatFixed renders an integer amount of minor units at scale as a decimal,
// trimming trailing zeros beyond two decimals.
func formatFixed(amount string, scale uint8) (string, error) {
	if amount == "" {
		return "", fmt.Errorf("empty amount")
	}
	for _, r := range amount {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("invalid amount %q", amount)
		}
	}
	s := strings.TrimLeft(amount, "0")
	n := int(scale)
	if n == 0 {
		if s == "" {
			return "0", nil
		}
		return s, nil
	}
	for len(s) <= n {
		s = "0" + s
	}
	whole, frac := s[:len(s)-n], s[len(s)-n:]
	frac = strings.TrimRight(frac, "0")
	for len(frac) < 2 {
		frac += "0"
	}
	return whole + "." + frac, nil
}

// shortPrincipal renders a canonical principal ID for a table when no display
// name is known: "principal-4c74…bea6".
func shortPrincipal(id string) string {
	if id == "" {
		return "—"
	}
	if strings.HasPrefix(id, "principal-") && len(id) > 24 {
		return middleEllipsis(id, 21)
	}
	return id
}
