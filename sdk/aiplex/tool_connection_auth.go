package aiplex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	// Rules are policy expressions evaluated after the lists, the caller's
	// scope and read_only. See ToolRule.
	Rules []ToolRule `json:"rules,omitempty"`
}

// ToolRule is one policy rule on tool calls. When is a CEL boolean expression
// over tool, args, caller, access_point and request; Effect is "deny" or
// "allow". Message is shown to the caller when the rule refuses a call and
// must be safe to publish.
type ToolRule struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	When        string `json:"when"`
	Effect      string `json:"effect"`
	Message     string `json:"message,omitempty"`
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
	// Provenance is set on connections imported from an MCP Registry.
	Provenance *ToolProvenance `json:"provenance,omitempty"`
}

// ToolProvenance records the MCP Registry entry a connection was imported
// from. It is informational: nothing in authorization or dispatch reads it.
type ToolProvenance struct {
	Registry    string `json:"registry"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	RetrievedAt string `json:"retrieved_at"`
	SHA256      string `json:"sha256"`
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

// ConnectionTool is one tool a connection's server listed through the gateway.
type ConnectionTool struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Annotations *struct {
		ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
	} `json:"annotations,omitempty"`
}

// ToolListError is a JSON-RPC error the tool server (or the gateway on its
// behalf) answered a tools/list with.
type ToolListError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ToolListError) Error() string {
	return fmt.Sprintf("tools/list failed (%d): %s", e.Code, e.Message)
}

// ListConnectionTools performs a real tools/list against the connection's
// server through the gateway (POST /api/v1/mcp/connections/{id}/invoke), with
// the connection's configured credential and tools policy applied. Only the
// tools the policy lets callers see are returned.
func (c *Client) ListConnectionTools(ctx context.Context, id string) ([]ConnectionTool, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	var all []ConnectionTool
	cursor := ""
	for page := 0; page < 20; page++ {
		request := map[string]any{"jsonrpc": "2.0", "id": page + 1, "method": "tools/list"}
		if cursor != "" {
			request["params"] = map[string]any{"cursor": cursor}
		}
		var out struct {
			Result *struct {
				Tools      []ConnectionTool `json:"tools"`
				NextCursor string           `json:"nextCursor"`
			} `json:"result"`
			Error *ToolListError `json:"error"`
		}
		if err := c.do(ctx, http.MethodPost, "/api/v1/mcp/connections/"+url.PathEscape(id)+"/invoke", request, &out); err != nil {
			return nil, err
		}
		if out.Error != nil {
			return nil, out.Error
		}
		if out.Result == nil {
			return nil, fmt.Errorf("aiplex: tools/list returned no result")
		}
		all = append(all, out.Result.Tools...)
		if out.Result.NextCursor == "" {
			break
		}
		cursor = out.Result.NextCursor
	}
	return all, nil
}

// ValidateToolConnectionID reports whether id is safe to put in a connection
// route. The methods that take a connection ID check it the same way before
// sending anything; callers that take input from a person can use it to
// refuse bad input as a usage problem.
func ValidateToolConnectionID(id string) error { return validateResourceID(id) }

// ToolContent is one content item of a tool result. Binary payloads (such as
// image data) are not decoded here; read them from ToolCallResult.Raw.
type ToolContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ToolCallResult is the result of one tools/call. Raw is the exact JSON-RPC
// "result" object the server answered with.
type ToolCallResult struct {
	Content []ToolContent   `json:"content"`
	IsError bool            `json:"isError,omitempty"`
	Raw     json.RawMessage `json:"-"`
}

// ToolCallError is a JSON-RPC error answered to a tools/call, by the gateway
// (a policy denial, for example) or by the tool server. StatusCode is the HTTP
// status it came with. Reason and Rule are the gateway's data.reason and
// data.rule when it gave them.
type ToolCallError struct {
	StatusCode int
	Code       int
	Message    string
	Reason     string
	Rule       string
}

func (e *ToolCallError) Error() string {
	text := fmt.Sprintf("tools/call failed (%d): %s", e.Code, e.Message)
	if e.Reason != "" {
		text += " [" + e.Reason
		if e.Rule != "" {
			text += ", rule " + e.Rule
		}
		text += "]"
	}
	return text
}

// CallConnectionTool sends one tools/call through the gateway
// (POST /api/v1/mcp/connections/{id}/invoke), with the connection's configured
// credential and policy applied and the caller's mcp:tools:{name} grant
// checked. arguments must be a JSON object, or empty for none. A JSON-RPC
// error, including a policy denial, is returned as *ToolCallError; any other
// refusal is an *Error. A result with IsError set is returned as a result: the
// tool ran and reported a failure.
func (c *Client) CallConnectionTool(ctx context.Context, id, tool string, arguments json.RawMessage) (*ToolCallResult, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	if tool == "" {
		return nil, fmt.Errorf("aiplex: tool name is required")
	}
	args := json.RawMessage(`{}`)
	if len(bytes.TrimSpace(arguments)) > 0 {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(arguments, &object); err != nil || object == nil {
			return nil, fmt.Errorf("aiplex: tool arguments must be a JSON object")
		}
		args = arguments
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		return nil, fmt.Errorf("aiplex: marshal request: %w", err)
	}
	path := "/api/v1/mcp/connections/" + url.PathEscape(id) + "/invoke"
	if err = validateAPIPath(path); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("aiplex: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aiplex: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil {
		return nil, fmt.Errorf("aiplex: read response: %w", err)
	}
	if len(raw) > 8<<20 {
		return nil, fmt.Errorf("aiplex: tools/call response exceeds 8 MiB")
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Reason string `json:"reason"`
				Rule   string `json:"rule"`
			} `json:"data"`
		} `json:"error"`
	}
	decodeErr := json.Unmarshal(raw, &envelope)
	if envelope.Error != nil {
		return nil, &ToolCallError{StatusCode: resp.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message, Reason: envelope.Error.Data.Reason, Rule: envelope.Error.Data.Rule}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var apiErr Error
		if decodeErr != nil || json.Unmarshal(raw, &apiErr) != nil {
			return nil, &Error{Code: "unknown", Message: resp.Status, StatusCode: resp.StatusCode}
		}
		apiErr.StatusCode = resp.StatusCode
		return nil, &apiErr
	}
	if decodeErr != nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil, fmt.Errorf("aiplex: tools/call returned no result")
	}
	out := &ToolCallResult{Raw: envelope.Result}
	if err = json.Unmarshal(envelope.Result, out); err != nil {
		return nil, fmt.Errorf("aiplex: decode tools/call result: %w", err)
	}
	return out, nil
}
