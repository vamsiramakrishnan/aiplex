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
	// quotaProfileRealtime meters one admitted OpenAI Realtime response on
	// disjoint modality meters (Realtime prices text, audio and image input
	// and text and audio output separately).
	quotaProfileRealtime = "openai-realtime"
	// quotaProfileLiveAllowance meters a Gemini Live session in renewable
	// allowance windows held from the saved surface configuration.
	quotaProfileLiveAllowance = "gemini-live-allowance"
	// quotaProfileEmbedding meters input-only embedding exchanges.
	quotaProfileEmbedding = "embedding-input"
)

var (
	quotaRealtimeInputCounters  = []string{"uncached_text_input_tokens", "uncached_audio_input_tokens", "uncached_image_input_tokens", "cached_text_input_tokens", "cached_audio_input_tokens", "cached_image_input_tokens"}
	quotaRealtimeOutputCounters = []string{"text_output_tokens", "audio_output_tokens"}
	quotaLiveCounters           = []string{"input_tokens", "cached_tokens", "output_tokens", "thought_tokens", "tool_use_tokens", "total_tokens"}
)

func quotaStrictProfile(profile string) bool {
	switch profile {
	case quotaProfileGemini, quotaProfileOpenAI, quotaProfileAnthropic, quotaProfileInteraction, quotaProfileBedrockConverse, quotaProfileVertexOpenAI, quotaProfileRealtime, quotaProfileLiveAllowance, quotaProfileEmbedding:
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
	case quotaProfileEmbedding:
		return []string{"input_tokens"}
	case quotaProfileRealtime:
		return append(append([]string(nil), quotaRealtimeInputCounters...), quotaRealtimeOutputCounters...)
	}
	return nil
}

// quotaProfileOutputCounters are the subsets of one output cap.
func quotaProfileOutputCounters(profile string) []string {
	switch profile {
	case quotaProfileGemini, quotaProfileInteraction:
		return []string{"output_tokens", "thought_tokens"}
	case quotaProfileRealtime:
		return quotaRealtimeOutputCounters
	case quotaProfileEmbedding:
		return nil
	}
	return []string{"output_tokens"}
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
	if profile == quotaProfileLiveAllowance {
		for _, counter := range quotaLiveCounters {
			if native == counter {
				return true
			}
		}
		return false
	}
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
	if binding.Profile == quotaProfileVertexOpenAI || binding.Profile == quotaProfileEmbedding {
		return []string{"input_tokens"}
	}
	if binding.Profile == quotaProfileRealtime {
		return quotaRealtimeInputCounters
	}
	counters := []string{"uncached_input_tokens", "cached_tokens"}
	writes, _ := quotaBindingCacheWrites(binding)
	return append(counters, writes...)
}

func quotaProfileOperation(binding QuotaBinding) bool {
	switch binding.Profile {
	case quotaProfileGemini:
		if binding.Provider == "gemini-developer" {
			// Gemini Developer API: same GenerateContentResponse usage.
			return binding.OperationID == "gemini.models.generateContent" || binding.OperationID == "gemini.models.streamGenerateContent"
		}
		return binding.Provider == "google" && (binding.OperationID == "models.generateContent" || binding.OperationID == "models.streamGenerateContent" || binding.OperationID == "endpoints.generateContent" || binding.OperationID == "endpoints.streamGenerateContent")
	case quotaProfileOpenAI:
		// Provider operations share the OpenAI wire format and usage composition.
		switch binding.OperationID {
		case "openai.chat-completions", "response.create", "openai.v1.chat-completions", "openai.v1.responses", "azure.v1.chat-completions", "azure.v1.responses", "azure.deployments.chat-completions":
			return true
		case "openai.v1.responses-websocket":
			// One admitted response.create per exchange (token-metered
			// WebSocket session).
			return true
		case "compat.chat-completions":
			// Generic compatible servers carry no provider usage contract;
			// the surface must declare OpenAI or DeepSeek usage semantics
			// (checked when the invocation is prepared). Both settle as
			// uncached and cached input; DeepSeek's cache hit is the cached
			// part.
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
		return (binding.Provider == "google" && binding.OperationID == "interaction.create") || (binding.Provider == "gemini-developer" && binding.OperationID == "gemini.interactions.create")
	case quotaProfileEmbedding:
		switch binding.OperationID {
		case "openai.v1.embeddings":
			return binding.Provider == "openai"
		case "azure.v1.embeddings", "azure.deployments.embeddings":
			return binding.Provider == "azure-openai"
		case "gemini.models.embedContent", "gemini.models.batchEmbedContents":
			return binding.Provider == "gemini-developer"
		case "models.predict", "endpoints.predict", "models.embedContent":
			return binding.Provider == "google"
		}
		return false
	case quotaProfileRealtime:
		return binding.OperationID == "openai.v1.realtime"
	case quotaProfileLiveAllowance:
		return binding.Provider == "google" && binding.OperationID == "live.connect"
	}
	return binding.Profile == quotaProfileRequests
}
