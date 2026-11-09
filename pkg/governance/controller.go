package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type registeredDriver struct {
	DriverRegistration
	operations map[string]nativeapi.OperationInfo
	validator  nativeapi.Validator
}
type Controller struct {
	store                        resources.Store
	drivers                      map[string]registeredDriver
	authorize                    Authorizer
	approvals                    ApprovalVerifier
	allowApply                   bool
	ttl, observationAge, timeout time.Duration
	now                          func() time.Time
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Interface, reflect.Func, reflect.Slice:
		return v.IsNil()
	}
	return false
}
func NewController(cfg Config) (*Controller, error) {
	if nilInterface(cfg.Store) || nilInterface(cfg.Authorize) || len(cfg.Drivers) == 0 {
		return nil, fmt.Errorf("governance store, authorizer and native drivers required")
	}
	if cfg.AllowApply && nilInterface(cfg.Approvals) {
		return nil, fmt.Errorf("apply requires explicit approval verification")
	}
	if cfg.MaxPlanTTL == 0 {
		cfg.MaxPlanTTL = 15 * time.Minute
	}
	if cfg.MaxPlanTTL < time.Second || cfg.MaxPlanTTL > time.Hour {
		return nil, fmt.Errorf("invalid maximum plan TTL")
	}
	if cfg.MaxObservationAge == 0 {
		cfg.MaxObservationAge = 5 * time.Minute
	}
	if cfg.MaxObservationAge < time.Second || cfg.MaxObservationAge > time.Hour {
		return nil, fmt.Errorf("invalid observation freshness bound")
	}
	if cfg.OperationTimeout == 0 {
		cfg.OperationTimeout = 30 * time.Second
	}
	if cfg.OperationTimeout < time.Millisecond || cfg.OperationTimeout > 5*time.Minute {
		return nil, fmt.Errorf("invalid native operation timeout")
	}
	c := &Controller{store: cfg.Store, drivers: map[string]registeredDriver{}, authorize: cfg.Authorize, approvals: cfg.Approvals, allowApply: cfg.AllowApply, ttl: cfg.MaxPlanTTL, observationAge: cfg.MaxObservationAge, timeout: cfg.OperationTimeout, now: time.Now}
	for id, d := range cfg.Drivers {
		if !identifier.MatchString(id) || !boundedIdentity(d.TenantID) || !fingerprint.MatchString(d.ProviderRevision) || nilInterface(d.Driver) {
			return nil, fmt.Errorf("native driver requires id, app tenant and frozen provider fingerprint")
		}
		validator, ok := d.Driver.(nativeapi.Validator)
		if !ok || nilInterface(validator) {
			return nil, fmt.Errorf("native driver must implement pure operation validation")
		}
		d.ETagReads = maps.Clone(d.ETagReads)
		registered := registeredDriver{DriverRegistration: d, operations: map[string]nativeapi.OperationInfo{}, validator: validator}
		for _, op := range d.Driver.Operations() {
			if !identifier.MatchString(op.Name) {
				return nil, fmt.Errorf("invalid native operation name")
			}
			if _, ok := registered.operations[op.Name]; ok {
				return nil, fmt.Errorf("duplicate native operation")
			}
			registered.operations[op.Name] = op
		}
		if len(registered.operations) == 0 {
			return nil, fmt.Errorf("driver exposes no native operations")
		}
		for write, read := range d.ETagReads {
			w, wok := registered.operations[write]
			r, rok := registered.operations[read]
			if !wok || !rok || !w.Mutating || r.Mutating || w.Credential || r.Credential {
				return nil, fmt.Errorf("ETag proof requires explicit mutation/read operation pair")
			}
		}
		c.drivers[id] = registered
	}
	return c, nil
}
func (c *Controller) ValidateBinding(binding AgentBinding) error {
	if e := binding.Validate(); e != nil {
		return e
	}
	driver, ok := c.drivers[binding.DriverRef]
	if !ok || driver.TenantID != binding.TenantID {
		return fmt.Errorf("binding driver is not configured for this tenant")
	}
	for _, g := range binding.Allowed {
		info, ok := driver.operations[g.Operation]
		if !ok || info.Credential {
			return fmt.Errorf("unknown or credential operation is not allowed in governance bindings")
		}
	}
	for _, d := range binding.Desired {
		if e := c.validateNative(driver, binding, d.Operation); e != nil {
			return e
		}
	}
	return nil
}
func (c *Controller) validateNative(driver registeredDriver, b AgentBinding, op nativeapi.Operation) error {
	if e := validateOperation(op); e != nil {
		return e
	}
	if !allowed(b, op) {
		return ErrDenied
	}
	info, ok := driver.operations[op.Name]
	if !ok || info.Credential {
		return ErrDenied
	}
	if e := driver.validator.ValidateOperation(cloneOperation(op)); e != nil {
		return fmt.Errorf("provider operation validation failed")
	}
	return nil
}
func cloneOperation(o nativeapi.Operation) nativeapi.Operation {
	o.Payload = append(json.RawMessage(nil), o.Payload...)
	return o
}
func (c *Controller) binding(ctx context.Context, request contracts.RequestContext, id string, revision uint64) (AgentBinding, resources.Record, registeredDriver, error) {
	if e := request.Validate(); e != nil {
		return AgentBinding{}, resources.Record{}, registeredDriver{}, e
	}
	if request.ResourceID != id || revision == 0 {
		return AgentBinding{}, resources.Record{}, registeredDriver{}, ErrDenied
	}
	record, e := c.store.Get(ctx, request.TenantID, BindingKind, id)
	if e != nil {
		return AgentBinding{}, record, registeredDriver{}, e
	}
	if record.Revision != revision {
		return AgentBinding{}, record, registeredDriver{}, ErrStale
	}
	var b AgentBinding
	if e = json.Unmarshal(record.Spec, &b); e != nil {
		return b, record, registeredDriver{}, fmt.Errorf("invalid saved agent binding")
	}
	if b.ID != id || b.TenantID != request.TenantID {
		return b, record, registeredDriver{}, ErrDenied
	}
	if e = c.ValidateBinding(b); e != nil {
		return b, record, registeredDriver{}, e
	}
	return b, record, c.drivers[b.DriverRef], nil
}
func (c *Controller) authorizeAction(ctx context.Context, phase string, request contracts.RequestContext, b AgentBinding, revision uint64, op nativeapi.Operation, digest string) error {
	if e := c.authorize.Authorize(ctx, Action{phase, request, b.ID, revision, b.DriverRef, op.Name, op.Resource, digest}); e != nil {
		return ErrDenied
	}
	return nil
}

