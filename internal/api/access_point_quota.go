package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// AccessPointQuotaFragment is the reviewable QuotaPlex rendering of a QoS
// block. Rate and concurrency controls are scoped per principal and select
// the access point through the access_point_id match dimension.
type AccessPointQuotaFragment struct {
	Meters      map[string]quotaplex.Meter  `json:"meters,omitempty"`
	Rates       []quotaplex.RateRule        `json:"rates,omitempty"`
	Concurrency []quotaplex.ConcurrencyRule `json:"concurrency,omitempty"`
	// BudgetRefs are existing budgets of the tenant policy, shown unchanged:
	// they apply to access-point traffic through their own selectors.
	BudgetRefs []quotaplex.BudgetRule `json:"budget_refs,omitempty"`
}

// AccessPointSchedulerRoute mirrors a quota scheduler route; access_point_id
// selects the flow for traffic that entered through the access point.
type AccessPointSchedulerRoute struct {
	ResourceID    string `json:"resource_id"`
	OperationID   string `json:"operation_id"`
	AccessPointID string `json:"access_point_id"`
	FlowID        string `json:"flow_id"`
}

// AccessPointSchedulerFragment is the weighted-fair-queue contribution: one
// flow and its weight, to be merged into a configured scheduler pool.
type AccessPointSchedulerFragment struct {
	Class  string                      `json:"class"`
	FlowID string                      `json:"flow_id"`
	Weight uint32                      `json:"weight"`
	Routes []AccessPointSchedulerRoute `json:"routes"`
}

type AccessPointQuotaValidation struct {
	Valid     bool                 `json:"valid"`
	Validator string               `json:"validator"` // engine (against the active policy) or compile
	Reference *quotaplex.PolicyRef `json:"reference,omitempty"`
	Error     string               `json:"error,omitempty"`
}

type AccessPointQuotaPlan struct {
	AccessPointID string                        `json:"access_point_id"`
	TenantID      string                        `json:"tenant_id"`
	Applied       bool                          `json:"applied"`
	Fragment      AccessPointQuotaFragment      `json:"fragment"`
	Scheduler     *AccessPointSchedulerFragment `json:"scheduler,omitempty"`
	Policy        quotaplex.Policy              `json:"policy"`
	Validation    AccessPointQuotaValidation    `json:"validation"`
	Warnings      []string                      `json:"warnings"`
}

func meterOfUnit(meters map[string]quotaplex.Meter, unit, preferred string) (string, bool, bool) {
	if m, ok := meters[preferred]; ok && m.Unit == unit {
		return preferred, true, false
	}
	var found []string
	for id, m := range meters {
		if m.Unit == unit {
			found = append(found, id)
		}
	}
	if len(found) == 1 {
		return found[0], true, false
	}
	return preferred, false, len(found) > 1
}

