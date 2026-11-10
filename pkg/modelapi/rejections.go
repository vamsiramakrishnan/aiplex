package modelapi

// rejectedBeforeProcessing reports an HTTP status that the provider documents
// as a refusal before any generation: rate limiting, overload, authentication
// or permission failure, and request validation. Such a response carries no
// usage, is not billed, and is terminal evidence that the request was not
// processed. Everything else (500, 502, 503, 504, 408, 424, a cut connection,
// or an error event after a 2xx) stays ambiguous: the request body was
// accepted and work may have run, so exposure is retained.
//
// Per provider (the status line is the evidence; the body is relayed as sent):
//
//	OpenAI, Azure OpenAI, OpenAI-compatible: 400, 401, 403, 404, 413, 422, 429.
//	  429 covers rate limits and quota exhaustion; 503 ("overloaded"/
//	  "Slow Down") is not documented as unbilled and is retained.
//	Anthropic Messages: 400 invalid_request_error, 401 authentication_error,
//	  402 billing_error, 403 permission_error, 404 not_found_error,
//	  413 request_too_large, 429 rate_limit_error, 529 overloaded_error.
//	  500 api_error and 504 timeout_error are retained.
//	Google (Vertex, incl. partner rawPredict, and Gemini): 400
//	  INVALID_ARGUMENT/FAILED_PRECONDITION, 401 UNAUTHENTICATED, 403
//	  PERMISSION_DENIED, 404 NOT_FOUND, 429 RESOURCE_EXHAUSTED. 500/503/504
//	  are retained.
//	Bedrock Runtime: 400 ValidationException/ServiceQuotaExceededException,
//	  403 AccessDeniedException, 404 ResourceNotFoundException, 429
//	  ThrottlingException/ModelNotReadyException. 408 ModelTimeoutException,
//	  424 ModelErrorException, 500 and 503 are retained.
func (g *Gateway) rejectedBeforeProcessing(status int) bool {
	switch status {
	case 400, 401, 403, 404, 429:
		return true
	}
	provider := ""
	if g.surface.Provider != nil {
		provider = g.surface.Provider.ID
	}
	switch provider {
	case ProviderOpenAI, ProviderAzureOpenAI, ProviderOpenAICompatible:
		return status == 413 || status == 422
	case ProviderAnthropic:
		return status == 402 || status == 413 || status == 529
	}
	return false
}
