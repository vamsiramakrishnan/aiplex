package hooks

import (
	"fmt"
	"math/big"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// RedactConfig lists pattern rules. This is deterministic pattern matching:
// it finds text shaped like the patterns and nothing else. It is not a
// guarantee that personal data or secrets are removed.
type RedactConfig struct {
	Rules []RedactRule `json:"rules"`
}

// RedactRule is a built-in pattern or an operator RE2 pattern, and the
// action taken on a match.
type RedactRule struct {
	ID      string `json:"id"`
	Builtin string `json:"builtin,omitempty"`
	Pattern string `json:"pattern,omitempty"`
	// Action is mask (replace the match with Placeholder) or deny.
	Action      string `json:"action"`
	Placeholder string `json:"placeholder,omitempty"`
}

// Rule actions.
const (
	ActionMask = "mask"
	ActionDeny = "deny"
)

type builtinPattern struct {
	pattern     string
	placeholder string
	check       func(string) bool
}

// Builtins are the built-in redaction patterns.
var builtins = map[string]builtinPattern{
	"email":             {`(?i)\b[a-z0-9._%+\-]+@[a-z0-9\-]+(?:\.[a-z0-9\-]+)*\.[a-z]{2,}\b`, "[EMAIL]", nil},
	"phone":             {`(?:\+\d{1,3}[ .\-]?)?(?:\(\d{3}\)|\b\d{3})[ .\-]\d{3}[ .\-]\d{4}\b|\+\d{8,15}\b`, "[PHONE]", nil},
	"credit_card":       {`\b\d(?:[ \-]?\d){12,18}\b`, "[CREDIT_CARD]", luhn},
	"iban":              {`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]){11,30}\b`, "[IBAN]", ibanValid},
	"us_ssn":            {`\b\d{3}-\d{2}-\d{4}\b`, "[US_SSN]", ssnValid},
	"ipv4":              {`\b(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(?:\.(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}\b`, "[IP_ADDRESS]", nil},
	"ipv6":              {`(?i)(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}`, "[IP_ADDRESS]", ipv6Valid},
	"aws_access_key_id": {`\b(?:AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}\b`, "[AWS_ACCESS_KEY_ID]", nil},
	"github_token":      {`\b(?:gh[pousr]_[A-Za-z0-9]{36,255}|github_pat_[A-Za-z0-9_]{22,255})\b`, "[GITHUB_TOKEN]", nil},
	"jwt":               {`\beyJ[A-Za-z0-9_\-]{4,}\.eyJ[A-Za-z0-9_\-]{4,}\.[A-Za-z0-9_\-]*`, "[JWT]", nil},
	"private_key":       {`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----(?:[\s\S]*?-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----)?`, "[PRIVATE_KEY]", nil},
	"bearer_token":      {`(?i)\bbearer[ \t]+[A-Za-z0-9\-._~+/]{16,}=*`, "[BEARER_TOKEN]", nil},
	"google_api_key":    {`\bAIza[0-9A-Za-z_\-]{35}\b`, "[GOOGLE_API_KEY]", nil},
}

// builtinRegexps are compiled once; regexp.Regexp is safe for concurrent use.
var builtinRegexps = func() map[string]*regexp.Regexp {
	out := map[string]*regexp.Regexp{}
	for name, b := range builtins {
		out[name] = regexp.MustCompile(b.pattern)
	}
	return out
}()

// BuiltinNames lists the built-in pattern names, sorted.
func BuiltinNames() []string {
	out := make([]string, 0, len(builtins))
	for name := range builtins {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type compiledRule struct {
	id          string
	re          *regexp.Regexp
	check       func(string) bool
	action      string
	placeholder string
}

var placeholderPattern = regexp.MustCompile(`^[\[\]A-Za-z0-9_ .:\-<>*#]{1,64}$`)

func compileRedact(cfg RedactConfig) ([]compiledRule, error) {
	if len(cfg.Rules) == 0 || len(cfg.Rules) > MaxRulesPerHook {
		return nil, fmt.Errorf("redact.rules must list 1 to %d rules", MaxRulesPerHook)
	}
	seen := map[string]bool{}
	out := make([]compiledRule, 0, len(cfg.Rules))
	for i, r := range cfg.Rules {
		if !identifier.MatchString(r.ID) || seen[r.ID] {
			return nil, fmt.Errorf("redact.rules[%d].id must be a unique lowercase identifier", i)
		}
		seen[r.ID] = true
		if r.Action != ActionMask && r.Action != ActionDeny {
			return nil, fmt.Errorf("redact.rules[%d].action must be mask or deny", i)
		}
		rule := compiledRule{id: r.ID, action: r.Action, placeholder: r.Placeholder}
		switch {
		case r.Builtin != "" && r.Pattern != "":
			return nil, fmt.Errorf("redact.rules[%d]: set builtin or pattern, not both", i)
		case r.Builtin != "":
			b, ok := builtins[r.Builtin]
			if !ok {
				return nil, fmt.Errorf("redact.rules[%d]: unknown builtin %q (one of %s)", i, r.Builtin, strings.Join(BuiltinNames(), ", "))
			}
			rule.re, rule.check = builtinRegexps[r.Builtin], b.check
			if rule.placeholder == "" {
				rule.placeholder = b.placeholder
			}
		case r.Pattern != "":
			if len(r.Pattern) > MaxPatternLength {
				return nil, fmt.Errorf("redact.rules[%d].pattern is longer than %d bytes", i, MaxPatternLength)
			}
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				// The compile error names the construct, not the input text.
				return nil, fmt.Errorf("redact.rules[%d].pattern is not a valid RE2 expression: %v", i, err)
			}
			if re.MatchString("") {
				return nil, fmt.Errorf("redact.rules[%d].pattern must not match the empty string", i)
			}
			rule.re = re
			if rule.placeholder == "" {
				rule.placeholder = "[" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(r.ID)) + "]"
			}
		default:
			return nil, fmt.Errorf("redact.rules[%d]: builtin or pattern is required", i)
		}
		if !placeholderPattern.MatchString(rule.placeholder) {
			return nil, fmt.Errorf("redact.rules[%d].placeholder must be 1-64 characters of letters, digits, brackets and punctuation", i)
		}
		out = append(out, rule)
	}
	return out, nil
}

type match struct {
	start, end int
	rule       *compiledRule
}

// redactText applies the rules to one text. It returns the masked text, the
// match count per rule id and the id of the first deny rule that matched.
func redactText(rules []compiledRule, text string, counts map[string]int) (string, string) {
	var matches []match
	for i := range rules {
		rule := &rules[i]
		for _, loc := range rule.re.FindAllStringIndex(text, -1) {
			if loc[0] == loc[1] {
				continue
			}
			if rule.check != nil && !rule.check(text[loc[0]:loc[1]]) {
				continue
			}
			matches = append(matches, match{loc[0], loc[1], rule})
		}
	}
	if len(matches) == 0 {
		return text, ""
	}
	// Deny rules are decided on every match, before overlaps are resolved,
	// so a mask rule can never hide a deny match.
	denied := ""
	masks := matches[:0:0]
	for _, m := range matches {
		if m.rule.action == ActionDeny {
			counts[m.rule.id]++
			if denied == "" {
				denied = m.rule.id
			}
			continue
		}
		masks = append(masks, m)
	}
	// Earliest start wins; among equal starts the longest; then rule order.
	sort.SliceStable(masks, func(i, j int) bool {
		if masks[i].start != masks[j].start {
			return masks[i].start < masks[j].start
		}
		return masks[i].end > masks[j].end
	})
	var b strings.Builder
	at := 0
	for _, m := range masks {
		if m.start < at {
			continue
		}
		counts[m.rule.id]++
		b.WriteString(text[at:m.start])
		b.WriteString(m.rule.placeholder)
		at = m.end
	}
	b.WriteString(text[at:])
	return b.String(), denied
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// luhn validates a 13-19 digit card number.
func luhn(s string) bool {
	d := digits(s)
	if len(d) < 13 || len(d) > 19 {
		return false
	}
	sum := 0
	for i := 0; i < len(d); i++ {
		n := int(d[len(d)-1-i] - '0')
		if i%2 == 1 {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
	}
	return sum%10 == 0
}

// ibanValid applies the ISO 13616 mod-97 check.
func ibanValid(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	rearranged := s[4:] + s[:4]
	var b strings.Builder
	for _, r := range rearranged {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			fmt.Fprintf(&b, "%d", r-'A'+10)
		default:
			return false
		}
	}
	n, ok := new(big.Int).SetString(b.String(), 10)
	return ok && new(big.Int).Mod(n, big.NewInt(97)).Int64() == 1
}

// ssnValid excludes numbers the SSA never issues.
func ssnValid(s string) bool {
	area, group, serial := s[0:3], s[4:6], s[7:11]
	return area != "000" && area != "666" && area[0] != '9' && group != "00" && serial != "0000"
}

func ipv6Valid(s string) bool {
	if strings.Count(s, ":") < 2 {
		return false
	}
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is6()
}
