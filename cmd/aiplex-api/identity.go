package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentidentity"
	"io"
	"os"
)

type identitySourceSpec struct {
	NativeAgentIdentity *agentidentity.Config `json:"native_agent_identity,omitempty"`
	Issuer              string                `json:"issuer"`
	Audience            string                `json:"audience"`
	JWKSURL             string                `json:"jwks_url"`
	PoolID              string                `json:"pool_id"`
	IsWorkforce         bool                  `json:"is_workforce"`
}
type identitySourceFile struct {
	Sources []identitySourceSpec `json:"sources"`
}

func identityValidator(store auth.IdentityStore, cfg *config.Config) (*auth.WIFValidator, error) {
	if cfg.DevelopmentMode {
		return auth.NewWIFValidator(store, auth.WIFConfig{}), nil
	}
	sources := []identitySourceSpec{}
	if cfg.IdentityProvidersFile != "" {
		file, err := os.Open(cfg.IdentityProvidersFile)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		if err != nil {
			return nil, err
		}
		if len(data) > 1<<20 {
			return nil, fmt.Errorf("identity provider config exceeds 1MiB")
		}
		var document identitySourceFile
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&document); err != nil {
			return nil, err
		}
		if err = decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("trailing identity configuration")
		}
		sources = document.Sources
	} else {
		pool := cfg.WIFPool
		if cfg.IdentityWorkforce {
			pool = cfg.WorkforcePoolID
		}
		sources = append(sources, identitySourceSpec{Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience, JWKSURL: cfg.JWTJWKSURL, PoolID: pool, IsWorkforce: cfg.IdentityWorkforce})
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("configure at least one verified identity source")
	}
	trusted := make([]auth.IdentitySource, 0, len(sources))
	for _, s := range sources {
		var verifier auth.TokenVerifier
		var err error
		if s.NativeAgentIdentity != nil {
			native := s.NativeAgentIdentity
			if s.IsWorkforce || s.Issuer != native.Issuer || s.Audience != native.Audience || s.PoolID != native.TrustDomain {
				return nil, fmt.Errorf("native agent source must bind workload issuer/audience/trust domain exactly")
			}
			if native.BindingMode == agentidentity.RequiredBinding && (cfg.ClientCAFile == "" || cfg.TLSCertFile == "" || cfg.TLSKeyFile == "") {
				return nil, fmt.Errorf("bound Agent Identity requires direct TLS and client CA configuration")
			}
			verifier, err = auth.NewNativeAgentVerifier(*native, auth.JWKSConfig{})
		} else {
			verifier, err = auth.NewJWKSVerifier(auth.JWKSConfig{Issuer: s.Issuer, Audience: s.Audience, JWKSURL: s.JWKSURL})
		}
		if err != nil {
			return nil, err
		}
		trusted = append(trusted, auth.IdentitySource{Issuer: s.Issuer, Audience: s.Audience, Verifier: verifier, PoolID: s.PoolID, IsWorkforce: s.IsWorkforce})
	}
	return auth.NewWIFValidatorFromSources(store, trusted)
}
