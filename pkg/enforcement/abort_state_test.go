package enforcement_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
)

func TestAbortInitiationPermanentlyExcludesSettlementAndRetriesOnlyPendingCleanup(t *testing.T) {
	for _, allFail := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial cleanup", true: "all cleanup failed"}[allFail], func(t *testing.T) {
			aborts := [2]int{}
			completes := 0
			providers := map[string]enforcement.Provider{}
			for i, name := range []string{"first", "second"} {
				i := i
				receipt := callbackReceipt{complete: func(context.Context, enforcement.Outcome) error { completes++; return nil }, abort: func(context.Context) error {
					aborts[i]++
					if aborts[i] == 1 && (i == 0 || allFail) {
						return errors.New("cleanup temporarily unavailable")
					}
					return nil
				}}
				providers[name] = admissionFunc(func(context.Context, enforcement.Request) (enforcement.Receipt, error) { return receipt, nil })
			}
			pipeline, err := enforcement.New([]string{"first", "second"}, providers)
			if err != nil {
				t.Fatal(err)
			}
			ticket, err := pipeline.Admit(context.Background(), trustedRequest())
			if err != nil {
				t.Fatal(err)
			}
			if err = ticket.Abort(context.Background()); err == nil {
				t.Fatal("expected initial cleanup failure")
			}
			if err = ticket.Complete(context.Background(), enforcement.Outcome{Successful: true, Usage: map[string]int64{"tokens": 1}}); err == nil || completes != 0 {
				t.Fatal("aborting ticket settled remaining provider", err, completes)
			}
			if err = ticket.Abort(context.Background()); err != nil {
				t.Fatal("failed cleanup could not be retried", err)
			}
			wantSecond := 1
			if allFail {
				wantSecond = 2
			}
			if aborts[0] != 2 || aborts[1] != wantSecond {
				t.Fatalf("successful cleanup repeated or pending cleanup lost: %v", aborts)
			}
			if err = ticket.Complete(context.Background(), enforcement.Outcome{}); err == nil || completes != 0 {
				t.Fatal("cleaned ticket became settleable")
			}
			if err = ticket.Abort(context.Background()); err != nil || aborts[0] != 2 || aborts[1] != wantSecond {
				t.Fatal("completed cleanup was not idempotent", err, aborts)
			}
		})
	}
}

func TestConcurrentCompleteCannotSettleAfterAbortStarts(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var completes atomic.Int32
	cleanupCalls := 0
	receipt := callbackReceipt{complete: func(context.Context, enforcement.Outcome) error { completes.Add(1); return nil }, abort: func(context.Context) error {
		cleanupCalls++
		if cleanupCalls == 1 {
			close(entered)
			<-release
			return errors.New("retry cleanup")
		}
		return nil
	}}
	pipeline, err := enforcement.New([]string{"mandatory"}, map[string]enforcement.Provider{"mandatory": admissionFunc(func(context.Context, enforcement.Request) (enforcement.Receipt, error) { return receipt, nil })})
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := pipeline.Admit(context.Background(), trustedRequest())
	if err != nil {
		t.Fatal(err)
	}
	abortResult := make(chan error, 1)
	go func() { abortResult <- ticket.Abort(context.Background()) }()
	<-entered
	completeResult := make(chan error, 1)
	go func() { completeResult <- ticket.Complete(context.Background(), enforcement.Outcome{}) }()
	close(release)
	if err = <-abortResult; err == nil {
		t.Fatal("expected retryable abort failure")
	}
	if err = <-completeResult; err == nil || completes.Load() != 0 {
		t.Fatal("concurrent completion crossed abort boundary", err)
	}
	if err = ticket.Abort(context.Background()); err != nil || cleanupCalls != 2 {
		t.Fatal("cleanup retry lost", err, cleanupCalls)
	}
}
