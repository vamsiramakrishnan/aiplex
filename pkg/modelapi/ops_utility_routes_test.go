package modelapi

// Conformance routes for the text and utility operations (ops_utility.go).
// utilityRoutes replaces, by provider and file name, the earlier
// unsupported / format-checked entries of usageRoutes.

func overrideRoutes(base, replacements []fixtureRoute) []fixtureRoute {
	replaced := map[string]fixtureRoute{}
	for _, r := range replacements {
		replaced[r.provider+"/"+r.file] = r
	}
	out := make([]fixtureRoute, 0, len(base))
	for _, r := range base {
		if next, ok := replaced[r.provider+"/"+r.file]; ok {
			out = append(out, next)
			delete(replaced, r.provider+"/"+r.file)
			continue
		}
		out = append(out, r)
	}
	for _, r := range replacements {
		if _, pending := replaced[r.provider+"/"+r.file]; pending {
			out = append(out, r)
		}
	}
	return out
}

func geminiDASurface(ids ...string) func(string) Surface {
	return providerSurfaceFor(Provider{ID: ProviderGeminiDeveloper}, ProtocolGoogleGenAI, []string{"model-a"}, ids...)
}

func utilityRoutes() []fixtureRoute {
	const countOnly = "operation coverage is request-count; usage is recorded as advisory ledger telemetry (no token profile is installed for this operation)"
	const estimate = "an input-size estimate, never consumption; only the request is counted and the response is never read for usage"
	advisory := func(provider, file, surface, transport string, call *fixtureCall, conv expectConvention, native nativeSemantics, final bool, reason string, allow map[string]string) fixtureRoute {
		return fixtureRoute{provider: provider, file: file, surface: surface, transport: transport, status: statusAdvisory, reason: reason, call: call, conv: conv, native: native, final: final, allowUnknown: allow}
	}
	da := func(path, body string, ids ...string) *fixtureCall {
		return &fixtureCall{surface: geminiDASurface(ids...), path: path, body: body}
	}
	const daModel = "/v1beta/models/model-a"
	daGenerate := da(daModel+":generateContent", controlledBody, "gemini.models.generateContent")
	daStream := da(daModel+":streamGenerateContent?alt=sse", controlledBody, "gemini.models.streamGenerateContent")
	daCount := da(daModel+":countTokens", controlledBody, "gemini.models.countTokens")
	daEmbed := da(daModel+":embedContent", `{"model":"models/model-a","content":{"parts":[{"text":"x"}]}}`, "gemini.models.embedContent")
	daInteraction := da("/v1beta/interactions", `{"model":"model-a","input":"hi","store":false}`, "gemini.interactions.create")
	daCache := da("/v1beta/cachedContents", `{"model":"models/model-a","contents":[{"role":"user","parts":[{"text":"x"}]}]}`, "gemini.cachedContents.create")
	openAISurface := func(protocol string, ids ...string) func(string) Surface {
		return providerSurfaceFor(Provider{ID: ProviderOpenAI}, protocol, []string{"fixture-model-a", DefaultModerationModel}, ids...)
	}
	respRetrieve := &fixtureCall{method: "GET", surface: openAISurface(ProtocolOpenAIResponses, "openai.v1.responses-retrieve"), path: "/v1/responses/resp_fix0006", bind: []Resource{{"response", "resp_fix0006"}}}
	respCancel := &fixtureCall{surface: openAISurface(ProtocolOpenAIResponses, "openai.v1.responses-cancel"), path: "/v1/responses/resp_fix0007/cancel", bind: []Resource{{"response", "resp_fix0007"}}}
	return []fixtureRoute{
		// ---------------- OpenAI ----------------
		advisory("openai", "embeddings.json", "embeddings", "json", &fixtureCall{surface: openAISurface(ProtocolOpenAIEmbeddings, "openai.v1.embeddings"), path: "/v1/embeddings", body: `{"model":"fixture-model-a","input":"hi"}`}, convOpenAI, natOpenAI, true, "input tokens only (prompt_tokens, total_tokens); "+countOnly+": embeddings report no output counter, which the token profiles require", nil),
		advisory("openai", "moderation.json", "moderations", "json", &fixtureCall{surface: openAISurface(ProtocolOpenAIModerations, "openai.v1.moderations"), path: "/v1/moderations", body: `{"input":"hi"}`}, convOpenAI, natOpenAI, false, "request-count; the response documents no usage", nil),
		advisory("openai", "responses.background.queued.json", "responses-retrieve", "json", respRetrieve, convOpenAI, natOpenAI, false, "GET /v1/responses/{id}: request-count, owner-only; never read for usage, so a retrieved background response is not settled a second time", nil),
		advisory("openai", "responses.background.cancelled.json", "responses-cancel", "json", respCancel, convOpenAI, natOpenAI, false, "POST /v1/responses/{id}/cancel: request-count, owner-only; never read for usage", nil),

		// ---------------- Anthropic ----------------
		advisory("anthropic", "count-tokens.json", "count-tokens", "json", &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01"}, ProtocolAnthropicMessages, []string{"claude-fixture"}, "anthropic.messages-count-tokens"), path: "/v1/messages/count_tokens", body: `{"model":"claude-fixture","messages":[]}`}, convNoConsumption, natAnthropic, false, "POST /v1/messages/count_tokens: "+estimate, nil),

		// ---------------- Gemini Developer API ----------------
		advisory("google", "gemini-generate.json", "gemini-generateContent", "json", daGenerate, convGemini, natGemini, true, countOnly, nil),
		advisory("google", "gemini-generate-tooluse.json", "gemini-generateContent", "json", daGenerate, convGemini, natGemini, true, countOnly, nil),
		advisory("google", "gemini-generate-error-429.json", "gemini-generateContent", "json", with(daGenerate, func(c *fixtureCall) { c.upstream = 429 }), convGemini, natGemini, false, countOnly, nil),
		advisory("google", "gemini-generate.sse", "gemini-streamGenerateContent", "sse", daStream, convGemini, natGemini, true, countOnly, nil),
		advisory("google", "gemini-generate-interrupted.sse", "gemini-streamGenerateContent", "sse", daStream, convGemini, natGemini, false, countOnly, nil),
		advisory("google", "gemini-generate.stream.json", "gemini-streamGenerateContent", "json-array", with(da(daModel+":streamGenerateContent", controlledBody, "gemini.models.streamGenerateContent"), func(c *fixtureCall) { c.media = "application/json" }), convGemini, natGemini, true, "JSON-array framing (no alt=sse) is observed exactly; "+countOnly, nil),
		advisory("google", "gemini-count-tokens.json", "gemini-countTokens", "json", daCount, convNoConsumption, natGemini, false, "countTokens: "+estimate, nil),
		advisory("google", "gemini-embed.json", "gemini-embedContent", "json", daEmbed, convGemini, natGemini, true, "input tokens only (usageMetadata.promptTokenCount); "+countOnly, nil),
		advisory("google", "gemini-interactions.json", "gemini-interactions", "json", daInteraction, convGemini, natGemini, true, countOnly, nil),
		advisory("google", "gemini-interactions.sse", "gemini-interactions", "sse", with(daInteraction, func(c *fixtureCall) { c.path += "?alt=sse"; c.body = `{"model":"model-a","input":"hi","stream":true}` }), convGemini, natGemini, true, countOnly, nil),
		advisory("google", "gemini-interactions-interrupted.sse", "gemini-interactions", "sse", with(daInteraction, func(c *fixtureCall) { c.path += "?alt=sse"; c.body = `{"model":"model-a","input":"hi","stream":true}` }), convGemini, natGemini, false, countOnly, nil),
		advisory("google", "gemini-cachedcontent-create.json", "gemini-cachedContents", "json", daCache, convGemini, natGemini, false, "cachedContents create/get/list/update/delete are request-count and never read for usage: usageMetadata.totalTokenCount is the cache size, repeated on every get/list, and how creation is billed is UNCONFIRMED", map[string]string{"cache_write_tokens": "cache billing UNCONFIRMED; not recorded", "total_tokens": "cache size, not an exchange total; not recorded"}),
	}
}
