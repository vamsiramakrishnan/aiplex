package main

import (
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

// toolCredentialProvider builds the dispatch-time credential provider for
// tool connections with an explicit auth block. Without an outbound endpoint
// policy there is no provider and such connections fail closed.
func toolCredentialProvider(runtime *connections.Runtime, sm *secrets.Manager, development bool) (*toolcredential.Provider, error) {
	if runtime == nil || runtime.Policy == nil || runtime.HTTP == nil {
		return nil, nil
	}
	var resolver toolcredential.SecretResolver
	switch {
	case sm != nil:
		resolver = connections.ToolSecrets{Manager: sm}
	case development:
		resolver = connections.DevelopmentToolSecrets{}
	}
	return toolcredential.NewProvider(toolcredential.Config{HTTPClient: runtime.HTTP, Policy: runtime.Policy, Secrets: resolver, IDTokens: runtime})
}
