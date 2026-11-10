package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

type quotaHostLease struct {
	mu                sync.Mutex
	host              *QuotaHost
	engine            *quotaplex.Engine
	bound             *quotaplex.BoundProvider
	ticket            *enforcement.Ticket
	demand            quotaplex.Demand
	binding           QuotaBinding
	proof             *QuotaBoundsProof
	claim             *quotaplex.DispatchClaim
	claimUncertain    bool
	resource          string
	modelVersion      string
	counters          execution.CumulativeUsage
	evidence          *quotaplex.TerminalEvidence
	acknowledged      bool
	completed         bool
	exposureViolation bool
}

func newQuotaHostLease(host *QuotaHost, engine *quotaplex.Engine, bound *quotaplex.BoundProvider, ticket *enforcement.Ticket, demand quotaplex.Demand, binding QuotaBinding, proof *QuotaBoundsProof) *quotaHostLease {
	return &quotaHostLease{host: host, engine: engine, bound: bound, ticket: ticket, demand: demand, binding: binding, proof: proof}
}
func (l *quotaHostLease) AttemptID() string       { return l.demand.AttemptID }
func (l *quotaHostLease) MeteringProfile() string { return l.binding.Profile }
func quotaDurableContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func (l *quotaHostLease) BeforeDispatch(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.claim != nil || l.claimUncertain {
		return quotaStatus(409, "QUOTA_DISPATCH_CONFLICT", "dispatch has already been claimed")
	}
	claim, err := l.bound.ClaimDispatch(ctx)
	if err != nil {
		// A definite conflict belongs to an already claimed attempt; this
		// request must not relabel another live execution as unknown.
		if errors.Is(err, quotaplex.ErrDispatchClaimed) || errors.Is(err, quotaplex.ErrConflict) || errors.Is(err, quotaplex.ErrInvalidTransition) {
			return quotaAdmissionError(err)
		}
		bounded, cancel := quotaDurableContext(ctx)
		defer cancel()
		info, readErr := l.engine.GetAttempt(bounded, l.demand.Context.TenantID, l.demand.AttemptID)
		if readErr != nil {
			l.claimUncertain = true
		} else if info.Claim.Fence != 0 {
			copy := info.Claim
			l.claim = &copy
			_ = l.engine.MarkUnknown(bounded, copy, "dispatch claim acknowledgement unavailable")
		}
		var status *execution.StatusError
		if errors.As(err, &status) {
			return status
		}
		return quotaAdmissionError(err)
	}
	l.claim = &claim
	return nil
}

func (l *quotaHostLease) resourceLocked(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if !quotaText(id, true) {
		return quotaStatus(502, "QUOTA_RESOURCE_INVALID", "upstream resource identifier is invalid")
	}
	if l.claim == nil {
		return quotaplex.ErrDispatchNotClaimed
	}
	if l.evidence != nil && l.resource != id {
		return quotaStatus(502, "QUOTA_RESOURCE_CHANGED", "resource cannot change after terminal evidence")
	}
	if l.resource != "" && l.resource != id {
		return quotaStatus(502, "QUOTA_RESOURCE_CHANGED", "upstream resource identifier changed")
	}
	if err := l.engine.RecordProviderResource(ctx, *l.claim, id); err != nil {
		return quotaStatus(503, "QUOTA_RESOURCE_UNACKNOWLEDGED", "upstream resource could not be durably pinned")
	}
	l.resource = id
	return nil
}

func (l *quotaHostLease) Resource(ctx context.Context, resource execution.Resource) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	bounded, cancel := quotaDurableContext(ctx)
	defer cancel()
	return l.resourceLocked(bounded, resource.ID)
}

