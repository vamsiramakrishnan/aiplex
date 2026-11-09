package auth

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

var canonicalPrincipal = regexp.MustCompile(`^principal-[0-9a-f]{64}$`)

// IsCanonicalPrincipalID distinguishes Aiplex's issuer/pool/subject identity
// keys from mutable aliases and native provider resource selectors.
func IsCanonicalPrincipalID(value string) bool { return canonicalPrincipal.MatchString(value) }

// NormalizeScopes validates individual OAuth scope tokens before joining them
// into a space-delimited server field. A single list entry cannot inject a
// second permission. Empty lists explicitly clear manual grants.
func NormalizeScopes(scopes []string) ([]string, error) {
	if len(scopes) > 1024 {
		return nil, fmt.Errorf("too many scopes")
	}
	normalized := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, scope := range scopes {
		if scope == "" || len(scope) > 1024 {
			return nil, fmt.Errorf("bounded nonempty scope tokens required")
		}
		for _, ch := range scope {
			if ch < 0x21 || ch > 0x7e || ch == '"' || ch == '\\' {
				return nil, fmt.Errorf("each scope must be one OAuth scope token")
			}
		}
		if !seen[scope] {
			seen[scope] = true
			normalized = append(normalized, scope)
		}
	}
	sort.Strings(normalized)
	return normalized, nil
}

// AgentActorSubject is stable within the issuing OAuth authority. A configured
// native SPIFFE identity is retained; registrations without one use the actual
// immutable OAuth client ID instead of emitting an empty delegated actor.
func AgentActorSubject(agent *models.Agent) string {
	if agent == nil {
		return ""
	}
	if agent.SpiffeID != "" {
		return agent.SpiffeID
	}
	return agent.ClientID
}
