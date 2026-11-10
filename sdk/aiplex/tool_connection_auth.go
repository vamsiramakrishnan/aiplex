package aiplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// Tool connection auth types accepted by the gateway. The authoritative list
// and per-type fields come from ListToolAuthTypes.
const (
	ToolAuthNone                   = "none"
	ToolAuthAPIKey                 = "api_key"
	ToolAuthBearer                 = "bearer"
	ToolAuthBasic                  = "basic"
	ToolAuthOAuthUser              = "oauth_user"
	ToolAuthOAuthClientCredentials = "oauth_client_credentials"
	ToolAuthTokenExchange          = "token_exchange"
	ToolAuthGoogleIDToken          = "google_id_token"
)

// Execution credential modes a tool connection can select.
const (
	ToolCredentialConnectionAuth = "connection-auth"
	ToolCredentialDelegatedOAuth = "delegated-oauth"
)

// ToolAuth is the explicit auth block of a tool connection. Secrets are
// always references (names in the tenant secret namespace), never values.
type ToolAuth struct {
	Type string `json:"type"`

	In     string `json:"in,omitempty"`
	Name   string `json:"name,omitempty"`
	Prefix string `json:"prefix,omitempty"`

	SecretRef string `json:"secret_ref,omitempty"`
	Username  string `json:"username,omitempty"`

	TokenEndpoint    string   `json:"token_endpoint,omitempty"`
	ClientID         string   `json:"client_id,omitempty"`
	ClientSecretRef  string   `json:"client_secret_ref,omitempty"`
	ClientAuthMethod string   `json:"client_auth_method,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	Resource         string   `json:"resource,omitempty"`

	Audience           string `json:"audience,omitempty"`
	SubjectTokenType   string `json:"subject_token_type,omitempty"`
	RequestedTokenType string `json:"requested_token_type,omitempty"`

	IdentityRef string `json:"identity_ref,omitempty"`
}

// ToolPolicy is connection-level tool authorization. Patterns are exact tool
// names or prefixes ending in '*'. Deny wins over allow.
type ToolPolicy struct {
	Allow    []string `json:"allow,omitempty"`
	Deny     []string `json:"deny,omitempty"`
	ReadOnly bool     `json:"read_only,omitempty"`
}

// ToolAuthField describes one auth block member for generated forms.
type ToolAuthField struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Required        bool     `json:"required"`
	SecretReference bool     `json:"secret_reference"`
	Enum            []string `json:"enum,omitempty"`
	Description     string   `json:"description"`
}

// ToolAuthType describes one auth type in the catalog.
type ToolAuthType struct {
	Type                    string          `json:"type"`
	Title                   string          `json:"title"`
	Description             string          `json:"description"`
	ExecutionCredentialMode string          `json:"execution_credential_mode"`
	Fields                  []ToolAuthField `json:"fields"`
}

// ToolSecretReferences explains how secret reference fields are written.
type ToolSecretReferences struct {
	Pattern   string `json:"pattern"`
	Namespace string `json:"namespace,omitempty"`
	Note      string `json:"note"`
}

// ToolAuthCatalog is the response of GET /api/v1/mcp/auth-types.
type ToolAuthCatalog struct {
	Types            []ToolAuthType       `json:"types"`
	DefaultType      string               `json:"default_type"`
	DefaultBehaviour string               `json:"default_behaviour"`
	SecretReferences ToolSecretReferences `json:"secret_references"`
	ToolsPolicy      []ToolAuthField      `json:"tools_policy"`
}

// ListToolAuthTypes returns the auth types, their fields and the tools policy
// description the gateway accepts on tool connections.
func (c *Client) ListToolAuthTypes(ctx context.Context) (*ToolAuthCatalog, error) {
	var out ToolAuthCatalog
	if err := c.do(ctx, http.MethodGet, "/api/v1/mcp/auth-types", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

var toolSecretRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$`)

// ValidToolSecretRef reports whether ref is shaped like a secret reference
// name. It cannot tell a reference from a value that happens to fit; the
// gateway checks that the reference exists.
func ValidToolSecretRef(ref string) bool { return toolSecretRef.MatchString(ref) }

