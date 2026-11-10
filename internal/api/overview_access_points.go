package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// OverviewCaller is one resolved caller of an access point.
type OverviewCaller struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name,omitempty"`
	Declared    bool           `json:"declared"`
	Observed    bool           `json:"observed"`
	LastSeen    *time.Time     `json:"last_seen"`
	Usage       *OverviewUsage `json:"usage"`
}

// OverviewCallers says who can use an access point. Open lists the caller
// kinds admitted without an identity constraint; Groups are declared groups
// whose members cannot be enumerated.
type OverviewCallers struct {
	CallerKinds []string             `json:"caller_kinds"`
	Open        []string             `json:"open_to_all_of_kind"`
	Users       []OverviewCaller     `json:"users"`
	Agents      []OverviewCaller     `json:"agents"`
	Clients     []OverviewCaller     `json:"clients"`
	Groups      []quotaplex.GroupRef `json:"groups"`
	Note        string               `json:"note,omitempty"`
}

// OverviewResourceModel is a client-facing model of an access point.
type OverviewResourceModel struct {
	Name          string   `json:"name"`
	NativeModel   string   `json:"native_model"`
	ModelID       string   `json:"model_id,omitempty"`
	SurfaceRef    string   `json:"surface_ref"`
	Publisher     string   `json:"publisher,omitempty"`
	SurfaceType   string   `json:"surface_protocol,omitempty"`
	WireProtocols []string `json:"wire_protocols"`
	Resolved      bool     `json:"resolved"`
}

// OverviewResourceTool is one exposed tool connection.
type OverviewResourceTool struct {
	ConnectionRef string   `json:"connection_ref"`
	Expose        []string `json:"expose,omitempty"`
	Tools         []string `json:"tools"`
	Resolved      bool     `json:"resolved"`
}

// OverviewResourceSkill is one exposed skill server.
type OverviewResourceSkill struct {
	InstanceRef string   `json:"instance_ref"`
	Skills      []string `json:"skills"`
	Resolved    bool     `json:"resolved"`
}

// OverviewResources lists what an access point exposes.
type OverviewResources struct {
	Models []OverviewResourceModel `json:"models"`
	Tools  []OverviewResourceTool  `json:"tools"`
	Skills []OverviewResourceSkill `json:"skills"`
}

// OverviewAccessPointUsage is the ledger view of an access point.
type OverviewAccessPointUsage struct {
	Available    bool                 `json:"available"`
	Truncated    bool                 `json:"truncated"`
	Total        *OverviewUsage       `json:"total"`
	ByCallerKind []OverviewUsageGroup `json:"by_caller_kind"`
	ByModel      []OverviewUsageGroup `json:"by_model"`
	ByTool       []OverviewUsageGroup `json:"by_tool"`
}

// OverviewAccessPoint is the response of GET /overview/access-points/{id}.
type OverviewAccessPoint struct {
	AccessPoint AccessPoint              `json:"access_point"`
	Revision    uint64                   `json:"revision"`
	Since       time.Time                `json:"since"`
	Callers     OverviewCallers          `json:"callers"`
	Resources   OverviewResources        `json:"resources"`
	QoS         *AccessPointQoS          `json:"qos"`
	Quota       OverviewAccessPointQuota `json:"quota"`
	Balance     OverviewBalance          `json:"balance"`
	Usage       OverviewAccessPointUsage `json:"usage"`
	Notes       []string                 `json:"notes"`
}

