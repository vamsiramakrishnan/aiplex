package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// BuiltInProviders returns a Source that serves hardcoded LLM provider templates.
type BuiltInProviders struct{}

func NewBuiltInProviders() *BuiltInProviders { return &BuiltInProviders{} }

func (b *BuiltInProviders) Name() string        { return "builtin-llm-providers" }
func (b *BuiltInProviders) Plane() models.Plane { return models.PlaneLLMPlex }

func (b *BuiltInProviders) Fetch(_ context.Context) ([]models.Template, error) {
	return []models.Template{
		{
			ID: "gemini-2.5-flash", Source: "builtin", Plane: models.PlaneLLMPlex,
			Name: "Gemini 2.5 Flash", Description: "Google's fast multimodal model",
			ModelID: "gemini-2.5-flash", Provider: "google",
			Capabilities: []string{"text", "vision", "code"},
			Category: "llm", Verified: true,
			Pricing: &models.Pricing{Input: 0.15, Output: 0.60},
		},
		{
			ID: "gemini-2.5-pro", Source: "builtin", Plane: models.PlaneLLMPlex,
			Name: "Gemini 2.5 Pro", Description: "Google's most capable model",
			ModelID: "gemini-2.5-pro", Provider: "google",
			Capabilities: []string{"text", "vision", "code", "reasoning"},
			Category: "llm", Verified: true,
			Pricing: &models.Pricing{Input: 1.25, Output: 10.00},
		},
		{
			ID: "claude-opus-4", Source: "builtin", Plane: models.PlaneLLMPlex,
			Name: "Claude Opus 4", Description: "Anthropic's most capable model",
			ModelID: "claude-opus-4", Provider: "anthropic",
			Capabilities: []string{"text", "vision", "code", "reasoning"},
			Category: "llm", Verified: true,
			Pricing: &models.Pricing{Input: 15.00, Output: 75.00},
		},
		{
			ID: "claude-sonnet-4", Source: "builtin", Plane: models.PlaneLLMPlex,
			Name: "Claude Sonnet 4", Description: "Anthropic's balanced model",
			ModelID: "claude-sonnet-4", Provider: "anthropic",
			Capabilities: []string{"text", "vision", "code"},
			Category: "llm", Verified: true,
			Pricing: &models.Pricing{Input: 3.00, Output: 15.00},
		},
		{
			ID: "gpt-4.1", Source: "builtin", Plane: models.PlaneLLMPlex,
			Name: "GPT-4.1", Description: "OpenAI's flagship model",
			ModelID: "gpt-4.1", Provider: "openai",
			Capabilities: []string{"text", "vision", "code"},
			Category: "llm", Verified: true,
			Pricing: &models.Pricing{Input: 2.00, Output: 8.00},
		},
		{
			ID: "gpt-4.1-mini", Source: "builtin", Plane: models.PlaneLLMPlex,
			Name: "GPT-4.1 Mini", Description: "OpenAI's fast model",
			ModelID: "gpt-4.1-mini", Provider: "openai",
			Capabilities: []string{"text", "vision", "code"},
			Category: "llm", Verified: true,
			Pricing: &models.Pricing{Input: 0.40, Output: 1.60},
		},
	}, nil
}
