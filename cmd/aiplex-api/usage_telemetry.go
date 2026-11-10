package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/otlpingest"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// configureUsageTelemetry sets up what sits beside the ledger: record tags,
// feedback, the trace viewer link and the OTLP receiver for harness
// telemetry. Invalid configuration stops startup, so a typo in a bound or an
// allowlist does not silently open or close a path.
//
//	AIPLEX_TAG_KEYS_ALLOWED, AIPLEX_TAG_MAX_VALUES   caller-supplied tags
//	AIPLEX_FEEDBACK_COMMENTS=true                    free-text feedback comments
//	AIPLEX_TRACE_URL_TEMPLATE                        trace viewer link ({trace_id})
//	AIPLEX_OTLP_FORWARD=true                         forward unrecorded OTLP data
//	AIPLEX_OTLP_*                                    receiver bounds (see api.NewOTLPHandler)
func configureUsageTelemetry(store registry.Store, usage *api.UsageHandler, connected *api.ConnectedHandler, closers *[]func()) (*api.OTLPHandler, error) {
	tags, err := api.TagPolicyFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	connected.Tags, usage.Tags = tags, tags

	if firestore, ok := store.(*registry.FirestoreStore); ok {
		usage.Feedback = firestore.UsageFeedback(usageledger.DefaultRetention)
	} else {
		usage.Feedback = usageledger.NewMemoryFeedbackStore(0)
	}
	if raw := strings.TrimSpace(os.Getenv("AIPLEX_FEEDBACK_COMMENTS")); raw != "" {
		on, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("AIPLEX_FEEDBACK_COMMENTS must be true or false")
		}
		usage.FeedbackComments = on
	}
	template := strings.TrimSpace(os.Getenv(api.TraceURLTemplateEnv))
	if err := api.ValidateTraceURLTemplate(template); err != nil {
		return nil, err
	}
	usage.TraceURLTemplate = template

	otlp, err := connected.NewOTLPHandler(usage.Ledger, os.Getenv)
	if err != nil {
		return nil, err
	}
	forwarder, err := otlpingest.NewForwarder(os.Getenv, nil, func(signal otlpingest.Signal, ok bool) {
		if !ok {
			telemetry.Metrics().RecordOTLP(context.Background(), string(signal), otlpingest.HarnessUnknown, telemetry.OTLPForwardFailed, 1)
		}
	})
	if err != nil {
		return nil, err
	}
	if forwarder != nil {
		otlp.Forwarder = forwarder
		*closers = append(*closers, forwarder.Close)
		log.Info().Str("logs", forwarder.Endpoint(otlpingest.SignalLogs)).Msg("forwarding unrecorded OTLP telemetry (AIPLEX_OTLP_FORWARD)")
	}
	return otlp, nil
}
