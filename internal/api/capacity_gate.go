package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
)

// The capacity gate turns the last provider limit observation into an
// additional admission step in front of upstream dispatch. It is configured
// per surface by capacity_policy (off by default), runs after authorization
// and before QuotaPlex admission, credential acquisition and upstream I/O,
// and never replaces any of them.

func (h *ConnectedHandler) capacityGate() *providerlimits.Gate {
	store := h.capacityStore()
	capacityMu.Lock()
	defer capacityMu.Unlock()
	if h.CapacityGate == nil {
		h.CapacityGate = providerlimits.NewGate(store)
	}
	return h.CapacityGate
}

// validateCapacityPolicy is the save-time contract of the block. Only provider
// surfaces report limit signals, so a Google surface cannot carry one.
func validateCapacityPolicy(c ModelConnection) error {
	if c.CapacityPolicy == nil {
		return nil
	}
	if c.Surface.Provider == nil {
		return fmt.Errorf("capacity_policy applies only to provider surfaces")
	}
	return c.CapacityPolicy.Validate()
}

// capacityRequest ties one request to its gate decision so the probe can be
// closed from the response observation, which may arrive on another goroutine.
type capacityRequest struct {
	mu       sync.Mutex
	decision providerlimits.Decision
}

func (c *capacityRequest) set(d providerlimits.Decision) {
	c.mu.Lock()
	c.decision = d
	c.mu.Unlock()
}

func (c *capacityRequest) get() providerlimits.Decision {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.decision
}

// installCapacityGate hooks the gate into a provider surface's gateway
// options and returns the function that releases an unanswered probe when the
// request ends. It is a no-op for surfaces without an enabled policy.
func (h *ConnectedHandler) installCapacityGate(r *http.Request, tenant, surface string, c ModelConnection, opts *modelapi.Options, usage *usageCapture) func() {
	noop := func() {}
	if c.CapacityPolicy == nil || c.Surface.Provider == nil || !c.CapacityPolicy.Enabled() {
		return noop
	}
	policy := *c.CapacityPolicy
	gate := h.capacityGate()
	var weight uint32
	if call := accessPointCallFrom(r.Context()); call != nil && call.QoS {
		weight = call.Weight
	}
	request := &capacityRequest{}
	opts.Admit = func(_ context.Context, _ contracts.RequestContext, _ modelapi.Operation, _ modelapi.Metadata) error {
		decision := gate.Admit(tenant, surface, policy, weight)
		if decision.Allow {
			request.set(decision)
			return nil
		}
		// Codes become the ledger error_class, distinct from provider errors.
		message := "the provider is rate limiting this surface; retry later"
		if decision.Reason == providerlimits.ReasonShed {
			message = "provider capacity is reserved for higher-priority callers; retry later"
		}
		err := &execution.StatusError{Status: http.StatusTooManyRequests, Code: decision.Reason, Message: message, RetryAfter: decision.RetryAfter}
		usage.noteError(err)
		return err
	}
	if observe := opts.ObserveLimits; observe != nil {
		opts.ObserveLimits = func(ctx context.Context, owner modelapi.Owner, limits providerlimits.Limits) {
			observe(ctx, owner, limits) // record first: a limiting response must be visible to the next Admit
			request.get().Responded(limits)
		}
	}
	return func() { request.get().Done() }
}

// capacityPolicyReport is the capacity endpoint's view of the policy and the
// scope's current cooldown.
type capacityPolicyReport struct {
	Enabled  bool                          `json:"enabled"`
	Policy   *providerlimits.Policy        `json:"policy,omitempty"`
	Cooldown *providerlimits.CooldownState `json:"cooldown,omitempty"`
}

func (h *ConnectedHandler) capacityReport(tenant, surface string, c ModelConnection) capacityPolicyReport {
	if c.CapacityPolicy == nil || !c.CapacityPolicy.Enabled() {
		return capacityPolicyReport{}
	}
	effective := c.CapacityPolicy.WithDefaults()
	state := h.capacityGate().State(tenant, surface, *c.CapacityPolicy)
	return capacityPolicyReport{Enabled: true, Policy: &effective, Cooldown: &state}
}
