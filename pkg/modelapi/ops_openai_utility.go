package modelapi

// OpenAI (and Azure OpenAI v1) utility operations: embeddings, moderations and
// the Responses lifecycle (retrieve, cancel, delete).
//
// Reference: https://developers.openai.com/api/reference/resources/embeddings/methods/create,
// .../moderations/methods/create, .../responses/methods/retrieve,
// .../responses/methods/cancel, .../responses/methods/delete. Azure OpenAI v1
// serves the same operations under /openai/v1 (and embeddings per deployment
// under /openai/deployments/{deployment}/embeddings with a pinned api-version);
// Azure has no Moderations operation (content filtering is built in).
//
// Embeddings report usage.prompt_tokens / usage.total_tokens only; the exact
// values are recorded as input_tokens / total_tokens ledger telemetry on a
// request-count lease. Moderations carry no usage object (fixture
// moderation.json). Retrieve/cancel/delete address a response by ID and are
// served only to the principal that created it on this surface (the same
// ownership binding previous_response_id uses); they never read usage, so a
// completed background response cannot be settled a second time.

func openAIUtilityOps() []utilityOp {
	openaiRef := "https://developers.openai.com/api/reference/resources/"
	azureRef := "https://learn.microsoft.com/en-us/azure/ai-foundry/openai/reference"
	retrieveQuery := map[string]func(string) bool{
		"stream":              queryMatches(boolQuery),
		"starting_after":      queryMatches(digitsQuery),
		"include_obfuscation": queryMatches(boolQuery),
		"include[]":           queryMatches(enumQuery),
	}
	resource := func(provider, id, method, path string, action Action, ref string, query map[string]func(string) bool) utilityOp {
		return utilityOp{providerOp{Operation{ID: id, Method: method, Path: path, Transport: HTTP, Action: action, ResourceKind: "response", Stateful: true, SourceURL: ref}, FormatOpenAIResponses, ProtocolOpenAIResponses}, provider, utilitySpec{kind: utilNoUsage, noBody: true, asynchronous: true, query: query}}
	}
	simple := func(provider, id, path, ref, format, protocol string, spec utilitySpec) utilityOp {
		return utilityOp{providerOp{Operation{ID: id, Method: "POST", Path: path, Transport: HTTP, Action: Invoke, SourceURL: ref}, format, protocol}, provider, spec}
	}
	embedding := utilitySpec{kind: utilEmbedding, stateless: true}
	return []utilityOp{
		simple(ProviderOpenAI, "openai.v1.embeddings", "/v1/embeddings", openaiRef+"embeddings/methods/create", FormatOpenAIEmbeddings, ProtocolOpenAIEmbeddings, embedding),
		simple(ProviderOpenAI, "openai.v1.moderations", "/v1/moderations", openaiRef+"moderations/methods/create", FormatOpenAIModerations, ProtocolOpenAIModerations, utilitySpec{kind: utilNoUsage, stateless: true}),
		resource(ProviderOpenAI, "openai.v1.responses-retrieve", "GET", "/v1/responses/{id}", Read, openaiRef+"responses/methods/retrieve", retrieveQuery),
		resource(ProviderOpenAI, "openai.v1.responses-cancel", "POST", "/v1/responses/{id}/cancel", Cancel, openaiRef+"responses/methods/cancel", nil),
		resource(ProviderOpenAI, "openai.v1.responses-delete", "DELETE", "/v1/responses/{id}", Delete, openaiRef+"responses/methods/delete", nil),

		simple(ProviderAzureOpenAI, "azure.v1.embeddings", "/openai/v1/embeddings", azureRef, FormatOpenAIEmbeddings, ProtocolOpenAIEmbeddings, embedding),
		simple(ProviderAzureOpenAI, "azure.deployments.embeddings", "/openai/deployments/{model}/embeddings", azureRef, FormatOpenAIEmbeddings, ProtocolOpenAIEmbeddings, embedding),
		resource(ProviderAzureOpenAI, "azure.v1.responses-retrieve", "GET", "/openai/v1/responses/{id}", Read, azureRef, retrieveQuery),
		resource(ProviderAzureOpenAI, "azure.v1.responses-cancel", "POST", "/openai/v1/responses/{id}/cancel", Cancel, azureRef, nil),
		resource(ProviderAzureOpenAI, "azure.v1.responses-delete", "DELETE", "/openai/v1/responses/{id}", Delete, azureRef, nil),
	}
}