func (l *quotaHostLease) Observe(ctx context.Context, observation execution.Observation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.claim == nil {
		return quotaplex.ErrDispatchNotClaimed
	}
	if observation.Source != "upstream" || len(observation.Counters) > 64 {
		return quotaStatus(502, "QUOTA_USAGE_UNTRUSTED", "usage is outside the trusted producer contract")
	}
	for _, value := range observation.Counters {
		if value < 0 {
			return quotaStatus(502, "QUOTA_USAGE_INVALID", "upstream usage contains negative quantities")
		}
	}
	bounded, cancel := quotaDurableContext(ctx)
	defer cancel()
	if err := l.resourceLocked(bounded, observation.ResourceID); err != nil {
		return err
	}
	if observation.ModelVersion != "" {
		if !quotaText(observation.ModelVersion, true) {
			return quotaStatus(502, "QUOTA_MODEL_VERSION_INVALID", "upstream model version is invalid")
		}
		if l.modelVersion != "" && l.modelVersion != observation.ModelVersion {
			if err := l.host.quarantineBinding(bounded, l.binding, observation.ModelVersion, "model_version_changed"); err != nil {
				return quotaStatus(503, "QUOTA_QUARANTINE_UNACKNOWLEDGED", "model-version quarantine could not be persisted")
			}
			return quotaStatus(502, "QUOTA_MODEL_VERSION_CHANGED", "upstream model version changed within the attempt")
		}
		l.modelVersion = observation.ModelVersion
		if !observation.Terminal && l.binding.ModelVersion != "" && l.modelVersion != l.binding.ModelVersion {
			if err := l.host.quarantineBinding(bounded, l.binding, l.modelVersion, "model_version_mismatch"); err != nil {
				return quotaStatus(503, "QUOTA_QUARANTINE_UNACKNOWLEDGED", "model-version quarantine could not be persisted")
			}
			return quotaStatus(502, "QUOTA_MODEL_VERSION_MISMATCH", "upstream model version differs from the configured authority")
		}
	}
	if quotaStrictProfile(l.binding.Profile) {
		if !observation.Cumulative {
			return quotaStatus(502, "QUOTA_USAGE_INVALID", "protected Gemini usage must be cumulative")
		}
		if err := l.counters.Observe(observation.Counters); err != nil {
			if errors.Is(err, execution.ErrUsageRegression) {
				return quotaStatus(502, "QUOTA_USAGE_REGRESSED", "upstream cumulative usage regressed")
			}
			return quotaStatus(502, "QUOTA_USAGE_INVALID", "upstream cumulative usage is invalid")
		}
		if !observation.Terminal && l.exceedsExposure(l.counters.Snapshot()) {
			l.exposureViolation = true
			if err := l.host.quarantineBinding(bounded, l.binding, l.modelVersion, "exposure_bound_exceeded"); err != nil {
				return quotaStatus(503, "QUOTA_QUARANTINE_UNACKNOWLEDGED", "provider-exposure quarantine could not be persisted")
			}
			// Stop reuse of the disproven exposure profile immediately while
			// allowing this claimed stream to produce complete actual evidence.
		}
	}
	if !observation.Terminal {
		return nil
	}
	if l.binding.Profile == quotaProfileRequests {
		return l.terminalLocked(bounded, map[string]int64{"requests": 1, "tool_calls": 1})
	}
	return l.terminalLocked(bounded, observation.Counters)
}

// Missing partial counters do not assert zero. A known subset beyond its bound
// or known disjoint subsets whose sum exceeds a shared ceiling is already
// sufficient evidence.
func (l *quotaHostLease) exceedsExposure(native map[string]int64) bool {
	if l.binding.Profile == quotaProfileLiveAllowance {
		// A window beyond its allowance is a recorded overrun (debt that
		// blocks further admissions), not a disproven provider bound.
		return false
	}
	for counter, meter := range l.binding.Meters {
		if value, known := native[counter]; known && value > l.demand.Bounds[meter] {
			return true
		}
	}
	if l.proof == nil {
		return false
	}
	inputLimit, attested := l.proof.NativeBounds["uncached_input_tokens"]
	if !attested {
		inputLimit = l.proof.NativeBounds["input_tokens"]
	}
	if value, known := native["input_tokens"]; known && value > inputLimit {
		return true
	}
	exceeds := func(counters []string, limit int64) bool {
		var sum int64
		for _, counter := range counters {
			value, known := native[counter]
			if !known {
				continue
			}
			if value > limit || sum > limit-value {
				return true
			}
			sum += value
		}
		return false
	}
	inputs := []string{"uncached_input_tokens", "cached_tokens", "cache_write_tokens"}
	if quotaExclusiveInputProfile(l.binding.Profile) {
		if _, normalized := native["uncached_input_tokens"]; !normalized {
			inputs[0] = "input_tokens"
		}
	}
	if exceeds(inputs, inputLimit) {
		return true
	}
	if l.binding.Profile == quotaProfileRealtime && exceeds(quotaRealtimeInputCounters, inputLimit) {
		return true
	}
	return exceeds(quotaProfileOutputCounters(l.binding.Profile), l.proof.NativeBounds["output_tokens"])
}

