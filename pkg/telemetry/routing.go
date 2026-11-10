package telemetry

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// routingInstruments count routed attempts, retries, fallbacks, breaker
// transitions and cache lookups. Every label is a bounded set.
type routingInstruments struct {
	meter       metric.Meter
	attempts    metric.Int64Counter
	retries     metric.Int64Counter
	fallbacks   metric.Int64Counter
	transitions metric.Int64Counter
	cache       metric.Int64Counter
	once        *sync.Once
}

func newRoutingInstruments(m metric.Meter, counter func(name, unit, description string) metric.Int64Counter) routingInstruments {
	return routingInstruments{
		meter:       m,
		attempts:    counter(MetricRoutingAttempts, "{attempt}", "Attempts of routed access-point model exchanges, by outcome class."),
		retries:     counter(MetricRoutingRetries, "{retry}", "Retries on the same backend, by the outcome class that caused them."),
		fallbacks:   counter(MetricRoutingFallbacks, "{fallback}", "Moves to the next backend, by the outcome class that caused them."),
		transitions: counter(MetricBreakerTransition, "{transition}", "Circuit-breaker state changes, by new state."),
		cache:       counter(MetricCacheLookups, "{lookup}", "Response-cache decisions: hit, miss or bypass."),
		once:        &sync.Once{},
	}
}

// routingOutcomes is the label set of outcome classes (pkg/routing).
var routingOutcomes = []string{"ok", "connect_error", "transport_error", "rate_limited", "overloaded", "unavailable", "server_error", "bad_gateway", "gateway_timeout", "request_timeout", "upstream_error", "capacity_refused", "quota_denied", "not_granted", "breaker_open", "refused", "body_mismatch", "deadline_exceeded", "backend_unavailable", "cache_hit"}

func outcomeLabel(v string) attribute.KeyValue {
	return attribute.String(AttrRoutingOutcome, boundedLabel(v, routingOutcomes...))
}

// RecordRoutingAttempt counts one attempt by outcome class.
func (in *Instruments) RecordRoutingAttempt(ctx context.Context, outcome string) {
	if in == nil || in.routing.attempts == nil {
		return
	}
	in.routing.attempts.Add(ctx, 1, metric.WithAttributes(outcomeLabel(outcome)))
}

// RecordRoutingRetry counts a retry on the same backend.
func (in *Instruments) RecordRoutingRetry(ctx context.Context, outcome string) {
	if in == nil || in.routing.retries == nil {
		return
	}
	in.routing.retries.Add(ctx, 1, metric.WithAttributes(outcomeLabel(outcome)))
}

// RecordRoutingFallback counts a move to the next backend.
func (in *Instruments) RecordRoutingFallback(ctx context.Context, outcome string) {
	if in == nil || in.routing.fallbacks == nil {
		return
	}
	in.routing.fallbacks.Add(ctx, 1, metric.WithAttributes(outcomeLabel(outcome)))
}

// RecordBreakerTransition counts a breaker entering state.
func (in *Instruments) RecordBreakerTransition(ctx context.Context, state string) {
	if in == nil || in.routing.transitions == nil {
		return
	}
	in.routing.transitions.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrRoutingBreakerState, boundedLabel(state, "closed", "open", "half_open"))))
}

// RecordCacheLookup counts one cache decision.
func (in *Instruments) RecordCacheLookup(ctx context.Context, result string) {
	if in == nil || in.routing.cache == nil {
		return
	}
	in.routing.cache.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrCacheStatus, boundedLabel(result, "hit", "miss", "bypass"))))
}

// ObserveBreakers registers, once per instrument set, a gauge of how many
// tracked backends are in each breaker state.
func (in *Instruments) ObserveBreakers(counts func() map[string]int64) {
	if in == nil || in.routing.meter == nil || in.routing.once == nil || counts == nil {
		return
	}
	in.routing.once.Do(func() {
		_, _ = in.routing.meter.Int64ObservableGauge(MetricBreakers, metric.WithUnit("{backend}"), metric.WithDescription("Backends of routed models by circuit-breaker state (this process)."),
			metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
				for state, n := range counts() {
					o.Observe(n, metric.WithAttributes(attribute.String(AttrRoutingBreakerState, boundedLabel(state, "closed", "open", "half_open"))))
				}
				return nil
			}))
	})
}
