package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

// Admission and grant states. "unknown" is a real answer: the server does not
// hold the facts (for example a user's group memberships) to decide.
const (
	overviewYes     = "yes"
	overviewNo      = "no"
	overviewUnknown = "unknown"

	overviewGranted    = "granted"
	overviewNotGranted = "not_granted"
)

// OverviewIdentity says how a subject authenticates.
type OverviewIdentity struct {
	Source string `json:"source"`
	Detail string `json:"detail"`
}

// OverviewSubject is one row of the subject table.
type OverviewSubject struct {
	Kind         string           `json:"kind"`
	ID           string           `json:"id"`
	DisplayName  string           `json:"display_name,omitempty"`
	Identity     OverviewIdentity `json:"identity"`
	AccessPoints []string         `json:"access_points"`
	LastSeen     *time.Time       `json:"last_seen"`
	Usage        *OverviewUsage   `json:"usage"`
	// KnownFrom names the sources that made this subject known.
	KnownFrom []string `json:"known_from"`
}

// OverviewSubjectPage is the response of GET /overview/subjects.
type OverviewSubjectPage struct {
	Kind           string            `json:"kind"`
	Since          time.Time         `json:"since"`
	Subjects       []OverviewSubject `json:"subjects"`
	NextCursor     string            `json:"next_cursor,omitempty"`
	UsageAvailable bool              `json:"usage_available"`
	UsageTruncated bool              `json:"usage_truncated"`
	DerivedFrom    []string          `json:"derived_from"`
	Limitations    []string          `json:"limitations"`
}

// ── directory ────────────────────────────────────────────────────────────

type overviewAgent struct {
	ID          string
	DisplayName string
	Aliases     map[string]bool
	Bindings    []agentfederation.Binding
	Registry    *models.Agent
}

type overviewDirectory struct {
	tenant   string
	aps      []AccessPoint
	invalid  int
	agents   map[string]*overviewAgent
	alias    map[string]string
	registry []models.Agent
	clients  map[string]clientplex.Registration
}

func (d *overviewDirectory) ensureAgent(id string) *overviewAgent {
	a := d.agents[id]
	if a == nil {
		a = &overviewAgent{ID: id, Aliases: map[string]bool{id: true}}
		d.agents[id] = a
		d.alias[id] = id
	}
	return a
}

func (d *overviewDirectory) addAlias(a *overviewAgent, alias string) {
	if alias != "" {
		a.Aliases[alias] = true
		d.alias[alias] = a.ID
	}
}

func (d *overviewDirectory) attachRegistry(a *overviewAgent, reg *models.Agent) {
	a.Registry = reg
	if a.DisplayName == "" {
		a.DisplayName = reg.DisplayName
	}
	d.addAlias(a, reg.ClientID)
	d.addAlias(a, reg.SpiffeID)
}

// agentID resolves any identifier an agent appears under (client id, SPIFFE
// id, federated actor or principal id) to its registered client id. Unknown
// identifiers are returned unchanged. Only identifiers already evidenced in
// this tenant are passed in, so the global agent registry never adds agents
// to a tenant's view.
func (d *overviewDirectory) agentID(raw string) string {
	if id, ok := d.alias[raw]; ok {
		return id
	}
	for i := range d.registry {
		reg := &d.registry[i]
		if reg.ClientID == raw || (reg.SpiffeID != "" && reg.SpiffeID == raw) {
			a := d.ensureAgent(reg.ClientID)
			d.attachRegistry(a, reg)
			return a.ID
		}
	}
	return raw
}

func (h *OverviewHandler) loadDirectory(ctx context.Context, tenant string) (*overviewDirectory, error) {
	d := &overviewDirectory{tenant: tenant, agents: map[string]*overviewAgent{}, alias: map[string]string{}, clients: map[string]clientplex.Registration{}}
	records, err := h.Connected.Store.List(ctx, tenant, AccessPointKind)
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		if rec.TenantID != tenant || rec.Kind != AccessPointKind {
			continue
		}
		ap, err := decodeAccessPoint(rec.ID, rec.Spec)
		if err != nil {
			d.invalid++
			continue
		}
		d.aps = append(d.aps, ap)
	}
	sort.Slice(d.aps, func(i, j int) bool { return d.aps[i].ID < d.aps[j].ID })
	regs, err := h.Connected.Store.List(ctx, tenant, clientplex.Kind)
	if err != nil {
		return nil, err
	}
	for _, rec := range regs {
		var reg clientplex.Registration
		if rec.TenantID == tenant && json.Unmarshal(rec.Spec, &reg) == nil && reg.ID == rec.ID {
			d.clients[reg.ID] = reg
		}
	}
	if h.Agents != nil {
		if list, err := h.Agents.ListAgents(ctx); err == nil {
			d.registry = list
		}
	}
	if h.AgentBindings != nil {
		bindings, err := h.AgentBindings.List(ctx, tenant, "")
		if err != nil {
			return nil, err
		}
		pool := auth.FederationPoolID(tenant)
		for _, b := range bindings {
			a := d.ensureAgent(b.ClientID)
			a.Bindings = append(a.Bindings, b)
			if a.DisplayName == "" {
				a.DisplayName = b.DisplayName
			}
			d.addAlias(a, auth.CanonicalPrincipalID(b.Issuer, pool, b.Subject))
			for i := range d.registry {
				if d.registry[i].ClientID == b.ClientID {
					d.attachRegistry(a, &d.registry[i])
					d.addAlias(a, auth.CanonicalPrincipalID(b.Issuer, pool, auth.AgentActorSubject(&d.registry[i])))
				}
			}
		}
	}
	return d, nil
}

