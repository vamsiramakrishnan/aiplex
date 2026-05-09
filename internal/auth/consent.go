package auth

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// ConsentHandler implements Hydra's consent webhook.
// It computes effective permissions as A ∩ B ∩ C
// (agent ceiling ∩ user ceiling ∩ requested caps).
type ConsentHandler struct {
	hydra *HydraClient
	store registry.Store
}

// NewConsentHandler creates a consent handler.
func NewConsentHandler(hydra *HydraClient, store registry.Store) *ConsentHandler {
	return &ConsentHandler{hydra: hydra, store: store}
}

// HandleConsent processes a consent challenge:
//  1. Fetches the consent request from Hydra
//  2. Looks up Dimension A (agent ceiling) and Dimension B (user ceiling)
//  3. Computes A ∩ B ∩ C (requested caps)
//  4. Accepts the consent with the intersection in the `caps` claim
func (ch *ConsentHandler) HandleConsent(ctx context.Context, challenge string) (redirectURL string, err error) {
	cr, err := ch.hydra.GetConsentRequest(ctx, challenge)
	if err != nil {
		return "", err
	}

	// Dimension A: agent ceiling
	agent, err := ch.store.GetAgent(ctx, cr.Client.ClientID)
	if err != nil {
		return "", err
	}

	// Dimension B: user ceiling
	userCaps, err := ch.store.GetUserCaps(ctx, cr.Subject)
	if err != nil {
		return "", err
	}

	// Dimension C: requested caps (Hydra's `requested_scope` carries cap URIs).
	requested := make(capability.CapSet, 0, len(cr.RequestedScope))
	for _, uri := range cr.RequestedScope {
		requested = append(requested, capability.Cap{URI: uri})
	}

	// Effective = A ∩ B ∩ C
	granted := agent.AllowedCaps.Intersect(userCaps).Intersect(requested)

	actClaim := map[string]string{
		"sub": agent.SpiffeID,
	}

	return ch.hydra.AcceptConsent(ctx, challenge, granted, actClaim)
}
