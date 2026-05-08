package config

import (
	"context"

	"github.com/sethvargo/go-envconfig"
)

// Config holds all AIPlex API configuration, populated from environment variables.
type Config struct {
	// Server
	Port int    `env:"PORT,default=8080"`
	Host string `env:"HOST,default=0.0.0.0"`

	// Auth (Ory Hydra + Kratos)
	HydraAdminURL  string `env:"HYDRA_ADMIN_URL,default=http://hydra-admin:4445"`
	HydraPublicURL string `env:"HYDRA_PUBLIC_URL,default=http://hydra-public:4444"`
	KratosAdminURL string `env:"KRATOS_ADMIN_URL,default=http://kratos-admin:4434"`
	KratosPublicURL string `env:"KRATOS_PUBLIC_URL,default=http://kratos-public:4433"`

	// Firestore (production persistence)
	GCPProject      string `env:"GCP_PROJECT,default="`
	FirestoreDB     string `env:"FIRESTORE_DATABASE,default=(default)"`

	// Envoy AI Gateway
	GatewayNamespace string `env:"GATEWAY_NAMESPACE,default=aiplex-system"`
	GatewayName       string `env:"GATEWAY_NAME,default=aiplex-gateway"`

	// Identity
	TrustDomain     string `env:"TRUST_DOMAIN,default=aiplex-prod.global"`
	WIFPool         string `env:"WIF_POOL,default=aiplex-agents"`
	WorkforcePoolID string `env:"WORKFORCE_POOL_ID,default=aiplex-users"`

	// Observability
	LogLevel string `env:"LOG_LEVEL,default=info"`
	OTelEndpoint string `env:"OTEL_ENDPOINT,default="`

	// Console SPA origins allowed by CORS. Comma-separated list of full origins
	// (scheme + host[:port]). When empty, CORS is permissive ("*") for local dev;
	// production should always set this.
	AllowedOrigins []string `env:"CONSOLE_ORIGINS,default="`
}

// Load reads Config from the environment.
func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