// ── identity ─────────────────────────────────────────────────────────────

func (d *overviewDirectory) agentIdentity(a *overviewAgent) OverviewIdentity {
	switch {
	case a != nil && len(a.Bindings) > 0:
		b := a.Bindings[0]
		detail := fmt.Sprintf("preset=%s issuer=%s %s=%s", b.Preset, b.Issuer, b.SubjectClaim, b.Subject)
		if len(a.Bindings) > 1 {
			detail += fmt.Sprintf(" (+%d more bindings)", len(a.Bindings)-1)
		}
		return OverviewIdentity{Source: "federated-identity", Detail: detail}
	case a != nil && a.Registry != nil && a.Registry.SpiffeID != "":
		return OverviewIdentity{Source: "spiffe", Detail: a.Registry.SpiffeID}
	case a != nil && a.Registry != nil && a.Registry.WIFPrincipal != "":
		return OverviewIdentity{Source: "workload-identity-federation", Detail: a.Registry.WIFPrincipal}
	case a != nil && a.Registry != nil:
		return OverviewIdentity{Source: "oauth-client", Detail: "auth_method=" + a.Registry.AuthMethod}
	}
	return OverviewIdentity{Source: overviewUnknown, Detail: "seen in this tenant's access points or usage ledger only; no identity binding or registered agent resolves it"}
}

func overviewUserIdentity(id string) OverviewIdentity {
	if auth.IsCanonicalPrincipalID(id) {
		return OverviewIdentity{Source: "workforce-principal", Detail: "canonical principal id; the issuer and subject are hashed into it and cannot be recovered from it"}
	}
	return OverviewIdentity{Source: "declared-principal", Detail: "named by an access point subject; not a canonical principal id"}
}

func overviewClientIdentity(reg clientplex.Registration, ok bool) (OverviewIdentity, string) {
	if !ok {
		return OverviewIdentity{Source: overviewUnknown, Detail: "no client registration with this id in the tenant"}, ""
	}
	name := reg.ID
	if p, err := clientplex.FindProfile(reg.ProfileID); err == nil {
		name = p.Name + " (" + reg.ID + ")"
	}
	return OverviewIdentity{Source: "client-registration", Detail: fmt.Sprintf("profile=%s mode=%s protocol=%s auth_mode=%s", reg.ProfileID, reg.Mode, reg.Protocol, reg.AuthMode)}, name
}

// ── admission ────────────────────────────────────────────────────────────

func overviewAnd(a, b string) string {
	if a == overviewNo || b == overviewNo {
		return overviewNo
	}
	if a == overviewUnknown || b == overviewUnknown {
		return overviewUnknown
	}
	return overviewYes
}

// overviewAdmits evaluates caller_kinds and subjects for a subject the way
// AccessPoint.admits does for a verified caller, but over stored facts only.
// Group membership is not stored for any subject, so group constraints yield
// "unknown". For clients, identity constraints apply to the end user who
// presents the client token, so they also yield "unknown".
func overviewAdmits(ap AccessPoint, kind string, aliases map[string]bool) (string, string) {
	kindOK := false
	for _, k := range ap.CallerKinds {
		if k == kind {
			kindOK = true
		}
	}
	if !kindOK {
		return overviewNo, "access point admits caller kinds " + strings.Join(ap.CallerKinds, ", ")
	}
	s := ap.Subjects
	state, why := overviewYes, "access point admits every caller of kind "+kind
	if len(s.Principals)+len(s.Groups)+len(s.Agents) > 0 {
		matched := false
		for _, p := range s.Principals {
			if aliases[p] {
				matched, why = true, "listed in subjects.principals"
			}
		}
		for _, a := range s.Agents {
			if kind == CallerAgent && aliases[a] {
				matched, why = true, "listed in subjects.agents"
			}
		}
		switch {
		case kind == CallerClient:
			state, why = overviewUnknown, "identity constraints apply to the end user presenting this client"
		case matched:
			state = overviewYes
		case len(s.Groups) > 0:
			state, why = overviewUnknown, "admitted if a member of a listed group; group membership is not stored"
		default:
			state, why = overviewNo, "not listed in the access point subjects"
		}
	}
	if len(s.ClientRegistrations) > 0 {
		clientState, clientWhy := overviewUnknown, "admitted only through a listed client registration; the client presented is not known here"
		if kind == CallerClient {
			clientState, clientWhy = overviewNo, "client registration is not listed in subjects.client_registrations"
			for _, c := range s.ClientRegistrations {
				if aliases[c] {
					clientState, clientWhy = overviewYes, "listed in subjects.client_registrations"
				}
			}
		}
		if clientState != overviewYes {
			why = clientWhy
		}
		state = overviewAnd(state, clientState)
	}
	return state, why
}

// ── scope sources ────────────────────────────────────────────────────────

