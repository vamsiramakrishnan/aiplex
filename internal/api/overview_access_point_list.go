package api

import (
	"net/http"
	"sort"
)

// OverviewAccessPointCounts are the resources an access point exposes.
type OverviewAccessPointCounts struct {
	Models int `json:"models"`
	Tools  int `json:"tools"`
	Skills int `json:"skills"`
}

// OverviewAccessPointSummary is one row of the access point list. It is built
// from saved configuration and the active quota policy only; it never scans
// the usage ledger or resolves model surfaces and connections.
type OverviewAccessPointSummary struct {
	ID            string                    `json:"id"`
	DisplayName   string                    `json:"display_name,omitempty"`
	CallerKinds   []string                  `json:"caller_kinds"`
	QoSClass      string                    `json:"qos_class,omitempty"`
	Counts        OverviewAccessPointCounts `json:"counts"`
	QuotaCoverage OverviewQuotaCoverage     `json:"quota_coverage"`
}

// OverviewAccessPointList is the response of GET /overview/access-points.
type OverviewAccessPointList struct {
	AccessPoints   []OverviewAccessPointSummary `json:"access_points"`
	NextCursor     string                       `json:"next_cursor,omitempty"`
	SkippedInvalid int                          `json:"skipped_invalid,omitempty"`
}

// AccessPointList handles GET /overview/access-points: a cheap, paginated
// list for landing pages, ordered by id.
func (h *OverviewHandler) AccessPointList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	if err := overviewParams(r, "limit", "cursor"); err != nil {
		overviewQueryError(w, r, err.Error())
		return
	}
	limit, err := parseOverviewLimit(r)
	if err != nil {
		overviewQueryError(w, r, err.Error())
		return
	}
	after, err := decodeOverviewCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		overviewQueryError(w, r, err.Error())
		return
	}
	records, err := h.Connected.Store.List(r.Context(), tenant, AccessPointKind)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "OVERVIEW_UNAVAILABLE", "tenant configuration is unavailable")
		return
	}
	out := OverviewAccessPointList{AccessPoints: []OverviewAccessPointSummary{}}
	aps := []AccessPoint{}
	for _, rec := range records {
		if rec.TenantID != tenant || rec.Kind != AccessPointKind {
			continue
		}
		ap, err := decodeAccessPoint(rec.ID, rec.Spec)
		if err != nil {
			out.SkippedInvalid++
			continue
		}
		aps = append(aps, ap)
	}
	sort.Slice(aps, func(i, j int) bool { return aps[i].ID < aps[j].ID })
	for _, ap := range aps {
		if ap.ID <= after {
			continue
		}
		if len(out.AccessPoints) == limit {
			out.NextCursor = encodeOverviewCursor(out.AccessPoints[len(out.AccessPoints)-1].ID)
			break
		}
		kinds := overviewNormalizeAccessPoint(ap).CallerKinds
		quota := h.apQuota(tenant, ap)
		out.AccessPoints = append(out.AccessPoints, OverviewAccessPointSummary{
			ID: ap.ID, DisplayName: ap.DisplayName, CallerKinds: kinds, QoSClass: quota.QoSClass,
			Counts:        OverviewAccessPointCounts{Models: len(ap.Models), Tools: len(ap.Tools), Skills: len(ap.Skills)},
			QuotaCoverage: quota.Coverage,
		})
	}
	JSON(w, http.StatusOK, out)
}

// overviewNormalizeAccessPoint makes list fields that clients iterate empty
// rather than nil, so they render as [] (a saved record may predate the
// save-time validation that requires caller_kinds).
func overviewNormalizeAccessPoint(ap AccessPoint) AccessPoint {
	if ap.CallerKinds == nil {
		ap.CallerKinds = []string{}
	}
	return ap
}
