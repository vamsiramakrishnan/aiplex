package enforcement

import (
	"context"
	"errors"
	"testing"
)

type receipt struct {
	aborts, completes int
	fail              bool
}

func (r *receipt) Abort(context.Context) error { r.aborts++; return nil }
func (r *receipt) Complete(context.Context, Outcome) error {
	r.completes++
	if r.fail {
		return errors.New("retry")
	}
	return nil
}

type provider struct {
	r   *receipt
	err error
}

func (p provider) Admit(context.Context, Request) (Receipt, error) {
	if p.r == nil {
		return nil, p.err
	}
	return p.r, p.err
}
func request() Request {
	return Request{Version: "v1", TenantID: "t", RequestID: "r", PrincipalID: "p", ResourceID: "m"}
}
func TestMandatoryAndRollback(t *testing.T) {
	if _, e := New([]string{"quota"}, nil); e == nil {
		t.Fatal("missing provider accepted")
	}
	a := &receipt{}
	b := &receipt{}
	p, _ := New([]string{"a", "b"}, map[string]Provider{"a": provider{a, nil}, "b": provider{b, errors.New("denied")}})
	if _, e := p.Admit(context.Background(), request()); e == nil || a.aborts != 1 || b.aborts != 1 {
		t.Fatalf("rollback: %v %+v %+v", e, a, b)
	}
}
func TestSettlementRetryPreservesProgress(t *testing.T) {
	a := &receipt{}
	b := &receipt{fail: true}
	p, _ := New([]string{"a", "b"}, map[string]Provider{"a": provider{a, nil}, "b": provider{b, nil}})
	ticket, e := p.Admit(context.Background(), request())
	if e != nil {
		t.Fatal(e)
	}
	if e = ticket.Complete(context.Background(), Outcome{}); e == nil {
		t.Fatal("expected retry")
	}
	if e = ticket.Abort(context.Background()); e == nil {
		t.Fatal("consumed work released")
	}
	b.fail = false
	if e = ticket.Complete(context.Background(), Outcome{}); e != nil {
		t.Fatal(e)
	}
	if a.completes != 1 || b.completes != 2 {
		t.Fatal(a, b)
	}
}

func TestSettlementRetryRejectsChangedUsage(t *testing.T) {
	a := &receipt{}
	b := &receipt{fail: true}
	p, _ := New([]string{"a", "b"}, map[string]Provider{"a": provider{a, nil}, "b": provider{b, nil}})
	ticket, _ := p.Admit(context.Background(), request())
	usage := map[string]int64{"tokens": 10}
	_ = ticket.Complete(context.Background(), Outcome{Usage: usage})
	usage["tokens"] = 100
	b.fail = false
	if e := ticket.Complete(context.Background(), Outcome{Usage: usage}); e == nil {
		t.Fatal("changed settlement accepted")
	}
	if e := ticket.Complete(context.Background(), Outcome{Usage: map[string]int64{"tokens": 10}}); e != nil {
		t.Fatal(e)
	}
}
