package workflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// OnRegister implements deploy.KindHook for kind=workflow. The workflow spec
// is carried on the template's config map under the key "spec" (JSON-encoded
// or already-shaped). The deploy engine forwards the cap + attrs; we look
// the spec up off the instance's config.
func (e *Executor) OnRegister(_ context.Context, inst *models.Instance, c capability.Cap, _ capability.Attrs) error {
	spec, err := specFromInstance(inst)
	if err != nil {
		return fmt.Errorf("workflow %s: %w", c.URI, err)
	}
	e.Register(c.URI, spec)
	return nil
}

// OnUnregister removes the workflow's spec from the executor.
func (e *Executor) OnUnregister(_ context.Context, _ *models.Instance, c capability.Cap) error {
	e.Unregister(c.URI)
	return nil
}

// specFromInstance pulls the workflow Spec from the deployed Instance. The
// engine carries config (a free-form map); workflow templates put the spec
// under config["spec"] either as a Spec struct shape (map) or as a JSON
// string.
func specFromInstance(inst *models.Instance) (Spec, error) {
	if inst == nil {
		return Spec{}, fmt.Errorf("nil instance")
	}
	raw, ok := inst.Config["spec"]
	if !ok {
		return Spec{}, fmt.Errorf("instance config missing 'spec' key")
	}

	// Round-trip through JSON so map[string]any → typed Spec works the same
	// way regardless of whether the source was a Go map or a JSON string.
	var data []byte
	switch v := raw.(type) {
	case string:
		data = []byte(v)
	default:
		var err error
		data, err = json.Marshal(v)
		if err != nil {
			return Spec{}, fmt.Errorf("marshal spec: %w", err)
		}
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return Spec{}, fmt.Errorf("unmarshal spec: %w", err)
	}
	if len(spec.Steps) == 0 {
		return Spec{}, fmt.Errorf("workflow spec has no steps")
	}
	return spec, nil
}
