package workflow

import (
	"fmt"
	"regexp"
	"strings"
)

// templatePattern matches `{{ path.to.value }}` references. Whitespace inside
// the braces is permitted and trimmed. Identifier chars only — no operators,
// no function calls. Safe to evaluate against untrusted-but-shaped data.
var templatePattern = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_\.\[\]\-]*)\s*\}\}`)

// renderString interpolates a template string against the run context. The
// supported syntax is intentionally narrow: `{{ inputs.foo }}` or
// `{{ steps.<id>.output.bar }}`. References that fail to resolve are
// substituted with the empty string and tracked in the returned `missing`
// list so the caller can decide whether to fail-fast or skip.
func renderString(in string, ctx map[string]any) (out string, missing []string) {
	out = templatePattern.ReplaceAllStringFunc(in, func(match string) string {
		expr := strings.TrimSpace(match[2 : len(match)-2])
		val, ok := lookupPath(ctx, expr)
		if !ok {
			missing = append(missing, expr)
			return ""
		}
		return stringify(val)
	})
	return out, missing
}

// renderValue walks a JSON-shaped tree and renders every string. Returns a
// fresh tree with substitutions applied, plus the union of missing references
// across all nested templates.
func renderValue(v any, ctx map[string]any) (any, []string) {
	switch t := v.(type) {
	case string:
		// If the entire value is a single template reference, return the raw
		// referenced value (keeps types — numbers stay numbers, maps stay maps).
		if m := templatePattern.FindStringSubmatch(t); m != nil && m[0] == t {
			expr := strings.TrimSpace(m[1])
			if val, ok := lookupPath(ctx, expr); ok {
				return val, nil
			}
			return nil, []string{expr}
		}
		s, missing := renderString(t, ctx)
		return s, missing
	case map[string]any:
		out := make(map[string]any, len(t))
		var missing []string
		for k, vv := range t {
			rendered, m := renderValue(vv, ctx)
			out[k] = rendered
			missing = append(missing, m...)
		}
		return out, missing
	case []any:
		out := make([]any, len(t))
		var missing []string
		for i, vv := range t {
			rendered, m := renderValue(vv, ctx)
			out[i] = rendered
			missing = append(missing, m...)
		}
		return out, missing
	default:
		return v, nil
	}
}

// lookupPath walks dotted paths like "steps.fetch.output.text" against a
// JSON-shaped map. Bracket indexing ("items[0]") is supported. Returns
// ok=false if any segment is absent.
func lookupPath(root map[string]any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	var cur any = root
	for _, seg := range splitPathSegments(path) {
		switch c := cur.(type) {
		case map[string]any:
			next, ok := c[seg.key]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			if seg.index < 0 || seg.index >= len(c) {
				return nil, false
			}
			cur = c[seg.index]
		default:
			return nil, false
		}
		if seg.index >= 0 {
			// Apply array index against the resolved key value.
			arr, ok := cur.([]any)
			if !ok {
				return nil, false
			}
			if seg.index >= len(arr) {
				return nil, false
			}
			cur = arr[seg.index]
		}
	}
	return cur, true
}

type pathSeg struct {
	key   string
	index int // -1 if not an indexed segment
}

// splitPathSegments parses "steps.fetch.output.items[0].name" into
// [{steps,-1}, {fetch,-1}, {output,-1}, {items,0}, {name,-1}].
func splitPathSegments(path string) []pathSeg {
	parts := strings.Split(path, ".")
	out := make([]pathSeg, 0, len(parts))
	for _, p := range parts {
		idx := -1
		key := p
		if lb := strings.IndexByte(p, '['); lb >= 0 && strings.HasSuffix(p, "]") {
			key = p[:lb]
			n := 0
			_, err := fmt.Sscanf(p[lb+1:len(p)-1], "%d", &n)
			if err == nil {
				idx = n
			}
		}
		out = append(out, pathSeg{key: key, index: idx})
	}
	return out
}

// stringify renders a value the way you'd expect inside a string template.
// Maps and slices marshal-ish; primitives go through Sprintf.
func stringify(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", t)
	}
}
