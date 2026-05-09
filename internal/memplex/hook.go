package memplex

import (
	"context"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// OnRegister implements deploy.KindHook. The deploy engine calls this when
// a kind=memory capability is provisioned. We translate the cap + attrs into
// a Namespace record and bind it to a backend.
func (b *Broker) OnRegister(_ context.Context, _ *models.Instance, c capability.Cap, attrs capability.Attrs) error {
	u, err := capability.ParseURI(c.URI)
	if err != nil {
		return err
	}

	ns := Namespace{
		URI:       u,
		Backend:   attrs.Backend,
		DataClass: attrs.DataClass,
	}
	if attrs.RetentionDays > 0 {
		ns.Retention = time.Duration(attrs.RetentionDays) * 24 * time.Hour
	}
	if attrs.DataClass == "pii" || attrs.DataClass == "regulated" {
		ns.PII = &PIIPolicy{Enabled: true} // operator can extend with rules
	}

	// Resolve named backend → concrete backend. Future: lookup against
	// a backend registry; for now route everything to defaults.
	backend := b.backendFor(attrs.Backend)
	b.Register(ns, backend)
	return nil
}

// OnUnregister tears down the namespace mapping. Backend data is NOT erased
// here — that's a separate explicit `aiplex memory purge` operation so a
// rolling redeploy doesn't lose state.
func (b *Broker) OnUnregister(_ context.Context, _ *models.Instance, c capability.Cap) error {
	b.Unregister(c.URI)
	return nil
}

// backendFor maps a backend name to a concrete MemoryBackend. Returns the
// broker's default if the name is unknown so misconfigured templates still
// get a working namespace in dev.
func (b *Broker) backendFor(name string) MemoryBackend {
	if name == "" {
		return b.defaults
	}
	// Future: registry lookup. For now we only ship LocalBackend; the
	// production deployment will register Firestore/AlloyDB via dependency
	// injection in cmd/aiplex-api/main.go.
	if b.defaults != nil && b.defaults.Name() == name {
		return b.defaults
	}
	return b.defaults
}
