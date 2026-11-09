package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

const quotaProfileRequests = "request-count"
const quotaProfileGemini = "gemini-text"

func quotaDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func quotaBindingKey(tenant, plane, resource, operation, model string) string {
	digest, _ := quotaDigest([]string{tenant, plane, resource, operation, model})
	return digest
}
func quotaCloneBinding(in QuotaBinding) QuotaBinding {
	meters := map[string]string{}
	for key, value := range in.Meters {
		meters[key] = value
	}
	limits := map[string]int64{}
	for key, value := range in.Limits {
		limits[key] = value
	}
	in.Meters, in.Limits = meters, limits
	return in
}
func quotaText(value string, required bool) bool {
	if value == "" {
		return !required
	}
	if len(value) > 1024 || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
func quotaInterfacePresent(value any) bool {
	if value == nil {
		return false
	}
	r := reflect.ValueOf(value)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return !r.IsNil()
	}
	return true
}

// NewQuotaHost validates every item before explicit operator activation. Its
// persistent host configuration head prevents stale bindings across replicas.
func NewQuotaHost(ctx context.Context, cfg QuotaHostConfig, store quotaplex.Store, clock func() time.Time, actor QuotaActorResolver, bounds TrustedBoundsAuthority, options ...QuotaHostOption) (*QuotaHost, error) {
	if ctx == nil || !quotaInterfacePresent(store) || len(cfg.Tenants) == 0 || len(cfg.Tenants) > 64 || len(cfg.Bindings) == 0 || len(cfg.Bindings) > 1024 {
		return nil, fmt.Errorf("quota host needs bounded tenants, bindings and transactional storage")
	}
	if clock == nil {
		clock = time.Now
	}
	if cfg.MaxMembershipAge == 0 {
		cfg.MaxMembershipAge = 5 * time.Minute
	}
	if cfg.MaxMembershipAge < time.Second || cfg.MaxMembershipAge > 24*time.Hour {
		return nil, fmt.Errorf("quota membership age must be between one second and 24 hours")
	}
	h := &QuotaHost{tenants: map[string]quotaTenant{}, bindings: map[string]QuotaBinding{}, clock: clock, actor: actor, bounds: bounds, maxMembershipAge: cfg.MaxMembershipAge, store: store, configurationDigests: map[string]string{}}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("quota host option is nil")
		}
		if err := option(h); err != nil {
			return nil, err
		}
	}
	for _, tenant := range cfg.Tenants {
		compiled, err := quotaplex.Compile(tenant.Policy)
		if err != nil {
			return nil, err
		}
		id := compiled.Policy().TenantID
		if _, exists := h.tenants[id]; exists {
			return nil, fmt.Errorf("duplicate quota tenant")
		}
		engine, err := quotaplex.NewEngine(compiled, store, clock)
		if err != nil {
			return nil, err
		}
		h.tenants[id] = quotaTenant{engine: engine, policy: compiled}
	}
	seenIDs := map[string]bool{}
	for _, input := range cfg.Bindings {
		binding := quotaCloneBinding(input)
		for _, value := range []string{binding.ID, binding.TenantID, binding.ResourceID, binding.OperationID, binding.Provider} {
			if !quotaText(value, true) {
				return nil, fmt.Errorf("quota binding requires bounded exact coordinates")
			}
		}
		for _, value := range []string{binding.Model, binding.FundingAccountID, binding.PoolID, binding.BillingAccount, binding.Region, binding.ModelVersion} {
			if !quotaText(value, false) {
				return nil, fmt.Errorf("quota binding has invalid metadata")
			}
		}
		for _, value := range []string{binding.ResourceID, binding.OperationID, binding.Model} {
			if strings.Contains(value, "*") {
				return nil, fmt.Errorf("quota bindings require exact selectors, not wildcard patterns")
			}
		}
		if binding.Plane != "llmplex" && binding.Plane != "mcplex" && binding.Plane != "a2aplex" {
			return nil, fmt.Errorf("quota binding plane is unsupported")
		}
		idKey := binding.TenantID + "\x00" + binding.ID
		if seenIDs[idKey] {
			return nil, fmt.Errorf("duplicate quota binding identity")
		}
		seenIDs[idKey] = true
		tenant, ok := h.tenants[binding.TenantID]
		if !ok {
			return nil, fmt.Errorf("quota binding references an unconfigured tenant")
		}
		if binding.Profile != quotaProfileRequests && !quotaStrictProfile(binding.Profile) {
			return nil, fmt.Errorf("quota binding profile is unsupported")
		}
		if quotaStrictProfile(binding.Profile) && (!quotaInterfacePresent(bounds) || binding.Plane != "llmplex" || binding.Model == "" || binding.ModelVersion == "") {
			return nil, fmt.Errorf("Gemini quota profile needs a trusted bounds authority and exact model version")
		}
		if quotaStrictProfile(binding.Profile) && !quotaProfileOperation(binding) {
			return nil, fmt.Errorf("token quota profile does not support this native operation")
		}
		if len(binding.Limits) > 0 {
			return nil, fmt.Errorf("binding limits are not an exposure authority; configure provider-limit evidence")
		}
		if len(binding.Meters) == 0 || len(binding.Meters) > 8 || binding.Meters["requests"] == "" {
			return nil, fmt.Errorf("quota binding requires a request meter and bounded mappings")
		}
		policy := tenant.policy.Policy()
		seenMeters := map[string]bool{}
		for native, meterID := range binding.Meters {
			unit := ""
			switch native {
			case "requests":
				unit = "requests"
			case "tool_calls":
				if binding.Plane == "mcplex" {
					unit = "calls"
				}
			case "uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens":
				if quotaStrictProfile(binding.Profile) {
					for _, required := range quotaProfileCounters(binding.Profile) {
						if native == required {
							unit = "tokens"
						}
					}
				}
			}
			meter, known := policy.Meters[meterID]
			if unit == "" || !known || meter.Unit != unit || seenMeters[meterID] {
				return nil, fmt.Errorf("quota binding has incompatible or overlapping meter mappings")
			}
			seenMeters[meterID] = true
		}
		if quotaStrictProfile(binding.Profile) {
			for _, native := range quotaProfileCounters(binding.Profile) {
				if binding.Meters[native] == "" {
					return nil, fmt.Errorf("Gemini profile requires complete disjoint token mappings")
				}
			}
		}
		if binding.FundingAccountID != "" {
			found := false
			for _, account := range policy.Accounts {
				if account.ID == binding.FundingAccountID {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("quota binding names an unknown funding account")
			}
		}
		key := quotaBindingKey(binding.TenantID, binding.Plane, binding.ResourceID, binding.OperationID, binding.Model)
		if _, exists := h.bindings[key]; exists {
			return nil, fmt.Errorf("duplicate quota operation binding")
		}
		h.bindings[key] = binding
	}
	if err := h.configureHeads(ctx, cfg); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *QuotaHost) Engines() map[string]*quotaplex.Engine {
	out := map[string]*quotaplex.Engine{}
	if h != nil {
		for tenant, value := range h.tenants {
			out[tenant] = value.engine
		}
	}
	return out
}
func (h *QuotaHost) Ready(ctx context.Context) error {
	if h == nil {
		return fmt.Errorf("quota host unavailable")
	}
	ids := make([]string, 0, len(h.tenants))
	for id := range h.tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := h.assertActive(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func quotaStatus(status int, code, message string) error {
	return &execution.StatusError{Status: status, Code: code, Message: message}
}

func (h *QuotaHost) subjects(ctx context.Context, r *http.Request, tenant string, now time.Time) (quotaplex.Subject, *quotaplex.Subject, error) {
	identity := GetWIFIdentity(r.Context())
	if identity == nil {
		return quotaplex.Subject{}, nil, quotaStatus(401, "QUOTA_IDENTITY_REQUIRED", "verified quota identity required")
	}
	if h.membership != nil {
		principal, err := h.membership.ResolveQuotaSubject(ctx, tenant, identity, "principal", now)
		now = h.clock().UTC()
		kind := "agent"
		if identity.IsWorkforce {
			kind = "human"
		}
		if err != nil || !quotaResolvedSubjectValid(principal, tenant, identity.PrincipalID, kind, now, h.maxMembershipAge) {
			return quotaplex.Subject{}, nil, quotaStatus(503, "QUOTA_MEMBERSHIP_UNAVAILABLE", "authoritative principal membership unavailable")
		}
		quotaClampSubjectExpiry(&principal, h.maxMembershipAge)
		if identity.ActorID == "" {
			return principal, nil, nil
		}
		actor, err := h.membership.ResolveQuotaSubject(ctx, tenant, identity, "actor", now)
		now = h.clock().UTC()
		if err != nil || !quotaResolvedSubjectValid(principal, tenant, identity.PrincipalID, kind, now, h.maxMembershipAge) || !quotaResolvedSubjectValid(actor, tenant, identity.ActorID, "agent", now, h.maxMembershipAge) {
			return quotaplex.Subject{}, nil, quotaStatus(503, "QUOTA_ACTOR_MEMBERSHIP_UNAVAILABLE", "independent agent membership unavailable")
		}
		quotaClampSubjectExpiry(&actor, h.maxMembershipAge)
		return principal, &actor, nil
	}
	snapshot, err := auth.PrincipalMembership(identity, tenant, now)
	if err != nil {
		return quotaplex.Subject{}, nil, quotaStatus(503, "QUOTA_MEMBERSHIP_UNAVAILABLE", "authoritative principal membership unavailable")
	}
	source := auth.MembershipSource(identity.Provider, identity.PoolID)
	groups := make([]quotaplex.GroupRef, 0, len(snapshot.Groups))
	for _, qualified := range snapshot.Groups {
		prefix := source + ":"
		if !strings.HasPrefix(qualified, prefix) || strings.TrimPrefix(qualified, prefix) == "" {
			return quotaplex.Subject{}, nil, quotaStatus(503, "QUOTA_MEMBERSHIP_UNAVAILABLE", "principal group source is unavailable")
		}
		groups = append(groups, quotaplex.GroupRef{Source: source, ID: strings.TrimPrefix(qualified, prefix)})
	}
	expires := snapshot.ExpiresAt
	maxExpiry := snapshot.IssuedAt.Add(h.maxMembershipAge)
	if maxExpiry.Before(expires) {
		expires = maxExpiry
	}
	if !expires.After(now) {
		return quotaplex.Subject{}, nil, quotaStatus(503, "QUOTA_MEMBERSHIP_STALE", "principal membership requires refresh")
	}
	kind := "agent"
	if identity.IsWorkforce {
		kind = "human"
	}
	principal := quotaplex.Subject{ID: identity.PrincipalID, Kind: kind, Membership: quotaplex.Membership{Ref: snapshot.Ref, TenantID: tenant, SubjectID: identity.PrincipalID, Groups: groups, IssuedAt: snapshot.IssuedAt, ExpiresAt: expires}}
	if identity.ActorID == "" {
		return principal, nil, nil
	}
	if !quotaInterfacePresent(h.actor) {
		return quotaplex.Subject{}, nil, quotaStatus(503, "QUOTA_ACTOR_MEMBERSHIP_UNAVAILABLE", "independent agent membership required")
	}
	actor, err := h.actor.ResolveActor(ctx, tenant, identity.ActorID, now)
	if err != nil || actor.ID != identity.ActorID || actor.Kind != "agent" || actor.Membership.TenantID != tenant || actor.Membership.SubjectID != actor.ID || actor.Membership.IssuedAt.IsZero() || actor.Membership.IssuedAt.After(now) || !actor.Membership.ExpiresAt.After(now) {
		return quotaplex.Subject{}, nil, quotaStatus(503, "QUOTA_ACTOR_MEMBERSHIP_UNAVAILABLE", "independent agent membership unavailable")
	}
	actorExpiry := actor.Membership.IssuedAt.Add(h.maxMembershipAge)
	if actorExpiry.Before(actor.Membership.ExpiresAt) {
		actor.Membership.ExpiresAt = actorExpiry
	}
	if !actor.Membership.ExpiresAt.After(now) {
		return quotaplex.Subject{}, nil, quotaStatus(503, "QUOTA_ACTOR_MEMBERSHIP_STALE", "agent membership requires refresh")
	}
	return principal, &actor, nil
}

func quotaHeader(headers http.Header, key string) (string, error) {
	values := headers.Values(key)
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || len(values[0]) > 256 || !quotaText(values[0], true) {
		return "", quotaStatus(400, "QUOTA_INVALID_HEADER", "invalid quota request metadata")
	}
	return values[0], nil
}
func quotaAttempt(request contracts.RequestContext, operation string, headers http.Header) (string, string, error) {
	key, err := quotaHeader(headers, "Idempotency-Key")
	if err != nil {
		return "", "", err
	}
	if key == "" {
		var random [32]byte
		if _, err = rand.Read(random[:]); err != nil {
			return "", "", err
		}
		key = hex.EncodeToString(random[:])
	}
	digest, err := quotaDigest([]string{request.TenantID, request.PrincipalID, request.ActorID, request.ApplicationID, request.ResourceID, operation, key})
	if err != nil {
		return "", "", err
	}
	logicalKey, err := quotaHeader(headers, "X-Aiplex-Operation-Id")
	if err != nil {
		return "", "", err
	}
	logicalDigest := digest
	if logicalKey != "" {
		logicalDigest, err = quotaDigest([]string{request.TenantID, request.PrincipalID, request.ActorID, request.ApplicationID, logicalKey})
		if err != nil {
			return "", "", err
		}
	}
	return "attempt-" + digest, "operation-" + logicalDigest, nil
}
func quotaAdmissionError(err error) error {
	var denied *quotaplex.AdmissionDenied
	if errors.As(err, &denied) {
		status := 429
		if strings.HasPrefix(denied.Decision.Reason, "funding_") {
			status = 403
		}
		return &execution.StatusError{Status: status, Code: "QUOTA_" + strings.ToUpper(denied.Decision.Reason), Message: "quota admission denied", RetryAfter: denied.Decision.RetryAfter}
	}
	if errors.Is(err, quotaplex.ErrConflict) || errors.Is(err, quotaplex.ErrInvalidTransition) || errors.Is(err, quotaplex.ErrDispatchClaimed) {
		return quotaStatus(409, "QUOTA_ATTEMPT_CONFLICT", "quota attempt cannot be repeated with these facts")
	}
	return quotaStatus(503, "QUOTA_UNAVAILABLE", "quota admission unavailable")
}

func (h *QuotaHost) prepare(ctx context.Context, r *http.Request, in QuotaInvocation) (execution.Lease, error) {
	if h == nil || r == nil {
		return nil, quotaStatus(503, "QUOTA_UNAVAILABLE", "quota host unavailable")
	}
	// Re-proving exposure at dispatch must use the same native bytes admitted
	// here, even if an embedding reuses its request buffers or header map.
	in.Body = append([]byte(nil), in.Body...)
	in.Headers = in.Headers.Clone()
	if err := in.Context.Validate(); err != nil {
		return nil, quotaStatus(401, "QUOTA_IDENTITY_REQUIRED", "verified execution context required")
	}
	tenant, ok := h.tenants[in.Context.TenantID]
	if !ok {
		return nil, quotaStatus(503, "QUOTA_TENANT_UNAVAILABLE", "quota tenant unavailable")
	}
	key := quotaBindingKey(in.Context.TenantID, in.Plane, in.ResourceID, in.OperationID, in.Model)
	binding, ok := h.bindings[key]
	if !ok {
		return nil, quotaStatus(503, "QUOTA_COVERAGE_UNAVAILABLE", "operation has no protected quota binding")
	}
	if err := h.assertBindingActive(ctx, in.Context.TenantID, binding.ID); err != nil {
		return nil, quotaStatus(503, "QUOTA_CONFIGURATION_INACTIVE", "quota binding is inactive or quarantined")
	}
	if in.Reauthorize == nil || in.Context.ResourceID != in.ResourceID {
		return nil, quotaStatus(503, "QUOTA_BINDING_UNAVAILABLE", "trusted dispatch binding unavailable")
	}
	identity := GetWIFIdentity(r.Context())
	if identity == nil || identity.PrincipalID != in.Context.PrincipalID || identity.ActorID != in.Context.ActorID {
		return nil, quotaStatus(401, "QUOTA_IDENTITY_REQUIRED", "execution identity does not match verified caller")
	}
	now := h.clock().UTC()
	principal, actor, err := h.subjects(ctx, r, in.Context.TenantID, now)
	if err != nil {
		return nil, err
	}
	request := in.Context
	request.MembershipSnapshotRef = &principal.Membership.Ref
	request.ApplicationID = identity.AuthorizedParty
	request.Purpose = "native-execution"
	requestedFunding, err := quotaHeader(in.Headers, "X-Aiplex-Funding-Account")
	if err != nil {
		return nil, err
	}
	if binding.FundingAccountID != "" && requestedFunding != "" && requestedFunding != binding.FundingAccountID {
		return nil, quotaStatus(403, "QUOTA_FUNDING_DENIED", "funding account conflicts with operation policy")
	}
	if requestedFunding == "" {
		requestedFunding = binding.FundingAccountID
	}
	attempt, logical, err := quotaAttempt(request, in.OperationID, in.Headers)
	if err != nil {
		return nil, err
	}
	target := in.Target
	if target.Provider == "" {
		target.Provider = binding.Provider
	}
	if target.Provider != binding.Provider || (target.ResourceID != "" && target.ResourceID != in.ResourceID) || (target.Operation != "" && target.Operation != in.OperationID) {
		return nil, quotaStatus(503, "QUOTA_TARGET_UNAVAILABLE", "resolved quota target disagrees with binding")
	}
	target.Plane, target.ResourceID, target.Operation = in.Plane, in.ResourceID, in.OperationID
	for _, pair := range [][2]string{{binding.BillingAccount, target.BillingAccount}, {binding.Region, target.Region}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return nil, quotaStatus(503, "QUOTA_TARGET_UNAVAILABLE", "resolved provider authority disagrees with binding")
		}
	}
	target.PoolID, target.ModelVersion = binding.PoolID, binding.ModelVersion
	configuration := struct {
		Binding       QuotaBinding
		Configuration any
		Target        quotaplex.Target
		MembershipAge time.Duration
	}{binding, in.Configuration, target, h.maxMembershipAge}
	baseDigest, err := quotaDigest(configuration)
	if err != nil {
		return nil, quotaStatus(503, "QUOTA_CONFIGURATION_UNAVAILABLE", "quota configuration cannot be pinned")
	}
	target.ConfigurationDigest = baseDigest
	nativeBounds, proof, err := h.nativeBounds(ctx, binding, target, in, now)
	if err != nil {
		return nil, err
	}
	if proof != nil {
		target.ConfigurationDigest, err = quotaDigest(struct {
			Base  string
			Proof QuotaBoundsProof
		}{baseDigest, *proof})
		if err != nil {
			return nil, err
		}
	}
	bounds := quotaplex.Vector{}
	for native, meter := range binding.Meters {
		quantity, present := nativeBounds[native]
		if !present || quantity < 0 {
			return nil, quotaStatus(503, "QUOTA_BOUNDS_UNAVAILABLE", "trusted quota exposure is incomplete")
		}
		bounds[meter] = quantity
	}
	payloadDigest, err := quotaDigest(struct {
		Method, Path, Query string
		Body                []byte
	}{in.Method, in.Path, in.RawQuery, in.Body})
	if err != nil {
		return nil, err
	}
	demand := quotaplex.Demand{Version: quotaplex.Version, Context: request, Principal: principal, Actor: actor, LogicalOperationID: logical, AttemptID: attempt, PayloadDigest: payloadDigest, Target: target, FundingAccountID: requestedFunding, Bounds: bounds, RequireModelVersion: quotaStrictProfile(binding.Profile)}
	authorize := func(callCtx context.Context, pinned quotaplex.Demand) error {
		if err := h.assertBindingActive(callCtx, request.TenantID, binding.ID); err != nil {
			return quotaStatus(503, "QUOTA_CONFIGURATION_INACTIVE", "quota host configuration changed")
		}
		if h.admission != nil {
			if err := h.admission.Recheck(callCtx, tenant.engine, pinned); err != nil {
				return quotaStatus(503, "QUOTA_SCHEDULER_INACTIVE", "assigned capacity or scheduling policy changed before dispatch")
			}
		}
		if err := in.Reauthorize(callCtx); err != nil {
			return quotaStatus(403, "QUOTA_EXECUTION_REVOKED", "execution authority or configuration changed")
		}
		freshPrincipal, freshActor, err := h.subjects(callCtx, r, request.TenantID, h.clock().UTC())
		if err != nil {
			return err
		}
		sameMembership := reflect.DeepEqual(freshPrincipal, pinned.Principal) && reflect.DeepEqual(freshActor, pinned.Actor)
		if h.membership != nil {
			sameMembership = quotaSameSubjectEligibility(freshPrincipal, pinned.Principal) && quotaSameActorEligibility(freshActor, pinned.Actor)
		}
		if !sameMembership {
			return quotaStatus(409, "QUOTA_MEMBERSHIP_CHANGED", "execution membership changed after admission")
		}
		if proof != nil {
			baseTarget := target
			baseTarget.ConfigurationDigest = baseDigest
			freshBounds, freshProof, err := h.nativeBounds(callCtx, binding, baseTarget, in, h.clock().UTC())
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(freshBounds, nativeBounds) || !reflect.DeepEqual(freshProof, proof) {
				return quotaStatus(409, "QUOTA_BOUNDS_CHANGED", "provider exposure authority changed after admission")
			}
		}
		return nil
	}
	admit := tenant.engine.Admit
	if h.admission != nil {
		admit = func(callCtx context.Context, admitted quotaplex.Demand) (quotaplex.Decision, error) {
			return h.admission.Admit(callCtx, tenant.engine, admitted)
		}
	}
	bound, err := tenant.engine.BindWithAdmission(demand, authorize, admit)
	if err != nil {
		return nil, quotaAdmissionError(err)
	}
	pipeline, err := enforcement.New([]string{"quota"}, map[string]enforcement.Provider{"quota": bound})
	if err != nil {
		return nil, quotaAdmissionError(err)
	}
	ticket, err := pipeline.Admit(ctx, demand.Context)
	if err != nil {
		return nil, quotaAdmissionError(err)
	}
	return newQuotaHostLease(h, tenant.engine, bound, ticket, demand, binding, proof), nil
}
