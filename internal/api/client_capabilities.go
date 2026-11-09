package api

import (
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Built-in developer clients need an actual mapped inference operation, not a
// protocol label on an otherwise unrelated native API surface.
func clientSurfaceReady(surface modelapi.Surface) bool {
	var ids []string
	switch surface.Protocol {
	case clientplex.GenAI:
		ids = []string{"models.generateContent", "models.streamGenerateContent"}
	case clientplex.OpenAIChat:
		ids = []string{"openai.chat-completions"}
	case clientplex.Responses:
		ids = []string{"response.create"}
	case clientplex.Messages, clientplex.VertexAnthropic:
		if surface.Publisher != "anthropic" {
			return false
		}
		ids = []string{"models.rawPredict", "models.streamRawPredict"}
	default:
		return false
	}
	for _, id := range ids {
		known, _ := modelapi.SelectOperations(id)
		for _, operation := range surface.Operations {
			if operation.ID == id && operation.Method == known[0].Method && operation.Path == known[0].Path {
				return true
			}
		}
	}
	return false
}
