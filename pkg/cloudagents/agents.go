// Package cloudagents governs native agent invocation at explicit provider
// control points. It does not claim to intercept a managed agent's internal calls.
package cloudagents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"errors"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

type Provider string

const (
	GoogleRuntime    Provider = "google-agent-runtime"
	BedrockAgent     Provider = "aws-bedrock-agent"
	AgentCoreRuntime Provider = "aws-agentcore-runtime"
	FoundryAgent     Provider = "azure-foundry-agent"
	CopilotStudio    Provider = "copilot-studio"
)

type Connection struct {
	ID                   string   `json:"id"`
	TenantID             string   `json:"tenant_id"`
	Provider             Provider `json:"provider"`
	Endpoint             string   `json:"endpoint"`
	ResourceID           string   `json:"resource_id"`
	IdentityRef          string   `json:"identity_ref"`
	NetworkProfileRef    string   `json:"network_profile_ref,omitempty"`
	GovernanceBindingRef string   `json:"governance_binding_ref,omitempty"`
	Region               string   `json:"region,omitempty"`
	AgentID              string   `json:"agent_id,omitempty"`
	AliasID              string   `json:"alias_id,omitempty"`
	Project              string   `json:"project,omitempty"`
	APIVersion           string   `json:"api_version,omitempty"`
	GovernanceMode       string   `json:"governance_mode"`
}

var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var googleResource = regexp.MustCompile(`^projects/[A-Za-z0-9_-]+/locations/[a-z0-9-]+/reasoningEngines/[A-Za-z0-9_-]+$`)
var runtimeARN = regexp.MustCompile(`^arn:(aws|aws-us-gov|aws-cn):bedrock-agentcore:[a-z0-9-]+:[0-9]{12}:runtime/[A-Za-z0-9_-]+$`)

func (c Connection) Validate() error {
	if !segment.MatchString(c.ID) || c.TenantID == "" || c.IdentityRef == "" || c.ResourceID == "" {
		return fmt.Errorf("connection id, tenant, resource and identity reference required")
	}
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Scheme != "https" {
		return fmt.Errorf("agent endpoint must be a pinned HTTPS origin")
	}
	if c.GovernanceMode != "invocation" {
		return fmt.Errorf("native adapter governs invocation only; egress enforcement needs a separately configured gateway/network control point")
	}
	switch c.Provider {
	case GoogleRuntime:
		if !googleResource.MatchString(c.ResourceID) {
			return fmt.Errorf("Google runtime requires canonical reasoningEngine resource")
		}
	case BedrockAgent:
		if !segment.MatchString(c.AgentID) || !segment.MatchString(c.AliasID) || !segment.MatchString(c.Region) {
			return fmt.Errorf("Bedrock requires region, agent and alias")
		}
	case AgentCoreRuntime:
		if !runtimeARN.MatchString(c.ResourceID) || !segment.MatchString(c.Region) {
			return fmt.Errorf("AgentCore requires runtime ARN and region")
		}
		if strings.Split(c.ResourceID, ":")[3] != c.Region {
			return fmt.Errorf("AgentCore ARN region mismatch")
		}
	case FoundryAgent:
		if !segment.MatchString(c.Project) || c.APIVersion == "" {
			return fmt.Errorf("Foundry requires project and explicit API version")
		}
		if c.AgentID != "" && !segment.MatchString(c.AgentID) {
			return fmt.Errorf("invalid Foundry agent id")
		}
	case CopilotStudio:
	default:
		return fmt.Errorf("unsupported native agent provider %q", c.Provider)
	}
	return nil
}

type Action struct {
	Context                                              contracts.RequestContext
	ConnectionID, ProviderResource, Operation, SessionID string
}
type Authorizer interface {
	Authorize(context.Context, Action) error
}
type AuthorizeFunc func(context.Context, Action) error

func (f AuthorizeFunc) Authorize(ctx context.Context, a Action) error { return f(ctx, a) }

