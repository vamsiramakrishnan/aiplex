package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// maxBackfillBody bounds a backfill request.
const maxBackfillBody = 16 << 10

// backfillRequest selects records whose unknown cost may be computed now.
// Every field is optional; the window and the model and provider narrow what
// is scanned. The tenant is always the verified one.
type backfillRequest struct {
	Since    *time.Time `json:"since"`
	Until    *time.Time `json:"until"`
	ModelID  string     `json:"model_id"`
	Provider string     `json:"provider"`
	DryRun   bool       `json:"dry_run"`
	// Limit bounds the records one call scans (default 500, at most 1000);
	// call again with next_cursor until it is empty.
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

// Backfill prices records whose cost was unknown when they were written, using
// the price effective at each record's own time. It never changes a settled or
// estimated cost and is idempotent. Operators only (route policy: admin).
// POST /api/v1/usage/backfill
func (h *UsageHandler) Backfill(w http.ResponseWriter, r *http.Request) {
	tenant, visibility, ok := h.scope(w, r)
	if !ok {
		return
	}
	if visibility != nil {
		Error(w, r, http.StatusForbidden, "FORBIDDEN", "back-estimating costs needs the admin role")
		return
	}
	if h.Prices == nil {
		Error(w, r, http.StatusServiceUnavailable, "PRICES_UNAVAILABLE", "model prices are not configured")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBackfillBody+1))
	if err != nil || len(raw) > maxBackfillBody {
		Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "request body is missing or too large")
		return
	}
	var req backfillRequest
	if len(bytes.TrimSpace(raw)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil || decoder.More() {
			Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid backfill request")
			return
		}
	}
	filter := usageledger.Filter{ModelID: req.ModelID, Provider: req.Provider}
	if len(req.ModelID) > usageledger.MaxTextLength || len(req.Provider) > usageledger.MaxTextLength {
		Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "model_id or provider is too long")
		return
	}
	if req.Since != nil {
		filter.Since = req.Since.UTC()
	}
	if req.Until != nil {
		filter.Until = req.Until.UTC()
	}
	if !filter.Since.IsZero() && !filter.Until.IsZero() && !filter.Until.After(filter.Since) {
		Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "until must be after since")
		return
	}
	if req.Cursor != "" {
		if _, err := usageledger.DecodeCursor(req.Cursor); err != nil {
			Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "cursor is malformed")
			return
		}
	}
	prices, err := h.Prices(r.Context(), tenant)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "PRICES_UNAVAILABLE", "model prices could not be read")
		return
	}
	result, err := usageledger.Backfill(r.Context(), h.Ledger, tenant, prices, usageledger.BackfillQuery{Filter: filter, Limit: req.Limit, Cursor: req.Cursor, DryRun: req.DryRun}, time.Now())
	switch {
	case errors.Is(err, usageledger.ErrBackfillUnsupported):
		Error(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "this usage ledger store cannot back-estimate costs")
		return
	case errors.Is(err, usageledger.ErrInvalid):
		Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "limit must be between 1 and "+itoa(usageledger.MaxBackfillBatch))
		return
	case err != nil:
		h.storeError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, result)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