// ToolExecution is the managed execution block of a tool connection (the
// fields relevant to auth).
type ToolExecution struct {
	Endpoint       string   `json:"endpoint,omitempty"`
	IdentityRef    string   `json:"identity_ref"`
	CredentialMode string   `json:"credential_mode,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
}

// ToolConnection is a typed read view of a tool-connection resource. Fields
// the SDK does not model are not represented; use UpdateToolConnection to
// change auth and tools policy without losing them.
type ToolConnection struct {
	ID          string         `json:"id"`
	ResourceURL string         `json:"resource_url,omitempty"`
	Execution   *ToolExecution `json:"execution,omitempty"`
	Auth        *ToolAuth      `json:"auth,omitempty"`
	Tools       *ToolPolicy    `json:"tools,omitempty"`
}

// ToolConnectionRecord is a tool connection with its stored revision.
type ToolConnectionRecord struct {
	Revision uint64 `json:"revision"`
	ToolConnection
}

// GetToolConnection reads a tool connection and its revision.
func (c *Client) GetToolConnection(ctx context.Context, id string) (*ToolConnectionRecord, error) {
	record, err := c.GetResource(ctx, ToolConnections, id)
	if err != nil {
		return nil, err
	}
	return decodeToolConnection(record)
}

func decodeToolConnection(record *resources.Record) (*ToolConnectionRecord, error) {
	out := &ToolConnectionRecord{Revision: record.Revision}
	if err := json.Unmarshal(record.Spec, &out.ToolConnection); err != nil {
		return nil, fmt.Errorf("aiplex: decode tool connection: %w", err)
	}
	return out, nil
}

// ToolConnectionChange selects what UpdateToolConnection changes. A nil
// pointer leaves that block as it is; the Clear flags remove the block.
type ToolConnectionChange struct {
	Auth       *ToolAuth
	ClearAuth  bool
	Tools      *ToolPolicy
	ClearTools bool
}

// UpdateToolConnection reads the connection, applies the change and writes it
// back with the revision it read (If-Match), so a concurrent change surfaces
// as a 409 REVISION_CONFLICT *Error instead of being overwritten. Fields the
// SDK does not model are preserved. Setting an auth block also aligns
// execution.credential_mode: oauth_user selects delegated-oauth and every
// other type selects connection-auth with an empty identity_ref (the
// google_id_token identity lives in the auth block).
func (c *Client) UpdateToolConnection(ctx context.Context, id string, change ToolConnectionChange) (*ToolConnectionRecord, error) {
	return c.UpdateToolConnectionWith(ctx, id, func(*ToolConnectionRecord) (ToolConnectionChange, error) { return change, nil })
}

// UpdateToolConnectionWith is UpdateToolConnection for changes that depend on
// the current configuration: build is called with the connection as read, and
// the result is written with that same read's revision.
func (c *Client) UpdateToolConnectionWith(ctx context.Context, id string, build func(current *ToolConnectionRecord) (ToolConnectionChange, error)) (*ToolConnectionRecord, error) {
	record, err := c.GetResource(ctx, ToolConnections, id)
	if err != nil {
		return nil, err
	}
	current, err := decodeToolConnection(record)
	if err != nil {
		return nil, err
	}
	change, err := build(current)
	if err != nil {
		return nil, err
	}
	if (change.Auth != nil && change.ClearAuth) || (change.Tools != nil && change.ClearTools) {
		return nil, fmt.Errorf("aiplex: conflicting tool connection change")
	}
	spec, err := applyToolConnectionChange(record.Spec, change)
	if err != nil {
		return nil, err
	}
	saved, err := c.PutResource(ctx, ToolConnections, id, json.RawMessage(spec), record.Revision)
	if err != nil {
		return nil, err
	}
	return decodeToolConnection(saved)
}

func applyToolConnectionChange(raw json.RawMessage, change ToolConnectionChange) ([]byte, error) {
	spec := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("aiplex: tool connection specification is not an object: %w", err)
	}
	execution := map[string]json.RawMessage{}
	if rawExec, ok := spec["execution"]; ok && string(rawExec) != "null" {
		if err := json.Unmarshal(rawExec, &execution); err != nil {
			return nil, fmt.Errorf("aiplex: tool connection execution is not an object: %w", err)
		}
	}
	hasExecution := len(execution) > 0
	set := func(target map[string]json.RawMessage, key string, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		target[key] = encoded
		return nil
	}
	if change.ClearAuth {
		var mode string
		if rawMode, ok := execution["credential_mode"]; ok {
			_ = json.Unmarshal(rawMode, &mode)
		}
		if mode == ToolCredentialConnectionAuth {
			return nil, fmt.Errorf("aiplex: connection uses connection-auth; set another auth type instead of clearing it")
		}
		delete(spec, "auth")
	}
	if change.Auth != nil {
		if change.Auth.Type == "" {
			return nil, fmt.Errorf("aiplex: auth type is required")
		}
		if change.Auth.Type != ToolAuthOAuthUser && !hasExecution {
			return nil, fmt.Errorf("aiplex: auth type %s requires a connection with managed execution", change.Auth.Type)
		}
		if err := set(spec, "auth", change.Auth); err != nil {
			return nil, err
		}
		if hasExecution {
			mode := ToolCredentialConnectionAuth
			if change.Auth.Type == ToolAuthOAuthUser {
				mode = ToolCredentialDelegatedOAuth
			}
			if err := set(execution, "credential_mode", mode); err != nil {
				return nil, err
			}
			if err := set(execution, "identity_ref", ""); err != nil {
				return nil, err
			}
			if err := set(spec, "execution", execution); err != nil {
				return nil, err
			}
		}
	}
	if change.ClearTools {
		delete(spec, "tools")
	}
	if change.Tools != nil {
		if !hasExecution {
			return nil, fmt.Errorf("aiplex: a tools policy requires a connection with managed execution")
		}
		if err := set(spec, "tools", change.Tools); err != nil {
			return nil, err
		}
	}
	return json.Marshal(spec)
}
