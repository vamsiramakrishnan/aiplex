package harness

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The diff AIPlex shows before writing, and when undoing, is computed over
// redacted copies of the files. The bytes that are merged, written, backed up
// and restored are never redacted: this file only affects what is displayed.
//
// Rules, in order:
//  1. A string value under a key whose name looks sensitive is replaced.
//     Containers under such a key redact every string below them.
//  2. Any string that looks like a credential is replaced, whatever its key.
//  3. A value that is only an environment reference ($VAR, ${VAR}, or
//     "Bearer ${VAR}") stays visible: it names a variable, not a secret.
//  4. A file that does not parse is redacted line by line with the same rules;
//     if that is not possible only a count of changed lines is shown.

const redacted = "[redacted]"

var (
	// Substrings that make a key name sensitive.
	sensitiveSubstrings = []string{
		"token", "secret", "password", "passwd", "passphrase", "api_key", "apikey", "api-key",
		"authorization", "credential", "cookie", "session", "private_key", "privatekey",
		"client_secret", "refresh", "bearer", "oauth",
	}
	// Keys whose value is a command or an environment-variable name, not a secret.
	exemptKeys = map[string]bool{"apikeyhelper": true, "env_key": true}

	envRef     = regexp.MustCompile(`^\$(\{[A-Za-z_][A-Za-z0-9_]*(:-[^}]*)?\}|[A-Za-z_][A-Za-z0-9_]*)$`)
	bearerRef  = regexp.MustCompile(`^(?i:bearer|basic)\s+\$(\{[A-Za-z_][A-Za-z0-9_]*(:-[^}]*)?\}|[A-Za-z_][A-Za-z0-9_]*)$`)
	bearerLit  = regexp.MustCompile(`(?i)\b(bearer|basic)\s+(\$\{[^}]*\}|[^\s"',;}]+)`)
	tokenInStr = regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9_\-]{6,}|sk-[A-Za-z0-9_\-]{8,}|gh[pousr]_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,}|AIza[0-9A-Za-z_\-]{16,}|xox[abprs]-[A-Za-z0-9\-]{8,}|glpat-[A-Za-z0-9_\-]{8,}|AKIA[0-9A-Z]{12,}|ya29\.[A-Za-z0-9_\-]{8,}|eyJ[A-Za-z0-9_\-]{4,}\.[A-Za-z0-9_\-]{4,}\.[A-Za-z0-9_\-]*)`)
	entropyOK  = regexp.MustCompile(`^[A-Za-z0-9_\-+/=.]{32,}$`)
	kvPair     = regexp.MustCompile(`("[^"\\]+"|'[^']+'|[A-Za-z0-9_.\-]+)(\s*[:=]\s*)("(?:[^"\\]|\\.)*"|'[^']*'|[^\s,}\]]+)`)
	camelSplit = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// keyClass: 0 not sensitive, 1 weak (an "auth" segment), 2 strong.
func keyClass(key string) int {
	k := strings.ToLower(strings.Trim(key, `"' `))
	if exemptKeys[k] || strings.HasSuffix(k, "_env_var") || strings.HasSuffix(k, "_env") {
		return 0
	}
	for _, s := range sensitiveSubstrings {
		if strings.Contains(k, s) {
			return 2
		}
	}
	// "auth" only as a whole segment: auth, x-auth, userAuth; not author.
	spaced := camelSplit.ReplaceAllString(strings.Trim(key, `"' `), "$1 $2")
	for _, seg := range strings.FieldsFunc(strings.ToLower(spaced), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if seg == "auth" {
			return 1
		}
	}
	return 0
}

func isEnvRef(v string) bool {
	v = strings.TrimSpace(v)
	return envRef.MatchString(v) || bearerRef.MatchString(v)
}

func shannon(s string) float64 {
	counts := map[rune]float64{}
	for _, r := range s {
		counts[r]++
	}
	n := float64(len([]rune(s)))
	var h float64
	for _, c := range counts {
		p := c / n
		h -= p * math.Log2(p)
	}
	return h
}

// looksLikeCredential applies the value heuristics to a whole string.
func looksLikeCredential(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || isEnvRef(v) {
		return false
	}
	if bearerLit.MatchString(v) && !isEnvRef(v) {
		return true
	}
	if tokenInStr.MatchString(v) {
		return true
	}
	if entropyOK.MatchString(v) && !strings.Contains(v, "://") && !strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "./") {
		hasDigit, hasLetter := false, false
		for _, r := range v {
			hasDigit = hasDigit || unicode.IsDigit(r)
			hasLetter = hasLetter || unicode.IsLetter(r)
		}
		return hasDigit && hasLetter && shannon(v) >= 3.5
	}
	return false
}

// scrubString removes credential-looking substrings from a string that is
// kept visible, such as a URL or a command line.
func scrubString(v string) string {
	if isEnvRef(v) {
		return v
	}
	v = bearerLit.ReplaceAllStringFunc(v, func(m string) string {
		if isEnvRef(m) {
			return m
		}
		return strings.Fields(m)[0] + " " + redacted
	})
	return tokenInStr.ReplaceAllString(v, redacted)
}

