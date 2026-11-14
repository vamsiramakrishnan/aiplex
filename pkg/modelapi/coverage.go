package modelapi

// ExecutionCoverage describes the metering boundary installed by this package.
// A request count measures a managed API exchange, never the work of a returned
// background job, retained cache, tool or agent. Providers and models remain
// explicit operator configuration rather than inferred from model names.
type ExecutionCoverage struct {
	RequestCount bool   `json:"request_count"`
	TokenProfile string `json:"token_profile,omitempty"`
	Asynchronous bool   `json:"asynchronous,omitempty"`
}

func Coverage(op Operation) ExecutionCoverage {
	switch op.ID {
	case "models.generateContent", "models.streamGenerateContent", "endpoints.generateContent", "endpoints.streamGenerateContent":
		return ExecutionCoverage{RequestCount: true, TokenProfile: "gemini-text"}
	case "openai.chat-completions", "response.create":
		return ExecutionCoverage{RequestCount: true, TokenProfile: "openai-text"}
	// Provider surfaces: a token profile only where exact usage observation is
	// implemented and tested for the operation's native format.
	case "openai.v1.chat-completions", "openai.v1.responses", "azure.v1.chat-completions", "azure.v1.responses", "azure.deployments.chat-completions":
		return ExecutionCoverage{RequestCount: true, TokenProfile: "openai-text"}
	case "bedrock.converse", "bedrock.converse-stream":
		return ExecutionCoverage{RequestCount: true, TokenProfile: "bedrock-converse-text"}
	case "bedrock.invoke", "bedrock.invoke-stream":
		// Anthropic Messages bodies only; other model-native bodies report no
		// usage the gateway understands and stay request-count.
		return ExecutionCoverage{RequestCount: true, TokenProfile: "anthropic-text"}
	case "compat.chat-completions":
		// No provider usage contract: request-count unless the surface
		// declares OpenAI or DeepSeek usage semantics
		// (Provider.UsageSemantics), which the quota host checks per
		// invocation.
		return ExecutionCoverage{RequestCount: true}
	case "anthropic.messages", BedrockMantleMessagesOperation, "models.rawPredict", "models.streamRawPredict":
		return ExecutionCoverage{RequestCount: true, TokenProfile: "anthropic-text"}
	case "interaction.create":
		return ExecutionCoverage{RequestCount: true, TokenProfile: "gemini-interaction-text"}
	// WebSocket sessions: by default one admitted request per session with
	// advisory usage (websocket.go); a surface with websocket.metering
	// "token" admits each response (Realtime, Responses) or allowance window
	// (Live) under these profiles instead (session_metering.go).
	case "live.connect":
		return ExecutionCoverage{RequestCount: true, TokenProfile: "gemini-live-allowance"}
	case geminiLiveOperation:
		// Developer API Live: the same observer, but whether its snapshots
		// are session-cumulative is undocumented, so usage is advisory and
		// no token profile (allowance windows) is offered.
		return ExecutionCoverage{RequestCount: true}
	case "openai.v1.realtime":
		return ExecutionCoverage{RequestCount: true, TokenProfile: "openai-realtime"}
	case "openai.v1.responses-websocket":
		return ExecutionCoverage{RequestCount: true, TokenProfile: "openai-text"}
	case "models.embedContent", "models.countTokens", "models.computeTokens", "models.predict", "endpoints.countTokens", "endpoints.computeTokens", "endpoints.predict", "endpoints.rawPredict", "endpoints.streamRawPredict":
		return ExecutionCoverage{RequestCount: true}
	case "models.predictLongRunning", "batch.create", "cache.create", "tuning.create", "operation.get", "models.fetchPredictOperation", "response.get", "response.delete", "interaction.get", "interaction.cancel", "interaction.delete", "cache.get", "cache.list", "cache.update", "cache.delete", "batch.get", "batch.list", "batch.cancel", "batch.delete", "tuning.get", "tuning.list", "tuning.cancel", "operation.cancel", "operation.delete":
		return ExecutionCoverage{RequestCount: true, Asynchronous: true}
	default:
		if coverage := utilityCoverage(op); coverage.RequestCount {
			return coverage
		}
		return extendedCoverage(op)
	}
}

// AnthropicOperations is a native Messages contract. A Vertex Anthropic surface
// instead selects models.rawPredict/streamRawPredict and keeps Google OAuth.
func AnthropicOperations() []Operation {
	return []Operation{{ID: "anthropic.messages", Method: "POST", Path: "/v1/messages", Transport: SSE, Action: Invoke, SourceURL: "https://platform.claude.com/docs/en/api/messages"}}
}

// Operations returns the combined native operation catalog. Each call owns its slice.
func Operations() []Operation { return append(GoogleOperations(), AnthropicOperations()...) }
