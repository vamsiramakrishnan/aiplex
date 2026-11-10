package telemetry

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Exchange is what telemetry records about one managed exchange: the same
// facts as its usage ledger record, never payloads or credentials. Empty
// strings and nil counters are unknown and produce no attribute.
type Exchange struct {
	Plane string
	// Operation is the native operation ID (for example
	// "openai.v1.chat-completions"); OperationName is gen_ai.operation.name.
	Operation, OperationName string
	// ProviderName is gen_ai.provider.name; Provider is AIPlex's own label.
	ProviderName, Provider        string
	RequestModel, ResponseModel   string
	MCPMethod, ToolName           string
	ResourceID, AccessPointID     string
	CallerKind                    string
	PrincipalID, ActorID          string
	ClientID, RequestID, RecordID string
	// Status is the ledger status; ErrorType is set only when it is not ok.
	Status, ErrorType  string
	HTTPStatus         int
	Streaming, Session bool
	Duration           time.Duration
	// TimeToFirstChunk is zero when unknown (not streamed, or nothing sent).
	TimeToFirstChunk time.Duration
	Usage            *usageledger.Usage
	UsageBound       string
	Cost             usageledger.Cost
	QuotaAttemptID   string
	QuotaDenied      bool
}

// SpanName follows the conventions: "{gen_ai.operation.name}
// {gen_ai.request.model}" for model calls, "{mcp.method.name} {tool}" for
// MCP and "invoke_agent" for agents.
func (e Exchange) SpanName() string {
	switch e.Plane {
	case usageledger.PlaneMCP:
		method := e.MCPMethod
		if method == "" {
			method = "mcp"
		}
		if e.ToolName != "" {
			return method + " " + e.ToolName
		}
		return method
	case usageledger.PlaneA2A:
		return OpInvokeAgent
	}
	name := e.OperationName
	if name == "" {
		name = "model"
	}
	if e.RequestModel != "" {
		return name + " " + e.RequestModel
	}
	return name
}

// Attributes are the client span attributes.
func (e Exchange) Attributes() []attribute.KeyValue {
	var out []attribute.KeyValue
	str := func(key, value string) {
		if value != "" {
			out = append(out, attribute.String(key, value))
		}
	}
	count := func(key string, value *int64) {
		if value != nil {
			out = append(out, attribute.Int64(key, *value))
		}
	}
	str(AttrPlane, e.Plane)
	str(AttrOperation, e.Operation)
	str(AttrGenAIOperationName, e.OperationName)
	str(AttrGenAIProviderName, e.ProviderName)
	str(AttrProviderNative, e.Provider)
	str(AttrGenAIRequestModel, e.RequestModel)
	str(AttrGenAIResponseModel, e.ResponseModel)
	str(AttrMCPMethodName, e.MCPMethod)
	str(AttrGenAIToolName, e.ToolName)
	str(AttrResourceID, e.ResourceID)
	if e.Plane == usageledger.PlaneA2A {
		str(AttrAgentID, e.ResourceID)
	}
	str(AttrAccessPointID, e.AccessPointID)
	str(AttrCallerKind, e.CallerKind)
	str(AttrPrincipalID, e.PrincipalID)
	str(AttrActorID, e.ActorID)
	str(AttrClientID, e.ClientID)
	str(AttrRequestID, e.RequestID)
	str(AttrUsageRecordID, e.RecordID)
	str(AttrStatus, e.Status)
	str(AttrErrorType, e.ErrorType)
	if e.HTTPStatus != 0 {
		out = append(out, attribute.Int(AttrResponseStatus, e.HTTPStatus))
	}
	if e.Plane == usageledger.PlaneLLM {
		out = append(out, attribute.Bool(AttrGenAIRequestStream, e.Streaming))
		if e.Streaming && e.TimeToFirstChunk > 0 {
			out = append(out, attribute.Float64(AttrGenAIResponseTTFC, e.TimeToFirstChunk.Seconds()))
		}
	} else {
		out = append(out, attribute.Bool(AttrStreaming, e.Streaming))
	}
	if e.Session {
		out = append(out, attribute.Bool(AttrSession, true))
	}
	if u := e.Usage; u != nil {
		count(AttrGenAIUsageInput, u.InputTokens)
		count(AttrGenAIUsageOutput, u.OutputTokens)
		count(AttrGenAIUsageCacheRead, u.CacheReadTokens)
		count(AttrGenAIUsageCacheWrite, u.CacheWriteTokens)
		count(AttrGenAIUsageReasoning, u.ReasoningTokens)
		count(AttrUsageTotal, u.TotalTokens)
	}
	str(AttrUsageBound, e.UsageBound)
	if e.Cost.Basis == "" {
		str(AttrCostBasis, usageledger.BasisUnknown)
	} else {
		str(AttrCostBasis, e.Cost.Basis)
	}
	if amount, ok := costDecimal(e.Cost); ok {
		str(AttrCostAmount, amount)
		str(AttrCostCurrency, e.Cost.Currency)
	}
	str(AttrQuotaAttemptID, e.QuotaAttemptID)
	switch {
	case e.QuotaDenied:
		str(AttrQuotaDecision, QuotaDecisionDenied)
	case e.QuotaAttemptID != "":
		str(AttrQuotaDecision, QuotaDecisionAdmit)
	}
	return out
}