// redactTree returns a copy of v safe to display. inherit means an ancestor
// key was sensitive.
func redactTree(key string, v any, inherit bool) any {
	switch t := v.(type) {
	case *object:
		class := keyClass(key)
		out := newObject()
		for _, k := range t.keys {
			out.set(k, redactTree(k, t.vals[k], inherit || class == 2))
		}
		return out
	case []any:
		class := keyClass(key)
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = redactTree(key, e, inherit || class == 2)
		}
		return out
	case string:
		if isEnvRef(t) {
			return t
		}
		if inherit || keyClass(key) > 0 || looksLikeCredential(t) {
			if strings.TrimSpace(t) == "" {
				return t
			}
			return redacted
		}
		return scrubString(t)
	}
	return v // numbers, booleans and null are configuration, not credentials
}

// redactJSON renders a parsed JSON document redacted, in canonical form.
func redactJSON(data []byte) (string, bool) {
	o, err := parseObject(data)
	if err != nil {
		return "", false
	}
	return string(redactTree("", o, false).(*object).marshal()), true
}

// collectSecrets gathers every string in a parsed TOML tree that rule 1 or 2
// would redact, so they can be removed from the raw text wherever they occur.
func collectSecrets(key string, v any, inherit bool, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		class := keyClass(key)
		for k, e := range t {
			collectSecrets(k, e, inherit || class == 2, out)
		}
	case []any:
		class := keyClass(key)
		for _, e := range t {
			collectSecrets(key, e, inherit || class == 2, out)
		}
	case string:
		if !isEnvRef(t) && strings.TrimSpace(t) != "" && (inherit || keyClass(key) > 0 || looksLikeCredential(t)) {
			*out = append(*out, t)
		}
	}
}

// redactLines is the line-based redaction used for TOML text and for files
// that do not parse. secrets are exact values to remove wherever they appear.
func redactLines(text string, secrets []string) string {
	// Longest first so a value that contains another is removed whole.
	for i := 0; i < len(secrets); i++ {
		for j := i + 1; j < len(secrets); j++ {
			if len(secrets[j]) > len(secrets[i]) {
				secrets[i], secrets[j] = secrets[j], secrets[i]
			}
		}
	}
	for _, s := range secrets {
		for _, part := range strings.Split(s, "\n") {
			if len(strings.TrimSpace(part)) >= 6 {
				text = strings.ReplaceAll(text, part, redacted)
			}
		}
		if enc, err := json.Marshal(s); err == nil && len(enc) > 2 {
			text = strings.ReplaceAll(text, string(enc[1:len(enc)-1]), redacted)
		}
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = redactLine(l)
	}
	return strings.Join(lines, "\n")
}

func redactLine(line string) string {
	line = kvPair.ReplaceAllStringFunc(line, func(m string) string {
		sub := kvPair.FindStringSubmatch(m)
		key, sep, val := sub[1], sub[2], sub[3]
		bare := strings.Trim(val, `"'`)
		if isEnvRef(bare) || bare == redacted || bare == "" {
			return m
		}
		// Documented: numbers and booleans are not redacted. Without this a
		// TOML flag such as requires_openai_auth = false was shown as
		// [redacted] because its name ends in an "auth" segment.
		if val == bare && (bare == "true" || bare == "false" || numberLiteral.MatchString(bare)) {
			return m
		}
		if keyClass(key) > 0 || looksLikeCredential(bare) {
			q := ""
			if len(val) > 0 && (val[0] == '"' || val[0] == '\'') {
				q = string(val[0])
			}
			return key + sep + q + redacted + q
		}
		return m
	})
	return scrubString(line)
}

var numberLiteral = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

const maxFallbackLine = 4000

// Redact returns data in displayable form. ok is false when the content
// cannot be shown safely.
func Redact(path string, data []byte) (string, bool) {
	if !utf8.Valid(data) {
		return "", false
	}
	text := string(data)
	if strings.HasSuffix(path, ".toml") {
		if m, err := parseTOML(text); err == nil {
			var secrets []string
			collectSecrets("", m, false, &secrets)
			return redactLines(text, secrets), true
		}
	} else if out, ok := redactJSON(data); ok {
		return out, true
	}
	for _, l := range strings.Split(text, "\n") {
		if len(l) > maxFallbackLine {
			return "", false
		}
	}
	return redactLines(text, nil), true
}

// Diff is the unified diff of the change as it may be displayed: computed over
// redacted copies of both sides. It is "" when nothing changes. A change that
// differs only inside redacted values is still reported.
func (c Change) Diff() string {
	if !c.Changed() {
		return ""
	}
	after := c.After
	if c.Delete {
		after = nil
	}
	b, bok := Redact(c.Path, c.Before)
	a, aok := Redact(c.Path, after)
	if !bok || !aok {
		ops := diffOps(splitLines(string(c.Before)), splitLines(string(after)))
		n := 0
		for _, op := range ops {
			if op.kind != ' ' {
				n++
			}
		}
		return diffName("a", c.Path) + ": " + strconv.Itoa(n) + " lines changed; content not shown\n"
	}
	newName := ""
	if c.Delete {
		newName = "/dev/null"
	}
	d := unifiedDiff(c.Path, []byte(b), []byte(a), c.Existed, newName)
	if d == "" {
		d = diffHeader(c.Path, c.Existed, newName) + "(a value changed; content redacted)\n"
	}
	return d
}
