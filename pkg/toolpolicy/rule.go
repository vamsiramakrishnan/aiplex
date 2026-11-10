// Package toolpolicy evaluates operator-written policy expressions on MCP tool
// calls. An expression is a CEL boolean over a small declared set of verified
// facts (the tool, its arguments, the caller, the access point, the request
// time). Expressions are compiled and type-checked when the connection is
// saved, and evaluation is bounded in size, cost and time.
//
// Rules decide allow or deny on structured facts; they never transform
// content. Evaluation fails closed: a rule that cannot be evaluated refuses
// the call with a distinct error class.
package toolpolicy

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Effect is what a matching rule does.
type Effect string

const (
	Deny  Effect = "deny"
	Allow Effect = "allow"
)

// Limits. They are part of the contract and are documented.
const (
	MaxRules           = 64
	MaxExpressionBytes = 1024
	MaxASTNodes        = 256
	MaxMessageBytes    = 200
	MaxDescription     = 500
	// MaxEvalCost bounds one expression's evaluation (CEL cost units).
	MaxEvalCost = 10_000
	// MaxArgsBytes bounds the JSON arguments evaluated; larger arguments are
	// refused without evaluating anything.
	MaxArgsBytes = 64 << 10
	// MaxHelperString bounds the strings the helper functions inspect.
	MaxHelperString = 8192
	MaxPatternBytes = 256
)

// Rule is one policy rule.
type Rule struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	// When is a CEL expression that must evaluate to a boolean.
	When   string `json:"when"`
	Effect Effect `json:"effect"`
	// Message is shown to the caller when the rule refuses a call. It must be
	// safe to publish; it is never templated.
	Message string `json:"message,omitempty"`
}

var ruleID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// Validate checks a rule's static fields (not its expression).
func (r Rule) Validate() error {
	if !ruleID.MatchString(r.ID) {
		return fmt.Errorf("rule id must match [a-z][a-z0-9_-]{0,62}")
	}
	if r.Effect != Deny && r.Effect != Allow {
		return fmt.Errorf("rule %s: effect must be deny or allow", r.ID)
	}
	if strings.TrimSpace(r.When) == "" {
		return fmt.Errorf("rule %s: when is required", r.ID)
	}
	if len(r.When) > MaxExpressionBytes {
		return fmt.Errorf("rule %s: when is bounded to %d bytes", r.ID, MaxExpressionBytes)
	}
	if !utf8.ValidString(r.When) {
		return fmt.Errorf("rule %s: when must be valid UTF-8", r.ID)
	}
	if len(r.Description) > MaxDescription || !printable(r.Description) {
		return fmt.Errorf("rule %s: description is bounded to %d printable characters", r.ID, MaxDescription)
	}
	if len(r.Message) > MaxMessageBytes || !printable(r.Message) {
		return fmt.Errorf("rule %s: message is bounded to %d printable characters", r.ID, MaxMessageBytes)
	}
	return nil
}

func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) || c == unicode.ReplacementChar {
			return false
		}
	}
	return true
}