type Authenticator interface {
	Authenticate(context.Context, *http.Request) error
}
type Invocation struct {
	Operation   string          `json:"operation"`
	SessionID   string          `json:"session_id,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ContentType string          `json:"content_type,omitempty"`
	Accept      string          `json:"accept,omitempty"`
	Qualifier   string          `json:"qualifier,omitempty"`
	Watermark   string          `json:"watermark,omitempty"`
}
type Invoker struct {
	connection Connection
	authorize  Authorizer
	client     *http.Client
	auth       Authenticator
	sessions   SessionStore
	execution  ExecutionPreparer
}

func NewInvoker(c Connection, a Authorizer, client *http.Client, credentials Authenticator, options ...Option) (*Invoker, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if isNil(a) || client == nil || isNil(credentials) {
		return nil, fmt.Errorf("authorizer, policy-constrained HTTP client and credential adapter are required")
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	v := &Invoker{connection: c, authorize: a, client: &copied, auth: credentials}
	for _, option := range options {
		if option != nil {
			option(v)
		}
	}
	if v.execution != nil && isNil(v.execution) {
		return nil, fmt.Errorf("typed-nil managed execution preparer is invalid")
	}
	if c.Provider == CopilotStudio && isNil(v.sessions) {
		return nil, fmt.Errorf("DirectLine session ownership store required")
	}
	if bound, ok := credentials.(interface{ ValidateConnection(Connection) error }); ok {
		if err := bound.ValidateConnection(c); err != nil {
			return nil, err
		}
	} else if c.Provider == BedrockAgent {
		return nil, fmt.Errorf("Bedrock classic requires connection-bound SigV4 authentication")
	}
	return v, nil
}

// Invoke authorizes the exact connection/resource/session before credentials or
// network activity. The returned native response body is never buffered or
// flattened; the caller owns closing it, including AWS event streams and SSE.
func (v *Invoker) Invoke(ctx context.Context, request contracts.RequestContext, in Invocation) (result *http.Response, resultErr error) {
	in.Payload = append(json.RawMessage(nil), in.Payload...)
	request = cloneExecutionContext(request)
	if e := request.Validate(); e != nil {
		return nil, e
	}
	if request.TenantID != v.connection.TenantID || request.ResourceID != v.connection.ID {
		return nil, fmt.Errorf("agent connection tenant/resource mismatch")
	}
	sessionOwner := request.PrincipalID
	if v.execution != nil {
		sessionOwner = agentSessionPrincipal(request)
	}
	logicalSession := in.SessionID
	if v.connection.Provider == BedrockAgent || v.connection.Provider == AgentCoreRuntime {
		if logicalSession == "" {
			return nil, fmt.Errorf("logical session id required")
		}
		in.SessionID = scopedSession(request.TenantID, sessionOwner, v.connection.ID, logicalSession)
	}
	req, e := v.build(ctx, in)
	if e != nil {
		return nil, e
	}
	if e = v.authorize.Authorize(ctx, Action{cloneExecutionContext(request), v.connection.ID, v.connection.ResourceID, in.Operation, logicalSession}); e != nil {
		return nil, e
	}
	if v.connection.Provider == CopilotStudio && in.Operation != "start-conversation" {
		if e = v.sessions.Check(ctx, request.TenantID, sessionOwner, v.connection.ID, in.SessionID); e != nil {
			return nil, e
		}
	}

	var run *agentExecution
	if v.execution != nil {
		body := append([]byte(nil), in.Payload...)
		if req.GetBody != nil {
			native, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			body, err = io.ReadAll(native)
			native.Close()
			if err != nil {
				return nil, err
			}
		} else if req.Body == nil {
			body = nil
		}
		snapshot := ExecutionRequest{Context: cloneExecutionContext(request), Connection: v.connection, Invocation: in, Method: req.Method, Path: req.URL.EscapedPath(), RawQuery: req.URL.RawQuery, Body: body, Headers: req.Header.Clone()}
		snapshot.Invocation.Payload = append(json.RawMessage(nil), in.Payload...)
		lease, err := v.execution.Prepare(ctx, snapshot)
		if err != nil {
			return nil, err
		}
		if isNil(lease) {
			return nil, &execution.StatusError{Status: 503, Code: "AGENT_EXECUTION_UNAVAILABLE", Message: "managed agent execution lease unavailable"}
		}
		run = &agentExecution{ctx: ctx, lease: lease}
		defer func() {
			if resultErr != nil {
				resultErr = errors.Join(resultErr, run.finish(false, resultErr))
			}
		}()
	}
	if e = v.auth.Authenticate(ctx, req); e != nil {
		return nil, fmt.Errorf("agent credentials unavailable: %w", e)
	}

	if run != nil {
		if e = v.authorize.Authorize(ctx, Action{cloneExecutionContext(request), v.connection.ID, v.connection.ResourceID, in.Operation, logicalSession}); e != nil {
			return nil, e
		}
		if v.connection.Provider == CopilotStudio && in.Operation != "start-conversation" {
			if e = v.sessions.Check(ctx, request.TenantID, sessionOwner, v.connection.ID, in.SessionID); e != nil {
				return nil, e
			}
		}
		if e = run.lease.BeforeDispatch(ctx); e != nil {
			return nil, e
		}
		run.claimed = true
	}
	response, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	if run != nil {
		run.status = response.StatusCode
	}
	if v.connection.Provider == CopilotStudio && in.Operation == "start-conversation" && response.StatusCode >= 200 && response.StatusCode < 300 {
		data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > 1<<20 {
			return nil, fmt.Errorf("DirectLine conversation response exceeds 1MiB")
		}
		var conversation struct {
			ID string `json:"conversationId"`
		}
		if json.Unmarshal(data, &conversation) != nil || !segment.MatchString(conversation.ID) {
			return nil, fmt.Errorf("invalid DirectLine conversation response")
		}
		if err = run.resource(conversation.ID); err != nil {
			return nil, err
		}
		if err = v.sessions.Bind(ctx, request.TenantID, sessionOwner, v.connection.ID, conversation.ID); err != nil {
			return nil, err
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
	}

	if run != nil {
		response.Body = &acknowledgedAgentBody{source: response.Body, run: run, expected: response.ContentLength}
		if response.Header == nil {
			response.Header = http.Header{}
		}
		if receipt, ok := run.lease.(execution.Receipt); ok {
			if id := receipt.AttemptID(); id != "" && len(id) <= 1024 && !strings.ContainsAny(id, "\r\n") {
				response.Header.Set(execution.AttemptHeader, id)
			}
		}
	}
	return response, nil
}
func (v *Invoker) build(ctx context.Context, in Invocation) (*http.Request, error) {
	c := v.connection
	if c.Provider == FoundryAgent && c.AgentID != "" && in.Operation != "agent-responses" {
		return nil, fmt.Errorf("connection is bound to a named Foundry agent")
	}
	if c.Provider == FoundryAgent {
		var body map[string]json.RawMessage
		if len(in.Payload) > 0 && json.Unmarshal(in.Payload, &body) != nil {
			return nil, fmt.Errorf("Foundry payload must be a JSON object")
		}
		for _, key := range []string{"agent", "agent_id", "agent_reference"} {
			if _, ok := body[key]; ok {
				return nil, fmt.Errorf("Foundry agent selector is owned by the connection")
			}
		}
		if c.AgentID != "" {
			if _, ok := body["model"]; ok {
				return nil, fmt.Errorf("named Foundry agent controls model selection")
			}
		}
	}
	method := http.MethodPost
	path := ""
	query := url.Values{}
	payload := in.Payload
	if len(payload) > 100_000_000 {
		return nil, fmt.Errorf("native agent payload exceeds 100MB")
	}
	// NewRequest escapes path segments exactly once. No native request may choose
	// its own destination, identity, resource ARN or credential headers.
	switch c.Provider {
	case GoogleRuntime:
		suffix := ""
		switch in.Operation {
		case "query":
			suffix = ":query"
		case "stream-query":
			suffix = ":streamQuery"
		default:
			return nil, fmt.Errorf("unsupported Google agent operation")
		}
		path = "/v1/" + c.ResourceID + suffix
	case BedrockAgent:
		if in.Operation != "invoke" || !segment.MatchString(in.SessionID) {
			return nil, fmt.Errorf("Bedrock invoke requires a valid session")
		}
		path = "/agents/" + url.PathEscape(c.AgentID) + "/agentAliases/" + url.PathEscape(c.AliasID) + "/sessions/" + url.PathEscape(in.SessionID) + "/text"
	case AgentCoreRuntime:
		if in.Operation != "invoke" || len(in.SessionID) < 33 || len(in.SessionID) > 256 || strings.ContainsAny(in.SessionID, "\r\n") {
			return nil, fmt.Errorf("AgentCore invoke requires a 33..256 character session")
		}
		if in.Qualifier != "" && !segment.MatchString(in.Qualifier) {
			return nil, fmt.Errorf("invalid AgentCore qualifier")
		}
		path = "/runtimes/" + url.PathEscape(c.ResourceID) + "/invocations"
		if in.Qualifier != "" {
			query.Set("qualifier", in.Qualifier)
		}
	case FoundryAgent:
		switch in.Operation {
		case "responses":
			path = "/api/projects/" + url.PathEscape(c.Project) + "/openai/v1/responses"
		case "agent-responses":
			if c.AgentID == "" {
				return nil, fmt.Errorf("Foundry agent id required")
			}
			path = "/api/projects/" + url.PathEscape(c.Project) + "/agents/" + url.PathEscape(c.AgentID) + "/endpoint/protocols/openai/responses"
			query.Set("api-version", c.APIVersion)
		default:
			return nil, fmt.Errorf("unsupported Foundry agent operation")
		}
	case CopilotStudio:
		path = "/v3/directline/conversations"
		switch in.Operation {
		case "start-conversation":
		case "send-activity", "get-activities":
			if !segment.MatchString(in.SessionID) {
				return nil, fmt.Errorf("DirectLine conversation id required")
			}
			path += "/" + url.PathEscape(in.SessionID) + "/activities"
			if in.Operation == "get-activities" {
				method = http.MethodGet
				payload = nil
				if in.Watermark != "" {
					query.Set("watermark", in.Watermark)
				}
			}
		default:
			return nil, fmt.Errorf("unsupported Copilot Studio operation")
		}
	}
	endpoint := strings.TrimSuffix(c.Endpoint, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if e != nil {
		return nil, e
	}
	content := in.ContentType
	if content == "" {
		content = "application/json"
	}
	accept := in.Accept
	if accept == "" {
		accept = "application/json"
	}
	if strings.ContainsAny(content+accept, "\r\n") {
		return nil, fmt.Errorf("invalid native media type")
	}
	req.Header.Set("Content-Type", content)
	req.Header.Set("Accept", accept)
	if c.Provider == AgentCoreRuntime {
		req.Header.Set("X-Amzn-Bedrock-AgentCore-Runtime-Session-Id", in.SessionID)
	}
	return req, nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
