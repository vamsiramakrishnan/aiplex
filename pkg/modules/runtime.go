package modules

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Runtime serializes lifecycle calls. Modules must not call back into lifecycle
// methods on the same Runtime. Health probes cannot race teardown.
type Runtime struct {
	mu       sync.Mutex
	registry *Registry
	started  []entry
	running  bool
}

func NewRuntime(registry *Registry) *Runtime { return &Runtime{registry: registry} }

func (r *Runtime) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return nil
	}
	if len(r.started) > 0 {
		return fmt.Errorf("module cleanup incomplete; call Stop before restarting")
	}
	if r.registry == nil {
		return fmt.Errorf("nil module registry")
	}
	entries, err := r.registry.freeze()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, r.rollback(ctx))
		}
		// Include the attempted module: Stop must clean partially-started work.
		r.started = append(r.started, e)
		if err := e.module.Start(ctx); err != nil {
			return errors.Join(fmt.Errorf("start module %s: %w", e.descriptor.Name, err), r.rollback(ctx))
		}
		if err := e.module.Ready(ctx); err != nil {
			return errors.Join(fmt.Errorf("module %s not ready: %w", e.descriptor.Name, err), r.rollback(ctx))
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, r.rollback(ctx))
	}
	r.running = true
	return nil
}

func (r *Runtime) rollback(ctx context.Context) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return r.stop(cleanup)
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stop(ctx)
}

func (r *Runtime) stop(ctx context.Context) error {
	r.running = false
	var errs []error
	pending := make([]entry, 0)
	byName := make(map[string]entry, len(r.started))
	for _, e := range r.started {
		byName[e.descriptor.Name] = e
	}
	retained := map[string]bool{}
	var retain func(string)
	retain = func(name string) {
		if retained[name] {
			return
		}
		e, present := byName[name]
		if !present {
			return
		}
		retained[name] = true
		for _, dependency := range e.descriptor.Dependencies {
			retain(dependency.Name)
		}
	}
	for i := len(r.started) - 1; i >= 0; i-- {
		e := r.started[i]
		if retained[e.descriptor.Name] {
			pending = append(pending, e)
			continue
		}
		if err := e.module.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop module %s: %w", e.descriptor.Name, err))
			// A failed consumer may still need its providers to finish cleanup.
			// Retain their transitive graph while cleaning unrelated modules.
			retain(e.descriptor.Name)
			pending = append(pending, e)
		}
	}
	// Restore dependency order for a later cleanup attempt.
	for i, j := 0, len(pending)-1; i < j; i, j = i+1, j-1 {
		pending[i], pending[j] = pending[j], pending[i]
	}
	r.started = pending
	return errors.Join(errs...)
}

func (r *Runtime) Ready(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return fmt.Errorf("module runtime is not running")
	}
	var errs []error
	for _, e := range r.started {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if err := e.module.Ready(ctx); err != nil {
			errs = append(errs, fmt.Errorf("module %s not ready: %w", e.descriptor.Name, err))
		}
	}
	return errors.Join(errs...)
}
