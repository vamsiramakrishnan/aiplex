package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// BuiltInAgents publishes a small reference set of agent-runtime templates.
// kind=agent capabilities front *external* agent runtimes — ADK on Vertex,
// LangGraph on Cloud Run, Letta self-hosted, raw HTTP services. The
// Capability gives them a stable URI, governance, audit and revocation;
// the runtime does the actual LLM/tool work.
type BuiltInAgents struct{}

func NewBuiltInAgents() *BuiltInAgents { return &BuiltInAgents{} }

func (b *BuiltInAgents) Name() string             { return "builtin-agents" }
func (b *BuiltInAgents) Kind() capability.Kind    { return capability.KindAgent }

func (b *BuiltInAgents) Fetch(_ context.Context) ([]models.Template, error) {
	return []models.Template{
		{
			ID:          "adk-tutor",
			Source:      "builtin",
			Kind:        capability.KindAgent,
			Name:        "ADK Tutor",
			Description: "Wraps a Google ADK-built tutor agent as a cap://agent. The ADK runtime stays where it is (Vertex Agent Engine, Cloud Run, anywhere); AIPlex governs delegation, audit, and revocation.",
			Image:       "gcr.io/example/adk-tutor:latest",
			Version:     "v1",
			Category:    "agent",
			Verified:    true,
			Capabilities: []capability.Capability{{
				URI:         "cap://agent/adk-tutor@v1",
				Kind:        capability.KindAgent,
				Name:        "adk-tutor",
				Version:     "v1",
				Description: "Tutor agent built with Google ADK",
				Attrs: capability.Attrs{
					SideEffect:      "external",
					DataClass:       "internal",
					LatencyBudgetMs: 30000,
				},
			}},
		},
		{
			ID:          "letta-research",
			Source:      "builtin",
			Kind:        capability.KindAgent,
			Name:        "Letta Research",
			Description: "Wraps a self-hosted Letta agent for long-form research. Memory lives in Letta; the cap URI is portable.",
			Image:       "ghcr.io/letta-ai/letta:latest",
			Version:     "v1",
			Category:    "agent",
			Verified:    true,
			Capabilities: []capability.Capability{{
				URI:         "cap://agent/letta-research@v1",
				Kind:        capability.KindAgent,
				Name:        "letta-research",
				Version:     "v1",
				Description: "Research agent built with Letta",
				Attrs: capability.Attrs{
					SideEffect:      "external",
					DataClass:       "internal",
					LatencyBudgetMs: 60000,
				},
			}},
		},
	}, nil
}
