package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// Unit and batch quota profiles.
//
// media-units: one HTTP exchange whose provider-reported usage is a non-token
// unit (generated images, milliseconds of input audio or generated video) and,
// optionally, input/output tokens. Settled once from the terminal observation;
// a terminal response that omits a bound meter fails closed (unknown).
//
// batch-usage: a provider batch job. Admitted once at create with a trusted
// exposure bound; the create exchange pins the batch id and leaves the attempt
// open (state unknown, reason batch_results_pending) so its exposure is held
// and visible in aging/unknown reporting. It is settled exactly once from the
// per-item usage of a complete results fetch (SettleBatch).
const (
	quotaProfileMedia = "media-units"
	quotaProfileBatch = "batch-usage"
)

// Native counters a unit/batch binding may map, with their meter unit.
var (
	quotaMediaMeterUnits = map[string]string{
		"requests":                              "requests",
		modelapi.CounterGeneratedImages:         "images",
		modelapi.CounterAudioInputMilliseconds:  "milliseconds",
		modelapi.CounterVideoOutputMilliseconds: "milliseconds",
		"input_tokens":                          "tokens",
		"output_tokens":                         "tokens",
	}
	quotaBatchMeterUnits = map[string]string{
		"requests":           "requests",
		"batch_requests":     "requests",
		"input_tokens":       "tokens",
		"output_tokens":      "tokens",
		"cached_tokens":      "tokens",
		"cache_write_tokens": "tokens",
	}
)

func quotaExtendedProfile(profile string) bool {
	return profile == quotaProfileMedia || profile == quotaProfileBatch
}

// quotaExtendedMeterUnit returns the meter unit a native counter must map to
// under an extended profile, or "".
func quotaExtendedMeterUnit(binding QuotaBinding, native string) string {
	switch binding.Profile {
	case quotaProfileMedia:
		return quotaMediaMeterUnits[native]
	case quotaProfileBatch:
		return quotaBatchMeterUnits[native]
	}
	return ""
}

// quotaExtendedOperation reports whether a binding's native operation has a
// supported unit or batch exposure contract.
func quotaExtendedOperation(binding QuotaBinding) bool {
	switch binding.Profile {
	case quotaProfileMedia:
		return modelapi.MediaQuotaOperation(binding.OperationID)
	case quotaProfileBatch:
		return modelapi.BatchCreateOperation(binding.OperationID)
	}
	return false
}

// validateExtendedBinding checks an extended-profile binding at host start.
func validateExtendedBinding(binding QuotaBinding, policy quotaplex.Policy) error {
	if binding.Plane != "llmplex" || !quotaExtendedOperation(binding) {
		return fmt.Errorf("unit/batch quota profile does not support this native operation")
	}
	if len(binding.Meters) == 0 || len(binding.Meters) > 8 || binding.Meters["requests"] == "" {
		return fmt.Errorf("quota binding requires a request meter and bounded mappings")
	}
	seen := map[string]bool{}
	for native, meterID := range binding.Meters {
		unit := quotaExtendedMeterUnit(binding, native)
		meter, known := policy.Meters[meterID]
		if unit == "" || !known || meter.Unit != unit || seen[meterID] {
			return fmt.Errorf("quota binding has incompatible or overlapping meter mappings")
		}
		seen[meterID] = true
	}
	return nil
}

// quotaImageCount is the provider-enforced image count of an image request:
// the explicit "n" (1..10) or the documented default of 1
// (https://developers.openai.com/api/reference/resources/images/methods/generate).
func quotaImageCount(body []byte) (int64, error) {
	object, err := quotaJSONObject(body)
	if err != nil {
		return 0, err
	}
	raw := object["n"]
	if raw == nil {
		return 1, nil
	}
	n, err := quotaPositiveInteger(raw)
	if err != nil || n > 10 {
		return 0, fmt.Errorf("image count must be 1..10")
	}
	return n, nil
}

