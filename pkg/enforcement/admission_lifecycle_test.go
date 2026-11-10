package enforcement_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
)

type admissionFunc func(context.Context, enforcement.Request) (enforcement.Receipt, error)

func (f admissionFunc) Admit(ctx context.Context, r enforcement.Request) (enforcement.Receipt, error) {
	return f(ctx, r)
}

type callbackReceipt struct {
	complete func(context.Context, enforcement.Outcome) error
	abort    func(context.Context) error
}

func (r callbackReceipt) Complete(ctx context.Context, outcome enforcement.Outcome) error {
	if r.complete != nil {
		return r.complete(ctx, outcome)
	}
	return nil
}

func (r callbackReceipt) Abort(ctx context.Context) error {
	if r.abort != nil {
		return r.abort(ctx)
	}
	return nil
}

func trustedRequest() enforcement.Request {
	return enforcement.Request{Version: contracts.Version, TenantID: "tenant", RequestID: "request", PrincipalID: "principal", ResourceID: "model"}
}

func TestCancellationAfterFinalAdmissionReleasesWithDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aborted := 0
	receipt := callbackReceipt{abort: func(cleanup context.Context) error {
		aborted++
		if cleanup.Err() != nil {
			t.Errorf("cleanup inherited canceled admission context: %v", cleanup.Err())
		}
		if _, ok := cleanup.Deadline(); !ok {
			t.Error("cleanup has no deadline")
		}
		return nil
	}}
	pipeline, err := enforcement.New([]string{"required"}, map[string]enforcement.Provider{
		"required": admissionFunc(func(context.Context, enforcement.Request) (enforcement.Receipt, error) {
			cancel()
			return receipt, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := pipeline.Admit(ctx, trustedRequest())
	if !errors.Is(err, context.Canceled) || ticket != nil || aborted != 1 {
		t.Fatalf("cancellation admitted or leaked state: ticket=%v error=%v aborted=%d", ticket, err, aborted)
	}
}

func TestSettlementCannotChangeAcrossRetriesOrProviders(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := callbackReceipt{complete: func(_ context.Context, out enforcement.Outcome) error {
		firstCalls++
		out.Usage["tokens"] = 999 // Provider-local mutation must not poison another provider.
		return nil
	}}
	second := callbackReceipt{complete: func(_ context.Context, out enforcement.Outcome) error {
		secondCalls++
		if out.Usage["tokens"] != 10 {
			t.Fatalf("provider observed mutated accounting outcome: %v", out.Usage)
		}
		if secondCalls == 1 {
			return errors.New("temporary accounting outage")
		}
		return nil
	}}
	providers := map[string]enforcement.Provider{
		"first":  admissionFunc(func(context.Context, enforcement.Request) (enforcement.Receipt, error) { return first, nil }),
		"second": admissionFunc(func(context.Context, enforcement.Request) (enforcement.Receipt, error) { return second, nil }),
	}
	pipeline, err := enforcement.New([]string{"first", "second"}, providers)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := pipeline.Admit(context.Background(), trustedRequest())
	if err != nil {
		t.Fatal(err)
	}
	outcome := enforcement.Outcome{Successful: true, Usage: map[string]int64{"tokens": 10}}
	if err := ticket.Complete(context.Background(), outcome); err == nil {
		t.Fatal("expected temporary settlement failure")
	}
	outcome.Usage["tokens"] = 20
	if err := ticket.Complete(context.Background(), outcome); err == nil {
		t.Fatal("changed outcome accepted after partial settlement")
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("changed outcome reached providers: %d, %d", firstCalls, secondCalls)
	}
	outcome.Usage["tokens"] = 10
	if err := ticket.Complete(context.Background(), outcome); err != nil {
		t.Fatal(err)
	}
	if firstCalls != 1 || secondCalls != 2 {
		t.Fatalf("retry did not preserve progress: %d, %d", firstCalls, secondCalls)
	}
}
