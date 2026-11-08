// Package deployment defines optional deployment providers. Preparing a plan is
// distinct from applying it, and successful invocation never implies readiness.
package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

const AnvilContractRevision = "c2d31108e2b02d8a543a7c1b01e8246f546bd6c9"

var ErrDisabled = errors.New("deployment execution is disabled")
var ErrConflict = errors.New("deployment idempotency conflict")

type State string

const (
	Planned   State = "planned"
	Pending   State = "pending"
	Succeeded State = "invocation_succeeded"
	Refused   State = "refused"
	Unknown   State = "unknown"
)

type Bundle struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Directory string `json:"directory"`
	// Digest uses Anvil's canonical bundleHash contract, not a ZIP/git hash.
	Digest string `json:"digest"`
}
type Binding struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	ResourceID string `json:"resource_id"`
	BundleID   string `json:"bundle_id"`
	// Command is the exact reviewed generated CLI handle, e.g. resources create.
	Command     []string `json:"command,omitempty"`
	OperationID string   `json:"operation_id,omitempty"`
	AuthProfile string   `json:"auth_profile,omitempty"`
	Scopes      []string `json:"anvil_scopes,omitempty"`
	// PlanTarget selects Anvil's plan-only deploy command instead of a native
	// infrastructure API operation. Its output can never be passed to Execute.
	PlanTarget string `json:"plan_target,omitempty"`
	Effect     string `json:"effect"`
}
type Config struct {
	ID                  string            `json:"id"`
	Executable          string            `json:"executable"`
	ExecutableSHA256    string            `json:"executable_sha256"`
	PrefixArgs          []string          `json:"prefix_args,omitempty"`
	PrefixSHA256        map[string]string `json:"prefix_sha256,omitempty"`
	ExpectedBuildDigest string            `json:"expected_build_digest"`
	Bundles             []Bundle          `json:"bundles"`
	Bindings            []Binding         `json:"bindings"`
	Environment         []string          `json:"-"`
	Timeout             time.Duration     `json:"timeout,omitempty"`
	MaxOutputBytes      int               `json:"max_output_bytes,omitempty"`
	AllowExecute        bool              `json:"allow_execute"`
}
type Request struct {
	Provenance     json.RawMessage          `json:"provenance,omitempty"`
	Context        contracts.RequestContext `json:"context"`
	BindingID      string                   `json:"binding_id"`
	Inputs         json.RawMessage          `json:"inputs"`
	IdempotencyKey string                   `json:"idempotency_key"`
}
type Plan struct {
	Version             string          `json:"version"`
	ProviderID          string          `json:"provider_id"`
	BindingID           string          `json:"binding_id"`
	TenantID            string          `json:"tenant_id"`
	PrincipalID         string          `json:"principal_id"`
	ActorID             string          `json:"actor_id,omitempty"`
	ResourceID          string          `json:"resource_id"`
	OperationID         string          `json:"operation_id,omitempty"`
	BundleDigest        string          `json:"bundle_digest"`
	SnapshotDigest      string          `json:"snapshot_digest"`
	ToolBuildDigest     string          `json:"tool_build_digest"`
	ExecutableDigest    string          `json:"executable_digest"`
	ProvenanceDigest    string          `json:"provenance_digest"`
	InputDigest         string          `json:"input_digest"`
	ConfigurationDigest string          `json:"configuration_digest"`
	BindingDigest       string          `json:"binding_digest"`
	PreviewDigest       string          `json:"preview_digest"`
	ActionDigest        string          `json:"action_digest"`
	IdempotencyKey      string          `json:"idempotency_key"`
	Effect              string          `json:"effect"`
	PlanOnly            bool            `json:"plan_only"`
	State               State           `json:"state"`
	CreatedAt           time.Time       `json:"created_at"`
	ExpiresAt           time.Time       `json:"expires_at"`
	Preview             json.RawMessage `json:"preview"`
}
type Action struct {
	Request                 Request
	Phase                   string
	ProviderID, OperationID string
}
type Authorizer interface {
	Authorize(context.Context, Action) error
}
type AuthorizeFunc func(context.Context, Action) error

