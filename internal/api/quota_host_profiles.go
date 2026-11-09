package api

const (
	quotaProfileOpenAI      = "openai-text"
	quotaProfileAnthropic   = "anthropic-text"
	quotaProfileInteraction = "gemini-interaction-text"
)

func quotaStrictProfile(profile string) bool {
	switch profile {
	case quotaProfileGemini, quotaProfileOpenAI, quotaProfileAnthropic, quotaProfileInteraction:
		return true
	}
	return false
}
func quotaProfileCounters(profile string) []string {
	switch profile {
	case quotaProfileGemini, quotaProfileInteraction:
		return []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"}
	case quotaProfileOpenAI, quotaProfileAnthropic:
		return []string{"uncached_input_tokens", "cached_tokens", "output_tokens"}
	}
	return nil
}
func quotaProfileOperation(binding QuotaBinding) bool {
	switch binding.Profile {
	case quotaProfileGemini:
		return binding.Provider == "google" && (binding.OperationID == "models.generateContent" || binding.OperationID == "models.streamGenerateContent" || binding.OperationID == "endpoints.generateContent" || binding.OperationID == "endpoints.streamGenerateContent")
	case quotaProfileOpenAI:
		return binding.OperationID == "openai.chat-completions" || binding.OperationID == "response.create"
	case quotaProfileAnthropic:
		return binding.OperationID == "anthropic.messages" || ((binding.OperationID == "models.rawPredict" || binding.OperationID == "models.streamRawPredict") && binding.Provider == "anthropic")
	case quotaProfileInteraction:
		return binding.Provider == "google" && binding.OperationID == "interaction.create"
	}
	return binding.Profile == quotaProfileRequests
}
