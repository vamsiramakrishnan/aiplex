package api

const (
	quotaProfileOpenAI      = "openai-text"
	quotaProfileAnthropic   = "anthropic-text"
	quotaProfileInteraction = "gemini-interaction-text"
	// quotaProfileBedrockConverse meters Bedrock Converse/ConverseStream,
	// whose TokenUsage inputTokens is exclusive of cache reads and writes.
	quotaProfileBedrockConverse = "bedrock-converse-text"
	// quotaProfileVertexOpenAI meters the Vertex OpenAI-compatible chat
	// endpoint, which reports no cached-input breakdown: total input and
	// billed output (reasoning folded in only when the native total proves
	// the composition).
	quotaProfileVertexOpenAI = "vertex-openai-text"
)

func quotaStrictProfile(profile string) bool {
	switch profile {
	case quotaProfileGemini, quotaProfileOpenAI, quotaProfileAnthropic, quotaProfileInteraction, quotaProfileBedrockConverse, quotaProfileVertexOpenAI:
		return true
	}
	return false
}

// quotaProfileCounters are the token meters every binding of the profile maps.
func quotaProfileCounters(profile string) []string {
	switch profile {
	case quotaProfileGemini, quotaProfileInteraction:
		return []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"}
	case quotaProfileOpenAI, quotaProfileAnthropic, quotaProfileBedrockConverse:
		return []string{"uncached_input_tokens", "cached_tokens", "output_tokens"}
	case quotaProfileVertexOpenAI:
		return []string{"input_tokens", "output_tokens"}
	}
	return nil
}

// quotaProfileCacheWriteOptions lists the alternative cache-write meter sets a
// binding of the profile may map: the scalar alone (one price for every TTL)
// or the provider's complete per-TTL breakdown (one price per TTL). Mapping
// both would double count. A binding that maps neither refuses any positive
// cache write (fail closed).
func quotaProfileCacheWriteOptions(profile string) [][]string {
	switch profile {
	case quotaProfileAnthropic, quotaProfileBedrockConverse:
		return [][]string{{"cache_write_tokens"}, {"cache_write_5m_tokens", "cache_write_1h_tokens"}}
	case quotaProfileOpenAI:
		return [][]string{{"cache_write_tokens"}, {"cache_write_30m_tokens", "cache_write_12h_tokens"}}
	}
	return nil
}

// quotaProfileTokenCounter reports whether a native counter may be mapped by
// a binding of the profile.
func quotaProfileTokenCounter(profile, native string) bool {
	for _, required := range quotaProfileCounters(profile) {
		if native == required {
			return true
		}
	}
	for _, option := range quotaProfileCacheWriteOptions(profile) {
		for _, counter := range option {
			if native == counter {
				return true
			}
		}
	}
	return false
}

// quotaBindingCacheWrites returns the cache-write counters a binding maps, or
// false when its mapping is not exactly one complete documented option.
func quotaBindingCacheWrites(binding QuotaBinding) ([]string, bool) {
	var mapped []string
	for _, option := range quotaProfileCacheWriteOptions(binding.Profile) {
		count := 0
		for _, counter := range option {
			if binding.Meters[counter] != "" {
				count++
			}
		}
		if count == 0 {
			continue
		}
		if count != len(option) || mapped != nil {
			return nil, false
		}
		mapped = option
	}
	return mapped, true
}

// quotaProfileInputCounters are the disjoint subsets of one input ceiling.
func quotaProfileInputCounters(binding QuotaBinding) []string {
	if binding.Profile == quotaProfileVertexOpenAI {
		return []string{"input_tokens"}
	}
	counters := []string{"uncached_input_tokens", "cached_tokens"}
	writes, _ := quotaBindingCacheWrites(binding)
	return append(counters, writes...)
}

func quotaProfileOperation(binding QuotaBinding) bool {
	switch binding.Profile {
	case quotaProfileGemini:
		return binding.Provider == "google" && (binding.OperationID == "models.generateContent" || binding.OperationID == "models.streamGenerateContent" || binding.OperationID == "endpoints.generateContent" || binding.OperationID == "endpoints.streamGenerateContent")
	case quotaProfileOpenAI:
		// Provider operations share the OpenAI wire format and usage composition.
		switch binding.OperationID {
		case "openai.chat-completions", "response.create", "openai.v1.chat-completions", "openai.v1.responses", "azure.v1.chat-completions", "azure.v1.responses", "azure.deployments.chat-completions":
			return true
		case "compat.chat-completions":
			// Generic compatible servers carry no provider usage contract;
			// the surface must declare OpenAI usage semantics (checked when
			// the invocation is prepared).
			return true
		}
		return false
	case quotaProfileAnthropic:
		switch binding.OperationID {
		case "anthropic.messages":
			return true
		case "models.rawPredict", "models.streamRawPredict":
			return binding.Provider == "anthropic"
		case "bedrock.invoke", "bedrock.invoke-stream":
			// Anthropic Messages bodies on Bedrock InvokeModel.
			return binding.Provider == "bedrock"
		}
		return false
	case quotaProfileBedrockConverse:
		return binding.Provider == "bedrock" && (binding.OperationID == "bedrock.converse" || binding.OperationID == "bedrock.converse-stream")
	case quotaProfileVertexOpenAI:
		return binding.Provider == "google" && binding.OperationID == "openai.chat-completions"
	case quotaProfileInteraction:
		return binding.Provider == "google" && binding.OperationID == "interaction.create"
	}
	return binding.Profile == quotaProfileRequests
}
