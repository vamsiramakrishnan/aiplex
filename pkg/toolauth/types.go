// Package toolauth implements OAuth client registration for HTTP MCP tools.
// It is independent of tool catalogs, token issuance and user authentication.
package toolauth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
)

type Mode string

const (
	PreRegistered Mode = "pre_registered"
	CIMD          Mode = "cimd"
	DCR           Mode = "dcr"
)

var ErrCredentialsNotFound = errors.New("OAuth client registration not found")

type Connection struct {
	TenantID    string `json:"tenant_id"`
	ID          string `json:"id"`
	ResourceURL string `json:"resource_url"`
	// Challenge is the WWW-Authenticate value returned by a trusted resource
	// response. It is not allowed to bypass the service's endpoint policy.
	Challenge string `json:"challenge,omitempty"`
	Issuer    string `json:"issuer,omitempty"`
}

type ClientProfile struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	RedirectURIs    []string `json:"redirect_uris"`
	ApplicationType string   `json:"application_type"`
	MetadataURL     string   `json:"metadata_url,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
	RefreshTokens   bool     `json:"refresh_tokens,omitempty"`
}

type ClientMetadata struct {
	ClientID                string   `json:"client_id,omitempty"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	ApplicationType         string   `json:"application_type,omitempty"`
}

type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

type AuthorizationServerMetadata struct {
	Issuer                               string   `json:"issuer"`
	AuthorizationEndpoint                string   `json:"authorization_endpoint"`
	TokenEndpoint                        string   `json:"token_endpoint"`
	RegistrationEndpoint                 string   `json:"registration_endpoint,omitempty"`
	CIMDSupported                        bool     `json:"client_id_metadata_document_supported,omitempty"`
	CodeChallengeMethodsSupported        []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported    []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	AuthorizationResponseIssuerSupported bool     `json:"authorization_response_iss_parameter_supported,omitempty"`
}

type CredentialKey struct{ TenantID, Issuer, ProfileID string }

// Credential is passed only to host-owned secure storage. Secret fields cannot
// be JSON-serialized into configuration, registration responses or snapshots.
type Credential struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"-"`
	RegistrationAccessToken string   `json:"-"`
	SecretExpiresAt         int64    `json:"client_secret_expires_at,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
}

func (c Credential) String() string { return "OAuthCredential(" + c.ClientID + ", secrets=redacted)" }

// CredentialStore implementations must enforce issuer/tenant/profile binding
// and protect secrets. Put returns an opaque secret reference, never the secret.
type CredentialStore interface {
	Get(context.Context, CredentialKey) (Credential, string, error)
	Put(context.Context, CredentialKey, Credential) (string, error)
}

type Registration struct {
	Mode                  Mode     `json:"mode"`
	TenantID              string   `json:"tenant_id"`
	ProfileID             string   `json:"profile_id"`
	Issuer                string   `json:"issuer"`
	Resource              string   `json:"resource"`
	ClientID              string   `json:"client_id"`
	CredentialRef         string   `json:"credential_ref,omitempty"`
	RedirectURIs          []string `json:"redirect_uris"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	ResourceParameter     string   `json:"resource_parameter"`
	PKCEMethod            string   `json:"pkce_method"`
	RequireIssuer         bool     `json:"require_issuer,omitempty"`
	AllowRefresh          bool     `json:"allow_refresh,omitempty"`
}

// RegistrationAuthorizer supplies an optional initial-access token for a
// protected DCR endpoint. Tokens are never accepted from registration requests.
type RegistrationAuthorizer interface {
	InitialAccessToken(context.Context, string) (string, error)
}

type Config struct {
	Network                endpointpolicy.Config
	Credentials            CredentialStore
	RegistrationAuthorizer RegistrationAuthorizer
	PreRegistered          []Registration
	MetadataDocuments      []ClientMetadata
	MetadataCacheTTL       time.Duration
}

type registrationResponse struct {
	ClientMetadata
	ClientSecret            string `json:"client_secret"`
	ClientSecretExpiresAt   int64  `json:"client_secret_expires_at"`
	RegistrationAccessToken string `json:"registration_access_token"`
}

type RegistrationError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *RegistrationError) Error() string { return e.Code + ": " + e.Description }

// CopyJSON is useful for storing the public result in a module-owned resource.
func (r Registration) CopyJSON() json.RawMessage { b, _ := json.Marshal(r); return b }
