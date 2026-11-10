package config

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/sethvargo/go-envconfig"
)

// Config holds all AIPlex API configuration, populated from environment variables.
type Config struct {
	// Server
	Port int    `env:"PORT,default=8080"`
	Host string `env:"HOST,default=0.0.0.0"`

	// Auth (Ory Hydra + Kratos)
	HydraAdminURL   string `env:"HYDRA_ADMIN_URL,default=http://hydra-admin:4445"`
	HydraPublicURL  string `env:"HYDRA_PUBLIC_URL,default=http://hydra-public:4444"`
	KratosAdminURL  string `env:"KRATOS_ADMIN_URL,default=http://kratos-admin:4434"`
	KratosPublicURL string `env:"KRATOS_PUBLIC_URL,default=http://kratos-public:4433"`

	// Firestore (production persistence)
	GCPProject  string `env:"GCP_PROJECT,default="`
	FirestoreDB string `env:"FIRESTORE_DATABASE,default=(default)"`

	// Envoy AI Gateway
	GatewayNamespace string `env:"GATEWAY_NAMESPACE,default=aiplex-system"`
	GatewayName      string `env:"GATEWAY_NAME,default=aiplex-gateway"`

	// Module composition. An empty list starts the shared shell only.
	RoleBindingsFile      string   `env:"AIPLEX_ROLE_BINDINGS_FILE,default="`
	ModuleManifest        string   `env:"AIPLEX_MODULE_MANIFEST,default="`
	IdentityProvidersFile string   `env:"AIPLEX_IDENTITY_PROVIDERS_FILE,default="`
	EnabledModules        []string `env:"AIPLEX_MODULES,default=mcplex,a2aplex,llmplex,skillsplex,clientplex,networking"`
	DevelopmentMode       bool     `env:"AIPLEX_DEV_MODE,default=false"`
	JWTIssuer             string   `env:"JWT_ISSUER,default="`
	JWTAudience           string   `env:"JWT_AUDIENCE,default="`
	JWTJWKSURL            string   `env:"JWT_JWKS_URL,default="`
	IdentityWorkforce     bool     `env:"IDENTITY_WORKFORCE,default=true"`

	// Connected module providers and trusted single-tenant binding.
	ConnectionsFile string `env:"AIPLEX_CONNECTIONS_FILE,default="`
	QuotaFile       string `env:"AIPLEX_QUOTA_FILE,default="`
	TenantID        string `env:"AIPLEX_TENANT_ID,default="`
	PublicURL       string `env:"AIPLEX_PUBLIC_URL,default="`
	TLSCertFile     string `env:"TLS_CERT_FILE,default="`
	TLSKeyFile      string `env:"TLS_KEY_FILE,default="`
	ClientCAFile    string `env:"CLIENT_CA_FILE,default="`

	// Identity
	TrustDomain     string `env:"TRUST_DOMAIN,default=aiplex-prod.global"`
	WIFPool         string `env:"WIF_POOL,default=aiplex-agents"`
	WorkforcePoolID string `env:"WORKFORCE_POOL_ID,default=aiplex-users"`

	// Observability
	LogLevel     string `env:"LOG_LEVEL,default=info"`
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
	// Development mode binds to loopback unless HOST is set explicitly; an
	// explicit non-loopback HOST is refused below rather than overridden.
	if _, set := os.LookupEnv("HOST"); cfg.DevelopmentMode && !set {
		cfg.Host = "127.0.0.1"
	}
	if cfg.DevelopmentMode && cfg.Host != "localhost" {
		ip := net.ParseIP(cfg.Host)
		if ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("AIPLEX_DEV_MODE requires a loopback HOST (127.0.0.1, ::1 or localhost), got %q: development mode is never served on a network interface", cfg.Host)
		}
	}
	return &cfg, nil
}
