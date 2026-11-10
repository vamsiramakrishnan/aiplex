// Package telemetry sets up OpenTelemetry tracing and metrics for aiplex-api.
//
// Export is off unless an OTLP endpoint is configured with the standard
// OTEL_* environment variables; with it off nothing is sent anywhere, but
// trace and span identifiers are still generated so logs, responses and the
// usage ledger correlate. Metrics are always served in Prometheus text format
// by MetricsHandler (a pull endpoint, no network calls).
//
// Exporting is asynchronous with bounded queues: a slow or failing collector
// never fails or slows a request. The first export error is logged once,
// without header values.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Options configure Setup. Production passes ServiceVersion only; Getenv and
// SpanExporter are test seams.
type Options struct {
	ServiceVersion string
	// Getenv replaces os.Getenv.
	Getenv func(string) string
	// SpanExporter, when set, receives spans synchronously and replaces the
	// OTLP trace exporter (tests).
	SpanExporter sdktrace.SpanExporter
}

// Telemetry is the process's configured providers.
type Telemetry struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	metrics        http.Handler
	// TracesExported and MetricsExported report whether OTLP export is on.
	TracesExported, MetricsExported bool
	// PropagateUpstream reports whether upstream requests carry traceparent.
	PropagateUpstream bool
}

type state struct {
	tracer      trace.Tracer
	instruments *Instruments
	propagate   bool
}

var current atomic.Pointer[state]

func init() {
	current.Store(&state{tracer: idOnlyProvider().Tracer(InstrumentationName), instruments: newInstruments(metricnoop.NewMeterProvider().Meter(InstrumentationName)), propagate: true})
}

// idOnlyProvider generates trace and span identifiers but exports nothing.
// It keeps a caller's sampled flag so propagation upstream stays faithful.
func idOnlyProvider() *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())))
}

// Tracer returns the AIPlex tracer. It is always usable: before Setup (and
// with export off) its spans carry real identifiers and are not exported.
func Tracer() trace.Tracer { return current.Load().tracer }

// Metrics returns the AIPlex instruments (no-ops before Setup).
func Metrics() *Instruments { return current.Load().instruments }

// PropagateUpstream reports whether trace context is injected on upstream
// requests.
func PropagateUpstream() bool { return current.Load().propagate }

// Setup configures tracing and metrics from the environment and installs
// them for Tracer and Metrics. Invalid configuration is an error: a typo in
// OTEL_EXPORTER_OTLP_PROTOCOL must not silently disable export.
func Setup(ctx context.Context, opts Options) (*Telemetry, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	propagate := true
	if v := strings.TrimSpace(getenv(EnvPropagateUpstream)); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s must be true or false, got %q", EnvPropagateUpstream, v)
		}
		propagate = b
	}
	if legacy := strings.TrimSpace(getenv(EnvLegacyEndpoint)); legacy != "" && getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		// Deprecated alias. The exporters read the standard variable from the
		// process environment themselves.
		base := getenv
		getenv = func(name string) string {
			if name == "OTEL_EXPORTER_OTLP_ENDPOINT" {
				return legacy
			}
			return base(name)
		}
		if opts.Getenv == nil {
			if err := os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", legacy); err != nil {
				return nil, err
			}
		}
		log.Warn().Msg("OTEL_ENDPOINT is deprecated; set OTEL_EXPORTER_OTLP_ENDPOINT to the same value")
	}
	disabled := strings.EqualFold(strings.TrimSpace(getenv("OTEL_SDK_DISABLED")), "true")
	traces, err := signalEnabled(getenv, "TRACES", disabled)
	if err != nil {
		return nil, err
	}
	metricsOn, err := signalEnabled(getenv, "METRICS", disabled)
	if err != nil {
		return nil, err
	}
	res, err := newResource(ctx, opts.ServiceVersion)
	if err != nil {
		return nil, err
	}
	installErrorHandler(getenv)

	t := &Telemetry{PropagateUpstream: propagate}
	var tpOpts []sdktrace.TracerProviderOption
	switch {
	case opts.SpanExporter != nil:
		tpOpts = append(tpOpts, sdktrace.WithResource(res), sdktrace.WithSyncer(opts.SpanExporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
		t.TracesExported = true
	case traces:
		exporter, err := traceExporter(ctx, protocol(getenv, "TRACES"))
		if err != nil {
			return nil, err
		}
		// The batch processor queues (bounded, OTEL_BSP_* sized) and drops
		// when full; export happens on its own goroutine. The sampler comes
		// from OTEL_TRACES_SAMPLER (default parentbased_always_on).
		tpOpts = append(tpOpts, sdktrace.WithResource(res), sdktrace.WithBatcher(exporter))
		t.TracesExported = true
	default:
		tpOpts = append(tpOpts, sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())))
	}
	t.tracerProvider = sdktrace.NewTracerProvider(tpOpts...)

	registry := prometheus.NewRegistry()
	promExporter, err := otelprom.New(otelprom.WithRegisterer(registry), otelprom.WithoutScopeInfo())
	if err != nil {
		return nil, fmt.Errorf("prometheus exporter: %w", err)
	}
	// Exemplars would attach trace identifiers to metric points; metrics
	// carry no per-request identifiers.
	mpOpts := []sdkmetric.Option{sdkmetric.WithResource(res), sdkmetric.WithReader(promExporter), sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter)}
	if metricsOn {
		exporter, err := metricExporter(ctx, protocol(getenv, "METRICS"))
		if err != nil {
			return nil, err
		}
		// Periodic export (OTEL_METRIC_EXPORT_INTERVAL, default 60s) on its
		// own goroutine.
		mpOpts = append(mpOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)))
		t.MetricsExported = true
	}
	t.meterProvider = sdkmetric.NewMeterProvider(mpOpts...)
	t.metrics = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	current.Store(&state{tracer: t.tracerProvider.Tracer(InstrumentationName), instruments: newInstruments(t.meterProvider.Meter(InstrumentationName)), propagate: propagate})
	return t, nil
}

