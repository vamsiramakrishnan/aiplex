// Package toolcredential resolves the upstream credential a managed MCP tool
// connection presents at dispatch time. Configuration carries only opaque
// secret references; values are resolved per dispatch, applied only to the
// connection's configured origin and never serialized, logged or echoed.
package toolcredential

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Auth types. An absent auth block on a connection keeps the legacy behaviour
// selected by its execution credential mode (operator identity or OAuthUser).
const (
	None                   = "none"
	APIKey                 = "api_key"
	Bearer                 = "bearer"
	Basic                  = "basic"
	OAuthUser              = "oauth_user"
	OAuthClientCredentials = "oauth_client_credentials"
	TokenExchange          = "token_exchange"
	GoogleIDToken          = "google_id_token"
)

const (
	InHeader = "header"
	InQuery  = "query"

	ClientSecretBasic = "client_secret_basic"
	ClientSecretPost  = "client_secret_post"

	TokenTypeJWT         = "urn:ietf:params:oauth:token-type:jwt"
	TokenTypeAccessToken = "urn:ietf:params:oauth:token-type:access_token"
	TokenTypeIDToken     = "urn:ietf:params:oauth:token-type:id_token"
	grantTokenExchange   = "urn:ietf:params:oauth:grant-type:token-exchange"
)

// Auth is the explicit `auth` block of a tool connection. Every field other
// than Type belongs to specific types; Validate rejects fields that do not
// apply to the selected type, so a block cannot silently carry unused data.
type Auth struct {
	Type string `json:"type"`

	// api_key
	In     string `json:"in,omitempty"`
	Name   string `json:"name,omitempty"`
	Prefix string `json:"prefix,omitempty"`

	// api_key, bearer, basic (password)
	SecretRef string `json:"secret_ref,omitempty"`
	// basic
	Username string `json:"username,omitempty"`

	// oauth_client_credentials, token_exchange
	TokenEndpoint    string   `json:"token_endpoint,omitempty"`
	ClientID         string   `json:"client_id,omitempty"`
	ClientSecretRef  string   `json:"client_secret_ref,omitempty"`
	ClientAuthMethod string   `json:"client_auth_method,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	Resource         string   `json:"resource,omitempty"`

	// token_exchange; Audience is also the google_id_token audience.
	Audience           string `json:"audience,omitempty"`
	SubjectTokenType   string `json:"subject_token_type,omitempty"`
	RequestedTokenType string `json:"requested_token_type,omitempty"`

	// google_id_token: operator identity profile of type gcp-adc.
	IdentityRef string `json:"identity_ref,omitempty"`
}

var (
	headerToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,64}$")
	queryName   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	secretRef   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$`)
	prefixToken = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)
	identityRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

// deniedHeaders cannot carry a credential: hop-by-hop, framing, routing and
// MCP transport headers are owned by the gateway, not by a connection.
var deniedHeaders = map[string]bool{
	"host": true, "connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "proxy-connection": true, "te": true, "trailer": true,
	"trailers": true, "transfer-encoding": true, "upgrade": true, "content-length": true,
	"content-type": true, "content-encoding": true, "accept": true, "accept-encoding": true,
	"expect": true, "cookie": true, "set-cookie": true, "forwarded": true, "via": true,
	"mcp-protocol-version": true, "mcp-session-id": true, "last-event-id": true,
	"idempotency-key": true, "x-request-id": true, "traceparent": true, "tracestate": true,
}

// ValidHeaderName reports whether a header may carry a connection credential.
func ValidHeaderName(name string) bool {
	if !headerToken.MatchString(name) {
		return false
	}
	lower := strings.ToLower(name)
	return !deniedHeaders[lower] && !strings.HasPrefix(lower, "x-forwarded-") && !strings.HasPrefix(lower, "proxy-") && !strings.HasPrefix(lower, "x-aiplex-")
}

// ValidSecretRef reports whether ref is an opaque reference name. References
// are names in the tenant's secret namespace, never secret values.
func ValidSecretRef(ref string) bool { return secretRef.MatchString(ref) }

type fieldSet map[string]bool