// quotaBatchItemCount is the inline request count of a batch body that lists
// its requests (Anthropic Message Batches "requests"); 0 when not inline.
func quotaBatchItemCount(body []byte) (int64, error) {
	object, err := quotaJSONObject(body)
	if err != nil {
		return 0, err
	}
	raw := object["requests"]
	if raw == nil {
		return 0, nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 || len(items) > 100_000 {
		return 0, fmt.Errorf("bounded inline batch requests required")
	}
	return int64(len(items)), nil
}

// extendedBounds proves the exposure of a unit or batch exchange. Quantities
// fixed by the request itself and enforced by the provider (image count n,
// inline batch item count) are derived from the native body; every other
// mapped meter needs the trusted bounds authority, as token profiles do.
func (h *QuotaHost) extendedBounds(ctx context.Context, binding QuotaBinding, target quotaplex.Target, in QuotaInvocation, now time.Time) (map[string]int64, *QuotaBoundsProof, error) {
	if in.Plane != "llmplex" || in.Model != binding.Model || !quotaExtendedOperation(binding) {
		return nil, nil, quotaStatus(503, "QUOTA_PROFILE_UNSUPPORTED", "operation has no supported exposure contract")
	}
	counts := map[string]int64{"requests": 1}
	derived := map[string]bool{"requests": true}
	if _, mapped := binding.Meters[modelapi.CounterGeneratedImages]; mapped {
		n, err := quotaImageCount(in.Body)
		if err != nil {
			return nil, nil, quotaStatus(400, "QUOTA_UNBOUNDED_REQUEST", "request is outside the protected image profile")
		}
		counts[modelapi.CounterGeneratedImages], derived[modelapi.CounterGeneratedImages] = n, true
	}
	if _, mapped := binding.Meters["batch_requests"]; mapped {
		n, err := quotaBatchItemCount(in.Body)
		if err != nil {
			return nil, nil, quotaStatus(400, "QUOTA_UNBOUNDED_REQUEST", "request is outside the protected batch profile")
		}
		if n > 0 {
			counts["batch_requests"], derived["batch_requests"] = n, true
		}
	}
	needsAuthority := false
	for native := range binding.Meters {
		if !derived[native] {
			needsAuthority = true
		}
	}
	if !needsAuthority {
		return counts, nil, nil
	}
	if !quotaInterfacePresent(h.bounds) {
		return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "trusted provider exposure authority required")
	}
	proof, err := h.bounds.Bounds(ctx, QuotaBoundsRequest{TenantID: in.Context.TenantID, BindingID: binding.ID, Target: target, Model: in.Model, Body: append([]byte(nil), in.Body...), At: now})
	if err != nil || !quotaText(proof.EvidenceRef, true) || !proof.ExpiresAt.After(now) || proof.ModelVersion != binding.ModelVersion || proof.ConfigurationDigest != target.ConfigurationDigest {
		return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "provider exposure authority is unavailable or stale")
	}
	copyBounds := map[string]int64{}
	for key, value := range proof.NativeBounds {
		if !quotaText(key, true) || value < 0 {
			return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "provider exposure proof is invalid")
		}
		copyBounds[key] = value
	}
	proof.NativeBounds = copyBounds
	for native := range binding.Meters {
		if derived[native] {
			// A derived quantity is exact; an authority may only agree or widen.
			if value, present := copyBounds[native]; present && value < counts[native] {
				return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "provider exposure proof is below the request's own bound")
			}
			continue
		}
		value, present := copyBounds[native]
		if !present {
			return nil, nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "provider exposure proof omits a mapped unit bound")
		}
		counts[native] = value
	}
	return counts, &proof, nil
}

// finishBatchLocked ends a batch-create exchange. A successful creation with
// a pinned batch id stays open (unknown) until SettleBatch; a provider
// rejection is provider evidence that no batch exists and settles one request
// with zero for every other bound meter.
func (l *quotaHostLease) finishBatchLocked(ctx context.Context, result execution.Result) error {
	// One rule for every exchange: only a documented not-processed rejection
	// of the create itself (modelapi rejectedBeforeProcessing) releases it.
	// Later polls and results fetches are separate attempts and never release
	// an open batch.
	if result.ResponseComplete && result.Err == nil && result.Rejected {
		zero := map[string]int64{}
		for native := range l.binding.Meters {
			zero[native] = 0
		}
		return l.terminalLocked(ctx, zero)
	}
	if result.ResponseComplete && result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 && l.resource != "" {
		if err := l.engine.MarkUnknown(ctx, *l.claim, "batch_results_pending"); err != nil {
			return quotaStatus(503, "QUOTA_UNKNOWN_UNACKNOWLEDGED", "open batch exposure could not be durably recorded")
		}
		return nil
	}
	if err := l.engine.MarkUnknown(ctx, *l.claim, "terminal usage unavailable"); err != nil {
		return quotaStatus(503, "QUOTA_UNKNOWN_UNACKNOWLEDGED", "uncertain execution could not be durably recorded")
	}
	return quotaplex.ErrTerminalEvidenceRequired
}

