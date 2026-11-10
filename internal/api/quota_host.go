package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// QuotaHostConfig is operator-owned deployment configuration. It is not accepted
// from native invocation bodies, funding headers, or unverified identity claims.
type QuotaHostConfig struct {
	Tenants          []QuotaTenantConfig `json:"tenants"`
	Bindings         []QuotaBinding      `json:"bindings"`
	MaxMembershipAge time.Duration       `json:"max_membership_age"`
}

type QuotaTenantConfig struct {
	Policy         quotaplex.Policy `json:"policy"`
	ExpectedDigest string           `json:"expected_digest,omitempty"`
	Activate       bool             `json:"activate,omitempty"`
}

type QuotaBinding struct {
	ID               string            `json:"id"`
	TenantID         string            `json:"tenant_id"`
	Plane            string            `json:"plane"`
	ResourceID       string            `json:"resource_id"`
	OperationID      string            `json:"operation_id"`
	Model            string            `json:"model,omitempty"`
	Profile          string            `json:"profile"` // request-count or gemini-text
	FundingAccountID string            `json:"funding_account_id,omitempty"`
	Provider         string            `json:"provider"`
	PoolID           string            `json:"pool_id,omitempty"`
	BillingAccount   string            `json:"billing_account,omitempty"`
	Region           string            `json:"region,omitempty"`
	ModelVersion     string            `json:"model_version,omitempty"`
	Meters           map[string]string `json:"meters"`           // normalized native counter -> policy meter
	Limits           map[string]int64  `json:"limits,omitempty"` // reserved; nonempty values are rejected
}

// QuotaActorResolver must independently establish the actor's membership view.
// A delegated principal's groups cannot be copied to this subject.
type QuotaActorResolver interface {
	ResolveActor(context.Context, string, string, time.Time) (quotaplex.Subject, error)
}

// QuotaMembershipResolver resolves principal and actor independently against
// an operator-pinned authoritative directory. Configuration is part of the
// durable host epoch, so an old replica cannot keep using replaced trust keys.
type QuotaMembershipResolver interface {
	ResolveQuotaSubject(context.Context, string, *models.WIFIdentity, string, time.Time) (quotaplex.Subject, error)
	ConfigurationDigest() string
}

type QuotaAdmissionController interface {
	Admit(context.Context, *quotaplex.Engine, quotaplex.Demand) (quotaplex.Decision, error)
	Recheck(context.Context, *quotaplex.Engine, quotaplex.Demand) error
	ConfigurationDigest() string
}

type QuotaHostOption func(*QuotaHost) error

func WithQuotaMembershipResolver(resolver QuotaMembershipResolver) QuotaHostOption {
	return func(h *QuotaHost) error {
		if !quotaInterfacePresent(resolver) || !quotaCanonicalDigest(resolver.ConfigurationDigest()) {
			return fmt.Errorf("authoritative membership resolver manifest required")
		}
		h.membership = resolver
		return nil
	}
}

func WithQuotaAdmissionController(controller QuotaAdmissionController) QuotaHostOption {
	return func(h *QuotaHost) error {
		if !quotaInterfacePresent(controller) || !quotaCanonicalDigest(controller.ConfigurationDigest()) {
			return fmt.Errorf("quota admission controller required")
		}
		h.admission = controller
		return nil
	}
}

type QuotaBoundsRequest struct {
	TenantID        string
	BindingID       string
	Target          quotaplex.Target
	Model           string
	Body            []byte
	RequestedOutput int64
	At              time.Time
}

type QuotaBoundsProof struct {
	NativeBounds        map[string]int64
	EvidenceRef         string
	ExpiresAt           time.Time
	ConfigurationDigest string
	ModelVersion        string
}

// TrustedBoundsAuthority attests provider-enforced exposure limits. Estimates,
// caller-declared token counts and unconstrained operator maxima are not proof.
// Gemini deployment authorities must also expose ConfigurationDigest() string,
// a canonical SHA-256 manifest digest pinned by the versioned host configuration.
type TrustedBoundsAuthority interface {
	Bounds(context.Context, QuotaBoundsRequest) (QuotaBoundsProof, error)
}

type quotaTenant struct {
	engine *quotaplex.Engine
	policy *quotaplex.CompiledPolicy
}

type QuotaHost struct {
	tenants              map[string]quotaTenant
	bindings             map[string]QuotaBinding
	clock                func() time.Time
	actor                QuotaActorResolver
	membership           QuotaMembershipResolver
	admission            QuotaAdmissionController
	bounds               TrustedBoundsAuthority
	maxMembershipAge     time.Duration
	store                quotaplex.Store
	configurationDigests map[string]string
	// budgets resolves a tenant's budget tree; nil means no tenant budgets.
	budgets BudgetSource
}

// QuotaInvocation is constructed by a trusted model/tool adapter. BeforeDispatch
// must recheck the saved configuration and current execution grants.
type QuotaInvocation struct {
	Context       contracts.RequestContext
	Plane         string
	ResourceID    string
	OperationID   string
	Model         string
	Target        quotaplex.Target
	Body          []byte
	Method        string
	Path          string
	RawQuery      string
	Headers       http.Header
	Configuration any
	Reauthorize   func(context.Context) error
	// SessionAllowance is the per-meter Live allowance window from the
	// saved surface configuration (websocket.token_allowance); 0 when the
	// surface does not meter sessions by token.
	SessionAllowance int64
	// UsageSemantics is the saved surface's declaration of a generic
	// compatible server's usage contract (provider.usage_semantics).
	UsageSemantics string
}

// Prepare builds one typed, admitted execution lease shared by native model and
// tool transports. Its implementation also pins trusted membership and prices.
func (h *QuotaHost) Prepare(ctx context.Context, r *http.Request, invocation QuotaInvocation) (execution.Lease, error) {
	return h.prepare(ctx, r, invocation)
}
