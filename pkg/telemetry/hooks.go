package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// hookInstruments count request-hook decisions and time hook runs.
type hookInstruments struct {
	decisions metric.Int64Counter
	duration  metric.Float64Histogram
}

var hookDurationBuckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

func newHookInstruments(histogram func(name, unit, description string, buckets []float64) metric.Float64Histogram, counter func(name, unit, description string) metric.Int64Counter) hookInstruments {
	return hookInstruments{
		decisions: counter(MetricHookDecisions, "{decision}", "Request-hook decisions by hook type, phase and decision."),
		duration:  histogram(MetricHookDuration, "s", "Duration of one request-hook run.", hookDurationBuckets),
	}
}

// boundedLabel keeps metric labels to known values.
func boundedLabel(v string, known ...string) string {
	for _, k := range known {
		if v == k {
			return v
		}
	}
	return "other"
}

// RecordHook records one hook run. Labels are the hook type, phase and
// decision only (bounded); no rule, policy, principal or content.
func (in *Instruments) RecordHook(ctx context.Context, hookType, phase, decision string, seconds float64) {
	if in == nil || in.hooks.decisions == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String(AttrHookType, boundedLabel(hookType, "redact", "size_limit", "http", "policy")),
		attribute.String(AttrHookPhase, boundedLabel(phase, "request", "response")),
		attribute.String(AttrHookDecision, boundedLabel(decision, "allow", "deny", "transform", "error", "skipped")),
	)
	in.hooks.decisions.Add(ctx, 1, attrs)
	in.hooks.duration.Record(ctx, seconds, attrs)
}
