// Package google implements bounded native Google agent governance APIs.
// Registry topology, authentication identity and IAM authorization remain
// distinct resources; a successful registration is not evidence of enforcement.
package google

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// Config pins one governance project and location. References outside this tree
// cannot be selected by an operation. URN/agent-principal references require
// explicit host allowlists because URNs do not encode a project boundary.
type Config struct {
	Project                string   `json:"project"`
	Location               string   `json:"location"`
	AllowedIdentifiers     []string `json:"allowed_identifiers,omitempty"`
	AllowedAgentPrincipals []string `json:"allowed_agent_principals,omitempty"`
	AllowedRoles           []string `json:"allowed_roles,omitempty"`
	// The registry currently documents no server-side etag for Service/Binding.
	// Opt-in permits fingerprint-checked writes; this is not atomic cloud CAS.
	AllowNonAtomicMutations bool `json:"allow_non_atomic_mutations,omitempty"`
}

var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var resourceID = regexp.MustCompile(`^[a-z][a-z0-9-]{2,61}[a-z0-9]$`)
var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var urn = regexp.MustCompile(`^urn:(agent|mcp|endpoint):[A-Za-z0-9._-]+:[A-Za-z0-9._-]+:[A-Za-z0-9._-]+$`)
var agentPrincipal = regexp.MustCompile(`^principal://agents\.global\.(org|proj)-[0-9]+\.system\.id\.goog/resources/[A-Za-z0-9/._-]+$`)
var predefinedRole = regexp.MustCompile(`^roles/[A-Za-z0-9][A-Za-z0-9_.]{0,127}$`)

func (c Config) validate() error {
	if !segment.MatchString(c.Project) || !segment.MatchString(c.Location) || strings.ContainsAny(c.Project+c.Location, ":") {
		return errors.New("Google governance requires an exact project and location")
	}
	for _, id := range c.AllowedIdentifiers {
		if !urn.MatchString(id) {
			return errors.New("invalid allowed registry identifier")
		}
	}
	for _, p := range c.AllowedAgentPrincipals {
		if !validPrincipal(p) {
			return errors.New("invalid allowed agent principal")
		}
	}
	for _, role := range c.AllowedRoles {
		if !predefinedRole.MatchString(role) {
			return errors.New("allowed IAM roles must be exact predefined role names")
		}
	}
	return nil
}
func (c Config) parent() string { return "projects/" + c.Project + "/locations/" + c.Location }
func (c Config) resource(raw string, collections ...string) (string, error) {
	prefix := c.parent() + "/"
	if !strings.HasPrefix(raw, prefix) {
		return "", errors.New("Google governance resource is outside configured project/location")
	}
	parts := strings.Split(strings.TrimPrefix(raw, prefix), "/")
	if len(parts) != 2 || !slices.Contains(collections, parts[0]) || !segment.MatchString(parts[1]) || parts[1] == "." || parts[1] == ".." {
		return "", errors.New("invalid Google governance resource")
	}
	return parts[1], nil
}
func strict(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return errors.New("operation payload is required")
	}
	if len(raw) > nativeapi.MaxBodyBytes {
		return errors.New("Google governance payload exceeds limit")
	}
	if e := uniqueJSON(raw); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(dst); e != nil {
		return errors.New("invalid Google governance payload")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing Google governance payload")
	}
	return nil
}
func empty(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) != 0 && string(bytes.TrimSpace(raw)) != "null" && string(bytes.TrimSpace(raw)) != "{}" {
		return errors.New("operation does not accept a payload")
	}
	return nil
}
func fingerprint(raw json.RawMessage) (string, error) {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return "", errors.New("invalid observed registry resource")
	}
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
func requestID(id string) error {
	if id != "" && (!uuid.MatchString(id) || id == "00000000-0000-0000-0000-000000000000") {
		return errors.New("requestId must be a nonzero UUID")
	}
	return nil
}

type Registry struct {
	cfg    Config
	client *nativeapi.Client
}

