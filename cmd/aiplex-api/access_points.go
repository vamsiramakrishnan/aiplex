package main

import (
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// accessPointOptions binds access-point endpoints to operator configuration:
// the skill registry, the authorization server advertised in RFC 9728
// metadata (the verified token issuer, else the Hydra public URL) and the
// browser origins accepted on MCP endpoints.
func accessPointOptions(cfg *config.Config, skills registry.SkillsPlexStore) api.AccessPointOptions {
	options := api.AccessPointOptions{Skills: skills, AllowedOrigins: cfg.AllowedOrigins}
	switch {
	case cfg.JWTIssuer != "":
		options.AuthorizationServers = []string{cfg.JWTIssuer}
	case cfg.HydraPublicURL != "":
		options.AuthorizationServers = []string{cfg.HydraPublicURL}
	}
	return options
}
