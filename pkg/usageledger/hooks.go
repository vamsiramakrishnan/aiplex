package usageledger

import "fmt"

// Hook decisions recorded on a ledger record.
const (
	HookAllow     = "allow"
	HookDeny      = "deny"
	HookTransform = "transform"
	HookError     = "error"
	HookSkipped   = "skipped"
	// HookDecisionNone is a filter value only: records no hook ran on.
	HookDecisionNone = "none"
	// MaxHookResults bounds the results one record carries.
	MaxHookResults = 64
)

// HookSummary records what request hooks decided on an exchange. It never
// holds matched text or content: only hook identifiers, decisions, rule
// identifiers with match counts, and durations.
type HookSummary struct {
	// Decision is the most significant decision of all results: deny, then
	// error, then transform, then allow, then skipped.
	Decision string `json:"decision"`
	// UninspectedParts counts parts (images, audio, files, unknown blocks)
	// that were present but not inspected.
	UninspectedParts int `json:"uninspected_parts"`
	// FailOpenUsed is true when a hook configured with fail_open failed and
	// the exchange continued without its decision.
	FailOpenUsed bool         `json:"fail_open_used,omitempty"`
	Results      []HookResult `json:"results"`
}

// HookResult is one hook run in one phase.
type HookResult struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Phase        string          `json:"phase"`
	Decision     string          `json:"decision"`
	Rules        []HookRuleCount `json:"rules,omitempty"`
	DurationMS   int64           `json:"duration_ms"`
	FailOpenUsed bool            `json:"fail_open_used,omitempty"`
}

// HookRuleCount is a rule identifier and how many times it matched.
type HookRuleCount struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

var hookRank = map[string]int{HookSkipped: 1, HookAllow: 2, HookTransform: 3, HookError: 4, HookDeny: 5}

// Summarize sets Decision and FailOpenUsed from Results.
func (s *HookSummary) Summarize() {
	s.Decision, s.FailOpenUsed = "", false
	for _, r := range s.Results {
		if hookRank[r.Decision] > hookRank[s.Decision] {
			s.Decision = r.Decision
		}
		s.FailOpenUsed = s.FailOpenUsed || r.FailOpenUsed
	}
}

// ValidHookDecision reports whether v is a hook decision filter value.
func ValidHookDecision(v string) bool {
	_, ok := hookRank[v]
	return ok || v == HookDecisionNone
}

func (s *HookSummary) validate() error {
	if s == nil {
		return nil
	}
	if s.UninspectedParts < 0 || len(s.Results) > MaxHookResults {
		return invalid("hooks summary is out of bounds")
	}
	if s.Decision != "" && hookRank[s.Decision] == 0 {
		return invalid("hooks.decision is not a hook decision")
	}
	for i, r := range s.Results {
		if hookRank[r.Decision] == 0 || r.DurationMS < 0 || len(r.Rules) > 256 {
			return invalid("hooks.results[%d] is invalid", i)
		}
		for name, value := range map[string]string{"id": r.ID, "type": r.Type, "phase": r.Phase} {
			if !validText(value, true) {
				return invalid("hooks.results[%d].%s is not bounded printable text", i, name)
			}
		}
		for _, rule := range r.Rules {
			if !validText(rule.ID, true) || rule.Count < 0 {
				return fmt.Errorf("%w: hooks.results[%d] rule is invalid", ErrInvalid, i)
			}
		}
	}
	return nil
}

func hookDecision(r Record) string {
	if r.Hooks == nil || r.Hooks.Decision == "" {
		return HookDecisionNone
	}
	return r.Hooks.Decision
}

func cloneHooks(s *HookSummary) *HookSummary {
	if s == nil {
		return nil
	}
	out := *s
	out.Results = make([]HookResult, len(s.Results))
	for i, r := range s.Results {
		r.Rules = append([]HookRuleCount(nil), r.Rules...)
		out.Results[i] = r
	}
	return &out
}
