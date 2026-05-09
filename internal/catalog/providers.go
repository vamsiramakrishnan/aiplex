package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// BuiltInProviders serves hardcoded LLM provider templates as kind=model
// capabilities.
type BuiltInProviders struct{}

func NewBuiltInProviders() *BuiltInProviders { return &BuiltInProviders{} }

func (b *BuiltInProviders) Name() string             { return "builtin-llm-providers" }
func (b *BuiltInProviders) Kind() capability.Kind    { return capability.KindModel }

func (b *BuiltInProviders) Fetch(_ context.Context) ([]models.Template, error) {
	defs := []struct {
		ID, Name, Desc, ModelID, Provider string
		Tags                              []string
		Pricing                           capability.Pricing
	}{
		{"gemini-2.5-flash", "Gemini 2.5 Flash", "Google's fast multimodal model", "gemini-2.5-flash", "google", []string{"text", "vision", "code"}, capability.Pricing{Input: 0.15, Output: 0.60}},
		{"gemini-2.5-pro", "Gemini 2.5 Pro", "Google's most capable model", "gemini-2.5-pro", "google", []string{"text", "vision", "code", "reasoning"}, capability.Pricing{Input: 1.25, Output: 10.00}},
		{"claude-opus-4", "Claude Opus 4", "Anthropic's most capable model", "claude-opus-4", "anthropic", []string{"text", "vision", "code", "reasoning"}, capability.Pricing{Input: 15.00, Output: 75.00}},
		{"claude-sonnet-4", "Claude Sonnet 4", "Anthropic's balanced model", "claude-sonnet-4", "anthropic", []string{"text", "vision", "code"}, capability.Pricing{Input: 3.00, Output: 15.00}},
		{"gpt-4.1", "GPT-4.1", "OpenAI's flagship model", "gpt-4.1", "openai", []string{"text", "vision", "code"}, capability.Pricing{Input: 2.00, Output: 8.00}},
		{"gpt-4.1-mini", "GPT-4.1 Mini", "OpenAI's fast model", "gpt-4.1-mini", "openai", []string{"text", "vision", "code"}, capability.Pricing{Input: 0.40, Output: 1.60}},
	}

	out := make([]models.Template, 0, len(defs))
	for _, d := range defs {
		uri := capability.New(capability.KindModel, d.ModelID, "v1")
		pricing := d.Pricing
		out = append(out, models.Template{
			ID:          d.ID,
			Source:      "builtin",
			Kind:        capability.KindModel,
			Name:        d.Name,
			Description: d.Desc,
			ModelID:     d.ModelID,
			Provider:    d.Provider,
			ModelTags:   d.Tags,
			Pricing:     &pricing,
			Capabilities: []capability.Capability{
				{
					URI:          uri.String(),
					Kind:         capability.KindModel,
					Name:         d.ModelID,
					Version:      "v1",
					Description:  d.Desc,
					Provider2:    d.Provider,
					Capabilities: d.Tags,
					Pricing:      &pricing,
				},
			},
			Category: "llm",
			Verified: true,
		})
	}
	return out, nil
}
