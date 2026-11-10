// Package execution defines acknowledged execution lifecycle hooks shared by
// model and tool transports. Implementations own their policy and persistence.
package execution

import (
	"context"
	"time"
)

const AttemptHeader = "X-Aiplex-Attempt-ID"

type Resource struct{ Kind, ID string }

// Observation contains provider-normalized facts, not estimates. A cumulative
// observation replaces prior values only under the adapter's declared semantics.
type Observation struct {
	Source       string
	EventID      string
	ResourceID   string
	ModelVersion string
	Counters     map[string]int64
	Cumulative   bool
	Terminal     bool
	// Bound qualifies a non-terminal observation that ends a session without
	// final usage evidence: BoundLowerBound (counters are a lower bound),
	// BoundAmbiguous (counter composition across a resumed session is not
	// established) or BoundPartial (non-token units were not captured).
	// Empty means no qualification.
	Bound string
	// Dimensions are provider-reported billing dimensions of the observed
	// units (for example image_size, image_quality) and the batch marker
	// ("batch": "true"). They never change a counter.
	Dimensions map[string]string
}

// Usage-bound labels for Observation.Bound.
const (
	BoundLowerBound = "lower_bound"
	BoundAmbiguous  = "ambiguous"
	BoundPartial    = "partial"
)

// ResponseComplete means the response body/protocol completed. Terminal also
// requires native finality and the operation's declared usage evidence.
type Result struct {
	StatusCode       int
	ResponseComplete bool
	Terminal         bool
	Err              error
}

// Lease is prepared after authorization and payload validation. BeforeDispatch
// runs immediately before provider I/O. After that claim, failures use Finish
// with uncertainty, never Abort. Observe/Resource/Finish must acknowledge durable
// state before a transport exposes terminal data to its client.
type Lease interface {
	BeforeDispatch(context.Context) error
	Observe(context.Context, Observation) error
	Resource(context.Context, Resource) error
	Finish(context.Context, Result) error
	Abort(context.Context) error
}

// Receipt identifies the durable attempt without changing native payloads.
type Receipt interface{ AttemptID() string }

// StatusError exposes a safe client-facing status without leaking native
// credentials or raw internal failures. Transports may use it before headers.
type StatusError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *StatusError) Error() string { return e.Code + ": " + e.Message }