func New(cfg Config, client *nativeapi.Client) (*Registry, error) {
	if e := cfg.validate(); e != nil {
		return nil, e
	}
	if client == nil {
		return nil, errors.New("registry client is required")
	}
	cfg.AllowedIdentifiers = slices.Clone(cfg.AllowedIdentifiers)
	cfg.AllowedAgentPrincipals = slices.Clone(cfg.AllowedAgentPrincipals)
	cfg.AllowedRoles = slices.Clone(cfg.AllowedRoles)
	return &Registry{cfg, client}, nil
}
func (d *Registry) Operations() []nativeapi.OperationInfo {
	var out []nativeapi.OperationInfo
	for _, kind := range []string{"services", "bindings"} {
		for _, op := range []string{"list", "get", "create", "patch", "delete"} {
			out = append(out, nativeapi.OperationInfo{Name: kind + "." + op, Mutating: op == "create" || op == "patch" || op == "delete", Description: "Google Agent Registry " + kind + " " + op})
		}
	}
	for _, kind := range []string{"agents", "mcpServers", "endpoints", "skills"} {
		for _, op := range []string{"list", "get"} {
			out = append(out, nativeapi.OperationInfo{Name: kind + "." + op, Description: "Discover native registry " + kind})
		}
	}
	out = append(out, nativeapi.OperationInfo{Name: "operations.get", Description: "Observe registry long-running operation; does not imply readiness"}, nativeapi.OperationInfo{Name: "aiApplications.getIamPolicy", Description: "Read registry application IAM policy"}, nativeapi.OperationInfo{Name: "aiApplications.setIamPolicy", Mutating: true, Description: "Set registry application IAM policy with policy etag"})
	return out
}

type listRequest struct {
	PageSize  int    `json:"pageSize,omitempty"`
	PageToken string `json:"pageToken,omitempty"`
	Filter    string `json:"filter,omitempty"`
	OrderBy   string `json:"orderBy,omitempty"`
}

func listQuery(raw json.RawMessage) (url.Values, error) {
	v := listRequest{}
	if len(raw) > 0 {
		if e := strict(raw, &v); e != nil {
			return nil, e
		}
	}
	if v.PageSize < 0 || v.PageSize > 1000 || len(v.PageToken) > 8192 || len(v.Filter) > 8192 || len(v.OrderBy) > 1024 {
		return nil, errors.New("registry list parameters exceed limits")
	}
	q := url.Values{}
	if v.PageSize > 0 {
		q.Set("pageSize", fmt.Sprint(v.PageSize))
	}
	if v.PageToken != "" {
		q.Set("pageToken", v.PageToken)
	}
	if v.Filter != "" {
		q.Set("filter", v.Filter)
	}
	if v.OrderBy != "" {
		q.Set("orderBy", v.OrderBy)
	}
	return q, nil
}

type ServiceSpec struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content,omitempty"`
}
type Interface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
}
type Service struct {
	Name          string       `json:"name,omitempty"`
	DisplayName   string       `json:"displayName,omitempty"`
	Description   string       `json:"description,omitempty"`
	Interfaces    []Interface  `json:"interfaces,omitempty"`
	AgentSpec     *ServiceSpec `json:"agentSpec,omitempty"`
	McpServerSpec *ServiceSpec `json:"mcpServerSpec,omitempty"`
	EndpointSpec  *ServiceSpec `json:"endpointSpec,omitempty"`
}
type Identifier struct {
	Identifier string `json:"identifier"`
}
type AuthProviderBinding struct {
	AuthProvider string   `json:"authProvider"`
	Scopes       []string `json:"scopes,omitempty"`
	ContinueURI  string   `json:"continueUri,omitempty"`
}
type Binding struct {
	Name                string               `json:"name,omitempty"`
	DisplayName         string               `json:"displayName,omitempty"`
	Description         string               `json:"description,omitempty"`
	Source              *Identifier          `json:"source,omitempty"`
	Target              *Identifier          `json:"target,omitempty"`
	AuthProviderBinding *AuthProviderBinding `json:"authProviderBinding,omitempty"`
}
type mutation struct {
	Resource   json.RawMessage `json:"resource,omitempty"`
	UpdateMask string          `json:"updateMask,omitempty"`
	RequestID  string          `json:"requestId,omitempty"`
}

