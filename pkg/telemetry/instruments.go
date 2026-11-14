package telemetry

import (
	"context"
	"math"
	"math/big"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

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
	otlpIngest          metric.Int64Counter
	tagRejected         metric.Int64Counter
	hooks               hookInstruments
	routing             routingInstruments
	toolPolicy          toolPolicyInstruments
	quota               *quotaInstruments
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
		otlpIngest:          counter(MetricOTLPIngest, "{record}", "Records, data points and spans received over OTLP from harnesses, by signal, harness and outcome (recorded, duplicate, rejected, dropped, forwarded)."),
		tagRejected:         counter(MetricTagRejected, "{tag}", "Record tags not stored, by reason (not_allowed, invalid, overflow)."),
		hooks:               newHookInstruments(histogram, counter),
		routing:             newRoutingInstruments(m, counter),
		toolPolicy:          newToolPolicyInstruments(counter),
		quota:               &quotaInstruments{meter: m},
	}
}

// RecordOTLP counts n received OTLP items. harness is a bounded label
// (claude-code, codex, gemini-cli or unknown).
func (in *Instruments) RecordOTLP(ctx context.Context, signal, harness, outcome string, n int64) {
	if in == nil || n <= 0 {
		return
	}
	in.otlpIngest.Add(ctx, n, metric.WithAttributes(attribute.String(AttrOTLPSignal, signal), attribute.String(AttrHarness, harness), attribute.String(AttrOTLPOutcome, outcome)))
}

