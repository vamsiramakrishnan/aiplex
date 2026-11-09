package api

import (
	"context"
	"net/http"
	"time"

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
	bounds               TrustedBoundsAuthority
	maxMembershipAge     time.Duration
	store                quotaplex.Store
	configurationDigests map[string]string
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
}

// Prepare builds one typed, admitted execution lease shared by native model and
// tool transports. Its implementation also pins trusted membership and prices.
func (h *QuotaHost) Prepare(ctx context.Context, r *http.Request, invocation QuotaInvocation) (execution.Lease, error) {
	return h.prepare(ctx, r, invocation)
}