// renderAccessPointQuota renders the QoS block onto a base policy (the
// tenant's active policy, or a standalone policy when none is configured).
func renderAccessPointQuota(tenant string, ap AccessPoint, base *quotaplex.Policy, now time.Time) (AccessPointQuotaPlan, error) {
	if ap.QoS == nil {
		return AccessPointQuotaPlan{}, fmt.Errorf("access point has no qos block")
	}
	plan := AccessPointQuotaPlan{AccessPointID: ap.ID, TenantID: tenant, Warnings: []string{}}
	prefix := "ap-" + ap.ID + "-"
	var policy quotaplex.Policy
	if base != nil {
		policy = *base
		policy.Revision++
		if policy.EffectiveAt.Before(now) {
			policy.EffectiveAt = now
		}
	} else {
		policy = quotaplex.Policy{Version: quotaplex.Version, TenantID: tenant, ID: "access-point-" + ap.ID, Revision: 1, EffectiveAt: now, Meters: map[string]quotaplex.Meter{}, Accounts: []quotaplex.Account{{ID: "access-point-" + ap.ID, TenantWide: true}}}
		plan.Warnings = append(plan.Warnings, "no quota policy is active for this tenant; rendered a standalone policy with one tenant-wide account")
	}
	meters := map[string]quotaplex.Meter{}
	for id, m := range policy.Meters {
		meters[id] = m
	}
	fragmentMeters := map[string]quotaplex.Meter{}
	match := quotaplex.Match{AccessPointID: ap.ID}
	principal := quotaplex.Scope{Kind: "principal"}
	var rates []quotaplex.RateRule
	var concurrency []quotaplex.ConcurrencyRule
	if l := ap.QoS.Limits; l != nil {
		if l.RequestsPerMinute > 0 {
			meter, ok, ambiguous := meterOfUnit(meters, "requests", "requests")
			if ambiguous {
				plan.Warnings = append(plan.Warnings, "several request meters exist; requests_per_minute uses meter \"requests\"")
			}
			if !ok {
				meters[meter] = quotaplex.Meter{ID: meter, Unit: "requests"}
				fragmentMeters[meter] = meters[meter]
			}
			rates = append(rates, quotaplex.RateRule{ID: prefix + "rpm", Scope: principal, Match: match, MeterID: meter, Rate: l.RequestsPerMinute, Interval: time.Minute, Burst: l.RequestsPerMinute, Basis: "admitted"})
		}
		if l.TokensPerMinute > 0 {
			meter, ok, ambiguous := meterOfUnit(meters, "tokens", "tokens")
			if ambiguous {
				plan.Warnings = append(plan.Warnings, "several token meters exist; tokens_per_minute uses meter \"tokens\", which every model binding through this access point must map")
			}
			if !ok {
				meters[meter] = quotaplex.Meter{ID: meter, Unit: "tokens"}
				fragmentMeters[meter] = meters[meter]
			}
			tokenMatch := match
			tokenMatch.Plane = "llmplex"
			rates = append(rates, quotaplex.RateRule{ID: prefix + "tpm", Scope: principal, Match: tokenMatch, MeterID: meter, Rate: l.TokensPerMinute, Interval: time.Minute, Burst: l.TokensPerMinute, Basis: "admitted"})
			plan.Warnings = append(plan.Warnings, "tokens_per_minute admits on the bounded token exposure of meter \""+meter+"\"; a model binding without that meter is denied (unsupported_meter)")
		}
		if l.Concurrency > 0 {
			concurrency = append(concurrency, quotaplex.ConcurrencyRule{ID: prefix + "concurrency", Scope: principal, Match: match, Limit: l.Concurrency})
		}
	}
	if len(rates)+len(concurrency) == 0 {
		plan.Warnings = append(plan.Warnings, "qos has no limits: requests fail closed until some quota control matches access_point_id "+ap.ID)
	}
	for _, ref := range ap.QoS.BudgetRefs {
		for _, rule := range policy.Budgets {
			if rule.ID == ref {
				plan.Fragment.BudgetRefs = append(plan.Fragment.BudgetRefs, rule)
			}
		}
	}
	// Re-rendering replaces this access point's earlier controls.
	keepRates := policy.Rates[:0:0]
	for _, rule := range policy.Rates {
		if rule.ID != prefix+"rpm" && rule.ID != prefix+"tpm" {
			keepRates = append(keepRates, rule)
		}
	}
	keepConcurrency := policy.Concurrency[:0:0]
	for _, rule := range policy.Concurrency {
		if rule.ID != prefix+"concurrency" {
			keepConcurrency = append(keepConcurrency, rule)
		}
	}
	policy.Meters = meters
	policy.Rates = append(keepRates, rates...)
	policy.Concurrency = append(keepConcurrency, concurrency...)
	if len(fragmentMeters) > 0 {
		plan.Fragment.Meters = fragmentMeters
	}
	plan.Fragment.Rates, plan.Fragment.Concurrency, plan.Policy = rates, concurrency, policy
	return plan, nil
}