func (h *OverviewHandler) apResources(ctx context.Context, tenant string, ap AccessPoint) OverviewResources {
	out := OverviewResources{Models: []OverviewResourceModel{}, Tools: []OverviewResourceTool{}, Skills: []OverviewResourceSkill{}}
	for _, m := range ap.Models {
		item := OverviewResourceModel{Name: m.Name, NativeModel: m.NativeModel, SurfaceRef: m.SurfaceRef, WireProtocols: []string{}}
		if conn, err := h.Connected.accessPointSurface(ctx, tenant, m.SurfaceRef); err == nil {
			item.Resolved, item.Publisher, item.SurfaceType = true, conn.Surface.Publisher, conn.Surface.Protocol
			item.WireProtocols = accessPointServedWires(conn.Surface, m.NativeModel)
			if id, err := conn.Surface.ModelID(m.NativeModel); err == nil {
				item.ModelID = id
			}
		}
		out.Models = append(out.Models, item)
	}
	for _, t := range ap.Tools {
		item := OverviewResourceTool{ConnectionRef: t.ConnectionRef, Expose: t.Expose, Tools: []string{}}
		if conn, err := h.Connected.accessPointToolConnection(ctx, tenant, t.ConnectionRef); err == nil {
			item.Resolved = true
			expose := overviewExposePolicy(t.Expose)
			for _, name := range conn.Execution.AllowedTools {
				if expose.Permits(name) && (conn.Tools == nil || conn.Tools.Permits(name)) {
					item.Tools = append(item.Tools, name)
				}
			}
		}
		out.Tools = append(out.Tools, item)
	}
	for _, s := range ap.Skills {
		item := OverviewResourceSkill{InstanceRef: s.InstanceRef, Skills: []string{}}
		if names := h.skillNames(ctx, tenant, s.InstanceRef); names != nil {
			item.Resolved, item.Skills = true, names
		}
		out.Skills = append(out.Skills, item)
	}
	return out
}