// MetricsHandler serves the Prometheus text exposition of AIPlex metrics.
func (t *Telemetry) MetricsHandler() http.Handler {
	if t == nil || t.metrics == nil {
		return http.NotFoundHandler()
	}
	return t.metrics
}

// Shutdown flushes and stops exporters. It is bounded by ctx.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var errs []error
	if t.tracerProvider != nil {
		errs = append(errs, t.tracerProvider.Shutdown(ctx))
	}
	if t.meterProvider != nil {
		errs = append(errs, t.meterProvider.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

// signalEnabled reports whether OTLP export is configured for a signal
// ("TRACES" or "METRICS").
func signalEnabled(getenv func(string) string, signal string, disabled bool) (bool, error) {
	exporter := strings.ToLower(strings.TrimSpace(getenv("OTEL_" + signal + "_EXPORTER")))
	switch exporter {
	case "", "otlp":
	case "none":
		return false, nil
	case "prometheus":
		if signal == "METRICS" {
			// /metrics is always served; nothing else to do.
			return false, nil
		}
		fallthrough
	default:
		return false, fmt.Errorf("OTEL_%s_EXPORTER=%q is not supported (otlp or none)", signal, exporter)
	}
	if disabled {
		return false, nil
	}
	endpoint := getenv("OTEL_EXPORTER_OTLP_" + signal + "_ENDPOINT")
	if endpoint == "" {
		endpoint = getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if endpoint == "" {
		return false, nil
	}
	if p := protocol(getenv, signal); p != "grpc" && p != "http/protobuf" {
		return false, fmt.Errorf("OTLP protocol %q is not supported (grpc or http/protobuf)", p)
	}
	return true, nil
}

func protocol(getenv func(string) string, signal string) string {
	p := strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_" + signal + "_PROTOCOL"))
	if p == "" {
		p = strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	}
	if p == "" {
		p = "http/protobuf"
	}
	return p
}

// The exporters read endpoint, headers, TLS, compression and timeout from
// the OTEL_EXPORTER_OTLP_* variables themselves.
func traceExporter(ctx context.Context, protocol string) (sdktrace.SpanExporter, error) {
	if protocol == "grpc" {
		return otlptracegrpc.New(ctx)
	}
	return otlptracehttp.New(ctx)
}

func metricExporter(ctx context.Context, protocol string) (sdkmetric.Exporter, error) {
	if protocol == "grpc" {
		return otlpmetricgrpc.New(ctx)
	}
	return otlpmetrichttp.New(ctx)
}

func newResource(ctx context.Context, version string) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{attribute.String("service.name", DefaultServiceName)}
	if version != "" {
		attrs = append(attrs, attribute.String("service.version", version))
	}
	// Later detectors win: OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES
	// override the defaults.
	return resource.New(ctx, resource.WithAttributes(attrs...), resource.WithTelemetrySDK(), resource.WithFromEnv())
}

var errorOnce sync.Once

// installErrorHandler logs the first OpenTelemetry error once. Configured
// header values (which may hold collector credentials) are redacted.
func installErrorHandler(getenv func(string) string) {
	var secrets []string
	for _, name := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS"} {
		for _, pair := range strings.Split(getenv(name), ",") {
			if _, value, ok := strings.Cut(pair, "="); ok && strings.TrimSpace(value) != "" {
				secrets = append(secrets, strings.TrimSpace(value))
			}
		}
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		errorOnce.Do(func() {
			log.Warn().Str("error", Redact(err.Error(), secrets)).Msg("telemetry export failed; requests are unaffected and further telemetry errors are not logged")
		})
	}))
}

// Redact removes each secret from s and bounds its length.
func Redact(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	if len(s) > 512 {
		s = s[:512] + "..."
	}
	return s
}

// UseTracerProviderForTest installs tp until the returned function runs.
func UseTracerProviderForTest(tp trace.TracerProvider, propagate bool) func() {
	previous := current.Load()
	next := *previous
	next.tracer = tp.Tracer(InstrumentationName)
	next.propagate = propagate
	current.Store(&next)
	return func() { current.Store(previous) }
}

// UseMeterProviderForTest installs mp's instruments until the returned
// function runs.
func UseMeterProviderForTest(mp metric.MeterProvider) func() {
	previous := current.Load()
	next := *previous
	next.instruments = newInstruments(mp.Meter(InstrumentationName))
	current.Store(&next)
	return func() { current.Store(previous) }
}
