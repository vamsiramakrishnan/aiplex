package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// modelBatchKind is the tenant resource kind holding gateway batch records.
const modelBatchKind = "model-batch"

// resourceBatchStore keeps modelapi.BatchRecords as revisioned tenant
// resources, so the exactly-once settlement swap holds across replicas that
// share the resource store.
type resourceBatchStore struct{ store resources.Store }

func batchRecordID(owner modelapi.Owner, provider, batchID string) string {
	raw, _ := json.Marshal([]string{owner.SurfaceID, provider, batchID})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s resourceBatchStore) LoadBatch(ctx context.Context, owner modelapi.Owner, provider, batchID string) (modelapi.BatchRecord, uint64, error) {
	if s.store == nil {
		return modelapi.BatchRecord{}, 0, errors.New("batch store unavailable")
	}
	record, err := s.store.Get(ctx, owner.TenantID, modelBatchKind, batchRecordID(owner, provider, batchID))
	if errors.Is(err, resources.ErrNotFound) {
		return modelapi.BatchRecord{}, 0, modelapi.ErrBatchNotFound
	}
	if err != nil {
		return modelapi.BatchRecord{}, 0, err
	}
	var batch modelapi.BatchRecord
	if err = json.Unmarshal(record.Spec, &batch); err != nil {
		return modelapi.BatchRecord{}, 0, err
	}
	if batch.Owner.TenantID != owner.TenantID {
		return modelapi.BatchRecord{}, 0, errors.New("batch record tenant mismatch")
	}
	return batch, record.Revision, nil
}

func (s resourceBatchStore) SaveBatch(ctx context.Context, batch modelapi.BatchRecord, revision uint64) (uint64, error) {
	if s.store == nil {
		return 0, errors.New("batch store unavailable")
	}
	spec, err := json.Marshal(batch)
	if err != nil {
		return 0, err
	}
	saved, err := s.store.Put(ctx, resources.Record{TenantID: batch.Owner.TenantID, Kind: modelBatchKind, ID: batchRecordID(batch.Owner, batch.Provider, batch.BatchID), Spec: spec}, revision)
	if errors.Is(err, resources.ErrConflict) {
		return 0, modelapi.ErrBatchConflict
	}
	if err != nil {
		return 0, err
	}
	return saved.Revision, nil
}

// batchSettler settles a complete batch on its admitted quota attempt and
// puts its usage on the ledger record of the exchange that settled it.
type batchSettler struct {
	quota   *QuotaHost
	capture *usageCapture
	tenant  string
}

func (s batchSettler) SettleBatch(ctx context.Context, owner modelapi.Owner, settlement modelapi.BatchSettlement) error {
	if s.quota == nil || settlement.AttemptID == "" || owner.TenantID != s.tenant {
		return nil
	}
	err := s.quota.SettleBatch(ctx, s.tenant, settlement.AttemptID, settlement.BatchID, settlement.Counters)
	switch {
	case errors.Is(err, errBatchNotQuotaMetered):
		// Created under a request-count binding: the create exchange already
		// settled; the job's usage is ledger telemetry only.
		return nil
	case errors.Is(err, ErrBatchSettlementIncomplete):
		return modelapi.ErrBatchUsageIncomplete
	}
	return err
}

func (s batchSettler) RecordBatch(_ context.Context, _ modelapi.Owner, settlement modelapi.BatchSettlement) {
	c := s.capture
	if c == nil {
		return
	}
	counters := map[string]int64{}
	for key, n := range settlement.Counters {
		if key != "batch_requests" {
			counters[key] = n
		}
	}
	c.markBatch()
	c.mu.Lock()
	if settlement.AttemptID != "" {
		c.attemptID = settlement.AttemptID
	}
	if settlement.Model != "" && c.record.ModelID == "" {
		c.record.ModelID = settlement.Model
	}
	c.mu.Unlock()
	c.observe(counters, true, "", true, "")
}
