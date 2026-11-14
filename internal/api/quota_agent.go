package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/governance"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type quotaAgentConfiguration struct {
	Record            resources.Record
	Connection        cloudagents.Connection
	Identity          connections.Identity
	Network           endpointpolicy.Config
	NetworkProfile    *resources.Record
	GovernanceBinding *resources.Record
}

func (h *ConnectedHandler) quotaAgentConfiguration(ctx context.Context, tenant string, record resources.Record, c cloudagents.Connection) (quotaAgentConfiguration, error) {
	if h.Runtime == nil || h.Runtime.Policy == nil || h.Store == nil || record.TenantID != tenant || record.Kind != "agent-connection" || record.ID != c.ID || record.Revision == 0 || c.TenantID != tenant || c.ValidateWith(h.allowLoopback()) != nil {
		return quotaAgentConfiguration{}, fmt.Errorf("native agent configuration is unavailable")
	}
	if e := h.Runtime.Policy.ValidateURL(c.Endpoint); e != nil {
		return quotaAgentConfiguration{}, e
	}
	if e := h.Runtime.CheckDestination(tenant, c.IdentityRef, c.Endpoint); e != nil {
		return quotaAgentConfiguration{}, e
	}
	identity, e := h.Runtime.Identity(tenant, c.IdentityRef)
	if e != nil {
		return quotaAgentConfiguration{}, e
	}
	if e = h.checkNetworkRef(ctx, tenant, c.NetworkProfileRef); e != nil {
		return quotaAgentConfiguration{}, e
	}
	if e = h.checkAgentGovernanceRef(ctx, tenant, c); e != nil {
		return quotaAgentConfiguration{}, e
	}
	result := quotaAgentConfiguration{Record: record, Connection: c, Identity: identity, Network: h.Runtime.Config.Network}
	if c.NetworkProfileRef != "" {
		profile, e := h.Store.Get(ctx, tenant, "network-profile", c.NetworkProfileRef)
		if e != nil {
			return quotaAgentConfiguration{}, e
		}
		result.NetworkProfile = &profile
	}
	if c.GovernanceBindingRef != "" {
		binding, e := h.Store.Get(ctx, tenant, governance.BindingKind, c.GovernanceBindingRef)
		if e != nil {
			return quotaAgentConfiguration{}, e
		}
		result.GovernanceBinding = &binding
	}
	return result, nil
}

// AgentExecutionPreparer meters one native HTTP exchange. It never derives
// opaque internal model/tool usage or marks the provider's internal job finished.
func (h *ConnectedHandler) AgentExecutionPreparer(w http.ResponseWriter, r *http.Request, record resources.Record, c cloudagents.Connection) cloudagents.ExecutionPreparer {
	if h.Quota == nil {
		return nil
	}
	return cloudagents.ExecutionPreparerFunc(func(ctx context.Context, in cloudagents.ExecutionRequest) (execution.Lease, error) {
		if in.Context.TenantID != record.TenantID || in.Context.ResourceID != record.ID || in.Connection != c {
			return nil, quotaStatus(403, "QUOTA_AGENT_CONTEXT_CHANGED", "native agent context changed")
		}
		configuration, e := h.quotaAgentConfiguration(ctx, in.Context.TenantID, record, c)
		if e != nil {
			return nil, quotaStatus(503, "QUOTA_AGENT_CONFIGURATION_UNAVAILABLE", "native agent configuration is unavailable")
		}
		digest, e := quotaDigest(configuration)
		if e != nil {
			return nil, e
		}
		headers := http.Header{}
		for _, key := range []string{"Idempotency-Key", "X-Aiplex-Funding-Account", "X-Aiplex-Operation-Id"} {
			if values := r.Header.Values(key); len(values) > 0 {
				headers[key] = append([]string(nil), values...)
			}
		}
		target := quotaplex.Target{Provider: string(c.Provider), Region: c.Region}
		if c.Provider == cloudagents.GoogleRuntime {
			parts := strings.Split(c.ResourceID, "/")
			if len(parts) >= 4 {
				target.BillingAccount = parts[1]
				target.Region = parts[3]
			}
		}
		if c.Provider == cloudagents.AgentCoreRuntime {
			parts := strings.Split(c.ResourceID, ":")
			if len(parts) >= 6 {
				target.BillingAccount = parts[4]
			}
		}
		if c.Provider == cloudagents.BedrockAgent && c.GovernanceBindingRef != "" {
			if account, _, e := c.GovernanceAWSAccount(); e == nil {
				target.BillingAccount = account
			}
		}
		// Include logical invocation selectors in addition to the actual native body;
		// they may affect provider sessions, media types, versions and continuation.
		body, e := json.Marshal(struct {
			Invocation cloudagents.Invocation
			Native     []byte
			Headers    http.Header
		}{in.Invocation, in.Body, in.Headers})
		if e != nil {
			return nil, e
		}
		reauthorize := func(call context.Context) error {
			current, e := h.Store.Get(call, in.Context.TenantID, "agent-connection", record.ID)
			if e != nil || current.Revision != record.Revision {
				return quotaStatus(409, "QUOTA_AGENT_CONFIGURATION_CHANGED", "native agent configuration changed")
			}
			var latest cloudagents.Connection
			if json.Unmarshal(current.Spec, &latest) != nil {
				return quotaStatus(409, "QUOTA_AGENT_CONFIGURATION_CHANGED", "native agent configuration changed")
			}
			fresh, e := h.quotaAgentConfiguration(call, in.Context.TenantID, current, latest)
			if e != nil {
				return quotaStatus(409, "QUOTA_AGENT_CONFIGURATION_CHANGED", "native agent configuration changed")
			}
			now, e := quotaDigest(fresh)
			if e != nil || now != digest {
				return quotaStatus(409, "QUOTA_AGENT_CONFIGURATION_CHANGED", "native agent authority changed")
			}
			if e = h.authorizeCurrent(call, r, "a2a:agent:"+record.ID+":"+in.Invocation.Operation); e != nil {
				return quotaStatus(403, "QUOTA_AGENT_GRANT_REVOKED", "current native agent grant required")
			}
			return nil
		}
		lease, e := h.Quota.Prepare(ctx, r, QuotaInvocation{Context: in.Context, Plane: "a2aplex", ResourceID: record.ID, OperationID: in.Invocation.Operation, Target: target, Method: in.Method, Path: in.Path, RawQuery: in.RawQuery, Body: body, Headers: headers, Configuration: configuration, Reauthorize: reauthorize})
		if e == nil {
			if receipt, ok := lease.(execution.Receipt); ok {
				w.Header().Set(execution.AttemptHeader, receipt.AttemptID())
			}
		}
		return lease, e
	})
}
