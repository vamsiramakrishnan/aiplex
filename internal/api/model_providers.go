package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

// Provider surfaces (Surface.Provider != nil) select their upstream
// credential in exactly one of two ways:
//
//   - auth: a tenant-scoped secret reference, resolved per dispatch by the
//     tool credential provider (same namespace as tool connections) and bound
//     to the surface origin;
//   - identity_ref: an operator identity profile whose allowed_origins
//     approve the surface origin (azure-default for Entra, aws-default for
//     SigV4, bearer-env for an operator environment token).
//
// Google surfaces keep identity_ref only; auth is rejected for them.
func providerAuthAllowed(surface modelapi.Surface, auth toolcredential.Auth) error {
	if surface.Provider == nil {
		return fmt.Errorf("auth applies only to provider surfaces")
	}
	provider := surface.Provider.ID
	if surface.BedrockMantle() {
		// bedrock-mantle documents an Amazon Bedrock API key in x-api-key
		// (inference-messages-api.html, count-tokens.html), not a bearer.
		if auth.Type == toolcredential.APIKey && auth.In == toolcredential.InHeader && strings.EqualFold(auth.Name, modelapi.BedrockMantleAPIKeyHeader) && auth.Prefix == "" {
			return nil
		}
		return fmt.Errorf("bedrock-mantle requires auth type api_key in header %s without prefix, or identity_ref aws-default (SigV4)", modelapi.BedrockMantleAPIKeyHeader)
	}
	switch provider {
	case modelapi.ProviderOpenAI:
		if auth.Type == toolcredential.Bearer {
			return nil
		}
	case modelapi.ProviderBedrock:
		if auth.Type == toolcredential.Bearer { // Bedrock API keys
			return nil
		}
	case modelapi.ProviderOpenAICompatible:
		if auth.Type == toolcredential.Bearer || auth.Type == toolcredential.None {
			return nil
		}
	case modelapi.ProviderAnthropic, modelapi.ProviderAzureOpenAI, modelapi.ProviderGeminiDeveloper:
		d, _ := modelapi.ProviderByID(provider)
		if auth.Type == toolcredential.APIKey && auth.In == toolcredential.InHeader && strings.EqualFold(auth.Name, d.APIKeyHeader) && auth.Prefix == "" {
			return nil
		}
		return fmt.Errorf("%s requires auth type api_key in header %s without prefix", provider, d.APIKeyHeader)
	}
	return fmt.Errorf("auth type %s is not supported for provider %s", auth.Type, provider)
}

func providerIdentityAllowed(provider, identityType string) bool {
	switch provider {
	case modelapi.ProviderAzureOpenAI:
		return identityType == "azure-default"
	case modelapi.ProviderBedrock:
		return identityType == "aws-default"
	case modelapi.ProviderOpenAI, modelapi.ProviderOpenAICompatible:
		return identityType == "bearer-env"
	}
	return false
}

// validateProviderConnection is the strict save-time contract of a provider
// model surface. The surface itself was already validated.
func (h *ConnectedHandler) validateProviderConnection(ctx context.Context, tenant string, c ModelConnection, validateNetworkRef func(string) error) error {
	provider := c.Surface.Provider.ID
	if c.BillingProject != "" {
		return fmt.Errorf("billing_project applies only to Google surfaces")
	}
	if (c.Auth == nil) == (c.IdentityRef == "") {
		return fmt.Errorf("provider surfaces require exactly one of auth or identity_ref")
	}
	if h.Runtime == nil {
		return fmt.Errorf("outbound endpoint policy unavailable")
	}
	if c.Auth != nil {
		if e := c.Auth.Validate(); e != nil {
			return e
		}
		if e := providerAuthAllowed(c.Surface, *c.Auth); e != nil {
			return e
		}
		if refs := c.Auth.SecretRefs(); len(refs) > 0 {
			credentials, e := h.toolCredentials()
			if e != nil {
				return fmt.Errorf("credential provider unavailable")
			}
			for _, ref := range refs {
				if e = credentials.CheckSecret(ctx, tenant, ref); e != nil {
					return e
				}
			}
		}
	} else {
		identity, e := h.Runtime.Identity(tenant, c.IdentityRef)
		if e != nil {
			return e
		}
		if !providerIdentityAllowed(provider, identity.Type) {
			return fmt.Errorf("identity profile type %s cannot authenticate provider %s", identity.Type, provider)
		}
		if e = h.Runtime.CheckDestination(tenant, c.IdentityRef, c.Surface.Origin); e != nil {
			return e
		}
	}
	p, e := endpointpolicy.New(h.Runtime.Config.Network)
	if e != nil {
		return e
	}
	if e = p.ValidateURL(c.Surface.Origin); e != nil {
		if e.Error() == "origin not approved" {
			return withField("surface/origin", fmt.Errorf("origin not approved: %s is not in the approved outbound origins. Add it to network.allowed_origins in the connections file (AIPLEX_CONNECTIONS_FILE); with aiplex dev, restart it with --allow-origin %s", c.Surface.Origin, c.Surface.Origin))
		}
		return withField("surface/origin", e)
	}
	return validateNetworkRef(c.NetworkProfileRef)
}

// checkModelDestination re-proves credential destination authority at
// dispatch. Secret-reference credentials are origin-bound by construction
// and require the outbound endpoint policy instead of an identity profile.
func (h *ConnectedHandler) checkModelDestination(tenant string, c ModelConnection) error {
	if c.Surface.Provider != nil && c.Auth != nil && c.IdentityRef == "" {
		if h.Runtime.Policy == nil || h.Runtime.Policy.ValidateURL(c.Surface.Origin) != nil {
			return fmt.Errorf("surface origin is not approved by the outbound endpoint policy")
		}
		return nil
	}
	return h.Runtime.CheckDestination(tenant, c.IdentityRef, c.Surface.Origin)
}