// OverviewReason says which configuration grants (or conditionally grants) an
// item.
type OverviewReason struct {
	Type        string `json:"type"` // access-point | scope | manual-scope | role-binding | agent-ceiling
	Ref         string `json:"ref,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Conditional bool   `json:"conditional,omitempty"`
}

type overviewScopeSource struct {
	Scope  string
	Reason OverviewReason
}

// overviewScopes is what is known about a subject's scope grants. When Known
// is false every item that is not granted outright is "unknown", never
// "not_granted".
type overviewScopes struct {
	Known   bool
	Note    string
	Sources []overviewScopeSource
}

func (s overviewScopes) check(required string) (string, []OverviewReason) {
	var granted, conditional []OverviewReason
	for _, src := range s.Sources {
		if scopeAllowed([]string{src.Scope}, required) {
			if src.Reason.Conditional {
				conditional = append(conditional, src.Reason)
			} else {
				granted = append(granted, src.Reason)
			}
		}
	}
	switch {
	case len(granted) > 0:
		return overviewGranted, granted
	case len(conditional) > 0:
		return overviewUnknown, conditional
	case !s.Known:
		return overviewUnknown, nil
	}
	return overviewNotGranted, nil
}

func (h *OverviewHandler) scopesFor(ctx context.Context, kind, id string, agent *overviewAgent) overviewScopes {
	switch kind {
	case CallerUser:
		out := overviewScopes{Known: h.UserScopes != nil && h.RoleBindings != nil, Note: "Group memberships are not stored, so role bindings are listed as conditional grants. Manual scopes are exact."}
		if h.UserScopes != nil {
			scopes, err := h.UserScopes.GetUserScopes(ctx, id)
			switch {
			case err == nil || isNotFound(err):
				for _, sc := range scopes {
					out.Sources = append(out.Sources, overviewScopeSource{sc, OverviewReason{Type: "manual-scope", Ref: id, Detail: "scope assigned directly to this principal"}})
				}
			default:
				out.Known = false
			}
		}
		if h.RoleBindings != nil {
			if bindings, err := h.RoleBindings.ListRoleBindings(ctx); err == nil {
				for _, b := range bindings {
					scopes := b.Scopes
					if scopes == nil {
						scopes = models.DefaultRoleScopes[b.Role]
					}
					for _, sc := range scopes {
						out.Sources = append(out.Sources, overviewScopeSource{sc, OverviewReason{Type: "role-binding", Ref: b.ID, Detail: fmt.Sprintf("role %s for group %q", b.Role, b.Group), Conditional: true}})
					}
				}
			} else {
				out.Known = false
			}
		}
		return out
	case CallerAgent:
		if agent != nil && agent.Registry != nil {
			out := overviewScopes{Known: true, Note: "Agent ceiling (dimension A) only. A token carries the intersection of this ceiling, the user ceiling and the user's consent."}
			for _, sc := range agent.Registry.AllowedScopes {
				out.Sources = append(out.Sources, overviewScopeSource{sc, OverviewReason{Type: "agent-ceiling", Ref: agent.Registry.ClientID, Detail: "allowed_scopes of the registered agent"}})
			}
			return out
		}
		return overviewScopes{Note: "No registered agent resolves this identity, so its scope ceiling is unknown."}
	}
	return overviewScopes{Note: "A client registration holds no scopes; scopes come from the token of the user or agent that presents it."}
}

func isNotFound(err error) bool {
	return err != nil && (errors.Is(err, resources.ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found"))
}

// ── access ───────────────────────────────────────────────────────────────

// OverviewAccessItem is one model, tool or skill a subject may reach.
type OverviewAccessItem struct {
	Kind          string           `json:"kind"` // model | tool | skill
	Name          string           `json:"name"`
	AccessPointID string           `json:"access_point_id"`
	Resource      string           `json:"resource,omitempty"`
	Required      string           `json:"required_scope,omitempty"`
	State         string           `json:"state"` // granted | not_granted | unknown
	Reasons       []OverviewReason `json:"reasons"`
}

// OverviewAccess is the effective-access section of a subject.
type OverviewAccess struct {
	Models      []OverviewAccessItem `json:"models"`
	Tools       []OverviewAccessItem `json:"tools"`
	Skills      []OverviewAccessItem `json:"skills"`
	ScopesKnown bool                 `json:"scopes_known"`
	ScopesNote  string               `json:"scopes_note"`
	Truncated   bool                 `json:"truncated"`
}

func overviewCombine(admission, scope string) string {
	switch {
	case scope == overviewNotGranted || admission == overviewNo:
		return overviewNotGranted
	case admission == overviewYes && scope == overviewGranted:
		return overviewGranted
	}
	return overviewUnknown
}

func (h *OverviewHandler) accessFor(ctx context.Context, tenant string, aps []AccessPoint, kind string, aliases map[string]bool, scopes overviewScopes) OverviewAccess {
	out := OverviewAccess{Models: []OverviewAccessItem{}, Tools: []OverviewAccessItem{}, Skills: []OverviewAccessItem{}, ScopesKnown: scopes.Known, ScopesNote: scopes.Note}
	total := 0
	add := func(list *[]OverviewAccessItem, item OverviewAccessItem) {
		if total >= overviewMaxAccess {
			out.Truncated = true
			return
		}
		total++
		*list = append(*list, item)
	}
	for _, ap := range aps {
		admission, why := overviewAdmits(ap, kind, aliases)
		if admission == overviewNo {
			continue
		}
		apReason := OverviewReason{Type: "access-point", Ref: ap.ID, Detail: why, Conditional: admission != overviewYes}
		build := func(itemKind, name, resource, required string) OverviewAccessItem {
			item := OverviewAccessItem{Kind: itemKind, Name: name, AccessPointID: ap.ID, Resource: resource, Required: required, Reasons: []OverviewReason{apReason}}
			state, reasons := scopes.check(required)
			item.State = overviewCombine(admission, state)
			item.Reasons = append(item.Reasons, reasons...)
			return item
		}
		for _, m := range ap.Models {
			conn, err := h.Connected.accessPointSurface(ctx, tenant, m.SurfaceRef)
			if err != nil {
				continue
			}
			modelID, err := conn.Surface.ModelID(m.NativeModel)
			if err != nil {
				continue
			}
			add(&out.Models, build("model", m.Name, modelID, "llm:model:"+modelID))
		}
		for _, t := range ap.Tools {
			conn, err := h.Connected.accessPointToolConnection(ctx, tenant, t.ConnectionRef)
			if err != nil {
				continue
			}
			expose := toolexec.ToolPolicy{Allow: t.Expose}
			for _, name := range conn.Execution.AllowedTools {
				if !expose.Permits(name) || (conn.Tools != nil && !conn.Tools.Permits(name)) {
					continue
				}
				add(&out.Tools, build("tool", t.ConnectionRef+"__"+name, t.ConnectionRef, "mcp:tools:"+name))
			}
		}
		for _, sk := range ap.Skills {
			names := h.skillNames(ctx, tenant, sk.InstanceRef)
			if names == nil {
				add(&out.Skills, OverviewAccessItem{Kind: "skill", Name: sk.InstanceRef, AccessPointID: ap.ID, Resource: sk.InstanceRef, State: overviewUnknown, Reasons: []OverviewReason{apReason, {Type: "scope", Detail: "skill server is not resolvable, so its skill names are unknown"}}})
				continue
			}
			for _, name := range names {
				add(&out.Skills, build("skill", name, sk.InstanceRef, "skill:invoke:"+name))
			}
		}
	}
	return out
}

// skillNames resolves a skill server's declared skills; nil means unresolved.
func (h *OverviewHandler) skillNames(ctx context.Context, tenant, ref string) []string {
	instance, err := h.Connected.accessPointSkillInstance(ctx, tenant, ref)
	if err != nil {
		return nil
	}
	template, err := h.Connected.AccessPoint.Skills.GetTemplate(ctx, instance.TemplateID)
	if err != nil || template == nil {
		return nil
	}
	names := []string{}
	for _, s := range deployedSkills(instance, template) {
		names = append(names, s.Name)
	}
	return names
}

// ── list ─────────────────────────────────────────────────────────────────

type overviewCandidate struct {
	ID        string
	KnownFrom map[string]bool
}

type overviewCandidates map[string]*overviewCandidate

func (c overviewCandidates) add(id, source string) {
	if id == "" {
		return
	}
	e := c[id]
	if e == nil {
		e = &overviewCandidate{ID: id, KnownFrom: map[string]bool{}}
		c[id] = e
	}
	e.KnownFrom[source] = true
}

func overviewValidKind(kind string) bool {
	return kind == CallerAgent || kind == CallerUser || kind == CallerClient
}

// scanSubjects scans the window once for a kind and returns usage keyed by
// subject id. Agents are keyed by their registered client id when one
// resolves, otherwise by the actor (or principal) identifier in the ledger.
func (h *OverviewHandler) scanSubjects(ctx context.Context, tenant, kind string, since time.Time, accessPointID string, d *overviewDirectory) (overviewScan, error) {
	filter := usageledger.Filter{Since: since, AccessPointID: accessPointID}
	mutate := func(rec *usageledger.Record) bool {
		switch kind {
		case CallerAgent:
			raw := rec.ActorID
			if raw == "" && rec.CallerKind == CallerAgent {
				raw = rec.PrincipalID
			}
			if raw == "" {
				return false
			}
			rec.PrincipalID = d.agentID(raw)
		case CallerUser:
			if rec.CallerKind != CallerUser || rec.PrincipalID == "" {
				return false
			}
		case CallerClient:
			if rec.ClientID == "" {
				return false
			}
			rec.PrincipalID = rec.ClientID
		}
		return true
	}
	return h.scan(ctx, tenant, filter, []string{"principal_id"}, mutate)
}

func overviewUserSources() []string {
	return []string{"access-point subjects.principals", "usage ledger (caller_kind=user) within the window"}
}

// Subjects handles GET /overview/subjects.
func (h *OverviewHandler) Subjects(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	if err := overviewParams(r, "kind", "since", "limit", "cursor"); err != nil {
		overviewQueryError(w, r, err.Error())
		return
	}
	kind := r.URL.Query().Get("kind")
	if !overviewValidKind(kind) {
		overviewQueryError(w, r, "kind must be agent, user or client")
		return
	}
	since, err := parseOverviewSince(r.URL.Query().Get("since"), h.now())
	if err != nil {
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
	dir, err := h.loadDirectory(r.Context(), tenant)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "OVERVIEW_UNAVAILABLE", "tenant configuration is unavailable")
		return
	}
	cands := overviewCandidates{}
	page := OverviewSubjectPage{Kind: kind, Since: since, Subjects: []OverviewSubject{}, UsageAvailable: h.Ledger != nil, Limitations: []string{}}
	switch kind {
	case CallerAgent:
		page.DerivedFrom = []string{"agent identity bindings", "access point subjects.agents", "usage ledger (actor_id) within the window"}
		for id := range dir.agents {
			cands.add(id, "identity-binding")
		}
		for _, ap := range dir.aps {
			for _, a := range ap.Subjects.Agents {
				cands.add(dir.agentID(a), "access-point")
			}
		}
	case CallerUser:
		page.DerivedFrom = overviewUserSources()
		page.Limitations = append(page.Limitations,
			"There is no user directory. Role bindings map groups, not users, and manual scopes can only be read for a known principal, so neither enumerates users.",
			"Users who have not appeared in the ledger window and are not named by an access point are not listed.")
		for _, ap := range dir.aps {
			for _, p := range ap.Subjects.Principals {
				cands.add(p, "access-point")
			}
		}
	case CallerClient:
		page.DerivedFrom = []string{"client registrations", "access point subjects.client_registrations", "usage ledger (client_id) within the window"}
		for id := range dir.clients {
			cands.add(id, "client-registration")
		}
		for _, ap := range dir.aps {
			for _, c := range ap.Subjects.ClientRegistrations {
				cands.add(c, "access-point")
			}
		}
	}
	scan := overviewScan{Last: map[string]time.Time{}}
	usage := map[string]usageledger.SummaryGroup{}
	if h.Ledger != nil {
		scan, err = h.scanSubjects(r.Context(), tenant, kind, since, "", dir)
		if err != nil {
			Error(w, r, http.StatusServiceUnavailable, "USAGE_LEDGER_UNAVAILABLE", "usage ledger is unavailable")
			return
		}
		for _, g := range scan.Groups {
			usage[g.Key["principal_id"]] = g
			cands.add(g.Key["principal_id"], "usage-ledger")
		}
		page.UsageTruncated = scan.Truncated
	} else {
		page.Limitations = append(page.Limitations, "The usage ledger is not configured; usage and last_seen are unavailable.")
	}
	if len(cands) > overviewMaxCandidates {
		page.Limitations = append(page.Limitations, fmt.Sprintf("more than %d subjects; the list is bounded", overviewMaxCandidates))
	}
	if scan.Truncated {
		page.Limitations = append(page.Limitations, "The ledger scan reached its record bound; usage covers the newest records only.")
	}
	ids := overviewSortedKeys(cands)
	if len(ids) > overviewMaxCandidates {
		ids = ids[:overviewMaxCandidates]
	}
	for _, id := range ids {
		if id <= after {
			continue
		}
		if len(page.Subjects) == limit {
			page.NextCursor = encodeOverviewCursor(page.Subjects[len(page.Subjects)-1].ID)
			break
		}
		page.Subjects = append(page.Subjects, h.subjectRow(r.Context(), tenant, kind, cands[id], dir, usage, scan))
	}
	JSON(w, http.StatusOK, page)
}

func (d *overviewDirectory) aliasesOf(kind, id string) map[string]bool {
	aliases := map[string]bool{id: true}
	if kind == CallerAgent {
		if a := d.agents[d.agentID(id)]; a != nil {
			for k := range a.Aliases {
				aliases[k] = true
			}
		}
	}
	return aliases
}

func (d *overviewDirectory) accessPointIDs(kind, id string) []string {
	aliases := d.aliasesOf(kind, id)
	out := []string{}
	for _, ap := range d.aps {
		if state, _ := overviewAdmits(ap, kind, aliases); state != overviewNo {
			out = append(out, ap.ID)
		}
	}
	return out
}

func (h *OverviewHandler) subjectRow(ctx context.Context, tenant, kind string, c *overviewCandidate, d *overviewDirectory, usage map[string]usageledger.SummaryGroup, scan overviewScan) OverviewSubject {
	row := OverviewSubject{Kind: kind, ID: c.ID, AccessPoints: d.accessPointIDs(kind, c.ID), KnownFrom: overviewSortedKeys(c.KnownFrom)}
	switch kind {
	case CallerAgent:
		a := d.agents[c.ID]
		row.Identity = d.agentIdentity(a)
		if a != nil {
			row.DisplayName = a.DisplayName
		}
	case CallerUser:
		row.Identity = overviewUserIdentity(c.ID)
	case CallerClient:
		reg, ok := d.clients[c.ID]
		row.Identity, row.DisplayName = overviewClientIdentity(reg, ok)
	}
	if h.Ledger == nil {
		return row
	}
	if g, ok := usage[c.ID]; ok {
		if kind == CallerUser && row.DisplayName == "" {
			// The verified email or name recorded with the user's requests.
			row.DisplayName = g.Labels["principal_id"]
		}
		row.Usage = overviewUsageOf(g)
		if t, ok := scan.Last[c.ID]; ok {
			tt := t
			row.LastSeen = &tt
		}
		return row
	}
	// No activity in the window: the usage roll-up is a true empty window, but
	// last_seen can still come from older ledger records.
	row.Usage = &OverviewUsage{Tokens: map[string]int64{}, TokensUnknown: map[string]int64{}, Cost: []OverviewCost{}}
	if t, ok := h.subjectLastSeen(ctx, tenant, kind, c.ID, d); ok {
		row.LastSeen = &t
	}
	return row
}

// subjectFilter returns a ledger filter and predicate selecting a subject.
func (d *overviewDirectory) subjectMatcher(kind, id string) (usageledger.Filter, func(usageledger.Record) bool) {
	switch kind {
	case CallerUser:
		return usageledger.Filter{PrincipalID: id, CallerKind: CallerUser}, nil
	case CallerClient:
		return usageledger.Filter{ClientID: id}, nil
	}
	aliases := d.aliasesOf(kind, id)
	return usageledger.Filter{}, func(r usageledger.Record) bool {
		if r.ActorID != "" {
			return aliases[r.ActorID]
		}
		return r.CallerKind == CallerAgent && aliases[r.PrincipalID]
	}
}

func (h *OverviewHandler) subjectLastSeen(ctx context.Context, tenant, kind, id string, d *overviewDirectory) (time.Time, bool) {
	filter, keep := d.subjectMatcher(kind, id)
	return h.lastSeen(ctx, tenant, filter, keep)
}

// ── detail ───────────────────────────────────────────────────────────────

// OverviewSubjectQuota reports quota coverage for the access points that
// admit the subject. Balances are omitted with the stated reason.
type OverviewSubjectQuota struct {
	AccessPoints []OverviewAccessPointQuota `json:"access_points"`
	Balance      OverviewBalance            `json:"balance"`
}

// OverviewBalance is either a value or an explanation of why there is none.
type OverviewBalance struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

const overviewBalanceReason = "Budget balances are held per budget key (rule x scope instance) in the quota engine; the key derivation is not exported and the engine read needs the aiplex:quota:read grant on the quota administration API. Allocations are listed from the active policy instead."

// OverviewSubjectDetail is the response of GET /overview/subjects/{kind}/{id}.
type OverviewSubjectDetail struct {
	Kind         string               `json:"kind"`
	ID           string               `json:"id"`
	DisplayName  string               `json:"display_name,omitempty"`
	Identity     OverviewIdentity     `json:"identity"`
	Aliases      []string             `json:"aliases,omitempty"`
	Bindings     []OverviewBinding    `json:"identity_bindings,omitempty"`
	Providers    []OverviewProvider   `json:"user_identity_providers,omitempty"`
	AccessPoints []OverviewAdmission  `json:"access_points"`
	Access       OverviewAccess       `json:"access"`
	Quota        OverviewSubjectQuota `json:"quota"`
	Since        time.Time            `json:"since"`
	LastSeen     *time.Time           `json:"last_seen"`
	Usage        *OverviewUsage       `json:"usage"`
	ByModel      []OverviewUsageGroup `json:"usage_by_model"`
	ByTool       []OverviewUsageGroup `json:"usage_by_tool"`
	Notes        []string             `json:"notes"`
	KnownFrom    []string             `json:"known_from"`
}

// OverviewBinding is a redacted agent identity binding.
type OverviewBinding struct {
	ID      string `json:"id"`
	Preset  string `json:"preset"`
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
	Claim   string `json:"subject_claim"`
}

// OverviewProvider is a configured user identity provider (no secrets).
type OverviewProvider struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Issuer      string `json:"issuer"`
	Disabled    bool   `json:"disabled"`
}

// OverviewAdmission says whether an access point admits a subject.
type OverviewAdmission struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Admits      string `json:"admits"`
	Reason      string `json:"reason"`
}

func (h *OverviewHandler) userProviders(ctx context.Context, tenant string) []OverviewProvider {
	records, err := h.Connected.Store.List(ctx, tenant, userfederation.ProviderKind)
	if err != nil {
		return nil
	}
	out := []OverviewProvider{}
	for _, rec := range records {
		var p struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Issuer      string `json:"issuer"`
			Disabled    bool   `json:"disabled"`
		}
		if rec.TenantID == tenant && json.Unmarshal(rec.Spec, &p) == nil {
			out = append(out, OverviewProvider{ID: rec.ID, DisplayName: p.DisplayName, Issuer: p.Issuer, Disabled: p.Disabled})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// subjectKnown reports whether the tenant has any evidence of the subject.
func (h *OverviewHandler) subjectKnown(ctx context.Context, tenant, kind, id string, d *overviewDirectory) []string {
	var from []string
	switch kind {
	case CallerAgent:
		canonical := d.agentID(id)
		if _, ok := d.agents[canonical]; ok && len(d.agents[canonical].Bindings) > 0 {
			from = append(from, "identity-binding")
		}
		for _, ap := range d.aps {
			for _, a := range ap.Subjects.Agents {
				if d.agentID(a) == canonical {
					from = append(from, "access-point")
					break
				}
			}
		}
	case CallerUser:
		for _, ap := range d.aps {
			for _, p := range ap.Subjects.Principals {
				if p == id {
					from = append(from, "access-point")
					break
				}
			}
		}
	case CallerClient:
		if _, ok := d.clients[id]; ok {
			from = append(from, "client-registration")
		}
		for _, ap := range d.aps {
			for _, c := range ap.Subjects.ClientRegistrations {
				if c == id {
					from = append(from, "access-point")
					break
				}
			}
		}
	}
	if _, ok := h.subjectLastSeen(ctx, tenant, kind, id, d); ok {
		from = append(from, "usage-ledger")
	}
	return from
}

// Subject handles GET /overview/subjects/{kind}/{id}.
func (h *OverviewHandler) Subject(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	if err := overviewParams(r, "since", "id"); err != nil {
		overviewQueryError(w, r, err.Error())
		return
	}
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	if id == "" {
		// Ids that contain "/" (SPIFFE ids) may be passed as ?id= instead of a path segment.
		id = r.URL.Query().Get("id")
	} else if unescaped, err := url.PathUnescape(id); err == nil {
		id = unescaped // chi leaves escaped path segments as sent
	}
	if !overviewValidKind(kind) {
		overviewQueryError(w, r, "kind must be agent, user or client")
		return
	}
	if id == "" || len(id) > 1024 {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "subject not found")
		return
	}
	since, err := parseOverviewSince(r.URL.Query().Get("since"), h.now())
	if err != nil {
		overviewQueryError(w, r, err.Error())
		return
	}
	dir, err := h.loadDirectory(r.Context(), tenant)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "OVERVIEW_UNAVAILABLE", "tenant configuration is unavailable")
		return
	}
	if kind == CallerAgent {
		id = dir.agentID(id)
	}
	known := h.subjectKnown(r.Context(), tenant, kind, id, dir)
	if len(known) == 0 {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "subject not found")
		return
	}
	aliases := dir.aliasesOf(kind, id)
	detail := OverviewSubjectDetail{Kind: kind, ID: id, Since: since, KnownFrom: known, AccessPoints: []OverviewAdmission{}, Notes: []string{}, ByModel: []OverviewUsageGroup{}, ByTool: []OverviewUsageGroup{}}
	var agent *overviewAgent
	switch kind {
	case CallerAgent:
		agent = dir.agents[id]
		detail.Identity = dir.agentIdentity(agent)
		if agent != nil {
			detail.DisplayName = agent.DisplayName
			detail.Aliases = overviewSortedKeys(agent.Aliases)
			for _, b := range agent.Bindings {
				detail.Bindings = append(detail.Bindings, OverviewBinding{ID: b.ID, Preset: b.Preset, Issuer: b.Issuer, Subject: b.Subject, Claim: b.SubjectClaim})
			}
		}
	case CallerUser:
		detail.Identity = overviewUserIdentity(id)
		detail.Providers = h.userProviders(r.Context(), tenant)
		detail.Notes = append(detail.Notes, "Which identity provider issued this principal is not recoverable from its id; the tenant's configured user identity providers are listed.")
	case CallerClient:
		reg, found := dir.clients[id]
		detail.Identity, detail.DisplayName = overviewClientIdentity(reg, found)
	}
	for _, ap := range dir.aps {
		state, why := overviewAdmits(ap, kind, aliases)
		if state != overviewNo {
			detail.AccessPoints = append(detail.AccessPoints, OverviewAdmission{ID: ap.ID, DisplayName: ap.DisplayName, Admits: state, Reason: why})
		}
	}
	scopes := h.scopesFor(r.Context(), kind, id, agent)
	detail.Access = h.accessFor(r.Context(), tenant, dir.aps, kind, aliases, scopes)
	detail.Quota = h.subjectQuota(tenant, dir.aps, detail.AccessPoints)
	if h.Ledger == nil {
		detail.Notes = append(detail.Notes, h.ledgerUnavailableNote())
	} else {
		filter, keep := dir.subjectMatcher(kind, id)
		filter.Since = since
		mutate := func(rec *usageledger.Record) bool { return keep == nil || keep(*rec) }
		total, err := h.scan(r.Context(), tenant, filter, []string{}, mutate)
		if err != nil {
			Error(w, r, http.StatusServiceUnavailable, "USAGE_LEDGER_UNAVAILABLE", "usage ledger is unavailable")
			return
		}
		detail.Usage = &OverviewUsage{Tokens: map[string]int64{}, TokensUnknown: map[string]int64{}, Cost: []OverviewCost{}}
		if len(total.Groups) == 1 {
			detail.Usage = overviewUsageOf(total.Groups[0])
		}
		if t, ok := total.Last[""]; ok {
			tt := t
			detail.LastSeen = &tt
		} else {
			if t, ok := h.subjectLastSeen(r.Context(), tenant, kind, id, dir); ok {
				detail.LastSeen = &t
			}
		}
		byModel, err := h.scan(r.Context(), tenant, filter, []string{"model_id"}, func(rec *usageledger.Record) bool { return rec.ModelID != "" && mutate(rec) })
		if err == nil {
			detail.ByModel = overviewUsageGroups(byModel, []string{"model_id"})
		}
		tools, err := h.scan(r.Context(), tenant, filter, []string{"tool_name"}, func(rec *usageledger.Record) bool { return rec.ToolName != "" && mutate(rec) })
		if err == nil {
			detail.ByTool = overviewUsageGroups(tools, []string{"tool_name"})
		}
		if total.Truncated {
			detail.Notes = append(detail.Notes, "The ledger scan reached its record bound; usage covers the newest records only.")
		}
	}
	JSON(w, http.StatusOK, detail)
}

// ── quota ────────────────────────────────────────────────────────────────

// OverviewAccessPointQuota is the QoS and quota view of one access point.
type OverviewAccessPointQuota struct {
	AccessPointID string                `json:"access_point_id"`
	QoSClass      string                `json:"qos_class,omitempty"`
	Coverage      OverviewQuotaCoverage `json:"coverage"`
	Allocations   OverviewAllocations   `json:"allocations"`
}

// OverviewQuotaCoverage reports whether the quota policy covers the access
// point. It reuses accessPointCall.applyQuotaTarget, the request path's check.
type OverviewQuotaCoverage struct {
	State  string `json:"state"` // not_required | covered | uncovered | unknown
	Detail string `json:"detail"`
}

// OverviewAllocations are the controls of the active policy that select the
// access point by id.
type OverviewAllocations struct {
	Budgets     []quotaplex.BudgetRule      `json:"budgets"`
	Rates       []quotaplex.RateRule        `json:"rates"`
	Concurrency []quotaplex.ConcurrencyRule `json:"concurrency"`
}

func (h *OverviewHandler) apQuota(tenant string, ap AccessPoint) OverviewAccessPointQuota {
	out := OverviewAccessPointQuota{AccessPointID: ap.ID, Allocations: OverviewAllocations{Budgets: []quotaplex.BudgetRule{}, Rates: []quotaplex.RateRule{}, Concurrency: []quotaplex.ConcurrencyRule{}}}
	if ap.QoS == nil {
		out.Coverage = OverviewQuotaCoverage{State: "not_required", Detail: "the access point has no qos block, so requests do not require a quota control"}
		return out
	}
	out.QoSClass = ap.QoS.Class
	policy, ok := h.Connected.Quota.tenantPolicy(tenant)
	if !ok {
		out.Coverage = OverviewQuotaCoverage{State: overviewUnknown, Detail: "no quota policy is visible to this server for the tenant; requests through a qos access point fail closed without a matching control"}
		return out
	}
	for _, rule := range policy.Budgets {
		if rule.Match.AccessPointID == ap.ID {
			out.Allocations.Budgets = append(out.Allocations.Budgets, rule)
		}
	}
	for _, rule := range policy.Rates {
		if rule.Match.AccessPointID == ap.ID {
			out.Allocations.Rates = append(out.Allocations.Rates, rule)
		}
	}
	for _, rule := range policy.Concurrency {
		if rule.Match.AccessPointID == ap.ID {
			out.Allocations.Concurrency = append(out.Allocations.Concurrency, rule)
		}
	}
	planes := []string{}
	if len(ap.Models) > 0 {
		planes = append(planes, "llmplex")
	}
	if len(ap.Tools) > 0 {
		planes = append(planes, "mcplex")
	}
	if len(ap.Skills) > 0 {
		planes = append(planes, "skillsplex")
	}
	covered, total := 0, 0
	var firstErr string
	for _, kind := range ap.CallerKinds {
		for _, plane := range planes {
			total++
			call := &accessPointCall{ID: ap.ID, CallerKind: kind, QoS: true}
			target := quotaplex.Target{Plane: plane}
			if err := call.applyQuotaTarget(policy, &target); err == nil {
				covered++
			} else if firstErr == "" {
				firstErr = err.Error()
			}
		}
	}
	specific := len(out.Allocations.Budgets) + len(out.Allocations.Rates) + len(out.Allocations.Concurrency)
	switch {
	case total > 0 && covered == total:
		out.Coverage = OverviewQuotaCoverage{State: "covered", Detail: fmt.Sprintf("a quota control matches every caller kind and plane of the access point (%d checks)", total)}
	case specific > 0:
		out.Coverage = OverviewQuotaCoverage{State: overviewUnknown, Detail: fmt.Sprintf("%d control(s) select this access point, but %d of %d caller-kind/plane checks matched with only the access point's own dimensions; controls with narrower selectors (provider, resource, model version) depend on the individual request", specific, covered, total)}
	default:
		out.Coverage = OverviewQuotaCoverage{State: "uncovered", Detail: firstErr}
	}
	return out
}

func (h *OverviewHandler) subjectQuota(tenant string, aps []AccessPoint, admitted []OverviewAdmission) OverviewSubjectQuota {
	out := OverviewSubjectQuota{AccessPoints: []OverviewAccessPointQuota{}, Balance: OverviewBalance{Reason: overviewBalanceReason}}
	byID := map[string]AccessPoint{}
	for _, ap := range aps {
		byID[ap.ID] = ap
	}
	for _, a := range admitted {
		if ap, ok := byID[a.ID]; ok {
			out.AccessPoints = append(out.AccessPoints, h.apQuota(tenant, ap))
		}
	}
	return out
}

func overviewExposePolicy(expose []string) toolexec.ToolPolicy {
	return toolexec.ToolPolicy{Allow: expose}
}