func (a Auth) present() fieldSet {
	set := fieldSet{}
	mark := func(name string, ok bool) {
		if ok {
			set[name] = true
		}
	}
	mark("in", a.In != "")
	mark("name", a.Name != "")
	mark("prefix", a.Prefix != "")
	mark("secret_ref", a.SecretRef != "")
	mark("username", a.Username != "")
	mark("token_endpoint", a.TokenEndpoint != "")
	mark("client_id", a.ClientID != "")
	mark("client_secret_ref", a.ClientSecretRef != "")
	mark("client_auth_method", a.ClientAuthMethod != "")
	mark("scopes", a.Scopes != nil)
	mark("resource", a.Resource != "")
	mark("audience", a.Audience != "")
	mark("subject_token_type", a.SubjectTokenType != "")
	mark("requested_token_type", a.RequestedTokenType != "")
	mark("identity_ref", a.IdentityRef != "")
	return set
}

// Validate checks the block in isolation. Origin policy for token endpoints,
// secret existence and identity profiles are checked by the host.
func (a Auth) Validate() error {
	spec, ok := catalogByType[a.Type]
	if !ok {
		return fmt.Errorf("auth.type must be one of none, api_key, bearer, basic, oauth_user, oauth_client_credentials, token_exchange, google_id_token")
	}
	allowed := fieldSet{}
	for _, field := range spec.Fields {
		allowed[field.Name] = true
		if field.Required && !a.present()[field.Name] {
			return fmt.Errorf("auth.%s is required for %s", field.Name, a.Type)
		}
	}
	for name := range a.present() {
		if !allowed[name] {
			return fmt.Errorf("auth.%s is not valid for %s", name, a.Type)
		}
	}
	switch a.Type {
	case APIKey:
		switch a.In {
		case InHeader:
			if !ValidHeaderName(a.Name) {
				return fmt.Errorf("auth.name must be a header token and not a hop-by-hop, routing or gateway-owned header")
			}
			if a.Prefix != "" && !prefixToken.MatchString(a.Prefix) {
				return fmt.Errorf("auth.prefix must be a short scheme token")
			}
		case InQuery:
			if !queryName.MatchString(a.Name) {
				return fmt.Errorf("auth.name must be a bounded query parameter name")
			}
			if a.Prefix != "" {
				return fmt.Errorf("auth.prefix applies only to header API keys")
			}
		default:
			return fmt.Errorf("auth.in must be header or query")
		}
	case Basic:
		if len(a.Username) > 256 || strings.ContainsAny(a.Username, ":") || !printable(a.Username) {
			return fmt.Errorf("auth.username must be printable and must not contain ':'")
		}
	case OAuthClientCredentials, TokenExchange:
		if err := validEndpoint(a.TokenEndpoint, "auth.token_endpoint"); err != nil {
			return err
		}
		if a.Type == OAuthClientCredentials && a.ClientAuthMethod != ClientSecretBasic && a.ClientAuthMethod != ClientSecretPost {
			return fmt.Errorf("auth.client_auth_method must be client_secret_basic or client_secret_post")
		}
		if a.Type == TokenExchange {
			if (a.ClientSecretRef != "" || a.ClientAuthMethod != "") && (a.ClientID == "" || a.ClientSecretRef == "" || (a.ClientAuthMethod != ClientSecretBasic && a.ClientAuthMethod != ClientSecretPost)) {
				return fmt.Errorf("token exchange client authentication requires client_id, client_secret_ref and client_secret_basic or client_secret_post")
			}
			if a.Audience == "" && a.Resource == "" {
				return fmt.Errorf("token exchange requires auth.audience or auth.resource")
			}
			if a.SubjectTokenType != "" && a.SubjectTokenType != TokenTypeJWT && a.SubjectTokenType != TokenTypeAccessToken && a.SubjectTokenType != TokenTypeIDToken {
				return fmt.Errorf("auth.subject_token_type must be a JWT, access token or ID token URN")
			}
			if a.RequestedTokenType != "" && a.RequestedTokenType != TokenTypeAccessToken && a.RequestedTokenType != TokenTypeJWT {
				return fmt.Errorf("auth.requested_token_type must be an access token or JWT URN")
			}
			if a.Audience != "" && (len(a.Audience) > 1024 || !printable(a.Audience) || strings.Contains(a.Audience, " ")) {
				return fmt.Errorf("auth.audience must be a bounded printable identifier")
			}
		}
		if a.ClientID != "" && (len(a.ClientID) > 512 || !printable(a.ClientID)) {
			return fmt.Errorf("auth.client_id must be printable and bounded")
		}
		if a.ClientSecretRef != "" && !ValidSecretRef(a.ClientSecretRef) {
			return fmt.Errorf("auth.client_secret_ref must be a secret reference name, not a secret value")
		}
		if err := validScopes(a.Scopes); err != nil {
			return err
		}
		if a.Resource != "" {
			if err := validEndpoint(a.Resource, "auth.resource"); err != nil {
				return err
			}
		}
	case GoogleIDToken:
		if !identityRef.MatchString(a.IdentityRef) {
			return fmt.Errorf("auth.identity_ref must name an operator identity profile")
		}
		if err := validEndpoint(a.Audience, "auth.audience"); err != nil && (len(a.Audience) > 1024 || !printable(a.Audience) || strings.Contains(a.Audience, " ")) {
			return fmt.Errorf("auth.audience must be a fixed URL or bounded identifier")
		}
	}
	if a.SecretRef != "" && !ValidSecretRef(a.SecretRef) {
		return fmt.Errorf("auth.secret_ref must be a secret reference name, not a secret value")
	}
	return nil
}

