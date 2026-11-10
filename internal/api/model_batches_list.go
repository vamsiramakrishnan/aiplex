package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

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
	// CreatedAt and SettledAt are set once by the gateway. They are omitted
	// for records written before timestamps existed (unknown, not zero).
	CreatedAt *time.Time `json:"created_at,omitempty"`
	SettledAt *time.Time `json:"settled_at,omitempty"`
}

// ModelBatchPage is one page of batches, ordered by id.
type ModelBatchPage struct {
	Batches    []ModelBatchView `json:"batches"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// batchNewestLess orders created_at descending, records without created_at
// last, ties by id.
func batchNewestLess(a, b ModelBatchView) bool {
	switch {
	case a.CreatedAt != nil && b.CreatedAt != nil && !a.CreatedAt.Equal(*b.CreatedAt):
		return a.CreatedAt.After(*b.CreatedAt)
	case (a.CreatedAt == nil) != (b.CreatedAt == nil):
		return a.CreatedAt != nil
	}
	return a.ID < b.ID
}

// batchCursor is the position after which an order=newest page continues.
type batchCursor struct {
	created *time.Time
	id      string
}

func (c batchCursor) view() ModelBatchView { return ModelBatchView{ID: c.id, CreatedAt: c.created} }

// encodeBatchCursor renders "n:<unix nanos or ->:<id>".
func encodeBatchCursor(v ModelBatchView) string {
	stamp := "-"
	if v.CreatedAt != nil {
		stamp = strconv.FormatInt(v.CreatedAt.UnixNano(), 10)
	}
	return "n:" + stamp + ":" + v.ID
}

func parseBatchCursor(raw string) (batchCursor, bool) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[0] != "n" || parts[2] == "" {
		return batchCursor{}, false
	}
	c := batchCursor{id: parts[2]}
	if parts[1] != "-" {
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return batchCursor{}, false
		}
		t := time.Unix(0, n).UTC()
		c.created = &t
	}
	return c, true
}

// ListModelBatches lists the tenant's durable batch records. Administrative,
// read-only. Query: state (open|settled; default all), surface_id, limit
// (1..500, default 100), cursor (opaque, from next_cursor), order (id, the
// default, or newest: created_at descending with records that have no
// created_at last, by id) and older_than (RFC3339; only batches created
// before it, so "state=open&older_than=..." finds batches whose results were
// never fetched; records without created_at are excluded because their age is
// unknown). A cursor is valid only with the order that issued it.
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
	order := q.Get("order")
	if order != "" && order != "id" && order != "newest" {
		Error(w, r, 400, "INVALID_QUERY", "order must be id or newest")
		return
	}
	newest := order == "newest"
	var olderThan time.Time
	if raw := q.Get("older_than"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			Error(w, r, 400, "INVALID_QUERY", "older_than must be an RFC3339 timestamp")
			return
		}
		olderThan = t.UTC()
	}
	if cursor != "" && !newest && strings.HasPrefix(cursor, "n:") {
		Error(w, r, 400, "INVALID_QUERY", "cursor belongs to order=newest")
		return
	}
	var after *batchCursor
	if cursor != "" && newest {
		c, ok := parseBatchCursor(cursor)
		if !ok {
			Error(w, r, 400, "INVALID_QUERY", "cursor does not belong to order=newest")
			return
		}
		after = &c
	}
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
	views := []ModelBatchView{}
	for _, record := range records {
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
		if !batch.CreatedAt.IsZero() {
			created := batch.CreatedAt.UTC()
			view.CreatedAt = &created
		}
		if !batch.SettledAt.IsZero() {
			settled := batch.SettledAt.UTC()
			view.SettledAt = &settled
		}
		if (state != "" && view.State != state) || (surface != "" && view.SurfaceID != surface) {
			continue
		}
		if !olderThan.IsZero() && (view.CreatedAt == nil || !view.CreatedAt.Before(olderThan)) {
			continue
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if newest {
			return batchNewestLess(views[i], views[j])
		}
		return views[i].ID < views[j].ID
	})
	page := ModelBatchPage{Batches: []ModelBatchView{}}
	for _, view := range views {
		if cursor != "" {
			if newest && !batchNewestLess(after.view(), view) {
				continue
			}
			if !newest && view.ID <= cursor {
				continue
			}
		}
		if len(page.Batches) == limit {
			last := page.Batches[len(page.Batches)-1]
			page.NextCursor = last.ID
			if newest {
				page.NextCursor = encodeBatchCursor(last)
			}
			break
		}
		page.Batches = append(page.Batches, view)
	}
	JSON(w, 200, page)
}
