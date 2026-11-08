package auth

import (
	"context"
	"fmt"
	"sort"
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

// HandleConsent preserves the server-driven integration API, granting only
// requested scopes that fit both ceilings. Interactive UIs must use the explicit
// selected-scope methods below so unchecked choices cannot become grants.
func (ch *ConsentHandler) HandleConsent(ctx context.Context, challenge string) (string, error) {
	return ch.handleConsent(ctx, challenge, nil, nil, false)
}

func (ch *ConsentHandler) HandleConsentScopes(ctx context.Context, challenge string, selected []string) (string, error) {
	return ch.handleConsent(ctx, challenge, selected, nil, true)
}

// HandleConsentForAccess uses freshly resolved group grants for a verified
// caller. Hydra must use the canonical principal. External subject strings are
// issuer-scoped; matching subjects or email addresses never link identities.
func (ch *ConsentHandler) HandleConsentForAccess(ctx context.Context, challenge string, selected []string, access *models.ResolvedAccess) (string, error) {
	if access == nil {
		return "", fmt.Errorf("verified consent identity is required")
	}
	return ch.handleConsent(ctx, challenge, selected, access, true)
}

func (ch *ConsentHandler) handleConsent(ctx context.Context, challenge string, selected []string, access *models.ResolvedAccess, explicit bool) (string, error) {
	if strings.TrimSpace(challenge) == "" {
		return "", fmt.Errorf("consent challenge is required")
	}
	cr, err := ch.hydra.GetConsentRequest(ctx, challenge)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(cr.Subject) == "" || strings.TrimSpace(cr.Client.ClientID) == "" {
		return "", fmt.Errorf("consent request requires subject and client")
	}
	agent, err := ch.store.GetAgent(ctx, cr.Client.ClientID)
	if err != nil {
		return "", err
	}
	var userScopes []string
	if access != nil {
		identity := access.Identity
		if identity.Subject == "" || identity.Provider == "" || identity.PrincipalID != CanonicalPrincipalID(identity.Provider, identity.PoolID, identity.Subject) || cr.Subject != identity.PrincipalID {
			return "", fmt.Errorf("consent subject does not match verified caller")
		}
		userScopes = access.Scopes
	} else {
		userScopes, err = ch.store.GetUserScopes(ctx, cr.Subject)
		if err != nil {
			return "", err
		}
	}
	choices := selected
	if !explicit {
		choices = cr.RequestedScope
	}
	var granted []string
	seen := make(map[string]bool)
	requested := toSet(cr.RequestedScope)
	for _, scope := range choices {
		if scope == "" || !requested[scope] || !ScopeAllowed(agent.AllowedScopes, scope) || !ScopeAllowed(userScopes, scope) {
			if explicit {
				return "", fmt.Errorf("selected scope %q is not grantable", scope)
			}
			continue
		}
		if !seen[scope] {
			granted = append(granted, scope)
			seen[scope] = true
		}
	}
	sort.Strings(granted)
	return ch.hydra.AcceptConsent(ctx, challenge, granted, map[string]string{"sub": agent.SpiffeID})
}

// ScopeAllowed supports exact scopes and terminal namespace wildcards. A
// wildcard ceiling may cover a concrete requested scope, never the converse.
func ScopeAllowed(ceiling []string, requested string) bool {
	for _, scope := range ceiling {
		if scope == requested || (strings.HasSuffix(scope, ":*") && strings.HasPrefix(requested, strings.TrimSuffix(scope, "*"))) {
			return true
		}
	}
	return false
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
