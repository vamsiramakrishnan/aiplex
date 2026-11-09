// Package governance binds native cloud agents to reviewed, tenant-scoped
// governance operations. It never mints credentials or allocates model quotas.
package governance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

const BindingKind = "agent-binding"
const PlanKind = "governance-plan"
const ReceiptKind = "governance-receipt"
const ObservationKind = "governance-observation"

var ErrDenied = errors.New("governance operation denied")
var ErrApplyDisabled = errors.New("native governance apply is disabled")
var ErrStale = errors.New("governance binding or plan revision changed")
var ErrAlreadyAttempted = errors.New("governance action already attempted; inspect its receipt")

// TenantID identifies the native identity authority's tenant/directory/account.
// It is distinct from AgentBinding.TenantID, the Aiplex storage namespace.
type PrincipalRef struct {
	Provider    string `json:"provider"`
	TenantID    string `json:"tenant_id"`
	ImmutableID string `json:"immutable_id"`
	Kind        string `json:"kind"`
}

// NativeResourceName, DiscoveryURN and RuntimeRef are distinct coordinates.
// Discovery/import never silently substitutes an alias for a native resource.
type RegistryRef struct {
	Provider           string `json:"provider"`
	TenantID           string `json:"tenant_id"`
	NativeResourceName string `json:"native_resource_name"`
	DiscoveryURN       string `json:"discovery_urn,omitempty"`
	RuntimeRef         string `json:"runtime_ref,omitempty"`
}
type Grant struct {
	Operation string `json:"operation"`
	Resource  string `json:"resource"`
}
type DesiredOperation struct {
	ID        string              `json:"id"`
	Operation nativeapi.Operation `json:"operation"`
}
type AgentBinding struct {
	ID            string             `json:"id"`
	TenantID      string             `json:"tenant_id"`
	DriverRef     string             `json:"driver_ref"`
	Agent         PrincipalRef       `json:"agent"`
	DelegatedUser *PrincipalRef      `json:"delegated_user,omitempty"`
	Sponsor       *PrincipalRef      `json:"sponsor,omitempty"`
	Registry      *RegistryRef       `json:"registry,omitempty"`
	Allowed       []Grant            `json:"allowed"`
	Desired       []DesiredOperation `json:"desired,omitempty"`
}

// DriverRegistration is frozen operator configuration, not resource input.
// ProviderRevision binds endpoint/credential/config identity. ETagReads maps a
// mutation to its read operation when a fresh observation token is required.
// Tokens are native ETags or, only for explicitly enabled non-atomic adapters,
// resource fingerprints. Fingerprints do not provide provider-side atomic CAS.
type DriverRegistration struct {
	Driver              nativeapi.Driver
	TenantID            string
	ProviderRevision    string
	AllowApply          bool
	ETagReads           map[string]string
	InventoryProjection func(nativeapi.Operation, nativeapi.Result) (json.RawMessage, error)
}
type Action struct {
	Phase                                      string
	Context                                    contracts.RequestContext
	BindingID                                  string
	BindingRevision                            uint64
	DriverRef, Operation, Resource, PlanDigest string
}
type Authorizer interface {
	Authorize(context.Context, Action) error
}
type AuthorizeFunc func(context.Context, Action) error

func (f AuthorizeFunc) Authorize(ctx context.Context, a Action) error { return f(ctx, a) }

type Approval struct {
	Reference, TenantID, PrincipalID, ActorID, BindingID, PlanID, PlanDigest string
	PlanRevision                                                             uint64
}
type ApprovalVerifier interface {
	Verify(context.Context, Approval) error
}
type ApprovalFunc func(context.Context, Approval) error

func (f ApprovalFunc) Verify(ctx context.Context, a Approval) error { return f(ctx, a) }

type Config struct {
	Store             resources.Store
	Drivers           map[string]DriverRegistration
	Authorize         Authorizer
	Approvals         ApprovalVerifier
	AllowApply        bool
	MaxPlanTTL        time.Duration
	MaxObservationAge time.Duration
	OperationTimeout  time.Duration
}
type PlanRequest struct {
	Context         contracts.RequestContext
	BindingID       string
	BindingRevision uint64
	DesiredID       string
	TTL             time.Duration
}
type ApplyRequest struct {
	Context      contracts.RequestContext
	PlanID       string
	PlanRevision uint64
	ApprovalRef  string
}
type ReadRequest struct {
	Context         contracts.RequestContext
	BindingID       string
	BindingRevision uint64
	Operation       nativeapi.Operation
}
type Plan struct {
	Version          string    `json:"version"`
	ID               string    `json:"id"`
	Revision         uint64    `json:"revision"`
	TenantID         string    `json:"tenant_id"`
	PrincipalID      string    `json:"principal_id"`
	ActorID          string    `json:"actor_id,omitempty"`
	BindingID        string    `json:"binding_id"`
	BindingRevision  uint64    `json:"binding_revision"`
	BindingDigest    string    `json:"binding_digest"`
	DesiredID        string    `json:"desired_id"`
	DriverRef        string    `json:"driver_ref"`
	ProviderRevision string    `json:"provider_revision"`
	Operation        string    `json:"operation"`
	Resource         string    `json:"resource"`
	PayloadHash      string    `json:"payload_hash"`
	ETag             string    `json:"etag,omitempty"`
	Digest           string    `json:"digest"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}
type Receipt struct {
	Version      string    `json:"version"`
	PlanID       string    `json:"plan_id"`
	PlanRevision uint64    `json:"plan_revision"`
	PlanDigest   string    `json:"plan_digest"`
	TenantID     string    `json:"tenant_id"`
	PrincipalID  string    `json:"principal_id"`
	ActorID      string    `json:"actor_id,omitempty"`
	BindingID    string    `json:"binding_id"`
	State        string    `json:"state"`
	StatusCode   int       `json:"status_code,omitempty"`
	ResultDigest string    `json:"result_digest,omitempty"`
	ETag         string    `json:"etag,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	Readiness    string    `json:"readiness"`
}
type Observation struct {
	Version               string          `json:"version"`
	ID                    string          `json:"id"`
	Revision              uint64          `json:"revision"`
	TenantID              string          `json:"tenant_id"`
	PrincipalID           string          `json:"principal_id"`
	ActorID               string          `json:"actor_id,omitempty"`
	BindingID             string          `json:"binding_id"`
	BindingRevision       uint64          `json:"binding_revision"`
	BindingIdentityDigest string          `json:"binding_identity_digest"`
	DriverRef             string          `json:"driver_ref"`
	ProviderRevision      string          `json:"provider_revision"`
	Operation             string          `json:"operation"`
	Resource              string          `json:"resource"`
	StatusCode            int             `json:"status_code"`
	ETag                  string          `json:"etag,omitempty"`
	ResultDigest          string          `json:"result_digest"`
	Body                  json.RawMessage `json:"body,omitempty"`
	ObservedAt            time.Time       `json:"observed_at"`
}