func quotaExclusiveInputProfile(profile string) bool {
	return profile == quotaProfileAnthropic || profile == quotaProfileBedrockConverse
}

// quotaUnmeteredCacheWrites fails closed when a terminal report carries a
// positive cache write that the binding has no meter for, or a per-TTL
// mapping whose buckets do not add up to the reported total.
func (l *quotaHostLease) quotaUnmeteredCacheWrites(native map[string]int64) error {
	if !quotaStrictProfile(l.binding.Profile) {
		return nil
	}
	write := native["cache_write_tokens"]
	mapped, _ := quotaBindingCacheWrites(l.binding)
	if write > 0 && len(mapped) == 0 {
		return quotaStatus(502, "QUOTA_USAGE_UNMETERED", "positive cache writes have no configured meter")
	}
	if len(mapped) > 1 {
		var sum int64
		for _, counter := range mapped {
			value, present := native[counter]
			if !present {
				return quotaStatus(502, "QUOTA_USAGE_INCOMPLETE", "terminal usage omits the cache-write breakdown")
			}
			sum += value
		}
		if sum != write {
			return quotaStatus(502, "QUOTA_USAGE_INVALID", "cache-write breakdown conflicts with its total")
		}
	}
	if native["server_tool_calls"] != 0 {
		return quotaStatus(502, "QUOTA_USAGE_UNMETERED", "server-tool quantities have no token meter")
	}
	return nil
}

func (l *quotaHostLease) terminalLocked(ctx context.Context, native map[string]int64) error {
	if l.claim == nil {
		return quotaplex.ErrDispatchNotClaimed
	}
	if err := l.quotaUnmeteredCacheWrites(native); err != nil {
		return err
	}
	usage := quotaplex.Vector{}
	for counter, meter := range l.binding.Meters {
		value, present := native[counter]
		if counter == "requests" {
			value, present = 1, true
		}
		if !present || value < 0 {
			return quotaStatus(502, "QUOTA_USAGE_INCOMPLETE", "terminal usage omits a protected meter")
		}
		usage[meter] = value
	}
	if l.exceedsExposure(native) {
		l.exposureViolation = true
	}
	producer := "native-model"
	if l.binding.Plane == "mcplex" {
		producer = "native-tool"
	}
	if l.binding.Plane == "a2aplex" {
		producer = "native-agent"
	}
	eventID, _ := quotaDigest([]string{l.demand.Context.TenantID, l.demand.AttemptID, producer, "terminal"})
	evidence := quotaplex.TerminalEvidence{Version: quotaplex.Version, ProducerID: producer, ConfigurationDigest: l.demand.Target.ConfigurationDigest, Settlement: quotaplex.Settlement{TenantID: l.demand.Context.TenantID, AttemptID: l.demand.AttemptID, EventID: eventID, Claim: *l.claim, Usage: usage, ProviderResourceID: l.resource}, ObservedModelVersion: l.modelVersion, RequireModelVersion: quotaStrictProfile(l.binding.Profile)}
	if l.evidence != nil {
		oldDigest, _ := quotaDigest(l.evidence)
		newDigest, _ := quotaDigest(evidence)
		if oldDigest != newDigest {
			return quotaStatus(502, "QUOTA_TERMINAL_CHANGED", "terminal evidence changed on replay")
		}
	} else {
		l.evidence = &evidence
	}
	return l.flushTerminalLocked(ctx)
}