// ErrBatchSettlementIncomplete means the batch's results do not report every
// bound meter; the attempt keeps its exposure.
var ErrBatchSettlementIncomplete = errors.New("batch results omit a protected meter")

// errBatchNotQuotaMetered: the attempt was not admitted by a batch-usage
// binding (for example a request-count binding counted only the create).
var errBatchNotQuotaMetered = errors.New("batch attempt has no batch-usage binding")

// SettleBatch settles an open batch-usage attempt from the summed per-item
// usage of its complete results. It is idempotent: the event id is derived
// from the attempt alone, so a repeated fetch with the same totals re-acknowledges
// the same evidence and a different total is refused as a conflict. It never
// adds batch-object totals; callers pass only per-item sums.
func (h *QuotaHost) SettleBatch(ctx context.Context, tenant, attemptID, batchID string, native map[string]int64) error {
	if h == nil {
		return quotaStatus(503, "QUOTA_UNAVAILABLE", "quota host unavailable")
	}
	value, ok := h.tenants[tenant]
	if !ok || value.engine == nil {
		return quotaStatus(503, "QUOTA_TENANT_UNAVAILABLE", "quota tenant unavailable")
	}
	bounded, cancel := quotaDurableContext(ctx)
	defer cancel()
	info, err := value.engine.GetAttempt(bounded, tenant, attemptID)
	if err != nil {
		return quotaStatus(503, "QUOTA_ATTEMPT_UNAVAILABLE", "batch attempt unavailable")
	}
	if info.Claim.Fence == 0 || info.ProviderResource != batchID {
		return quotaStatus(409, "QUOTA_BATCH_MISMATCH", "batch does not match its admitted attempt")
	}
	binding, err := h.batchBinding(tenant, info)
	if err != nil {
		return err
	}
	usage := quotaplex.Vector{}
	for counter, meter := range binding.Meters {
		quantity, present := native[counter]
		if counter == "requests" {
			quantity, present = 1, true
		}
		if !present || quantity < 0 {
			return ErrBatchSettlementIncomplete
		}
		usage[meter] = quantity
	}
	eventID, _ := quotaDigest([]string{tenant, attemptID, "native-batch", "terminal"})
	evidence := quotaplex.TerminalEvidence{Version: quotaplex.Version, ProducerID: "native-batch", ConfigurationDigest: info.Demand.Target.ConfigurationDigest, Settlement: quotaplex.Settlement{TenantID: tenant, AttemptID: attemptID, EventID: eventID, Claim: info.Claim, Usage: usage, ProviderResourceID: batchID}}
	ack, err := value.engine.EnqueueTerminal(bounded, evidence)
	if err != nil {
		if errors.Is(err, quotaplex.ErrConflict) {
			return quotaStatus(409, "QUOTA_BATCH_SETTLEMENT_CONFLICT", "batch already settled with different usage")
		}
		return quotaStatus(503, "QUOTA_USAGE_UNACKNOWLEDGED", "batch usage could not be durably acknowledged")
	}
	_, _ = value.engine.ProcessTerminal(bounded, ack.TenantID, ack.EventID)
	return nil
}

// batchBinding finds the batch-usage binding that admitted an attempt: same
// tenant, surface and operation, and exactly the attempt's bound meters. All
// candidates must agree on the native mapping, otherwise nothing settles.
func (h *QuotaHost) batchBinding(tenant string, info quotaplex.AttemptInfo) (QuotaBinding, error) {
	var found *QuotaBinding
	for _, candidate := range h.bindings {
		if candidate.TenantID != tenant || candidate.Profile != quotaProfileBatch || candidate.ResourceID != info.Demand.Target.ResourceID || candidate.OperationID != info.Demand.Target.Operation || len(candidate.Meters) != len(info.Demand.Bounds) {
			continue
		}
		match := true
		for _, meter := range candidate.Meters {
			if _, bound := info.Demand.Bounds[meter]; !bound {
				match = false
			}
		}
		if !match {
			continue
		}
		if found != nil && !reflect.DeepEqual(found.Meters, candidate.Meters) {
			return QuotaBinding{}, quotaStatus(503, "QUOTA_BINDING_AMBIGUOUS", "batch binding is ambiguous")
		}
		copy := candidate
		found = &copy
	}
	if found == nil {
		return QuotaBinding{}, errBatchNotQuotaMetered
	}
	return *found, nil
}
