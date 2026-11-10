package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Tool policy rules (AIPlex). Attributes carry the decision class and the
// deciding rule identifier only, never arguments, caller facts or messages.
const (
	// AttrToolPolicyDecision is tool_policy_denied or tool_policy_error on an
	// exchange a policy rule refused.
	AttrToolPolicyDecision = "aiplex.tool_policy.decision"
	// AttrToolPolicyRule is the identifier of the rule that decided; absent
	// for a default deny (allow rules present, none matched).
	AttrToolPolicyRule = "aiplex.tool_policy.rule"

	// MetricToolPolicyDecisions counts rule-set evaluations by decision
	// (allow, deny or error). The only label is the decision: no rule id,
	// tool, principal or connection.
	MetricToolPolicyDecisions = "aiplex.tool_policy.decisions"
)

// toolPolicyInstruments counts policy decisions on tool calls.
type toolPolicyInstruments struct {
	decisions metric.Int64Counter
}

func newToolPolicyInstruments(counter func(name, unit, description string) metric.Int64Counter) toolPolicyInstruments {
	return toolPolicyInstruments{decisions: counter(MetricToolPolicyDecisions, "{decision}", "Policy-rule decisions on managed tool calls by decision (allow, deny, error).")}
}

// RecordToolPolicy records one rule-set evaluation. The label is bounded.
func (in *Instruments) RecordToolPolicy(ctx context.Context, decision string) {
	if in == nil || in.toolPolicy.decisions == nil {
		return
	}
	in.toolPolicy.decisions.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrToolPolicyDecision, boundedLabel(decision, "allow", "deny", "error"))))
}
