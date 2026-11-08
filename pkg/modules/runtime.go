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
	failed := make([]entry, 0)
	for i := len(r.started) - 1; i >= 0; i-- {
		e := r.started[i]
		if err := e.module.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop module %s: %w", e.descriptor.Name, err))
			failed = append(failed, e)
		}
	}
	// Restore dependency order for a later cleanup attempt.
	for i, j := 0, len(failed)-1; i < j; i, j = i+1, j-1 {
		failed[i], failed[j] = failed[j], failed[i]
	}
	r.started = failed
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
