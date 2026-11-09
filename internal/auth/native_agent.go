package auth

import (
	"github.com/vamsiramakrishnan/aiplex/pkg/agentidentity"
	"net/http"
)

// RequestTokenVerifier verifies request-bound credentials such as native Google
// Agent Identity mTLS assertions. Forwarded certificate headers are not trusted.
type RequestTokenVerifier interface {
	VerifyRequest(*http.Request, string) (map[string]any, error)
}

func NewNativeAgentVerifier(config agentidentity.Config, jwks JWKSConfig) (*agentidentity.Verifier, error) {
	endpoint, err := config.JWKSURL()
	if err != nil {
		return nil, err
	}
	jwks.Issuer = config.Issuer
	jwks.Audience = config.Audience
	jwks.JWKSURL = endpoint
	verifier, err := NewJWKSVerifier(jwks)
	if err != nil {
		return nil, err
	}
	return agentidentity.New(config, verifier)
}
