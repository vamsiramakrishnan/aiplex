package aiplex

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
)

type Identity struct {
	PrincipalID string   `json:"principal_id"`
	Subject     string   `json:"subject"`
	Email       string   `json:"email"`
	Provider    string   `json:"provider"`
	PoolID      string   `json:"pool_id"`
	Groups      []string `json:"groups"`
	ActorID     string   `json:"actor_id,omitempty"`
	IsWorkforce bool     `json:"is_workforce"`
}
type IdentityAccess struct {
	Identity    Identity `json:"identity"`
	Roles       []string `json:"roles"`
	Scopes      []string `json:"scopes"`
	GroupScopes []string `json:"group_scopes"`
}

func (c *Client) GetIdentity(ctx context.Context) (IdentityAccess, error) {
	var out IdentityAccess
	err := c.do(ctx, http.MethodGet, "/api/v1/iam/whoami", nil, &out)
	return out, err
}

var canonicalPrincipalID = regexp.MustCompile(`^principal-[a-f0-9]{64}$`)

func validatePrincipalID(id string) error {
	if !canonicalPrincipalID.MatchString(id) {
		return fmt.Errorf("canonical principal ID required; resolve it with whoami --server")
	}
	return nil
}