func (h *OverviewHandler) apCallers(ctx context.Context, tenant string, ap AccessPoint, since time.Time, d *overviewDirectory) (OverviewCallers, bool, error) {
	out := OverviewCallers{CallerKinds: ap.CallerKinds, Open: []string{}, Users: []OverviewCaller{}, Agents: []OverviewCaller{}, Clients: []OverviewCaller{}, Groups: ap.Subjects.Groups}
	if out.Groups == nil {
		out.Groups = []quotaplex.GroupRef{}
	}
	s := ap.Subjects
	identityConstrained := len(s.Principals)+len(s.Groups)+len(s.Agents) > 0
	for _, kind := range ap.CallerKinds {
		if !identityConstrained && (kind != CallerClient || len(s.ClientRegistrations) == 0) {
			out.Open = append(out.Open, kind)
		}
	}
	if len(s.Groups) > 0 {
		out.Note = "Members of declared groups are not enumerable; they are listed only once observed in the ledger."
	}
	rows := map[string]map[string]*OverviewCaller{CallerUser: {}, CallerAgent: {}, CallerClient: {}}
	get := func(kind, id string) *OverviewCaller {
		c := rows[kind][id]
		if c == nil {
			c = &OverviewCaller{ID: id}
			rows[kind][id] = c
		}
		return c
	}
	for _, p := range s.Principals {
		get(CallerUser, p).Declared = true
	}
	for _, a := range s.Agents {
		get(CallerAgent, d.agentID(a)).Declared = true
	}
	for _, c := range s.ClientRegistrations {
		get(CallerClient, c).Declared = true
	}
	truncated := false
	if h.Ledger != nil {
		for _, kind := range []string{CallerUser, CallerAgent, CallerClient} {
			scan, err := h.scanSubjects(ctx, tenant, kind, since, ap.ID, d)
			if err != nil {
				return out, false, err
			}
			truncated = truncated || scan.Truncated
			for _, g := range scan.Groups {
				id := g.Key["principal_id"]
				c := get(kind, id)
				c.Observed, c.Usage = true, overviewUsageOf(g)
				if t, ok := scan.Last[id]; ok {
					tt := t
					c.LastSeen = &tt
				}
			}
		}
	}
	for kind, m := range rows {
		list := make([]OverviewCaller, 0, len(m))
		for _, id := range overviewSortedKeys(m) {
			c := m[id]
			switch kind {
			case CallerAgent:
				if a := d.agents[id]; a != nil {
					c.DisplayName = a.DisplayName
				}
			case CallerClient:
				reg, found := d.clients[id]
				_, c.DisplayName = overviewClientIdentity(reg, found)
			}
			list = append(list, *c)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		if len(list) > 200 {
			list = list[:200]
		}
		switch kind {
		case CallerUser:
			out.Users = list
		case CallerAgent:
			out.Agents = list
		case CallerClient:
			out.Clients = list
		}
	}
	return out, truncated, nil
}

// AccessPointOverview handles GET /overview/access-points/{id}.
func (h *OverviewHandler) AccessPointOverview(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	if err := overviewParams(r, "since"); err != nil {
		overviewQueryError(w, r, err.Error())
		return
	}
	since, err := parseOverviewSince(r.URL.Query().Get("since"), h.now())
	if err != nil {
		overviewQueryError(w, r, err.Error())
		return
	}
	id := chi.URLParam(r, "id")
	record, ap, err := h.Connected.loadAccessPointRecord(r.Context(), tenant, id)
	if err != nil {
		if errors.Is(err, resources.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "access point not found")
		} else {
			Error(w, r, http.StatusConflict, "ACCESS_POINT_INVALID", "saved access point is invalid")
		}
		return
	}
	ap = overviewNormalizeAccessPoint(ap)
	dir, err := h.loadDirectory(r.Context(), tenant)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "OVERVIEW_UNAVAILABLE", "tenant configuration is unavailable")
		return
	}
	out := OverviewAccessPoint{AccessPoint: ap, Revision: record.Revision, Since: since, QoS: ap.QoS, Notes: []string{}}
	out.Resources = h.apResources(r.Context(), tenant, ap)
	out.Quota = h.apQuota(tenant, ap)
	out.Balance = OverviewBalance{Reason: overviewBalanceReason}
	callers, truncated, err := h.apCallers(r.Context(), tenant, ap, since, dir)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "USAGE_LEDGER_UNAVAILABLE", "usage ledger is unavailable")
		return
	}
	out.Callers = callers
	out.Usage = OverviewAccessPointUsage{Available: h.Ledger != nil, ByCallerKind: []OverviewUsageGroup{}, ByModel: []OverviewUsageGroup{}, ByTool: []OverviewUsageGroup{}}
	if h.Ledger == nil {
		out.Notes = append(out.Notes, "The usage ledger is not configured; usage is unavailable.")
	} else {
		filter := usageledger.Filter{Since: since, AccessPointID: id}
		total, err := h.scan(r.Context(), tenant, filter, []string{}, nil)
		if err != nil {
			Error(w, r, http.StatusServiceUnavailable, "USAGE_LEDGER_UNAVAILABLE", "usage ledger is unavailable")
			return
		}
		out.Usage.Total = &OverviewUsage{Tokens: map[string]int64{}, TokensUnknown: map[string]int64{}, Cost: []OverviewCost{}}
		if len(total.Groups) == 1 {
			out.Usage.Total = overviewUsageOf(total.Groups[0])
		}
		out.Usage.Truncated = total.Truncated || truncated
		if byKind, err := h.scan(r.Context(), tenant, filter, []string{"caller_kind"}, nil); err == nil {
			out.Usage.ByCallerKind = overviewUsageGroups(byKind, []string{"caller_kind"})
		}
		if byModel, err := h.scan(r.Context(), tenant, filter, []string{"model_id"}, func(rec *usageledger.Record) bool { return rec.ModelID != "" }); err == nil {
			out.Usage.ByModel = overviewUsageGroups(byModel, []string{"model_id"})
		}
		if byTool, err := h.scan(r.Context(), tenant, filter, []string{"tool_name"}, func(rec *usageledger.Record) bool { return rec.ToolName != "" }); err == nil {
			out.Usage.ByTool = overviewUsageGroups(byTool, []string{"tool_name"})
		}
		if out.Usage.Truncated {
			out.Notes = append(out.Notes, "The ledger scan reached its record bound; usage covers the newest records only.")
		}
	}
	JSON(w, http.StatusOK, out)
}
