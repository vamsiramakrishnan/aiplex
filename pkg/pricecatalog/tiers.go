package pricecatalog

// ConfirmedTiers lists, per catalogue provider, the long-context thresholds
// (input tokens) whose billing rule was confirmed from the provider's own
// documentation: the higher rates apply to the WHOLE request once the total
// input exceeds the threshold, and cached tokens count toward it. A usageledger
// price tier has exactly that meaning, so only these thresholds are carried
// into snapshots; any other tier field stays "blocking" and the entry needs
// --accept-partial.
//
//   - anthropic: "a request whose prompt is over 100,000 tokens pays higher
//     prices. A request's prompt length counts all of its input tokens,
//     including cache reads and cache writes. Each request is priced on its
//     own" (https://platform.claude.com/docs/en/about-claude/pricing#long-context-pricing,
//     Claude Haiku 5.5).
//   - openai: "prompts with >272K input tokens are priced at 2x input and 1.5x
//     output for the full session" (https://developers.openai.com/api/docs/models/gpt-5.5).
//     272K is read as 272,000 (the 400K window splits as 272K input + 128K
//     output).
//
// Not confirmed, so their tier fields stay unimported: Gemini (the pricing
// page labels "prompts <= 200k tokens" and "> 200k" but does not say whether
// the higher rate covers the whole prompt or whether cached tokens count),
// xAI ("requests whose prompt reaches the listed token threshold are billed at
// the higher rate for all tokens in the request": whole request is stated, but
// "reaches" is not the catalogue's "above"), Azure OpenAI, Bedrock and
// Vertex-hosted Claude (their own pricing pages were not confirmed).
var ConfirmedTiers = map[string][]int64{
	"anthropic": {100_000},
	"openai":    {272_000},
}

// TierConfirmed reports whether a long-context threshold is confirmed for the
// catalogue provider.
func TierConfirmed(provider string, above int64) bool {
	for _, t := range ConfirmedTiers[provider] {
		if t == above {
			return true
		}
	}
	return false
}

// ModalityProviders report per-modality token usage that the ledger holds
// (OpenAI and Azure *_tokens_details, Gemini promptTokensDetails), so modality
// token rates are imported only for them.
var ModalityProviders = map[string]bool{"openai": true, "azure-openai": true, "gemini-developer": true, "google": true}

// ServiceTierProviders report the service tier a request was served on, so a
// priority or flex price can be selected by what actually happened (OpenAI
// service_tier; Gemini usageMetadata.serviceTier).
var ServiceTierProviders = map[string]bool{"openai": true, "gemini-developer": true, "google": true}

// WebSearchPerCallProviders report the web search call count in usage and
// charge the same price per call whatever the context size: Anthropic
// (usage.server_tool_use.web_search_requests; "$10 USD per 1,000 searches").
// OpenAI chat search models report no call count, and Gemini grounding has a
// monthly free allowance and is billed per prompt on older models.
var WebSearchPerCallProviders = map[string]bool{"anthropic": true}