func validScopes(scopes []string) error {
	if scopes != nil && len(scopes) == 0 {
		return fmt.Errorf("auth.scopes must be omitted or non-empty")
	}
	if len(scopes) > 64 {
		return fmt.Errorf("auth.scopes is bounded to 64 entries")
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if scope == "" || len(scope) > 256 || seen[scope] || !printable(scope) || strings.ContainsAny(scope, " \"\\") {
			return fmt.Errorf("auth.scopes must be unique scope tokens")
		}
		seen[scope] = true
	}
	return nil
}

func validEndpoint(raw, field string) error {
	u, err := url.Parse(raw)
	if err != nil || raw == "" || len(raw) > 2048 || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "\\\x00\r\n ") || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("%s must be an absolute URL without credentials or fragment", field)
	}
	return nil
}

func printable(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// SecretRefs lists the references the block resolves at dispatch.
func (a Auth) SecretRefs() []string {
	var refs []string
	if a.SecretRef != "" {
		refs = append(refs, a.SecretRef)
	}
	if a.ClientSecretRef != "" {
		refs = append(refs, a.ClientSecretRef)
	}
	return refs
}

// Field describes one auth block member for generated forms.
type Field struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	SecretRef   bool     `json:"secret_reference"`
	Enum        []string `json:"enum,omitempty"`
	Description string   `json:"description"`
}

// TypeSpec describes one auth type.
type TypeSpec struct {
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Execution   string  `json:"execution_credential_mode"`
	Fields      []Field `json:"fields"`
}