// costDecimal is the known cost as a decimal string.
func costDecimal(c usageledger.Cost) (string, bool) {
	if c.Basis == "" || c.Basis == usageledger.BasisUnknown || c.Currency == "" {
		return "", false
	}
	if _, ok := c.AmountInt(); !ok {
		return "", false
	}
	return usageledger.DecimalString(c.Amount, c.Scale)
}

// metricAttributes are bounded: configured identifiers and enumerations only.
// No principal, request, record or trace identifiers.
func (e Exchange) metricAttributes(extra ...attribute.KeyValue) metric.MeasurementOption {
	out := make([]attribute.KeyValue, 0, 8+len(extra))
	str := func(key, value string) {
		if value != "" {
			out = append(out, attribute.String(key, value))
		}
	}
	str(AttrPlane, e.Plane)
	str(AttrGenAIProviderName, e.ProviderName)
	str(AttrGenAIOperationName, e.OperationName)
	str(AttrGenAIRequestModel, e.RequestModel)
	str(AttrMCPMethodName, e.MCPMethod)
	str(AttrAccessPointID, e.AccessPointID)
	str(AttrCallerKind, e.CallerKind)
	str(AttrStatus, e.Status)
	out = append(out, extra...)
	return metric.WithAttributes(out...)
}

// RecordExchange adds the exchange to the metrics. Unknown counters and
// unknown cost are not added as zero; they are counted separately.
func (in *Instruments) RecordExchange(ctx context.Context, e Exchange) {
	if in == nil {
		return
	}
	var errorType []attribute.KeyValue
	if e.ErrorType != "" {
		errorType = []attribute.KeyValue{attribute.String(AttrErrorType, e.ErrorType)}
	}
	seconds := e.Duration.Seconds()
	switch e.Plane {
	case usageledger.PlaneLLM:
		in.inferenceDuration.Record(ctx, seconds, e.metricAttributes(errorType...))
		if e.Streaming && e.TimeToFirstChunk > 0 {
			in.timeToFirstChunk.Record(ctx, e.TimeToFirstChunk.Seconds(), e.metricAttributes())
		}
	case usageledger.PlaneMCP:
		in.mcpDuration.Record(ctx, seconds, e.metricAttributes(errorType...))
	case usageledger.PlaneA2A:
		in.invokeAgentDuration.Record(ctx, seconds, e.metricAttributes(errorType...))
	}
	attrs := e.metricAttributes()
	finalUsage := e.Usage != nil && (e.UsageBound == "" || e.UsageBound == usageledger.UsagePartial)
	// The conventions' token counters apply to any GenAI operation, told apart
	// by gen_ai.operation.name (chat, invoke_agent, ...); MCP has no tokens.
	if (e.Plane == usageledger.PlaneLLM || e.Plane == usageledger.PlaneA2A) && finalUsage {
		for _, c := range []struct {
			counter metric.Int64Counter
			value   *int64
		}{{in.inputTokens, e.Usage.InputTokens}, {in.outputTokens, e.Usage.OutputTokens}, {in.cacheReadTokens, e.Usage.CacheReadTokens}, {in.cacheWriteTokens, e.Usage.CacheWriteTokens}, {in.reasoningTokens, e.Usage.ReasoningTokens}} {
			if c.value != nil && *c.value >= 0 {
				c.counter.Add(ctx, *c.value, attrs)
			}
		}
	}
	if !finalUsage {
		in.unknownUsage.Add(ctx, 1, e.metricAttributes(attribute.String(AttrUsageBound, boundLabel(e))))
	}
	if amount, ok := costFloat(e.Cost); ok {
		in.cost.Add(ctx, amount, e.metricAttributes(attribute.String(AttrCostCurrency, e.Cost.Currency), attribute.String(AttrCostBasis, e.Cost.Basis)))
	} else if e.Cost.Basis != usageledger.BasisCached {
		// A cache hit cost nothing at the provider: neither a cost nor unknown.
		in.unknownCost.Add(ctx, 1, attrs)
	}
	if e.QuotaDenied {
		in.quotaDenials.Add(ctx, 1, e.metricAttributes(errorType...))
	} else if e.Status == usageledger.StatusError || e.Status == usageledger.StatusUnknown {
		in.upstreamErrors.Add(ctx, 1, e.metricAttributes(errorType...))
	}
}

func boundLabel(e Exchange) string {
	if e.Usage == nil {
		return "unknown"
	}
	return e.UsageBound
}

// IsQuotaDenial reports whether a ledger outcome is a QuotaPlex refusal
// (its error class is a lower-cased QUOTA_* code). A provider's own 429 is
// not a quota denial.
func IsQuotaDenial(status, errorClass string) bool {
	return status == usageledger.StatusDenied && strings.HasPrefix(errorClass, "quota_")
}
