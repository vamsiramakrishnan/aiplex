package telemetry

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// RemoteParent returns the trace and span ids of a valid inbound W3C
// traceparent header, or empty strings. The span id is the caller's own span
// (for a harness, its LLM request span), which lets a harness's telemetry be
// matched to the managed exchange that continued it.
func RemoteParent(header http.Header) (traceID, spanID string) {
	ctx := propagator.Extract(context.Background(), propagation.HeaderCarrier(header))
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}

// TraceContextFields returns the traceparent and tracestate values to put in
// an MCP request's params._meta for the span in ctx. ok is false when
// upstream propagation is off (AIPLEX_TRACE_PROPAGATE_UPSTREAM=false) or ctx
// has no valid span; tracestate is empty when there is none.
func TraceContextFields(ctx context.Context) (traceparent, tracestate string, ok bool) {
	if !PropagateUpstream() {
		return "", "", false
	}
	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)
	traceparent = carrier.Get("traceparent")
	if traceparent == "" {
		return "", "", false
	}
	return traceparent, carrier.Get("tracestate"), true
}
