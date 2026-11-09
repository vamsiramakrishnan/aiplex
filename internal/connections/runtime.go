// Package connections binds public Plex contracts to host-owned credentials and
// tenant-scoped persistence. It never obtains credentials from request payloads.
package connections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
	"github.com/vamsiramakrishnan/aiplex/pkg/deployment"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type Identity struct {
	AllowedOrigins []string `json:"allowed_origins"`
	ID             string   `json:"id"`
	TenantID       string   `json:"tenant_id"`
	Type           string   `json:"type"`
	Scopes         []string `json:"scopes,omitempty"`
	TokenEnv       string   `json:"token_env,omitempty"`
	Audience       string   `json:"audience,omitempty"`
}
type Config struct {
	GovernanceProviders []GovernanceProvider      `json:"governance_providers,omitempty"`
	DeploymentProviders []DeploymentProvider      `json:"deployment_providers,omitempty"`
	Network             endpointpolicy.Config     `json:"network"`
	Identities          []Identity                `json:"identities"`
	MetadataDocuments   []toolauth.ClientMetadata `json:"metadata_documents,omitempty"`
	PreRegistered       []toolauth.Registration   `json:"pre_registered,omitempty"`
}
type Runtime struct {
	Policy        *endpointpolicy.Policy
	HTTP          *http.Client
	Streaming     *http.Client
	Config        Config
	Secrets       *secrets.Manager
	mu            sync.Mutex
	sources       map[string]oauth2.TokenSource
	azure         map[string]*azidentity.DefaultAzureCredential
	governanceAWS map[string]aws.CredentialsProvider
}

func New(cfg Config, sm *secrets.Manager) (*Runtime, error) {
	cfg.Identities = append([]Identity(nil), cfg.Identities...)
	for i := range cfg.Identities {
		cfg.Identities[i].Scopes = append([]string(nil), cfg.Identities[i].Scopes...)
		cfg.Identities[i].AllowedOrigins = append([]string(nil), cfg.Identities[i].AllowedOrigins...)
	}
	cfg.Network.AllowedOrigins = append([]string(nil), cfg.Network.AllowedOrigins...)
	cfg.Network.AllowPrivateCIDRs = append([]string(nil), cfg.Network.AllowPrivateCIDRs...)

	if len(cfg.Network.AllowedOrigins) > 0 {
		if _, e := endpointpolicy.New(cfg.Network); e != nil {
			return nil, e
		}
	}
	seen := map[string]bool{}
	for _, id := range cfg.Identities {
		k := id.TenantID + "\x00" + id.ID
		if id.ID == "" || id.TenantID == "" || seen[k] {
			return nil, fmt.Errorf("invalid duplicate identity profile")
		}
		seen[k] = true
		switch id.Type {
		case "gcp-adc", "azure-default":
		case "bearer-env":
			if id.TokenEnv == "" {
				return nil, fmt.Errorf("bearer identity needs operator environment reference")
			}
		case "aws-default":
		default:
			return nil, fmt.Errorf("unsupported credential adapter %q", id.Type)
		}
	}
	runtime := &Runtime{Config: cfg, Secrets: sm, sources: map[string]oauth2.TokenSource{}, azure: map[string]*azidentity.DefaultAzureCredential{}}
	if len(cfg.Network.AllowedOrigins) > 0 {
		policy, e := endpointpolicy.New(cfg.Network)
		if e != nil {
			return nil, e
		}
		runtime.Policy = policy
		runtime.HTTP = policy.Client()
		runtime.Streaming = policy.StreamingClient()
	}
	return runtime, nil
}
func (r *Runtime) Identity(tenant, ref string) (Identity, error) {
	for _, id := range r.Config.Identities {
		if id.TenantID == tenant && id.ID == ref {
			id.Scopes = append([]string(nil), id.Scopes...)
			id.AllowedOrigins = append([]string(nil), id.AllowedOrigins...)
			return id, nil
		}
	}
	return Identity{}, fmt.Errorf("tenant identity profile not configured")
}
func (r *Runtime) Token(ctx context.Context, tenant, ref string) (string, error) {
	id, e := r.Identity(tenant, ref)
	if e != nil {
		return "", e
	}
	if id.Type == "bearer-env" {
		value := os.Getenv(id.TokenEnv)
		if value == "" {
			return "", fmt.Errorf("operator bearer credential unavailable")
		}
		return value, nil
	}
	if id.Type == "azure-default" {
		key := tenant + "\x00" + ref
		r.mu.Lock()
		credential := r.azure[key]
		if credential == nil {
			credential, e = azidentity.NewDefaultAzureCredential(nil)
			if e != nil {
				r.mu.Unlock()
				return "", fmt.Errorf("Azure default credential chain unavailable")
			}
			r.azure[key] = credential
		}
		r.mu.Unlock()
		scopes := id.Scopes
		if len(scopes) == 0 {
			scopes = []string{"https://ai.azure.com/.default"}
		}
		token, e := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: scopes})
		if e != nil {
			return "", fmt.Errorf("Azure token acquisition failed")
		}
		return token.Token, nil
	}
	if id.Type != "gcp-adc" {
		return "", fmt.Errorf("profile is not a bearer credential adapter")
	}
	// Each cache entry binds tenant/profile/scopes. ADC handles GCP attached
	// identities and external-account WIF exchanges without service-account keys.
	key := tenant + "\x00" + ref + "\x00" + fmt.Sprint(id.Scopes)
	r.mu.Lock()
	source := r.sources[key]
	if source == nil {
		scopes := append([]string(nil), id.Scopes...)
		if len(scopes) == 0 {
			scopes = []string{"https://www.googleapis.com/auth/cloud-platform"}
		}
		credentials, e := google.FindDefaultCredentials(context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: 10 * time.Second}), scopes...)
		if e != nil {
			r.mu.Unlock()
			return "", fmt.Errorf("Google ADC unavailable")
		}
		source = oauth2.ReuseTokenSource(nil, credentials.TokenSource)
		r.sources[key] = source
	}
	r.mu.Unlock()
	token, e := source.Token()
	if e != nil {
		return "", fmt.Errorf("Google token refresh failed")
	}
	return token.AccessToken, nil
}

