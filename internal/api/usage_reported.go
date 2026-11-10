package api

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// reportedUsageLedger mirrors POST /api/v1/llm/usage reports into the usage
// ledger with source=reported. The legacy report stays authoritative for that
// endpoint's response; mirroring failures are logged only.
type reportedUsageLedger struct {
	recorder *usageledger.Recorder
	tenant   TenantResolver
	prices   func(context.Context, string) ([]usageledger.Price, error)
}

// WithUsageLedger mirrors reported usage into the ledger. Configure before
// serving. prices may be nil, in which case reported cost is unknown.
func (h *LLMHandler) WithUsageLedger(recorder *usageledger.Recorder, tenant TenantResolver, prices func(context.Context, string) ([]usageledger.Price, error)) *LLMHandler {
	h.ledger = reportedUsageLedger{recorder: recorder, tenant: tenant, prices: prices}
	return h
}

// reportedRecord maps a legacy report. The report's float cost is not copied:
// it is not a fixed-point amount, so cost is estimated from a model-price
// resource or left unknown.
func reportedRecord(tenant, requestID string, report models.UsageRecord) usageledger.Record {
	finished := report.Timestamp.UTC()
	latency := int64(report.LatencyMs)
	if latency < 0 {
		latency = 0
	}
	started := finished.Add(-time.Duration(latency) * time.Millisecond)
	return usageledger.Record{
		ID: usageledger.NewID(), TenantID: tenant, StartedAt: started, FinishedAt: finished, LatencyMS: latency,
		Plane: usageledger.PlaneLLM, Provider: report.Provider, ModelID: report.ModelID,
		PrincipalID: report.UserID, ActorID: report.AgentID, RequestID: requestID,
		Status: usageledger.StatusOK, Source: usageledger.SourceReported, Requests: 1,
		Usage: &usageledger.Usage{
			InputTokens:  usageledger.Int64(int64(report.InputTokens)),
			OutputTokens: usageledger.Int64(int64(report.OutputTokens)),
			TotalTokens:  usageledger.Int64(int64(report.TotalTokens)),
		},
		Cost: usageledger.UnknownCost(),
	}
}

func (l reportedUsageLedger) record(r *http.Request, report models.UsageRecord) {
	if l.recorder == nil || l.tenant == nil {
		return
	}
	tenant, err := l.tenant(r)
	if err != nil || tenant == "" {
		log.Warn().Str("request_id", GetRequestID(r.Context())).Msg("reported usage not mirrored to ledger: no verified tenant")
		return
	}
	record := reportedRecord(tenant, GetRequestID(r.Context()), report)
	// A reported record is correlated with the request that reported it.
	record.TraceID, record.SpanID = telemetry.TraceID(r.Context()), telemetry.SpanID(r.Context())
	prices := l.prices
	l.recorder.Submit(r.Context(), func(ctx context.Context) (usageledger.Record, error) {
		if prices != nil {
			if list, err := prices(ctx, tenant); err == nil {
				record.Cost = usageledger.Estimate(list, record)
			}
		}
		return record, nil
	})
}
