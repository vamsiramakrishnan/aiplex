package deployment

import (
	"context"
	"encoding/json"
	"fmt"
)

// Get inspects one fully scoped invocation. It never retries the operation.
func (j ResourceJournal) Get(ctx context.Context, key Receipt) (Receipt, error) {
	if j.Store == nil || !validReceipt(key) {
		return Receipt{}, fmt.Errorf("invalid deployment receipt identity")
	}
	record, err := j.Store.Get(ctx, key.TenantID, "deployment-invocation", journalID(key))
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if json.Unmarshal(record.Spec, &receipt) != nil || !sameInvocation(receipt, key) || receipt.ActionDigest != key.ActionDigest {
		return Receipt{}, ErrConflict
	}
	return receipt, nil
}
