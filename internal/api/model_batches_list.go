package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

const (
	batchListDefaultLimit = 100
	batchListMaxLimit     = 500
)

// ModelBatchView is the read-only administrative view of one durable provider
// batch record. It carries no usage counters, prompts or results.
type ModelBatchView struct {
	// ID is the record id (a digest of surface, provider and batch id).
	ID              string `json:"id"`
	SurfaceID       string `json:"surface_id"`
	Provider        string `json:"provider"`
	BatchID         string `json:"batch_id"`
	AttemptID       string `json:"attempt_id,omitempty"`
	Model           string `json:"model,omitempty"`
	CreateOperation string `json:"create_operation"`
	// State is "open" until results were fetched through the gateway and
	// settled, then "settled". An open batch keeps its admitted quota
	// exposure and concurrency slot.
	State string `json:"state"`
	// ProviderStatus and Ended are the provider's last reported batch status.
	ProviderStatus string `json:"provider_status,omitempty"`
	Ended          bool   `json:"ended"`
	// Expected is the provider's item count; null while unknown.
	Expected *int64 `json:"expected"`
}

// ModelBatchPage is one page of batches, ordered by id.
type ModelBatchPage struct {
	Batches    []ModelBatchView `json:"batches"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// ListModelBatches lists the tenant's durable batch records. Administrative,
// read-only. Query: state (open|settled; default all), surface_id, limit
// (1..500, default 100), cursor (opaque, from next_cursor).
func (h *ConnectedHandler) ListModelBatches(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	access := GetWIFAccess(r.Context())
	admin := false
	if access != nil {
		for _, role := range access.Roles {
			admin = admin || role == models.RoleAdmin
		}
	}
	if !admin {
		Error(w, r, 403, "ADMIN_REQUIRED", "listing model batches requires an administrative role")
		return
	}
	q := r.URL.Query()
	state, surface, cursor := q.Get("state"), q.Get("surface_id"), q.Get("cursor")
	if state != "" && state != "open" && state != "settled" {
		Error(w, r, 400, "INVALID_QUERY", "state must be open or settled")
		return
	}
	limit := batchListDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > batchListMaxLimit {
			Error(w, r, 400, "INVALID_QUERY", "limit must be between 1 and "+strconv.Itoa(batchListMaxLimit))
			return
		}
		limit = n
	}
	records, err := h.Store.List(r.Context(), tenant, modelBatchKind)
	if err != nil {
		resourceError(w, r, err)
		return
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	page := ModelBatchPage{Batches: []ModelBatchView{}}
	for _, record := range records {
		if cursor != "" && record.ID <= cursor {
			continue
		}
		var batch modelapi.BatchRecord
		if json.Unmarshal(record.Spec, &batch) != nil || batch.AliasOf != "" || batch.Owner.TenantID != tenant {
			continue // result-file pointers and foreign or unreadable records are not batches
		}
		view := ModelBatchView{ID: record.ID, SurfaceID: batch.Owner.SurfaceID, Provider: batch.Provider, BatchID: batch.BatchID, AttemptID: batch.AttemptID, Model: batch.Model, CreateOperation: batch.CreateOperation, State: "open", ProviderStatus: batch.Status, Ended: batch.Ended}
		if batch.Settled {
			view.State = "settled"
		}
		if batch.Expected >= 0 {
			expected := batch.Expected
			view.Expected = &expected
		}
		if (state != "" && view.State != state) || (surface != "" && view.SurfaceID != surface) {
			continue
		}
		if len(page.Batches) == limit {
			page.NextCursor = page.Batches[len(page.Batches)-1].ID
			break
		}
		page.Batches = append(page.Batches, view)
	}
	JSON(w, 200, page)
}