var catalog = []TypeSpec{
	{Type: None, Title: "No credential", Execution: "connection-auth", Description: "The gateway sends no upstream credential.", Fields: []Field{}},
	{Type: APIKey, Title: "API key", Execution: "connection-auth", Description: "A secret reference sent in a named header or, only when in=query, a query parameter.", Fields: []Field{
		{Name: "in", Type: "string", Required: true, Enum: []string{InHeader, InQuery}, Description: "Where the key is sent. Query placement must be chosen explicitly."},
		{Name: "name", Type: "string", Required: true, Description: "Header or query parameter name. Hop-by-hop, routing and gateway-owned headers are rejected."},
		{Name: "prefix", Type: "string", Description: "Optional scheme placed before the key in a header, for example Token."},
		{Name: "secret_ref", Type: "string", Required: true, SecretRef: true, Description: "Reference to the API key secret."},
	}},
	{Type: Bearer, Title: "Static bearer token", Execution: "connection-auth", Description: "A secret reference sent as Authorization: Bearer.", Fields: []Field{
		{Name: "secret_ref", Type: "string", Required: true, SecretRef: true, Description: "Reference to the bearer token secret."},
	}},
	{Type: Basic, Title: "HTTP Basic", Execution: "connection-auth", Description: "A username and a password secret reference sent as Authorization: Basic.", Fields: []Field{
		{Name: "username", Type: "string", Required: true, Description: "Username; must not contain ':'."},
		{Name: "secret_ref", Type: "string", Required: true, SecretRef: true, Description: "Reference to the password secret."},
	}},
	{Type: OAuthUser, Title: "Delegated user OAuth", Execution: "delegated-oauth", Description: "The existing per-user OAuth grant (pre-registered, CIMD or DCR client). Configure issuer and client on the connection.", Fields: []Field{}},
	{Type: OAuthClientCredentials, Title: "OAuth client credentials", Execution: "connection-auth", Description: "A service token from a policy-checked token endpoint, cached per tenant and connection configuration until shortly before expiry.", Fields: []Field{
		{Name: "token_endpoint", Type: "url", Required: true, Description: "Token endpoint; must pass the outbound endpoint policy."},
		{Name: "client_id", Type: "string", Required: true, Description: "OAuth client identifier."},
		{Name: "client_secret_ref", Type: "string", Required: true, SecretRef: true, Description: "Reference to the client secret."},
		{Name: "client_auth_method", Type: "string", Required: true, Enum: []string{ClientSecretBasic, ClientSecretPost}, Description: "Token endpoint client authentication."},
		{Name: "scopes", Type: "string[]", Description: "Requested scopes."},
		{Name: "resource", Type: "url", Description: "Optional RFC 8707 resource indicator."},
	}},
	{Type: TokenExchange, Title: "Token exchange", Execution: "connection-auth", Description: "RFC 8693 exchange of the caller's verified token for a downstream audience. Cached per caller and never shared across principals.", Fields: []Field{
		{Name: "token_endpoint", Type: "url", Required: true, Description: "Security token service endpoint; must pass the outbound endpoint policy."},
		{Name: "audience", Type: "string", Description: "Requested audience. Either audience or resource is required."},
		{Name: "resource", Type: "url", Description: "Requested RFC 8707 resource."},
		{Name: "scopes", Type: "string[]", Description: "Requested scopes."},
		{Name: "subject_token_type", Type: "string", Enum: []string{TokenTypeJWT, TokenTypeAccessToken, TokenTypeIDToken}, Description: "Type of the caller's token; defaults to JWT."},
		{Name: "requested_token_type", Type: "string", Enum: []string{TokenTypeAccessToken, TokenTypeJWT}, Description: "Requested issued token type; defaults to access token."},
		{Name: "client_id", Type: "string", Description: "Optional client identifier for the exchange."},
		{Name: "client_secret_ref", Type: "string", SecretRef: true, Description: "Optional reference to the exchange client secret."},
		{Name: "client_auth_method", Type: "string", Enum: []string{ClientSecretBasic, ClientSecretPost}, Description: "Required with client_secret_ref."},
	}},
	{Type: GoogleIDToken, Title: "Google ID token", Execution: "connection-auth", Description: "A workload-issued Google ID token for a fixed audience, from an operator identity profile of type gcp-adc.", Fields: []Field{
		{Name: "audience", Type: "string", Required: true, Description: "Fixed audience, usually the Cloud Run or IAP-protected service URL."},
		{Name: "identity_ref", Type: "string", Required: true, Description: "Operator identity profile (type gcp-adc) whose allowed_origins include the connection resource."},
	}},
}

var catalogByType = func() map[string]TypeSpec {
	out := map[string]TypeSpec{}
	for _, spec := range catalog {
		out[spec.Type] = spec
	}
	return out
}()

// Catalog returns a copy of every auth type description.
func Catalog() []TypeSpec {
	out := make([]TypeSpec, len(catalog))
	for i, spec := range catalog {
		spec.Fields = append([]Field{}, spec.Fields...)
		for j := range spec.Fields {
			spec.Fields[j].Enum = append([]string(nil), spec.Fields[j].Enum...)
		}
		out[i] = spec
	}
	return out
}
