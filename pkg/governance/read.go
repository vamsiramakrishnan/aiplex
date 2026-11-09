package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// Read observes one explicitly granted noncredential native operation. The
// driver cannot run until authorization and pure provider validation succeed.
// The default persisted observation contains metadata only; output requires an
// explicit host projection and is validated again before persistence.
func (c *Controller) Read(ctx context.Context, request ReadRequest) (Observation, error) {
	b, record, driver, err := c.binding(ctx, request.Context, request.BindingID, request.BindingRevision)
	if err != nil {
		return Observation{}, err
	}
	op := cloneOperation(request.Operation)
	if err = c.validateNative(driver, b, op); err != nil {
		return Observation{}, err
	}
	if driver.operations[op.Name].Mutating {
		return Observation{}, ErrDenied
	}
	if err = c.authorizeAction(ctx, "read", request.Context, b, record.Revision, op, ""); err != nil {
		return Observation{}, err
	}
	if ctx.Err() != nil {
		return Observation{}, fmt.Errorf("native inventory request canceled before execution")
	}
	call, cancel := context.WithTimeout(ctx, c.timeout)
	result, callErr := executeDriver(call, driver.Driver, op)
	interrupted := call.Err() != nil
	cancel()
	if callErr != nil || interrupted || result.StatusCode < 200 || result.StatusCode >= 300 {
		return Observation{}, fmt.Errorf("native inventory observation failed")
	}
	if len(result.ETag) > 2048 || strings.ContainsAny(result.ETag, "\x00\r\n") {
		return Observation{}, fmt.Errorf("native inventory returned invalid ETag")
	}
	observation := Observation{Version: "v1", TenantID: b.TenantID, PrincipalID: request.Context.PrincipalID, ActorID: request.Context.ActorID, BindingID: b.ID, BindingRevision: record.Revision, BindingIdentityDigest: bindingIdentityDigest(b), DriverRef: b.DriverRef, ProviderRevision: driver.ProviderRevision, Operation: op.Name, Resource: op.Resource, StatusCode: result.StatusCode, ETag: result.ETag, ResultDigest: hashBytes(result.Body), ObservedAt: c.now().UTC()}
	observation.ID = observationID(request.Context, b, driver, op.Name, op.Resource)
	if driver.InventoryProjection != nil {
		body, err := driver.InventoryProjection(cloneOperation(op), result)
		if err != nil {
			return Observation{}, fmt.Errorf("native inventory projection failed")
		}
		if err = nativeapi.ValidatePublicPayload(body); err != nil {
			return Observation{}, fmt.Errorf("native inventory projection contains unsupported material")
		}
		observation.Body = append(json.RawMessage(nil), body...)
	}
	// Recheck the binding after the read. A removed/changed grant cannot produce
	// a usable ETag proof, even if the original cloud read completed successfully.
	latest, err := c.store.Get(ctx, b.TenantID, BindingKind, b.ID)
	if err != nil || latest.Revision != record.Revision || hashBytes(latest.Spec) != hashBytes(record.Spec) {
		return Observation{}, ErrStale
	}
	expected := uint64(0)
	prior, err := c.store.Get(ctx, b.TenantID, ObservationKind, observation.ID)
	if err == nil {
		expected = prior.Revision
	} else if !errors.Is(err, resources.ErrNotFound) {
		return Observation{}, err
	}
	wire, _ := json.Marshal(observation)
	saved, err := c.store.Put(ctx, resources.Record{TenantID: b.TenantID, Kind: ObservationKind, ID: observation.ID, Spec: wire}, expected)
	if err != nil {
		return Observation{}, err
	}
	observation.Revision = saved.Revision
	return observation, nil
}

func observationID(request contracts.RequestContext, binding AgentBinding, driver registeredDriver, operation, resource string) string {
	return hashObject(struct {
		Tenant, Principal, Actor, Binding, Identity, Driver, Revision, Operation, Resource string
	}{request.TenantID, request.PrincipalID, request.ActorID, binding.ID, bindingIdentityDigest(binding), binding.DriverRef, driver.ProviderRevision, operation, resource})
}

func (c *Controller) requireETag(ctx context.Context, request contracts.RequestContext, binding AgentBinding, driver registeredDriver, op nativeapi.Operation) error {
	read := driver.ETagReads[op.Name]
	if read == "" {
		return nil
	}
	if op.ETag == "" {
		return fmt.Errorf("native mutation requires a freshly observed ETag")
	}
	id := observationID(request, binding, driver, read, op.Resource)
	record, err := c.store.Get(ctx, request.TenantID, ObservationKind, id)
	if err != nil {
		return fmt.Errorf("native mutation requires a freshly observed ETag")
	}
	var observation Observation
	if json.Unmarshal(record.Spec, &observation) != nil || observation.Version != "v1" || observation.ID != id || observation.TenantID != request.TenantID || observation.PrincipalID != request.PrincipalID || observation.ActorID != request.ActorID || observation.BindingID != binding.ID || observation.BindingIdentityDigest != bindingIdentityDigest(binding) || observation.DriverRef != binding.DriverRef || observation.ProviderRevision != driver.ProviderRevision || observation.Operation != read || observation.Resource != op.Resource || observation.ETag != op.ETag || observation.StatusCode < 200 || observation.StatusCode >= 300 || observation.ObservedAt.IsZero() || observation.ObservedAt.After(c.now()) || c.now().Sub(observation.ObservedAt) > c.observationAge {
		return fmt.Errorf("native ETag observation is stale or does not match this action")
	}
	return nil
}
