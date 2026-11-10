package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// accessPointCaller is derived only from the verified identity and saved
// tenant resources.
type accessPointCaller struct {
	Kind string
	// ClientID is a ClientPlex registration id equal to the verified token's
	// authorized party (azp), or empty when no registration matches.
	ClientID string
}

// resolveAccessPointCaller derives caller_kind:
//   - agent  when the token carries a delegated actor (RFC 8693 act) or the
//     principal is a workload (non-workforce) identity;
//   - client when the verified authorized party (azp) equals the id of a
//     ClientPlex registration in the tenant;
//   - user   otherwise.
//
// The client id is resolved independently of the kind so that an agent or a
// user presenting a registered client is still attributed to it. A public
// OAuth client id is attribution, not a security boundary.
func (h *ConnectedHandler) resolveAccessPointCaller(ctx context.Context, tenant string, identity *models.WIFIdentity) (accessPointCaller, error) {
	if identity == nil || identity.PrincipalID == "" {
		return accessPointCaller{}, fmt.Errorf("verified identity required")
	}
	caller := accessPointCaller{}
	if azp := identity.AuthorizedParty; azp != "" && len(azp) <= 256 {
		record, err := h.Store.Get(ctx, tenant, clientplex.Kind, azp)
		switch {
		case err == nil:
			var registration clientplex.Registration
			if json.Unmarshal(record.Spec, &registration) == nil && registration.ID == azp && record.TenantID == tenant {
				caller.ClientID = azp
			}
		case errors.Is(err, resources.ErrNotFound):
		default:
			return accessPointCaller{}, err
		}
	}
	switch {
	case identity.ActorID != "" || !identity.IsWorkforce:
		caller.Kind = CallerAgent
	case caller.ClientID != "":
		caller.Kind = CallerClient
	default:
		caller.Kind = CallerUser
	}
	return caller, nil
}

// identityGroups returns the verified groups of the principal, or nil when
// the verifier recorded that the group view is missing or invalid. Missing
// groups never satisfy a group constraint.
func identityGroups(identity *models.WIFIdentity) []string {
	if e := identity.VerifiedEvidence; e != nil && (!e.GroupsPresent || !e.GroupsValid) {
		return nil
	}
	return identity.Groups
}

