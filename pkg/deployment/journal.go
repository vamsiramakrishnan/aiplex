package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// ResourceJournal uses the host's tenant-scoped CAS store. Supply a durable
// backend (e.g. the Firestore adapter) when enabling execution in production.
type ResourceJournal struct{ Store resources.Store }

func journalID(r Receipt) string {
	data, _ := json.Marshal([]string{r.ProviderID, r.PrincipalID, r.ResourceID, r.IdempotencyKey})
	return digest(data)
}
func validReceipt(r Receipt) bool {
	return r.ProviderID != "" && r.TenantID != "" && r.PrincipalID != "" && r.ResourceID != "" && r.IdempotencyKey != "" && hexDigest.MatchString(r.ActionDigest)
}
func (j ResourceJournal) Begin(ctx context.Context, r Receipt) (Receipt, bool, error) {
	if j.Store == nil || !validReceipt(r) || r.State != Pending || r.Readiness != "unverified" {
		return Receipt{}, false, fmt.Errorf("invalid pending deployment receipt")
	}
	data, _ := json.Marshal(r)
	_, e := j.Store.Put(ctx, resources.Record{TenantID: r.TenantID, Kind: "deployment-invocation", ID: journalID(r), Spec: data}, 0)
	if e == nil {
		return r, true, nil
	}
	if !errors.Is(e, resources.ErrConflict) {
		return Receipt{}, false, e
	}
	record, e := j.Store.Get(ctx, r.TenantID, "deployment-invocation", journalID(r))
	if e != nil {
		return Receipt{}, false, e
	}
	var prior Receipt
	if e = json.Unmarshal(record.Spec, &prior); e != nil {
		return Receipt{}, false, e
	}
	if !sameInvocation(prior, r) || prior.ActionDigest != r.ActionDigest {
		return Receipt{}, false, ErrConflict
	}
	return prior, false, nil
}
func (j ResourceJournal) Finish(ctx context.Context, r Receipt) error {
	if j.Store == nil || !validReceipt(r) || r.State == Pending || r.State == Planned || r.Readiness != "unverified" {
		return fmt.Errorf("invalid terminal deployment receipt")
	}
	record, e := j.Store.Get(ctx, r.TenantID, "deployment-invocation", journalID(r))
	if e != nil {
		return e
	}
	var prior Receipt
	if e = json.Unmarshal(record.Spec, &prior); e != nil {
		return e
	}
	if !sameInvocation(prior, r) || prior.ActionDigest != r.ActionDigest {
		return ErrConflict
	}
	if prior.State != Pending {
		old, _ := json.Marshal(prior)
		next, _ := json.Marshal(r)
		if string(old) == string(next) {
			return nil
		}
		return ErrConflict
	}
	data, _ := json.Marshal(r)
	_, e = j.Store.Put(ctx, resources.Record{TenantID: r.TenantID, Kind: record.Kind, ID: record.ID, Spec: data}, record.Revision)
	return e
}
func sameInvocation(a, b Receipt) bool {
	return a.ProviderID == b.ProviderID && a.TenantID == b.TenantID && a.PrincipalID == b.PrincipalID && a.ResourceID == b.ResourceID && a.IdempotencyKey == b.IdempotencyKey
}
