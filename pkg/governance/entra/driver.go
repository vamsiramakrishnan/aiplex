// Package entra implements Microsoft Entra Agent ID governance through the
// explicitly selected Microsoft Graph beta contract. Token acquisition is a
// separate, privileged SDK-sidecar broker; governance never returns credentials.
package entra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

type Blueprint struct {
	ObjectID string `json:"object_id"`
	AppID    string `json:"app_id"`
}
type Config struct {
	TenantID               string      `json:"tenant_id"`
	APIVersion             string      `json:"api_version"`
	AllowedBlueprints      []Blueprint `json:"allowed_blueprints"`
	AllowBlueprintCreation bool        `json:"allow_blueprint_creation,omitempty"`
}
type Sponsor struct {
	ObjectID string `json:"object_id"`
	Type     string `json:"type"`
}

// Selector is a host-authorized operation payload, not a raw Graph patch.
// Directory object IDs, application/client IDs, sponsor IDs and user IDs are
// distinct. BlueprintObjectID is required for each child-agent operation.
type Selector struct {
	TenantID          string    `json:"tenant_id"`
	BlueprintObjectID string    `json:"blueprint_object_id,omitempty"`
	DisplayName       string    `json:"display_name,omitempty"`
	Description       *string   `json:"description,omitempty"`
	Sponsors          []Sponsor `json:"sponsors,omitempty"`
	Sponsor           *Sponsor  `json:"sponsor,omitempty"`
}
type Driver struct {
	config     Config
	graph      *nativeapi.Client
	blueprints map[string]Blueprint
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validID(s string) bool { return uuid.MatchString(s) }
func New(cfg Config, graph *nativeapi.Client) (*Driver, error) {
	if graph == nil || !validID(cfg.TenantID) || cfg.APIVersion != "beta" {
		return nil, errors.New("Entra requires a pinned client, tenant GUID and explicit beta API version")
	}
	d := &Driver{config: cfg, graph: graph, blueprints: map[string]Blueprint{}}
	for _, b := range cfg.AllowedBlueprints {
		if !validID(b.ObjectID) || !validID(b.AppID) {
			return nil, errors.New("blueprint object ID and application ID must be separate GUIDs")
		}
		b.ObjectID = strings.ToLower(b.ObjectID)
		b.AppID = strings.ToLower(b.AppID)
		if _, ok := d.blueprints[b.ObjectID]; ok {
			return nil, errors.New("duplicate blueprint binding")
		}
		d.blueprints[b.ObjectID] = b
	}
	if len(d.blueprints) == 0 && !cfg.AllowBlueprintCreation {
		return nil, errors.New("configure an allowed blueprint or explicit blueprint creation")
	}
	d.config.AllowedBlueprints = append([]Blueprint(nil), cfg.AllowedBlueprints...)
	return d, nil
}

var operations = []nativeapi.OperationInfo{
	{Name: "blueprint.create", Mutating: true, Description: "Create a sponsored agent identity blueprint; operator opt-in required"},
	{Name: "blueprint.get", Description: "Inspect an approved agent identity blueprint"},
	{Name: "blueprint.update", Mutating: true, Description: "Update blueprint display name or description"},
	{Name: "blueprint.delete", Mutating: true, Description: "Delete an approved blueprint"},
	{Name: "blueprint.sponsors.list", Description: "Inspect blueprint sponsors"},
	{Name: "blueprint.sponsors.add", Mutating: true, Description: "Add a user or group sponsor"},
	{Name: "blueprint.sponsors.remove", Mutating: true, Description: "Remove a blueprint sponsor reference"},
	{Name: "agent.create", Mutating: true, Description: "Create a sponsored child agent identity"},
	{Name: "agent.list", Description: "Inspect child agents under an approved blueprint"},
	{Name: "agent.get", Description: "Inspect one approved blueprint's child identity"},
	{Name: "agent.update", Mutating: true, Description: "Update child agent display name"},
	{Name: "agent.enable", Mutating: true, Description: "Enable the native agent identity account"},
	{Name: "agent.disable", Mutating: true, Description: "Disable the native agent identity account"},
	{Name: "agent.delete", Mutating: true, Description: "Delete a child agent identity"},
	{Name: "agent.sponsors.list", Description: "Inspect child agent sponsors"},
	{Name: "agent.sponsors.add", Mutating: true, Description: "Add a user or group sponsor to a child identity"},
	{Name: "agent.sponsors.remove", Mutating: true, Description: "Remove a child identity sponsor reference"},
	{Name: "agent.appRoles.list", Description: "Inspect assigned application permissions"},
	{Name: "agent.delegatedGrants.list", Description: "Inspect delegated consent grants separately from app roles"},
}

func (d *Driver) Operations() []nativeapi.OperationInfo {
	out := append([]nativeapi.OperationInfo(nil), operations...)
	if !d.config.AllowBlueprintCreation {
		for i, o := range out {
			if o.Name == "blueprint.create" {
				out = append(out[:i], out[i+1:]...)
				break
			}
		}
	}
	return out
}
func decode(raw json.RawMessage, target any) error {
	if len(raw) == 0 || len(raw) > nativeapi.MaxBodyBytes {
		return errors.New("bounded typed Entra payload required")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(target) != nil {
		return errors.New("invalid Entra selector")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing Entra payload")
	}
	return nil
}

// ValidateOperation performs local policy-binding validation without RPCs.
// Execute still verifies authoritative Graph parentage before any child action.
func (d *Driver) ValidateOperation(op nativeapi.Operation) error {
	if d == nil {
		return errors.New("Entra driver is not configured")
	}
	known := false
	for _, info := range d.Operations() {
		if info.Name == op.Name {
			known = true
			break
		}
	}
	if !known {
		return errors.New("unsupported Entra operation")
	}
	if op.ETag != "" {
		return errors.New("this Graph Agent ID contract does not advertise conditional ETag writes")
	}
	var in Selector
	if e := decode(op.Payload, &in); e != nil {
		return e
	}
	if !strings.EqualFold(in.TenantID, d.config.TenantID) {
		return errors.New("Entra tenant mismatch")
	}
	if op.Name == "blueprint.create" {
		if !strings.EqualFold(op.Resource, d.config.TenantID) || in.BlueprintObjectID != "" || in.Description != nil || in.Sponsor != nil {
			return errors.New("invalid blueprint creation selector")
		}
		_, e := d.createBody(in, nil)
		return e
	}
	if !validID(op.Resource) {
		return errors.New("immutable Entra object GUID required")
	}
	isBlueprint := strings.HasPrefix(op.Name, "blueprint.") || op.Name == "agent.create" || op.Name == "agent.list"
	id := in.BlueprintObjectID
	if isBlueprint {
		id = op.Resource
	}
	b, ok := d.blueprints[strings.ToLower(id)]
	if !ok || (in.BlueprintObjectID != "" && !strings.EqualFold(in.BlueprintObjectID, b.ObjectID)) {
		return errors.New("blueprint is outside the configured allowlist")
	}
	switch op.Name {
	case "agent.create":
		if in.Description != nil || in.Sponsor != nil {
			return errors.New("unsupported agent creation fields")
		}
		_, e := d.createBody(in, &b)
		return e
	case "blueprint.update", "agent.update":
		if len(in.Sponsors) > 0 || in.Sponsor != nil {
			return errors.New("sponsors require relationship operations")
		}
		_, e := updateBody(in, op.Name == "blueprint.update")
		return e
	}
	if in.DisplayName != "" || in.Description != nil || len(in.Sponsors) > 0 {
		return errors.New("unexpected Entra operation fields")
	}
	if strings.HasSuffix(op.Name, ".sponsors.add") {
		if in.Sponsor == nil {
			return errors.New("sponsor required")
		}
		_, e := d.sponsorReference(*in.Sponsor)
		return e
	}
	if strings.HasSuffix(op.Name, ".sponsors.remove") {
		if in.Sponsor == nil || !validID(in.Sponsor.ObjectID) || (in.Sponsor.Type != "" && in.Sponsor.Type != "user" && in.Sponsor.Type != "group") {
			return errors.New("sponsor object GUID required")
		}
		return nil
	}
	if in.Sponsor != nil {
		return errors.New("unexpected sponsor selector")
	}
	return nil
}

func (d *Driver) Execute(ctx context.Context, op nativeapi.Operation) (nativeapi.Result, error) {
	if e := d.ValidateOperation(op); e != nil {
		return nativeapi.Result{}, e
	}
	var in Selector
	if err := decode(op.Payload, &in); err != nil {
		return nativeapi.Result{}, err
	}
	if op.Name == "blueprint.create" {
		body, err := d.createBody(in, nil)
		if err != nil {
			return nativeapi.Result{}, err
		}
		result, err := d.graph.Do(ctx, "POST", "beta/applications/microsoft.graph.agentIdentityBlueprint", nil, body)
		return safeResult(result, err)
	}
	isBlueprint := strings.HasPrefix(op.Name, "blueprint.") || op.Name == "agent.create" || op.Name == "agent.list"
	blueprintID := in.BlueprintObjectID
	if isBlueprint {
		blueprintID = op.Resource
	}
	b := d.blueprints[strings.ToLower(blueprintID)]
	blueprintPath := "beta/applications/" + b.ObjectID + "/microsoft.graph.agentIdentityBlueprint"
	if isBlueprint {
		// Verify the application/object binding instead of confusing appId with id.
		result, err := d.graph.Do(ctx, "GET", blueprintPath, url.Values{"$select": {"id,appId,displayName,description,signInAudience,tags,requiredResourceAccess"}}, nil)
		if err != nil {
			return nativeapi.Result{}, err
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(unwrapObject(result.Body), &object) != nil || !sameField(object, "id", b.ObjectID) || !sameField(object, "appId", b.AppID) {
			return nativeapi.Result{}, errors.New("Graph blueprint object/application binding mismatch")
		}
		if op.Name == "blueprint.get" {
			return safeResult(result, nil)
		}
		switch op.Name {
		case "blueprint.update":
			body, err := updateBody(in, true)
			if err != nil {
				return nativeapi.Result{}, err
			}
			r, e := d.graph.Do(ctx, "PATCH", blueprintPath, nil, body)
			return safeResult(r, e)
		case "blueprint.delete":
			r, e := d.graph.Do(ctx, "DELETE", blueprintPath, nil, nil)
			return safeResult(r, e)
		case "agent.create":
			body, err := d.createBody(in, &b)
			if err != nil {
				return nativeapi.Result{}, err
			}
			r, e := d.graph.Do(ctx, "POST", "beta/servicePrincipals/microsoft.graph.agentIdentity", nil, body)
			return safeResult(r, e)
		case "agent.list":
			r, e := d.graph.Do(ctx, "GET", "beta/servicePrincipals/microsoft.graph.agentIdentity", url.Values{"$filter": {"agentIdentityBlueprintId eq '" + b.AppID + "'"}, "$select": {"id,appId,displayName,agentIdentityBlueprintId,accountEnabled,servicePrincipalType"}}, nil)
			if e != nil {
				return nativeapi.Result{}, e
			}
			var list struct {
				Value []map[string]json.RawMessage `json:"value"`
			}
			if json.Unmarshal(r.Body, &list) != nil {
				return nativeapi.Result{}, errors.New("invalid Graph agent collection")
			}
			for _, v := range list.Value {
				if !sameField(v, "agentIdentityBlueprintId", b.AppID) {
					return nativeapi.Result{}, errors.New("Graph returned a child from another blueprint")
				}
			}
			return safeResult(r, nil)
		default:
			return d.sponsors(ctx, op.Name, blueprintPath, in)
		}
	}
	agentPath := "beta/servicePrincipals/" + strings.ToLower(op.Resource) + "/microsoft.graph.agentIdentity"
	result, err := d.graph.Do(ctx, "GET", agentPath, url.Values{"$select": {"id,appId,displayName,agentIdentityBlueprintId,accountEnabled,servicePrincipalType"}}, nil)
	if err != nil {
		return nativeapi.Result{}, err
	}
	var child map[string]json.RawMessage
	if json.Unmarshal(unwrapObject(result.Body), &child) != nil || !sameField(child, "id", op.Resource) || !sameField(child, "agentIdentityBlueprintId", b.AppID) {
		return nativeapi.Result{}, errors.New("agent does not belong to the approved blueprint")
	}
	switch op.Name {
	case "agent.get":
		return safeResult(result, nil)
	case "agent.update":
		body, e := updateBody(in, false)
		if e != nil {
			return nativeapi.Result{}, e
		}
		r, e := d.graph.Do(ctx, "PATCH", agentPath, nil, body)
		return safeResult(r, e)
	case "agent.enable", "agent.disable":
		r, e := d.graph.Do(ctx, "PATCH", agentPath, nil, map[string]bool{"accountEnabled": op.Name == "agent.enable"})
		return safeResult(r, e)
	case "agent.delete":
		r, e := d.graph.Do(ctx, "DELETE", agentPath, nil, nil)
		return safeResult(r, e)
	case "agent.appRoles.list":
		r, e := d.graph.Do(ctx, "GET", "beta/servicePrincipals/"+strings.ToLower(op.Resource)+"/appRoleAssignments", nil, nil)
		return safeResult(r, e)
	case "agent.delegatedGrants.list":
		r, e := d.graph.Do(ctx, "GET", "beta/servicePrincipals/"+strings.ToLower(op.Resource)+"/oauth2PermissionGrants", nil, nil)
		return safeResult(r, e)
	default:
		return d.sponsors(ctx, op.Name, agentPath, in)
	}
}
func sameField(v map[string]json.RawMessage, key, want string) bool {
	var actual string
	return json.Unmarshal(v[key], &actual) == nil && strings.EqualFold(actual, want)
}
func unwrapObject(raw json.RawMessage) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		if v := object["value"]; len(v) > 0 && v[0] == '{' {
			return v
		}
	}
	return raw
}
func validateDisplayName(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 256 && !strings.ContainsAny(s, "\x00\r\n")
}
func updateBody(in Selector, blueprint bool) (map[string]any, error) {
	body := map[string]any{}
	if in.DisplayName != "" {
		if !validateDisplayName(in.DisplayName) {
			return nil, errors.New("invalid display name")
		}
		body["displayName"] = in.DisplayName
	}
	if in.Description != nil {
		if !blueprint || len(*in.Description) > 1024 {
			return nil, errors.New("description is supported only on blueprints")
		}
		body["description"] = *in.Description
	}
	if len(body) == 0 {
		return nil, errors.New("no supported update fields")
	}
	return body, nil
}
func (d *Driver) sponsorReference(s Sponsor) (string, error) {
	if !validID(s.ObjectID) || (s.Type != "user" && s.Type != "group") {
		return "", errors.New("sponsor must name an immutable user or group object")
	}
	kind := "users"
	if s.Type == "group" {
		kind = "groups"
	}
	return "https://graph.microsoft.com/beta/" + kind + "/" + strings.ToLower(s.ObjectID), nil
}
func (d *Driver) createBody(in Selector, b *Blueprint) (map[string]any, error) {
	if !validateDisplayName(in.DisplayName) || len(in.Sponsors) == 0 || len(in.Sponsors) > 100 {
		return nil, errors.New("creation requires display name and one to 100 sponsors")
	}
	refs := []string{}
	seen := map[string]bool{}
	groups := 0
	for _, s := range in.Sponsors {
		ref, e := d.sponsorReference(s)
		if e != nil {
			return nil, e
		}
		if seen[ref] {
			return nil, errors.New("duplicate sponsor")
		}
		seen[ref] = true
		if s.Type == "group" {
			groups++
		}
		refs = append(refs, ref)
	}
	if groups > 5 {
		return nil, errors.New("at most five group sponsors are supported")
	}
	body := map[string]any{"displayName": in.DisplayName, "sponsors@odata.bind": refs}
	if b != nil {
		body["agentIdentityBlueprintId"] = b.AppID
	}
	return body, nil
}
func (d *Driver) sponsors(ctx context.Context, name, path string, in Selector) (nativeapi.Result, error) {
	switch {
	case strings.HasSuffix(name, ".list"):
		r, e := d.graph.Do(ctx, "GET", path+"/sponsors", url.Values{"$select": {"id,displayName"}}, nil)
		return safeResult(r, e)
	case strings.HasSuffix(name, ".add"):
		if in.Sponsor == nil {
			return nativeapi.Result{}, errors.New("sponsor required")
		}
		_, e := d.sponsorReference(*in.Sponsor)
		if e != nil {
			return nativeapi.Result{}, e
		}
		r, e := d.graph.Do(ctx, "POST", path+"/sponsors/$ref", nil, map[string]string{"@odata.id": "https://graph.microsoft.com/beta/directoryObjects/" + strings.ToLower(in.Sponsor.ObjectID)})
		return safeResult(r, e)
	case strings.HasSuffix(name, ".remove"):
		if in.Sponsor == nil || !validID(in.Sponsor.ObjectID) {
			return nativeapi.Result{}, errors.New("sponsor object GUID required")
		}
		r, e := d.graph.Do(ctx, "DELETE", path+"/sponsors/"+strings.ToLower(in.Sponsor.ObjectID)+"/$ref", nil, nil)
		return safeResult(r, e)
	}
	return nativeapi.Result{}, errors.New("unsupported sponsor operation")
}

// Graph errors are sanitized by nativeapi. Successful governance output also
// strips credential-bearing properties even if an upstream ignores $select.
func safeResult(r nativeapi.Result, err error) (nativeapi.Result, error) {
	if err != nil {
		return nativeapi.Result{}, err
	}
	var value any
	if json.Unmarshal(r.Body, &value) != nil {
		return nativeapi.Result{}, errors.New("invalid Graph response")
	}
	r.Body, _ = json.Marshal(redact(value))
	return r, nil
}
func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			n := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(k))
			switch n {
			case "passwordcredentials", "keycredentials", "secrettext", "clientsecret", "accesstoken", "refreshtoken", "authorizationheader", "registrationaccesstoken":
				continue
			}
			out[k] = redact(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = redact(v)
		}
		return out
	default:
		return v
	}
}

// AllowedBlueprints returns deterministic operator configuration without secrets.
func (d *Driver) AllowedBlueprints() []Blueprint {
	out := make([]Blueprint, 0, len(d.blueprints))
	for _, b := range d.blueprints {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectID < out[j].ObjectID })
	return out
}
