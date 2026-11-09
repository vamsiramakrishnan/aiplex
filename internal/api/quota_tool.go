package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

type quotaToolConfiguration struct {
	Connection     ToolConnection        `json:"connection"`
	Revision       uint64                `json:"revision"`
	Identity       connections.Identity  `json:"identity"`
	Network        endpointpolicy.Config `json:"network"`
	NetworkProfile *resources.Record     `json:"network_profile,omitempty"`
	OAuthGrantID   string                `json:"oauth_grant_id,omitempty"`
}

func (h *ConnectedHandler) toolConfiguration(ctx context.Context, tenant string, record resources.Record, connection ToolConnection) (quotaToolConfiguration, error) {
	if h.Runtime == nil || h.Runtime.Policy == nil || connection.Execution == nil || connection.ID != record.ID || connection.ID == "" || record.TenantID != tenant || record.Kind != "tool-connection" {
		return quotaToolConfiguration{}, fmt.Errorf("tool execution configuration is unavailable")
	}
	config := *connection.Execution
	if config.ID != connection.ID || config.Endpoint != connection.ResourceURL {
		return quotaToolConfiguration{}, fmt.Errorf("execution identity or endpoint does not bind the saved connection")
	}
	if err := config.Validate(); err != nil {
		return quotaToolConfiguration{}, err
	}
	if err := h.Runtime.Policy.ValidateURL(config.Endpoint); err != nil {
		return quotaToolConfiguration{}, err
	}
	result := quotaToolConfiguration{Connection: connection, Revision: record.Revision, Network: h.Runtime.Config.Network}
	if config.CredentialMode == toolexec.DelegatedOAuth {
		key, err := h.toolSessionKey(ctx, tenant, record, connection)
		if err != nil || h.ToolSessions == nil {
			return quotaToolConfiguration{}, fmt.Errorf("delegated tool authorization unavailable")
		}
		session, err := h.ToolSessions.Info(ctx, key)
		if err != nil {
			return quotaToolConfiguration{}, err
		}
		for _, required := range connection.Client.Scopes {
			found := false
			for _, granted := range session.Scopes {
				if granted == required {
					found = true
				}
			}
			if !found {
				return quotaToolConfiguration{}, fmt.Errorf("delegated tool grant has insufficient scopes")
			}
		}
		result.OAuthGrantID = session.GrantID
	} else {
		if err := h.Runtime.CheckDestination(tenant, config.IdentityRef, config.Endpoint); err != nil {
			return quotaToolConfiguration{}, err
		}
		identity, err := h.Runtime.Identity(tenant, config.IdentityRef)
		if err != nil {
			return quotaToolConfiguration{}, err
		}
		result.Identity = identity
	}
	if connection.NetworkProfileRef != "" {
		profile, err := h.Store.Get(ctx, tenant, "network-profile", connection.NetworkProfileRef)
		if err != nil {
			return quotaToolConfiguration{}, err
		}
		if profile.TenantID != tenant || profile.Kind != "network-profile" || profile.ID != connection.NetworkProfileRef {
			return quotaToolConfiguration{}, fmt.Errorf("network profile namespace mismatch")
		}
		result.NetworkProfile = &profile
	}
	return result, nil
}

// NativeTool is an explicit managed execution path, distinct from registration.
// It supports only operator-declared stateless JSON HTTP tools/call connections.
func (h *ConnectedHandler) NativeTool(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	id := idParam(r)
	w, usage := h.beginUsage(w, r, tenant, usageledger.PlaneMCP, id)
	defer usage.finish()
	record, err := h.Store.Get(r.Context(), tenant, "tool-connection", id)
	if err != nil {
		resourceError(w, r, err)
		return
	}
	var connection ToolConnection
	if json.Unmarshal(record.Spec, &connection) != nil || connection.ID != id {
		Error(w, r, 503, "TOOL_CONFIGURATION_UNAVAILABLE", "saved tool connection is invalid")
		return
	}
	configuration, err := h.toolConfiguration(r.Context(), tenant, record, connection)
	if err != nil {
		Error(w, r, 503, "TOOL_CONFIGURATION_UNAVAILABLE", "tool execution prerequisites are unavailable")
		return
	}
	config := *connection.Execution
	client := h.Runtime.Streaming
	if client == nil {
		client = h.Runtime.HTTP
	}
	requestedTool := ""
	opts := toolexec.Options{HTTPClient: client, TokenSource: toolexec.TokenSourceFunc(func(ctx context.Context) (string, error) { return h.Runtime.Token(ctx, tenant, config.IdentityRef) }), Context: func(req *http.Request) (contracts.RequestContext, error) { return h.trusted(req, id) }, Authorize: func(ctx context.Context, _ contracts.RequestContext, _ toolexec.Connection, request toolexec.Request) error {
		requestedTool = request.Name
		return h.authorizeCurrent(ctx, r, "mcp:tools:"+request.Name)
	}, AllowHTTPForTests: h.Development}
	opts.BeforeDispatch = func(ctx context.Context) error {
		if err := h.authorizeCurrent(ctx, r, "mcp:tools:"+requestedTool); err != nil {
			return &execution.StatusError{Status: 403, Code: "TOOL_SCOPE_REVOKED", Message: "current tool grant required"}
		}
		return nil
	}
	if config.CredentialMode == toolexec.DelegatedOAuth {
		key, err := h.toolSessionKey(r.Context(), tenant, record, connection)
		if err != nil {
			Error(w, r, 401, "TOOL_IDENTITY_REQUIRED", "verified tool owner required")
			return
		}
		opts.TokenSource = toolexec.TokenSourceFunc(func(ctx context.Context) (string, error) {
			return h.ToolSessions.AccessToken(ctx, key, configuration.OAuthGrantID)
		})
		currentGrant := opts.BeforeDispatch
		opts.BeforeDispatch = func(ctx context.Context) error {
			if err := currentGrant(ctx); err != nil {
				return err
			}
			if err := h.ToolSessions.CheckGrant(ctx, key, configuration.OAuthGrantID); err != nil {
				return &execution.StatusError{Status: 409, Code: "TOOL_AUTHORIZATION_CHANGED", Message: "tool authorization grant changed"}
			}
			return nil
		}
	}
	if h.Quota != nil {
		opts.Prepare = h.ToolExecutionPreparer(r, record, connection)
	}
	usage.instrumentTool(&opts)
	gateway, err := toolexec.NewGateway(config, opts)
	if err != nil {
		Error(w, r, 503, "TOOL_EXECUTION_UNAVAILABLE", "managed tool execution is unavailable")
		return
	}
	gateway.ServeHTTP(w, r)
}