type sealedOperation struct {
	Name, Resource, ETag string
	Payload              []byte
}
type savedPlan struct {
	Plan      Plan            `json:"plan"`
	Operation sealedOperation `json:"operation"`
}

func sealed(o nativeapi.Operation) sealedOperation {
	return sealedOperation{o.Name, o.Resource, o.ETag, append([]byte(nil), o.Payload...)}
}
func (s sealedOperation) native() nativeapi.Operation {
	return nativeapi.Operation{Name: s.Name, Resource: s.Resource, ETag: s.ETag, Payload: append(json.RawMessage(nil), s.Payload...)}
}
func (c *Controller) Plan(ctx context.Context, request PlanRequest) (Plan, error) {
	b, record, driver, e := c.binding(ctx, request.Context, request.BindingID, request.BindingRevision)
	if e != nil {
		return Plan{}, e
	}
	var op nativeapi.Operation
	found := false
	for _, desired := range b.Desired {
		if desired.ID == request.DesiredID {
			op = cloneOperation(desired.Operation)
			found = true
			break
		}
	}
	if !found {
		return Plan{}, fmt.Errorf("desired operation is not saved in binding")
	}
	info := driver.operations[op.Name]
	if !info.Mutating || info.Credential {
		return Plan{}, ErrDenied
	}
	if e = c.validateNative(driver, b, op); e != nil {
		return Plan{}, e
	}
	if e = c.authorizeAction(ctx, "plan", request.Context, b, record.Revision, op, ""); e != nil {
		return Plan{}, e
	}
	if e = c.requireETag(ctx, request.Context, b, driver, op); e != nil {
		return Plan{}, e
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = c.ttl
	}
	if ttl < time.Second || ttl > c.ttl {
		return Plan{}, fmt.Errorf("plan TTL exceeds configured bound")
	}
	now := c.now().UTC()
	plan := Plan{Version: "v1", TenantID: b.TenantID, PrincipalID: request.Context.PrincipalID, ActorID: request.Context.ActorID, BindingID: b.ID, BindingRevision: record.Revision, BindingDigest: hashBytes(record.Spec), DesiredID: request.DesiredID, DriverRef: b.DriverRef, ProviderRevision: driver.ProviderRevision, Operation: op.Name, Resource: op.Resource, PayloadHash: hashBytes(op.Payload), ETag: op.ETag}
	// One exact desired action per binding revision/caller/provider snapshot has
	// one stable plan identity. Replanning never creates an accidental second write.
	plan.ID = hashObject(plan)
	if _, e = c.store.Get(ctx, b.TenantID, ReceiptKind, plan.ID); e == nil {
		return Plan{}, ErrAlreadyAttempted
	} else if !errors.Is(e, resources.ErrNotFound) {
		return Plan{}, e
	}
	expected := uint64(0)
	prior, e := c.store.Get(ctx, b.TenantID, PlanKind, plan.ID)
	if e == nil {
		var old savedPlan
		if json.Unmarshal(prior.Spec, &old) != nil {
			return Plan{}, fmt.Errorf("invalid saved plan")
		}
		if old.Plan.ExpiresAt.After(now) && old.Plan.Digest == planDigest(old.Plan) {
			old.Plan.Revision = prior.Revision
			return old.Plan, nil
		}
		expected = prior.Revision
	} else if !errors.Is(e, resources.ErrNotFound) {
		return Plan{}, e
	}
	plan.CreatedAt = now
	plan.ExpiresAt = now.Add(ttl)
	plan.Digest = planDigest(plan)
	wire, _ := json.Marshal(savedPlan{plan, sealed(op)})
	saved, e := c.store.Put(ctx, resources.Record{TenantID: b.TenantID, Kind: PlanKind, ID: plan.ID, Spec: wire}, expected)
	if e != nil {
		return Plan{}, e
	}
	plan.Revision = saved.Revision
	return plan, nil
}
func (c *Controller) Apply(ctx context.Context, request ApplyRequest) (Receipt, error) {
	if !c.allowApply {
		return Receipt{}, ErrApplyDisabled
	}
	if e := request.Context.Validate(); e != nil {
		return Receipt{}, e
	}
	if request.PlanID == "" || request.PlanRevision == 0 || request.ApprovalRef == "" {
		return Receipt{}, ErrDenied
	}
	record, e := c.store.Get(ctx, request.Context.TenantID, PlanKind, request.PlanID)
	if e != nil {
		return Receipt{}, e
	}
	if record.Revision != request.PlanRevision {
		return Receipt{}, ErrStale
	}
	var saved savedPlan
	if json.Unmarshal(record.Spec, &saved) != nil {
		return Receipt{}, fmt.Errorf("invalid saved plan")
	}
	plan := saved.Plan
	op := saved.Operation.native()
	if e = c.validateSavedPlan(request, plan, op); e != nil {
		return Receipt{}, e
	}
	b, bindingRecord, driver, e := c.binding(ctx, request.Context, plan.BindingID, plan.BindingRevision)
	if e != nil {
		return Receipt{}, e
	}
	if !driver.AllowApply {
		return Receipt{}, ErrApplyDisabled
	}
	if hashBytes(bindingRecord.Spec) != plan.BindingDigest || driver.ProviderRevision != plan.ProviderRevision {
		return Receipt{}, ErrStale
	}
	if e = c.validateNative(driver, b, op); e != nil {
		return Receipt{}, e
	}
	if !driver.operations[op.Name].Mutating {
		return Receipt{}, ErrDenied
	}
	if e = c.authorizeAction(ctx, "apply", request.Context, b, bindingRecord.Revision, op, plan.Digest); e != nil {
		return Receipt{}, e
	}
	if e = c.requireETag(ctx, request.Context, b, driver, op); e != nil {
		return Receipt{}, e
	}
	approval := Approval{request.ApprovalRef, plan.TenantID, plan.PrincipalID, plan.ActorID, plan.BindingID, plan.ID, plan.Digest, record.Revision}
	if e = c.approvals.Verify(ctx, approval); e != nil {
		return Receipt{}, ErrDenied
	}
	receipt := Receipt{Version: "v1", PlanID: plan.ID, PlanRevision: record.Revision, PlanDigest: plan.Digest, TenantID: plan.TenantID, PrincipalID: plan.PrincipalID, ActorID: plan.ActorID, BindingID: plan.BindingID, State: "pending", StartedAt: c.now().UTC(), Readiness: "unverified"}
	pendingPayload, _ := json.Marshal(receipt)
	pending, e := c.store.Put(ctx, resources.Record{TenantID: plan.TenantID, Kind: ReceiptKind, ID: plan.ID, Spec: pendingPayload}, 0)
	if errors.Is(e, resources.ErrConflict) {
		old, e := c.store.Get(ctx, plan.TenantID, ReceiptKind, plan.ID)
		if e != nil {
			return Receipt{}, e
		}
		var prior Receipt
		if json.Unmarshal(old.Spec, &prior) != nil || prior.PlanDigest != plan.Digest || prior.PlanRevision != record.Revision || prior.PrincipalID != plan.PrincipalID || prior.ActorID != plan.ActorID {
			return Receipt{}, ErrStale
		}
		return prior, nil
	}
	if e != nil {
		return Receipt{}, e
	}
	// A monotonic revision above one means a prior receipt was deleted. Erasing
	// journal history must never re-enable the same exact external action.
	if pending.Revision != 1 {
		receipt.State = "unknown"
		receipt.FinishedAt = c.now().UTC()
		_ = c.finish(ctx, pending, receipt)
		return receipt, ErrAlreadyAttempted
	}
	failBeforeCall := func(cause error) (Receipt, error) {
		receipt.State = "failed"
		receipt.FinishedAt = c.now().UTC()
		if e := c.finish(ctx, pending, receipt); e != nil {
			receipt.State = "unknown"
			return receipt, fmt.Errorf("governance receipt persistence failed")
		}
		return receipt, cause
	}
	// Recheck saved revision, freshness and authorization after approval/CAS claim.
	latest, e := c.store.Get(ctx, plan.TenantID, BindingKind, plan.BindingID)
	if e != nil || latest.Revision != plan.BindingRevision || hashBytes(latest.Spec) != plan.BindingDigest {
		return failBeforeCall(ErrStale)
	}
	if !plan.ExpiresAt.After(c.now()) {
		return failBeforeCall(fmt.Errorf("governance plan expired"))
	}
	if e = c.authorizeAction(ctx, "apply", request.Context, b, bindingRecord.Revision, op, plan.Digest); e != nil {
		return failBeforeCall(e)
	}
	if e = c.validateNative(driver, b, op); e != nil {
		return failBeforeCall(e)
	}
	if e = c.requireETag(ctx, request.Context, b, driver, op); e != nil {
		return failBeforeCall(e)
	}
	if e = ctx.Err(); e != nil {
		return failBeforeCall(fmt.Errorf("native governance request canceled before execution"))
	}
	call, cancel := context.WithTimeout(ctx, c.timeout)
	result, callErr := executeDriver(call, driver.Driver, cloneOperation(op))
	interrupted := call.Err() != nil
	cancel()
	receipt.FinishedAt = c.now().UTC()
	receipt.StatusCode = result.StatusCode
	receipt.ResultDigest = hashBytes(result.Body)
	if len(result.ETag) <= 2048 && !strings.ContainsAny(result.ETag, "\x00\r\n") {
		receipt.ETag = result.ETag
	}
	receipt.State = "unknown"
	if callErr == nil && !interrupted && result.StatusCode >= 200 && result.StatusCode < 300 {
		receipt.State = "succeeded"
	} else if !interrupted && result.StatusCode >= 400 && result.StatusCode < 500 && result.StatusCode != 408 && result.StatusCode != 499 {
		receipt.State = "failed"
	}
	if e = c.finish(ctx, pending, receipt); e != nil {
		receipt.State = "unknown"
		return receipt, fmt.Errorf("native effect may have completed; governance receipt persistence failed")
	}
	if receipt.State != "succeeded" {
		return receipt, fmt.Errorf("native governance outcome %s; inspect receipt before any new action", receipt.State)
	}
	return receipt, nil
}
func executeDriver(ctx context.Context, driver nativeapi.Driver, op nativeapi.Operation) (result nativeapi.Result, err error) {
	defer func() {
		if recover() != nil {
			result = nativeapi.Result{}
			err = fmt.Errorf("native driver interrupted")
		}
	}()
	return driver.Execute(ctx, op)
}
func (c *Controller) validateSavedPlan(request ApplyRequest, p Plan, op nativeapi.Operation) error {
	now := c.now()
	if p.Version != "v1" || p.ID != request.PlanID || p.Digest != planDigest(p) || p.TenantID != request.Context.TenantID || p.PrincipalID != request.Context.PrincipalID || p.ActorID != request.Context.ActorID || p.BindingID != request.Context.ResourceID || p.CreatedAt.IsZero() || p.CreatedAt.After(now) || !p.ExpiresAt.After(now) || p.ExpiresAt.Sub(p.CreatedAt) > c.ttl {
		return ErrDenied
	}
	if p.Operation != op.Name || p.Resource != op.Resource || p.PayloadHash != hashBytes(op.Payload) || p.ETag != op.ETag {
		return ErrStale
	}
	return nil
}
func (c *Controller) finish(ctx context.Context, pending resources.Record, receipt Receipt) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	payload, _ := json.Marshal(receipt)
	_, e := c.store.Put(cleanup, resources.Record{TenantID: receipt.TenantID, Kind: ReceiptKind, ID: receipt.PlanID, Spec: payload}, pending.Revision)
	return e
}
