package telemetry

// Every span name, attribute key and metric name AIPlex emits is defined in
// this file, so a rename in the conventions is a one-file change.
//
// Sources, read on 2026-11-10:
//
//   - GenAI, MCP and agent conventions: github.com/open-telemetry/semantic-conventions-genai
//     at commit e6658f14c9d9ea0760825f029cb82e7673f48fab (2026-11-06), files
//     docs/gen-ai/client-inference.md, gen-ai-token-metrics.md, mcp.md,
//     client-invoke-agent.md and docs/registry/attributes/gen-ai.md. Status:
//     Development. Names have changed between releases (for example
//     gen_ai.client.token.usage was split into per-direction counters and
//     gen_ai.usage.cache_creation.input_tokens became
//     gen_ai.usage.cache_write.input_tokens) and may change again.
//   - HTTP, error and server attributes: OpenTelemetry semantic conventions
//     v1.45.0 (the version the GenAI documents link to). Status: Stable.
//
// Names under "aiplex." are AIPlex's own and are not part of any convention.
const (
	// SemconvGenAICommit identifies the GenAI conventions revision above.
	SemconvGenAICommit = "e6658f14c9d9ea0760825f029cb82e7673f48fab"
	// SemconvVersion is the core conventions version for HTTP and error names.
	SemconvVersion = "1.45.0"
	// InstrumentationName is the tracer and meter scope name.
	InstrumentationName = "github.com/vamsiramakrishnan/aiplex"
	// DefaultServiceName is service.name unless OTEL_SERVICE_NAME is set.
	DefaultServiceName = "aiplex-api"
)

// HTTP server (stable).
const (
	AttrHTTPRequestMethod      = "http.request.method"
	AttrHTTPRoute              = "http.route"
	AttrHTTPResponseStatusCode = "http.response.status_code"
	AttrErrorType              = "error.type"
	AttrServerAddress          = "server.address"
	AttrServerPort             = "server.port"

	MetricHTTPServerDuration = "http.server.request.duration"
)

// GenAI (Development).
const (
	AttrGenAIOperationName   = "gen_ai.operation.name"
	AttrGenAIProviderName    = "gen_ai.provider.name"
	AttrGenAIRequestModel    = "gen_ai.request.model"
	AttrGenAIRequestStream   = "gen_ai.request.stream"
	AttrGenAIResponseModel   = "gen_ai.response.model"
	AttrGenAIResponseTTFC    = "gen_ai.response.time_to_first_chunk"
	AttrGenAIToolName        = "gen_ai.tool.name"
	AttrGenAIUsageInput      = "gen_ai.usage.input_tokens"
	AttrGenAIUsageOutput     = "gen_ai.usage.output_tokens"
	AttrGenAIUsageCacheRead  = "gen_ai.usage.cache_read.input_tokens"
	AttrGenAIUsageCacheWrite = "gen_ai.usage.cache_write.input_tokens"
	AttrGenAIUsageReasoning  = "gen_ai.usage.reasoning.output_tokens"

	// Well-known gen_ai.operation.name values.
	OpChat            = "chat"
	OpGenerateContent = "generate_content"
	OpEmbeddings      = "embeddings"
	OpTextCompletion  = "text_completion"
	OpRealtime        = "realtime_inference"
	OpFetchResponse   = "fetch_response"
	OpExecuteTool     = "execute_tool"
	OpInvokeAgent     = "invoke_agent"

	// Well-known gen_ai.provider.name values.
	ProviderOpenAI      = "openai"
	ProviderAnthropic   = "anthropic"
	ProviderAWSBedrock  = "aws.bedrock"
	ProviderAzureOpenAI = "azure.ai.openai"
	ProviderGCPGemini   = "gcp.gemini"
	ProviderGCPVertexAI = "gcp.vertex_ai"

	MetricGenAIInferenceDuration = "gen_ai.client.inference.duration"
	MetricGenAITimeToFirstChunk  = "gen_ai.client.inference.time_to_first_chunk"
	MetricGenAIInvokeAgentDur    = "gen_ai.client.invoke_agent.duration"
	MetricGenAIUsageInput        = "gen_ai.client.inference.usage.input_tokens"
	MetricGenAIUsageOutput       = "gen_ai.client.inference.usage.output_tokens"
	MetricGenAIUsageCacheRead    = "gen_ai.client.inference.usage.cache_read.input_tokens"
	MetricGenAIUsageCacheWrite   = "gen_ai.client.inference.usage.cache_write.input_tokens"
	MetricGenAIUsageReasoning    = "gen_ai.client.inference.usage.reasoning.output_tokens"
)

// MCP (Development). The client span name is "{mcp.method.name} {target}"
// where the target is gen_ai.tool.name when there is one.
const (
	AttrMCPMethodName = "mcp.method.name"
	AttrMCPSessionID  = "mcp.session.id"

	MetricMCPClientDuration = "mcp.client.operation.duration"
)

// AIPlex attributes.
const (
	AttrAccessPointID   = "aiplex.access_point.id"
	AttrCallerKind      = "aiplex.caller.kind"
	AttrPrincipalID     = "aiplex.principal.id"
	AttrActorID         = "aiplex.actor.id"
	AttrClientID        = "aiplex.client.id"
	AttrPlane           = "aiplex.plane"
	AttrResourceID      = "aiplex.resource.id"
	AttrOperation       = "aiplex.operation"
	AttrRequestID       = "aiplex.request.id"
	AttrStatus          = "aiplex.status"
	AttrQuotaAttemptID  = "aiplex.quota.attempt_id"
	AttrQuotaDecision   = "aiplex.quota.decision"
	AttrCostAmount      = "aiplex.cost.amount"
	AttrCostCurrency    = "aiplex.cost.currency"
	AttrCostBasis       = "aiplex.cost.basis"
	AttrUsageRecordID   = "aiplex.usage.record_id"
	AttrUsageBound      = "aiplex.usage.bound"
	AttrUsageTotal      = "aiplex.usage.total_tokens"
	AttrProviderNative  = "aiplex.provider"
	AttrStreaming       = "aiplex.streaming"
	AttrSession         = "aiplex.session"
	QuotaDecisionAdmit  = "admitted"
	QuotaDecisionDenied = "denied"

	MetricCost               = "aiplex.usage.cost"
	MetricUnknownCostRecords = "aiplex.usage.unknown_cost"
	MetricUnknownUsage       = "aiplex.usage.unknown_usage"
	MetricQuotaDenials       = "aiplex.quota.denials"
	MetricUpstreamErrors     = "aiplex.upstream.errors"
)

// Configuration environment variables AIPlex reads in addition to the
// standard OTEL_* variables the SDK and exporters read themselves.
const (
	// EnvPropagateUpstream turns W3C trace context on upstream model, tool
	// and agent requests on (default) or off.
	EnvPropagateUpstream = "AIPLEX_TRACE_PROPAGATE_UPSTREAM"
	// EnvLegacyEndpoint is the deprecated spelling of
	// OTEL_EXPORTER_OTLP_ENDPOINT, used only when the standard variable is
	// unset.
	EnvLegacyEndpoint = "OTEL_ENDPOINT"
)

// Explicit bucket boundaries the conventions advise for duration histograms.
var (
	genAIDurationBuckets = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}
	mcpDurationBuckets   = []float64{0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 60, 120, 300}
	httpDurationBuckets  = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
)
