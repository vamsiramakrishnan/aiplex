package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"

	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
)

var ErrTerminalEvidenceRequired = errors.New("trusted terminal usage is required")
var ErrDispatchNotClaimed = errors.New("dispatch has not been claimed")
var ErrRequestContextMismatch = errors.New("quota demand does not match trusted request context")

// DispatchAuthorizer rechecks execution authority immediately before a host
// acquires a dispatch claim. It must use verified identity/access, not trust a
// caller's arbitrary payload. The callback runs outside storage transactions.
type DispatchAuthorizer func(context.Context, Demand) error

type AdmissionDenied struct{ Decision Decision }

func (e *AdmissionDenied) Error() string { return "quota admission denied: " + e.Decision.Reason }

// BoundProvider binds one typed demand to the generic enforcement pipeline.
// ClaimDispatch and SettleTerminal remain explicit host lifecycle operations;
// generic Outcome.Successful cannot establish terminal evidence or zero usage.
type BoundProvider struct {
	mu        sync.Mutex
	engine    *Engine
	demand    Demand
	authorize DispatchAuthorizer
	used      bool
	admitted  bool
	claim     *DispatchClaim
	terminal  *Settlement
}

func copyDemand(d Demand) Demand {
	if d.Context.MembershipSnapshotRef != nil {
		ref := *d.Context.MembershipSnapshotRef
		d.Context.MembershipSnapshotRef = &ref
	}
	d.Principal.Membership.Groups = slices.Clone(d.Principal.Membership.Groups)
	if d.Actor != nil {
		actor := *d.Actor
		actor.Membership.Groups = slices.Clone(actor.Membership.Groups)
		d.Actor = &actor
	}
	d.Bounds = CloneVector(d.Bounds)
	return d
}

func (e *Engine) Bind(d Demand, authorize DispatchAuthorizer) (*BoundProvider, error) {
	if e == nil || authorize == nil {
		return nil, fmt.Errorf("engine and dispatch authorizer are required")
	}
	if err := d.Context.Validate(); err != nil {
		return nil, err
	}
	d = copyDemand(d)
	return &BoundProvider{engine: e, demand: d, authorize: authorize}, nil
}

func (b *BoundProvider) Admit(ctx context.Context, request enforcement.Request) (enforcement.Receipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !reflect.DeepEqual(request, b.demand.Context) {
		return nil, ErrRequestContextMismatch
	}
	if b.used {
		return nil, ErrConflict
	}
	b.used = true
	decision, err := b.engine.Admit(ctx, b.demand)
	if err != nil {
		return nil, err
	}
	if !decision.Allowed {
		return nil, &AdmissionDenied{Decision: decision}
	}
	if decision.State != "admitted" {
		return nil, ErrInvalidTransition
	}
	b.admitted = true
	return &boundReceipt{bound: b}, nil
}

func (b *BoundProvider) ClaimDispatch(ctx context.Context) (DispatchClaim, error) {
	b.mu.Lock()
	if !b.admitted {
		b.mu.Unlock()
		return DispatchClaim{}, ErrDispatchNotClaimed
	}
	if b.claim != nil {
		b.mu.Unlock()
		return DispatchClaim{}, ErrConflict
	}
	demand := copyDemand(b.demand)
	b.mu.Unlock()
	if err := b.authorize(ctx, demand); err != nil {
		return DispatchClaim{}, err
	}
	claim, err := b.engine.ClaimDispatch(ctx, demand.Context.TenantID, demand.AttemptID)
	if err != nil {
		return DispatchClaim{}, err
	}
	b.mu.Lock()
	b.claim = &claim
	b.mu.Unlock()
	return claim, nil
}

// RecordProviderResource durably pins a known job/session coordinate as soon as
// the host receives it, allowing recovery before terminal usage is available.
func (b *BoundProvider) RecordProviderResource(ctx context.Context, resourceID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.claim == nil {
		return ErrDispatchNotClaimed
	}
	return b.engine.RecordProviderResource(ctx, *b.claim, resourceID)
}

// SettleTerminal durably acknowledges one trusted final usage result. A client
// disconnect or missing final frame must use MarkUnknown instead. Native host
// adapters, not arbitrary API callers, establish that this evidence is terminal.
func (b *BoundProvider) SettleTerminal(ctx context.Context, eventID string, usage Vector, providerResourceID string) (Decision, error) {
	return b.SettleTerminalWithModelVersion(ctx, eventID, usage, providerResourceID, "")
}

// SettleTerminalWithModelVersion binds terminal usage to an observed model
// version when the admission requires it. The version is part of replay identity.
func (b *BoundProvider) SettleTerminalWithModelVersion(ctx context.Context, eventID string, usage Vector, providerResourceID, modelVersion string) (Decision, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.claim == nil {
		return Decision{}, ErrDispatchNotClaimed
	}
	if usage == nil {
		return Decision{}, ErrTerminalEvidenceRequired
	}
	// Explicit zeros distinguish known-zero usage from a missing measurement.
	for meter := range b.demand.Bounds {
		if _, ok := usage[meter]; !ok {
			return Decision{}, fmt.Errorf("%w: missing meter %s", ErrTerminalEvidenceRequired, meter)
		}
	}
	settlement := Settlement{TenantID: b.demand.Context.TenantID, AttemptID: b.demand.AttemptID, EventID: eventID, Claim: *b.claim, Usage: CloneVector(usage), ProviderResourceID: providerResourceID, ModelVersion: modelVersion}
	decision, err := b.engine.Settle(ctx, settlement)
	if err != nil {
		return Decision{}, err
	}
	b.terminal = &settlement
	return decision, nil
}

func (b *BoundProvider) MarkUnknown(ctx context.Context, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.claim == nil {
		return ErrDispatchNotClaimed
	}
	return b.engine.MarkUnknown(ctx, *b.claim, reason)
}

type boundReceipt struct{ bound *BoundProvider }

func (r *boundReceipt) Complete(ctx context.Context, out enforcement.Outcome) error {
	b := r.bound
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.terminal == nil {
		return ErrTerminalEvidenceRequired
	}
	if out.Usage != nil && !maps.Equal(out.Usage, b.terminal.Usage) {
		return ErrConflict
	}
	_, err := b.engine.Settle(ctx, *b.terminal)
	return err
}

func (r *boundReceipt) Abort(ctx context.Context) error {
	b := r.bound
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.engine.Abort(ctx, b.demand.Context.TenantID, b.demand.AttemptID)
}
