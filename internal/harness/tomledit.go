package harness

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// TOML libraries drop comments and reorder keys when they rewrite a file, so
// the Codex config is edited as text: AIPlex owns two delimited blocks and
// everything else is left byte for byte. A parse of the result, compared with
// a parse of the original, proves that nothing outside the blocks changed.
const (
	topBegin   = "# >>> aiplex connect: top-level keys (managed; undo with `aiplex connect --undo`)"
	topEnd     = "# <<< aiplex connect: top-level keys"
	tableBegin = "# >>> aiplex connect: tables (managed; undo with `aiplex connect --undo`)"
	tableEnd   = "# <<< aiplex connect: tables"
	disabled   = "# aiplex disabled: "
)

var (
	tableHeader = regexp.MustCompile(`^\s*\[\[?[^\]\n]+\]\]?\s*(#.*)?$`)
	topKeyLine  = regexp.MustCompile(`^(model|model_provider)\s*=`)
)

func parseTOML(text string) (map[string]any, error) {
	m := map[string]any{}
	if err := toml.Unmarshal([]byte(text), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// stripBlock removes a delimited block and the single blank line that
// separates it from the rest.
func stripBlock(text, begin, end string, blankAfter bool) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	in := false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case !in && l == begin:
			in = true
			if !blankAfter && len(out) > 0 && out[len(out)-1] == "" {
				out = out[:len(out)-1]
			}
		case in && l == end:
			in = false
			if blankAfter && i+1 < len(lines) && lines[i+1] == "" {
				i++
			}
		case !in:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// MergeTOML installs the managed blocks into a Codex config file. top holds
// top-level keys (they are placed first, where TOML allows them); tables holds
// table sections (appended). ownedTop lists the top-level keys the block sets,
// and owned lists dotted table paths the blocks define, for the preservation
// check.
func MergeTOML(before string, top, tables string, ownedTop []string, owned [][]string) (string, error) {
	if _, err := parseTOML(before); err != nil {
		return "", fmt.Errorf("the existing file is not valid TOML (%v); fix it first, nothing was changed", err)
	}
	body := stripBlock(stripBlock(before, tableBegin, tableEnd, false), topBegin, topEnd, true)
	userText := body
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}

	// A top-level key we set must not also exist in the user's own text.
	if top != "" {
		lines := strings.Split(body, "\n")
		for i, l := range lines {
			if tableHeader.MatchString(l) {
				break
			}
			if topKeyLine.MatchString(l) {
				lines[i] = disabled + l
			}
		}
		body = strings.Join(lines, "\n")
	}

	var sb strings.Builder
	if top != "" {
		sb.WriteString(topBegin + "\n" + strings.TrimRight(top, "\n") + "\n" + topEnd + "\n")
		if body != "" {
			sb.WriteString("\n")
		}
	}
	sb.WriteString(body)
	if tables != "" {
		if body != "" || top != "" {
			sb.WriteString("\n")
		}
		sb.WriteString(tableBegin + "\n" + strings.TrimRight(tables, "\n") + "\n" + tableEnd + "\n")
	}
	after := sb.String()

	got, err := parseTOML(after)
	if err != nil {
		return "", fmt.Errorf("the merged file would not be valid TOML (%v). It probably defines a table AIPlex also needs (for example [otel] or [model_providers.aiplex]); remove or rename it, or configure the harness by hand", err)
	}
	orig, err := parseTOML(userText)
	if err != nil {
		return "", fmt.Errorf("the existing file is not valid TOML outside the AIPlex blocks (%v)", err)
	}
	for _, k := range ownedTop {
		delete(orig, k)
		delete(got, k)
	}
	for _, path := range owned {
		deletePath(orig, path)
		deletePath(got, path)
	}
	if !reflect.DeepEqual(orig, got) {
		return "", fmt.Errorf("refusing to write: the merge would change settings outside the AIPlex blocks; configure the harness by hand")
	}
	return after, nil
}

func deletePath(m map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	if len(path) == 1 {
		delete(m, path[0])
		return
	}
	next, ok := m[path[0]].(map[string]any)
	if !ok {
		return
	}
	deletePath(next, path[1:])
	if len(next) == 0 { // a table that existed only to hold our entry
		delete(m, path[0])
	}
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			sb.WriteString(`\"`)
		case r == '\\':
			sb.WriteString(`\\`)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\t':
			sb.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&sb, `\u%04X`, r)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	return tomlString(k)
}
