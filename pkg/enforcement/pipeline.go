// Package enforcement composes mandatory admission providers without knowing
// about quota, authorization, models, or any provider's storage schema.
package enforcement

import (
	"context"
	"errors"
	"fmt"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"maps"
	"reflect"
	"sync"
	"time"
)

type Request = contracts.RequestContext
type Outcome struct {
	Successful bool
	Usage      map[string]int64
}

// Receipt owns provider-specific reservation/decision state. Complete must be
// idempotent; an error may be retried against the same receipt. Abort releases
// unconsumed admission state. Implementations must honor context deadlines.
type Receipt interface {
	Complete(context.Context, Outcome) error
	Abort(context.Context) error
}
type Provider interface {
	Admit(context.Context, Request) (Receipt, error)
}
type Binding struct {
	Name     string
	Provider Provider
}
type Pipeline struct{ bindings []Binding }

func New(required []string, providers map[string]Provider) (*Pipeline, error) {
	p := &Pipeline{}
	seen := map[string]bool{}
	for _, name := range required {
		if name == "" || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate required provider %q", name)
		}
		seen[name] = true
		v := providers[name]
		if v == nil || (reflect.ValueOf(v).Kind() == reflect.Pointer && reflect.ValueOf(v).IsNil()) {
			return nil, fmt.Errorf("required provider %s unavailable", name)
		}
		p.bindings = append(p.bindings, Binding{name, v})
	}
	return p, nil
}
func (p *Pipeline) Admit(ctx context.Context, req Request) (*Ticket, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	ticket := &Ticket{}
	for _, b := range p.bindings {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, abortBounded(ctx, ticket))
		}
		requestCopy := req
		if req.MembershipSnapshotRef != nil {
			ref := *req.MembershipSnapshotRef
			requestCopy.MembershipSnapshotRef = &ref
		}
		receipt, err := b.Provider.Admit(ctx, requestCopy)
		if receipt != nil {
			ticket.receipts = append(ticket.receipts, receipt)
		}
		if err != nil || receipt == nil {
			if err == nil {
				err = fmt.Errorf("nil receipt")
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			abortErr := ticket.Abort(cleanup)
			cancel()
			return nil, errors.Join(fmt.Errorf("admission %s: %w", b.Name, err), abortErr)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, abortBounded(ctx, ticket))
	}
	return ticket, nil
}

type Ticket struct {
	mu        sync.Mutex
	receipts  []Receipt
	completed int
	aborted   bool
	aborting  bool // sticky once cleanup starts, even if cleanup must be retried
	settling  bool
	outcome   Outcome
}

func (t *Ticket) Complete(ctx context.Context, out Outcome) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for meter, quantity := range out.Usage {
		if meter == "" || quantity < 0 {
			return fmt.Errorf("invalid usage quantity for %q", meter)
		}
	}
	if t.aborting || t.aborted {
		return fmt.Errorf("ticket abort started")
	}
	if t.settling {
		if t.outcome.Successful != out.Successful || !maps.Equal(t.outcome.Usage, out.Usage) {
			return fmt.Errorf("settlement outcome changed")
		}
	} else {
		t.outcome = Outcome{Successful: out.Successful, Usage: maps.Clone(out.Usage)}
		t.settling = true
	}
	// A failed completion retains progress and can be retried, never aborting
	// already-consumed work or rerunning successfully completed providers.
	for t.completed < len(t.receipts) {
		if err := t.receipts[t.completed].Complete(ctx, Outcome{Successful: t.outcome.Successful, Usage: maps.Clone(t.outcome.Usage)}); err != nil {
			return err
		}
		t.completed++
	}
	return nil
}
func (t *Ticket) Abort(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.aborted {
		return nil
	}
	if t.settling {
		return fmt.Errorf("ticket settlement started")
	}
	// Choosing abort permanently excludes settlement. Cleanup progress is
	// tracked separately, so failed receipts remain retryable without allowing
	// Complete to settle a reduced set of mandatory admission providers.
	t.aborting = true
	var errs []error
	pending := make([]Receipt, 0)
	for i := len(t.receipts) - 1; i >= 0; i-- {
		if err := t.receipts[i].Abort(ctx); err != nil {
			errs = append(errs, err)
			pending = append(pending, t.receipts[i])
		}
	}
	for i, j := 0, len(pending)-1; i < j; i, j = i+1, j-1 {
		pending[i], pending[j] = pending[j], pending[i]
	}
	t.receipts = pending
	t.aborted = len(pending) == 0
	return errors.Join(errs...)
}

func abortBounded(ctx context.Context, t *Ticket) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return t.Abort(cleanup)
}
