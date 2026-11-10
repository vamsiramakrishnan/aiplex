package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/routing"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

// AccessPointKind is the tenant resource kind of an access point: one stable
// URL (/ap/{id}/…) that is the enforcement point for identity, access, QoS
// and quota in front of models, tools and skills.
const AccessPointKind = "access-point"

// Caller kinds are derived from verified identity, never from the request.
const (
	CallerAgent  = "agent"
	CallerUser   = "user"
	CallerClient = "client"
)

// SkillTenantLabel is the instance label that binds a deployed skill server
// to a tenant. Registry instances carry no other tenant fact.
const SkillTenantLabel = "aiplex.tenant"

// AccessPoint is the saved specification. Unknown fields are rejected.
type AccessPoint struct {
	ID          string              `json:"id"`
	DisplayName string              `json:"display_name,omitempty"`
	CallerKinds []string            `json:"caller_kinds"`
	Subjects    AccessPointSubjects `json:"subjects"`
	Models      []AccessPointModel  `json:"models,omitempty"`
	Tools       []AccessPointTool   `json:"tools,omitempty"`
	Skills      []AccessPointSkill  `json:"skills,omitempty"`
	QoS         *AccessPointQoS     `json:"qos,omitempty"`
}

// AccessPointSubjects narrows who may call. Principals, groups and agents are
// one identity constraint (any of them admits); client_registrations is an
// independent constraint. When both are set a caller must satisfy both.
// An empty subjects block admits every caller of an allowed kind.
//
// A client registration identified only by a public OAuth client id is
// attribution, not a security boundary: any holder of a user token issued to
// that public client presents the same authorized party.
type AccessPointSubjects struct {
	Groups              []quotaplex.GroupRef `json:"groups,omitempty"`
	Principals          []string             `json:"principals,omitempty"`
	Agents              []string             `json:"agents,omitempty"`
	ClientRegistrations []string             `json:"client_registrations,omitempty"`
}

// AccessPointModel maps a client-facing model name to a saved model surface
// (surface_ref and native_model), or to an ordered list of backends with a
// routing block (reliability-and-routing). cache turns on the exact-match
// response cache for the model; it is off unless present.
type AccessPointModel struct {
	Name        string            `json:"name"`
	SurfaceRef  string            `json:"surface_ref,omitempty"`
	NativeModel string            `json:"native_model,omitempty"`
	Backends    []routing.Backend `json:"backends,omitempty"`
	Routing     *routing.Routing  `json:"routing,omitempty"`
	Cache       *routing.Cache    `json:"cache,omitempty"`
	// derived marks SurfaceRef/NativeModel filled from Backends[0] when the
	// specification was decoded (see decodeAccessPoint); never serialized.
	derived bool
}

// allBackends lists the model's backends: the backends list, or the single
// surface_ref/native_model pair.
func (m AccessPointModel) allBackends() []routing.Backend {
	if len(m.Backends) > 0 {
		return m.Backends
	}
	return []routing.Backend{{SurfaceRef: m.SurfaceRef, NativeModel: m.NativeModel}}
}

// primary is the first listed backend.
func (m AccessPointModel) primary() routing.Backend { return m.allBackends()[0] }

// routed reports whether the model goes through the router (several
// backends, a routing block or a cache). A plain surface_ref model is served
// exactly as before.
func (m AccessPointModel) routed() bool {
	return len(m.Backends) > 0 || m.Routing != nil || m.Cache != nil
}

// AccessPointTool exposes one saved tool connection. Expose is an optional
// list of tool names or prefixes ending in '*'; omitted exposes every tool
// the connection itself permits.
type AccessPointTool struct {
	ConnectionRef string   `json:"connection_ref"`
	Expose        []string `json:"expose,omitempty"`
}

type AccessPointSkill struct {
	InstanceRef string `json:"instance_ref"`
}

// AccessPointQoS is rendered into a QuotaPlex policy fragment for review; it
// is never enforced on its own. A request through an access point with a QoS
// block requires an active quota binding and a policy control that matches
// the access point, and fails closed otherwise.
type AccessPointQoS struct {
	Class  string `json:"class"`
	Weight uint32 `json:"weight,omitempty"`
	// Scope selects who shares the rate and concurrency limits: "principal"
	// (the default; each caller principal has its own allowance) or
	// "access_point" (one pool shared by every caller of the access point).
	Scope      string            `json:"scope,omitempty"`
	Limits     *AccessPointLimit `json:"limits,omitempty"`
	BudgetRefs []string          `json:"budget_refs,omitempty"`
}