// originBound refuses to present a credential to any URL other than the
// surface origin, independently of the credential implementation.
func originBound(origin string, inner modelapi.Credential) modelapi.Credential {
	return modelapi.CredentialFunc(func(ctx context.Context, r *http.Request) error {
		want, err := toolcredential.Origin(origin)
		got, gotErr := toolcredential.Origin(r.URL.String())
		if err != nil || gotErr != nil || want != got {
			return fmt.Errorf("credential is bound to a different origin")
		}
		return inner.Authenticate(ctx, r)
	})
}

func (h *ConnectedHandler) providerCredential(r *http.Request, tenant string, record resources.Record, c ModelConnection) (modelapi.Credential, error) {
	if c.Surface.Provider == nil {
		return nil, fmt.Errorf("provider credential requires a provider surface")
	}
	origin := c.Surface.Origin
	if c.Auth != nil {
		credentials, err := h.toolCredentials()
		if err != nil {
			return nil, err
		}
		auth := *c.Auth
		target := toolcredential.Target{TenantID: tenant, ConnectionID: "model-surface/" + c.Surface.ID, Revision: record.Revision, Resource: origin}
		if identity := GetWIFIdentity(r.Context()); identity != nil {
			target.PrincipalID, target.ActorID = identity.PrincipalID, identity.ActorID
		}
		return originBound(origin, modelapi.CredentialFunc(func(ctx context.Context, req *http.Request) error {
			credential, err := credentials.Acquire(ctx, auth, target)
			if err != nil {
				return err
			}
			return credential.Apply(req)
		})), nil
	}
	identity, err := h.Runtime.Identity(tenant, c.IdentityRef)
	if err != nil || !providerIdentityAllowed(c.Surface.Provider.ID, identity.Type) {
		return nil, fmt.Errorf("provider identity profile unavailable")
	}
	switch identity.Type {
	case "aws-default":
		// The signing service follows the endpoint: bedrock (Runtime) or
		// bedrock-mantle; the region is the surface's validated region.
		region, service := c.Surface.Provider.Region, c.Surface.BedrockSigningService()
		return originBound(origin, modelapi.CredentialFunc(func(ctx context.Context, req *http.Request) error {
			cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
			if err != nil {
				return fmt.Errorf("AWS credential chain unavailable")
			}
			return (cloudagents.SigV4Credentials{Source: cfg.Credentials, Region: region, Service: service}).Authenticate(ctx, req)
		})), nil
	default: // azure-default (Entra) and bearer-env
		ref := c.IdentityRef
		return originBound(origin, modelapi.BearerCredential(modelapi.TokenSourceFunc(func(ctx context.Context) (string, error) {
			return h.Runtime.Token(ctx, tenant, ref)
		}))), nil
	}
}

var capacityMu sync.Mutex

func (h *ConnectedHandler) capacityStore() *providerlimits.Store {
	capacityMu.Lock()
	defer capacityMu.Unlock()
	if h.Capacity == nil {
		h.Capacity = providerlimits.NewStore(0)
	}
	return h.Capacity
}

// configureProviderOptions installs the provider credential and limit
// observation on gateway options for a provider surface.
func (h *ConnectedHandler) configureProviderOptions(r *http.Request, tenant, id string, record resources.Record, c ModelConnection, opts *modelapi.Options, usage *usageCapture) error {
	credential, err := h.providerCredential(r, tenant, record, c)
	if err != nil {
		return err
	}
	opts.Credential = credential
	opts.UpstreamHeaders = nil
	store := h.capacityStore()
	opts.ObserveLimits = func(_ context.Context, _ modelapi.Owner, limits providerlimits.Limits) {
		store.Record(tenant, id, limits)
		usage.limits(limits)
	}
	return nil
}

// SurfaceCapacity reports the last provider limit observation for one of the
// tenant's surfaces on this replica. It is administrative and advisory.
func (h *ConnectedHandler) SurfaceCapacity(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	if !privilegedOrDevelopment(r, h.Development, models.RoleAdmin) {
		Error(w, r, 403, "ADMIN_REQUIRED", "surface capacity requires an administrative role")
		return
	}
	id := idParam(r)
	record, err := h.Store.Get(r.Context(), tenant, "model-surface", id)
	if err != nil {
		resourceError(w, r, err)
		return
	}
	var connection ModelConnection
	_ = json.Unmarshal(record.Spec, &connection)
	limits, observed := h.capacityStore().LastObserved(tenant, id)
	response := struct {
		SurfaceID string                 `json:"surface_id"`
		Observed  bool                   `json:"observed"`
		Scope     string                 `json:"scope"`
		Limits    *providerlimits.Limits `json:"limits,omitempty"`
		// CapacityPolicy is the surface's enforcement policy and cooldown state.
		CapacityPolicy capacityPolicyReport `json:"capacity_policy"`
	}{SurfaceID: id, CapacityPolicy: h.capacityReport(tenant, id, connection), Observed: observed, Scope: "replica-local last observation; advisory"}
	if observed {
		response.Limits = &limits
	}
	JSON(w, 200, response)
}

// CapacitySignal exposes the store through the narrow scheduler contract.
func (h *ConnectedHandler) CapacitySignal() providerlimits.CapacitySignal { return h.capacityStore() }
