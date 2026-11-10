package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type quotaModelConfiguration struct {
	Record         resources.Record
	Connection     ModelConnection
	Identity       connections.Identity
	Network        endpointpolicy.Config
	NetworkProfile *resources.Record
}

func (h *ConnectedHandler) quotaModelConfiguration(ctx context.Context, tenant string, record resources.Record, connection ModelConnection) (quotaModelConfiguration, error) {
	if h.Runtime == nil || h.Store == nil || record.TenantID != tenant || record.Kind != "model-surface" || record.ID != connection.Surface.ID || record.Revision == 0 {
		return quotaModelConfiguration{}, fmt.Errorf("model configuration identity is unavailable")
	}
	// Provider surfaces with a secret-reference credential have no identity
	// profile; the auth block itself is pinned through the connection.
	var identity connections.Identity
	var err error
	if connection.Surface.Provider == nil || connection.IdentityRef != "" {
		if identity, err = h.Runtime.Identity(tenant, connection.IdentityRef); err != nil {
			return quotaModelConfiguration{}, err
		}
	}
	if err = h.checkModelDestination(tenant, connection); err != nil {
		return quotaModelConfiguration{}, err
	}
	if h.Runtime.Policy == nil || h.Runtime.Policy.ValidateURL(connection.Surface.Origin) != nil {
		return quotaModelConfiguration{}, fmt.Errorf("model endpoint policy is unavailable")
	}
	configuration := quotaModelConfiguration{Record: record, Connection: connection, Identity: identity, Network: h.Runtime.Config.Network}
	if connection.NetworkProfileRef != "" {
		profile, err := h.Store.Get(ctx, tenant, "network-profile", connection.NetworkProfileRef)
		if err != nil {
			return quotaModelConfiguration{}, err
		}
		if profile.TenantID != tenant || profile.Kind != "network-profile" || profile.ID != connection.NetworkProfileRef {
			return quotaModelConfiguration{}, fmt.Errorf("network profile namespace changed")
		}
		configuration.NetworkProfile = &profile
	}
	return configuration, nil
}

// ModelExecutionPreparer binds the actual saved surface and resolved credential,
// billing and network facts to the shared quota host. No client group/cost claim
// or unconfigured model alias can choose its funding/coverage contract.
func (h *ConnectedHandler) ModelExecutionPreparer(r *http.Request, record resources.Record, connection ModelConnection) modelapi.ExecutionPreparer {
	if h.Quota == nil {
		return nil
	}
	return modelapi.ExecutionPreparerFunc(func(ctx context.Context, input modelapi.ExecutionRequest) (execution.Lease, error) {
		if !modelapi.Coverage(input.Operation).RequestCount {
			return nil, quotaStatus(501, "QUOTA_OPERATION_UNSUPPORTED", "this model operation has no managed execution adapter")
		}
		// WebSocket sessions are admitted only under the request-count profile
		// (quotaProfileOperation admits no token profile for them): one session
		// is one admitted exchange, settled once at session end, bounded by the
		// gateway's maximum stream duration. The gateway aborts any other lease.
		if input.Context.TenantID != record.TenantID || input.Context.ResourceID != record.ID || input.Surface.ID != connection.Surface.ID {
			return nil, quotaStatus(403, "QUOTA_SURFACE_MISMATCH", "verified surface context changed")
		}
		model := ""
		if input.Metadata.Model != "" {
			var err error
			model, err = connection.Surface.ModelID(input.Metadata.Model)
			if err != nil {
				return nil, quotaStatus(403, "QUOTA_MODEL_UNAVAILABLE", "resolved model is unavailable")
			}
		}
		configuration, err := h.quotaModelConfiguration(ctx, input.Context.TenantID, record, connection)
		if err != nil {
			return nil, quotaStatus(503, "QUOTA_CONFIGURATION_UNAVAILABLE", "model identity, network or configuration is unavailable")
		}
		pinnedDigest, err := quotaDigest(configuration)
		if err != nil {
			return nil, err
		}
		provider := connection.Surface.ProviderID()
		billing := connection.BillingProject
		if billing == "" {
			billing = connection.Surface.Project
		}
		region := connection.Surface.Location
		if connection.Surface.Provider != nil {
			region = connection.Surface.Provider.Region
		}
		target := quotaplex.Target{Plane: "llmplex", Provider: provider, ResourceID: record.ID, Operation: input.Operation.ID, BillingAccount: billing, Region: region}
		reauthorize := func(callCtx context.Context) error {
			current, err := h.Store.Get(callCtx, input.Context.TenantID, "model-surface", record.ID)
			if err != nil {
				return err
			}
			if current.Revision != record.Revision || current.TenantID != record.TenantID || current.ID != record.ID || current.Kind != record.Kind {
				return fmt.Errorf("model surface revision changed")
			}
			var latest ModelConnection
			if err = json.Unmarshal(current.Spec, &latest); err != nil {
				return err
			}
			fresh, err := h.quotaModelConfiguration(callCtx, input.Context.TenantID, current, latest)
			if err != nil {
				return err
			}
			digest, err := quotaDigest(fresh)
			if err != nil {
				return err
			}
			if digest != pinnedDigest || !reflect.DeepEqual(input.Surface, latest.Surface) {
				return fmt.Errorf("resolved provider configuration changed")
			}
			required := []string{"llm:surface:" + record.ID + ":" + input.Operation.ID}
			if input.Metadata.Agent != "" {
				required = append(required, "llm:agent:"+input.Metadata.Agent)
			}
			if model != "" {
				required = append(required, "llm:model:"+model)
			}
			return h.authorizeAllCurrent(callCtx, r, required...)
		}
		if input.Operation.ID == "models.streamGenerateContent" || input.Operation.ID == "endpoints.streamGenerateContent" {
			query, err := url.ParseQuery(input.RawQuery)
			// alt=sse streams frames; without alt the response is one JSON
			// array, which a lease buffers (bounded by the body limit),
			// observes whole and settles before delivery.
			if err != nil || len(query["alt"]) > 1 || (len(query["alt"]) == 1 && query.Get("alt") != "sse") {
				return nil, quotaStatus(400, "QUOTA_STREAM_SHAPE_UNSUPPORTED", "protected Gemini streaming requires alt=sse or the JSON-array form")
			}
		}
		protocolHeaders := http.Header{}
		for _, key := range []string{"Anthropic-Version", "Anthropic-Beta", "Last-Event-ID"} {
			if values := input.Headers.Values(key); len(values) > 0 {
				protocolHeaders[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
			}
		}
		invocationConfiguration := struct {
			Provider        quotaModelConfiguration
			ProtocolHeaders http.Header
		}{configuration, protocolHeaders}
		return h.Quota.Prepare(ctx, r, QuotaInvocation{Context: input.Context, Plane: "llmplex", ResourceID: record.ID, OperationID: input.Operation.ID, Model: model, Target: target, Body: append([]byte(nil), input.Body...), Method: input.Method, Path: input.Path, RawQuery: input.RawQuery, Headers: input.Headers, Configuration: invocationConfiguration, Reauthorize: reauthorize})
	})
}