// admits applies caller_kinds and subjects. Identity constraints (principals,
// groups, agents) and the client constraint are ANDed when both are set.
func (ap AccessPoint) admits(identity *models.WIFIdentity, caller accessPointCaller) bool {
	kindAllowed := false
	for _, kind := range ap.CallerKinds {
		if kind == caller.Kind {
			kindAllowed = true
		}
	}
	if !kindAllowed {
		return false
	}
	s := ap.Subjects
	if len(s.Principals)+len(s.Groups)+len(s.Agents) > 0 {
		matched := false
		for _, p := range s.Principals {
			if p == identity.PrincipalID {
				matched = true
			}
		}
		agentID := identity.ActorID
		if agentID == "" && !identity.IsWorkforce {
			agentID = identity.PrincipalID
		}
		for _, a := range s.Agents {
			if agentID != "" && a == agentID {
				matched = true
			}
		}
		if len(s.Groups) > 0 {
			source := auth.MembershipSource(identity.Provider, identity.PoolID)
			for _, member := range identityGroups(identity) {
				for _, g := range s.Groups {
					if g == (quotaplex.GroupRef{Source: source, ID: member}) {
						matched = true
					}
				}
			}
		}
		if !matched {
			return false
		}
	}
	if len(s.ClientRegistrations) > 0 {
		matched := false
		for _, c := range s.ClientRegistrations {
			if caller.ClientID != "" && c == caller.ClientID {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// accessPointCall travels in the request context from the access-point gate
// to the managed handlers, quota host and usage ledger.
type accessPointCall struct {
	ID         string
	CallerKind string
	ClientID   string
	Revision   uint64
	QoS        bool
	Weight     uint32 // QoS weight; zero without QoS
	recheck    func(context.Context) error
	// missingGrant is set by the model authorizer when it refuses a caller the
	// access point admitted: the operator-facing explanation of which grants
	// are missing. It only changes the wording of the refusal; it never
	// permits anything.
	missingGrant string
}

type accessPointKey struct{}

func withAccessPointCall(ctx context.Context, call *accessPointCall) context.Context {
	return context.WithValue(ctx, accessPointKey{}, call)
}

func accessPointCallFrom(ctx context.Context) *accessPointCall {
	call, _ := ctx.Value(accessPointKey{}).(*accessPointCall)
	return call
}

// recheckCall confirms, at dispatch, that the access point is unchanged and
// still admits the caller. A nil call (no access point) always passes.
func (c *accessPointCall) recheckCall(ctx context.Context) error {
	if c == nil || c.recheck == nil {
		return nil
	}
	return c.recheck(ctx)
}

// applyQuotaTarget adds the access-point dimensions to a quota target. With
// a QoS block the tenant policy must carry a control for this access point;
// otherwise admission fails closed rather than silently ignoring the QoS.
func (c *accessPointCall) applyQuotaTarget(policy quotaplex.Policy, target *quotaplex.Target) error {
	if c == nil {
		return nil
	}
	target.AccessPointID, target.CallerKind, target.ClientID = c.ID, c.CallerKind, c.ClientID
	if !c.QoS {
		return nil
	}
	covered := func(m quotaplex.Match) bool {
		return m.AccessPointID == c.ID && quotaplex.MatchesTarget(m, *target)
	}
	for _, rule := range policy.Budgets {
		if covered(rule.Match) {
			return nil
		}
	}
	for _, rule := range policy.Rates {
		if covered(rule.Match) {
			return nil
		}
	}
	for _, rule := range policy.Concurrency {
		if covered(rule.Match) {
			return nil
		}
	}
	return quotaStatus(503, "QUOTA_ACCESS_POINT_UNCOVERED", "access point has a QoS block but no active quota control matches it")
}

// accessPointErrorWriter renders a gate failure in the endpoint's own shape.
type accessPointErrorWriter func(w http.ResponseWriter, r *http.Request, status int, code, message string)

// accessPointGate resolves tenant, verified identity, the saved access point
// and the caller, and checks caller_kinds and subjects before anything else.
// Only then are the access point's references re-resolved.
func (h *ConnectedHandler) accessPointGate(w http.ResponseWriter, r *http.Request, fail accessPointErrorWriter) (string, AccessPoint, *accessPointCall, bool) {
	identity := GetWIFIdentity(r.Context())
	if identity == nil || identity.PrincipalID == "" {
		fail(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
		return "", AccessPoint{}, nil, false
	}
	tenant, err := h.Tenant(r)
	if err != nil || tenant == "" {
		fail(w, r, http.StatusForbidden, "TENANT_REQUIRED", "verified tenant binding required")
		return "", AccessPoint{}, nil, false
	}
	id := chi.URLParam(r, "apid")
	record, ap, err := h.loadAccessPointRecord(r.Context(), tenant, id)
	if err != nil {
		if errors.Is(err, resources.ErrNotFound) {
			fail(w, r, http.StatusNotFound, "NOT_FOUND", "access point not found")
		} else {
			fail(w, r, http.StatusServiceUnavailable, "ACCESS_POINT_UNAVAILABLE", "access point configuration is unavailable")
		}
		return "", AccessPoint{}, nil, false
	}
	caller, err := h.resolveAccessPointCaller(r.Context(), tenant, identity)
	if err != nil {
		fail(w, r, http.StatusServiceUnavailable, "CALLER_UNAVAILABLE", "caller resolution is unavailable")
		return "", AccessPoint{}, nil, false
	}
	if !ap.admits(identity, caller) {
		fail(w, r, http.StatusForbidden, "CALLER_NOT_ALLOWED", "caller kind "+caller.Kind+" or identity is not admitted by this access point")
		return "", AccessPoint{}, nil, false
	}
	if ap, err = h.validateAccessPoint(r.Context(), tenant, id, record.Spec); err != nil {
		fail(w, r, http.StatusServiceUnavailable, "ACCESS_POINT_UNAVAILABLE", "access point references are unavailable")
		return "", AccessPoint{}, nil, false
	}
	revision := record.Revision
	call := &accessPointCall{ID: id, CallerKind: caller.Kind, ClientID: caller.ClientID, Revision: revision, QoS: ap.QoS != nil}
	if ap.QoS != nil {
		call.Weight = ap.QoS.Weight
	}
	call.recheck = func(ctx context.Context) error {
		current, currentAP, err := h.loadAccessPointRecord(ctx, tenant, id)
		if err != nil || current.Revision != revision {
			return fmt.Errorf("access point changed")
		}
		now, err := h.resolveAccessPointCaller(ctx, tenant, identity)
		if err != nil || now != caller || !currentAP.admits(identity, now) {
			return fmt.Errorf("caller no longer admitted")
		}
		return nil
	}
	return tenant, ap, call, true
}

// delegateRequest clones r for a managed handler: new path, body and route
// parameters, the same verified context, plus the access-point call.
func delegateRequest(r *http.Request, call *accessPointCall, path, resourceID string, body []byte) *http.Request {
	route := chi.NewRouteContext()
	route.URLParams.Add("id", resourceID)
	ctx := context.WithValue(withAccessPointCall(r.Context(), call), chi.RouteCtxKey, route)
	cloned := r.Clone(ctx)
	copied := *r.URL
	cloned.URL = &copied
	cloned.URL.Path = path
	cloned.URL.RawPath = ""
	cloned.URL.RawQuery = r.URL.RawQuery
	cloned.RequestURI = ""
	cloned.Body = http.NoBody
	cloned.ContentLength = int64(len(body))
	if body != nil {
		cloned.Body = readCloser(body)
	}
	cloned.Header = r.Header.Clone()
	cloned.Header.Del("Content-Length")
	return cloned
}