// schedulerFragment renders the QoS weight as one flow with exact routes for
// every operation reachable through the access point.
func (h *ConnectedHandler) schedulerFragment(r *http.Request, tenant string, ap AccessPoint) *AccessPointSchedulerFragment {
	if ap.QoS == nil || ap.QoS.Weight == 0 {
		return nil
	}
	flow := "ap-" + ap.ID + "-" + ap.QoS.Class
	fragment := &AccessPointSchedulerFragment{Class: ap.QoS.Class, FlowID: flow, Weight: ap.QoS.Weight, Routes: []AccessPointSchedulerRoute{}}
	seen := map[string]bool{}
	add := func(resource, operation string) {
		key := resource + "\x00" + operation
		if !seen[key] {
			seen[key] = true
			fragment.Routes = append(fragment.Routes, AccessPointSchedulerRoute{ResourceID: resource, OperationID: operation, AccessPointID: ap.ID, FlowID: flow})
		}
	}
	for _, m := range ap.Models {
		if connection, err := h.accessPointSurface(r.Context(), tenant, m.SurfaceRef); err == nil {
			for _, op := range connection.Surface.Operations {
				add(m.SurfaceRef, op.ID)
			}
		}
	}
	for _, t := range ap.Tools {
		if connection, err := h.accessPointToolConnection(r.Context(), tenant, t.ConnectionRef); err == nil {
			for _, name := range connection.Execution.AllowedTools {
				if t.exposes(name) {
					add(t.ConnectionRef, "tools/call:"+name)
				}
			}
		}
	}
	sort.Slice(fragment.Routes, func(i, j int) bool {
		a, b := fragment.Routes[i], fragment.Routes[j]
		if a.ResourceID != b.ResourceID {
			return a.ResourceID < b.ResourceID
		}
		return a.OperationID < b.OperationID
	})
	return fragment
}

// AccessPointQuotaPlan renders and validates the QoS policy fragment. It
// never activates a policy; apply it through the existing policy flow.
func (h *ConnectedHandler) AccessPointQuotaPlan(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdministrator(w, r) {
		return
	}
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	id := idParam(r)
	record, _, err := h.loadAccessPointRecord(r.Context(), tenant, id)
	if err != nil {
		accessPointLoadError(w, r, err, func(err error) bool { return errors.Is(err, resources.ErrNotFound) })
		return
	}
	ap, err := h.validateAccessPoint(r.Context(), tenant, id, record.Spec)
	if err != nil {
		Error(w, r, http.StatusConflict, "ACCESS_POINT_INVALID", err.Error())
		return
	}
	if ap.QoS == nil {
		Error(w, r, http.StatusBadRequest, "NO_QOS", "access point has no qos block")
		return
	}
	clock := time.Now
	if h.Quota != nil && h.Quota.clock != nil {
		clock = h.Quota.clock // effective time on the admission clock
	}
	now := clock().UTC().Truncate(time.Second)
	var base *quotaplex.Policy
	var engine *quotaplex.Engine
	expected := ""
	if h.Quota != nil {
		engine = h.Quota.Engines()[tenant]
	}
	if engine != nil {
		inspection, err := engine.InspectPolicy(r.Context(), tenant)
		if err != nil && !errors.Is(err, quotaplex.ErrNotFound) {
			Error(w, r, http.StatusServiceUnavailable, "QUOTA_UNAVAILABLE", "quota policy is unavailable")
			return
		}
		policy := inspection.Policy
		if inspection.Active.Revision > policy.Revision {
			policy.Revision = inspection.Active.Revision
		}
		base, expected = &policy, inspection.Active.Digest
	}
	plan, err := renderAccessPointQuota(tenant, ap, base, now)
	if err != nil {
		Error(w, r, http.StatusBadRequest, "NO_QOS", err.Error())
		return
	}
	plan.Scheduler = h.schedulerFragment(r, tenant, ap)
	if engine != nil {
		plan.Validation.Validator = "engine"
		artifact, err := engine.ValidatePolicy(r.Context(), plan.Policy, expected)
		if err == nil {
			plan.Validation.Valid, plan.Validation.Reference = true, &artifact.Reference
		} else {
			plan.Validation.Error = err.Error()
		}
	} else {
		plan.Validation.Validator = "compile"
		compiled, err := quotaplex.Compile(plan.Policy)
		if err == nil {
			ref := compiled.Reference()
			plan.Validation.Valid, plan.Validation.Reference = true, &ref
		} else {
			plan.Validation.Error = err.Error()
		}
	}
	JSON(w, http.StatusOK, plan)
}