func (l *quotaHostLease) flushTerminalLocked(ctx context.Context) error {
	if l.evidence == nil {
		return quotaplex.ErrTerminalEvidenceRequired
	}
	mismatch := (quotaStrictProfile(l.binding.Profile) && l.modelVersion == "") || (l.modelVersion != "" && l.binding.ModelVersion != "" && l.modelVersion != l.binding.ModelVersion)
	if mismatch || l.exposureViolation {
		observed := l.modelVersion
		if observed == "" {
			observed = "<missing>"
		}
		reason := "exposure_bound_exceeded"
		if mismatch {
			reason = "model_version_mismatch"
		}
		if err := l.host.quarantineBinding(ctx, l.binding, observed, reason); err != nil {
			return quotaStatus(503, "QUOTA_QUARANTINE_UNACKNOWLEDGED", "provider-exposure quarantine could not be persisted")
		}
	}
	ack, err := l.engine.EnqueueTerminal(ctx, *l.evidence)
	if err != nil {
		return quotaStatus(503, "QUOTA_USAGE_UNACKNOWLEDGED", "terminal usage could not be durably acknowledged")
	}
	l.acknowledged = true
	if !mismatch {
		processed, processErr := l.engine.ProcessTerminal(ctx, ack.TenantID, ack.EventID)
		if processErr == nil && processed.Status == quotaplex.UsageSettled && !l.completed {
			// The durable outbox is authoritative. These idempotent calls complete
			// the in-process pipeline too; failures cannot undo its settled ledger.
			if _, err = l.bound.SettleTerminalWithModelVersion(ctx, l.evidence.Settlement.EventID, l.evidence.Settlement.Usage, l.resource, l.modelVersion); err == nil {
				if err = l.ticket.Complete(ctx, enforcement.Outcome{Successful: true, Usage: l.evidence.Settlement.Usage}); err == nil {
					l.completed = true
				}
			}
		}
	}
	if mismatch {
		return quotaStatus(502, "QUOTA_MODEL_VERSION_MISMATCH", "upstream model version does not match its exposure authority")
	}
	return nil
}

func (l *quotaHostLease) Finish(ctx context.Context, result execution.Result) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	bounded, cancel := quotaDurableContext(ctx)
	defer cancel()
	if l.evidence != nil {
		if l.acknowledged {
			return nil
		}
		return l.flushTerminalLocked(bounded)
	}
	if l.claim == nil {
		if l.claimUncertain {
			return quotaStatus(503, "QUOTA_DISPATCH_UNCERTAIN", "dispatch claim requires recovery")
		}
		return l.ticket.Abort(bounded)
	}
	if l.binding.Profile == quotaProfileRequests && result.ResponseComplete {
		return l.terminalLocked(bounded, map[string]int64{"requests": 1, "tool_calls": 1})
	}
	if result.Rejected && result.ResponseComplete && l.binding.Plane == "llmplex" {
		// A documented not-processed rejection (see modelapi
		// rejectedBeforeProcessing): one request, zero consumption. No model
		// ran, so the pinned target version stands as the version proof.
		zero := map[string]int64{"requests": 1}
		for counter := range l.binding.Meters {
			if counter != "requests" {
				zero[counter] = 0
			}
		}
		if l.modelVersion == "" {
			l.modelVersion = l.binding.ModelVersion
		}
		return l.terminalLocked(bounded, zero)
	}
	info, err := l.engine.GetAttempt(bounded, l.demand.Context.TenantID, l.demand.AttemptID)
	if err == nil && info.Decision.State == "settled" {
		return nil
	}
	err = l.engine.MarkUnknown(bounded, *l.claim, "terminal usage unavailable")
	if err != nil {
		return quotaStatus(503, "QUOTA_UNKNOWN_UNACKNOWLEDGED", "uncertain execution could not be durably recorded")
	}
	return quotaplex.ErrTerminalEvidenceRequired
}

func (l *quotaHostLease) Abort(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	bounded, cancel := quotaDurableContext(ctx)
	defer cancel()
	if l.claim != nil || l.claimUncertain {
		return quotaplex.ErrInvalidTransition
	}
	return l.ticket.Abort(bounded)
}

var _ execution.Lease = (*quotaHostLease)(nil)
var _ execution.Receipt = (*quotaHostLease)(nil)
