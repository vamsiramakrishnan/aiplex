package api

import (
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Built-in developer clients need an actual mapped inference operation, not a
// protocol label on an otherwise unrelated native API surface.
func clientSurfaceReady(surface modelapi.Surface) bool {
	if surface.Provider != nil {
		return providerClientSurfaceReady(surface)
	}
	var ids []string
	switch surface.Protocol {
	case clientplex.GenAI:
		ids = []string{"models.generateContent", "models.streamGenerateContent"}
	case clientplex.OpenAIChat:
		ids = []string{"openai.chat-completions"}
	case clientplex.Responses:
		ids = []string{"response.create"}
	case clientplex.Messages:
		if surface.Publisher != "anthropic" {
			return false
		}
		ids = []string{"anthropic.messages", "models.rawPredict", "models.streamRawPredict"}
	case clientplex.VertexAnthropic:
		if surface.Publisher != "anthropic" {
			return false
		}
		ids = []string{"models.rawPredict", "models.streamRawPredict"}
	default:
		return false
	}
	for _, id := range ids {
		if clientOperationReady(surface, id) {
			return true
		}
	}
	return false
}

func clientOperationReady(surface modelapi.Surface, id string) bool {
	known, err := modelapi.SelectOperations(id)
	if err != nil || len(known) != 1 {
		return false
	}
	canonical := known[0]
	for _, operation := range surface.Operations {
		if operation.ID == canonical.ID && operation.Method == canonical.Method && operation.Path == canonical.Path && operation.Transport == canonical.Transport && operation.Action == canonical.Action && operation.ResourceKind == canonical.ResourceKind && operation.Stateful == canonical.Stateful {
			return true
		}
	}
	return false
}