func (f AuthorizeFunc) Authorize(ctx context.Context, a Action) error { return f(ctx, a) }

// Admission is mandatory. A host may explicitly install its no-budget admission
// policy, but omitting enforcement never silently yields an allow decision.
type Admission interface {
	Admit(context.Context, Action) (AdmissionReceipt, error)
}
type AdmissionReceipt interface {
	Complete(context.Context, bool) error
	Abort(context.Context) error
}
type Approval struct{ Reference, TenantID, PrincipalID, ResourceID, ActionDigest string }
type ApprovalVerifier interface {
	Verify(context.Context, Approval) error
}
type Receipt struct {
	ProviderID     string    `json:"provider_id"`
	TenantID       string    `json:"tenant_id"`
	PrincipalID    string    `json:"principal_id"`
	ResourceID     string    `json:"resource_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	ActionDigest   string    `json:"action_digest"`
	State          State     `json:"state"`
	OutputDigest   string    `json:"output_digest,omitempty"`
	ErrorCode      string    `json:"error_code,omitempty"`
	Readiness      string    `json:"readiness"`
	FinishedAt     time.Time `json:"finished_at,omitempty"`
}

// Journal.Begin atomically stores Pending before execution. An existing key
// must return its prior receipt; a mismatched action digest must return conflict.
// Production implementations must be durable. No runtime retry is automatic.
type Journal interface {
	Begin(context.Context, Receipt) (Receipt, bool, error)
	Finish(context.Context, Receipt) error
}

// Provider permits other native/artifact providers without importing Anvil.
type Provider interface {
	Prepare(context.Context, Request) (Plan, error)
	Execute(context.Context, Request, Plan, string) (Receipt, error)
}
type ProcessResult struct {
	Stdout, Stderr []byte
	ExitCode       int
	Started        bool
	Interrupted    bool
}
type Command struct {
	Executable  string
	Args, Env   []string
	Directory   string
	OutputLimit int
}

// Runner must execute structured argv without a shell, bound output/time, and
// report interruption separately from a completed nonzero exit.
type Runner interface {
	Run(context.Context, Command) (ProcessResult, error)
}

// BindingInfo is safe discovery metadata; runtime paths, environment and
// credentials remain operator-owned configuration.
type BindingInfo struct {
	ID          string `json:"id"`
	ResourceID  string `json:"resource_id"`
	OperationID string `json:"operation_id,omitempty"`
	Effect      string `json:"effect"`
	PlanOnly    bool   `json:"plan_only"`
}

// Intent connects a tenant-owned desired-state resource to an approved provider
// binding. The host supplies the tenant namespace and validates all references.
// Inputs are reviewed non-secret desired state; credentials are host-owned refs.
type Intent struct {
	ID                string          `json:"id"`
	ProviderRef       string          `json:"provider_ref"`
	BindingID         string          `json:"binding_id"`
	NetworkProfileRef string          `json:"network_profile_ref,omitempty"`
	Inputs            json.RawMessage `json:"inputs"`
}

func (i Intent) Validate() error {
	if !handle.MatchString(i.ID) || !handle.MatchString(i.ProviderRef) || !handle.MatchString(i.BindingID) {
		return fmt.Errorf("intent id, provider and binding references required")
	}
	if i.NetworkProfileRef != "" && !handle.MatchString(i.NetworkProfileRef) {
		return fmt.Errorf("invalid network profile reference")
	}
	var object map[string]json.RawMessage
	if len(i.Inputs) > 1<<20 || json.Unmarshal(i.Inputs, &object) != nil || object == nil {
		return fmt.Errorf("intent inputs must be a bounded JSON object")
	}
	return nil
}
