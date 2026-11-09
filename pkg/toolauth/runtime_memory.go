package toolauth

import (
	"context"
	"sync"
)

// MemorySessionVault is a development/test adapter. Production must inject an
// encrypted durable secret store; resource-store persistence alone is inadequate.
type MemorySessionVault struct {
	mu     sync.Mutex
	values map[string]memorySessionSecret
}
type memorySessionSecret struct {
	Binding string
	Value   SessionSecrets
}

func NewMemorySessionVault() *MemorySessionVault {
	return &MemorySessionVault{values: map[string]memorySessionSecret{}}
}
func (v *MemorySessionVault) Seal(ctx context.Context, binding string, value SessionSecrets) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	id, e := randomValue()
	if e != nil {
		return "", e
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.values[id] = memorySessionSecret{binding, value}
	return id, nil
}
func (v *MemorySessionVault) Open(ctx context.Context, binding, ref string) (SessionSecrets, error) {
	if e := ctx.Err(); e != nil {
		return SessionSecrets{}, e
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	value, ok := v.values[ref]
	if !ok || value.Binding != binding {
		return SessionSecrets{}, runtimeError("secret_not_found")
	}
	return value.Value, nil
}
func (v *MemorySessionVault) Destroy(ctx context.Context, ref string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.values, ref)
	return nil
}