// RecordTagRejected counts n tags that were not stored.
func (in *Instruments) RecordTagRejected(ctx context.Context, reason string, n int64) {
	if in == nil || n <= 0 {
		return
	}
	in.tagRejected.Add(ctx, n, metric.WithAttributes(attribute.String(AttrTagReason, reason)))
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

// QuotaUnknown is the state of QuotaPlex attempts whose outcome was never
// learned, as of the last complete recovery scan. Unknown amounts are never
// reported as zero: an exposure that is not Complete carries only the known
// part, and Uncomputed counts the attempts whose amount is missing from it.
type QuotaUnknown struct {
	Attempts   int64
	Undated    int64
	Unreadable int64
	// OldestAge is meaningful only when OldestAgeKnown is true.
	OldestAge      time.Duration
	OldestAgeKnown bool
	// ScanAge is the time since the stalest tenant's last complete scan, so
	// a dashboard can tell fresh figures from figures kept after failures.
	ScanAge  time.Duration
	Exposure []QuotaExposure
}

// QuotaExposure is the admitted bound held on one meter. KnownAttempts is
// how many attempts contributed a known amount to KnownPart; with none, the
// exposure gauge is not observed at all rather than reported as 0.
type QuotaExposure struct {
	Meter, Unit, Currency string
	Scale                 int
	KnownPart             int64
	KnownAttempts         int64
	Complete              bool
	Uncomputed            int64
}

type quotaInstruments struct {
	meter    metric.Meter
	once     sync.Once
	snapshot atomic.Pointer[func() (QuotaUnknown, bool)]
}

var quotaUnits = []string{"tokens", "requests", "calls", "milliseconds", "bytes", "rows", "money", "unknown"}

func quotaLabel(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

// quotaExposureGroup is one emitted label set of the exposure gauges.
type quotaExposureGroup struct {
	attrs         []attribute.KeyValue
	known         int64
	knownAttempts int64
	uncomputed    int64
	complete      bool
}

// groupQuotaExposure merges entries that map to the same labels (for example
// two non-standard units, both labelled "other"), so one observation per
// label set carries their sum instead of one replacing the other. A group is
// incomplete when any member is, or when its known sum would overflow (the
// known part then stays a lower bound).
func groupQuotaExposure(entries []QuotaExposure) []quotaExposureGroup {
	index := map[string]int{}
	var out []quotaExposureGroup
	for _, e := range entries {
		attrs := []attribute.KeyValue{
			attribute.String(AttrQuotaMeter, quotaLabel(e.Meter)),
			attribute.String(AttrQuotaUnit, boundedLabel(quotaLabel(e.Unit), quotaUnits...)),
		}
		if e.Currency != "" || e.Unit == "money" {
			attrs = append(attrs, attribute.String(AttrQuotaCurrency, quotaLabel(e.Currency)), attribute.String(AttrQuotaScale, strconv.Itoa(e.Scale)))
		}
		set := attribute.NewSet(attrs...)
		key := set.Encoded(attribute.DefaultEncoder())
		i, ok := index[key]
		if !ok {
			index[key] = len(out)
			out = append(out, quotaExposureGroup{attrs: attrs, known: e.KnownPart, knownAttempts: e.KnownAttempts, uncomputed: e.Uncomputed, complete: e.Complete})
			continue
		}
		g := &out[i]
		if e.KnownPart > 0 && g.known > math.MaxInt64-e.KnownPart {
			g.complete = false
		} else {
			g.known += e.KnownPart
		}
		g.knownAttempts += e.KnownAttempts
		g.uncomputed += e.Uncomputed
		g.complete = g.complete && e.Complete
	}
	return out
}

// ObserveQuotaUnknown sets the source of the unknown-attempt gauges and
// registers them once per instrument set. snapshot returns false until a
// first complete scan exists; nothing is observed then, rather than zero.
func (in *Instruments) ObserveQuotaUnknown(snapshot func() (QuotaUnknown, bool)) {
	if in == nil || in.quota == nil || in.quota.meter == nil || snapshot == nil {
		return
	}
	q := in.quota
	q.snapshot.Store(&snapshot)
	q.once.Do(func() {
		m := q.meter
		attempts, err1 := m.Int64ObservableGauge(MetricQuotaUnknownAttempts, metric.WithUnit("{attempt}"), metric.WithDescription("QuotaPlex attempts in the unknown state (dispatched, outcome never learned) at the last complete recovery scan. They keep holding budget."))
		undated, err2 := m.Int64ObservableGauge(MetricQuotaUnknownUndated, metric.WithUnit("{attempt}"), metric.WithDescription("Unknown attempts without a valid dispatch time; while nonzero the oldest age is unknown and not reported."))
		unreadable, err3 := m.Int64ObservableGauge(MetricQuotaUnknownUnreadable, metric.WithUnit("{attempt}"), metric.WithDescription("Attempt records the last scan could not read; while nonzero the other unknown-attempt gauges may be understated."))
		age, err4 := m.Float64ObservableGauge(MetricQuotaUnknownOldestAge, metric.WithUnit("s"), metric.WithDescription("Age since dispatch of the oldest unknown attempt, at the last complete scan. Not reported when there is none or when it is unknown."))
		exposure, err5 := m.Int64ObservableGauge(MetricQuotaUnknownExposure, metric.WithUnit("{quantity}"), metric.WithDescription("Admitted bound held by unknown attempts, in units of the meter (aiplex.quota.unit; money in minor units at aiplex.quota.scale). With aiplex.quota.exposure.complete=false it is only the known part."))
		scanAge, err7 := m.Float64ObservableGauge(MetricQuotaUnknownScanAge, metric.WithUnit("s"), metric.WithDescription("Time since the last complete recovery scan the unknown-attempt gauges come from (the stalest tenant). It grows while scans keep failing."))
		uncomputed, err6 := m.Int64ObservableGauge(MetricQuotaUnknownUncomputed, metric.WithUnit("{attempt}"), metric.WithDescription("Unknown attempts whose exposure on the meter could not be computed and is not in "+MetricQuotaUnknownExposure+"."))
		for _, err := range []error{err1, err2, err3, err4, err5, err6, err7} {
			if err != nil {
				return
			}
		}
		_, _ = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
			source := q.snapshot.Load()
			if source == nil {
				return nil
			}
			s, ok := (*source)()
			if !ok {
				return nil
			}
			o.ObserveInt64(attempts, s.Attempts)
			o.ObserveInt64(undated, s.Undated)
			o.ObserveInt64(unreadable, s.Unreadable)
			o.ObserveFloat64(scanAge, s.ScanAge.Seconds())
			if s.Attempts > 0 && s.OldestAgeKnown {
				o.ObserveFloat64(age, s.OldestAge.Seconds())
			}
			for _, g := range groupQuotaExposure(s.Exposure) {
				o.ObserveInt64(uncomputed, g.uncomputed, metric.WithAttributes(g.attrs...))
				if g.knownAttempts > 0 {
					o.ObserveInt64(exposure, g.known, metric.WithAttributes(append(g.attrs, attribute.Bool(AttrQuotaExposureComplete, g.complete))...))
				}
			}
			return nil
		}, attempts, undated, unreadable, age, exposure, uncomputed, scanAge)
	})
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
