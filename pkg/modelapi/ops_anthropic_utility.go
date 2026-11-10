package modelapi

// Anthropic token counting: POST /v1/messages/count_tokens
// (https://platform.claude.com/docs/en/api/messages/count_tokens).
//
// The response is {"input_tokens": N}: an estimate, free, with no cache logic
// and no usage wrapper. It is request-count metered and never read for usage.
// The body is a Messages-shaped request whose model must be on the allowlist,
// so generic provider validation applies unchanged.
//
// Not added: the Models list/get endpoints (GET /v1/models[/{id}]). They would
// return the upstream account's whole model catalog, which is not the surface's
// allowlist; access points already serve their own /v1/models.
// Vertex (publishers/anthropic/models/count-tokens:rawPredict) and Bedrock
// (CountTokens) equivalents are not added: Vertex's endpoint takes a model-less
// rawPredict body that needs its own validation, and Bedrock's request wraps an
// InvokeModel/Converse body whose exact schema was not confirmed.

func anthropicUtilityOps() []utilityOp {
	return []utilityOp{{providerOp{Operation{ID: "anthropic.messages-count-tokens", Method: "POST", Path: "/v1/messages/count_tokens", Transport: HTTP, Action: Invoke, SourceURL: "https://platform.claude.com/docs/en/api/messages/count_tokens"}, FormatAnthropicCountTokens, ProtocolAnthropicMessages}, ProviderAnthropic, utilitySpec{kind: utilNoUsage, stateless: true}}}
}