// QoS limit scopes.
const (
	QoSScopePrincipal   = "principal"
	QoSScopeAccessPoint = "access_point"
)

type AccessPointLimit struct {
	RequestsPerMinute int64 `json:"requests_per_minute,omitempty"`
	TokensPerMinute   int64 `json:"tokens_per_minute,omitempty"`
	Concurrency       int64 `json:"concurrency,omitempty"`
}

// AccessPointOptions is operator configuration for access-point endpoints.
type AccessPointOptions struct {
	// Skills resolves skill server instances; nil rejects skill references.
	Skills registry.SkillsPlexStore
	// AuthorizationServers are advertised in RFC 9728 metadata.
	AuthorizationServers []string
	// AllowedOrigins are browser origins accepted on MCP endpoints in addition
	// to the public URL's own origin. "*" accepts any origin.
	AllowedOrigins []string
}

var (
	accessPointIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	// ':' appears in Bedrock model and inference-profile ids ("...-v1:0").
	accessPointModelPattern = regexp.MustCompile(`^[A-Za-z0-9._@-][A-Za-z0-9._@/:-]{0,255}$`)
	accessPointClassPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	// Connection ids namespace aggregate tool names as {connection}__{tool}.
	accessPointConnectionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
)

func accessPointText(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func uniqueTexts(field string, values []string, max int) error {
	if len(values) > 256 {
		return fmt.Errorf("%s is bounded to 256 entries", field)
	}
	seen := map[string]bool{}
	for _, v := range values {
		if !accessPointText(v, max) || seen[v] {
			return fmt.Errorf("%s entries must be unique bounded identifiers", field)
		}
		seen[v] = true
	}
	return nil
}

// accessPointAliasRewritable reports whether the access point can rewrite the
// client-facing model name to the native one for this surface: the model is a
// path segment (GenAI, Bedrock), a connect-time query (Realtime) or a
// top-level JSON string field that is rewritten in place (OpenAI chat,
// responses and embeddings; Anthropic Messages). Elsewhere (moderations,
// images and audio surfaces, which may carry a multipart form) the body is
// relayed verbatim, so the client-facing name must be the native one.
func accessPointAliasRewritable(s modelapi.Surface) bool {
	switch s.Protocol {
	case clientplex.GenAI:
		return true
	case clientplex.Messages, clientplex.OpenAIChat, clientplex.Responses, modelapi.ProtocolOpenAIEmbeddings:
		return true
	case modelapi.ProtocolBedrockConverse, modelapi.ProtocolBedrockInvoke, modelapi.ProtocolOpenAIRealtime:
		// The model is the path segment (Bedrock) or connect-time query
		// (Realtime): the access point rewrites it without touching a body.
		return true
	}
	return false
}

func accessPointConnectionNamespaced(id string) error {
	if !accessPointConnectionPattern.MatchString(id) || strings.Contains(id, "__") || strings.HasSuffix(id, "_") {
		return fmt.Errorf("tool connection %q cannot be namespaced as {connection}__{tool}: ids need [A-Za-z0-9_.-], no '__' and no trailing '_'", id)
	}
	return nil
}

// decodeAccessPoint parses a saved specification strictly.
func decodeAccessPoint(id string, raw []byte) (AccessPoint, error) {
	var ap AccessPoint
	if err := decodeSpec(raw, &ap); err != nil {
		return ap, err
	}
	if ap.ID != id || !accessPointIDPattern.MatchString(id) {
		return ap, withField("id", fmt.Errorf("access point id must match the resource id and [A-Za-z0-9._-]{1,128}"))
	}
	for i := range ap.Models {
		// A backends list fills the single-backend fields with its first
		// backend, so readers that describe one surface per model (connect,
		// overview, quota plan) describe the primary backend.
		if m := &ap.Models[i]; len(m.Backends) > 0 && m.SurfaceRef == "" && m.NativeModel == "" {
			m.SurfaceRef, m.NativeModel, m.derived = m.Backends[0].SurfaceRef, m.Backends[0].NativeModel, true
		}
	}
	return ap, nil
}

// validateAccessPoint checks the specification and resolves every reference
// in the caller's tenant. It runs at save time and again for every request.
func (h *ConnectedHandler) validateAccessPoint(ctx context.Context, tenant, id string, raw []byte) (AccessPoint, error) {
	ap, err := decodeAccessPoint(id, raw)
	if err != nil {
		return ap, err
	}
	if ap.DisplayName != "" && !accessPointText(ap.DisplayName, 256) {
		return ap, withField("display_name", fmt.Errorf("display_name must be bounded printable text"))
	}
	if len(ap.CallerKinds) == 0 || len(ap.CallerKinds) > 3 {
		return ap, withField("caller_kinds", fmt.Errorf("caller_kinds requires one or more of agent, user, client"))
	}
	kinds := map[string]bool{}
	for i, kind := range ap.CallerKinds {
		if (kind != CallerAgent && kind != CallerUser && kind != CallerClient) || kinds[kind] {
			return ap, withField(fmt.Sprintf("caller_kinds/%d", i), fmt.Errorf("caller_kinds entries must be unique values of agent, user, client"))
		}
		kinds[kind] = true
	}
	if err = h.validateAccessPointSubjects(ctx, tenant, ap.Subjects); err != nil {
		return ap, err
	}
	if len(ap.Models)+len(ap.Tools)+len(ap.Skills) == 0 {
		return ap, fmt.Errorf("an access point exposes at least one model, tool connection or skill")
	}
	if err = h.validateAccessPointModels(ctx, tenant, ap.Models); err != nil {
		return ap, err
	}
	if err = h.validateAccessPointTools(ctx, tenant, ap.Tools); err != nil {
		return ap, err
	}
	if err = h.validateAccessPointSkills(ctx, tenant, ap.Skills); err != nil {
		return ap, err
	}
	if err = h.validateAccessPointQoS(tenant, ap.QoS); err != nil {
		return ap, err
	}
	return ap, nil
}

func (h *ConnectedHandler) validateAccessPointSubjects(ctx context.Context, tenant string, s AccessPointSubjects) error {
	if len(s.Groups) > 256 {
		return withField("subjects/groups", fmt.Errorf("subjects.groups is bounded to 256 entries"))
	}
	seen := map[quotaplex.GroupRef]bool{}
	for i, g := range s.Groups {
		if !accessPointText(g.Source, 1024) || !accessPointText(g.ID, 1024) || seen[g] {
			return withField(fmt.Sprintf("subjects/groups/%d", i), fmt.Errorf("subjects.groups entries must be unique source-qualified groups"))
		}
		seen[g] = true
	}
	if err := uniqueTexts("subjects.principals", s.Principals, 1024); err != nil {
		return withField("subjects/principals", err)
	}
	if err := uniqueTexts("subjects.agents", s.Agents, 1024); err != nil {
		return withField("subjects/agents", err)
	}
	if err := uniqueTexts("subjects.client_registrations", s.ClientRegistrations, 256); err != nil {
		return withField("subjects/client_registrations", err)
	}
	for i, ref := range s.ClientRegistrations {
		record, err := h.Store.Get(ctx, tenant, clientplex.Kind, ref)
		if err != nil && !errors.Is(err, resources.ErrNotFound) {
			return referenceUnavailable(err)
		}
		if err != nil {
			return withField(fmt.Sprintf("subjects/client_registrations/%d", i), fmt.Errorf("client registration %q does not resolve in this tenant", ref))
		}
		var registration clientplex.Registration
		if json.Unmarshal(record.Spec, &registration) != nil || registration.ID != ref {
			return withField(fmt.Sprintf("subjects/client_registrations/%d", i), fmt.Errorf("client registration %q is invalid", ref))
		}
	}
	return nil
}

// accessPointSurface loads a saved model surface for an access point.
func (h *ConnectedHandler) accessPointSurface(ctx context.Context, tenant, ref string) (ModelConnection, error) {
	var connection ModelConnection
	record, err := h.Store.Get(ctx, tenant, "model-surface", ref)
	if err != nil && !errors.Is(err, resources.ErrNotFound) {
		return connection, referenceUnavailable(err)
	}
	if err != nil {
		return connection, fmt.Errorf("model surface %q does not resolve in this tenant", ref)
	}
	if json.Unmarshal(record.Spec, &connection) != nil || connection.Surface.ID != ref || connection.Surface.Validate() != nil {
		return connection, fmt.Errorf("model surface %q is invalid", ref)
	}
	return connection, nil
}

func (h *ConnectedHandler) validateAccessPointModels(ctx context.Context, tenant string, entries []AccessPointModel) error {
	if len(entries) > 256 {
		return withField("models", fmt.Errorf("models is bounded to 256 entries"))
	}
	names := map[string]bool{}
	for i, m := range entries {
		at := func(field string) string { return fmt.Sprintf("models/%d/%s", i, field) }
		if len(m.Backends) > 0 || m.Routing != nil || m.Cache != nil {
			if err := h.validateRoutedModel(ctx, tenant, i, m); err != nil {
				return err
			}
			if names[m.Name] {
				return withField(at("name"), fmt.Errorf("model name %q is not unique", m.Name))
			}
			names[m.Name] = true
			if len(m.Backends) > 0 {
				continue
			}
		}
		if !accessPointModelPattern.MatchString(m.Name) || !accessPointModelPattern.MatchString(m.NativeModel) || strings.Contains(m.Name+m.NativeModel, "..") || m.SurfaceRef == "" {
			field := "surface_ref"
			switch {
			case !accessPointModelPattern.MatchString(m.Name) || strings.Contains(m.Name, ".."):
				field = "name"
			case !accessPointModelPattern.MatchString(m.NativeModel) || strings.Contains(m.NativeModel, ".."):
				field = "native_model"
			}
			return withField(at(field), fmt.Errorf("models entries need name, surface_ref and native_model ([A-Za-z0-9._@/:-], no '..')"))
		}
		if names[m.Name] && !m.routed() {
			return withField(at("name"), fmt.Errorf("model name %q is not unique", m.Name))
		}
		names[m.Name] = true
		connection, err := h.accessPointSurface(ctx, tenant, m.SurfaceRef)
		if err != nil {
			return withField(at("surface_ref"), err)
		}
		if len(accessPointWireProtocols(connection.Surface.Protocol)) == 0 {
			return withField(at("surface_ref"), fmt.Errorf("model surface %q protocol %q is not served by any access-point wire protocol", m.SurfaceRef, connection.Surface.Protocol))
		}
		if err = accessPointSurfaceServes(connection.Surface, m.NativeModel); err != nil {
			return withField(at("native_model"), fmt.Errorf("model surface %q cannot serve native model %q: %w", m.SurfaceRef, m.NativeModel, err))
		}
		if _, err = connection.Surface.ModelID(m.NativeModel); err != nil {
			return withField(at("native_model"), fmt.Errorf("model surface %q does not list native model %q", m.SurfaceRef, m.NativeModel))
		}
		if m.Name != m.NativeModel && !accessPointAliasRewritable(connection.Surface) {
			return withField(at("name"), fmt.Errorf("model %q: surface %q relays the request body verbatim, so name must equal native_model", m.Name, m.SurfaceRef))
		}
	}
	return nil
}

// accessPointToolConnection loads a saved, managed tool connection.
func (h *ConnectedHandler) accessPointToolConnection(ctx context.Context, tenant, ref string) (ToolConnection, error) {
	var connection ToolConnection
	record, err := h.Store.Get(ctx, tenant, "tool-connection", ref)
	if err != nil && !errors.Is(err, resources.ErrNotFound) {
		return connection, referenceUnavailable(err)
	}
	if err != nil {
		return connection, fmt.Errorf("tool connection %q does not resolve in this tenant", ref)
	}
	if json.Unmarshal(record.Spec, &connection) != nil || connection.ID != ref || connection.Execution == nil {
		return connection, fmt.Errorf("tool connection %q is not a managed execution connection", ref)
	}
	return connection, nil
}

func (h *ConnectedHandler) validateAccessPointTools(ctx context.Context, tenant string, entries []AccessPointTool) error {
	if len(entries) > 64 {
		return withField("tools", fmt.Errorf("tools is bounded to 64 connections"))
	}
	seen := map[string]bool{}
	for i, t := range entries {
		at := func(field string) string { return fmt.Sprintf("tools/%d/%s", i, field) }
		if err := accessPointConnectionNamespaced(t.ConnectionRef); err != nil {
			return withField(at("connection_ref"), err)
		}
		if seen[t.ConnectionRef] {
			return withField(at("connection_ref"), fmt.Errorf("tool connection %q is listed twice", t.ConnectionRef))
		}
		seen[t.ConnectionRef] = true
		if t.Expose != nil {
			if err := (toolexec.ToolPolicy{Allow: t.Expose}).Validate(); err != nil {
				return withField(at("expose"), fmt.Errorf("tools[%s].expose: %v", t.ConnectionRef, strings.TrimPrefix(err.Error(), "tools.allow ")))
			}
		}
		if _, err := h.accessPointToolConnection(ctx, tenant, t.ConnectionRef); err != nil {
			return withField(at("connection_ref"), err)
		}
	}
	return nil
}

func (h *ConnectedHandler) accessPointSkillInstance(ctx context.Context, tenant, ref string) (*models.Instance, error) {
	if h.AccessPoint.Skills == nil {
		return nil, fmt.Errorf("skill references require a configured skill registry")
	}
	instance, err := h.AccessPoint.Skills.GetInstance(ctx, ref)
	if err != nil || instance == nil || instance.ID != ref || instance.Plane != models.PlaneSkillsPlex {
		return nil, fmt.Errorf("skill instance %q does not resolve", ref)
	}
	if instance.Labels[SkillTenantLabel] != tenant {
		return nil, fmt.Errorf("skill instance %q is not labelled %s=%s", ref, SkillTenantLabel, tenant)
	}
	return instance, nil
}

func (h *ConnectedHandler) validateAccessPointSkills(ctx context.Context, tenant string, entries []AccessPointSkill) error {
	if len(entries) > 64 {
		return withField("skills", fmt.Errorf("skills is bounded to 64 instances"))
	}
	seen := map[string]bool{}
	for i, s := range entries {
		field := fmt.Sprintf("skills/%d/instance_ref", i)
		if !accessPointText(s.InstanceRef, 256) || seen[s.InstanceRef] {
			return withField(field, fmt.Errorf("skills entries need unique instance_ref values"))
		}
		seen[s.InstanceRef] = true
		if _, err := h.accessPointSkillInstance(ctx, tenant, s.InstanceRef); err != nil {
			return withField(field, err)
		}
	}
	return nil
}

func (h *ConnectedHandler) validateAccessPointQoS(tenant string, q *AccessPointQoS) error {
	if q == nil {
		return nil
	}
	if !accessPointClassPattern.MatchString(q.Class) {
		return withField("qos/class", fmt.Errorf("qos.class must match [a-z][a-z0-9-]{0,62}"))
	}
	if q.Weight > 1000 {
		return withField("qos/weight", fmt.Errorf("qos.weight is bounded to 1..1000"))
	}
	if q.Scope != "" && q.Scope != QoSScopePrincipal && q.Scope != QoSScopeAccessPoint {
		return withField("qos/scope", fmt.Errorf("qos.scope must be principal or access_point"))
	}
	if q.Limits != nil {
		l := q.Limits
		if l.RequestsPerMinute < 0 || l.TokensPerMinute < 0 || l.Concurrency < 0 || l.RequestsPerMinute > 1<<40 || l.TokensPerMinute > 1<<50 || l.Concurrency > 1<<20 {
			return withField("qos/limits", fmt.Errorf("qos.limits must be positive bounded values"))
		}
		if l.RequestsPerMinute == 0 && l.TokensPerMinute == 0 && l.Concurrency == 0 {
			return withField("qos/limits", fmt.Errorf("qos.limits must set at least one limit or be omitted"))
		}
	}
	if err := uniqueTexts("qos.budget_refs", q.BudgetRefs, 1024); err != nil {
		return withField("qos/budget_refs", err)
	}
	if len(q.BudgetRefs) > 0 {
		policy, ok := h.Quota.tenantPolicy(tenant)
		if !ok {
			return withField("qos/budget_refs", fmt.Errorf("qos.budget_refs require a configured quota policy for this tenant"))
		}
		for i, ref := range q.BudgetRefs {
			found := false
			for _, rule := range policy.Budgets {
				if rule.ID == ref {
					found = true
				}
			}
			if !found {
				return withField(fmt.Sprintf("qos/budget_refs/%d", i), fmt.Errorf("qos.budget_refs %q is not a budget in this tenant's quota policy", ref))
			}
		}
	}
	return nil
}

// tenantPolicy returns the compiled tenant policy of a configured host.
func (h *QuotaHost) tenantPolicy(tenant string) (quotaplex.Policy, bool) {
	if h == nil {
		return quotaplex.Policy{}, false
	}
	t, ok := h.tenants[tenant]
	if !ok || t.policy == nil {
		return quotaplex.Policy{}, false
	}
	return t.policy.Policy(), true
}

// requireAdministrator admits only the admin role, except in explicit local
// development without a verified role context.
func (h *ConnectedHandler) requireAdministrator(w http.ResponseWriter, r *http.Request) bool {
	access := GetWIFAccess(r.Context())
	if access == nil {
		if h.Development {
			return true
		}
		Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified role context required")
		return false
	}
	for _, role := range access.Roles {
		if role == models.RoleAdmin {
			return true
		}
	}
	Error(w, r, http.StatusForbidden, "FORBIDDEN", "access point changes require the admin role")
	return false
}

// MountAccessPoints mounts the access-point resource API under /api/v1.
// Operators read the revisioned resource; other callers read only the access
// points that admit them (accessPointReads). Writes are admin-only.
func (h *ConnectedHandler) MountAccessPoints(r chi.Router) {
	resource := h.Resources(AccessPointKind)
	write := func(w http.ResponseWriter, r *http.Request) {
		if h.requireAdministrator(w, r) {
			resource(w, r)
		}
	}
	read := h.accessPointReads(resource)
	r.Route("/access-points", func(r chi.Router) {
		r.Get("/", read)
		r.Get("/{id}", read)
		r.Put("/{id}", write)
		r.Delete("/{id}", write)
		r.Post("/{id}/quota-plan", h.AccessPointQuotaPlan)
		r.Get("/{id}/routing", h.AccessPointRouting)
		r.Post("/{id}/cache/purge", h.AccessPointCachePurge)
	})
}

// loadAccessPointRecord reads and strictly decodes a saved access point.
func (h *ConnectedHandler) loadAccessPointRecord(ctx context.Context, tenant, id string) (resources.Record, AccessPoint, error) {
	record, err := h.Store.Get(ctx, tenant, AccessPointKind, id)
	if err != nil {
		return record, AccessPoint{}, err
	}
	if record.TenantID != tenant || record.Kind != AccessPointKind || record.ID != id {
		return record, AccessPoint{}, resources.ErrNotFound
	}
	ap, err := decodeAccessPoint(id, record.Spec)
	if err != nil {
		return record, ap, fmt.Errorf("%w: %v", errAccessPointRecordInvalid, err)
	}
	return record, ap, nil
}

// errAccessPointRecordInvalid marks a record that loaded from the store but
// does not decode. Any other loadAccessPointRecord error is a store failure.
var errAccessPointRecordInvalid = errors.New("saved access point is invalid")

// accessPointLoadError writes the response for a failed loadAccessPointRecord:
// 404 for a missing record, 409 for a record that loads but is invalid, and
// 503 for a store failure.
func accessPointLoadError(w http.ResponseWriter, r *http.Request, err error, notFound func(error) bool) {
	switch {
	case notFound(err):
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "access point not found")
	case errors.Is(err, errAccessPointRecordInvalid):
		Error(w, r, http.StatusConflict, "ACCESS_POINT_INVALID", "saved access point is invalid")
	default:
		Error(w, r, http.StatusServiceUnavailable, "ACCESS_POINT_UNAVAILABLE", "access point configuration is unavailable")
	}
}

var errAccessPointUnavailable = errors.New("access point unavailable")

// referenceUnavailable marks a reference lookup that failed in the store, so
// an outage is reported as unavailable rather than as an invalid reference.
func referenceUnavailable(err error) error {
	return fmt.Errorf("%w: referenced resource lookup failed", errAccessPointUnavailable)
}
