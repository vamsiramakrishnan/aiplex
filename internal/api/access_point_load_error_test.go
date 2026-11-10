package api

import (
	"context"
	"errors"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// failingAccessPointStore fails reads of access-point records the way an
// unavailable backend does, and passes everything else through.
type failingAccessPointStore struct{ resources.Store }

func (s failingAccessPointStore) Get(ctx context.Context, tenant, kind, id string) (resources.Record, error) {
	if kind == AccessPointKind {
		return resources.Record{}, errors.New("backend unavailable")
	}
	return s.Store.Get(ctx, tenant, kind, id)
}

func TestAccessPointLoadErrorsDistinguishStoreFailureFromInvalidRecord(t *testing.T) {
	f := newOverviewFixture(t)
	paths := []struct{ method, path string }{
		{"GET", "/api/v1/overview/access-points/ide"},
		{"GET", "/api/v1/overview/access-points/ide/connect"},
		{"POST", "/api/v1/access-points/ide/quota-plan"},
	}
	do := func(method, path string) int {
		if method == "GET" {
			return f.get(path).Code
		}
		return f.do(method, path, "").Code
	}

	// A record that loads but does not decode stays 409.
	record, err := f.store.Get(context.Background(), "tenant", AccessPointKind, "ide")
	if err != nil {
		t.Fatal(err)
	}
	broken := record
	broken.Spec = []byte(`{"id":"ide","not_a_field":true}`)
	if _, err = f.store.Put(context.Background(), broken, record.Revision); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if code := do(p.method, p.path); code != 409 {
			t.Errorf("%s %s with an invalid record: %d, want 409", p.method, p.path, code)
		}
	}

	// A store read failure is 503 on all of them, as on the request path.
	f.h.Store = failingAccessPointStore{f.h.Store}
	for _, p := range paths {
		if code := do(p.method, p.path); code != 503 {
			t.Errorf("%s %s with a failing store: %d, want 503", p.method, p.path, code)
		}
	}
}