type secretCredential struct {
	Tenant, Issuer, Profile, ClientID, ClientSecret, RegistrationAccessToken, TokenEndpointAuthMethod string
	SecretExpiresAt                                                                                   int64
	RedirectURIs                                                                                      []string
}
type SecretVersionStore interface {
	GetLatestVersion(context.Context, string) ([]byte, string, error)
	PutVersion(context.Context, string, []byte) (string, error)
}
type SecretCredentials struct{ Manager SecretVersionStore }

func credentialID(k toolauth.CredentialKey) string {
	b, _ := json.Marshal([]string{k.TenantID, k.Issuer, k.ProfileID})
	sum := sha256.Sum256(b)
	return "aiplex-oauth-" + hex.EncodeToString(sum[:])
}
func (s SecretCredentials) Get(ctx context.Context, k toolauth.CredentialKey) (toolauth.Credential, string, error) {
	if s.Manager == nil {
		return toolauth.Credential{}, "", fmt.Errorf("secret storage not configured")
	}
	id := credentialID(k)
	payload, version, e := s.Manager.GetLatestVersion(ctx, id)
	if e != nil {
		if status.Code(e) == codes.NotFound {
			return toolauth.Credential{}, "", toolauth.ErrCredentialsNotFound
		}
		return toolauth.Credential{}, "", e
	}
	var doc secretCredential
	if e = json.Unmarshal(payload, &doc); e != nil {
		return toolauth.Credential{}, "", e
	}
	if doc.Tenant != k.TenantID || doc.Issuer != k.Issuer || doc.Profile != k.ProfileID {
		return toolauth.Credential{}, "", fmt.Errorf("credential namespace mismatch")
	}
	return toolauth.Credential{ClientID: doc.ClientID, ClientSecret: doc.ClientSecret, RegistrationAccessToken: doc.RegistrationAccessToken, SecretExpiresAt: doc.SecretExpiresAt, TokenEndpointAuthMethod: doc.TokenEndpointAuthMethod, RedirectURIs: doc.RedirectURIs}, version, nil
}
func (s SecretCredentials) Put(ctx context.Context, k toolauth.CredentialKey, c toolauth.Credential) (string, error) {
	if s.Manager == nil {
		return "", fmt.Errorf("secret storage not configured")
	}
	if k.TenantID == "" || k.Issuer == "" || k.ProfileID == "" {
		return "", fmt.Errorf("credential namespace required")
	}
	id := credentialID(k)
	payload, e := json.Marshal(secretCredential{k.TenantID, k.Issuer, k.ProfileID, c.ClientID, c.ClientSecret, c.RegistrationAccessToken, c.TokenEndpointAuthMethod, c.SecretExpiresAt, append([]string(nil), c.RedirectURIs...)})
	if e != nil {
		return "", e
	}
	return s.Manager.PutVersion(ctx, id, payload)
}

// CheckDestination binds a tenant credential profile to its explicitly approved
// origins; the global transport allowlist alone is not credential authority.
func (r *Runtime) CheckDestination(tenant, ref, origin string) error {
	id, e := r.Identity(tenant, ref)
	if e != nil {
		return e
	}
	if len(id.AllowedOrigins) == 0 {
		return fmt.Errorf("identity profile needs explicit allowed_origins")
	}
	p, e := endpointpolicy.New(endpointpolicy.Config{AllowedOrigins: id.AllowedOrigins, AllowLoopback: r.Config.Network.AllowLoopback})
	if e != nil {
		return e
	}
	return p.ValidateURL(origin)
}

// DeploymentProvider is operator-owned. EnvironmentRefs names host variables;
// their values never enter resource specs or public plan records.
type DeploymentProvider struct {
	Config          deployment.Config `json:"config"`
	EnvironmentRefs []string          `json:"environment_refs,omitempty"`
}
