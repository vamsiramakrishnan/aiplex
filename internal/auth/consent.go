package auth

import (
	"context"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// ConsentHandler implements Hydra's consent webhook.
// It computes effective permissions as A ∩ B ∩ C (agent ceiling ∩ user ceiling ∩ requested scopes).
type ConsentHandler struct {
	hydra *HydraClient
	store registry.Store
}

// NewConsentHandler creates a consent handler.
func NewConsentHandler(hydra *HydraClient, store registry.Store) *ConsentHandler {
	return &ConsentHandler{hydra: hydra, store: store}
}

// HandleConsent processes a consent challenge:
// 1. Fetches the consent request from Hydra
// 2. Looks up Dimension A (agent ceiling) and Dimension B (user ceiling)
// 3. Computes A ∩ B ∩ C (requested scopes)
// 4. Accepts the consent with the intersection
func (ch *ConsentHandler) HandleConsent(ctx context.Context, challenge string) (redirectURL string, err error) {
	// Fetch consent request from Hydra
	cr, err := ch.hydra.GetConsentRequest(ctx, challenge)
	if err != nil {
		return "", err
	}

	// Dimension A: agent ceiling (from store/Hydra client)
	agent, err := ch.store.GetAgent(ctx, cr.Client.ClientID)
	if err != nil {
		return "", err
	}
	agentScopes := toSet(agent.AllowedScopes)

	// Dimension B: user ceiling (from store)
	userScopes, err := ch.store.GetUserScopes(ctx, cr.Subject)
	if err != nil {
		return "", err
	}
	userSet := toSet(userScopes)

	// Dimension C: requested scopes
	requestedSet := toSet(cr.RequestedScope)

	// Effective = A ∩ B ∩ C
	var granted []string
	for scope := range requestedSet {
		if agentScopes[scope] && userSet[scope] {
			granted = append(granted, scope)
		}
	}

	// Build act claim with agent's SPIFFE ID
	actClaim := map[string]string{
		"sub": agent.SpiffeID,
	}

	return ch.hydra.AcceptConsent(ctx, challenge, granted, actClaim)
}

// ScopesByPlane groups scopes by their plane prefix.
func ScopesByPlane(scopes []string) map[models.Plane][]string {
	result := make(map[models.Plane][]string)
	for _, s := range scopes {
		switch {
		case strings.HasPrefix(s, "mcp:"):
			result[models.PlaneMCPlex] = append(result[models.PlaneMCPlex], s)
		case strings.HasPrefix(s, "a2a:"):
			result[models.PlaneA2APlex] = append(result[models.PlaneA2APlex], s)
		case strings.HasPrefix(s, "llm:"):
			result[models.PlaneLLMPlex] = append(result[models.PlaneLLMPlex], s)
		}
	}
	return result
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}
