package usageledger

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Recorder writes records off the request path. A write failure is reported
// to OnError and never propagates to the exchange that produced the record.
type Recorder struct {
	store   Store
	onError func(Record, error)
	timeout time.Duration
	sem     chan struct{}
	wg      sync.WaitGroup
}

type RecorderOption func(*Recorder)

// WithErrorHandler receives records that could not be built or stored.
func WithErrorHandler(fn func(Record, error)) RecorderOption {
	return func(r *Recorder) { r.onError = fn }
}

// WithConcurrency bounds background writes; extra writes run inline.
func WithConcurrency(n int) RecorderOption {
	return func(r *Recorder) {
		if n > 0 {
			r.sem = make(chan struct{}, n)
		}
	}
}

func NewRecorder(store Store, opts ...RecorderOption) *Recorder {
	r := &Recorder{store: store, timeout: 10 * time.Second, sem: make(chan struct{}, 64), onError: func(Record, error) {}}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Store returns the underlying store.
func (r *Recorder) Store() Store {
	if r == nil {
		return nil
	}
	return r.store
}

// Submit builds and appends a record in the background. build runs with a
// bounded context detached from the request's cancellation, so a client that
// disconnects is still recorded.
func (r *Recorder) Submit(ctx context.Context, build func(context.Context) (Record, error)) {
	if r == nil || r.store == nil || build == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	detached := context.WithoutCancel(ctx)
	r.wg.Add(1)
	select {
	case r.sem <- struct{}{}:
		go func() {
			defer func() { <-r.sem }()
			r.write(detached, build)
		}()
	default:
		r.write(detached, build)
	}
}

func (r *Recorder) write(ctx context.Context, build func(context.Context) (Record, error)) {
	defer r.wg.Done()
	var record Record
	defer func() {
		if p := recover(); p != nil {
			r.onError(record, fmt.Errorf("usage record panicked: %v", p))
		}
	}()
	bounded, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	record, err := build(bounded)
	if err == nil {
		err = r.store.Append(bounded, record)
	}
	if err != nil {
		r.onError(record, err)
	}
}

// Flush waits for submitted records to be written (tests and shutdown).
func (r *Recorder) Flush() {
	if r != nil {
		r.wg.Wait()
	}
}
