package telemetry

import (
	"context"
	"math/big"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Instruments are the AIPlex metrics. Names are in semconv.go.
type Instruments struct {
	httpDuration        metric.Float64Histogram
	inferenceDuration   metric.Float64Histogram
	timeToFirstChunk    metric.Float64Histogram
	invokeAgentDuration metric.Float64Histogram
	mcpDuration         metric.Float64Histogram
	inputTokens         metric.Int64Counter
	outputTokens        metric.Int64Counter
	cacheReadTokens     metric.Int64Counter
	cacheWriteTokens    metric.Int64Counter
	reasoningTokens     metric.Int64Counter
	cost                metric.Float64Counter
	unknownCost         metric.Int64Counter
	unknownUsage        metric.Int64Counter
	quotaDenials        metric.Int64Counter
	upstreamErrors      metric.Int64Counter
	hooks               hookInstruments
	toolPolicy          toolPolicyInstruments
}

func newInstruments(m metric.Meter) *Instruments {
	noop := metricnoop.NewMeterProvider().Meter(InstrumentationName)
	histogram := func(name, unit, description string, buckets []float64) metric.Float64Histogram {
		h, err := m.Float64Histogram(name, metric.WithUnit(unit), metric.WithDescription(description), metric.WithExplicitBucketBoundaries(buckets...))
		if err != nil {
			h, _ = noop.Float64Histogram(name)
		}
		return h
	}
	counter := func(name, unit, description string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithUnit(unit), metric.WithDescription(description))
		if err != nil {
			c, _ = noop.Int64Counter(name)
		}
		return c
	}
	cost, err := m.Float64Counter(MetricCost, metric.WithDescription("Known cost of managed exchanges, in units of aiplex.cost.currency (settled or estimated, see aiplex.cost.basis). Exchanges with unknown cost are counted by "+MetricUnknownCostRecords+"."))
	if err != nil {
		cost, _ = noop.Float64Counter(MetricCost)
	}
	return &Instruments{
		httpDuration:        histogram(MetricHTTPServerDuration, "s", "Duration of HTTP server requests.", httpDurationBuckets),
		inferenceDuration:   histogram(MetricGenAIInferenceDuration, "s", "Duration of managed model exchanges, from admission to the end of the upstream response.", genAIDurationBuckets),
		timeToFirstChunk:    histogram(MetricGenAITimeToFirstChunk, "s", "Time from the start of a streamed model exchange to the first bytes written to the caller.", genAIDurationBuckets),
		invokeAgentDuration: histogram(MetricGenAIInvokeAgentDur, "s", "Duration of managed agent invocations.", genAIDurationBuckets),
		mcpDuration:         histogram(MetricMCPClientDuration, "s", "Duration of managed MCP tool requests.", mcpDurationBuckets),
		inputTokens:         counter(MetricGenAIUsageInput, "{token}", "Input tokens of managed model exchanges with known final usage, including cached tokens."),
		outputTokens:        counter(MetricGenAIUsageOutput, "{token}", "Output tokens of managed model exchanges with known final usage."),
		cacheReadTokens:     counter(MetricGenAIUsageCacheRead, "{token}", "Input tokens served from a provider cache."),
		cacheWriteTokens:    counter(MetricGenAIUsageCacheWrite, "{token}", "Input tokens written to a provider cache."),
		reasoningTokens:     counter(MetricGenAIUsageReasoning, "{token}", "Output tokens used for reasoning."),
		cost:                cost,
		unknownCost:         counter(MetricUnknownCostRecords, "{exchange}", "Managed exchanges whose cost is unknown (no settlement and no matching price)."),
		unknownUsage:        counter(MetricUnknownUsage, "{exchange}", "Managed exchanges whose usage is unknown or not final (aiplex.usage.bound); their tokens are not in the token counters."),
		quotaDenials:        counter(MetricQuotaDenials, "{exchange}", "Managed exchanges refused by quota admission."),
		upstreamErrors:      counter(MetricUpstreamErrors, "{exchange}", "Managed exchanges that ended in an error or incomplete (upstream errors passed through included), by error.type."),
		hooks:               newHookInstruments(histogram, counter),
		toolPolicy:          newToolPolicyInstruments(counter),
	}
}

// RecordHTTP records one HTTP server request. route is the mounted pattern
// ("" when no route matched).
func (in *Instruments) RecordHTTP(ctx context.Context, method, route string, status int, seconds float64) {
	if in == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String(AttrHTTPRequestMethod, method), attribute.Int(AttrHTTPResponseStatusCode, status)}
	if route != "" {
		attrs = append(attrs, attribute.String(AttrHTTPRoute, route))
	}
	if status >= 500 {
		attrs = append(attrs, attribute.String(AttrErrorType, statusText(status)))
	}
	in.httpDuration.Record(ctx, seconds, metric.WithAttributes(attrs...))
}

// costFloat converts a known cost to currency units for the cost counter.
// Metrics are approximate (float); the ledger keeps the exact amount.
func costFloat(c usageledger.Cost) (float64, bool) {
	if c.Basis == "" || c.Basis == usageledger.BasisUnknown || c.Currency == "" {
		return 0, false
	}
	amount, ok := c.AmountInt()
	if !ok {
		return 0, false
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(c.Scale)), nil)
	value, _ := new(big.Rat).SetFrac(amount, scale).Float64()
	return value, true
}
