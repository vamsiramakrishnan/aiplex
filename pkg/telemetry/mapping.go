package telemetry

import "strings"

// ModelProviderName maps an AIPlex model provider ID to gen_ai.provider.name.
// vertex is true for a Vertex AI surface (no provider adapter). IDs without a
// well-known value are returned unchanged (a custom value).
func ModelProviderName(providerID string, vertex bool) string {
	if vertex {
		return ProviderGCPVertexAI
	}
	switch providerID {
	case "openai":
		return ProviderOpenAI
	case "anthropic":
		return ProviderAnthropic
	case "bedrock":
		return ProviderAWSBedrock
	case "azure-openai":
		return ProviderAzureOpenAI
	case "gemini-developer":
		return ProviderGCPGemini
	}
	return providerID
}

// AgentProviderName maps a native agent provider to gen_ai.provider.name.
func AgentProviderName(provider string) string {
	switch {
	case strings.HasPrefix(provider, "google-"):
		return ProviderGCPVertexAI
	case strings.HasPrefix(provider, "aws-"):
		return ProviderAWSBedrock
	}
	return provider
}

// ModelOperationName maps a native model operation ID to
// gen_ai.operation.name. An operation with no well-known value keeps its
// native ID (a custom value).
func ModelOperationName(operationID string) string {
	id := strings.ToLower(operationID)
	last := id
	if i := strings.LastIndexAny(id, ".:/"); i >= 0 {
		last = id[i+1:]
	}
	switch {
	case strings.Contains(id, "realtime") || strings.HasPrefix(id, "live") || strings.HasSuffix(id, ".live"):
		return OpRealtime
	case strings.Contains(id, "responses-retrieve"):
		return OpFetchResponse
	case strings.Contains(id, "chat-completions"), strings.HasSuffix(id, "anthropic.messages"), strings.Contains(id, "converse"), strings.HasSuffix(id, ".responses"), strings.HasSuffix(id, ".responses-websocket"), last == "messages":
		return OpChat
	case last == "generatecontent" || last == "streamgeneratecontent":
		return OpGenerateContent
	case strings.Contains(id, "embed"):
		return OpEmbeddings
	case last == "completions":
		return OpTextCompletion
	}
	return operationID
}