func (h *ConnectedHandler) toolSessionKey(ctx context.Context, tenant string, record resources.Record, connection ToolConnection) (toolauth.SessionKey, error) {
	identity := GetWIFIdentity(ctx)
	if identity == nil || identity.PrincipalID == "" || connection.Issuer == "" || record.TenantID != tenant || record.ID != connection.ID || record.Revision == 0 {
		return toolauth.SessionKey{}, fmt.Errorf("verified delegated owner and exact tool issuer required")
	}
	return toolauth.SessionKey{Owner: toolauth.Owner{TenantID: tenant, PrincipalID: identity.PrincipalID, ActorID: identity.ActorID}, ConnectionID: connection.ID, ConnectionRevision: record.Revision, Resource: connection.ResourceURL, Issuer: connection.Issuer, ProfileID: connection.Client.ID}, nil
}

func (h *ConnectedHandler) ToolExecutionPreparer(r *http.Request, record resources.Record, connection ToolConnection) func(context.Context, toolexec.Invocation) (execution.Lease, error) {
	return func(ctx context.Context, in toolexec.Invocation) (execution.Lease, error) {
		if h.Quota == nil {
			return nil, &execution.StatusError{Status: 503, Code: "QUOTA_UNAVAILABLE", Message: "quota service unavailable"}
		}
		configuration, err := h.toolConfiguration(ctx, in.Context.TenantID, record, connection)
		if err != nil {
			return nil, &execution.StatusError{Status: 503, Code: "TOOL_CONFIGURATION_UNAVAILABLE", Message: "tool execution prerequisites are unavailable"}
		}
		original, err := json.Marshal(configuration)
		if err != nil {
			return nil, err
		}
		endpoint, _ := url.Parse(connection.Execution.Endpoint)
		return h.Quota.Prepare(ctx, r, QuotaInvocation{Context: in.Context, Plane: "mcplex", ResourceID: connection.ID, OperationID: "tools/call:" + in.Request.Name, Target: quotaplex.Target{Provider: "mcp"}, Method: http.MethodPost, Path: endpoint.EscapedPath(), RawQuery: endpoint.RawQuery, Body: in.Request.Body, Headers: in.Headers, Configuration: configuration, Reauthorize: func(recheck context.Context) error {
			current, err := h.Store.Get(recheck, in.Context.TenantID, "tool-connection", connection.ID)
			if err != nil {
				return &execution.StatusError{Status: 409, Code: "TOOL_CONFIGURATION_CHANGED", Message: "tool configuration changed"}
			}
			var currentConnection ToolConnection
			if json.Unmarshal(current.Spec, &currentConnection) != nil {
				return &execution.StatusError{Status: 409, Code: "TOOL_CONFIGURATION_CHANGED", Message: "tool configuration changed"}
			}
			currentConfiguration, err := h.toolConfiguration(recheck, in.Context.TenantID, current, currentConnection)
			if err != nil {
				return &execution.StatusError{Status: 409, Code: "TOOL_CONFIGURATION_CHANGED", Message: "tool execution prerequisites changed"}
			}
			now, err := json.Marshal(currentConfiguration)
			if err != nil || !bytes.Equal(original, now) {
				return &execution.StatusError{Status: 409, Code: "TOOL_CONFIGURATION_CHANGED", Message: "tool configuration changed"}
			}
			return h.authorizeCurrent(recheck, r, "mcp:tools:"+in.Request.Name)
		}})
	}
}