func (d *Registry) Execute(ctx context.Context, op nativeapi.Operation) (nativeapi.Result, error) {
	if e := d.ValidateOperation(op); e != nil {
		return nativeapi.Result{}, e
	}
	parts := strings.Split(op.Name, ".")
	if len(parts) != 2 {
		return nativeapi.Result{}, errors.New("unknown Google registry operation")
	}
	kind, action := parts[0], parts[1]
	if kind == "aiApplications" {
		return policyExecute(ctx, d.client, d.cfg, op, "v1/", false, "aiApplications")
	}
	mutable := kind == "services" || kind == "bindings"
	if !slices.Contains([]string{"services", "bindings", "agents", "mcpServers", "endpoints", "skills", "operations"}, kind) || !slices.Contains([]string{"list", "get", "create", "patch", "delete"}, action) || (!mutable && action != "list" && action != "get") || (kind == "operations" && action != "get") {
		return nativeapi.Result{}, errors.New("unknown Google registry operation")
	}
	version := "v1/"
	if kind == "skills" {
		version = "v1alpha/"
	}
	if action == "list" {
		if op.Resource != d.cfg.parent() {
			return nativeapi.Result{}, errors.New("list requires configured registry parent")
		}
		q, e := listQuery(op.Payload)
		if e != nil {
			return nativeapi.Result{}, e
		}
		return d.client.Do(ctx, http.MethodGet, version+op.Resource+"/"+kind, q, nil)
	}
	id, e := d.cfg.resource(op.Resource, kind)
	if e != nil {
		return nativeapi.Result{}, e
	}
	if action == "get" {
		if e = empty(op.Payload); e != nil {
			return nativeapi.Result{}, e
		}
		r, e := d.client.Do(ctx, http.MethodGet, version+op.Resource, nil, nil)
		if e == nil && mutable {
			r.ETag, e = fingerprint(r.Body)
		}
		return r, e
	}
	m := mutation{}
	if e = strict(op.Payload, &m); e != nil {
		return nativeapi.Result{}, e
	}
	if e = requestID(m.RequestID); e != nil {
		return nativeapi.Result{}, e
	}
	q := url.Values{}
	if m.RequestID != "" {
		q.Set("requestId", m.RequestID)
	}
	var body any
	if action != "delete" {
		body, e = d.validateResource(kind, op.Resource, m.Resource, action == "create")
		if e != nil {
			return nativeapi.Result{}, e
		}
	} else if len(m.Resource) > 0 || m.UpdateMask != "" {
		return nativeapi.Result{}, errors.New("delete accepts requestId only")
	}
	if action == "create" {
		if op.ETag != "" || m.UpdateMask != "" || !resourceID.MatchString(id) {
			return nativeapi.Result{}, errors.New("create requires a new RFC1034 resource id and no etag/update mask")
		}
		q.Set(strings.TrimSuffix(kind, "s")+"Id", id)
		return d.client.Do(ctx, http.MethodPost, version+d.cfg.parent()+"/"+kind, q, body)
	}
	if !d.cfg.AllowNonAtomicMutations || !strings.HasPrefix(op.ETag, "sha256:") {
		return nativeapi.Result{}, errors.New("registry update/delete requires explicit non-atomic mutation opt-in and observed resource fingerprint")
	}
	if action == "patch" {
		if !validMask(kind, m.UpdateMask) {
			return nativeapi.Result{}, errors.New("explicit writable registry updateMask is required")
		}
		q.Set("updateMask", m.UpdateMask)
	}
	observed, e := d.client.Do(ctx, http.MethodGet, version+op.Resource, nil, nil)
	if e != nil {
		return nativeapi.Result{}, e
	}
	observedTag, e := fingerprint(observed.Body)
	if e != nil {
		return nativeapi.Result{}, e
	}
	if observedTag != op.ETag {
		return nativeapi.Result{}, errors.New("registry observation changed; review a new plan")
	}
	method := http.MethodPatch
	if action == "delete" {
		method = http.MethodDelete
	}
	return d.client.Do(ctx, method, version+op.Resource, q, body)
}
func validMask(kind, mask string) bool {
	allowed := []string{"displayName", "description", "source", "target", "authProviderBinding"}
	if kind == "services" {
		allowed = []string{"displayName", "description", "interfaces", "agentSpec", "mcpServerSpec", "endpointSpec"}
	}
	if mask == "" {
		return false
	}
	for _, f := range strings.Split(mask, ",") {
		if !slices.Contains(allowed, f) {
			return false
		}
	}
	return true
}
func (d *Registry) validateResource(kind, name string, raw json.RawMessage, create bool) (any, error) {
	if kind == "services" {
		var v Service
		if e := strict(raw, &v); e != nil {
			return nil, e
		}
		if v.Name != "" && v.Name != name {
			return nil, errors.New("service body/path mismatch")
		}
		v.Name = name
		if len(v.DisplayName) > 63 || len(v.Description) > 2048 {
			return nil, errors.New("service metadata exceeds limits")
		}
		for _, iface := range v.Interfaces {
			u, e := url.Parse(iface.URL)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !slices.Contains([]string{"JSONRPC", "GRPC", "HTTP_JSON"}, iface.ProtocolBinding) {
				return nil, errors.New("service interfaces require credential-free HTTPS URLs and a documented protocol binding")
			}
		}
		n := 0
		for _, s := range []*ServiceSpec{v.AgentSpec, v.McpServerSpec, v.EndpointSpec} {
			if s != nil {
				n++
				if s.Type == "" || len(s.Content) > 10240 {
					return nil, errors.New("invalid service spec")
				}
			}
		}
		if n > 1 || (create && n != 1) {
			return nil, errors.New("service requires exactly one spec")
		}
		for _, entry := range []struct {
			spec  *ServiceSpec
			types []string
		}{{v.AgentSpec, []string{"NO_SPEC", "A2A_AGENT_CARD"}}, {v.McpServerSpec, []string{"NO_SPEC", "TOOL_SPEC"}}, {v.EndpointSpec, []string{"NO_SPEC"}}} {
			if entry.spec != nil {
				if !slices.Contains(entry.types, entry.spec.Type) {
					return nil, errors.New("unsupported service spec type")
				}
				content := bytes.TrimSpace(entry.spec.Content)
				if entry.spec.Type == "NO_SPEC" && len(content) > 0 && string(content) != "null" && string(content) != "{}" {
					return nil, errors.New("NO_SPEC service cannot include spec content")
				}
			}
		}
		if v.AgentSpec != nil && v.AgentSpec.Type == "A2A_AGENT_CARD" && len(v.Interfaces) > 0 {
			return nil, errors.New("A2A Agent Card supplies interfaces; separate interfaces must be empty")
		}
		return v, nil
	}
	var v Binding
	if e := strict(raw, &v); e != nil {
		return nil, e
	}
	if v.Name != "" && v.Name != name {
		return nil, errors.New("binding body/path mismatch")
	}
	v.Name = name
	if len(v.DisplayName) > 63 || len(v.Description) > 2048 {
		return nil, errors.New("binding metadata exceeds limits")
	}
	if create && v.Target == nil {
		return nil, errors.New("binding target is required")
	}
	for _, i := range []*Identifier{v.Source, v.Target} {
		if i != nil && (!urn.MatchString(i.Identifier) || !slices.Contains(d.cfg.AllowedIdentifiers, i.Identifier)) {
			return nil, errors.New("binding identifier is not explicitly allowed")
		}
	}
	if v.Source != nil && !strings.HasPrefix(v.Source.Identifier, "urn:agent:") {
		return nil, errors.New("binding source must be an agent")
	}
	if a := v.AuthProviderBinding; a != nil {
		if _, e := d.cfg.resource(a.AuthProvider, "authProviders"); e != nil {
			return nil, e
		}
		if a.ContinueURI != "" && !httpsURI(a.ContinueURI) {
			return nil, errors.New("binding continue URI requires HTTPS")
		}
	}
	return v, nil
}
func httpsURI(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}
