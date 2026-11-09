package deployment

import (
	"context"
	"fmt"
	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
)

// PipelineAdmission reuses the common Aiplex admission contract. Quota, policy
// or approval providers remain first-class registered pipeline providers.
type PipelineAdmission struct{ Pipeline *enforcement.Pipeline }

func (p PipelineAdmission) Admit(ctx context.Context, a Action) (AdmissionReceipt, error) {
	if p.Pipeline == nil {
		return nil, fmt.Errorf("admission pipeline unavailable")
	}
	ticket, e := p.Pipeline.Admit(ctx, a.Request.Context)
	if e != nil {
		return nil, e
	}
	return pipelineReceipt{ticket}, nil
}

type pipelineReceipt struct{ ticket *enforcement.Ticket }

func (r pipelineReceipt) Complete(ctx context.Context, success bool) error {
	return r.ticket.Complete(ctx, enforcement.Outcome{Successful: success})
}
func (r pipelineReceipt) Abort(ctx context.Context) error { return r.ticket.Abort(ctx) }
