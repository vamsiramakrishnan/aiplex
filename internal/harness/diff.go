package harness

import (
	"fmt"
	"strings"
)

const diffContext = 3

// UnifiedDiff renders a unified diff from before to after. It returns ""
// when the two are identical. A nil before means the file does not exist yet.
func UnifiedDiff(path string, before, after []byte, existed bool) string {
	return unifiedDiff(path, before, after, existed, "")
}

func diffHeader(path string, existed bool, newName string) string {
	oldName := diffName("a", path)
	if !existed {
		oldName = "/dev/null"
	}
	if newName == "" {
		newName = diffName("b", path)
	}
	return fmt.Sprintf("--- %s\n+++ %s\n", oldName, newName)
}

func unifiedDiff(path string, before, after []byte, existed bool, newName string) string {
	if existed && string(before) == string(after) {
		return ""
	}
	a, b := splitLines(string(before)), splitLines(string(after))
	ops := diffOps(a, b)
	var sb strings.Builder
	sb.WriteString(diffHeader(path, existed, newName))

	var changes []int
	for i, op := range ops {
		if op.kind != ' ' {
			changes = append(changes, i)
		}
	}
	for k := 0; k < len(changes); {
		start := max(changes[k]-diffContext, 0)
		stop := min(changes[k]+diffContext+1, len(ops))
		k++
		for k < len(changes) && changes[k]-diffContext <= stop {
			stop = min(changes[k]+diffContext+1, len(ops))
			k++
		}
		oldStart, newStart, oldN, newN := 1, 1, 0, 0
		for _, op := range ops[:start] {
			if op.kind != '+' {
				oldStart++
			}
			if op.kind != '-' {
				newStart++
			}
		}
		for _, op := range ops[start:stop] {
			if op.kind != '+' {
				oldN++
			}
			if op.kind != '-' {
				newN++
			}
		}
		if oldN == 0 {
			oldStart--
		}
		if newN == 0 {
			newStart--
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", oldStart, oldN, newStart, newN)
		for _, op := range ops[start:stop] {
			fmt.Fprintf(&sb, "%c%s\n", op.kind, op.text)
		}
	}
	return sb.String()
}

// diffName is git's a/ and b/ prefix, without a doubled slash for absolute paths.
func diffName(prefix, path string) string {
	if strings.HasPrefix(path, "/") {
		return prefix + path
	}
	return prefix + "/" + path
}

type diffOp struct {
	kind byte // ' ', '-', '+'
	text string
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func diffOps(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	var ops []diffOp
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	if len(ma)*len(mb) > 4_000_000 {
		// Too large for the quadratic table: show a block replacement.
		for _, l := range ma {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range mb {
			ops = append(ops, diffOp{'+', l})
		}
	} else {
		ops = append(ops, lcsOps(ma, mb)...)
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	t := make([][]int32, n+1)
	for i := range t {
		t[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else if t[i+1][j] >= t[i][j+1] {
				t[i][j] = t[i+1][j]
			} else {
				t[i][j] = t[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}
